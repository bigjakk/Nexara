package rolling

import db "github.com/bigjakk/nexara/internal/db/generated"

// rebootAction is what to do when an upgrade has finished and a reboot is
// wanted: take it, put it off, or treat the situation as a failed node.
type rebootAction int

const (
	// rebootProceed reboots the node now.
	rebootProceed rebootAction = iota
	// rebootDefer completes the node with reboot_required set. The upgrade
	// landed; the reboot is owed and someone will schedule it.
	rebootDefer
	// rebootFail fails the node. Only for a drained job, where a guest on the
	// node means the drain did not hold.
	rebootFail
)

// decideReboot is the single rule for whether a node may be rebooted after its
// upgrade, shared by the automated path and the manual confirm path so the two
// cannot disagree.
//
// violations is what verifyNodeDrained found running on the node, and verifyErr
// is its error — the two are mutually exclusive, and the difference between
// "nothing is running" and "could not tell" is the whole point of this
// function. It is a pure decision over three inputs precisely so it can be
// tested without a database or a Proxmox server, which is not true of either
// call site.
//
// The drained and in-place jobs answer "could not tell" in opposite directions,
// and both are right for their own case:
//
//   - Drained: the node was emptied and verified minutes ago, so a failed
//     re-check is a transient API blip over a node that is almost certainly
//     still empty. Rebooting is the low-stakes choice, and refusing would
//     strand the job on a node that has already been drained. Unchanged
//     behaviour.
//
//   - In place: the guests are running by request. "Could not tell" therefore
//     means "about to reboot a loaded hypervisor blind", and the read is most
//     likely to fail exactly when the upgrade has just restarted pveproxy —
//     i.e. the moment a reboot would do the most damage. Assume guests are
//     present and defer.
func decideReboot(job db.RollingUpdateJob, violations []string, verifyErr error) rebootAction {
	if job.DrainGuests {
		if verifyErr != nil {
			return rebootProceed
		}
		if len(violations) > 0 {
			return rebootFail
		}
		return rebootProceed
	}

	// An in-place job never reboots unless the operator asked for one. The
	// caller may have raised needsReboot on its own from
	// /var/run/reboot-required — which every kernel update leaves behind — and
	// that is a reason to tell the operator a reboot is owed, not a licence to
	// take the node down under guests they chose to leave running. A node whose
	// guests all happen to be stopped is still serving storage, Ceph OSDs and
	// the operator's own SSH session.
	if !job.RebootAfterUpdate {
		return rebootDefer
	}
	if verifyErr != nil || len(violations) > 0 {
		return rebootDefer
	}
	return rebootProceed
}
