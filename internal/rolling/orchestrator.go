// Package rolling implements the rolling update orchestrator for Proxmox nodes.
package rolling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/drs"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/notifications"
	"github.com/bigjakk/nexara/internal/proxmox"
	sshpkg "github.com/bigjakk/nexara/internal/ssh"
)

// validDebianPkgName matches valid Debian package names: lowercase alphanum, dots, plus, hyphens.
var validDebianPkgName = regexp.MustCompile(`^[a-z0-9][a-z0-9.+\-]{0,127}$`)

func isValidDebianPkgName(name string) bool {
	return validDebianPkgName.MatchString(name)
}

// CVEScanner is the subset of the scanner.Engine API the orchestrator
// uses to trigger a post-upgrade rescan. Kept as a small interface so the
// rolling package doesn't import scanner directly (and so tests don't
// need to construct a real engine).
type CVEScanner interface {
	ScanCluster(ctx context.Context, clusterID uuid.UUID) (uuid.UUID, error)
}

// Orchestrator drives the rolling update state machine on a scheduler tick.
type Orchestrator struct {
	queries        *db.Queries
	encryptionKey  string
	cache          *proxmox.ClientCache // nil-safe; falls back to per-call construction
	logger         *slog.Logger
	eventPub       *events.Publisher
	notifyRegistry *notifications.Registry
	cveScanner     CVEScanner // nil-safe; when set, a post-upgrade scan fires on successful completion
	// shutdownCtx is the parent for goroutines that must outlive a
	// scheduler tick (SSH upgrade, task polling, notification dispatch)
	// but should still be cancelled on graceful shutdown (SIGTERM).
	shutdownCtx context.Context
}

// SetProxmoxCache attaches the shared per-server cache. Nil-safe.
func (o *Orchestrator) SetProxmoxCache(cache *proxmox.ClientCache) {
	o.cache = cache
}

// SetCVEScanner attaches the CVE scanner used to refresh the cluster's
// security posture after a rolling update completes successfully. Nil-safe.
func (o *Orchestrator) SetCVEScanner(s CVEScanner) {
	o.cveScanner = s
}

// NewOrchestrator creates a new rolling update orchestrator. shutdownCtx
// should be the per-server context that's cancelled on SIGTERM; nil falls
// back to context.Background() for tests / partial construction.
func NewOrchestrator(shutdownCtx context.Context, queries *db.Queries, encryptionKey string, logger *slog.Logger, eventPub *events.Publisher, notifyRegistry *notifications.Registry) *Orchestrator {
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}
	return &Orchestrator{
		queries:        queries,
		encryptionKey:  encryptionKey,
		logger:         logger,
		eventPub:       eventPub,
		notifyRegistry: notifyRegistry,
		shutdownCtx:    shutdownCtx,
	}
}

// Tick is called on each scheduler interval. It advances all running jobs.
func (o *Orchestrator) Tick(ctx context.Context) {
	jobs, err := o.queries.ListRunningRollingUpdateJobs(ctx)
	if err != nil {
		o.logger.Error("failed to list running rolling update jobs", "error", err)
		return
	}

	for _, job := range jobs {
		o.processJob(ctx, job)
	}
}

func (o *Orchestrator) processJob(ctx context.Context, job db.RollingUpdateJob) {
	// Disable DRS for this cluster while the rolling update is running.
	// We do this once on the first tick and record it so we can restore later.
	if !job.DrsWasEnabled {
		o.disableDRSIfEnabled(ctx, job)
	}

	client, err := o.createClient(ctx, job.ClusterID)
	if err != nil {
		o.logger.Error("failed to create proxmox client for rolling update",
			"job_id", job.ID, "cluster_id", job.ClusterID, "error", err)
		return
	}

	// Pause Proxmox's native CRS dynamic auto-rebalancer (PVE 9.2+) for the
	// duration of the job so it can't migrate HA guests back onto a node we're
	// draining/rebooting. Runs after the client is built (it needs cluster
	// options). Mirrors the DRS-disable above; restored on completion/failure.
	if !job.NativeCrsPaused {
		o.pauseNativeCRSIfActive(ctx, client, job)
	}

	nodes, err := o.queries.ListRollingUpdateNodes(ctx, job.ID)
	if err != nil {
		o.logger.Error("failed to list rolling update nodes", "job_id", job.ID, "error", err)
		return
	}

	// Advance each active node through the state machine.
	for _, node := range nodes {
		switch node.Step {
		case "draining":
			o.advanceDraining(ctx, client, job, node)
		case "awaiting_upgrade":
			// This step requires manual confirmation — no automatic advance.
			continue
		case "upgrading":
			o.advanceUpgrading(ctx, client, job, node)
		case "rebooting":
			o.advanceRebooting(ctx, client, job, node)
		case "health_check":
			o.advanceHealthCheck(ctx, client, job, node)
		case "restoring":
			o.advanceRestoring(ctx, client, job, node)
		}
	}

	// Check if a node failed during this tick.
	for _, node := range nodes {
		if node.Step == "draining" || node.Step == "upgrading" || node.Step == "rebooting" || node.Step == "health_check" || node.Step == "restoring" {
			// Re-read to check for failure.
			updated, err := o.queries.GetRollingUpdateNode(ctx, node.ID)
			if err == nil && updated.Step == "failed" {
				o.failJob(ctx, job, fmt.Sprintf("node %s failed: %s", node.NodeName, updated.FailureReason))
				return
			}
		}
	}

	// Check if we should start a new node.
	activeCount, err := o.queries.CountActiveNodes(ctx, job.ID)
	if err != nil {
		o.logger.Error("failed to count active nodes", "job_id", job.ID, "error", err)
		return
	}

	if activeCount < int64(job.Parallelism) {
		nextNode, err := o.queries.GetNextPendingNode(ctx, job.ID)
		if err == nil {
			o.startNode(ctx, client, job, nextNode)
		}
	}

	// Check if job is done.
	counts, err := o.queries.CountCompletedNodes(ctx, job.ID)
	if err != nil {
		return
	}
	finishedCount := counts.Completed + counts.Failed + counts.Skipped
	if finishedCount == counts.Total {
		if counts.Failed > 0 {
			o.failJob(ctx, job, "one or more nodes failed")
		} else {
			if err := o.queries.CompleteRollingUpdateJob(ctx, job.ID); err != nil {
				o.logger.Error("failed to complete rolling update job", "job_id", job.ID, "error", err)
				return
			}
			o.publishEvent(ctx, job.ClusterID, job.ID, "completed")
			o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_completed", nil)
			o.sendJobNotification(ctx, job, "completed", "Rolling update completed successfully")
			o.logger.Info("rolling update job completed", "job_id", job.ID)
			o.triggerPostUpgradeCVEScan(job)
		}
		// Re-enable DRS + native CRS if we paused them at the start.
		o.restoreDRS(ctx, job)
		o.restoreNativeCRS(ctx, job)
	}
}

func (o *Orchestrator) startNode(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode) {
	o.logger.Info("starting rolling update for node", "job_id", job.ID, "node", node.NodeName)

	// Check if the node actually has pending updates. Refresh apt index first
	// to get the current state, then skip if there's nothing to update.
	upid, err := client.RefreshNodeAptIndex(ctx, node.NodeName)
	if err == nil {
		o.waitForTask(ctx, client, node.NodeName, upid)
	}
	updates, err := client.GetNodeAptUpdates(ctx, node.NodeName)
	if err == nil && len(updates) == 0 {
		o.logger.Info("node has no pending updates, skipping", "node", node.NodeName)
		_ = o.queries.SkipRollingUpdateNodeAny(ctx, db.SkipRollingUpdateNodeAnyParams{
			ID:         node.ID,
			SkipReason: "no pending updates",
		})
		o.publishEvent(ctx, job.ClusterID, job.ID, "node_skipped")
		return
	}
	// Update the packages_json with the fresh data.
	if err == nil {
		freshPkgs, _ := json.Marshal(updates)
		_ = o.queries.SetNodePackagesJSON(ctx, db.SetNodePackagesJSONParams{
			ID:           node.ID,
			PackagesJson: freshPkgs,
		})
	}

	// Snapshot running guests on this node.
	guests, err := o.snapshotGuests(ctx, client, node.NodeName)
	if err != nil {
		o.logger.Error("failed to snapshot guests", "node", node.NodeName, "error", err)
		o.failNode(ctx, job, node, fmt.Sprintf("snapshot guests: %v", err))
		return
	}

	// Build a set of SIDs for guests on this node.
	guestSIDs := make(map[string]bool, len(guests))
	for _, g := range guests {
		sid := fmt.Sprintf("%s:%d", guestTypeToSIDPrefix(g.Type), g.VMID)
		guestSIDs[sid] = true
	}

	// Disable HA rules (affinity/anti-affinity) that reference any guest on this node.
	// This prevents ha-manager from blocking migrations due to rule violations.
	// We only disable rules, NOT resource states — disabling HA resource state stops VMs.
	var disabledRules []DisabledHARule
	haRulesList, _ := client.GetHARules(ctx)
	for _, rule := range haRulesList {
		if rule.Disable == 1 {
			continue // Already disabled.
		}
		// Check if this rule references any guest being drained.
		ruleSIDs := strings.Split(rule.Resources, ",")
		affectsGuest := false
		for _, rs := range ruleSIDs {
			if guestSIDs[strings.TrimSpace(rs)] {
				affectsGuest = true
				break
			}
		}
		if !affectsGuest {
			continue
		}
		o.logger.Info("disabling HA rule before drain",
			"rule", rule.Rule, "type", rule.Type, "node", node.NodeName)
		if err := client.SetHARuleDisabled(ctx, rule.Rule, rule.Type, true); err != nil {
			o.logger.Warn("failed to disable HA rule",
				"rule", rule.Rule, "error", err)
		} else {
			disabledRules = append(disabledRules, DisabledHARule{Rule: rule.Rule, Type: rule.Type})
		}
	}

	// Store disabled rules in the DB so we can re-enable after restore.
	if len(disabledRules) > 0 {
		rulesJSON, _ := json.Marshal(disabledRules)
		_ = o.queries.SetNodeDisabledHARules(ctx, db.SetNodeDisabledHARulesParams{
			ID:              node.ID,
			DisabledHaRules: rulesJSON,
		})
	}

	guestsJSON, _ := json.Marshal(guests)
	_ = o.queries.SetNodeGuestsJSON(ctx, db.SetNodeGuestsJSONParams{
		ID:         node.ID,
		GuestsJson: guestsJSON,
	})

	if err := o.queries.SetNodeDrainStarted(ctx, node.ID); err != nil {
		o.logger.Error("failed to set node drain started", "node_id", node.ID, "error", err)
		return
	}

	o.publishEvent(ctx, job.ClusterID, job.ID, "node_draining")

	// If no running guests, skip drain and go straight to awaiting_upgrade.
	if len(guests) == 0 {
		o.logger.Info("no running guests on node, skipping drain", "node", node.NodeName)
		o.completeDrain(ctx, client, job, node)
		return
	}

	// Find target nodes for migration.
	clusterNodes, err := client.GetNodes(ctx)
	if err != nil {
		o.failNode(ctx, job, node, fmt.Sprintf("list cluster nodes: %v", err))
		return
	}

	var targets []string
	for _, cn := range clusterNodes {
		if cn.Node != node.NodeName && cn.Status == "online" {
			targets = append(targets, cn.Node)
		}
	}

	if len(targets) == 0 {
		o.failNode(ctx, job, node, "no available target nodes for migration")
		return
	}

	// Build HA constraint data for smart target selection.
	haResources, _ := client.GetHAResources(ctx)
	haResMap := make(map[string]proxmox.HAResource, len(haResources))
	for _, r := range haResources {
		haResMap[r.SID] = r
	}
	haGroups, _ := client.GetHAGroups(ctx)
	haGrpMap := make(map[string]proxmox.HAGroup, len(haGroups))
	for _, g := range haGroups {
		haGrpMap[g.Group] = g
	}
	haRules, _ := client.GetHARules(ctx)
	dbRules, _ := o.queries.ListDRSRules(ctx, job.ClusterID)
	drsRules := drs.ParseDBRules(dbRules)

	// Build workload map for constraint checking.
	nodeWorkloads := BuildNodeWorkloads(ctx, client, clusterNodes)

	// Migrate all guests using HA-aware target selection.
	// Passthrough guests (PCI/USB) cannot be live-migrated — shut them down
	// in-place and restart them on the same node after the update completes.
	for i, guest := range guests {
		// Check for cancellation between each guest operation.
		if i > 0 && o.isJobCancelled(ctx, job.ID) {
			o.logger.Info("job cancelled during drain, stopping migrations", "node", node.NodeName)
			return
		}

		// Verify the guest is still on this node (DRS or manual action may have moved it).
		if !o.isGuestOnNode(ctx, client, node.NodeName, guest) {
			o.logger.Info("guest no longer on node, skipping",
				"vmid", guest.VMID, "type", guest.Type, "node", node.NodeName)
			continue
		}

		if guest.Passthrough {
			// Gracefully shut down the passthrough guest — it stays on this node.
			o.logger.Info("shutting down passthrough guest (cannot live-migrate)",
				"vmid", guest.VMID, "type", guest.Type, "name", guest.Name, "node", node.NodeName)

			var upid string
			var shutErr error
			switch guest.Type {
			case "qemu":
				upid, shutErr = client.ShutdownVM(ctx, node.NodeName, guest.VMID)
			case "lxc":
				upid, shutErr = client.ShutdownCT(ctx, node.NodeName, guest.VMID)
			}
			if shutErr != nil {
				o.failNode(ctx, job, node, fmt.Sprintf("shutdown passthrough %s %d: %v", guest.Type, guest.VMID, shutErr))
				return
			}

			status := o.waitForTask(ctx, client, node.NodeName, upid)
			if status == "interrupted" {
				o.logger.Info("drain interrupted by shutdown during passthrough shutdown; next leader will resume",
					"node", node.NodeName, "vmid", guest.VMID)
				return
			}
			if status != "completed" {
				// Graceful shutdown failed — force stop.
				o.logger.Warn("graceful shutdown failed, force stopping passthrough guest",
					"vmid", guest.VMID, "status", status)
				switch guest.Type {
				case "qemu":
					upid, shutErr = client.StopVM(ctx, node.NodeName, guest.VMID)
				case "lxc":
					upid, shutErr = client.StopCT(ctx, node.NodeName, guest.VMID)
				}
				if shutErr != nil {
					o.failNode(ctx, job, node, fmt.Sprintf("force stop passthrough %s %d: %v", guest.Type, guest.VMID, shutErr))
					return
				}
				if forceStatus := o.waitForTask(ctx, client, node.NodeName, upid); forceStatus == "interrupted" {
					o.logger.Info("drain interrupted by shutdown during passthrough force-stop; next leader will resume",
						"node", node.NodeName, "vmid", guest.VMID)
					return
				}
			}

			o.logger.Info("passthrough guest shut down, will restart after update",
				"vmid", guest.VMID, "name", guest.Name)
			continue
		}

		gs := GuestSnapshot{VMID: guest.VMID, Name: guest.Name, Type: guest.Type, Status: guest.Status}
		target, err := SelectTarget(gs, node.NodeName, targets, haResMap, haGrpMap, haRules, drsRules, nodeWorkloads)
		if err != nil {
			o.failNode(ctx, job, node, fmt.Sprintf("no valid target for %s %d: %v", guest.Type, guest.VMID, err))
			return
		}

		params := proxmox.MigrateParams{
			Target: target,
			Online: guest.Status == "running",
		}

		var upid string
		var migrateErr error

		switch guest.Type {
		case "qemu":
			upid, migrateErr = client.MigrateVM(ctx, node.NodeName, guest.VMID, params)
		case "lxc":
			upid, migrateErr = client.MigrateCT(ctx, node.NodeName, guest.VMID, params)
		default:
			continue
		}

		if migrateErr != nil {
			o.failNode(ctx, job, node, fmt.Sprintf("migrate %s %d: %v", guest.Type, guest.VMID, migrateErr))
			return
		}

		o.logger.Info("migrating guest for drain",
			"vmid", guest.VMID, "type", guest.Type,
			"from", node.NodeName, "to", target, "upid", upid)

		// Wait for migration to complete.
		status := o.waitForTask(ctx, client, node.NodeName, upid)
		if status == "interrupted" {
			o.logger.Info("drain interrupted by shutdown while waiting for migration; next leader will resume",
				"node", node.NodeName, "vmid", guest.VMID, "upid", upid)
			return
		}
		if status != "completed" {
			o.failNode(ctx, job, node, fmt.Sprintf("migration of %s %d failed (status: %s)", guest.Type, guest.VMID, status))
			return
		}
		lockStatus := o.waitForGuestUnlocked(ctx, client, guest.Type, guest.VMID)
		if lockStatus == "interrupted" {
			o.logger.Info("drain interrupted by shutdown while waiting for guest unlock; next leader will resume",
				"node", node.NodeName, "vmid", guest.VMID)
			return
		}
		if lockStatus != "completed" {
			o.failNode(ctx, job, node, fmt.Sprintf("%s %d still locked after migrate (status: %s)", guest.Type, guest.VMID, lockStatus))
			return
		}

		// Update workload map so next guest picks an accurate target.
		w := drs.Workload{VMID: guest.VMID, Name: guest.Name, Type: guest.Type, Node: target}
		nodeWorkloads[target] = append(nodeWorkloads[target], w)
		// Remove from source.
		remaining := nodeWorkloads[node.NodeName][:0]
		for _, wl := range nodeWorkloads[node.NodeName] {
			if wl.VMID != guest.VMID {
				remaining = append(remaining, wl)
			}
		}
		nodeWorkloads[node.NodeName] = remaining
	}

	// All guests migrated — complete drain.
	o.completeDrain(ctx, client, job, node)
}

func (o *Orchestrator) completeDrain(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode) {
	// Refresh apt index on the node.
	upid, err := client.RefreshNodeAptIndex(ctx, node.NodeName)
	if err != nil {
		o.logger.Warn("failed to refresh apt index", "node", node.NodeName, "error", err)
	} else {
		o.waitForTask(ctx, client, node.NodeName, upid)
	}

	if job.AutoUpgrade {
		// Auto-upgrade mode: transition to 'upgrading' step — the scheduler
		// tick will pick this up and run apt dist-upgrade via SSH.
		if err := o.queries.SetNodeDrainCompletedAuto(ctx, node.ID); err != nil {
			o.logger.Error("failed to set node drain completed (auto)", "node_id", node.ID, "error", err)
			return
		}
		o.publishEvent(ctx, job.ClusterID, job.ID, "node_upgrading")
		o.logger.Info("node drained, starting automated upgrade", "node", node.NodeName)
	} else {
		// Manual mode: pause for admin confirmation.
		if err := o.queries.SetNodeDrainCompletedManual(ctx, node.ID); err != nil {
			o.logger.Error("failed to set node drain completed (manual)", "node_id", node.ID, "error", err)
			return
		}
		o.publishEvent(ctx, job.ClusterID, job.ID, "node_awaiting_upgrade")
		o.logger.Info("node drained, awaiting manual upgrade", "node", node.NodeName)
	}
}

// resumeDrain re-runs the drain for guests that are still on the node after a
// container restart interrupted the original drain goroutine.
func (o *Orchestrator) resumeDrain(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode, remaining []GuestSnapshot) {
	// Touch updated_at so the next tick doesn't also try to resume.
	_ = o.queries.TouchRollingUpdateNode(ctx, node.ID)

	clusterNodes, err := client.GetNodes(ctx)
	if err != nil {
		o.failNode(ctx, job, node, fmt.Sprintf("resume drain — list cluster nodes: %v", err))
		return
	}

	var targets []string
	for _, cn := range clusterNodes {
		if cn.Node != node.NodeName && cn.Status == "online" {
			targets = append(targets, cn.Node)
		}
	}
	if len(targets) == 0 {
		o.failNode(ctx, job, node, "resume drain — no available target nodes")
		return
	}

	haResources, _ := client.GetHAResources(ctx)
	haResMap := make(map[string]proxmox.HAResource, len(haResources))
	for _, r := range haResources {
		haResMap[r.SID] = r
	}
	haGroups, _ := client.GetHAGroups(ctx)
	haGrpMap := make(map[string]proxmox.HAGroup, len(haGroups))
	for _, g := range haGroups {
		haGrpMap[g.Group] = g
	}
	haRules, _ := client.GetHARules(ctx)
	dbRules, _ := o.queries.ListDRSRules(ctx, job.ClusterID)
	drsRules := drs.ParseDBRules(dbRules)
	nodeWorkloads := BuildNodeWorkloads(ctx, client, clusterNodes)

	for _, guest := range remaining {
		if o.isJobCancelled(ctx, job.ID) {
			return
		}

		if guest.Passthrough {
			o.logger.Info("resume drain: shutting down passthrough guest",
				"vmid", guest.VMID, "name", guest.Name, "node", node.NodeName)
			var upid string
			var shutErr error
			switch guest.Type {
			case "qemu":
				upid, shutErr = client.ShutdownVM(ctx, node.NodeName, guest.VMID)
			case "lxc":
				upid, shutErr = client.ShutdownCT(ctx, node.NodeName, guest.VMID)
			}
			if shutErr != nil {
				o.failNode(ctx, job, node, fmt.Sprintf("resume drain — shutdown passthrough %s %d: %v", guest.Type, guest.VMID, shutErr))
				return
			}
			status := o.waitForTask(ctx, client, node.NodeName, upid)
			if status == "interrupted" {
				o.logger.Info("resume drain interrupted by shutdown during passthrough shutdown; next leader will resume",
					"node", node.NodeName, "vmid", guest.VMID)
				return
			}
			if status != "completed" {
				switch guest.Type {
				case "qemu":
					upid, shutErr = client.StopVM(ctx, node.NodeName, guest.VMID)
				case "lxc":
					upid, shutErr = client.StopCT(ctx, node.NodeName, guest.VMID)
				}
				if shutErr != nil {
					o.failNode(ctx, job, node, fmt.Sprintf("resume drain — force stop passthrough %s %d: %v", guest.Type, guest.VMID, shutErr))
					return
				}
				if forceStatus := o.waitForTask(ctx, client, node.NodeName, upid); forceStatus == "interrupted" {
					o.logger.Info("resume drain interrupted by shutdown during passthrough force-stop; next leader will resume",
						"node", node.NodeName, "vmid", guest.VMID)
					return
				}
			}
			continue
		}

		// Before issuing a new migration, wait for any in-flight Proxmox
		// lock to clear. A previous orchestrator instance may have started
		// a migrate that's still progressing (or has just completed) — if
		// we issue a fresh MigrateVM call while the guest is locked, Proxmox
		// returns "VM is locked (migrate)" and we'd falsely mark the node
		// failed. After the lock clears, re-check whether the guest is even
		// still on the source node before deciding to migrate again.
		lockStatus := o.waitForGuestUnlocked(ctx, client, guest.Type, guest.VMID)
		if lockStatus == "interrupted" {
			o.logger.Info("resume drain interrupted by shutdown while waiting for prior lock to clear",
				"node", node.NodeName, "vmid", guest.VMID)
			return
		}
		if !o.isGuestOnNode(ctx, client, node.NodeName, guest) {
			o.logger.Info("resume drain: guest already migrated by prior orchestrator instance, skipping",
				"vmid", guest.VMID, "type", guest.Type, "node", node.NodeName)
			// Track the moved guest as an additional workload on its current
			// location so subsequent target selections stay accurate.
			continue
		}

		gs := GuestSnapshot{VMID: guest.VMID, Name: guest.Name, Type: guest.Type, Status: guest.Status}
		target, err := SelectTarget(gs, node.NodeName, targets, haResMap, haGrpMap, haRules, drsRules, nodeWorkloads)
		if err != nil {
			o.failNode(ctx, job, node, fmt.Sprintf("resume drain — no valid target for %s %d: %v", guest.Type, guest.VMID, err))
			return
		}

		params := proxmox.MigrateParams{
			Target: target,
			Online: guest.Status == "running",
		}

		var upid string
		var migrateErr error
		switch guest.Type {
		case "qemu":
			upid, migrateErr = client.MigrateVM(ctx, node.NodeName, guest.VMID, params)
		case "lxc":
			upid, migrateErr = client.MigrateCT(ctx, node.NodeName, guest.VMID, params)
		default:
			continue
		}
		if migrateErr != nil {
			o.failNode(ctx, job, node, fmt.Sprintf("resume drain — migrate %s %d: %v", guest.Type, guest.VMID, migrateErr))
			return
		}

		o.logger.Info("resume drain: migrating guest",
			"vmid", guest.VMID, "type", guest.Type, "from", node.NodeName, "to", target)

		status := o.waitForTask(ctx, client, node.NodeName, upid)
		if status == "interrupted" {
			o.logger.Info("resume drain interrupted by shutdown while waiting for migration; next leader will resume",
				"node", node.NodeName, "vmid", guest.VMID, "upid", upid)
			return
		}
		if status != "completed" {
			o.failNode(ctx, job, node, fmt.Sprintf("resume drain — migration of %s %d failed (status: %s)", guest.Type, guest.VMID, status))
			return
		}
		postLockStatus := o.waitForGuestUnlocked(ctx, client, guest.Type, guest.VMID)
		if postLockStatus == "interrupted" {
			o.logger.Info("resume drain interrupted by shutdown while waiting for guest unlock; next leader will resume",
				"node", node.NodeName, "vmid", guest.VMID)
			return
		}
		if postLockStatus != "completed" {
			o.failNode(ctx, job, node, fmt.Sprintf("resume drain — %s %d still locked after migrate (status: %s)", guest.Type, guest.VMID, postLockStatus))
			return
		}

		w := drs.Workload{VMID: guest.VMID, Name: guest.Name, Type: guest.Type, Node: target}
		nodeWorkloads[target] = append(nodeWorkloads[target], w)
	}

	o.completeDrain(ctx, client, job, node)
}

// getGuestStatus returns the current status of a guest on a node ("running", "stopped", etc.).
func (o *Orchestrator) getGuestStatus(ctx context.Context, client *proxmox.Client, nodeName string, guest GuestSnapshot) string {
	switch guest.Type {
	case "qemu":
		vms, err := client.GetVMs(ctx, nodeName)
		if err != nil {
			return ""
		}
		for _, vm := range vms {
			if vm.VMID == guest.VMID {
				return vm.Status
			}
		}
	case "lxc":
		cts, err := client.GetContainers(ctx, nodeName)
		if err != nil {
			return ""
		}
		for _, ct := range cts {
			if ct.VMID == guest.VMID {
				return ct.Status
			}
		}
	}
	return ""
}

// upgradeAbsoluteTimeout caps the time we'll keep retrying a node's
// apt dist-upgrade from the first launch. Long enough to cover one full
// re-launch cycle after a container restart plus dpkg-lock waits.
const upgradeAbsoluteTimeout = 60 * time.Minute

// upgradeHeartbeatStale is how long updated_at can go without a touch
// before we conclude the SSH goroutine died (Swarm reschedule, K8s
// rolling restart, OOM-kill) and re-launch the upgrade. The runSSHUpgrade
// heartbeat ticks every 30s, so 3 minutes catches a dead goroutine
// quickly while tolerating transient DB hiccups.
const upgradeHeartbeatStale = 3 * time.Minute

func (o *Orchestrator) advanceUpgrading(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode) {
	// If upgrade already started, check the absolute timeout first.
	if node.UpgradeStartedAt.Valid {
		if time.Since(node.UpgradeStartedAt.Time) > upgradeAbsoluteTimeout {
			o.failNode(ctx, job, node, fmt.Sprintf("automated upgrade timed out after %s", upgradeAbsoluteTimeout))
			return
		}
		// If the heartbeat is fresh, an SSH goroutine is still working —
		// just wait for the next tick.
		if node.UpdatedAt.IsZero() || time.Since(node.UpdatedAt) <= upgradeHeartbeatStale {
			return
		}
		// Heartbeat is stale: the previous SSH goroutine died with its
		// container. Fall through to re-launch. The remote apt command
		// is resume-safe — it waits for any still-running dpkg to release
		// the lock and exits 0 when there's nothing left to upgrade.
		o.logger.Info("upgrade heartbeat stale, re-launching SSH upgrade after orchestrator restart",
			"node", node.NodeName,
			"last_heartbeat", node.UpdatedAt,
			"upgrade_started_at", node.UpgradeStartedAt.Time)
	}

	// First time seeing this node in 'upgrading' (or resuming after a
	// dead SSH goroutine) — kick off the SSH upgrade.
	sshCreds, err := o.queries.GetClusterSSHCredentials(ctx, job.ClusterID)
	if err != nil {
		o.failNode(ctx, job, node, fmt.Sprintf("SSH credentials not found: %v", err))
		return
	}

	// Decrypt credentials.
	var password, privateKey string
	if sshCreds.EncryptedPassword != "" {
		password, err = crypto.Decrypt(sshCreds.EncryptedPassword, o.encryptionKey)
		if err != nil {
			o.failNode(ctx, job, node, fmt.Sprintf("decrypt SSH password: %v", err))
			return
		}
	}
	if sshCreds.EncryptedPrivateKey != "" {
		privateKey, err = crypto.Decrypt(sshCreds.EncryptedPrivateKey, o.encryptionKey)
		if err != nil {
			o.failNode(ctx, job, node, fmt.Sprintf("decrypt SSH key: %v", err))
			return
		}
	}

	// Look up the node's IP address from the DB (populated by collector from corosync).
	// Fail loudly if missing — the previous fallback to using node name as
	// hostname could end up at any DNS-resolved address.
	sshHost, addrErr := o.queries.GetNodeAddressByName(ctx, db.GetNodeAddressByNameParams{
		ClusterID: job.ClusterID,
		Name:      node.NodeName,
	})
	if addrErr != nil || sshHost == "" {
		o.failNode(ctx, job, node, fmt.Sprintf("no IP address known for node %q (collector hasn't reported it). Wait for the collector to tick, then retry.", node.NodeName))
		return
	}

	// Require a pinned host key. The previous InsecureIgnoreHostKey policy
	// is gone; an unpinned host now fails closed.
	pinned, pinErr := o.queries.GetSSHKnownHost(ctx, db.GetSSHKnownHostParams{
		ClusterID: job.ClusterID,
		Host:      sshHost,
		Port:      sshCreds.Port,
	})
	if pinErr != nil {
		o.failNode(ctx, job, node, fmt.Sprintf("SSH host key not pinned for %s. Visit Settings → SSH Credentials, run Test Connection, and confirm the fingerprint to pin it.", sshHost))
		return
	}
	knownKey, parseErr := sshpkg.ParseAuthorizedKey(pinned.PublicKey)
	if parseErr != nil {
		o.failNode(ctx, job, node, fmt.Sprintf("stored SSH host key for %s is corrupt: %v — delete and re-pin", sshHost, parseErr))
		return
	}

	sshCfg := sshpkg.Config{
		Host:         sshHost,
		Port:         int(sshCreds.Port),
		Username:     sshCreds.Username,
		Password:     password,
		PrivateKey:   privateKey,
		KnownHostKey: knownKey,
	}

	_ = o.queries.SetNodeUpgradeStarted(ctx, node.ID)
	if !node.UpgradeStartedAt.Valid {
		o.publishEvent(ctx, job.ClusterID, job.ID, "node_upgrade_started")
		o.logger.Info("starting automated apt dist-upgrade via SSH", "node", node.NodeName)
	}

	// Run apt dist-upgrade in a goroutine so we don't block the tick.
	// Detach from the tick scope so a tick rollover doesn't cancel a
	// long-running SSH session, but stay rooted in shutdownCtx so a
	// graceful SIGTERM aborts the SSH call cleanly. The 45-minute SSH
	// timeout gives breathing room for dpkg-lock waits when this is a
	// re-launch after a prior orchestrator instance was killed mid-upgrade.
	upgradeCtx, upgradeCancel := context.WithTimeout(o.shutdownCtx, 45*time.Minute)
	go o.runSSHUpgrade(upgradeCtx, upgradeCancel, job, node, client, sshCfg)
}

// startUpgradeHeartbeat spawns a 30-second-interval goroutine that bumps
// updated_at on the rolling_update_nodes row while the SSH upgrade is in
// flight. advanceUpgrading on a different scheduler-leader instance uses
// the staleness of updated_at to detect a goroutine that died with its
// container (Swarm reschedule, K8s rolling restart, OOM) and re-launch.
//
// Parented to the SSH upgrade ctx so the heartbeat stops promptly when
// SSH returns or shutdownCtx cancels.
func (o *Orchestrator) startUpgradeHeartbeat(ctx context.Context, nodeID uuid.UUID) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = o.queries.TouchRollingUpdateNode(ctx, nodeID)
			}
		}
	}()
}

func (o *Orchestrator) runSSHUpgrade(ctx context.Context, cancel context.CancelFunc, job db.RollingUpdateJob, node db.RollingUpdateNode, client *proxmox.Client, sshCfg sshpkg.Config) {
	defer cancel()

	// Heartbeat: keep updated_at fresh so advanceUpgrading on another
	// scheduler-leader instance can distinguish a live SSH session from a
	// goroutine that died with its container. Without this, a leader
	// takeover during an SSH session can't tell whether to wait or resume.
	//
	// Parented to ctx (the SSH upgrade context) so the ticker stops as
	// soon as the SSH call returns or shutdownCtx cancels — at that point
	// it's correct for the heartbeat to stop, since the next leader
	// should treat us as dead and re-launch.
	o.startUpgradeHeartbeat(ctx, node.ID)

	// Build package exclude args with strict validation.
	// Debian package names: [a-z0-9][a-z0-9.+\-]+ (max 128 chars).
	var excludeArgs string
	if len(job.PackageExcludes) > 0 {
		for _, pkg := range job.PackageExcludes {
			if !isValidDebianPkgName(pkg) {
				o.failNode(ctx, job, node, fmt.Sprintf("invalid package exclude name: %q", pkg))
				return
			}
			excludeArgs += " --exclude " + pkg
		}
	}

	// The command: recover any half-installed package state from a prior
	// interrupted run, refresh the apt index, then dist-upgrade.
	//
	// Resume-safe design — this command can be re-run after a prior
	// orchestrator instance was killed mid-upgrade:
	//   - `dpkg --configure -a` finishes configuring packages that were
	//     unpacked but not configured when the previous SSH session was
	//     terminated. The `|| true` keeps us moving if there's nothing
	//     to recover or if the dpkg lock is briefly held by another apt.
	//   - DPkg::Lock::Timeout=1800 makes apt wait up to 30 min for the
	//     dpkg lock to clear (Debian/Proxmox apt 2.0+).
	//   - apt-get update + dist-upgrade are both idempotent: when there's
	//     nothing left to upgrade, dist-upgrade exits 0 with "0 upgraded".
	//   - DEBIAN_FRONTEND=noninteractive and the Dpkg::Options=--force-conf*
	//     flags prevent any interactive prompts.
	cmd := fmt.Sprintf(
		"export DEBIAN_FRONTEND=noninteractive && "+
			"(dpkg --configure -a 2>&1 || true) && "+
			"apt-get update -o DPkg::Lock::Timeout=1800 && "+
			"apt-get dist-upgrade -y"+
			" -o DPkg::Lock::Timeout=1800"+
			" -o Dpkg::Options::=--force-confdef"+
			" -o Dpkg::Options::=--force-confold"+
			"%s 2>&1",
		excludeArgs,
	)

	result, err := sshpkg.Execute(ctx, sshCfg, cmd)
	if err != nil {
		// A graceful container shutdown (Swarm reschedule, K8s rolling
		// restart) cancels upgradeCtx mid-SSH and surfaces here as a
		// context.Canceled error. The dpkg/apt process on the remote
		// keeps running independently of our SSH stream, so marking the
		// node failed here would discard work that the OS is still doing.
		// Instead, exit cleanly. The next scheduler leader's
		// advanceUpgrading will detect the stalled UpgradeStartedAt and
		// re-launch the upgrade — apt is idempotent (a re-run after
		// completion is a no-op), and if dpkg is still running it'll
		// block on the dpkg lock and report so via exit code.
		if errors.Is(err, context.Canceled) || o.shutdownCtx.Err() != nil {
			o.logger.Info("SSH upgrade interrupted by shutdown — dpkg may still be running on remote; next leader will resume",
				"node", node.NodeName)
			return
		}
		o.failNode(ctx, job, node, fmt.Sprintf("SSH upgrade failed: %v", err))
		return
	}

	// Store the output (truncate to 64KB).
	output := result.Stdout
	if len(output) > 65536 {
		output = output[len(output)-65536:]
	}
	_ = o.queries.SetNodeUpgradeOutput(ctx, db.SetNodeUpgradeOutputParams{
		ID:            node.ID,
		UpgradeOutput: output,
	})

	if result.ExitCode != 0 {
		// The apt command uses `2>&1` so Stderr is empty on failure. Tail the
		// combined output (capped to keep the failure_reason readable) so the
		// real cause — mirror sync error, broken package, etc. — is visible
		// without having to query the upgrade_output column in the DB.
		tail := output
		const maxTail = 2000
		if len(tail) > maxTail {
			tail = "…(truncated)…\n" + tail[len(tail)-maxTail:]
		}
		o.failNode(ctx, job, node, fmt.Sprintf("apt dist-upgrade exited with code %d:\n%s", result.ExitCode, tail))
		return
	}

	o.logger.Info("automated upgrade completed", "node", node.NodeName)

	// Check if the job has been cancelled while the upgrade was running.
	if o.isJobCancelled(ctx, job.ID) {
		o.logger.Info("job cancelled during upgrade, stopping", "node", node.NodeName)
		return
	}

	// Check if a reboot is actually required (kernel update, etc.).
	// Debian/Proxmox creates /var/run/reboot-required when a reboot is needed.
	needsReboot := job.RebootAfterUpdate
	if !needsReboot {
		rebootCheck, rebootErr := sshpkg.Execute(ctx, sshCfg, "test -f /var/run/reboot-required && echo REBOOT_NEEDED || echo NO_REBOOT")
		if rebootErr == nil && strings.Contains(rebootCheck.Stdout, "REBOOT_NEEDED") {
			o.logger.Info("reboot required after upgrade (kernel/critical update detected)", "node", node.NodeName)
			needsReboot = true
		}
	}

	if needsReboot {
		if err := o.queries.SetNodeUpgradeCompleted(ctx, node.ID); err != nil {
			o.logger.Error("failed to set node upgrade completed", "node_id", node.ID, "error", err)
			return
		}

		if err := client.RebootNode(ctx, node.NodeName); err != nil {
			o.failNode(ctx, job, node, fmt.Sprintf("reboot node after upgrade: %v", err))
			return
		}

		o.publishEvent(ctx, job.ClusterID, job.ID, "node_rebooting")
		o.logger.Info("node rebooting after automated upgrade", "node", node.NodeName)
	} else {
		if err := o.queries.SetNodeUpgradeCompletedNoReboot(ctx, node.ID); err != nil {
			o.logger.Error("failed to set node upgrade completed (no reboot)", "node_id", node.ID, "error", err)
			return
		}

		o.publishEvent(ctx, job.ClusterID, job.ID, "node_health_check_passed")
		o.logger.Info("automated upgrade completed, no reboot needed", "node", node.NodeName)
	}
}

func (o *Orchestrator) advanceDraining(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode) {
	// Check if drain has been going on too long (1 hour timeout).
	if node.DrainStartedAt.Valid && time.Since(node.DrainStartedAt.Time) > time.Hour {
		o.failNode(ctx, job, node, "drain timed out after 1 hour")
		return
	}

	// Resilience: if the drain hasn't progressed in 2 minutes (e.g. container
	// restart killed the goroutine), check Proxmox and resume or complete.
	if !node.UpdatedAt.IsZero() && time.Since(node.UpdatedAt) < 2*time.Minute {
		return // Drain is actively progressing.
	}

	// Parse the original guest list from the snapshot.
	var guests []GuestSnapshot
	if err := json.Unmarshal(node.GuestsJson, &guests); err != nil || len(guests) == 0 {
		o.completeDrain(ctx, client, job, node)
		return
	}

	// Check how many guests are still on this node.
	var remaining []GuestSnapshot
	for _, g := range guests {
		if o.isGuestOnNode(ctx, client, node.NodeName, g) {
			// For passthrough guests, check if already stopped (shutdown succeeded before restart).
			if g.Passthrough {
				status := o.getGuestStatus(ctx, client, node.NodeName, g)
				if status == "stopped" {
					continue // Already shut down, don't count as remaining.
				}
			}
			remaining = append(remaining, g)
		}
	}

	if len(remaining) == 0 {
		o.logger.Info("drain resume: all guests already drained, completing",
			"node", node.NodeName)
		o.completeDrain(ctx, client, job, node)
		return
	}

	// Re-trigger drain for the remaining guests.
	o.logger.Info("drain resume: re-triggering drain for remaining guests",
		"node", node.NodeName, "remaining", len(remaining), "total", len(guests))
	o.resumeDrain(ctx, client, job, node, remaining)
}

func (o *Orchestrator) advanceRebooting(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode) {
	// Check if reboot timed out (10 minutes).
	if node.RebootStartedAt.Valid && time.Since(node.RebootStartedAt.Time) > 10*time.Minute {
		o.failNode(ctx, job, node, "reboot timed out after 10 minutes")
		return
	}

	// Don't even probe for the first 30s — a Proxmox node usually takes 10-30s
	// to start its actual shutdown sequence after `RebootNode` returns success,
	// and during that window the API still answers with the *old* uptime.
	if node.RebootStartedAt.Valid && time.Since(node.RebootStartedAt.Time) < 30*time.Second {
		return
	}

	// Try to get node status. Connection errors are expected (the node is down).
	status, err := client.GetNodeStatus(ctx, node.NodeName)
	if err != nil {
		return
	}

	// The node only counts as "rebooted" when its uptime is shorter than the
	// time elapsed since we issued the reboot. Otherwise we're seeing the
	// pre-reboot process (the OS hasn't shut down yet) and must keep waiting.
	// A small skew buffer guards against clock drift between Nexara and the node.
	const clockSkewBuffer = 10 * time.Second
	elapsed := time.Since(node.RebootStartedAt.Time)
	if time.Duration(status.Uptime)*time.Second >= elapsed+clockSkewBuffer {
		o.logger.Debug("node still reporting pre-reboot uptime, waiting",
			"node", node.NodeName, "uptime_s", status.Uptime, "elapsed_s", int64(elapsed.Seconds()))
		return
	}

	if err := o.queries.SetNodeRebootCompleted(ctx, node.ID); err != nil {
		o.logger.Error("failed to set node reboot completed", "node_id", node.ID, "error", err)
		return
	}
	if err := o.queries.SetNodeHealthCheckPassed(ctx, node.ID); err != nil {
		o.logger.Error("failed to set node health check passed", "node_id", node.ID, "error", err)
		return
	}
	o.publishEvent(ctx, job.ClusterID, job.ID, "node_health_check_passed")
	o.logger.Info("node back online after reboot",
		"node", node.NodeName, "uptime_s", status.Uptime, "elapsed_s", int64(elapsed.Seconds()))
}

func (o *Orchestrator) advanceHealthCheck(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode) {
	// Node is healthy — start restoring guests if configured.
	if !job.AutoRestoreGuests {
		// Re-enable HA rules even when not auto-restoring guests.
		o.restoreHAStates(ctx, client, node)
		if err := o.queries.SetNodeRestoreCompleted(ctx, node.ID); err != nil {
			o.logger.Error("failed to set node restore completed", "node_id", node.ID, "error", err)
			return
		}
		o.publishEvent(ctx, job.ClusterID, job.ID, "node_completed")
		o.logger.Info("node update completed (no auto-restore)", "node", node.NodeName)
		return
	}

	// Parse guests snapshot.
	var guests []GuestSnapshot
	_ = json.Unmarshal(node.GuestsJson, &guests)

	if len(guests) == 0 {
		o.restoreHAStates(ctx, client, node)
		if err := o.queries.SetNodeRestoreCompleted(ctx, node.ID); err != nil {
			o.logger.Error("failed to set node restore completed", "node_id", node.ID, "error", err)
		}
		o.publishEvent(ctx, job.ClusterID, job.ID, "node_completed")
		return
	}

	if err := o.queries.SetNodeRestoreStarted(ctx, node.ID); err != nil {
		o.logger.Error("failed to set node restore started", "node_id", node.ID, "error", err)
		return
	}

	o.publishEvent(ctx, job.ClusterID, job.ID, "node_restoring")

	// Find where guests currently are so we can migrate them back.
	var clusterNodes []proxmox.NodeListEntry
	err := o.callWithRetryFailover(ctx, job.ClusterID, client, func(c *proxmox.Client) error {
		var ferr error
		clusterNodes, ferr = c.GetNodes(ctx)
		return ferr
	})
	if err != nil {
		o.failNode(ctx, job, node, fmt.Sprintf("list nodes for restore: %v", err))
		return
	}

	for _, guest := range guests {
		// Passthrough guests were shut down in-place — start them back up.
		if guest.Passthrough {
			o.logger.Info("starting passthrough guest after node update",
				"vmid", guest.VMID, "type", guest.Type, "name", guest.Name, "node", node.NodeName)

			var upid string
			var startErr error
			switch guest.Type {
			case "qemu":
				upid, startErr = client.StartVM(ctx, node.NodeName, guest.VMID)
			case "lxc":
				upid, startErr = client.StartCT(ctx, node.NodeName, guest.VMID)
			}
			if startErr != nil {
				o.logger.Warn("failed to start passthrough guest after update",
					"vmid", guest.VMID, "error", startErr)
				continue // Non-fatal — skip the guest.
			}

			status := o.waitForTask(ctx, client, node.NodeName, upid)
			if status == "interrupted" {
				o.logger.Info("restore interrupted by shutdown during passthrough start; next leader will resume",
					"node", node.NodeName, "vmid", guest.VMID)
				return
			}
			if status != "completed" {
				o.logger.Warn("passthrough guest start failed",
					"vmid", guest.VMID, "status", status)
			} else {
				o.logger.Info("passthrough guest started successfully",
					"vmid", guest.VMID, "name", guest.Name)
			}
			continue
		}

		// Find the guest on other nodes.
		currentNode := o.findGuestLocation(ctx, client, clusterNodes, guest, node.NodeName)
		if currentNode == "" || currentNode == node.NodeName {
			continue // Already on this node or not found.
		}

		params := proxmox.MigrateParams{
			Target: node.NodeName,
			Online: true,
		}

		var upid string
		var migrateErr error

		switch guest.Type {
		case "qemu":
			upid, migrateErr = client.MigrateVM(ctx, currentNode, guest.VMID, params)
		case "lxc":
			upid, migrateErr = client.MigrateCT(ctx, currentNode, guest.VMID, params)
		}

		if migrateErr != nil {
			o.logger.Warn("failed to restore guest, skipping",
				"vmid", guest.VMID, "type", guest.Type, "error", migrateErr)
			continue // Non-fatal for restore — skip the guest.
		}

		o.logger.Info("restoring guest to node",
			"vmid", guest.VMID, "type", guest.Type,
			"from", currentNode, "to", node.NodeName)

		status := o.waitForTask(ctx, client, currentNode, upid)
		if status == "interrupted" {
			o.logger.Info("restore interrupted by shutdown while waiting for migration; next leader will resume",
				"node", node.NodeName, "vmid", guest.VMID, "upid", upid)
			return
		}
		if status != "completed" {
			o.logger.Warn("guest restore migration failed",
				"vmid", guest.VMID, "status", status)
			continue
		}
		// HA-managed guests redirect to a fast hamigrate UPID; wait for the
		// underlying qmigrate to drop the lock so the next node's drain
		// doesn't race the in-flight restore migration.
		lockStatus := o.waitForGuestUnlocked(ctx, client, guest.Type, guest.VMID)
		if lockStatus == "interrupted" {
			o.logger.Info("restore interrupted by shutdown while waiting for guest unlock; next leader will resume",
				"node", node.NodeName, "vmid", guest.VMID)
			return
		}
		if lockStatus != "completed" {
			o.logger.Warn("guest still locked after restore migrate",
				"vmid", guest.VMID, "status", lockStatus)
		}
	}

	// Re-enable HA on all guests that had it before drain.
	o.restoreHAStates(ctx, client, node)

	if err := o.queries.SetNodeRestoreCompleted(ctx, node.ID); err != nil {
		o.logger.Error("failed to set node restore completed", "node_id", node.ID, "error", err)
		return
	}

	o.publishEvent(ctx, job.ClusterID, job.ID, "node_completed")
	o.logger.Info("node update completed", "node", node.NodeName)
}

func (o *Orchestrator) advanceRestoring(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode) {
	// Check for timeout (30 minutes).
	if node.RestoreStartedAt.Valid && time.Since(node.RestoreStartedAt.Time) > 30*time.Minute {
		o.logger.Warn("guest restore timed out, marking node completed", "node", node.NodeName)
		o.restoreHAStates(ctx, client, node)
		_ = o.queries.SetNodeRestoreCompleted(ctx, node.ID)
		o.publishEvent(ctx, job.ClusterID, job.ID, "node_completed")
		return
	}

	// Resilience: if restore hasn't progressed in 2 minutes, re-trigger.
	if !node.UpdatedAt.IsZero() && time.Since(node.UpdatedAt) < 2*time.Minute {
		return
	}

	o.logger.Info("restore resume: re-triggering guest restore after stall", "node", node.NodeName)
	o.advanceHealthCheck(ctx, client, job, node)
}

// ConfirmUpgrade is called by the API when an admin confirms a node upgrade is done.
func (o *Orchestrator) ConfirmUpgrade(ctx context.Context, job db.RollingUpdateJob, node db.RollingUpdateNode) error {
	if node.Step != "awaiting_upgrade" {
		return fmt.Errorf("node is not awaiting upgrade (current step: %s)", node.Step)
	}

	if err := o.queries.ConfirmNodeUpgrade(ctx, node.ID); err != nil {
		return fmt.Errorf("confirm node upgrade: %w", err)
	}

	if job.RebootAfterUpdate {
		client, err := o.createClient(ctx, job.ClusterID)
		if err != nil {
			return fmt.Errorf("create client for reboot: %w", err)
		}

		if err := client.RebootNode(ctx, node.NodeName); err != nil {
			// Mark reboot started even on failure — the node might still reboot.
			_ = o.queries.SetNodeRebootStarted(ctx, node.ID)
			o.failNode(ctx, job, node, fmt.Sprintf("reboot node: %v", err))
			return fmt.Errorf("reboot node: %w", err)
		}

		if err := o.queries.SetNodeRebootStarted(ctx, node.ID); err != nil {
			return fmt.Errorf("set node reboot started: %w", err)
		}

		o.publishEvent(ctx, job.ClusterID, job.ID, "node_rebooting")
		o.logger.Info("node rebooting after upgrade", "node", node.NodeName)
	} else {
		// No reboot — go straight to health check / completed.
		if err := o.queries.SetNodeHealthCheckPassed(ctx, node.ID); err != nil {
			return fmt.Errorf("set health check: %w", err)
		}

		o.publishEvent(ctx, job.ClusterID, job.ID, "node_health_check_passed")
		o.logger.Info("upgrade confirmed, no reboot", "node", node.NodeName)
	}

	return nil
}

func (o *Orchestrator) snapshotGuests(ctx context.Context, client *proxmox.Client, nodeName string) ([]GuestSnapshot, error) {
	var guests []GuestSnapshot

	vms, err := client.GetVMs(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("get VMs: %w", err)
	}
	for _, vm := range vms {
		if vm.Template == 1 {
			continue
		}
		if vm.Status == "running" || vm.Status == "paused" {
			gs := GuestSnapshot{
				VMID:   vm.VMID,
				Name:   vm.Name,
				Type:   "qemu",
				Status: vm.Status,
			}
			// Check for PCI/USB passthrough — these VMs cannot be live-migrated.
			config, cfgErr := client.GetVMConfig(ctx, nodeName, vm.VMID)
			if cfgErr == nil && hasPassthrough(config) {
				gs.Passthrough = true
				o.logger.Info("detected hardware passthrough on VM",
					"vmid", vm.VMID, "name", vm.Name, "node", nodeName)
			}
			guests = append(guests, gs)
		}
	}

	cts, err := client.GetContainers(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("get containers: %w", err)
	}
	for _, ct := range cts {
		if ct.Status == "running" {
			guests = append(guests, GuestSnapshot{
				VMID:   ct.VMID,
				Name:   ct.Name,
				Type:   "lxc",
				Status: ct.Status,
			})
		}
	}

	return guests, nil
}

func (o *Orchestrator) findGuestLocation(ctx context.Context, client *proxmox.Client, clusterNodes []proxmox.NodeListEntry, guest GuestSnapshot, excludeNode string) string {
	for _, cn := range clusterNodes {
		if cn.Node == excludeNode || cn.Status != "online" {
			continue
		}

		switch guest.Type {
		case "qemu":
			vms, err := client.GetVMs(ctx, cn.Node)
			if err != nil {
				continue
			}
			for _, vm := range vms {
				if vm.VMID == guest.VMID {
					return cn.Node
				}
			}
		case "lxc":
			cts, err := client.GetContainers(ctx, cn.Node)
			if err != nil {
				continue
			}
			for _, ct := range cts {
				if ct.VMID == guest.VMID {
					return cn.Node
				}
			}
		}
	}
	return ""
}

// isGuestOnNode checks if a guest (VM/CT) is still present on the given node.
func (o *Orchestrator) isGuestOnNode(ctx context.Context, client *proxmox.Client, nodeName string, guest GuestSnapshot) bool {
	switch guest.Type {
	case "qemu":
		vms, err := client.GetVMs(ctx, nodeName)
		if err != nil {
			return true // Assume still there on error to avoid skipping incorrectly.
		}
		for _, vm := range vms {
			if vm.VMID == guest.VMID {
				return true
			}
		}
		return false
	case "lxc":
		cts, err := client.GetContainers(ctx, nodeName)
		if err != nil {
			return true
		}
		for _, ct := range cts {
			if ct.VMID == guest.VMID {
				return true
			}
		}
		return false
	}
	return true
}

// isJobCancelled re-reads the job from the DB to check if it's been cancelled or paused.
func (o *Orchestrator) isJobCancelled(ctx context.Context, jobID uuid.UUID) bool {
	job, err := o.queries.GetRollingUpdateJob(ctx, jobID)
	if err != nil {
		return false
	}
	return job.Status == "cancelled" || job.Status == "paused"
}

// disableDRSIfEnabled checks if DRS is enabled for the cluster and disables it
// during the rolling update to prevent DRS from migrating VMs back to nodes being drained.
func (o *Orchestrator) disableDRSIfEnabled(ctx context.Context, job db.RollingUpdateJob) {
	cfg, err := o.queries.GetDRSConfig(ctx, job.ClusterID)
	if err != nil {
		// No DRS config = DRS not set up, nothing to do.
		return
	}
	if !cfg.Enabled {
		return
	}

	o.logger.Info("disabling DRS during rolling update",
		"job_id", job.ID, "cluster_id", job.ClusterID)

	// Record that DRS was enabled so we can restore it later.
	_ = o.queries.SetJobDRSWasEnabled(ctx, db.SetJobDRSWasEnabledParams{
		ID:            job.ID,
		DrsWasEnabled: true,
	})

	// Disable DRS.
	if err := o.queries.SetDRSEnabled(ctx, db.SetDRSEnabledParams{
		ClusterID: job.ClusterID,
		Enabled:   false,
	}); err != nil {
		o.logger.Warn("failed to disable DRS during rolling update",
			"job_id", job.ID, "error", err)
	}
}

// restoreDRS re-enables DRS if it was disabled at the start of the rolling update.
func (o *Orchestrator) restoreDRS(ctx context.Context, job db.RollingUpdateJob) {
	// Re-read the job so a fail-on-the-same-tick-as-disable path still sees the
	// persisted flag: disableDRSIfEnabled sets drs_was_enabled in the DB but not
	// on the in-memory struct passed in (read at the top of the tick), so a
	// first-tick failNode → failJob → restoreDRS would otherwise skip the
	// re-enable and leave DRS off permanently. Mirrors restoreNativeCRS.
	if fresh, err := o.queries.GetRollingUpdateJob(ctx, job.ID); err == nil {
		job = fresh
	}
	if !job.DrsWasEnabled {
		return
	}

	o.logger.Info("re-enabling DRS after rolling update",
		"job_id", job.ID, "cluster_id", job.ClusterID)

	if err := o.queries.SetDRSEnabled(ctx, db.SetDRSEnabledParams{
		ClusterID: job.ClusterID,
		Enabled:   true,
	}); err != nil {
		o.logger.Warn("failed to re-enable DRS after rolling update",
			"job_id", job.ID, "error", err)
	}
}

// pauseNativeCRSIfActive turns off Proxmox's native CRS dynamic auto-rebalancer
// for the duration of the rolling update. Because we drain nodes with plain
// live-migration (which leaves the node online and HA-eligible), the native
// balancer would otherwise migrate HA guests right back onto a node we're
// draining or rebooting. This mirrors disableDRSIfEnabled for Nexara's own DRS.
//
// Fail-open: any error leaves the cluster's CRS untouched and the update
// proceeds — the HA-rule disabling and Nexara-DRS disable still apply, and the
// operator can pause auto-rebalance manually. Only clusters on PVE 9.2+ with
// ha-auto-rebalance=1 are affected; for everyone else this is a no-op.
func (o *Orchestrator) pauseNativeCRSIfActive(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob) {
	opts, err := client.GetClusterOptions(ctx)
	if err != nil {
		o.logger.Warn("rolling: could not read cluster options to check native CRS; proceeding without pausing it",
			"job_id", job.ID, "cluster_id", job.ClusterID, "error", err)
		return
	}
	crs := proxmox.ParseCRSSettings(opts.CRS)
	if !crs.AutoRebalanceActive() {
		return // Native auto-rebalance off (or pre-9.2 cluster) — nothing to pause.
	}

	saved := crs.Restorable()
	paused := crs.PausedAutoRebalance()

	if err := client.SetClusterOptions(ctx, proxmox.UpdateClusterOptionsParams{CRS: &paused}); err != nil {
		o.logger.Warn("rolling: failed to pause native CRS auto-rebalance; proceeding (native balancer may contend with the drain)",
			"job_id", job.ID, "error", err)
		return
	}

	if err := o.queries.SetJobNativeCRSPaused(ctx, db.SetJobNativeCRSPausedParams{
		ID:             job.ID,
		SavedCrsConfig: saved,
	}); err != nil {
		// We changed Proxmox but couldn't persist the restore state. Roll the
		// pause back immediately so we don't leave the balancer off with no
		// record to restore it from later.
		o.logger.Error("rolling: paused native CRS but failed to persist restore state; rolling back",
			"job_id", job.ID, "error", err)
		if rbErr := client.SetClusterOptions(ctx, proxmox.UpdateClusterOptionsParams{CRS: &saved}); rbErr != nil {
			o.logger.Error("rolling: failed to roll back native CRS pause — auto-rebalance left off; restore manually via Datacenter Options → CRS",
				"job_id", job.ID, "saved_crs", saved, "error", rbErr)
		}
		return
	}

	o.logger.Info("paused native CRS auto-rebalance for rolling update",
		"job_id", job.ID, "cluster_id", job.ClusterID, "saved_crs", saved, "paused_crs", paused)
	o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_crs_paused",
		map[string]string{"saved_crs": saved, "paused_crs": paused})
}

// restoreNativeCRS writes back the CRS config saved by pauseNativeCRSIfActive.
// It re-reads the job first so a fail-on-the-same-tick-as-pause path still sees
// the persisted flag (the in-memory job copy may predate the pause). It builds
// its own client so it can run from the no-client failJob path as well. If the
// restore can't be applied, it logs loudly and audits a restore_failed entry so
// the operator knows auto-rebalance is still paused.
func (o *Orchestrator) restoreNativeCRS(ctx context.Context, job db.RollingUpdateJob) {
	if fresh, err := o.queries.GetRollingUpdateJob(ctx, job.ID); err == nil {
		job = fresh
	}
	if !job.NativeCrsPaused {
		return
	}

	saved := job.SavedCrsConfig
	client, err := o.createClient(ctx, job.ClusterID)
	if err != nil {
		o.logger.Error("rolling: failed to build client to restore native CRS — auto-rebalance left paused; restore manually via Datacenter Options → CRS",
			"job_id", job.ID, "cluster_id", job.ClusterID, "error", err)
		o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_crs_restore_failed",
			map[string]string{"saved_crs": saved, "error": err.Error()})
		return
	}

	if err := client.SetClusterOptions(ctx, proxmox.UpdateClusterOptionsParams{CRS: &saved}); err != nil {
		o.logger.Error("rolling: failed to restore native CRS auto-rebalance — left paused; restore manually",
			"job_id", job.ID, "saved_crs", saved, "error", err)
		o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_crs_restore_failed",
			map[string]string{"saved_crs": saved, "error": err.Error()})
		return
	}

	o.logger.Info("restored native CRS auto-rebalance after rolling update",
		"job_id", job.ID, "cluster_id", job.ClusterID, "crs", saved)
	o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_crs_restored",
		map[string]string{"crs": saved})
}

// sendJobNotification dispatches a notification to the configured channel when a job completes or fails.
func (o *Orchestrator) sendJobNotification(ctx context.Context, job db.RollingUpdateJob, status, message string) {
	if o.notifyRegistry == nil || !job.NotifyChannelID.Valid {
		return
	}

	channelID := uuid.UUID(job.NotifyChannelID.Bytes)
	channel, err := o.queries.GetNotificationChannelEnabled(ctx, channelID)
	if err != nil {
		o.logger.Warn("notification channel not found or disabled", "channel_id", channelID, "error", err)
		return
	}

	configJSON, err := crypto.Decrypt(channel.ConfigEncrypted, o.encryptionKey)
	if err != nil {
		o.logger.Error("failed to decrypt channel config", "channel_id", channelID, "error", err)
		return
	}

	dispatcher, ok := o.notifyRegistry.Get(channel.ChannelType)
	if !ok {
		o.logger.Warn("no dispatcher for channel type", "type", channel.ChannelType)
		return
	}

	severity := "info"
	if status == "failed" {
		severity = "critical"
	}

	clusterName := job.ClusterID.String()
	if cluster, err := o.queries.GetCluster(ctx, job.ClusterID); err == nil {
		clusterName = cluster.Name
	}

	payload := notifications.AlertPayload{
		RuleName:     "Rolling Update",
		Severity:     severity,
		State:        status,
		Metric:       "rolling_update",
		ResourceName: clusterName,
		ClusterID:    clusterName,
		Message:      message,
		FiredAt:      time.Now().UTC().Format(time.RFC3339Nano),
	}

	go func() {
		// Derive from shutdownCtx so SIGTERM aborts the dispatch quickly;
		// detach from the tick context so a tick rollover doesn't kill it.
		sendCtx, cancel := context.WithTimeout(o.shutdownCtx, 30*time.Second)
		defer cancel()

		if err := dispatcher.Send(sendCtx, json.RawMessage(configJSON), payload); err != nil {
			o.logger.Error("rolling update notification dispatch failed",
				"channel_id", channelID, "channel_type", channel.ChannelType, "error", err)
		} else {
			o.logger.Info("rolling update notification sent",
				"channel_id", channelID, "status", status)
		}
	}()
}

// triggerPostUpgradeCVEScan kicks off a CVE rescan in the background after a
// rolling update completes successfully so the security posture reflects the
// new package state without waiting for the next scheduled tick. The scan
// itself handles a concurrent-running scan via the existing scheduler logic;
// here we only guard against firing while a scan is already in flight.
func (o *Orchestrator) triggerPostUpgradeCVEScan(job db.RollingUpdateJob) {
	if o.cveScanner == nil {
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				o.logger.Error("post-upgrade CVE scan panicked",
					"job_id", job.ID, "cluster_id", job.ClusterID, "panic", r)
			}
		}()

		// Detach from the tick context (tick rollover would cancel it) but
		// derive from shutdownCtx so SIGTERM still aborts the scan cleanly.
		// CVE scans can take a while across many nodes, so cap the budget
		// generously rather than relying on the scheduler's per-tick deadline.
		scanCtx, cancel := context.WithTimeout(o.shutdownCtx, 30*time.Minute)
		defer cancel()

		// Skip if a scan is already running/pending for this cluster — the
		// existing scan will capture the post-upgrade state.
		if latest, err := o.queries.GetLatestCVEScan(scanCtx, job.ClusterID); err == nil {
			if latest.Status == "running" || latest.Status == "pending" {
				o.logger.Info("post-upgrade CVE scan skipped — scan already in progress",
					"job_id", job.ID, "cluster_id", job.ClusterID, "existing_scan_id", latest.ID)
				return
			}
		}

		o.logger.Info("triggering post-upgrade CVE scan",
			"job_id", job.ID, "cluster_id", job.ClusterID)
		scanID, err := o.cveScanner.ScanCluster(scanCtx, job.ClusterID)
		if err != nil {
			o.logger.Error("post-upgrade CVE scan failed",
				"job_id", job.ID, "cluster_id", job.ClusterID, "error", err)
			return
		}
		o.logger.Info("post-upgrade CVE scan completed",
			"job_id", job.ID, "cluster_id", job.ClusterID, "scan_id", scanID)
	}()
}

// restoreHAStates re-enables HA rules that were temporarily disabled before drain.
func (o *Orchestrator) restoreHAStates(ctx context.Context, client *proxmox.Client, node db.RollingUpdateNode) {
	var disabledRules []DisabledHARule
	if err := json.Unmarshal(node.DisabledHaRules, &disabledRules); err == nil {
		for _, rule := range disabledRules {
			o.logger.Info("re-enabling HA rule after update",
				"rule", rule.Rule, "type", rule.Type, "node", node.NodeName)
			if err := client.SetHARuleDisabled(ctx, rule.Rule, rule.Type, false); err != nil {
				o.logger.Warn("failed to re-enable HA rule",
					"rule", rule.Rule, "type", rule.Type, "error", err)
			}
		}
	}
}

func (o *Orchestrator) failNode(ctx context.Context, job db.RollingUpdateJob, node db.RollingUpdateNode, reason string) {
	o.logger.Error("rolling update node failed", "node", node.NodeName, "reason", reason)
	dbCtx, cancel := cleanupCtxFor(ctx)
	defer cancel()
	_ = o.queries.FailRollingUpdateNode(dbCtx, db.FailRollingUpdateNodeParams{
		ID:            node.ID,
		FailureReason: reason,
	})
	o.publishEvent(dbCtx, job.ClusterID, job.ID, "node_failed")
	o.failJob(dbCtx, job, fmt.Sprintf("node %s failed: %s", node.NodeName, reason))
}

func (o *Orchestrator) failJob(ctx context.Context, job db.RollingUpdateJob, reason string) {
	o.logger.Error("rolling update job failed", "job_id", job.ID, "reason", reason)
	dbCtx, cancel := cleanupCtxFor(ctx)
	defer cancel()
	_ = o.queries.FailRollingUpdateJob(dbCtx, db.FailRollingUpdateJobParams{
		ID:            job.ID,
		FailureReason: reason,
	})
	o.publishEvent(dbCtx, job.ClusterID, job.ID, "failed")
	o.auditLog(dbCtx, job.ClusterID, job.ID, "rolling_update_failed", map[string]string{"reason": reason})
	o.sendJobNotification(dbCtx, job, "failed", fmt.Sprintf("Rolling update failed: %s", reason))
	// Re-enable DRS + native CRS if we paused them at the start, and any HA
	// rules still disabled on nodes the failure abandoned mid-flight (normal
	// completion restores them per node; this path never reached it).
	o.restoreDRS(dbCtx, job)
	o.restoreNativeCRS(dbCtx, job)
	o.restoreAllNodeHAStates(dbCtx, job)
}

// CleanupCancelledJob releases everything a cancelled job may still hold:
// the DRS pause, the native CRS pause, and HA rules disabled for in-flight
// nodes. Cancelled jobs drop out of the running-jobs tick, so without an
// explicit cleanup these stay leaked until another job on the same cluster
// happens to complete — or forever.
func (o *Orchestrator) CleanupCancelledJob(ctx context.Context, jobID uuid.UUID) {
	job, err := o.queries.GetRollingUpdateJob(ctx, jobID)
	if err != nil {
		o.logger.Warn("rolling: cancelled-job cleanup could not load job",
			"job_id", jobID, "error", err)
		return
	}
	o.restoreDRS(ctx, job)
	o.restoreNativeCRS(ctx, job)
	o.restoreAllNodeHAStates(ctx, job)
	o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_cancel_cleanup", nil)
}

// restoreAllNodeHAStates re-enables every HA rule still recorded as disabled
// on any of the job's nodes. The client is built lazily so jobs that never
// disabled anything cost nothing.
func (o *Orchestrator) restoreAllNodeHAStates(ctx context.Context, job db.RollingUpdateJob) {
	nodes, err := o.queries.ListRollingUpdateNodes(ctx, job.ID)
	if err != nil {
		o.logger.Warn("rolling: failed to list nodes for HA-rule restore",
			"job_id", job.ID, "error", err)
		return
	}
	var client *proxmox.Client
	for _, node := range nodes {
		if len(node.DisabledHaRules) == 0 || string(node.DisabledHaRules) == "null" || string(node.DisabledHaRules) == "[]" {
			continue
		}
		if client == nil {
			c, clientErr := o.createClient(ctx, job.ClusterID)
			if clientErr != nil {
				o.logger.Error("rolling: failed to build client to restore HA rules — re-enable manually via Datacenter → HA",
					"job_id", job.ID, "cluster_id", job.ClusterID, "error", clientErr)
				o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_ha_restore_failed",
					map[string]string{"error": clientErr.Error()})
				return
			}
			client = c
		}
		o.restoreHAStates(ctx, client, node)
	}
}

// cleanupCtxFor returns (ctx, no-op cancel) when ctx is still alive, or a
// fresh 5-second timeout context derived from Background when the parent
// is already cancelled. Use it for the DB / event writes that record a
// failure outcome — without it, a SIGTERM cancellation would leave the
// row in 'running' / 'migrating' status forever because the DB write
// silently no-ops on a cancelled context.
func cleanupCtxFor(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// waitForGuestUnlocked polls the cluster until the guest's lock clears.
//
// HA-managed guests redirect the migrate call to the HA manager, which
// returns a fast "hamigrate" UPID that completes the moment the request is
// queued. The underlying qmigrate runs asynchronously and leaves the guest
// in lock=migrate state for the actual migration duration. Without this
// wait, the next orchestration step (next node's drain, restore, etc.)
// races the in-flight migration and fails with "VM is locked (migrate)".
//
// For non-HA guests the migration UPID itself tracks the work, so the
// post-task lock is normally clear on the first poll — making this safe
// to call unconditionally.
//
// Return values:
//   - "completed"   the guest is unlocked (or no longer visible)
//   - "timeout"     the 30-minute deadline elapsed while still locked
//   - "interrupted" the shutdown context was cancelled (graceful container
//     restart in Swarm/K8s). The lock may still be in place;
//     the next scheduler leader will resume polling via the
//     stall detector in advanceDraining / advanceRestoring.
func (o *Orchestrator) waitForGuestUnlocked(_ context.Context, client *proxmox.Client, guestType string, vmid int) string {
	pollCtx, cancel := context.WithTimeout(o.shutdownCtx, 30*time.Minute)
	defer cancel()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	matchesGuest := func(r proxmox.ClusterResource) bool {
		if r.VMID != vmid {
			return false
		}
		switch guestType {
		case "qemu":
			return r.Type == "qemu"
		case "lxc":
			return r.Type == "lxc"
		}
		return false
	}

	var lastLock string
	for {
		select {
		case <-pollCtx.Done():
			// Discriminate: graceful shutdown (recoverable) vs real 30-min
			// deadline. shutdownCtx cancellation propagates into pollCtx,
			// so check it first.
			if o.shutdownCtx.Err() != nil {
				o.logger.Info("waitForGuestUnlocked interrupted by shutdown",
					"vmid", vmid, "type", guestType, "lock", lastLock)
				return "interrupted"
			}
			if lastLock == "" {
				return "completed"
			}
			o.logger.Warn("guest still locked at deadline",
				"vmid", vmid, "type", guestType, "lock", lastLock)
			return "timeout"
		case <-ticker.C:
			resources, err := client.GetClusterResources(pollCtx, "vm")
			if err != nil {
				continue
			}
			found := false
			for _, r := range resources {
				if !matchesGuest(r) {
					continue
				}
				found = true
				lastLock = r.Lock
				if r.Lock == "" {
					return "completed"
				}
				break
			}
			if !found {
				// Guest is no longer visible (deleted/destroyed) — treat
				// as settled so we don't loop forever.
				return "completed"
			}
		}
	}
}

// waitForTask polls a Proxmox task UPID until it reaches a terminal state.
//
// Return values:
//   - "completed"   the task finished with a successful exit status
//   - "failed"      the task finished with a non-OK exit status
//   - "timeout"     the 30-minute deadline elapsed before the task stopped
//   - "interrupted" the shutdown context was cancelled (graceful container
//     restart in Swarm/K8s) while the task was still running.
//     The task remains in flight on Proxmox; callers must NOT
//     mark the rolling-update node as failed — the next
//     scheduler leader will resume polling via the stall
//     detector in advanceDraining / advanceRestoring (which
//     re-checks guest state and re-attempts only what's still
//     pending).
func (o *Orchestrator) waitForTask(_ context.Context, client *proxmox.Client, node string, upid string) string {
	// Detach from the scheduler tick so a tick rollover doesn't abandon
	// in-flight Proxmox tasks, but root in shutdownCtx so SIGTERM cancels
	// the poll loop. The final-check block below uses context.Background()
	// on purpose so the outcome can still be recorded during graceful
	// shutdown instead of orphaning the task.
	pollCtx, cancel := context.WithTimeout(o.shutdownCtx, 30*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-pollCtx.Done():
			// If shutdownCtx is cancelled, this is a graceful container
			// shutdown (Swarm reschedule / K8s rolling restart) — the task
			// on Proxmox is still progressing. Return "interrupted" so the
			// caller can exit cleanly without marking the node failed.
			// The next scheduler leader will resume via the stall detector.
			if o.shutdownCtx.Err() != nil {
				o.logger.Info("waitForTask interrupted by shutdown — task may still be running on Proxmox",
					"node", node, "upid", upid)
				return "interrupted"
			}
			// Real 30-minute deadline — try a final status read on a fresh
			// Background-derived ctx so the outcome can still be recorded.
			finalCtx, finalCancel := context.WithTimeout(context.Background(), 10*time.Second)
			ts, err := client.GetTaskStatus(finalCtx, node, upid)
			finalCancel()
			if err == nil && ts.Status == "stopped" {
				if proxmox.TaskSucceeded(ts.ExitStatus) {
					return "completed"
				}
				return "failed"
			}
			return "timeout"
		case <-ticker.C:
			ts, err := client.GetTaskStatus(pollCtx, node, upid)
			if err != nil {
				continue
			}
			if ts.Status == "stopped" {
				if proxmox.TaskSucceeded(ts.ExitStatus) {
					return "completed"
				}
				return "failed"
			}
		}
	}
}

func (o *Orchestrator) createClient(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, error) {
	// The cache covers the primary-URL happy path. If it returns
	// successfully and a quick connectivity probe succeeds, we keep the
	// cached instance; otherwise we fall through to the failover-URL
	// loop below, which builds throwaway clients pointed at alternate
	// node addresses (those clients are not cached because they're
	// keyed by node-address, not cluster).
	if o.cache != nil {
		if client, err := o.cache.Get(ctx, clusterID); err == nil {
			if _, testErr := client.GetNodes(ctx); testErr == nil {
				return client, nil
			}
			o.logger.Warn("rolling: cached primary URL unreachable, trying failover nodes",
				"cluster_id", clusterID)
		} else {
			o.logger.Warn("rolling: proxmox cache get failed, building per-call",
				"cluster_id", clusterID, "error", err)
		}
	}

	cluster, err := o.queries.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("get cluster %s: %w", clusterID, err)
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, o.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}

	// Try the primary URL first.
	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        60 * time.Second,
	})
	if err == nil {
		// Quick connectivity check.
		_, testErr := client.GetNodes(ctx)
		if testErr == nil {
			return client, nil
		}
		// Only a connectivity failure warrants failover — an auth/permission
		// error would hit every member identically (same token), so return the
		// primary client and let the caller surface it.
		if !errors.Is(testErr, proxmox.ErrConnectionFailed) {
			return client, nil
		}
		o.logger.Warn("primary API URL unreachable, trying failover nodes",
			"cluster_id", clusterID, "url", cluster.ApiUrl, "error", testErr)
	}

	// Failover: try other node endpoints. Each cluster member presents its own
	// certificate, so every alternate is pinned to that node's own fingerprint
	// rather than the cluster's primary one (which only matches the api_url
	// node and would fail TLS verification everywhere else).
	endpoints, _ := o.queries.ListNodeEndpoints(ctx, clusterID)
	for _, t := range failoverTargets(cluster, tokenSecret, endpoints) {
		failClient, failErr := proxmox.NewClient(t.config)
		if failErr != nil {
			continue
		}
		if _, testErr := failClient.GetNodes(ctx); testErr == nil {
			o.logger.Info("failover to alternate node succeeded",
				"cluster_id", clusterID, "node", t.name, "url", t.config.BaseURL)
			return failClient, nil
		}
	}

	// If we got a client from primary but connectivity failed, return it anyway
	// (caller will get errors on actual operations).
	if client != nil {
		return client, nil
	}
	return nil, fmt.Errorf("create client: %w", err)
}

// buildFailoverClients returns clients pointing at all known cluster
// endpoints other than the primary. Used by callWithRetryFailover when the
// primary client's host has gone unreachable mid-tick.
func (o *Orchestrator) buildFailoverClients(ctx context.Context, clusterID uuid.UUID) []*proxmox.Client {
	cluster, err := o.queries.GetCluster(ctx, clusterID)
	if err != nil {
		return nil
	}
	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, o.encryptionKey)
	if err != nil {
		return nil
	}
	endpoints, _ := o.queries.ListNodeEndpoints(ctx, clusterID)
	var clients []*proxmox.Client
	for _, t := range failoverTargets(cluster, tokenSecret, endpoints) {
		c, cerr := proxmox.NewClient(t.config)
		if cerr != nil {
			continue
		}
		clients = append(clients, c)
	}
	return clients
}

// failoverTarget is an alternate cluster endpoint to try when the primary
// api_url node is unreachable, paired with the node name for logging.
type failoverTarget struct {
	name   string
	config proxmox.ClientConfig
}

// failoverTargets builds the client configs for every known cluster member
// other than the primary api_url node. Each config reuses the primary's scheme
// and port but is pinned to that node's own TLS fingerprint, since cluster
// members present distinct certificates. Nodes with no recorded address or
// fingerprint, and the primary endpoint itself, are skipped — a missing
// fingerprint would otherwise downgrade TLS to system-CA verification.
func failoverTargets(cluster db.Cluster, tokenSecret string, endpoints []db.ListNodeEndpointsRow) []failoverTarget {
	primaryHost := proxmox.APIURLHost(cluster.ApiUrl)
	var targets []failoverTarget
	for _, ep := range endpoints {
		if ep.Address == "" || ep.Address == primaryHost || ep.SslFingerprint == "" {
			continue
		}
		targets = append(targets, failoverTarget{
			name: ep.Name,
			config: proxmox.ClientConfig{
				BaseURL:        proxmox.FailoverBaseURL(cluster.ApiUrl, ep.Address),
				TokenID:        cluster.TokenID,
				TokenSecret:    tokenSecret,
				TLSFingerprint: ep.SslFingerprint,
				Timeout:        60 * time.Second,
			},
		})
	}
	return targets
}

// callWithRetryFailover invokes fn against the primary client. On
// proxmox.ErrConnectionFailed it retries with brief backoff, then iterates
// fallback endpoints. Non-connection errors are returned immediately. This
// covers two cases the per-tick createClient failover misses: transient
// blips on the primary, and the primary going offline mid-tick.
func (o *Orchestrator) callWithRetryFailover(
	ctx context.Context,
	clusterID uuid.UUID,
	primary *proxmox.Client,
	fn func(*proxmox.Client) error,
) error {
	backoffs := []time.Duration{0, 2 * time.Second, 5 * time.Second}
	var err error
	for _, delay := range backoffs {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		err = fn(primary)
		if err == nil {
			return nil
		}
		if !errors.Is(err, proxmox.ErrConnectionFailed) {
			return err
		}
	}

	for _, fb := range o.buildFailoverClients(ctx, clusterID) {
		fbErr := fn(fb)
		if fbErr == nil {
			o.logger.Warn("primary endpoint failed mid-call, succeeded via failover",
				"cluster_id", clusterID)
			return nil
		}
		if !errors.Is(fbErr, proxmox.ErrConnectionFailed) {
			return fbErr
		}
	}
	return err
}

func (o *Orchestrator) publishEvent(ctx context.Context, clusterID uuid.UUID, jobID uuid.UUID, action string) {
	if o.eventPub != nil {
		o.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindRollingUpdate, "rolling_update", jobID.String(), action)
	}
}

func (o *Orchestrator) auditLog(ctx context.Context, clusterID uuid.UUID, jobID uuid.UUID, action string, extra interface{}) {
	var details json.RawMessage
	if extra != nil {
		details, _ = json.Marshal(extra)
	} else {
		details = json.RawMessage(`{}`)
	}

	err := o.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: clusterID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: "rolling_update",
		ResourceID:   jobID.String(),
		Action:       action,
		Details:      details,
	})
	if err != nil {
		o.logger.Warn("failed to insert rolling update audit log", "action", action, "error", err)
	}
}
