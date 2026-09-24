package proxmox

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// guestFirewallCall is one of the four guest-firewall methods, called with a
// guest type, and the method and path (sans /api2/json and query) it must use.
type guestFirewallCall struct {
	name   string
	method string
	suffix string // after /nodes/pve-01/{type}/101/firewall/rules
	call   func(c *Client, guestType string) error
}

func guestFirewallCalls() []guestFirewallCall {
	ctx := context.Background()
	rule := FirewallRuleParams{Type: "in", Action: "ACCEPT", Enable: 1}
	return []guestFirewallCall{
		{"list", http.MethodGet, "", func(c *Client, gt string) error {
			_, err := c.GetVMFirewallRules(ctx, "pve-01", gt, 101)
			return err
		}},
		{"create", http.MethodPost, "", func(c *Client, gt string) error {
			return c.CreateVMFirewallRule(ctx, "pve-01", gt, 101, rule)
		}},
		{"update", http.MethodPut, "/3", func(c *Client, gt string) error {
			return c.UpdateVMFirewallRule(ctx, "pve-01", gt, 101, 3, rule, "")
		}},
		{"delete", http.MethodDelete, "/3", func(c *Client, gt string) error {
			return c.DeleteVMFirewallRule(ctx, "pve-01", gt, 101, 3, "")
		}},
	}
}

// TestGuestFirewallAddressesTheGuestsOwnTree: a VM's rules live under
// /nodes/{node}/qemu/{vmid}/firewall/rules and a container's under
// /nodes/{node}/lxc/{vmid}/firewall/rules (qemu-server Qemu.pm registers
// PVE::API2::Firewall::VM, pve-container LXC.pm PVE::API2::Firewall::CT). Each
// of the four methods must build the tree of the type it was given.
func TestGuestFirewallAddressesTheGuestsOwnTree(t *testing.T) {
	for _, call := range guestFirewallCalls() {
		for _, guestType := range []string{"qemu", "lxc"} {
			t.Run(call.name+"/"+guestType, func(t *testing.T) {
				srv, seen := newFWDigestCaptureServer(t)
				if err := call.call(newTestClient(t, srv.URL), guestType); err != nil {
					t.Fatalf("call: %v", err)
				}
				if len(*seen) != 1 {
					t.Fatalf("issued %d requests, want 1", len(*seen))
				}
				want := "/api2/json/nodes/pve-01/" + guestType + "/101/firewall/rules" + call.suffix
				if got := (*seen)[0]; got.method != call.method || got.path != want {
					t.Fatalf("sent %s %s, want %s %s", got.method, got.path, call.method, want)
				}
			})
		}
	}
}

// TestGuestFirewallRefusesAnUnknownGuestType: a type that is neither "qemu"
// nor "lxc" is refused before anything is sent — not defaulted to the VM
// tree. The empty string is in the table because it is the zero value a
// row that never had its type filled would carry.
//
// The precondition twin (the "qemu" row through the same server) proves the
// server would have recorded a request had one been sent, so the zero count
// below is the refusal's doing.
func TestGuestFirewallRefusesAnUnknownGuestType(t *testing.T) {
	for _, call := range guestFirewallCalls() {
		t.Run(call.name+"/precondition", func(t *testing.T) {
			srv, seen := newFWDigestCaptureServer(t)
			if err := call.call(newTestClient(t, srv.URL), "qemu"); err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(*seen) != 1 {
				t.Fatalf("a known type issued %d requests, want 1", len(*seen))
			}
		})
		for _, guestType := range []string{"", "vm", "ct", "QEMU", "pve-01", "qemu/../lxc"} {
			t.Run(call.name+"/"+guestType, func(t *testing.T) {
				srv, seen := newFWDigestCaptureServer(t)
				err := call.call(newTestClient(t, srv.URL), guestType)
				if !errors.Is(err, ErrUnknownGuestType) {
					t.Fatalf("err = %v, want ErrUnknownGuestType", err)
				}
				// Nexara's bug, not the caller's: it must not read as a 400.
				if errors.Is(err, ErrInvalidInput) {
					t.Errorf("err = %v wraps ErrInvalidInput; an unknown guest type is not the caller's fault", err)
				}
				if len(*seen) != 0 {
					t.Fatalf("issued %d requests for type %q, want none", len(*seen), guestType)
				}
			})
		}
	}
}
