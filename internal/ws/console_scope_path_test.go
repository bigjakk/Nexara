package ws

import (
	"slices"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"

	"github.com/bigjakk/nexara/internal/auth"
)

// TestConsoleHandlers_OpenWhatTheScopeNames drives every console type through
// the stand-in Proxmox with a query string that names something else in every
// field, and pins the Proxmox paths the handler opened: the proxy ticket and
// the websocket, both for the node and the guest the validated scope names.
//
// The decoy kind is spelled the way each ROUTE spells a kind in its query —
// expectedQueryTypeForScope — because that is what a handler reading the query
// would act on: /ws/vnc says "lxc" for a container and nothing for a VM, so a
// vm_vnc scope is decoyed with ?type=lxc, the switch from a VM to a container
// sharing its VMID that 5f5c422 closed.
//
// The gate tests see only which CLUSTER a handler looks up — their stand-in
// database stops it there. This is the half that reaches Proxmox, where the
// node, the guest and the guest's KIND become a path, so a handler that went
// back to reading any of them from the request would open a console on
// something the token never named, and this is the test that sees it.
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
