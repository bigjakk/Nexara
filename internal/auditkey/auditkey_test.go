package auditkey

import (
	"strings"
	"testing"
)

func TestValidID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, id string
		want     bool
	}{
		{"a name", "pool01", true},
		{"empty", "", false},
		{"a control character", "pool\n01", false},
		{"at the bound", strings.Repeat("p", MaxIDLen), true},
		{"past the bound", strings.Repeat("p", MaxIDLen+1), false},
	} {
		if got := ValidID(tc.id); got != tc.want {
			t.Errorf("%s: ValidID = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// ACLPath mirrors PVE::AccessControl::normalize_path, whose edge is the root: "/"
// and "//" are both "/", and only a trailing slash after a segment goes.
func TestACLPath(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/vms/100":   "/vms/100",
		"/vms/100/":  "/vms/100",
		"/vms//100":  "/vms/100",
		"//vms///x/": "/vms/x",
		"/":          "/",
		"//":         "/",
	} {
		if got := ACLPath(in); got != want {
			t.Errorf("ACLPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirewallName(t *testing.T) {
	t.Parallel()
	if got := FirewallName("MyNet"); got != "mynet" {
		t.Errorf("FirewallName(MyNet) = %q, want mynet", got)
	}
}

// IPSetCIDR is what create_ip stores: clean_cidr's host bits cleared and address
// compressed, the prefix of a single host dropped, and an alias reference or
// anything unreadable left as sent.
func TestIPSetCIDR(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"192.0.2.5/24":            "192.0.2.0/24",
		"192.0.2.0/24":            "192.0.2.0/24",
		"192.0.2.5":               "192.0.2.5",
		"192.0.2.5/32":            "192.0.2.5",
		"2001:db8::1/128":         "2001:db8::1",
		"2001:DB8:0:0:0:0:0:5/64": "2001:db8::/64",
		"2001:db8::/64":           "2001:db8::/64",
		"dc/MyNet":                "dc/MyNet",
		"guest/web-01":            "guest/web-01",
		"MyNet":                   "MyNet",
		"10.0.0.0/0":              "10.0.0.0/0",
		"192.0.2.5/33":            "192.0.2.5/33",
		"not an address/24":       "not an address/24",
		"192.0.2.5/":              "192.0.2.5/",
		"fe80::1%eth0/64":         "fe80::1%eth0/64",
	} {
		if got := IPSetCIDR(in); got != want {
			t.Errorf("IPSetCIDR(%q) = %q, want %q", in, got, want)
		}
	}
	if got := IPSetEntry("MySet", "192.0.2.5/24"); got != "myset/192.0.2.0/24" {
		t.Errorf("IPSetEntry = %q, want the lower-cased set and the cleaned entry", got)
	}
}

// The HA sid of a bare VMID: only the canonical digits of a VMID name a guest, a
// guest's type picks vm: or ct:, and Proxmox's own answer is taken only as this
// VMID under one of the two.
func TestHASIDHelpers(t *testing.T) {
	t.Parallel()
	for sid, want := range map[string]bool{"109": true, "0109": false, "-1": false, "0": false, "1e2": false, "vm:109": false, "": false} {
		if _, ok := BareVMID(sid); ok != want {
			t.Errorf("BareVMID(%q) ok = %v, want %v", sid, ok, want)
		}
	}
	for typ, want := range map[string]string{"qemu": "vm:109", "lxc": "ct:109", "": "", "vm": ""} {
		if got, _ := HASID("109", typ); got != want {
			t.Errorf("HASID(109, %q) = %q, want %q", typ, got, want)
		}
	}
	for reported, want := range map[string]string{"vm:109": "vm:109", "ct:109": "ct:109", "vm:999": "", "ha:109": "", "": ""} {
		if got, _ := ReportedHASID("109", reported); got != want {
			t.Errorf("ReportedHASID(109, %q) = %q, want %q", reported, got, want)
		}
	}
}

func TestSDNSubnet(t *testing.T) {
	t.Parallel()
	if got := SDNSubnet("vnet01", "zone01-192.0.2.0-24"); got != "vnet01/zone01-192.0.2.0-24" {
		t.Errorf("SDNSubnet = %q", got)
	}
	listed := []string{"zone01-198.51.100.0-24", "zone01-192.0.2.0-24"}
	if id, ok := SDNSubnetID("192.0.2.0/24", listed); !ok || id != "zone01-192.0.2.0-24" {
		t.Errorf("SDNSubnetID = (%q, %v), want the entry ending in the dashed CIDR", id, ok)
	}
	if id, ok := SDNSubnetID("203.0.113.0/24", listed); ok {
		t.Errorf("SDNSubnetID found %q for a CIDR nothing lists", id)
	}
	// A shorter mask is another subnet: the dashed CIDR has to END the id.
	if id, ok := SDNSubnetID("192.0.2.0/2", listed); ok {
		t.Errorf("SDNSubnetID found %q for a prefix of a listed CIDR", id)
	}
}
