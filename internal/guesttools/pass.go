package guesttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// stagedUpdateMaxAge bounds how long a guest may sit staged before the row is
// abandoned.
//
// Generous on purpose: "staged" means "waiting for someone to reboot this
// guest", and a server that reboots monthly is doing nothing wrong. This exists
// so a guest that is decommissioned, or whose task was removed by hand, does
// not hold a concurrency slot forever.
const stagedUpdateMaxAge = 30 * 24 * time.Hour

// runningUpdateMaxAge bounds a guest whose task was actually started. The
// installer takes minutes; a few hours means it is not coming back.
const runningUpdateMaxAge = 6 * time.Hour

// bootInstallGrace is how long after a boot a guest is left out of the
// automatic-withdrawal path, in seconds to match the uptime Proxmox reports.
//
// Comfortably clear of the updater task's PT1M trigger delay plus the guest
// round trips a withdrawal takes, so the decision cannot be overtaken by the
// install it is deciding about.
const bootInstallGrace = 5 * 60

// RunPass detects guest tools versions across every cluster with the feature
// on, and stages updates for guests that are behind.
func (e *Engine) RunPass(ctx context.Context) error {
	configs, err := e.queries.ListActiveGuestToolsConfigs(ctx)
	if err != nil {
		return fmt.Errorf("list guest tools configs: %w", err)
	}
	for _, cfg := range configs {
		if err := e.runClusterPass(ctx, cfg); err != nil {
			e.logger.Warn("guest tools: cluster pass failed", "cluster_id", cfg.ClusterID, "error", err)
		}
	}
	return nil
}

func (e *Engine) runClusterPass(ctx context.Context, cfg db.GuestToolsConfig) error {
	client, err := e.CreateClient(ctx, cfg.ClusterID)
	if err != nil {
		return fmt.Errorf("proxmox client: %w", err)
	}
	fleet, err := e.queries.ListGuestToolsFleet(ctx, cfg.ClusterID)
	if err != nil {
		return fmt.Errorf("list fleet: %w", err)
	}
	if len(fleet) == 0 {
		return nil
	}

	// Keep state rows in step with reality. Non-nil, possibly-empty: pgx
	// encodes a nil slice as SQL NULL, and `NOT (x = ANY(NULL))` is NULL, so a
	// nil list would silently delete nothing.
	vmids := make([]int32, 0, len(fleet))
	for _, g := range fleet {
		vmids = append(vmids, g.Vmid)
	}
	// Only sweep when nothing is in flight for this cluster.
	//
	// The collector deletes and re-inserts vms rows as it churns, so a pass
	// landing in that window sees a guest as "vanished" when it is merely being
	// rewritten. Dropping its row then would strand a staged update: the
	// scheduled task and swapped CD-ROM stay in the guest with nothing left to
	// record the outcome or restore the media. Deferring the sweep costs a few
	// stale rows until the fleet settles; getting it wrong costs a guest.
	if inFlightBefore, err := e.queries.CountGuestToolsInFlightForCluster(ctx, cfg.ClusterID); err == nil && inFlightBefore == 0 {
		if err := e.queries.DeleteGuestToolsStateForVanishedGuests(ctx, db.DeleteGuestToolsStateForVanishedGuestsParams{
			ClusterID: cfg.ClusterID,
			Vmids:     vmids,
		}); err != nil {
			e.logger.Warn("guest tools: prune vanished guest state failed", "cluster_id", cfg.ClusterID, "error", err)
		}
	}

	inFlight, err := e.queries.CountGuestToolsInFlightForCluster(ctx, cfg.ClusterID)
	if err != nil {
		return fmt.Errorf("count in-flight: %w", err)
	}
	budget := int64(cfg.MaxConcurrent) - inFlight

	storage := ""
	if vwCfg, err := e.queries.GetVirtioWinConfig(ctx, cfg.ClusterID); err == nil {
		storage = vwCfg.Storage
	}

	for _, guest := range fleet {
		if guest.Template || !strings.EqualFold(guest.Status, "running") {
			continue
		}
		vmid := int(guest.Vmid)

		detection, err := e.Detect(ctx, client, cfg.ClusterID, guest.NodeName, vmid, guest.Uptime)
		if err != nil {
			// An agent-less or busy guest is expected, not exceptional.
			e.logger.Debug("guest tools: detection failed", "vmid", vmid, "error", err)
			continue
		}

		if cfg.Mode != "staged" {
			continue // report mode: detection only, no writes to any guest
		}
		if budget <= 0 {
			continue
		}
		// Skip only work that is genuinely in flight. Terminal stages must NOT
		// block a new attempt: a guest that updated successfully once would
		// otherwise be excluded from every future release forever, and one
		// transient failure would park a guest permanently.
		if guest.Stage.Valid && isInFlightStage(guest.Stage.String) {
			continue
		}
		if guest.Excluded.Valid && guest.Excluded.Bool {
			continue
		}

		policy := e.policyFor(ctx, cfg.ClusterID, guest.Vmid)
		target, err := e.ResolveTarget(ctx, cfg.ClusterID, cfg, policy)
		if err != nil {
			e.logger.Debug("guest tools: no target for guest", "vmid", vmid, "error", err)
			continue
		}
		if !NeedsUpdate(detection.InstalledVersion(), target.Version) {
			continue
		}
		if storage == "" {
			e.logger.Warn("guest tools: cannot stage, no virtio-win storage configured for this cluster",
				"cluster_id", cfg.ClusterID, "vmid", vmid)
			continue
		}

		if _, err := e.Stage(ctx, client, cfg.ClusterID, cfg, guest.NodeName, vmid, target, storage, false); err != nil {
			// A target whose ISO has not been downloaded yet is "not yet", not
			// "this guest failed". The catalog flips to a new stable release
			// the moment upstream publishes one, while the ~840 MiB fetch that
			// follows takes minutes — so a pass landing in that window would
			// otherwise paint every guest in the cluster red for a condition
			// none of them has anything to do with, and that nothing about the
			// guest can fix. Left alone, they stage on a later pass.
			if errors.Is(err, ErrISOUnavailable) {
				e.logger.Info("guest tools: target ISO is not on storage yet, deferring",
					"cluster_id", cfg.ClusterID, "vmid", vmid, "version", target.Version)
				continue
			}
			e.logger.Warn("guest tools: staging failed", "vmid", vmid, "error", err)
			e.markFailed(ctx, cfg.ClusterID, guest.Vmid, err.Error())
			continue
		}
		budget--
		e.logger.Info("guest tools: update staged for next boot",
			"cluster_id", cfg.ClusterID, "vmid", vmid, "guest", guest.Name,
			"from", detection.InstalledVersion(), "to", target.Version)
	}
	return nil
}

// isInFlightStage reports whether a stage represents work Nexara is still
// waiting on. succeeded/failed/idle are all "nothing in progress".
func isInFlightStage(stage string) bool {
	switch stage {
	case "staging", "staged", "running":
		return true
	default:
		return false
	}
}

func (e *Engine) policyFor(ctx context.Context, clusterID uuid.UUID, vmid int32) *db.GuestToolsPolicy {
	policy, err := e.queries.GetGuestToolsPolicy(ctx, db.GetGuestToolsPolicyParams{
		ClusterID: clusterID, Vmid: vmid,
	})
	if err != nil {
		return nil
	}
	return &policy
}

func (e *Engine) markFailed(ctx context.Context, clusterID uuid.UUID, vmid int32, msg string) {
	if err := e.queries.FinishGuestToolsUpdate(ctx, db.FinishGuestToolsUpdateParams{
		ClusterID: clusterID, Vmid: vmid, Stage: "failed", LastError: msg,
		RebootRequired: false, CdromRestored: true,
	}); err != nil {
		e.logger.Warn("guest tools: record failure", "vmid", vmid, "error", err)
	}
}

// Reconcile advances guests with an update in flight: reads the result file the
// in-guest updater leaves behind, restores borrowed media, and records the
// outcome.
//
// This is the durable owner of the staging state machine. Nothing in the guest
// reports back, and the install may happen days after staging across a reboot
// Nexara neither triggered nor observed, so the only way to learn the outcome
// is to keep looking.
func (e *Engine) Reconcile(ctx context.Context) error {
	rows, err := e.queries.ListGuestToolsInFlight(ctx)
	if err != nil {
		return fmt.Errorf("list in-flight guest tools updates: %w", err)
	}
	for _, row := range rows {
		e.reconcileOne(ctx, row)
	}
	e.retryPendingCDROMRestores(ctx)
	return nil
}

// retryPendingCDROMRestores puts back media that a finished update failed to
// restore at the time.
//
// Without this a single transient Proxmox error at exactly the wrong moment
// would leave the virtio-win ISO attached to a guest permanently, which also
// re-pins that ISO against pruning.
func (e *Engine) retryPendingCDROMRestores(ctx context.Context) {
	rows, err := e.queries.ListGuestToolsPendingCDROMRestore(ctx)
	if err != nil {
		e.logger.Warn("guest tools: list pending CD-ROM restores failed", "error", err)
		return
	}
	for _, state := range rows {
		vmid := int(state.Vmid)
		vm, err := e.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
			ClusterID: state.ClusterID, Vmid: state.Vmid,
		})
		if err != nil {
			// Guest is gone; nothing left to restore it on.
			if errors.Is(err, pgx.ErrNoRows) {
				e.clearCDROMRestore(ctx, state)
			}
			continue
		}
		node, err := e.queries.GetNode(ctx, vm.NodeID)
		if err != nil {
			continue
		}
		client, err := e.CreateClient(ctx, state.ClusterID)
		if err != nil {
			continue
		}
		if err := e.RestoreCDROM(ctx, client, node.Name, vmid, state.PriorCdromKey, state.PriorCdromValue); err != nil {
			e.logger.Warn("guest tools: retry of CD-ROM restore failed", "vmid", vmid, "error", err)
			continue
		}
		e.logger.Info("guest tools: CD-ROM restored on retry",
			"cluster_id", state.ClusterID, "vmid", vmid, "device", state.PriorCdromKey)
		e.clearCDROMRestore(ctx, state)
	}
}

func (e *Engine) clearCDROMRestore(ctx context.Context, state db.GuestToolsState) {
	if err := e.queries.ClearGuestToolsCDROMRestore(ctx, db.ClearGuestToolsCDROMRestoreParams{
		ClusterID: state.ClusterID, Vmid: state.Vmid,
	}); err != nil {
		e.logger.Warn("guest tools: clear CD-ROM restore record failed", "vmid", state.Vmid, "error", err)
	}
}

func (e *Engine) reconcileOne(ctx context.Context, state db.GuestToolsState) {
	vmid := int(state.Vmid)

	vm, err := e.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
		ClusterID: state.ClusterID, Vmid: state.Vmid,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Guest is gone; the vanished-guest sweep will drop the row.
			return
		}
		e.logger.Warn("guest tools: reconcile could not load guest", "vmid", vmid, "error", err)
		return
	}
	node, err := e.queries.GetNode(ctx, vm.NodeID)
	if err != nil {
		e.logger.Warn("guest tools: reconcile could not load node", "vmid", vmid, "error", err)
		return
	}

	if e.expireIfStale(ctx, state) {
		return
	}

	client, err := e.CreateClient(ctx, state.ClusterID)
	if err != nil {
		e.logger.Warn("guest tools: reconcile client build failed", "vmid", vmid, "error", err)
		return
	}

	// A stopped guest still gets the superseded-staging check, ahead of the
	// running-guest gate below.
	//
	// It is the case that needs it most: a powered-off guest has not had the
	// chance to install yet, so it is exactly the one a bad release is still
	// ahead of. There is no agent to delete the scheduled task through, but
	// detaching the ISO is enough on its own, and UpdateVMConfigSync works fine
	// against a stopped guest.
	//
	// What the orphaned task then does at next boot is self-limiting: the
	// updater looks its media up by volume label, throws when nothing matches,
	// and its finally block still deletes the task and writes a failed result.
	// So it fires exactly once and removes itself. That result is never
	// mistaken for a real outcome because ListGuestToolsInFlight selects only
	// staging/staged/running — this row is idle by then, so the reconciler
	// never looks at it — and the file itself is cleared by the next Stage().
	if !strings.EqualFold(vm.Status, "running") {
		e.withdrawSuperseded(ctx, client, node.Name, state, false, vm.Uptime)
		return // nothing else to read from a stopped guest
	}

	result, err := e.ReadResult(ctx, client, node.Name, vmid)
	if err != nil {
		e.logger.Debug("guest tools: reading updater result failed", "vmid", vmid, "error", err)
		return
	}
	if result == nil {
		// Nothing has reported in, so a staged version the target no longer
		// names can still be withdrawn. Checked only on this branch: a guest
		// that rebooted and installed moments ago has an outcome to record, and
		// withdrawing instead would throw it away.
		if e.withdrawSuperseded(ctx, client, node.Name, state, true, vm.Uptime) {
			return
		}
		// No result yet. Track uptime so a reboot is visible in the UI even
		// before the install reports in.
		if vm.Uptime < state.LastUptime {
			e.logger.Info("guest tools: guest rebooted while an update was staged",
				"vmid", vmid, "cluster_id", state.ClusterID)
		}
		if err := e.queries.SetGuestToolsUptime(ctx, db.SetGuestToolsUptimeParams{
			ClusterID: state.ClusterID, Vmid: state.Vmid, LastUptime: vm.Uptime,
		}); err != nil {
			e.logger.Debug("guest tools: uptime update failed", "vmid", vmid, "error", err)
		}
		return
	}

	// A result from a previous run must not be mistaken for this one's. The
	// script stamps the version it installed, so a mismatch means the file is
	// stale and the current attempt has not finished.
	if result.Version != "" && result.Version != state.StagedVersion {
		e.logger.Debug("guest tools: ignoring a stale result file",
			"vmid", vmid, "result_version", result.Version, "staged_version", state.StagedVersion)
		return
	}

	// Track whether the media actually came back off. A failure here must NOT
	// clear the bookkeeping: the row is about to go terminal and the in-flight
	// sweep will never look at it again, so losing the record would strand the
	// ISO on the guest forever. retryPendingCDROMRestores picks it up instead.
	cdromRestored := true
	if err := e.RestoreCDROM(ctx, client, node.Name, vmid, state.PriorCdromKey, state.PriorCdromValue); err != nil {
		cdromRestored = false
		e.logger.Warn("guest tools: could not restore CD-ROM after update, will retry",
			"vmid", vmid, "error", err)
	}

	stage, message := "succeeded", result.Message
	if !result.Succeeded() {
		stage = "failed"
		if message == "" {
			message = fmt.Sprintf("installer exited with %d", result.ExitCode)
		}
	}
	// A reboot-required result is recorded on its own field rather than folded
	// into last_error. It is not a failure, but it is not finished either, and
	// an operator needs to be able to see the difference.
	if err := e.queries.FinishGuestToolsUpdate(ctx, db.FinishGuestToolsUpdateParams{
		ClusterID:      state.ClusterID,
		Vmid:           state.Vmid,
		Stage:          stage,
		LastError:      message,
		RebootRequired: result.RebootRequired,
		CdromRestored:  cdromRestored,
	}); err != nil {
		e.logger.Warn("guest tools: record update outcome failed", "vmid", vmid, "error", err)
		return
	}

	// The outcome is recorded, so the guest no longer needs the result file.
	// Leaving it would litter every updated guest and, worse, make a later
	// retry of the same version resolve from a stale file.
	if err := e.clearResultFile(ctx, client, node.Name, vmid); err != nil {
		// Warn, not Debug: a result file that outlives its update is what makes
		// the next attempt resolve from stale data, and Debug is invisible at
		// the default log level.
		e.logger.Warn("guest tools: could not remove the updater result file", "vmid", vmid, "error", err)
	}

	// Re-read the guest so the recorded version reflects what is actually
	// installed, rather than what the installer claimed.
	if _, err := e.Detect(ctx, client, state.ClusterID, node.Name, vmid, vm.Uptime); err != nil {
		e.logger.Debug("guest tools: post-update detection failed", "vmid", vmid, "error", err)
	}

	e.logger.Info("guest tools: update finished",
		"cluster_id", state.ClusterID, "vmid", vmid, "stage", stage,
		"version", result.Version, "installed", result.InstalledVersion,
		"reboot_required", result.RebootRequired, "message", message)
}

// withdrawSuperseded takes back a staged update whose version the target no
// longer names, reporting whether it did.
//
// This is what makes changing the target mean something for a guest that is
// already staged. A staged install fires at the guest's NEXT BOOT, which may be
// weeks away, and nothing else here revisits the decision: without this,
// lowering the pin to back out a bad release leaves every staged guest still
// armed with the release being backed out, and it lands anyway — days later, on
// a guest whose Target column has read the new version the whole time.
//
// The comparison is staged-vs-target, not installed-vs-target. A target moved
// sideways (0.1.302 -> 0.1.290) is just as superseded as one moved back, and
// both want the stale staging gone so the next pass can decide afresh.
//
// agentUp says whether the guest is running and reachable. When it is not, the
// ISO is still detached — that alone neuters the install, because the updater
// finds its media by volume label — but the in-guest task is left in place for
// want of anything to delete it with.
func (e *Engine) withdrawSuperseded(ctx context.Context, client *proxmox.Client, node string, state db.GuestToolsState, agentUp bool, uptime int64) bool {
	// Cheap pre-filter before the queries and guest round trips below. The rule
	// itself lives in supersededStaging and is applied in full further down.
	if state.Stage != "staged" || state.StagedVersion == "" {
		return false
	}

	// Leave a freshly booted guest alone.
	//
	// The task's boot trigger carries a PT1M delay, and the checks below take
	// several guest round trips at guestExecPollEvery apiece — so a guest that
	// reads as Ready at the start of this function can cross its trigger before
	// the ISO is detached at the end of it, which is the one outcome none of
	// this may produce. Waiting a few minutes costs a tick; the guest is picked
	// up on the next one, or its install completes and is recorded normally.
	//
	// Zero blocks too. On a running guest it means either "booted within the
	// last second" or "we have not got an uptime", and both are reasons to wait
	// rather than reasons to proceed — every other thing this function cannot
	// establish leaves the staging alone, and an uptime it cannot read is no
	// different.
	if agentUp && uptime < bootInstallGrace {
		e.logger.Debug("guest tools: guest booted too recently to withdraw safely",
			"vmid", state.Vmid, "uptime", uptime)
		return false
	}

	// Re-read the row. ListGuestToolsInFlight snapshots every in-flight guest at
	// the top of the pass, and each guest reconciled before this one may have
	// spent up to guestExecTimeout in the guest — so `state` can be minutes old.
	// Acting on it could tear down an update an operator started in between.
	fresh, err := e.queries.GetGuestToolsState(ctx, db.GetGuestToolsStateParams{
		ClusterID: state.ClusterID, Vmid: state.Vmid,
	})
	if err != nil {
		e.logger.Debug("guest tools: could not re-read state before withdrawing",
			"vmid", state.Vmid, "error", err)
		return false
	}
	state = fresh
	if state.Stage != "staged" || state.StagedVersion == "" {
		return false
	}

	target, err := e.resolveTargetForState(ctx, state)
	if err != nil {
		// "We cannot resolve a target" is not "the target changed". Withdrawing
		// on an unresolvable one would unstage the fleet the moment a pin named
		// a version the catalog has not caught up with.
		e.logger.Debug("guest tools: no target to compare a staged version against",
			"vmid", state.Vmid, "error", err)
		return false
	}
	if !supersededStaging(state.Stage, state.StagedVersion, target.Version) {
		return false
	}

	vmid := int(state.Vmid)

	// A boot-triggered install spends its ENTIRE runtime in stage 'staged' —
	// nothing moves it to 'running', which only the operator's run-now path
	// ever sets. So the stage tells us nothing about whether the installer is
	// going right now, and ReadResult returning nil covers both "has not
	// started" and "is running and has not written its result yet".
	//
	// Ask the guest instead. Pulling the ISO out from under a live installer
	// leaves half-swapped storage and network drivers, and the staged path
	// deliberately takes no snapshot to roll back to.
	if agentUp {
		switch presence, err := e.updaterTaskPresence(ctx, client, node, vmid); {
		case err != nil:
			e.logger.Debug("guest tools: could not read the updater task state, leaving the staging alone",
				"vmid", vmid, "error", err)
			return false
		case presence == taskRunning:
			e.logger.Info("guest tools: not withdrawing a superseded update, the installer is already running",
				"cluster_id", state.ClusterID, "vmid", vmid, "staged_version", state.StagedVersion)
			return false
		case presence == taskUnknown:
			// The guest could not be asked: its Task Scheduler service or CIM
			// provider is not answering. Withdrawing blind could pull the ISO
			// from under a live install, so such a guest is only ever cancelled
			// by hand — or expired by expireIfStale at stagedUpdateMaxAge.
			e.logger.Debug("guest tools: cannot determine the updater task state, leaving the staging alone",
				"cluster_id", state.ClusterID, "vmid", vmid)
			return false
		}
	}

	// CancelStaged's own task delete is best-effort and it returns nil even when
	// nothing was removed, so the deletion is done and VERIFIED here first. A
	// row marked withdrawn while its task is still armed is the exact failure
	// this whole function exists to prevent, and the audit row would assert the
	// opposite.
	taskRemoved := false
	if agentUp {
		if err := e.deleteUpdaterTask(ctx, client, node, vmid); err != nil {
			e.logger.Warn("guest tools: could not remove the updater task, leaving the staging in place",
				"cluster_id", state.ClusterID, "vmid", vmid, "error", err)
			return false
		}
		taskRemoved = true
	}

	// finishCancel, not CancelStaged: the task is already gone and verified, or
	// there is no agent to reach one through, so CancelStaged's own best-effort
	// delete would be a guest round trip that cannot accomplish anything.
	if err := e.finishCancel(ctx, client, state.ClusterID, node, vmid, state); err != nil {
		// Leave the row staged and try again next tick. Returning false matters:
		// a half-withdrawn guest must not be reported as withdrawn.
		e.logger.Warn("guest tools: could not withdraw a superseded staged update",
			"cluster_id", state.ClusterID, "vmid", vmid,
			"staged_version", state.StagedVersion, "target_version", target.Version, "error", err)
		return false
	}

	e.logger.Info("guest tools: withdrew a staged update the target no longer names",
		"cluster_id", state.ClusterID, "vmid", vmid,
		"staged_version", state.StagedVersion, "target_version", target.Version,
		"task_removed", taskRemoved)
	e.auditSupersededCancel(ctx, state, target.Version, taskRemoved)
	return true
}

// resolveTargetForState resolves the effective target for one guest_tools_state
// row, distinguishing "there is no pin" from "we could not read whether there
// is a pin".
//
// That distinction is the whole point of not reusing policyFor here. policyFor
// maps every error to nil, which reads identically to "this guest has no pin" —
// so a pool timeout on the per-guest lookup would resolve to the cluster or
// stable target and withdraw a correctly staged, pinned guest, with an audit row
// claiming the target had changed when it had not. A blip hits many rows at once.
func (e *Engine) resolveTargetForState(ctx context.Context, state db.GuestToolsState) (Target, error) {
	cfg, err := e.queries.GetGuestToolsConfig(ctx, state.ClusterID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return Target{}, fmt.Errorf("read guest tools config: %w", err)
		}
		// No cluster policy row: the target still resolves from the virtio-win
		// pin or upstream stable, exactly as the fleet view resolves it.
		cfg = db.GuestToolsConfig{ClusterID: state.ClusterID}
	}

	var policy *db.GuestToolsPolicy
	p, err := e.queries.GetGuestToolsPolicy(ctx, db.GetGuestToolsPolicyParams{
		ClusterID: state.ClusterID, Vmid: state.Vmid,
	})
	switch {
	case err == nil:
		policy = &p
	case errors.Is(err, pgx.ErrNoRows):
		// Genuinely no per-guest pin.
	default:
		return Target{}, fmt.Errorf("read guest tools policy: %w", err)
	}

	return e.ResolveTarget(ctx, state.ClusterID, cfg, policy)
}

// supersededStaging reports whether a staged update should be withdrawn because
// the target no longer names the version it would install.
//
// Split out from withdrawSuperseded so the rule is testable without a database
// or a Proxmox client, because every clause is one somebody will get wrong
// later:
//
//   - Only 'staged'. 'staging' is Stage() still mid-flight writing this very
//     row, and 'running' is an operator-started install already underway. Note
//     that 'staged' does NOT imply the installer is idle — a boot-triggered
//     install never leaves that stage — so the caller has to ask the guest
//     directly before acting on a true from here.
//   - An empty target withdraws nothing. Ending up here with no target means
//     resolution failed, and "we do not know" must never read as "it changed".
//   - Plain string inequality, deliberately, not a version comparison. Both
//     sides come from a catalog row's Version, so they are already the same
//     shape — and a target moved DOWN (0.1.302-1 -> 0.1.285-1, backing a bad
//     release out) is the case this exists for. Ordering them would miss it.
func supersededStaging(stage, stagedVersion, targetVersion string) bool {
	if stage != "staged" || stagedVersion == "" || targetVersion == "" {
		return false
	}
	return stagedVersion != targetVersion
}

// auditSupersededCancel records an automatic withdrawal in the audit log.
//
// Nobody pressed anything for this one, which is exactly why it needs a row: a
// staged update that vanishes between two glances at the fleet table is
// otherwise unexplainable, and "Nexara withdrew it because you moved the
// target" is the answer an operator has to be able to find. Its own action name
// rather than the manual guest_tools_cancel, so the two are tellable apart.
func (e *Engine) auditSupersededCancel(ctx context.Context, state db.GuestToolsState, targetVersion string, taskRemoved bool) {
	details, _ := json.Marshal(map[string]any{
		"vmid":           state.Vmid,
		"staged_version": state.StagedVersion,
		"target_version": targetVersion,
		"reason":         "the staged version is no longer the target for this guest",
		// False for a guest that was powered off: the ISO was detached, which
		// is enough to stop the install, but the in-guest task is still
		// registered and will fire (and fail to find its media) at next boot.
		// Recorded because it is the difference between "gone" and "defused".
		"task_removed": taskRemoved,
	})
	if err := e.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: state.ClusterID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: "vm",
		ResourceID:   strconv.Itoa(int(state.Vmid)),
		Action:       "guest_tools_auto_cancel",
		Details:      details,
	}); err != nil {
		e.logger.Warn("guest tools: audit of a superseded-staging withdrawal failed",
			"vmid", state.Vmid, "error", err)
	}
}

// expireIfStale abandons an update that has waited past its ceiling, freeing
// the concurrency slot it holds. Returns true when the row was expired.
func (e *Engine) expireIfStale(ctx context.Context, state db.GuestToolsState) bool {
	if !state.StagedAt.Valid {
		return false
	}
	age := time.Since(state.StagedAt.Time)
	limit := stagedUpdateMaxAge
	reason := fmt.Sprintf("no reboot within %s of staging", stagedUpdateMaxAge)
	if state.Stage == "running" {
		limit = runningUpdateMaxAge
		reason = fmt.Sprintf("the updater did not report a result within %s of starting", runningUpdateMaxAge)
	}
	if age <= limit {
		return false
	}
	e.markFailed(ctx, state.ClusterID, state.Vmid, reason)
	e.logger.Warn("guest tools: abandoning a stale update",
		"cluster_id", state.ClusterID, "vmid", state.Vmid, "stage", state.Stage, "reason", reason)
	return true
}

// StageOne stages (and optionally immediately runs) an update for a single
// guest, for the operator-initiated path. Eligibility is checked here so the
// API can explain a refusal rather than silently doing nothing.
func (e *Engine) StageOne(ctx context.Context, clusterID uuid.UUID, vmid int, runNow bool) (StageResult, error) {
	var out StageResult

	cfg, err := e.queries.GetGuestToolsConfig(ctx, clusterID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return out, fmt.Errorf("read guest tools config: %w", err)
		}
		// No policy row yet. An explicit per-guest action is its own
		// authorisation; it does not need the cluster feature switched on.
		cfg = db.GuestToolsConfig{ClusterID: clusterID, MaxConcurrent: 1}
	}

	vm, err := e.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
		ClusterID: clusterID, Vmid: safeconv.Int32(vmid),
	})
	if err != nil {
		return out, fmt.Errorf("%w: guest %d not found", ErrNotEligible, vmid)
	}
	if vm.Type != "qemu" {
		return out, fmt.Errorf("%w: guest %d is a container", ErrNotEligible, vmid)
	}
	if !IsWindowsOSType(vm.ConfigOstype, vm.Ostype) {
		return out, fmt.Errorf("%w: guest %d is not Windows", ErrNotEligible, vmid)
	}
	if vm.Template {
		return out, fmt.Errorf("%w: guest %d is a template", ErrNotEligible, vmid)
	}
	if !strings.EqualFold(vm.Status, "running") {
		return out, fmt.Errorf("%w: guest %d is not running", ErrNotEligible, vmid)
	}
	if vm.LockState != "" {
		return out, fmt.Errorf("%w: guest %d is locked (%s)", ErrNotEligible, vmid, vm.LockState)
	}

	policy := e.policyFor(ctx, clusterID, safeconv.Int32(vmid))
	if policy != nil && policy.Excluded {
		return out, fmt.Errorf("%w: guest %d is excluded from guest tools updates", ErrNotEligible, vmid)
	}

	target, err := e.ResolveTarget(ctx, clusterID, cfg, policy)
	if err != nil {
		return out, err
	}

	vwCfg, err := e.queries.GetVirtioWinConfig(ctx, clusterID)
	if err != nil || vwCfg.Storage == "" {
		return out, fmt.Errorf("%w: configure a virtio-win ISO storage for this cluster first", ErrISOUnavailable)
	}

	node, err := e.queries.GetNode(ctx, vm.NodeID)
	if err != nil {
		return out, fmt.Errorf("resolve node for guest %d: %w", vmid, err)
	}

	client, err := e.CreateClient(ctx, clusterID)
	if err != nil {
		return out, fmt.Errorf("proxmox client: %w", err)
	}
	return e.Stage(ctx, client, clusterID, cfg, node.Name, vmid, target, vwCfg.Storage, runNow)
}

// CancelOne clears a staged update for a single guest.
func (e *Engine) CancelOne(ctx context.Context, clusterID uuid.UUID, vmid int) error {
	state, err := e.queries.GetGuestToolsState(ctx, db.GetGuestToolsStateParams{
		ClusterID: clusterID, Vmid: safeconv.Int32(vmid),
	})
	if err != nil {
		return fmt.Errorf("no staged update for guest %d", vmid)
	}
	vm, err := e.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
		ClusterID: clusterID, Vmid: safeconv.Int32(vmid),
	})
	if err != nil {
		return fmt.Errorf("guest %d not found", vmid)
	}
	node, err := e.queries.GetNode(ctx, vm.NodeID)
	if err != nil {
		return fmt.Errorf("resolve node for guest %d: %w", vmid, err)
	}
	client, err := e.CreateClient(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("proxmox client: %w", err)
	}
	return e.CancelStaged(ctx, client, clusterID, node.Name, vmid, state)
}

// DetectOne probes a single guest on demand.
func (e *Engine) DetectOne(ctx context.Context, clusterID uuid.UUID, vmid int) (Detection, error) {
	vm, err := e.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
		ClusterID: clusterID, Vmid: safeconv.Int32(vmid),
	})
	if err != nil {
		return Detection{}, fmt.Errorf("guest %d not found", vmid)
	}
	if !strings.EqualFold(vm.Status, "running") {
		return Detection{}, fmt.Errorf("%w: guest %d is not running", ErrNotEligible, vmid)
	}
	node, err := e.queries.GetNode(ctx, vm.NodeID)
	if err != nil {
		return Detection{}, fmt.Errorf("resolve node for guest %d: %w", vmid, err)
	}
	client, err := e.CreateClient(ctx, clusterID)
	if err != nil {
		return Detection{}, fmt.Errorf("proxmox client: %w", err)
	}
	detection, err := e.Detect(ctx, client, clusterID, node.Name, vmid, vm.Uptime)
	if err != nil {
		if errors.Is(err, proxmox.ErrGuestAgentUnavailable) {
			return Detection{}, fmt.Errorf("%w: the QEMU guest agent is not responding in guest %d", ErrNotEligible, vmid)
		}
		return Detection{}, err
	}
	return detection, nil
}
