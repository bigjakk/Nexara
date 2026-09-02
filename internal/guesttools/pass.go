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

// stageWorstCase is the longest a legitimate Stage can spend between writing the
// 'staging' marker and replacing it, derived from the timeouts that actually
// bound it rather than estimated.
//
// Derived, because the estimate was wrong. Engines reach Proxmox through the
// shared client cache, whose per-call bound is proxmox.CachedClientTimeout (5
// min) — not the 60s timeout on the uncached fallback that Engine.CreateClient
// only uses when the cache misses. Budgeting the fallback number made this three
// times too small.
//
// After the marker, Stage does three guest execs and two further HTTP calls —
// the media attach and the script write. An exec costs guestExecTimeout plus up
// to two client timeouts on top, because runScript sets
// its deadline only after the initial exec call returns and re-checks it only at
// the top of each poll, so a final in-flight status call can start just under the
// deadline and still run its full timeout.
//
// The snapshot — the one genuinely slow step, bounded by snapshotWaitTimeout —
// is taken BEFORE the marker is written, so it is deliberately not counted here.
const stageWorstCase = 3*(guestExecTimeout+2*proxmox.CachedClientTimeout) + 2*proxmox.CachedClientTimeout

// stagingUpdateMaxAge bounds a row still mid-Stage.
//
// Minutes, not days, because 'staging' is a marker Stage writes on its way
// through and overwrites seconds later — it is never a resting state. A row
// still wearing it long afterwards was stranded by a restart, or by an error
// that never reached markFailed.
//
// Comfortably above stageWorstCase so no live Stage is ever expired out from
// under itself; TestStaleCeilingFor holds that relationship, so raising a
// timeout it depends on fails the build rather than silently narrowing it.
//
// Without any of this such a row waited out stagedUpdateMaxAge — thirty days
// holding a max_concurrent slot, keeping its guest out of every pass, and
// blocking the vanished-guest sweep for its whole cluster.
const stagingUpdateMaxAge = 90 * time.Minute

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
	switch vwCfg, err := e.queries.GetVirtioWinConfig(ctx, cfg.ClusterID); {
	case err == nil:
		storage = vwCfg.Storage
	case !errors.Is(err, pgx.ErrNoRows):
		// Staging is skipped either way, but the per-guest warning below reads
		// as "you never configured a storage". Record the real cause once so
		// the operator is not sent to fix something that is already set.
		e.logger.Warn("guest tools: cannot read virtio-win config for this cluster",
			"cluster_id", cfg.ClusterID, "error", err)
	}

	// Counters for the failures that are reported once for the pass rather than
	// once per guest, because their cause is cluster-wide.
	pinFailures := 0
	var pinErr error

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

		policy, err := e.lookupPolicy(ctx, cfg.ClusterID, guest.Vmid)
		if err != nil {
			// Skipped rather than staged: resolving with a nil policy would
			// stage the cluster target on a guest that may be pinned away from
			// it. The row is read again next pass.
			//
			// Counted here and reported once below, like the virtio-win config
			// read above. The cause is a database blip, which hits every guest
			// in the cluster at once, so a line per guest buries the one fact
			// that matters — how much of the fleet this pass skipped.
			pinFailures++
			pinErr = err
			e.logger.Debug("guest tools: could not read the per-guest pin", "vmid", vmid, "error", err)
			continue
		}
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

	if pinFailures > 0 {
		e.logger.Warn("guest tools: skipped guests whose per-guest pin could not be read",
			"cluster_id", cfg.ClusterID, "guests", pinFailures, "error", pinErr)
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

// lookupPolicy returns a guest's per-guest pin, distinguishing "there is no
// pin" (nil, nil) from "we could not read whether there is a pin" (nil, err).
//
// Every caller must keep that distinction. Collapsing the error to a nil
// policy reads identically to "this guest has no pin", which is wrong three
// different ways: it withdraws a correctly staged pinned guest claiming the
// target changed, it stages the cluster target on a guest pinned away from it,
// and it walks straight past an Excluded flag — a guest the operator marked
// "never touch this" gets the ISO attached and the installer registered. A
// database blip hits many rows at once, so each of those is a fleet-wide
// event, and none of them is visible in the result.
func (e *Engine) lookupPolicy(ctx context.Context, clusterID uuid.UUID, vmid int32) (*db.GuestToolsPolicy, error) {
	policy, err := e.queries.GetGuestToolsPolicy(ctx, db.GetGuestToolsPolicyParams{
		ClusterID: clusterID, Vmid: vmid,
	})
	switch {
	case err == nil:
		return &policy, nil
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	default:
		return nil, fmt.Errorf("read guest tools policy: %w", err)
	}
}

// markFailed records a terminal failure, leaving the borrowed-CD-ROM record
// standing so the media can still be put back.
//
// CdromRestored is false here and that is the whole point. Nothing on this path
// restores anything — it is reached from a staging error and from an expiry,
// neither of which touches the guest — so claiming otherwise cleared
// prior_cdrom_key on the way past, and ListGuestToolsPendingCDROMRestore
// selects on that column being non-empty. The record the retry sweep needed was
// destroyed by the same write that made the sweep responsible for it, and the
// virtio-win ISO stayed mounted on the guest for good, re-pinning itself
// against pruning. Leaving the record intact hands the row straight to
// retryPendingCDROMRestores, which runs at the end of this same pass.
func (e *Engine) markFailed(ctx context.Context, clusterID uuid.UUID, vmid int32, msg string) {
	if err := e.queries.FinishGuestToolsUpdate(ctx, db.FinishGuestToolsUpdateParams{
		ClusterID: clusterID, Vmid: vmid, Stage: "failed", LastError: msg,
		RebootRequired: false, CdromRestored: false,
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
			// Logged, not silent: a row wedged here retries every 60s forever,
			// and Debug at least leaves a trail saying which one and why.
			e.logger.Debug("guest tools: pending restore could not load node", "vmid", vmid, "error", err)
			continue
		}
		client, err := e.CreateClient(ctx, state.ClusterID)
		if err != nil {
			e.logger.Debug("guest tools: pending restore could not build a client", "vmid", vmid, "error", err)
			continue
		}

		// Re-read immediately before touching the guest. This list was taken
		// once, and each row above this one costs up to four network round
		// trips, so a guest re-staged in the meantime would otherwise have the
		// ISO pulled straight back off an update that now needs it.
		fresh, err := e.queries.GetGuestToolsState(ctx, db.GetGuestToolsStateParams{
			ClusterID: state.ClusterID, Vmid: state.Vmid,
		})
		if err != nil {
			e.logger.Debug("guest tools: could not re-read state before a pending restore",
				"vmid", vmid, "error", err)
			continue
		}
		if isInFlightStage(fresh.Stage) || fresh.PriorCdromKey == "" {
			continue
		}
		// And act on what the re-read said, not on the snapshot. A drive that
		// changed rather than emptied would otherwise be restored to the old
		// key, which is the one thing the re-read exists to prevent.
		if err := e.RestoreCDROM(ctx, client, node.Name, vmid, fresh.PriorCdromKey, fresh.PriorCdromValue); err != nil {
			e.logger.Warn("guest tools: retry of CD-ROM restore failed", "vmid", vmid, "error", err)
			continue
		}
		e.logger.Info("guest tools: CD-ROM restored on retry",
			"cluster_id", fresh.ClusterID, "vmid", vmid, "device", fresh.PriorCdromKey)
		e.clearCDROMRestore(ctx, fresh)
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
// That distinction is what lookupPolicy returns and this function propagates;
// see lookupPolicy for what collapsing it costs. No cluster config row is not
// the same kind of unknown: the target still resolves from the virtio-win pin
// or upstream stable, exactly as the fleet view resolves it.
func (e *Engine) resolveTargetForState(ctx context.Context, state db.GuestToolsState) (Target, error) {
	cfg, err := ConfigOrDefault(ctx, e.queries, state.ClusterID)
	if err != nil {
		return Target{}, err
	}
	policy, err := e.lookupPolicy(ctx, state.ClusterID, state.Vmid)
	if err != nil {
		return Target{}, err
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

// staleCeilingFor returns how long a row in a given stage may go without
// progress, and how to describe having exceeded it.
//
// Three ceilings, because the three in-flight stages are waiting on completely
// different things and lumping them together is what stranded rows for a month:
//
//   - staging: Stage itself is still running. Bounded work, so minutes.
//   - staged:  waiting for somebody to reboot the guest. A server that reboots
//     monthly is doing nothing wrong, so this one is deliberately generous.
//   - running: the installer was actually started. It takes minutes; hours mean
//     it is not coming back.
//
// Anything else gets the staged ceiling. Nothing else reaches here today — the
// in-flight query selects exactly these three — but defaulting to the longest
// ceiling means a stage added later fails safe, waiting too long rather than
// tearing down an update somebody is relying on.
func staleCeilingFor(stage string) (limit time.Duration, reason string) {
	switch stage {
	case "staging":
		return stagingUpdateMaxAge, fmt.Sprintf("staging did not finish within %s", stagingUpdateMaxAge)
	case "running":
		return runningUpdateMaxAge, fmt.Sprintf("the updater did not report a result within %s of starting", runningUpdateMaxAge)
	default:
		return stagedUpdateMaxAge, fmt.Sprintf("no reboot within %s of staging", stagedUpdateMaxAge)
	}
}

// expireIfStale abandons an update that has waited past its ceiling, freeing
// the concurrency slot it holds. Returns true when the row was expired.
func (e *Engine) expireIfStale(ctx context.Context, state db.GuestToolsState) bool {
	if !state.StagedAt.Valid {
		return false
	}
	limit, reason := staleCeilingFor(state.Stage)
	if time.Since(state.StagedAt.Time) <= limit {
		return false
	}

	// Re-read before acting on a verdict this old.
	//
	// state comes from the snapshot ListGuestToolsInFlight took at the top of the
	// pass, and every guest reconciled ahead of this one can have spent minutes in
	// its own guest execs — so the snapshot's age can exceed the very ceiling
	// being applied. In that window an operator can have re-staged this guest:
	// Stage would then be running, or finished, against a row this function still
	// remembers as abandoned.
	//
	// That used to be survivable because markFailed cleared prior_cdrom_key, which
	// kept the mislabelled row out of the restore sweep entirely — the damage was
	// a wrong badge. Now that the record is deliberately preserved, the sweep acts
	// on it in the same pass and would pull the ISO off a guest whose update was
	// just staged, or is being installed. TestPendingRestoreIsRetryable states the
	// invariant this protects: an in-flight row must never be swept.
	fresh, err := e.queries.GetGuestToolsState(ctx, db.GetGuestToolsStateParams{
		ClusterID: state.ClusterID, Vmid: state.Vmid,
	})
	if err != nil {
		e.logger.Debug("guest tools: could not re-read state before expiring",
			"vmid", state.Vmid, "error", err)
		return false
	}
	//
	// Identity, not freshness: staged_at is written only by SetGuestToolsStage,
	// which always sets stage in the same statement, so an unchanged pair means
	// this is still the row that looked abandoned. No second age check is
	// needed after it — the age was taken above and time only moves forward.
	if fresh.Stage != state.Stage || !fresh.StagedAt.Valid ||
		!fresh.StagedAt.Time.Equal(state.StagedAt.Time) {
		return false
	}
	state = fresh
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

	// Note that nothing below reads cfg.Mode: an explicit per-guest action is
	// its own authorisation and does not need the cluster feature switched on,
	// so the disabled default ConfigOrDefault returns is not a refusal here.
	cfg, err := ConfigOrDefault(ctx, e.queries, clusterID)
	if err != nil {
		return out, err
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

	// The error is returned, not dropped: the check below decides whether an
	// operator's "never touch this guest" holds, and an unreadable pin that
	// reads as nil walks straight past it. Refusing is the honest answer —
	// StageUpdate maps this to a 502 and the operator can retry.
	policy, err := e.lookupPolicy(ctx, clusterID, safeconv.Int32(vmid))
	if err != nil {
		return out, err
	}
	if policy != nil && policy.Excluded {
		return out, fmt.Errorf("%w: guest %d is excluded from guest tools updates", ErrNotEligible, vmid)
	}

	target, err := e.ResolveTarget(ctx, clusterID, cfg, policy)
	if err != nil {
		return out, err
	}

	// A read failure is not the operator forgetting to configure a storage;
	// telling them to go configure one sends them to fix the wrong thing. Only
	// an absent row or an empty storage is "not configured yet".
	vwCfg, err := e.queries.GetVirtioWinConfig(ctx, clusterID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, fmt.Errorf("read virtio-win config: %w", err)
	}
	if vwCfg.Storage == "" {
		return out, fmt.Errorf("%w: configure a virtio-win ISO storage for this cluster first", ErrISOUnavailable)
	}

	node, client, err := e.nodeAndClient(ctx, clusterID, vm)
	if err != nil {
		return out, err
	}
	result, err := e.Stage(ctx, client, clusterID, cfg, node, vmid, target, vwCfg.Storage, runNow)
	if err != nil {
		// Do not leave the row wearing the 'staging' marker Stage wrote on its
		// way in. Nothing else on this path records a failure — markFailed is
		// reached only from the scheduled pass and from expiry — so a row
		// stranded here used to sit in flight until something swept it up much
		// later with a generic reason, holding a max_concurrent slot and keeping
		// the guest out of every pass in between. Recorded here, the operator
		// gets the real error, and the borrowed drive reaches the restore sweep.
		//
		// Gated on this call having written the marker, not on the row merely
		// reading 'staging'. Nothing serialises two stagings of one guest, and
		// both would see that stage — so a double-click whose second request
		// failed early would otherwise mark the first one's live row failed,
		// and the same-tick restore sweep would then take its media away.
		if result.MarkerWritten {
			e.markFailed(ctx, clusterID, safeconv.Int32(vmid), err.Error())
		}
	}
	return result, err
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
	node, client, err := e.nodeAndClient(ctx, clusterID, vm)
	if err != nil {
		return err
	}
	return e.CancelStaged(ctx, client, clusterID, node, vmid, state)
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
	node, client, err := e.nodeAndClient(ctx, clusterID, vm)
	if err != nil {
		return Detection{}, err
	}
	detection, err := e.Detect(ctx, client, clusterID, node, vmid, vm.Uptime)
	if err != nil {
		if errors.Is(err, proxmox.ErrGuestAgentUnavailable) {
			return Detection{}, fmt.Errorf("%w: the QEMU guest agent is not responding in guest %d", ErrNotEligible, vmid)
		}
		return Detection{}, err
	}
	return detection, nil
}
