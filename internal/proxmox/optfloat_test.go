package proxmox

import (
	"encoding/json"
	"testing"
)

// TestOptFloat pins how a pressure figure decodes. PVE serializes its regex
// captures as numbers or strings, and nothing it sends may fail the guest
// list that sync, DRS and rolling updates decode; a guest marshalled for a
// fake PVE must decode back to the same figures.
func TestOptFloat(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		in   string
		want OptFloat
	}{
		{`1.25`, OptFloat{Value: 1.25, Set: true}},
		{`"0.30"`, OptFloat{Value: 0.3, Set: true}},
		{`" 2.5 "`, OptFloat{Value: 2.5, Set: true}},
		{`null`, OptFloat{}},
		{`"NaN"`, OptFloat{}},
		{`"Inf"`, OptFloat{}},
		{`"n/a"`, OptFloat{}},
		{`true`, OptFloat{}},
	} {
		var got struct {
			P OptFloat `json:"p"`
		}
		if err := json.Unmarshal([]byte(`{"p":`+tt.in+`}`), &got); err != nil {
			t.Errorf("%s: %v, want no error", tt.in, err)
		}
		if got.P != tt.want {
			t.Errorf("%s decoded to %+v, want %+v", tt.in, got.P, tt.want)
		}
	}

	vm := VirtualMachine{VMID: 100}
	vm.PressureIOSome = OptFloat{Value: 1.5, Set: true}
	raw, err := json.Marshal(vm)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back VirtualMachine
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.GuestPressure != vm.GuestPressure {
		t.Errorf("round trip = %+v, want %+v", back.GuestPressure, vm.GuestPressure)
	}
}
