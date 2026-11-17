package rolling

import (
	"errors"
	"testing"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestDecideReboot covers the rule that decides whether a node may be rebooted
// after its upgrade.
//
// The case worth naming is "in place, could not verify". The reboot check fails
// most easily when apt has just restarted pveproxy — which is exactly when a
// reboot would hard-stop every guest on a node the operator deliberately left
// loaded. The drained job answers the same input the other way, and is right to:
// its node was emptied and verified minutes earlier, so a failed re-check is a
// blip over a node that is almost certainly still empty.
func TestDecideReboot(t *testing.T) {
	verifyFailed := errors.New("500 Internal Server Error")
	running := []string{"qemu 101 (linux01)"}

	tests := []struct {
		name        string
		drain       bool
		askedReboot bool
		violations  []string
		verifyErr   error
		want        rebootAction
	}{
		// Drained: unchanged from before in-place mode existed.
		{"drained, node empty", true, true, nil, nil, rebootProceed},
		{"drained, guest present", true, true, running, nil, rebootFail},
		{"drained, verify failed", true, true, nil, verifyFailed, rebootProceed},
		{"drained, reboot not requested (apt asked)", true, false, nil, nil, rebootProceed},

		// In place: never take the node down under guests, and never take it
		// down at all unless the operator asked.
		{"in place, guest present", false, true, running, nil, rebootDefer},
		{
			// THE ONE THAT MATTERS. Before decideReboot existed this fell into
			// the caller's fail-open branch and rebooted a loaded hypervisor.
			name:  "in place, verify failed — must not reboot blind",
			drain: false, askedReboot: true, violations: nil, verifyErr: verifyFailed,
			want: rebootDefer,
		},
		{
			// Nothing is running, but the operator asked for no reboot: the
			// node still serves storage, Ceph OSDs and their SSH session.
			name:  "in place, node empty but no reboot requested",
			drain: false, askedReboot: false, violations: nil, verifyErr: nil,
			want: rebootDefer,
		},
		{"in place, empty and reboot requested", false, true, nil, nil, rebootProceed},
		{"in place, no reboot requested and verify failed", false, false, nil, verifyFailed, rebootDefer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := db.RollingUpdateJob{
				DrainGuests:       tt.drain,
				RebootAfterUpdate: tt.askedReboot,
			}
			if got := decideReboot(job, tt.violations, tt.verifyErr); got != tt.want {
				t.Errorf("decideReboot() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDecideReboot_InPlaceNeverRebootsUnverified is the invariant stated
// standalone, because it is the one a future edit is most likely to break: any
// in-place input that is not "verified empty AND reboot requested" must not
// reach rebootProceed.
func TestDecideReboot_InPlaceNeverRebootsUnverified(t *testing.T) {
	for _, askedReboot := range []bool{true, false} {
		for _, violations := range [][]string{nil, {"lxc 200 (ct01)"}} {
			for _, verifyErr := range []error{nil, errors.New("unreachable")} {
				verified := verifyErr == nil && len(violations) == 0
				job := db.RollingUpdateJob{DrainGuests: false, RebootAfterUpdate: askedReboot}
				got := decideReboot(job, violations, verifyErr)

				mayReboot := verified && askedReboot
				if got == rebootProceed && !mayReboot {
					t.Errorf("in-place reboot allowed with askedReboot=%v violations=%v verifyErr=%v",
						askedReboot, violations, verifyErr)
				}
				// An in-place job must never fail a node over its own guests.
				if got == rebootFail {
					t.Errorf("in-place job failed the node (askedReboot=%v violations=%v verifyErr=%v); "+
						"the upgrade succeeded and the guests are there by request",
						askedReboot, violations, verifyErr)
				}
			}
		}
	}
}

// A reboot check that could not run is not a "no": straight after a kernel
// update the SSH setup bound or a dropped link can fail it, and a "no" marked
// the node done on its old kernel.
func TestRebootOwed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stdout string
		err    error
		want   bool
	}{
		{"apt left the marker", "REBOOT_NEEDED\n", nil, true},
		{"no marker", "NO_REBOOT\n", nil, false},
		{"the check could not run", "", errors.New("SSH handshake: i/o timeout"), true},
		{"the check ran but gave no answer", "This account is currently not available.\n", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := rebootOwed(tt.stdout, tt.err); got != tt.want {
				t.Errorf("rebootOwed(%q, %v) = %v, want %v", tt.stdout, tt.err, got, tt.want)
			}
		})
	}
}

// A Proxmox kernel's postinst never writes /var/run/reboot-required, so a
// kernel among the node's recorded updates is what says a reboot is owed. An
// automated upgrade counts only kernels it upgrades and does not hold: the new build a held package would pull in is still listed. A manual
// upgrade ignores the excludes.
func TestUpgradeInstallsKernel(t *testing.T) {
	t.Parallel()
	const series = `{"Package":"proxmox-kernel-6.17","OldVersion":"6.17.2-1","Version":"6.17.2-2"}`
	const build = `{"Package":"proxmox-kernel-6.17.2-2-pve-signed"}`
	const toNewSeries = `[{"Package":"proxmox-default-kernel","OldVersion":"2.0.0"},{"Package":"proxmox-kernel-6.18"}]`
	excluding := func(names ...string) db.RollingUpdateJob {
		return db.RollingUpdateJob{AutoUpgrade: true, PackageExcludes: names}
	}
	auto := excluding("proxmox-kernel-6.17", "proxmox-default-kernel")
	tests := []struct {
		name string
		job  db.RollingUpdateJob
		pkgs string
		want bool
	}{
		{"nothing recorded", db.RollingUpdateJob{}, "", false},
		{"userland only", db.RollingUpdateJob{}, `[{"Package":"pve-manager","OldVersion":"9.0.1"}]`, false},
		{"automated, no excludes: series upgrade and new build", excluding(), `[` + series + `,` + build + `]`, true},
		{"new build pulled in", db.RollingUpdateJob{}, `[{"Package":"proxmox-kernel-6.17.2-2-pve-signed"}]`, true},
		{"Debian kernel", db.RollingUpdateJob{}, `[{"Package":"linux-image-amd64","OldVersion":"6.1.1"}]`, true},
		{"the boot tool is no kernel", db.RollingUpdateJob{}, `[{"Package":"proxmox-kernel-helper","OldVersion":"9.0.1"}]`, false},
		{"unrelated exclude, series upgrade and new build", excluding("pve-manager"), `[` + series + `,` + build + `]`, true},
		{"series held, its new build listed", auto, `[{"Package":"pve-manager","OldVersion":"9.0.1"},` + series + `,` + build + `]`, false},
		{"default kernel held, its new series listed", excluding("proxmox-default-kernel"), toNewSeries, false},
		{"default kernel not held, bringing a new series", excluding("pve-manager"), toNewSeries, true},
		{"proxmox-ve pulls in the default kernel, nothing held", excluding(), `[{"Package":"proxmox-ve","OldVersion":"8.0.2"},` +
			`{"Package":"proxmox-default-kernel"},{"Package":"proxmox-kernel-6.5"},{"Package":"proxmox-kernel-6.5.11-4-pve-signed"}]`, true},
		{"excluded, but a manual upgrade", db.RollingUpdateJob{PackageExcludes: auto.PackageExcludes}, `[` + series + `,` + build + `]`, true},
		{"unreadable list", db.RollingUpdateJob{}, `{`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := upgradeInstallsKernel(tt.job, []byte(tt.pkgs)); got != tt.want {
				t.Errorf("upgradeInstallsKernel(%s) = %v, want %v", tt.pkgs, got, tt.want)
			}
		})
	}
}
