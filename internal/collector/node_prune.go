package collector

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

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

// clusterMembers reads which nodes the cluster holds from one pass's node list
// and cluster status, or says why this pass cannot tell. Only an answer it can
// tell from may remove a node row.
func clusterMembers(listed []proxmox.NodeListEntry, status []proxmox.ClusterStatusEntry, statusErr error) (members map[string]bool, skip string) {
	if statusErr != nil {
		return nil, "the cluster status could not be read"
	}
	if len(listed) == 0 {
		return nil, "the node list is empty"
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
		return nil, "the cluster status names no cluster"
	}
	if cluster.Quorate != 1 {
		return nil, "the cluster is not quorate"
	}
	members = make(map[string]bool, len(listed))
	for _, n := range listed {
		if n.Node == "" {
			return nil, "the node list has an entry with no name"
		}
		members[n.Node] = true
	}
	if !maps.Equal(members, inStatus) || cluster.Nodes != len(members) {
		return nil, "the node list and the cluster status disagree"
	}
	return members, ""
}

// planNodeRemovals decides which node rows go. A row goes when two readings in
// a row, both clusterMembers could tell from, lack its node: prevAbsent is what
// the previous reading lacked, absent what this one lacks. Nothing goes, and
// the count starts over, when this reading cannot tell, or when it lacks every
// row on record — a cluster that holds none of its nodes is a wrong endpoint
// or a bad answer, not a membership change.
func planNodeRemovals(rows []db.Node, listed []proxmox.NodeListEntry, status []proxmox.ClusterStatusEntry,
	statusErr error, prevAbsent map[string]bool) (remove []db.Node, absent map[string]bool, skip string) {
	members, skip := clusterMembers(listed, status, statusErr)
	if skip != "" {
		return nil, nil, skip
	}
	absent = make(map[string]bool)
	for _, r := range rows {
		if !members[r.Name] {
			absent[r.Name] = true
		}
	}
	if len(absent) > 0 && len(absent) == len(rows) {
		return nil, nil, "the cluster lists none of the nodes on record"
	}
	for _, r := range rows {
		if absent[r.Name] && prevAbsent[r.Name] {
			remove = append(remove, r)
		}
	}
	return remove, absent, ""
}

// pruneRemovedNodes removes the rows of the nodes the cluster no longer holds
// (planNodeRemovals) and reports whether it removed any. Each removal is logged,
// audited as a system action and published; the caller publishes the inventory
// change. The node's guest rows go with it (vms ON DELETE CASCADE): Proxmox
// lists them on a node that is not a member, which nothing can reach, and they
// come back by the normal path once their configs are on a member. History,
// audit and task rows keep theirs (migrations/000148).
func (s *Syncer) pruneRemovedNodes(ctx context.Context, cluster db.Cluster, listed []proxmox.NodeListEntry,
	status []proxmox.ClusterStatusEntry, statusErr error) bool {
	prev, _ := s.nodeAbsent.Load(cluster.ID)
	prevAbsent, _ := prev.(map[string]bool)

	rows, err := s.queries.ListNodesByCluster(ctx, cluster.ID)
	if err != nil {
		s.nodeAbsent.Delete(cluster.ID)
		s.logger.Warn("node membership: could not read the cluster's node rows; removing none",
			"cluster_id", cluster.ID, "error", err)
		return false
	}
	remove, absent, skip := planNodeRemovals(rows, listed, status, statusErr, prevAbsent)
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
	for name := range absent {
		if !prevAbsent[name] {
			s.logger.Info("node membership: the cluster does not list this node; its row is removed if the next pass agrees",
				"cluster_id", cluster.ID, "node", name)
		}
	}
	if len(remove) == 0 {
		s.nodeAbsent.Store(cluster.ID, absent)
		return false
	}

	ids := make([]uuid.UUID, 0, len(remove))
	for _, r := range remove {
		ids = append(ids, r.ID)
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
		s.auditNodeRemoved(ctx, cluster, d)
	}
	s.nodeAbsent.Store(cluster.ID, absent)
	return len(deleted) > 0
}

// auditNodeRemoved records a node row's removal as a system action, written as
// reportEndpointFailover writes its entry.
func (s *Syncer) auditNodeRemoved(ctx context.Context, cluster db.Cluster, node db.DeleteClusterNodesRow) {
	details, _ := json.Marshal(map[string]string{
		"cluster_name": cluster.Name,
		"node_id":      node.ID.String(),
	})

	_ = s.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: cluster.ID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: "node",
		ResourceID:   node.Name,
		Action:       "node_removed",
		Details:      details,
	})

	if s.eventPub != nil {
		s.eventPub.ClusterEvent(ctx, cluster.ID.String(), events.KindAuditEntry, "node", node.Name, "node_removed")
	}
}
