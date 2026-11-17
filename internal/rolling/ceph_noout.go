package rolling

import (
	"context"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The ceph_noout_state values (migration 000155).
const (
	nooutHeld       = "held"
	nooutAlreadySet = "already_set"
	nooutNotHeld    = "not_held"
	nooutReleased   = "released"
)

// holdNoout sets Ceph's noout flag as the job is about to take a node down,
// when ceph_noout is on and the cluster runs Ceph, and keeps it until the job
// ends (releaseHeldNoout, from releaseJobState and the cleanup sweep; a pause
// keeps it). As the HA rules are, it records 'held' before the set, and again
// after it: a release that ran between the two (a cancel, another node failing
// the job) found the flag unset and wrote 'released', and the flag the set then
// put on would have no record.
//
// A flag already set when the job looks is the operator's: the job records
// 'already_set' and never clears it. Fail open, as the CRS pause does: a flag
// that cannot be read or set is logged and audited, the job goes on, and
// 'not_held' with the reason tells the job view; the next node's start tries
// again. Proxmox changes flags only through the node behind the API address
// (CephFlagReason), so on a cluster whose endpoint has no Ceph it stays not held.
func (o *Orchestrator) holdNoout(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob) {
	if !job.CephNoout || !job.CephPresent.Valid || !job.CephPresent.Bool ||
		job.CephNooutState == nooutHeld || job.CephNooutState == nooutAlreadySet {
		return
	}
	set, err := client.GetCephFlag(ctx, proxmox.CephFlagNoout)
	if err != nil {
		o.nooutNotHeld(ctx, job, "read", err)
		return
	}
	if set {
		o.setNooutState(ctx, job, nooutAlreadySet, "")
		o.logger.Info("rolling update: noout was already set, leaving it to whoever set it", "job_id", job.ID)
		o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_ceph_noout_already_set", nil)
		return
	}
	if !o.recordNooutHeld(ctx, job) {
		return // No record, so no flag: the next node's start tries again.
	}
	if err := client.SetCephFlag(ctx, proxmox.CephFlagNoout, true); err != nil {
		// Whether the set landed decides what the record says: a read that says
		// unset is not held; one that fails keeps 'held', whose release is a
		// harmless unset.
		if isSet, rerr := client.GetCephFlag(ctx, proxmox.CephFlagNoout); rerr == nil && !isSet {
			o.nooutNotHeld(ctx, job, "set", err)
			return
		}
		o.logger.Warn("rolling update: setting noout failed and its state could not be confirmed; releasing it at the end",
			"job_id", job.ID, "error", err)
		o.recordNooutHeld(ctx, job)
		return
	}
	o.recordNooutHeld(ctx, job)
	o.logger.Info("rolling update: set Ceph noout for the job", "job_id", job.ID)
	o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_ceph_noout_set", nil)
	o.auditObject(ctx, job.ClusterID, job.ID, "ceph_osd", proxmox.CephFlagNoout, "osd_flag_set",
		map[string]any{"flag": proxmox.CephFlagNoout, "value": 1})
	switch o.jobStatus(ctx, job.ID) {
	case "cancelled", "failed", "completed":
		// The job ended while the flag was going on. Release now; the re-armed
		// sweep retries what this misses.
		o.releaseJobState(ctx, job.ID, true)
	}
}

// recordNooutHeld writes 'held' and re-arms the cleanup sweep, which a job
// already terminal (the cancel race) would otherwise have retired.
func (o *Orchestrator) recordNooutHeld(ctx context.Context, job db.RollingUpdateJob) bool {
	if !o.setNooutState(ctx, job, nooutHeld, "") {
		return false
	}
	if err := o.queries.ArmJobCleanupPending(ctx, job.ID); err != nil {
		o.logger.Warn("failed to re-arm cleanup after recording noout", "job_id", job.ID, "error", err)
	}
	return true
}

// releaseHeldNoout clears noout for a job that set it, and only for one: a
// state other than 'held' (the operator's flag, one never set) is released by
// doing nothing. Only a cluster-wide "not initialized" answer means there is
// nothing left to clear; any other failure, the endpoint node's own "binary not
// installed" included, keeps 'held' with the reason for the job view, for the
// cleanup sweep to retry (an unset is harmless and repeatable).
func (o *Orchestrator) releaseHeldNoout(ctx context.Context, client *proxmox.Client, job db.RollingUpdateJob, auditFailures bool) bool {
	if job.CephNooutState != nooutHeld {
		return true
	}
	if err := client.SetCephFlag(ctx, proxmox.CephFlagNoout, false); err != nil && !proxmox.IsCephNotInitialized(err) {
		o.logger.Error("rolling update: failed to clear Ceph noout; will retry via cleanup sweep", "job_id", job.ID, "error", err)
		o.setNooutState(ctx, job, nooutHeld, "Nexara could not clear noout ("+proxmox.CephFlagReason(err)+") and keeps trying.")
		if auditFailures {
			o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_ceph_noout_release_failed", nil)
		}
		return false
	}
	if !o.setNooutState(ctx, job, nooutReleased, "") {
		return false
	}
	o.logger.Info("rolling update: cleared Ceph noout", "job_id", job.ID)
	o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_ceph_noout_released", nil)
	o.auditObject(ctx, job.ClusterID, job.ID, "ceph_osd", proxmox.CephFlagNoout, "osd_flag_unset",
		map[string]any{"flag": proxmox.CephFlagNoout, "value": 0})
	return true
}

func (o *Orchestrator) setNooutState(ctx context.Context, job db.RollingUpdateJob, state, reason string) bool {
	if err := o.queries.SetJobCephNooutState(ctx, db.SetJobCephNooutStateParams{
		ID: job.ID, CephNooutState: state, CephNooutReason: reason,
	}); err != nil {
		o.logger.Warn("rolling update: could not record the noout state", "job_id", job.ID, "state", state, "error", err)
		return false
	}
	return true
}

// nooutNotHeld is the fail-open branch: logged, audited (the step, never the
// error text, which can carry an address), and shown on the job with a fixed
// reason.
func (o *Orchestrator) nooutNotHeld(ctx context.Context, job db.RollingUpdateJob, step string, err error) {
	o.logger.Warn("rolling update: could not hold Ceph noout; going on without it",
		"job_id", job.ID, "step", step, "error", err)
	o.setNooutState(ctx, job, nooutNotHeld, "noout is not held: "+proxmox.CephFlagReason(err)+".")
	o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_ceph_noout_failed", map[string]string{"step": step})
}
