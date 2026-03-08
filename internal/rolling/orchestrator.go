// Package rolling implements the rolling update orchestrator for Proxmox nodes.
package rolling

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/drs"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/notifications"
	"github.com/proxdash/proxdash/internal/proxmox"
	sshpkg "github.com/proxdash/proxdash/internal/ssh"
)

// SystemUserID is the well-known UUID for automated system operations.
var SystemUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// Orchestrator drives the rolling update state machine on a scheduler tick.
type Orchestrator struct {
	queries        *db.Queries
	encryptionKey  string
	logger         *slog.Logger
	eventPub       *events.Publisher
	notifyRegistry *notifications.Registry
}

// NewOrchestrator creates a new rolling update orchestrator.
func NewOrchestrator(queries *db.Queries, encryptionKey string, logger *slog.Logger, eventPub *events.Publisher, notifyRegistry *notifications.Registry) *Orchestrator {
	return &Orchestrator{
		queries:        queries,
		encryptionKey:  encryptionKey,
		logger:         logger,
		eventPub:       eventPub,
		notifyRegistry: notifyRegistry,
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
		}
		// Re-enable DRS if we disabled it at the start.
		o.restoreDRS(ctx, job)
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
	nodeWorkloads := buildNodeWorkloads(ctx, client, clusterNodes)

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
				o.waitForTask(ctx, client, node.NodeName, upid)
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
		if status != "completed" {
			o.failNode(ctx, job, node, fmt.Sprintf("migration of %s %d failed (status: %s)", guest.Type, guest.VMID, status))
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

func (o *Orchestrator) advanceUpgrading(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode) {
	// If upgrade already started, check for timeout (30 minutes).
	if node.UpgradeStartedAt.Valid {
		if time.Since(node.UpgradeStartedAt.Time) > 30*time.Minute {
			o.failNode(ctx, job, node, "automated upgrade timed out after 30 minutes")
		}
		// Otherwise still running — wait for next tick.
		return
	}

	// First time seeing this node in 'upgrading' — kick off the SSH upgrade.
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
	sshHost := node.NodeName
	nodeAddr, addrErr := o.queries.GetNodeAddressByName(ctx, db.GetNodeAddressByNameParams{
		ClusterID: job.ClusterID,
		Name:      node.NodeName,
	})
	if addrErr == nil && nodeAddr != "" {
		sshHost = nodeAddr
	}

	sshCfg := sshpkg.Config{
		Host:       sshHost,
		Port:       int(sshCreds.Port),
		Username:   sshCreds.Username,
		Password:   password,
		PrivateKey: privateKey,
	}

	_ = o.queries.SetNodeUpgradeStarted(ctx, node.ID)
	o.publishEvent(ctx, job.ClusterID, job.ID, "node_upgrade_started")
	o.logger.Info("starting automated apt dist-upgrade via SSH", "node", node.NodeName)

	// Run apt dist-upgrade in a goroutine so we don't block the tick.
	// Use a detached context with a 25-minute timeout since this outlives the scheduler tick.
	upgradeCtx, upgradeCancel := context.WithTimeout(context.Background(), 25*time.Minute) //nolint:gosec // intentionally detached from request scope
	go o.runSSHUpgrade(upgradeCtx, upgradeCancel, job, node, client, sshCfg)
}

func (o *Orchestrator) runSSHUpgrade(ctx context.Context, cancel context.CancelFunc, job db.RollingUpdateJob, node db.RollingUpdateNode, client *proxmox.Client, sshCfg sshpkg.Config) {
	defer cancel()

	// Build package exclude args.
	var excludeArgs string
	if len(job.PackageExcludes) > 0 {
		for _, pkg := range job.PackageExcludes {
			excludeArgs += fmt.Sprintf(" --exclude %s", pkg)
		}
	}

	// The command: update index, then dist-upgrade.
	// DEBIAN_FRONTEND=noninteractive prevents any interactive prompts.
	// -o Dpkg::Options prevents dpkg config file prompts.
	cmd := fmt.Sprintf(
		"export DEBIAN_FRONTEND=noninteractive && "+
			"apt-get update && "+
			"apt-get dist-upgrade -y"+
			" -o Dpkg::Options::=--force-confdef"+
			" -o Dpkg::Options::=--force-confold"+
			"%s 2>&1",
		excludeArgs,
	)

	result, err := sshpkg.Execute(ctx, sshCfg, cmd)
	if err != nil {
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
		o.failNode(ctx, job, node, fmt.Sprintf("apt dist-upgrade exited with code %d: %s", result.ExitCode, result.Stderr))
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

func (o *Orchestrator) advanceDraining(ctx context.Context, _ *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode) {
	// Check if drain has been going on too long (1 hour timeout).
	if node.DrainStartedAt.Valid && time.Since(node.DrainStartedAt.Time) > time.Hour {
		o.failNode(ctx, job, node, "drain timed out after 1 hour")
	}
	// Otherwise, drain is handled synchronously in startNode — if we see
	// "draining" here it means a previous tick started it and it's still in
	// progress. The startNode goroutine will advance the state when done.
}

func (o *Orchestrator) advanceRebooting(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode) {
	// Check if reboot timed out (10 minutes).
	if node.RebootStartedAt.Valid && time.Since(node.RebootStartedAt.Time) > 10*time.Minute {
		o.failNode(ctx, job, node, "reboot timed out after 10 minutes")
		return
	}

	// Try to get node status — if it's online, reboot is done.
	status, err := client.GetNodeStatus(ctx, node.NodeName)
	if err != nil {
		// Connection errors are expected while node is rebooting.
		return
	}

	if status.Uptime > 0 {
		if err := o.queries.SetNodeRebootCompleted(ctx, node.ID); err != nil {
			o.logger.Error("failed to set node reboot completed", "node_id", node.ID, "error", err)
			return
		}
		if err := o.queries.SetNodeHealthCheckPassed(ctx, node.ID); err != nil {
			o.logger.Error("failed to set node health check passed", "node_id", node.ID, "error", err)
			return
		}
		o.publishEvent(ctx, job.ClusterID, job.ID, "node_health_check_passed")
		o.logger.Info("node back online after reboot", "node", node.NodeName)
	}
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
	clusterNodes, err := client.GetNodes(ctx)
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
		if status != "completed" {
			o.logger.Warn("guest restore migration failed",
				"vmid", guest.VMID, "status", status)
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

func (o *Orchestrator) advanceRestoring(ctx context.Context, _ *proxmox.Client, job db.RollingUpdateJob, node db.RollingUpdateNode) {
	// Restore is handled synchronously in advanceHealthCheck.
	// If we see "restoring" here, a previous tick started it.
	// Check for timeout (30 minutes).
	if node.RestoreStartedAt.Valid && time.Since(node.RestoreStartedAt.Time) > 30*time.Minute {
		// Don't fail the job for restore timeout — just complete the node.
		o.logger.Warn("guest restore timed out, marking node completed", "node", node.NodeName)
		_ = o.queries.SetNodeRestoreCompleted(ctx, node.ID)
		o.publishEvent(ctx, job.ClusterID, job.ID, "node_completed")
	}
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
		FiredAt:      time.Now().UTC().Format(time.RFC3339),
	}

	go func() {
		sendCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	_ = o.queries.FailRollingUpdateNode(ctx, db.FailRollingUpdateNodeParams{
		ID:            node.ID,
		FailureReason: reason,
	})
	o.publishEvent(ctx, job.ClusterID, job.ID, "node_failed")
	o.failJob(ctx, job, fmt.Sprintf("node %s failed: %s", node.NodeName, reason))
}

func (o *Orchestrator) failJob(ctx context.Context, job db.RollingUpdateJob, reason string) {
	o.logger.Error("rolling update job failed", "job_id", job.ID, "reason", reason)
	_ = o.queries.FailRollingUpdateJob(ctx, db.FailRollingUpdateJobParams{
		ID:            job.ID,
		FailureReason: reason,
	})
	o.publishEvent(ctx, job.ClusterID, job.ID, "failed")
	o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_failed", map[string]string{"reason": reason})
	o.sendJobNotification(ctx, job, "failed", fmt.Sprintf("Rolling update failed: %s", reason))
	// Re-enable DRS if we disabled it at the start.
	o.restoreDRS(ctx, job)
}

func (o *Orchestrator) waitForTask(_ context.Context, client *proxmox.Client, node string, upid string) string {
	pollCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-pollCtx.Done():
			finalCtx, finalCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer finalCancel()
			ts, err := client.GetTaskStatus(finalCtx, node, upid)
			if err == nil && ts.Status == "stopped" {
				if taskSucceeded(ts.ExitStatus) {
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
				if taskSucceeded(ts.ExitStatus) {
					return "completed"
				}
				return "failed"
			}
		}
	}
}

func taskSucceeded(exitStatus string) bool {
	upper := strings.ToUpper(strings.TrimSpace(exitStatus))
	return upper == "" || upper == "OK" || strings.HasPrefix(upper, "OK ") || upper == "WARNINGS"
}

func (o *Orchestrator) createClient(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, error) {
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
		o.logger.Warn("primary API URL unreachable, trying failover nodes",
			"cluster_id", clusterID, "url", cluster.ApiUrl, "error", testErr)
	}

	// Failover: try other node addresses.
	nodeAddrs, _ := o.queries.ListNodeAddresses(ctx, clusterID)
	for _, na := range nodeAddrs {
		failoverURL := fmt.Sprintf("https://%s:8006", na.Address)
		if failoverURL == cluster.ApiUrl {
			continue
		}
		failClient, failErr := proxmox.NewClient(proxmox.ClientConfig{
			BaseURL:        failoverURL,
			TokenID:        cluster.TokenID,
			TokenSecret:    tokenSecret,
			TLSFingerprint: cluster.TlsFingerprint,
			Timeout:        60 * time.Second,
		})
		if failErr != nil {
			continue
		}
		_, testErr := failClient.GetNodes(ctx)
		if testErr == nil {
			o.logger.Info("failover to alternate node succeeded",
				"cluster_id", clusterID, "node", na.Name, "url", failoverURL)
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
		UserID:       SystemUserID,
		ResourceType: "rolling_update",
		ResourceID:   jobID.String(),
		Action:       action,
		Details:      details,
	})
	if err != nil {
		o.logger.Warn("failed to insert rolling update audit log", "action", action, "error", err)
	}
}

