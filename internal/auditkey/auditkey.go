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
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// MaxIDLen bounds a resource id taken from Proxmox before it is filed.
const MaxIDLen = 256

// ValidID reports whether id can be filed as an audit resource_id: not empty,
// not longer than MaxIDLen, and no control or format character (a zero-width
// space or a right-to-left override changes how a name reads, not what it is).
// An id read from a node's own lists is still a name somebody typed (a pool, an
// alias), and one that fails this is not filed under its own key.
func ValidID(id string) bool {
	if id == "" || len(id) > MaxIDLen {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
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

// FirewallName is the name a firewall alias, IP set or security group is stored
// and listed under. Proxmox folds the names to lower case when it parses the
// config (pve-firewall, Firewall.pm generic_fw_config_parser: lc($1) for [group
// ...] and [ipset ...]) and on every alias route (Aliases.pm: lc($param->{name}),
// and lc($rename)), so "MyNet" is created, updated and deleted as "mynet".
// Audited as typed, the same object would file under as many keys as the
// spellings its callers used.
func FirewallName(name string) string {
	return strings.ToLower(name)
}

// IPSetCIDR is the entry an IP set stores for an address or a network that was
// created, and so the key of the create. create_ip (IPSet.pm) runs clean_cidr,
// which clears the host bits of a network ("192.0.2.5/24" is "192.0.2.0/24") and
// writes the address back compressed, with the prefix as it was typed ("/024"
// stays "/024"); and parse_ip_or_cidr, which drops a literal "/32" from an IPv4
// address and a literal "/128" from an IPv6 one, and nothing else ("/032" stays).
// An alias reference ("dc/web", "guest/db01") is stored as sent, and so is
// anything else this cannot read as an address: Proxmox refused it or never took
// it, and there is nothing to normalise. A bare address is not rewritten either.
func IPSetCIDR(entry string) string {
	ip, length, hasPrefix := strings.Cut(entry, "/")
	// clean_cidr leaves an entry with no prefix, or a zero one, as it is.
	if !hasPrefix || length == "" || length == "0" || strings.Trim(length, "0123456789") != "" {
		return entry
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || addr.Zone() != "" {
		return entry
	}
	bits, err := strconv.Atoi(length)
	if err != nil || bits > addr.BitLen() {
		return entry
	}
	prefix, err := addr.Prefix(bits)
	if err != nil {
		return entry
	}
	clean := prefix.Addr().String()
	if !addr.Is4() {
		clean = netIPv6(prefix.Addr().As16())
	}
	if (addr.Is4() && length == "32") || (!addr.Is4() && length == "128") {
		return clean
	}
	return clean + "/" + length
}

// ipv6ZeroRun is a run of two or more zero groups, with the colons around it.
var ipv6ZeroRun = regexp.MustCompile(`(?:^|:)0(?::0)+(?::|$)`)

// netIPv6 writes an IPv6 address the way Net::IP::ip_compress_address does, which
// is how clean_cidr writes the address of a network back: lower-case hex groups
// without leading zeros, the longest run of zero groups (by its length as text,
// the first of equals) replaced by "::". An IPv4-mapped address is written as
// hex groups, not with a dotted tail ("::ffff:c000:204").
func netIPv6(a [16]byte) string {
	groups := make([]string, 8)
	for i := range groups {
		groups[i] = strconv.FormatUint(uint64(a[2*i])<<8|uint64(a[2*i+1]), 16)
	}
	ip := strings.Join(groups, ":")
	longest := ""
	for _, run := range ipv6ZeroRun.FindAllString(ip, -1) {
		if len(longest) < len(run) {
			longest = run
		}
	}
	if longest != "" {
		ip = strings.Replace(ip, longest, "::", 1)
	}
	return ip
}

// IPSetEntry is the key of an IP set entry created: its set, then the entry as
// the set stores it (IPSetCIDR).
func IPSetEntry(set, entry string) string {
	return FirewallName(set) + "/" + IPSetCIDR(entry)
}

// IPSetListed is the key of an IP set entry addressed as the set lists it, which
// is how a delete names it. The config parser applies parse_ip_or_cidr and not
// clean_cidr (Firewall.pm generic_fw_config_parser), so an entry written by hand
// keeps the host bits it was typed with, and the delete's path names exactly that.
func IPSetListed(set, entry string) string {
	return FirewallName(set) + "/" + entry
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
