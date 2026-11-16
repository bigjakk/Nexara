package proxmox

import (
	"encoding/json"
	"testing"
)

// pve-network names the VNet option "isolate-ports" (VnetPlugin.pm) on the way in and out;
// Nexara's own API keeps calling it "isolate".
func TestSDNVNetIsolateSpeaksIsolatePorts(t *testing.T) {
	t.Parallel()
	for name, form := range map[string]map[string][]string{
		"create": sdnVNetCreateToForm(CreateSDNVNetParams{VNet: "vnet01", Zone: "zone01", Isolate: 1}),
		"update": sdnVNetUpdateToForm(UpdateSDNVNetParams{Isolate: 1}),
	} {
		if got := form["isolate-ports"]; len(got) != 1 || got[0] != "1" || form["isolate"] != nil {
			t.Errorf("%s form = %v, want isolate-ports=1 and no isolate", name, form)
		}
	}

	var v SDNVNet
	if err := json.Unmarshal([]byte(`{"vnet":"vnet01","zone":"zone01","isolate-ports":1}`), &v); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(v)
	if v.Isolate != 1 || string(out) != `{"vnet":"vnet01","zone":"zone01","isolate":1}` {
		t.Errorf("read back Isolate=%d, served %s; want 1 and the isolate key", v.Isolate, out)
	}
}
