package ws

import (
	"testing"

	"github.com/bigjakk/nexara/internal/auth"
)

func TestValidateConsoleScopeFields(t *testing.T) {
	const clusterID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	baseVNC := &auth.ConsoleScope{
		ClusterID: clusterID,
		Node:      "pve1",
		VMID:      100,
		Type:      "vm_vnc",
	}
	baseCTVNC := &auth.ConsoleScope{
		ClusterID: clusterID,
		Node:      "pve1",
		VMID:      200,
		Type:      "ct_vnc",
	}
	baseShell := &auth.ConsoleScope{
		ClusterID: clusterID,
		Node:      "pve1",
		Type:      "node_shell",
	}

	// Note on the `typeStr` column: this is the value of the `?type=` query
	// param on the WS URL, NOT the scope type. The two differ for VNC:
	//   - vm_vnc → query type is empty (omitted)
	//   - ct_vnc → query type is "lxc"
	//   - terminal types match the scope type literally
	tests := []struct {
		name      string
		path      string
		clusterID string
		node      string
		vmid      string
		typeStr   string
		scope     *auth.ConsoleScope
		wantErr   bool
	}{
		// Happy paths
		{"vm_vnc match (empty query type)", "/ws/vnc", clusterID, "pve1", "100", "", baseVNC, false},
		{"ct_vnc match (lxc query type)", "/ws/vnc", clusterID, "pve1", "200", "lxc", baseCTVNC, false},
		{"node_shell match", "/ws/console", clusterID, "pve1", "", "node_shell", baseShell, false},

		// Path/type mismatch
		{"vm_vnc on console path", "/ws/console", clusterID, "pve1", "100", "", baseVNC, true},
		{"node_shell on vnc path", "/ws/vnc", clusterID, "pve1", "", "node_shell", baseShell, true},

		// The token carries the lowercase id the console-token route's uuid
		// format produced; the query carries whatever the client sent. An
		// uppercase copy of the SAME id is the same cluster, and the console
		// and VNC handlers resolve it with uuid.Parse to exactly that.
		{"cluster id in upper case", "/ws/vnc", "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA", "pve1", "100", "", baseVNC, false},

		// Param mismatches
		{"wrong cluster", "/ws/vnc", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "pve1", "100", "", baseVNC, true},
		{"wrong cluster in upper case", "/ws/vnc", "BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB", "pve1", "100", "", baseVNC, true},
		{"cluster id that is not a uuid", "/ws/vnc", "not-a-uuid", "pve1", "100", "", baseVNC, true},
		{"empty cluster id", "/ws/vnc", "", "pve1", "100", "", baseVNC, true},
		// uuid.Parse accepts these spellings of the same id; the console-token
		// route does not, and neither does this check.
		{"braced cluster id", "/ws/vnc", "{" + clusterID + "}", "pve1", "100", "", baseVNC, true},
		{"urn cluster id", "/ws/vnc", "urn:uuid:" + clusterID, "pve1", "100", "", baseVNC, true},
		{"undashed cluster id", "/ws/vnc", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "pve1", "100", "", baseVNC, true},
		{"garbage-wrapped cluster id", "/ws/vnc", "\n" + clusterID + "\x00", "pve1", "100", "", baseVNC, true},
		// Two values that do not parse are not a match, even when they are
		// the same text.
		{"unparsable id on both sides", "/ws/vnc", "not-a-uuid", "pve1", "100", "",
			&auth.ConsoleScope{ClusterID: "not-a-uuid", Node: "pve1", VMID: 100, Type: "vm_vnc"}, true},
		{"wrong node", "/ws/vnc", clusterID, "pve2", "100", "", baseVNC, true},
		{"wrong vmid", "/ws/vnc", clusterID, "pve1", "101", "", baseVNC, true},
		{"vm_vnc with stray lxc query type", "/ws/vnc", clusterID, "pve1", "100", "lxc", baseVNC, true},
		{"ct_vnc with empty query type", "/ws/vnc", clusterID, "pve1", "200", "", baseCTVNC, true},
		{"vmid on node_shell", "/ws/console", clusterID, "pve1", "50", "node_shell", baseShell, true},
		{"missing vmid for vnc", "/ws/vnc", clusterID, "pve1", "", "", baseVNC, true},
		{"garbage vmid", "/ws/vnc", clusterID, "pve1", "not-a-number", "", baseVNC, true},

		// Invalid scope type
		{"unknown scope type", "/ws/vnc", clusterID, "pve1", "100", "", &auth.ConsoleScope{ClusterID: clusterID, Node: "pve1", VMID: 100, Type: "bogus"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConsoleScopeFields(tt.path, tt.clusterID, tt.node, tt.vmid, tt.typeStr, tt.scope)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConsoleScopeFields() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
