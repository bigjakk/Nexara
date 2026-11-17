package rolling

import (
	"encoding/json"
	"slices"
	"strings"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// rebootCheckCommand asks a node whether something it installed asked for a
// reboot. Only a package hook that calls update-notifier's notify-reboot-required
// writes /var/run/reboot-required, and Debian has not shipped update-notifier
// since wheezy: a Proxmox kernel's postinst runs depmod and /etc/kernel/postinst.d
// and never writes it. So the file is one signal, and upgradeInstallsKernel the
// other.
const rebootCheckCommand = "test -f /var/run/reboot-required && echo REBOOT_NEEDED || echo NO_REBOOT"

// kernelPackagePrefixes name the packages that install a kernel: Proxmox's
// series and build packages (proxmox-kernel-6.17, proxmox-kernel-6.17.2-1-pve-signed),
// their PVE 7 names, and Debian's own.
var kernelPackagePrefixes = []string{"proxmox-kernel-", "pve-kernel-", "linux-image-"}

// notKernelSuffixes are the packages under those prefixes that install no
// kernel: the boot tool, the libc headers, debug symbols and the signing template.
var notKernelSuffixes = []string{"-helper", "-libc-dev", "-dbgsym", "-dbg", "-signed-template"}

// isKernelPackage reports whether installing name installs a kernel.
// proxmox-default-kernel moves the node to a newer series, so it counts.
func isKernelPackage(name string) bool {
	if name == "proxmox-default-kernel" {
		return true
	}
	if !slices.ContainsFunc(kernelPackagePrefixes, func(p string) bool { return strings.HasPrefix(name, p) }) {
		return false
	}
	return !slices.ContainsFunc(notKernelSuffixes, func(s string) bool { return strings.HasSuffix(name, s) })
}

// upgradeInstallsKernel reports whether the node's upgrade installs a kernel, read
// from packagesJSON: the pending updates startNode recorded from Proxmox's apt
// listing, which names both the series package being upgraded and the new build
// it pulls in. It serves the automated and the manual path alike, and needs
// nothing from the node. An automated upgrade counts only kernel packages it
// upgrades and does not hold: a new build or series is listed even when the
// held package that would pull it in keeps apt from installing it, and the
// upgraded package that pulls one in is always listed beside it. A list that cannot be read is not a "no", as in
// rebootOwed.
func upgradeInstallsKernel(job db.RollingUpdateJob, packagesJSON []byte) bool {
	if len(packagesJSON) == 0 {
		return false
	}
	var updates []proxmox.AptUpdate
	if err := json.Unmarshal(packagesJSON, &updates); err != nil {
		return true
	}
	for _, u := range updates {
		if !isKernelPackage(u.Package) {
			continue
		}
		if job.AutoUpgrade && (u.OldVersion == "" || slices.Contains(job.PackageExcludes, u.Package)) {
			continue
		}
		return true
	}
	return false
}

// rebootOwed reads the answer to rebootCheckCommand: a reboot is owed unless
// the node said NO_REBOOT. A check that could not run, or ran and gave no
// answer, is not a "no": straight after a kernel update the SSH setup bound or
// a dropped link can fail it, and a "no" would mark the node done on its old
// kernel. decideReboot then treats "owed" exactly as it does when apt asks.
func rebootOwed(stdout string, err error) bool {
	return err != nil || !strings.Contains(stdout, "NO_REBOOT")
}

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
	// caller may have raised needsReboot on its own, because the upgrade
	// installed a kernel or left /var/run/reboot-required behind, and that is
	// a reason to tell the operator a reboot is owed, not a licence to
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
