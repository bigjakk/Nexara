package ws

import (
	"slices"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"

	"github.com/bigjakk/nexara/internal/auth"
)

// TestConsoleHandlers_OpenWhatTheScopeNames: every console type, with a query naming something
// else in every field (kind spelled as each route spells it, e.g. ?type=lxc against a vm_vnc
// scope), must open the proxy ticket and websocket for the node and guest the scope names. The
// gate tests see only the cluster; this sees the node, the guest and its kind in the path.
func TestConsoleHandlers_OpenWhatTheScopeNames(t *testing.T) {
	scope := func(kind string) auth.ConsoleScope {
		s := auth.ConsoleScope{ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: kind}
		if kind == "node_shell" {
			s.VMID = 0 // a node shell's scope carries no guest; the mint refuses one
		}
		return s
	}
	for _, tc := range []struct {
		handler string
		scope   auth.ConsoleScope
		decoy   string // the other kind the query claims
		want    []string
	}{
		{"vnc", scope("vm_vnc"), "ct_vnc", []string{
			"POST /api2/json/nodes/pve-01/qemu/100/vncproxy",
			"GET /api2/json/nodes/pve-01/qemu/100/vncwebsocket",
		}},
		{"vnc", scope("ct_vnc"), "vm_vnc", []string{
			"POST /api2/json/nodes/pve-01/lxc/100/vncproxy",
			"GET /api2/json/nodes/pve-01/lxc/100/vncwebsocket",
		}},
		{"console", scope("vm_serial"), "node_shell", []string{
			"POST /api2/json/nodes/pve-01/qemu/100/vncproxy",
			"GET /api2/json/nodes/pve-01/qemu/100/vncwebsocket",
		}},
		{"console", scope("ct_attach"), "vm_serial", []string{
			"POST /api2/json/nodes/pve-01/lxc/100/vncproxy",
			"GET /api2/json/nodes/pve-01/lxc/100/vncwebsocket",
		}},
		{"console", scope("node_shell"), "ct_attach", []string{
			"POST /api2/json/nodes/pve-01/termproxy",
			"GET /api2/json/nodes/pve-01/vncwebsocket",
		}},
	} {
		t.Run(tc.handler+"/"+tc.scope.Type, func(t *testing.T) {
			t.Parallel()
			// Proxmox hangs up as soon as the websocket opens: the paths are
			// all this test is after, and they are recorded by then.
			pve := newFakeProxmox(t, func(*gorillaws.Conn) {})
			query := "?cluster_id=" + gateClusterB + "&node=pve-02&vmid=999"
			if decoy := expectedQueryTypeForScope(tc.decoy); decoy != "" {
				query += "&type=" + decoy
			}
			browser := openSession(t, tc.handler, tc.scope, pve, query)

			_ = browser.SetReadDeadline(time.Now().Add(closeDrainTimeout + 5*time.Second))
			for {
				if _, _, err := browser.ReadMessage(); err != nil {
					break
				}
			}
			if got := pve.requests(); !slices.Equal(got, tc.want) {
				t.Errorf("Proxmox was sent %q, want %q — the handler must open exactly what the validated "+
					"scope names, whatever the query says", got, tc.want)
			}
		})
	}
}
