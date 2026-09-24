package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The four guest-firewall routes serve VMs AND containers: :vm_id is a
// Proxmox VMID, and the guest's vms row says which kind it is. Proxmox keeps
// the two firewalls in separate trees — /nodes/{node}/qemu/{vmid}/firewall
// and /nodes/{node}/lxc/{vmid}/firewall — so the request Proxmox receives
// must follow the row's type. Driven through the REAL declarations and
// handlers with the same stand-ins as registry_firewall_digest_test.go.

// guestFWRoute is one guest-firewall route, with the part of the Proxmox path
// after /nodes/pve-01/{type}/101/firewall/rules.
type guestFWRoute struct {
	fwRuleRoute
	suffix     string
	wantStatus int
}

func guestFWRoutes() []guestFWRoute {
	target := "/api/v1/clusters/" + testClusterID + "/vms/101/firewall/rules"
	return []guestFWRoute{
		{fwRuleRoute{name: "list", method: fiber.MethodGet, path: guestFirewallScope + "/rules", target: target,
			pick: func(n *handlers.NetworkHandler, _ *handlers.NodeHandler) Handler { return n.ListVMFirewallRules }},
			"", fiber.StatusOK},
		{fwRuleRoute{name: "create", method: fiber.MethodPost, path: guestFirewallScope + "/rules", target: target,
			pick: func(n *handlers.NetworkHandler, _ *handlers.NodeHandler) Handler { return n.CreateVMFirewallRule }},
			"", fiber.StatusCreated},
		{fwRuleRoute{name: "update", method: fiber.MethodPut, path: guestFirewallScope + "/rules/:pos", target: target + "/3",
			pick: func(n *handlers.NetworkHandler, _ *handlers.NodeHandler) Handler { return n.UpdateVMFirewallRule }},
			"/3", fiber.StatusOK},
		{fwRuleRoute{name: "delete", method: fiber.MethodDelete, path: guestFirewallScope + "/rules/:pos", target: target + "/3",
			pick: func(n *handlers.NetworkHandler, _ *handlers.NodeHandler) Handler { return n.DeleteVMFirewallRule }},
			"/3", fiber.StatusOK},
	}
}

func guestFWRequest(r guestFWRoute) *http.Request {
	var req *http.Request
	switch r.method {
	case fiber.MethodGet, fiber.MethodDelete:
		req = httptest.NewRequest(r.method, r.target, nil)
	default:
		req = jsonRequest(r.method, r.target, `{"type":"in","action":"ACCEPT","enable":1}`)
	}
	req.Header.Set("X-Test-User", "yes")
	return req
}

// TestGuestFirewallRoutesFollowTheGuestType: a VM row sends the request to the
// qemu tree and a container row to the lxc tree, on all four routes.
func TestGuestFirewallRoutesFollowTheGuestType(t *testing.T) {
	for _, r := range guestFWRoutes() {
		for _, guestType := range []string{"qemu", "lxc"} {
			t.Run(r.name+"/"+guestType, func(t *testing.T) {
				pve := &fwRulePVE{}
				app, dbtx := newFWRuleAppForGuest(t, r.fwRuleRoute, pve, guestType)

				if status, env := send(t, app, guestFWRequest(r)); status != r.wantStatus {
					t.Fatalf("status = %d (%s: %q), want %d", status, env.Error, env.Message, r.wantStatus)
				}
				w := pve.only(t)
				want := "/api2/json/nodes/pve-01/" + guestType + "/101/firewall/rules" + r.suffix
				if w.method != r.method || w.path != want {
					t.Fatalf("Proxmox received %s %s, want %s %s", w.method, w.path, r.method, want)
				}
				wantAudits := 1
				if r.method == fiber.MethodGet {
					wantAudits = 0
				}
				if n := dbtx.auditCount(); n != wantAudits {
					t.Errorf("recorded %d audit rows, want %d", n, wantAudits)
				}
			})
		}
	}
}

// TestGuestFirewallRoutesRefuseAnUnknownGuestType: a row whose type is
// neither "qemu" nor "lxc" reaches Proxmox on neither tree. It is Nexara's
// own inconsistency, so it is a 500 rather than a 400, and nothing is
// audited because nothing happened.
func TestGuestFirewallRoutesRefuseAnUnknownGuestType(t *testing.T) {
	for _, r := range guestFWRoutes() {
		t.Run(r.name, func(t *testing.T) {
			pve := &fwRulePVE{}
			app, dbtx := newFWRuleAppForGuest(t, r.fwRuleRoute, pve, "pve-01")

			if status, env := send(t, app, guestFWRequest(r)); status != fiber.StatusInternalServerError {
				t.Fatalf("status = %d (%s: %q), want 500", status, env.Error, env.Message)
			}
			pve.mu.Lock()
			n := len(pve.writes)
			pve.mu.Unlock()
			if n != 0 {
				t.Fatalf("Proxmox received %d requests, want none: %+v", n, pve.writes)
			}
			if a := dbtx.auditCount(); a != 0 {
				t.Errorf("recorded %d audit rows, want 0", a)
			}
		})
	}
}
