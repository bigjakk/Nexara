package handlers

import (
	"slices"
	"testing"
)

// TestContainerStatusActions pins the actions POST .../containers/:ct_id/status accepts: the
// same slice is the declared enum. It does not prove the switch in PerformAction covers them
// (a missing case answers "dispatched" with no upid); naming the six here is the nearest
// check, and where a reader is told to look.
func TestContainerStatusActions(t *testing.T) {
	tests := []struct {
		name   string
		action string
		want   bool
	}{
		{name: "start", action: "start", want: true},
		{name: "stop", action: "stop", want: true},
		{name: "shutdown", action: "shutdown", want: true},
		{name: "reboot", action: "reboot", want: true},
		{name: "suspend", action: "suspend", want: true},
		{name: "resume", action: "resume", want: true},

		// reset is a VM action with no LXC equivalent — the one place this
		// list is deliberately NOT a copy of VMStatusActions.
		{name: "reset is QEMU-only", action: "reset", want: false},
		{name: "delete is not a power action", action: "delete", want: false},
		{name: "migrate is a different endpoint", action: "migrate", want: false},
		{name: "snapshot is a different endpoint", action: "snapshot", want: false},
		{name: "empty", action: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := slices.Contains(ContainerStatusActions, tt.action); got != tt.want {
				t.Errorf("ContainerStatusActions contains %q = %v, want %v", tt.action, got, tt.want)
			}
		})
	}
}

// TestContainerStatusActionsAreDistinctFromVMs states the relationship
// between the two lists rather than leaving it to a reader to notice: the
// container set is the VM set minus reset.
func TestContainerStatusActionsAreDistinctFromVMs(t *testing.T) {
	for _, action := range ContainerStatusActions {
		if !slices.Contains(VMStatusActions, action) {
			t.Errorf("ContainerStatusActions has %q, which VMStatusActions does not — "+
				"if LXC really grew an action QEMU has no name for, say so here", action)
		}
	}
	var onlyVM []string
	for _, action := range VMStatusActions {
		if !slices.Contains(ContainerStatusActions, action) {
			onlyVM = append(onlyVM, action)
		}
	}
	if len(onlyVM) != 1 || onlyVM[0] != "reset" {
		t.Errorf("VM-only actions = %v, want exactly [reset]", onlyVM)
	}
}
