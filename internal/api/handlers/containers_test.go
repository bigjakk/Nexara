package handlers

import (
	"slices"
	"testing"
)

// TestContainerStatusActions pins the vocabulary POST
// .../containers/:ct_id/status accepts.
//
// It matters more than the size of the list suggests: the same slice is
// the endpoint's declared enum (internal/api/registry_containers.go), so
// it is what rejects an unknown action at the edge.
//
// Be clear about what it does NOT prove. The switch in PerformAction is
// the other half — an action listed here but missing a case there leaves
// upid empty and still answers "dispatched" — and nothing reads that
// switch, here or in the registry tests. Naming the six actions below is
// the closest thing to a check: a case deleted from the switch has to be
// deleted from this list too for the handler to stay honest, and this is
// where a reader is told to look. Proving it needs an AST walk over the
// handler body, which neither guest domain has.
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
