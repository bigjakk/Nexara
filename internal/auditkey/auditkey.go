// Package auditkey spells the keys the objects of the cluster page are audited
// under. An object's History reads its rows by (cluster, resource_type,
// resource_id), so every writer — a handler recording a change made through
// Nexara, the collector filing a task it ingested, a diff of what Proxmox
// reports — has to spell the id the way Proxmox and the UI do, or one object
// files under as many keys as it has spellings. These functions are that
// spelling: pure, free of I/O, and free of any dependency on the handlers, so
// each writer calls the one definition.
package auditkey

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// MaxIDLen bounds a resource id taken from Proxmox before it is filed.
const MaxIDLen = 256

// ValidID reports whether id can be filed as an audit resource_id: not empty,
// not longer than MaxIDLen, no control character. An id read from a node's own
// lists is still a name somebody typed (a pool, an alias), and one that fails
// this is not filed under its own key.
func ValidID(id string) bool {
	if id == "" || len(id) > MaxIDLen {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// aclRepeatedSlashes is what PVE::AccessControl::normalize_path collapses.
var aclRepeatedSlashes = regexp.MustCompile(`/{2,}`)

// ACLPath is the path an ACL is listed under. Proxmox normalises it on the way
// in (normalize_path in pve-access-control: repeated slashes collapse, a
// trailing one goes, the root stays "/"), so "/vms/100/" and "/vms//100" both
// write the ACL at "/vms/100". Audited as sent, one path would file under every
// spelling its callers used.
func ACLPath(path string) string {
	key := aclRepeatedSlashes.ReplaceAllString(path, "/")
	if len(key) > 1 {
		key = strings.TrimSuffix(key, "/")
	}
	return key
}

// FirewallName is the name a firewall alias is stored and listed under. Proxmox
// folds alias names to lower case on every route (pve-firewall,
// PVE/API2/Firewall/Aliases.pm: lc($param->{name}), and lc($rename)), so "MyNet"
// is created, updated and deleted as "mynet". Audited as typed, the same alias
// would file under as many keys as the spellings its callers used.
func FirewallName(name string) string {
	return strings.ToLower(name)
}

// BareVMID reports the VMID a bare HA sid names ("109"). A sid with a type
// ("vm:109") and any spelling that is not the canonical digits of a VMID ("0109",
// "-1", "1e2") is not one.
func BareVMID(sid string) (vmid int32, ok bool) {
	n, err := strconv.ParseInt(sid, 10, 32)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != sid {
		return 0, false
	}
	return int32(n), true
}

// HASID is the sid Proxmox resolves a bare VMID to by the type of the guest that
// holds it (PVE::HA::Config::parse_sid): "qemu" is vm:N, "lxc" is ct:N. ok is
// false for any other type, which names no sid.
func HASID(bare, guestType string) (sid string, ok bool) {
	switch guestType {
	case "qemu":
		return "vm:" + bare, true
	case "lxc":
		return "ct:" + bare, true
	}
	return "", false
}

// ReportedHASID takes the sid Proxmox reports for a resource asked for by a
// bare VMID, but only if it is that VMID under vm: or ct:. Anything else it
// says is not a spelling of the resource asked for.
func ReportedHASID(bare, reported string) (sid string, ok bool) {
	if reported == "vm:"+bare || reported == "ct:"+bare {
		return reported, true
	}
	return "", false
}

// SDNSubnet is the key of an SDN subnet: its VNet, then its id as Proxmox lists
// it ("zone01-192.0.2.0-24"), which is how update and delete address it.
func SDNSubnet(vnet, id string) string {
	return vnet + "/" + id
}

// SDNSubnetID finds, among the ids a VNet lists, the one Proxmox derived from the
// CIDR a subnet was created with: its zone, then the CIDR with the slash dashed
// ("zone01-192.0.2.0-24"). The create takes the CIDR and the zone is not in the
// request, so the listing is the only place the id is reported.
func SDNSubnetID(cidr string, listed []string) (id string, ok bool) {
	suffix := "-" + strings.ReplaceAll(cidr, "/", "-")
	for _, candidate := range listed {
		if strings.HasSuffix(candidate, suffix) {
			return candidate, true
		}
	}
	return "", false
}
