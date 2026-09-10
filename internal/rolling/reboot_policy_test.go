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
