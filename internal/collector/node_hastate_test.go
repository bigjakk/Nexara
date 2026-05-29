package collector

import (
	"encoding/json"
	"testing"
)

func TestParseNodeHAState(t *testing.T) {
	raw := map[string]json.RawMessage{
		"manager_status": json.RawMessage(`{"node_status":{"pve1":"online","pve2":"maintenance","pve3":"online"},"master_node":"pve1"}`),
		"quorum":         json.RawMessage(`{"quorate":"1"}`),
	}
	got := parseNodeHAState(raw)
	if got["pve2"] != "maintenance" {
		t.Errorf("pve2 = %q, want maintenance", got["pve2"])
	}
	if got["pve1"] != "online" {
		t.Errorf("pve1 = %q, want online", got["pve1"])
	}

	// Absent manager_status → empty (HA not configured).
	if n := len(parseNodeHAState(map[string]json.RawMessage{})); n != 0 {
		t.Errorf("expected empty map when manager_status absent, got %d", n)
	}
	// Malformed JSON → empty, no panic.
	bad := map[string]json.RawMessage{"manager_status": json.RawMessage(`not json`)}
	if n := len(parseNodeHAState(bad)); n != 0 {
		t.Errorf("expected empty map on malformed manager_status, got %d", n)
	}
}
