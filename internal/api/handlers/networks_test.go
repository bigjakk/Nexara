package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// The create/update request structs embed proxmox.NetworkInterfaceOptions so
// the two share one field list. That only works if Fiber's body binding
// promotes flat JSON into the embedded struct — if it ever stopped, every
// option beyond iface/type would bind to its zero value and be dropped by the
// form builder, silently, with the request still returning 201.
func TestBindBody_PromotesEmbeddedNetworkOptions(t *testing.T) {
	app := fiber.New()
	var got proxmox.CreateNetworkInterfaceParams
	app.Post("/t", func(c fiber.Ctx) error {
		if err := c.Bind().Body(&got); err != nil {
			return err
		}
		return c.SendStatus(fiber.StatusOK)
	})

	body := `{"iface":"bond1","type":"bond","cidr":"10.0.0.2/24","gateway6":"fd00::1",
	          "mtu":9000,"bond_mode":"802.3ad","bond-primary":"eno1","ovs_tag":100,
	          "vlan-id":42,"bridge_vlan_aware":1,"autostart":1}`
	req := httptest.NewRequest(fiber.MethodPost, "/t", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	checks := []struct {
		name      string
		got, want any
	}{
		{"Iface", got.Iface, "bond1"},
		{"Type", got.Type, "bond"},
		{"CIDR", got.CIDR, "10.0.0.2/24"},
		{"Gateway6", got.Gateway6, "fd00::1"},
		{"MTU", got.MTU, 9000},
		{"BondMode", got.BondMode, "802.3ad"},
		// Hyphenated keys are the ones most likely to break on a binder swap.
		{"BondPrimary", got.BondPrimary, "eno1"},
		{"VLANID", got.VLANID, 42},
		{"OVSTag", got.OVSTag, 100},
		{"BridgeVLANAware", got.BridgeVLANAware, 1},
		{"Autostart", got.Autostart, 1},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestBindBody_PromotesUpdateDeleteList(t *testing.T) {
	app := fiber.New()
	var got proxmox.UpdateNetworkInterfaceParams
	app.Put("/t", func(c fiber.Ctx) error {
		if err := c.Bind().Body(&got); err != nil {
			return err
		}
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(fiber.MethodPut, "/t",
		strings.NewReader(`{"type":"bridge","cidr":"10.0.0.2/24","delete":["gateway","mtu"]}`))
	req.Header.Set("Content-Type", "application/json")
	if _, err := app.Test(req); err != nil {
		t.Fatalf("app.Test: %v", err)
	}

	if got.CIDR != "10.0.0.2/24" {
		t.Errorf("CIDR = %q, want 10.0.0.2/24", got.CIDR)
	}
	if len(got.Delete) != 2 || got.Delete[0] != "gateway" || got.Delete[1] != "mtu" {
		t.Errorf("Delete = %v, want [gateway mtu]", got.Delete)
	}
}

// networkAuditSettings feeds an audit row that any Viewer can read, while
// reading the interfaces themselves needs view:network. It must therefore name
// the settings that changed and never carry their values.
func TestNetworkAuditSettings_NamesKeysWithoutValues(t *testing.T) {
	got := networkAuditSettings(proxmox.NetworkInterfaceOptions{
		CIDR:     "10.0.0.2/24",
		Gateway:  "10.0.0.1",
		Comments: "uplink",
		MTU:      9000,
		BondMode: "802.3ad",
	})

	want := map[string]bool{"cidr": true, "gateway": true, "comments": true, "mtu": true, "bond_mode": true}
	if len(got) != len(want) {
		t.Fatalf("settings = %v, want %d entries", got, len(want))
	}
	for _, key := range got {
		if !want[key] {
			t.Errorf("unexpected setting %q", key)
		}
		// No entry may carry the value it names.
		for _, secret := range []string{"10.0.0.2/24", "10.0.0.1", "uplink", "9000", "802.3ad"} {
			if strings.Contains(key, secret) {
				t.Errorf("setting %q leaks the value %q", key, secret)
			}
		}
	}
}

func TestNetworkAuditSettings_EmptyOptionsNameNothing(t *testing.T) {
	if got := networkAuditSettings(proxmox.NetworkInterfaceOptions{}); len(got) != 0 {
		t.Errorf("settings = %v, want none", got)
	}
}
