package proxmox

import "testing"

// A halted VM keeps the status "running" and carries QEMU's state beside it;
// the node list (status + qmpstatus) and /cluster/resources (qmpstatus as the
// status) must name the same state, or the fast and slow passes flip the row.
func TestRunState(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status, qmpstatus, want string
	}{
		{"running", "running", ""},
		{"running", "paused", ""}, // the status "suspended" says it
		{"running", "prelaunch", ""},
		{"running", "io-error", "io-error"},
		{"running", "suspended", "suspended"},
		{"running", "watchdog", "watchdog"},
		{"running", "guest-panicked", "guest-panicked"},
		{"running", "shutdown", "shutdown"},
		{"running", "internal-error", "internal-error"},
		{"running", "debug", "debug"},
		{"stopped", "stopped", ""},
	} {
		vm := VirtualMachine{Status: tc.status, QMPStatus: tc.qmpstatus}
		if got := vm.RunState(); got != tc.want {
			t.Errorf("node list %s/%s: RunState = %q, want %q", tc.status, tc.qmpstatus, got, tc.want)
		}
		listed := ClusterResource{Type: "qemu", Status: tc.qmpstatus}
		if got := listed.RunState(); got != tc.want {
			t.Errorf("resources %s: RunState = %q, want %q", tc.qmpstatus, got, tc.want)
		}
		if listed.GuestStatus() != vm.EffectiveStatus() {
			t.Errorf("%s/%s: GuestStatus %q != EffectiveStatus %q", tc.status, tc.qmpstatus, listed.GuestStatus(), vm.EffectiveStatus())
		}
	}
	if got := (ClusterResource{Type: "lxc", Status: "shutdown"}).RunState(); got != "" {
		t.Errorf("container: RunState = %q, want empty", got)
	}
}
