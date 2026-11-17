package rolling

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// cephWaitLimit is how long a node may wait for Ceph, paused time not counted,
// before the job pauses itself. Pausing rather than failing: the node is fine
// and Ceph is slow, and a failed job releases what it holds and strands the
// guests it drained.
const cephWaitLimit = 30 * time.Minute

// cephPresent reads the cluster's Ceph status for a job that has a Ceph option
// on, and decides once per job whether the cluster runs Ceph: the first answer
// is remembered in ceph_present, and a cluster remembered without Ceph is never
// read again, so it pays one status read per job and logs nothing. A cluster
// that gains Ceph mid-job is not waited for. One remembered with Ceph that then
// answers "not configured" reads as could-not-look: its nodes keep waiting until
// the time limit pauses the job. A read that fails before the first answer is
// could-not-look too, so a job on an unreachable cluster starts no node.
func (o *Orchestrator) cephPresent(ctx context.Context, client *proxmox.Client, job *db.RollingUpdateJob) (present bool, status *proxmox.CephStatus, readErr error) {
	if job.CephPresent.Valid && !job.CephPresent.Bool {
		return false, nil, nil
	}
	status, err := client.GetClusterCephStatus(ctx)
	if err != nil && !job.CephPresent.Valid && proxmox.IsCephNotConfigured(err) {
		o.remember(ctx, job, false)
		return false, nil, nil
	}
	if err != nil {
		o.logger.Warn("rolling update: could not read the Ceph status, waiting",
			"job_id", job.ID, "cluster_id", job.ClusterID, "error", err)
		return true, nil, err
	}
	if !job.CephPresent.Valid {
		o.remember(ctx, job, true)
	}
	return true, status, nil
}

func (o *Orchestrator) remember(ctx context.Context, job *db.RollingUpdateJob, present bool) {
	if err := o.queries.SetJobCephPresent(ctx, db.SetJobCephPresentParams{
		ID: job.ID, CephPresent: pgtype.Bool{Bool: present, Valid: true},
	}); err != nil {
		o.logger.Warn("rolling update: could not record whether the cluster runs Ceph", "job_id", job.ID, "error", err)
		return
	}
	job.CephPresent = pgtype.Bool{Bool: present, Valid: true}
	o.logger.Info("rolling update: decided whether the cluster runs Ceph", "job_id", job.ID, "ceph", present)
}

// cephAllowsStart is the gate before a pending node is taken down (drained, or
// upgraded in place). With ceph_wait on, the node is not started until Ceph is
// clean; this holds the first node too. Passing it takes the OSD baseline the
// node's health check is held to. With only ceph_noout on, it settles whether
// the cluster runs Ceph, which holdNoout reads.
func (o *Orchestrator) cephAllowsStart(ctx context.Context, client *proxmox.Client, job *db.RollingUpdateJob, node db.RollingUpdateNode) bool {
	if !job.CephWait && !job.CephNoout {
		return true
	}
	present, status, err := o.cephPresent(ctx, client, job)
	if !present || !job.CephWait {
		return true
	}
	// No baseline here: one is taken now, and an OSD lost before this node
	// starts is either back or not this node's doing.
	check := cephCheck(status, err, 0)
	if check.Outcome != CephClean {
		o.cephWaiting(ctx, *job, node, check)
		return false
	}
	o.cephWaitOver(ctx, node)
	if err := o.queries.SetJobCephOSDBaseline(ctx, db.SetJobCephOSDBaselineParams{
		ID: job.ID, CephOsdsBaseline: int32(status.OSDMap.NumUpOSDs), //nolint:gosec // an OSD count
	}); err != nil {
		o.logger.Warn("rolling update: could not record the Ceph OSD baseline", "job_id", job.ID, "error", err)
	}
	job.CephOsdsBaseline = int32(status.OSDMap.NumUpOSDs) //nolint:gosec // an OSD count
	return true
}

// cephAllowsHealthy is the gate before a node in health_check is called
// healthy. Every path into health_check (the reboot coming back, an upgrade
// with no reboot, a deferred reboot, a confirmed manual upgrade) lands here
// before its guests are restored and the node completed, so with ceph_wait on
// none is moved back onto a node whose OSDs are still coming back.
func (o *Orchestrator) cephAllowsHealthy(ctx context.Context, client *proxmox.Client, job *db.RollingUpdateJob, node db.RollingUpdateNode) bool {
	if !job.CephWait {
		return true
	}
	present, status, err := o.cephPresent(ctx, client, job)
	if !present {
		return true
	}
	check := cephCheck(status, err, int(job.CephOsdsBaseline))
	if check.Outcome != CephClean {
		o.cephWaiting(ctx, *job, node, check)
		return false
	}
	o.cephWaitOver(ctx, node)
	o.logger.Info("rolling update: Ceph is clean, node called healthy", "job_id", job.ID, "node", node.NodeName)
	return true
}

// cephWaiting records why a node waits for Ceph, and pauses the job once the
// wait has lasted cephWaitLimit, counted as stepElapsed counts: from the later
// of the wait's start and the job's last resume, so a resume restarts the clock.
func (o *Orchestrator) cephWaiting(ctx context.Context, job db.RollingUpdateJob, node db.RollingUpdateNode, check CephCheck) {
	if node.CephWaitStartedAt.Valid && stepElapsed(job, node.CephWaitStartedAt.Time) > cephWaitLimit {
		reason := fmt.Sprintf("Paused by Nexara: %s waited more than %d minutes for Ceph (%s). Resume checks Ceph again and waits up to %d more minutes.",
			node.NodeName, int(cephWaitLimit.Minutes()), check.Reason, int(cephWaitLimit.Minutes()))
		rows, err := o.queries.PauseRollingUpdateJobWithReason(ctx, db.PauseRollingUpdateJobWithReasonParams{ID: job.ID, PauseReason: reason})
		if err != nil || rows == 0 {
			if err != nil {
				o.logger.Error("rolling update: could not pause the job after the Ceph wait limit", "job_id", job.ID, "error", err)
			}
			return
		}
		o.logger.Warn("rolling update paused: Ceph wait limit reached", "job_id", job.ID, "node", node.NodeName, "reason", check.Reason)
		o.auditLog(ctx, job.ClusterID, job.ID, "rolling_update_paused", map[string]string{"by": "ceph_wait_limit", "node": node.NodeName})
		o.publishEvent(ctx, job.ClusterID, job.ID, "paused")
		o.sendJobNotification(ctx, job, "paused", "Rolling update paused: "+reason)
		return
	}
	if node.CephWaitReason == check.Reason && node.CephWaitStartedAt.Valid {
		return
	}
	if err := o.queries.SetNodeCephWait(ctx, db.SetNodeCephWaitParams{ID: node.ID, CephWaitReason: check.Reason}); err != nil {
		o.logger.Warn("rolling update: could not record the Ceph wait", "node", node.NodeName, "error", err)
		return
	}
	o.logger.Info("rolling update: waiting for Ceph", "job_id", job.ID, "node", node.NodeName, "step", node.Step, "reason", check.Reason)
	o.publishEvent(ctx, job.ClusterID, job.ID, "node_waiting_for_ceph")
}

// cephWaitOver clears a node's wait, so the next gate times its own.
func (o *Orchestrator) cephWaitOver(ctx context.Context, node db.RollingUpdateNode) {
	if node.CephWaitReason == "" && !node.CephWaitStartedAt.Valid {
		return
	}
	if err := o.queries.ClearNodeCephWait(ctx, node.ID); err != nil {
		o.logger.Warn("rolling update: could not clear the Ceph wait", "node", node.NodeName, "error", err)
	}
}
