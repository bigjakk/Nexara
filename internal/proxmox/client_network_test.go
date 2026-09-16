package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newFormCaptureServer records the decoded request body of each call, so the
// tests below can assert on the exact parameters Proxmox would receive.
func newFormCaptureServer(t *testing.T) (*httptest.Server, *[]url.Values) {
	t.Helper()
	var seen []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		seen = append(seen, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestCreateNetworkInterface_SendsEveryOption(t *testing.T) {
	// The gap this closes: the create form used to carry six keys, so a bond's
	// mode or a bridge's MTU could be filled in and silently dropped.
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	err := c.CreateNetworkInterface(context.Background(), "pve1", CreateNetworkInterfaceParams{
		Iface: "bond1",
		Type:  "bond",
		NetworkInterfaceOptions: NetworkInterfaceOptions{
			CIDR:               "10.0.0.2/24",
			Gateway:            "10.0.0.1",
			CIDR6:              "fd00::2/64",
			Gateway6:           "fd00::1",
			Autostart:          1,
			Comments:           "uplink",
			MTU:                9000,
			Slaves:             "eno1 eno2",
			BondMode:           "802.3ad",
			BondXmitHashPolicy: "layer3+4",
		},
	})
	if err != nil {
		t.Fatalf("CreateNetworkInterface: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("issued %d requests, want 1", len(*seen))
	}

	want := map[string]string{
		"iface":                 "bond1",
		"type":                  "bond",
		"cidr":                  "10.0.0.2/24",
		"gateway":               "10.0.0.1",
		"cidr6":                 "fd00::2/64",
		"gateway6":              "fd00::1",
		"autostart":             "1",
		"comments":              "uplink",
		"mtu":                   "9000",
		"slaves":                "eno1 eno2",
		"bond_mode":             "802.3ad",
		"bond_xmit_hash_policy": "layer3+4",
	}
	form := (*seen)[0]
	for k, v := range want {
		if got := form.Get(k); got != v {
			t.Errorf("form[%q] = %q, want %q", k, got, v)
		}
	}
	// Unset options must stay off the wire: Proxmox rejects several of these
	// when present but empty.
	for _, k := range []string{"ovs_bridge", "ovs_tag", "vlan-id", "address", "bond-primary"} {
		if _, ok := form[k]; ok {
			t.Errorf("form carries %q = %q, want it omitted", k, form.Get(k))
		}
	}
}

func TestNetworkInterfaceForm_FlagsFollowProxmoxSemantics(t *testing.T) {
	// Proxmox's own dialog splits these two: autostart is a checkbox with an
	// unchecked value of 0, so off is sent as 0; bridge_vlan_aware is
	// deleteEmpty, so off is expressed by omitting the key (and cleared on an
	// existing bridge via `delete`). Getting this backwards either fails to
	// turn VLAN-awareness off or writes a stray flag onto non-bridge types.
	tests := []struct {
		name                 string
		opts                 NetworkInterfaceOptions
		wantAutostart        string
		wantVLANAwarePresent bool
	}{
		{"vlan aware on", NetworkInterfaceOptions{Autostart: 1, BridgeVLANAware: 1}, "1", true},
		{"vlan aware off", NetworkInterfaceOptions{Autostart: 1, BridgeVLANAware: 0}, "1", false},
		{"autostart off is explicit", NetworkInterfaceOptions{Autostart: 0}, "0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			networkIfaceOptionsToForm(form, tt.opts)

			if got := form.Get("autostart"); got != tt.wantAutostart {
				t.Errorf("autostart = %q, want %q", got, tt.wantAutostart)
			}
			if _, got := form["bridge_vlan_aware"]; got != tt.wantVLANAwarePresent {
				t.Errorf("bridge_vlan_aware present = %v, want %v", got, tt.wantVLANAwarePresent)
			}
		})
	}
}

func TestUpdateNetworkInterface_ClearsVLANAwareViaDelete(t *testing.T) {
	// Turning VLAN-awareness off on an existing bridge must name the key in
	// `delete`; sending bridge_vlan_aware=0 would rely on Proxmox's writer
	// treating a stored zero as falsy.
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	err := c.UpdateNetworkInterface(context.Background(), "pve1", "vmbr0", UpdateNetworkInterfaceParams{
		Type:   "bridge",
		Delete: []string{"bridge_vlan_aware"},
	})
	if err != nil {
		t.Fatalf("UpdateNetworkInterface: %v", err)
	}
	if got := (*seen)[0].Get("delete"); got != "bridge_vlan_aware" {
		t.Errorf("delete = %q, want bridge_vlan_aware", got)
	}
	if _, ok := (*seen)[0]["bridge_vlan_aware"]; ok {
		t.Error("bridge_vlan_aware sent alongside its own delete")
	}
}

func TestEditableTypesCoverEveryCreatableType(t *testing.T) {
	// A type you can create but not edit is a bug that only shows up long
	// after the create path was verified.
	for ifaceType := range creatableNetworkInterfaceTypes {
		if err := validateEditableNetworkInterfaceType(ifaceType); err != nil {
			t.Errorf("creatable type %q is not editable: %v", ifaceType, err)
		}
	}
}

func TestUpdateNetworkInterface_SendsDeleteList(t *testing.T) {
	// Proxmox ignores an empty value, so clearing a gateway means naming it in
	// `delete`. Without this the field appears cleared in the UI and comes
	// straight back on the next refresh.
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	err := c.UpdateNetworkInterface(context.Background(), "pve1", "vmbr0", UpdateNetworkInterfaceParams{
		Type:                    "bridge",
		NetworkInterfaceOptions: NetworkInterfaceOptions{CIDR: "10.0.0.2/24", Autostart: 1},
		Delete:                  []string{"gateway", "comments"},
	})
	if err != nil {
		t.Fatalf("UpdateNetworkInterface: %v", err)
	}
	if got := (*seen)[0].Get("delete"); got != "gateway,comments" {
		t.Errorf("delete = %q, want %q", got, "gateway,comments")
	}
}

func TestUpdateNetworkInterface_OmitsDeleteWhenNothingCleared(t *testing.T) {
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	err := c.UpdateNetworkInterface(context.Background(), "pve1", "vmbr0", UpdateNetworkInterfaceParams{
		Type: "bridge",
	})
	if err != nil {
		t.Fatalf("UpdateNetworkInterface: %v", err)
	}
	if _, ok := (*seen)[0]["delete"]; ok {
		t.Error("delete parameter present with an empty Delete list")
	}
}

func TestUpdateNetworkInterface_RejectsDotDot(t *testing.T) {
	// PUT /nodes/pve1/network/.. collapses to Proxmox's apply-pending-config
	// endpoint, which Nexara gates behind a different permission.
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	if err := c.UpdateNetworkInterface(context.Background(), "pve1", "..", UpdateNetworkInterfaceParams{Type: "bridge"}); err == nil {
		t.Fatal(`UpdateNetworkInterface("..") succeeded, want rejection`)
	}
	if len(*seen) != 0 {
		t.Errorf("issued %d request(s), want none", len(*seen))
	}
}

func TestUpdateNetworkInterface_RejectsUndeletableKeys(t *testing.T) {
	// `delete` takes raw Proxmox option names, so it is allow-listed: deleting
	// `type` or `iface` would corrupt the interface definition.
	//
	// Driven through UpdateNetworkInterface rather than the validator, because
	// the point of the check living in the client is that no caller can skip
	// it: a test that called the validator directly would still pass with the
	// call site deleted, which is the bug this shape exists to prevent.
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	for _, key := range []string{"type", "iface", "", "gateway,type", "../"} {
		err := c.UpdateNetworkInterface(context.Background(), "pve1", "vmbr0", UpdateNetworkInterfaceParams{
			Type:   "bridge",
			Delete: []string{key},
		})
		if err == nil {
			t.Errorf("UpdateNetworkInterface(delete=%q) accepted, want rejection", key)
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("UpdateNetworkInterface(delete=%q) = %v, want ErrInvalidInput", key, err)
		}
	}
	// Rejected before the request is built, so nothing reaches the node.
	if len(*seen) != 0 {
		t.Errorf("issued %d request(s), want none", len(*seen))
	}

	// Allow-listed keys still go through: without this the test would pass
	// just as well with every key rejected.
	if err := c.UpdateNetworkInterface(context.Background(), "pve1", "vmbr0", UpdateNetworkInterfaceParams{
		Type:   "bridge",
		Delete: []string{"gateway", "mtu", "bond-primary"},
	}); err != nil {
		t.Errorf("valid keys rejected: %v", err)
	}
}

func TestCreateNetworkInterface_RejectsUncreatableType(t *testing.T) {
	// eth, OVSPort, alias and unknown describe interfaces you don't create by
	// hand — but they ARE in PVE's own type enum, so a POST carrying one is
	// schema-valid and lands in the pending config. Nothing but this check
	// stops it, which is why it has to be in the client and not in one caller.
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	for _, bad := range []string{"eth", "OVSPort", "alias", "unknown", "", "Bridge"} {
		err := c.CreateNetworkInterface(context.Background(), "pve1", CreateNetworkInterfaceParams{
			Iface: "vmbr9",
			Type:  bad,
		})
		if err == nil {
			t.Errorf("CreateNetworkInterface(type=%q) accepted, want rejection", bad)
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("CreateNetworkInterface(type=%q) = %v, want ErrInvalidInput", bad, err)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("issued %d request(s), want none", len(*seen))
	}

	// The creatable set still goes through, or the check above would pass just
	// as well with every type rejected.
	for _, ok := range []string{"bridge", "bond", "vlan", "OVSBridge", "OVSBond", "OVSIntPort"} {
		if err := c.CreateNetworkInterface(context.Background(), "pve1", CreateNetworkInterfaceParams{
			Iface: "vmbr9",
			Type:  ok,
		}); err != nil {
			t.Errorf("type %q rejected for create: %v", ok, err)
		}
	}
}

func TestUpdateNetworkInterface_RejectsUnknownType(t *testing.T) {
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	for _, bad := range []string{"nonsense", "", "Bridge"} {
		err := c.UpdateNetworkInterface(context.Background(), "pve1", "vmbr0", UpdateNetworkInterfaceParams{Type: bad})
		if err == nil {
			t.Errorf("UpdateNetworkInterface(type=%q) accepted, want rejection", bad)
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("UpdateNetworkInterface(type=%q) = %v, want ErrInvalidInput", bad, err)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("issued %d request(s), want none", len(*seen))
	}

	// The edit path is wider — a physical NIC can still be given an address.
	for _, ok := range []string{"eth", "OVSPort", "bridge", "vlan"} {
		if err := c.UpdateNetworkInterface(context.Background(), "pve1", "vmbr0", UpdateNetworkInterfaceParams{Type: ok}); err != nil {
			t.Errorf("type %q rejected for edit: %v", ok, err)
		}
	}
}

func TestNetworkInterfaceOptions_RangeCheckedOnBothWritePaths(t *testing.T) {
	tests := []struct {
		name    string
		opts    NetworkInterfaceOptions
		wantErr bool
	}{
		{"zero means unset", NetworkInterfaceOptions{}, false},
		{"mtu at lower bound", NetworkInterfaceOptions{MTU: 1280}, false},
		{"mtu at upper bound", NetworkInterfaceOptions{MTU: 65520}, false},
		{"mtu below bound", NetworkInterfaceOptions{MTU: 1279}, true},
		{"mtu above bound", NetworkInterfaceOptions{MTU: 65521}, true},
		{"vlan tag in range", NetworkInterfaceOptions{VLANID: 4094}, false},
		{"vlan tag above range", NetworkInterfaceOptions{VLANID: 4095}, true},
		{"ovs tag negative", NetworkInterfaceOptions{OVSTag: -1}, true},
	}
	// Create and update both carry these options, so both are driven here: a
	// check only one path ran would leave the other unguarded, and a test that
	// called the validator directly would pass with either call site deleted.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newFormCaptureServer(t)
			c := newTestClient(t, srv.URL)

			errs := map[string]error{
				"create": c.CreateNetworkInterface(context.Background(), "pve1", CreateNetworkInterfaceParams{
					Iface:                   "vmbr9",
					Type:                    "bridge",
					NetworkInterfaceOptions: tt.opts,
				}),
				"update": c.UpdateNetworkInterface(context.Background(), "pve1", "vmbr0", UpdateNetworkInterfaceParams{
					Type:                    "bridge",
					NetworkInterfaceOptions: tt.opts,
				}),
			}
			for op, err := range errs {
				if (err != nil) != tt.wantErr {
					t.Errorf("%s error = %v, wantErr %v", op, err, tt.wantErr)
				}
				if err != nil && !errors.Is(err, ErrInvalidInput) {
					t.Errorf("%s error = %v, want ErrInvalidInput", op, err)
				}
			}
			switch {
			case tt.wantErr && len(*seen) != 0:
				t.Errorf("issued %d request(s) for rejected options, want none", len(*seen))
			case !tt.wantErr && len(*seen) != 2:
				t.Errorf("issued %d request(s), want 2 (create + update)", len(*seen))
			}
		})
	}
}

func TestGetNetworkInterfaces_DecodesFullInterface(t *testing.T) {
	// Proxmox is inconsistent about quoting numbers, and the fields added for
	// the richer form are exactly the ones prone to it.
	body := `{"data":[{
		"iface":"vmbr0","type":"bridge","active":1,"autostart":1,
		"cidr":"192.0.2.40/24","gateway":"192.0.2.1",
		"cidr6":"fd00::2/64","gateway6":"fd00::1","netmask6":64,
		"bridge_ports":"bond0","bridge_vlan_aware":1,"bridge_vids":"2 4 100-200",
		"mtu":"9000","comments":"uplink"
	},{
		"iface":"bond0","type":"bond","active":1,"autostart":1,
		"slaves":"eno1 eno2","bond_mode":"802.3ad","bond_xmit_hash_policy":"layer3+4",
		"bond-primary":"eno1","mtu":9000
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	ifaces, err := newTestClient(t, srv.URL).GetNetworkInterfaces(context.Background(), "pve1")
	if err != nil {
		t.Fatalf("GetNetworkInterfaces: %v", err)
	}
	if len(ifaces) != 2 {
		t.Fatalf("decoded %d interfaces, want 2", len(ifaces))
	}

	br := ifaces[0]
	if br.CIDR6 != "fd00::2/64" || br.Gateway6 != "fd00::1" {
		t.Errorf("IPv6 = %q / %q, want fd00::2/64 / fd00::1", br.CIDR6, br.Gateway6)
	}
	if br.Netmask6.String() != "64" {
		t.Errorf("Netmask6 = %q, want 64", br.Netmask6)
	}
	if br.BridgeVLANAware != 1 || br.BridgeVIDs.String() != "2 4 100-200" {
		t.Errorf("VLAN aware = %d, vids = %q", br.BridgeVLANAware, br.BridgeVIDs)
	}
	if br.MTU != 9000 { // arrived quoted
		t.Errorf("MTU = %d, want 9000", br.MTU)
	}

	bond := ifaces[1]
	if bond.BondMode != "802.3ad" || bond.BondXmitHashPolicy != "layer3+4" {
		t.Errorf("bond mode = %q, hash policy = %q", bond.BondMode, bond.BondXmitHashPolicy)
	}
	if bond.BondPrimary != "eno1" {
		t.Errorf("bond-primary = %q, want eno1", bond.BondPrimary)
	}
	if bond.Slaves != "eno1 eno2" {
		t.Errorf("slaves = %q, want %q", bond.Slaves, "eno1 eno2")
	}
	if bond.MTU != 9000 { // arrived as a number
		t.Errorf("MTU = %d, want 9000", bond.MTU)
	}
}

func TestNetworkIfaceOptionsToForm_SkipsEmptyStrings(t *testing.T) {
	form := url.Values{}
	networkIfaceOptionsToForm(form, NetworkInterfaceOptions{CIDR: "10.0.0.1/24"})

	for key, values := range form {
		if key == "autostart" {
			continue // deliberately always sent, including as 0
		}
		if strings.Join(values, "") == "" {
			t.Errorf("form carries empty %q", key)
		}
	}
	if form.Get("cidr") != "10.0.0.1/24" {
		t.Errorf("cidr = %q, want 10.0.0.1/24", form.Get("cidr"))
	}
}
