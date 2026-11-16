package collector

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/audit"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// Removing the row of a node the cluster no longer holds.
//
// What Proxmox lists, read in its source: GET /nodes (PVE::API2::Nodes index)
// and the node entries of GET /cluster/status (PVE::API2::Cluster get_status)
// are both the keys of pmxcfs's clinfo nodelist, which is corosync.conf's
// nodelist. An offline member stays in it, listed with status "offline" or
// "unknown". `pvecm delnode` (PVE::API2::ClusterConfig delnode) deletes the
// node from corosync.conf and nothing else, so the node leaves both listings.
// Its guests' configs stay under /etc/pve/nodes/<node>/, and pmxcfs's vmlist
// (memdb_recreate_vmlist) reads every nodes/ directory, member or not, so
// /cluster/resources goes on listing those guests on the removed node, status
// "unknown", until someone moves their configs to a member.
//
// One trap: PVE::Cluster::get_nodelist answers just the local node when the
// nodelist is missing or lacks it — a standalone node, or one whose pmxcfs has
// not loaded the cluster configuration. /cluster/status then has no "cluster"
// entry, which is how such a read is told apart from a cluster of one.

// clusterMembers reads which nodes the cluster holds, and the cluster's own
// (corosync) name, from one pass's node list and cluster status, or says why
// this pass cannot tell. Only an answer it can tell from may remove a node row.
func clusterMembers(listed []proxmox.NodeListEntry, status []proxmox.ClusterStatusEntry, statusErr error) (members map[string]bool, name, skip string) {
	if statusErr != nil {
		return nil, "", "the cluster status could not be read"
	}
	if len(listed) == 0 {
		return nil, "", "the node list is empty"
	}
	var cluster *proxmox.ClusterStatusEntry
	inStatus := make(map[string]bool, len(status))
	for i := range status {
		switch status[i].Type {
		case "cluster":
			cluster = &status[i]
		case "node":
			inStatus[status[i].Name] = true
		}
	}
	if cluster == nil {
		return nil, "", "the cluster status names no cluster"
	}
	if cluster.Quorate != 1 {
		return nil, "", "the cluster is not quorate"
	}
	members = make(map[string]bool, len(listed))
	for _, n := range listed {
		if n.Node == "" {
			return nil, "", "the node list has an entry with no name"
		}
		members[n.Node] = true
	}
	if !maps.Equal(members, inStatus) || cluster.Nodes != len(members) {
		return nil, "", "the node list and the cluster status disagree"
	}
	return members, cluster.Name, ""
}

// skipOtherCluster is planNodeRemovals' skip when the cluster status names a
// cluster other than the one this cluster row's nodes were read from.
const skipOtherCluster = "the cluster status names another cluster"

// membershipPlan is planNodeRemovals' decision.
type membershipPlan struct {
	remove  []db.Node       // the rows to delete
	absent  map[string]bool // the known rows this reading lacks, for the next pass
	cluster string          // the cluster this reading named
	skip    string          // why this reading decides nothing; "" when it decides
}

// planNodeRemovals decides which node rows go. known is the cluster's rows as
// they stood before this pass wrote any; prevAbsent is what the previous
// reading lacked; expected is the cluster name the rows were read from ("" when
// none is on record yet). A row goes when all of these hold:
//   - this reading and the previous one could both tell (clusterMembers), and
//     both lack its node;
//   - this reading names the expected cluster: an API URL moved to another
//     cluster must not empty this one, even where the two share a node name;
//   - some row the cluster still lists was on record by the time the row's
//     node was last seen. A cluster that lists only rows that came after (or
//     none of the known rows at all) is another cluster, not a smaller one.
//
// Otherwise nothing goes and the count starts over.
func planNodeRemovals(known []db.Node, listed []proxmox.NodeListEntry, status []proxmox.ClusterStatusEntry,
	statusErr error, prevAbsent map[string]bool, expected string) membershipPlan {
	members, name, skip := clusterMembers(listed, status, statusErr)
	if skip != "" {
		return membershipPlan{skip: skip}
	}
	if expected != "" && name != expected {
		return membershipPlan{cluster: name, skip: skipOtherCluster}
	}
	plan := membershipPlan{absent: make(map[string]bool), cluster: name}
	for _, r := range known {
		if !members[r.Name] {
			plan.absent[r.Name] = true
		}
	}
	for _, r := range known {
		if plan.absent[r.Name] && prevAbsent[r.Name] && establishedMemberRemains(known, members, r) {
			plan.remove = append(plan.remove, r)
		}
	}
	return plan
}

// establishedMemberRemains reports whether a row the cluster still lists was
// already on record when gone's node was last seen.
func establishedMemberRemains(known []db.Node, members map[string]bool, gone db.Node) bool {
	for _, k := range known {
		if members[k.Name] && !k.CreatedAt.After(gone.LastSeenAt) {
			return true
		}
	}
	return false
}

// pruneRemovedNodes removes the rows of the nodes the cluster no longer holds
// (planNodeRemovals) and reports whether it removed any. Each removal is logged,
// audited as a system action and published; the caller publishes the inventory
// change. The node's guest rows go with it (vms ON DELETE CASCADE): Proxmox
// lists them on a node that is not a member, which nothing can reach, and they
// come back by the normal path once their configs are on a member. History,
// audit and task rows keep theirs (migrations/000148); its alert rules and
// maintenance windows are detached first and wait for the name to rejoin
// (reattachNodeRows).
//
// The cluster name the rows were read from is clusters.corosync_name
// (migrations/000151), so a restart does not forget it. A reading that names
// another cluster removes nothing and is recorded in corosync_name_reported,
// which the clusters API reports as an issue, until the cluster names the
// recorded one again or an operator accepts the new name.
func (s *Syncer) pruneRemovedNodes(ctx context.Context, cluster db.Cluster, known []db.Node, knownErr error,
	listed []proxmox.NodeListEntry, status []proxmox.ClusterStatusEntry, statusErr error) bool {
	prev, _ := s.nodeAbsent.Load(cluster.ID)
	prevAbsent, _ := prev.(map[string]bool)

	if knownErr != nil {
		s.nodeAbsent.Delete(cluster.ID)
		s.logger.Warn("node membership: could not read the cluster's node rows; removing none",
			"cluster_id", cluster.ID, "error", knownErr)
		return false
	}
	stored, err := s.queries.GetClusterCorosyncName(ctx, cluster.ID)
	if err != nil {
		s.nodeAbsent.Delete(cluster.ID)
		s.logger.Warn("node membership: could not read the cluster name its node rows were read from; removing none",
			"cluster_id", cluster.ID, "error", err)
		return false
	}
	plan := planNodeRemovals(known, listed, status, statusErr, prevAbsent, stored.CorosyncName)
	remove, absent, skip := plan.remove, plan.absent, plan.skip
	if skip == skipOtherCluster {
		s.nodeAbsent.Delete(cluster.ID)
		s.reportOtherCluster(ctx, cluster, stored.CorosyncName, plan.cluster)
		return false
	}
	if skip != "" {
		s.nodeAbsent.Delete(cluster.ID)
		if len(prevAbsent) > 0 {
			s.logger.Info("node membership: this pass cannot tell which nodes the cluster holds; keeping every node row",
				"cluster_id", cluster.ID, "reason", skip, "not_listed_last_pass", slices.Sorted(maps.Keys(prevAbsent)))
		} else {
			s.logger.Debug("node membership: this pass cannot tell which nodes the cluster holds",
				"cluster_id", cluster.ID, "reason", skip)
		}
		return false
	}
	if stored.CorosyncName == "" || stored.CorosyncNameReported != "" {
		// First reading on record, or the recorded cluster answering again.
		if _, err := s.queries.RecordClusterCorosyncName(ctx, db.RecordClusterCorosyncNameParams{
			ID: cluster.ID, Name: plan.cluster}); err != nil {
			s.logger.Warn("node membership: could not record the cluster's name; removing none this pass",
				"cluster_id", cluster.ID, "error", err)
			s.nodeAbsent.Store(cluster.ID, absent)
			return false
		}
	}
	for name := range absent {
		if !prevAbsent[name] {
			s.logger.Info("node membership: the cluster does not list this node; its row is removed if the next pass agrees",
				"cluster_id", cluster.ID, "node", name)
		}
	}

	// Detach each node's rules and windows before its row goes. A node whose
	// detach failed is kept for a later pass; a detach whose delete then fails
	// is undone by reattachNodeRows, since the row is still there.
	ids := make([]uuid.UUID, 0, len(remove))
	disabled := make(map[uuid.UUID]int64, len(remove))
	for _, r := range remove {
		node := pgtype.UUID{Bytes: r.ID, Valid: true}
		n, err := s.queries.DetachNodeAlertRules(ctx, db.DetachNodeAlertRulesParams{NodeID: node, NodeName: r.Name})
		if err == nil {
			_, err = s.queries.DetachNodeMaintenanceWindows(ctx, db.DetachNodeMaintenanceWindowsParams{NodeID: node, NodeName: r.Name})
		}
		if err != nil {
			s.logger.Warn("node membership: could not set aside the alert rules and maintenance windows of a node the cluster no longer lists; keeping its row",
				"cluster_id", cluster.ID, "node", r.Name, "error", err)
			continue
		}
		disabled[r.ID] = n
		ids = append(ids, r.ID)
	}
	if len(ids) == 0 {
		s.nodeAbsent.Store(cluster.ID, absent)
		return false
	}

	// Read before the delete, which nulls alert_history.node_id.
	active, alertErr := s.queries.ListActiveAlertsForNodes(ctx, ids)
	if alertErr != nil {
		s.logger.Warn("node membership: could not read the active alerts of the nodes being removed; they stay open",
			"cluster_id", cluster.ID, "error", alertErr)
	}
	deleted, err := s.queries.DeleteClusterNodes(ctx, db.DeleteClusterNodesParams{ClusterID: cluster.ID, Ids: ids})
	if err != nil {
		s.nodeAbsent.Store(cluster.ID, absent)
		s.logger.Warn("node membership: failed to remove the rows of nodes the cluster no longer lists",
			"cluster_id", cluster.ID, "error", err)
		return false
	}
	for _, d := range deleted {
		delete(absent, d.Name)
		s.logger.Warn("node membership: removed the row of a node the cluster no longer lists, with its guest, storage and hardware rows",
			"cluster_id", cluster.ID, "node", d.Name, "node_id", d.ID)
		s.auditNodeRemoved(ctx, cluster, d, disabled[d.ID], s.resolveRemovedNodeAlerts(ctx, cluster, d, active))
	}
	s.nodeAbsent.Store(cluster.ID, absent)
	return len(deleted) > 0
}

// reportOtherCluster records that the cluster's API URL answered with another
// corosync cluster, and audits it as a system action the first pass it does
// (ReportClusterCorosyncNameMismatch writes a row only on a new name).
func (s *Syncer) reportOtherCluster(ctx context.Context, cluster db.Cluster, expected, answering string) {
	n, err := s.queries.ReportClusterCorosyncNameMismatch(ctx, db.ReportClusterCorosyncNameMismatchParams{
		ID: cluster.ID, Reported: answering})
	if err != nil {
		s.logger.Warn("node membership: could not record that the API URL reaches another cluster",
			"cluster_id", cluster.ID, "error", err)
		return
	}
	if n == 0 {
		return
	}
	s.logger.Warn("node membership: the API URL now reaches another cluster; removing no node rows",
		"cluster_id", cluster.ID, "expected_cluster", expected, "answering_cluster", answering)
	details, _ := json.Marshal(map[string]any{
		"cluster_name":      cluster.Name,
		"expected_cluster":  expected,
		"answering_cluster": answering,
	})
	_ = audit.Write(ctx, s.queries, s.eventPub, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: cluster.ID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: "cluster",
		ResourceID:   cluster.ID.String(),
		Action:       "cluster_name_mismatch",
		Details:      details,
	})
}

// reattachNodeRows points the alert rules and maintenance windows a removed
// node left behind (DetachNodeAlertRules) at the cluster's node row of the same
// name, once a node of that name is back: a rejoining node gets a new id.
func (s *Syncer) reattachNodeRows(ctx context.Context, clusterID uuid.UUID) {
	rules, err := s.queries.ReattachNodeAlertRules(ctx, clusterID)
	if err != nil {
		s.logger.Warn("node membership: could not reattach the alert rules of a node that rejoined",
			"cluster_id", clusterID, "error", err)
	}
	windows, werr := s.queries.ReattachNodeMaintenanceWindows(ctx, clusterID)
	if werr != nil {
		s.logger.Warn("node membership: could not reattach the maintenance windows of a node that rejoined",
			"cluster_id", clusterID, "error", werr)
	}
	if rules > 0 || windows > 0 {
		s.logger.Info("node membership: reattached a rejoined node's alert rules and maintenance windows",
			"cluster_id", clusterID, "alert_rules", rules, "maintenance_windows", windows)
	}
}

// resolveRemovedNodeAlerts resolves the active alerts on a removed node, as the
// alert engine's automatic resolve does: AutoResolveAlert, then the "resolved"
// notification for one that fired (NotifyResolved, given the row as it stood),
// and the alert event. Nothing will ever evaluate them again. Returns how many
// it resolved.
func (s *Syncer) resolveRemovedNodeAlerts(ctx context.Context, cluster db.Cluster, node db.DeleteClusterNodesRow, active []db.AlertHistory) int {
	resolved := 0
	for _, a := range active {
		if !a.NodeID.Valid || a.NodeID.Bytes != node.ID {
			continue
		}
		n, err := s.queries.AutoResolveAlert(ctx, a.ID)
		if err != nil {
			s.logger.Warn("node membership: could not resolve an alert on the removed node",
				"cluster_id", cluster.ID, "node", node.Name, "alert_id", a.ID, "error", err)
			continue
		}
		if n == 0 {
			continue // resolved meanwhile, which sent its own "resolved"
		}
		resolved++
		if s.alertNotifier != nil {
			s.alertNotifier.NotifyResolved(ctx, a)
		}
		if s.eventPub != nil {
			s.eventPub.ClusterEvent(ctx, cluster.ID.String(), events.KindAlertStateChange, "alert", a.ID.String(), "resolved")
		}
	}
	return resolved
}

// auditNodeRemoved records a node row's removal as a system action, written as
// reportEndpointFailover writes its entry, with how many of the node's alert
// rules were turned off and alerts resolved.
func (s *Syncer) auditNodeRemoved(ctx context.Context, cluster db.Cluster, node db.DeleteClusterNodesRow, rulesDisabled int64, alertsResolved int) {
	details, _ := json.Marshal(map[string]any{
		"cluster_name":    cluster.Name,
		"node_id":         node.ID.String(),
		"rules_disabled":  rulesDisabled,
		"alerts_resolved": alertsResolved,
	})

	_ = audit.Write(ctx, s.queries, s.eventPub, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: cluster.ID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: "node",
		ResourceID:   node.Name,
		Action:       "node_removed",
		Details:      details,
	})
}
