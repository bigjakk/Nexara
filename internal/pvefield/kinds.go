package pvefield

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Kind is one kind of object's fields, as the observer compares them and as an
// update handler predicts them. Ignore are dropped from every reading — run
// state and derived values, beside the file's digest, which is always dropped
// (internal/collector's fieldSpec). Sets hold a list whose order means nothing
// (Set); Members list other objects' ids, kept as the ids (MemberIDs). Stored
// is, for a field an update sends, how Proxmox stores the value sent, as its
// reading then gives it; one it does not name is stored as sent, as text.
// ReadIn names a field the reading never shows on its own, by the one it is
// read back folded into: what it is stored as is compared there.
type Kind struct {
	Ignore, Sets, Members []string
	Stored                map[string]Store
	ReadIn                map[string]string
}

// Store is how Proxmox stores sent, one field's value as an update's form
// sends it beside the update's other fields (update, by name): the JSON its
// reading then gives, or false when the reading leaves the field out.
type Store func(sent string, update map[string]string) (json.RawMessage, bool)

// Changed reports whether an update sending update (each field's value by its
// name, as the client writes it in its form) changes field name of the object
// read just before as before: whether the observer will find it moved, the
// value Proxmox stores compared with the one it read in their canonical form.
func (k Kind) Changed(name string, before map[string]json.RawMessage, update map[string]string) bool {
	if name == "digest" || slices.Contains(k.Ignore, name) {
		return false // never compared
	}
	store := k.Stored[name]
	if store == nil {
		store = Text
	}
	after, listed := store(update[name], update)
	read := name
	if in := k.ReadIn[name]; in != "" {
		read = in
	}
	was, had := k.canonical(read, before[read])
	if !listed || !had {
		return listed != had
	}
	now, ok := k.canonical(read, after)
	return !ok || now != was
}

// canonical is a field's value as the observer compares it: a member list's
// ids, a set, or a value (internal/collector's fieldSpec.split).
func (k Kind) canonical(name string, raw json.RawMessage) (string, bool) {
	if raw == nil {
		return "", false
	}
	if slices.Contains(k.Members, name) {
		if ids, ok := MemberIDs(raw); ok {
			b, _ := json.Marshal(ids)
			return "members " + string(b), true
		}
	}
	if slices.Contains(k.Sets, name) {
		return Set(raw)
	}
	return Value(raw)
}

func text(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// Text is a value stored as sent.
func Text(sent string, _ map[string]string) (json.RawMessage, bool) { return text(sent), true }

// Number is a SectionConfig integer or boolean: check_value numifies it
// ($value + 0, pve-common SectionConfig.pm), and its parser again on every
// read, so "08" is stored and read back as 8. JSONSchema takes an integer as
// [+-]?\d+ and a boolean as 1 or 0 alone (is_integer, check_type); anything
// else Proxmox refuses, and is compared as sent.
func Number(sent string, _ map[string]string) (json.RawMessage, bool) {
	if n, err := strconv.ParseInt(sent, 10, 64); err == nil {
		return json.RawMessage(strconv.FormatInt(n, 10)), true
	}
	return text(sent), true
}

// zoneNodes is a zone's nodes or exitnodes: decode_value keeps each name
// split_list gives once and encode_value joins them with commas, in no order
// (pve-network Zones/Plugin.pm); none is written as no line at all
// (SectionConfig.pm format_config_line).
func zoneNodes(sent string, _ map[string]string) (json.RawMessage, bool) {
	nodes := SplitList(sent)
	if len(nodes) == 0 {
		return nil, false
	}
	return text(strings.Join(nodes, ",")), true
}

// dhcpRanges is a subnet's dhcp-range, an array of property strings its
// reading gives back parsed (pve-network API2 Subnets.pm, Subnets.pm
// get_dhcp_ranges: parse_property_string, which takes each "<key>=<value>"
// as it is). A form sends one range, which replaces the array.
func dhcpRanges(sent string, _ map[string]string) (json.RawMessage, bool) {
	r := map[string]string{}
	for _, part := range strings.Split(sent, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return text(sent), true
		}
		r[key] = value
	}
	b, _ := json.Marshal([]map[string]string{r})
	return b, true
}

// netPorts is a bridge's bridge_ports or a bond's slaves: written with each
// run of commas, semicolons and white space as one space, and read back split
// on white space, each once, sorted, without the placeholder none (pve-common
// Network/Interfaces.pm __interface_to_string, __read_etc_network_interfaces).
func netPorts(sent string, _ map[string]string) (json.RawMessage, bool) {
	seen := map[string]bool{}
	for _, p := range strings.FieldsFunc(sent, func(r rune) bool { return r == ',' || r == ';' || isPerlSpace(r) }) {
		if p != "none" {
			seen[p] = true
		}
	}
	return text(strings.Join(slices.Sorted(maps.Keys(seen)), " ")), true
}

// netSTP is a bridge's bridge_stp, read back as on for on or yes in any case,
// and off for anything else.
func netSTP(sent string, _ map[string]string) (json.RawMessage, bool) {
	if s := strings.ToLower(strings.TrimFunc(sent, isPerlSpace)); s == "on" || s == "yes" {
		return text("on"), true
	}
	return text("off"), true
}

// netVLANAware is a bridge's bridge_vlan_aware: written as yes whenever it is
// set, whatever its value, and read back as 1.
func netVLANAware(string, map[string]string) (json.RawMessage, bool) {
	return json.RawMessage("1"), true
}

// netAutostart is an interface's autostart: an auto line, read back as 1, and
// for 0 none, which the reading leaves out. An OVS port, internal port or bond
// is started by its bridge, whatever was sent, and the ifupdown2 writer —
// Proxmox VE's own since 7 — gives it an auto line beside the bridge's, read
// back as 1 (pve-common Network/Interfaces.pm __interface_to_string); the
// legacy ifupdown writer's allow-<bridge> line reads back as none.
func netAutostart(sent string, update map[string]string) (json.RawMessage, bool) {
	switch update["type"] {
	case "OVSPort", "OVSIntPort", "OVSBond":
		return json.RawMessage("1"), true
	}
	if sent == "" || sent == "0" {
		return nil, false
	}
	return json.RawMessage("1"), true
}

// netComments is an interface's comments: written a line at a time as
// "#<line>" and read back each line without its trailing white space and with
// a newline. comments6, written in the inet6 stanza, is read back folded into
// comments, so the two sent are read back as one: comments6 is compared there
// (Network's ReadIn), as what the two make.
func netComments(sent string, update map[string]string) (json.RawMessage, bool) {
	read := commentLines(sent) + commentLines(update["comments6"])
	if read == "" {
		return nil, false
	}
	return text(read), true
}

func netComments6(_ string, update map[string]string) (json.RawMessage, bool) {
	return netComments(update["comments"], update)
}

func commentLines(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1] // Perl's split drops the empty fields at the end
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(strings.TrimRightFunc(line, isPerlSpace))
		b.WriteString("\n")
	}
	return b.String()
}

// ipv4MaskBits is JSONSchema.pm's $ipv4_mask_hash: the netmasks Proxmox takes.
var ipv4MaskBits = func() map[string]int {
	out := map[string]int{}
	for bits := 0; bits <= 32; bits++ {
		mask := uint32(0)
		if bits > 0 {
			mask = ^uint32(0) << (32 - bits)
		}
		out[strconv.Itoa(int(mask>>24))+"."+strconv.Itoa(int(mask>>16&0xff))+"."+
			strconv.Itoa(int(mask>>8&0xff))+"."+strconv.Itoa(int(mask&0xff))] = bits
	}
	return out
}()

// netNetmask is an IPv4 netmask: written into the address as its prefix
// length — as sent when it is digits, by get_netmask_bits when it is dotted,
// where 0.0.0.0's 0 writes none — and read back as that length.
func netNetmask(sent string, _ map[string]string) (json.RawMessage, bool) {
	if bits, ok := ipv4MaskBits[sent]; ok {
		if bits == 0 {
			return nil, false
		}
		return text(strconv.Itoa(bits)), true
	}
	return text(sent), true
}

// numbers stores fields as Number. Proxmox's SDN, backup jobs, metric servers
// and ACME plugins are SectionConfig files: each field is stored as sent but
// for the integers and booleans, which are numified. Their schemas are
// pve-network's plugins (VnetPlugin, SubnetPlugin, Zones/*, Controllers/*,
// Ipams/*, Dns/*), pve-guest-common VZDump/Common.pm and pve-manager
// API2/Backup.pm, pve-manager Status/{Plugin,InfluxDB,Graphite}.pm, and
// proxmox-acme ACME/{Challenge,DNSChallenge}.pm. A MAC (an EVPN zone's mac) is
// checked, not folded (pve_verify_mac_addr), so its case is kept, and compared.
func numbers(fields ...string) map[string]Store {
	out := make(map[string]Store, len(fields))
	for _, f := range fields {
		out[f] = Number
	}
	return out
}

// with is stores with field stored by store.
func with(stores map[string]Store, field string, store Store) map[string]Store {
	stores[field] = store
	return stores
}

var sdnIgnore = []string{"pending", "state"}

// The kinds an update handler diffs, and the observer reads alike.
var (
	// pve-manager API2/Backup.pm index: next-run is computed from now on every
	// read. vmid and exclude are comma lists of guests, kept as members: a
	// guest destroyed with purge leaves them (VZDump::Plugin
	// remove_vmid_from_backup_jobs), and only that guest's destroy explains it.
	// create_job writes enabled always, so a job lists it: there is no default
	// to read it as.
	Backup = Kind{Ignore: []string{"next-run"}, Members: []string{"vmid", "exclude"},
		Stored: numbers("enabled", "all")}
	// pve-manager API2/Network.pm index (read_file('interfaces', 1), so the
	// pending interfaces.new): active and exists are the interface's run state,
	// altnames come from `ip link`, priority is the stanza's place in the file
	// and families is derived from method/method6. Not SectionConfig: each
	// field is read back as pve-common Network/Interfaces.pm writes and parses
	// it.
	Network = Kind{Ignore: []string{"active", "exists", "altnames", "priority", "families"},
		Stored: map[string]Store{
			"autostart": netAutostart, "comments": netComments, "comments6": netComments6, "netmask": netNetmask,
			"bridge_ports": netPorts, "slaves": netPorts, "bridge_stp": netSTP, "bridge_vlan_aware": netVLANAware,
		},
		ReadIn: map[string]string{"comments6": "comments"}}
	// Zones/Plugin.pm encode_value: nodes and exitnodes join(',', keys %$value).
	SDNZone = Kind{Ignore: sdnIgnore, Sets: []string{"nodes", "exitnodes"},
		Stored: with(with(numbers("tag", "mtu", "vrf-vxlan", "advertise-subnets", "disable-arp-nd-suppression"),
			"nodes", zoneNodes), "exitnodes", zoneNodes)}
	// pve-network: the plain view carries no pending or state, which only the
	// pending view does; dropped should a release add them.
	SDNVNet       = Kind{Ignore: sdnIgnore, Stored: numbers("tag", "vlanaware", "isolate-ports")}
	SDNSubnet     = Kind{Ignore: sdnIgnore, Stored: with(numbers("snat"), "dhcp-range", dhcpRanges)}
	SDNController = Kind{Ignore: sdnIgnore, Stored: numbers("asn", "ebgp-multihop")}
	SDNIPAM       = Kind{Ignore: sdnIgnore, Stored: numbers("section")}
	SDNDNS        = Kind{Ignore: sdnIgnore, Stored: numbers("ttl", "reversemaskv6")}
	// A metric server's token is kept in /etc/pve/priv, and never read.
	MetricServer = Kind{Stored: numbers("port", "disable", "mtu", "timeout", "max-body-size", "verify-certificate")}
	// pve-manager API2/ACMEPlugin.pm: nodes is a list; data, the plugin's DNS
	// API credentials, is a value like any other and is never stored.
	ACMEPlugin = Kind{Sets: []string{"nodes"}, Stored: numbers("validation-delay", "disable")}
	// pve-storage Storage/Plugin.pm encode_value: content via
	// content_hash_to_string and nodes via join(',', keys %$value), both
	// unsorted; the integers and booleans of the API schema numified, as a
	// SectionConfig's are. A plugin's sensitive properties (password,
	// encryption-key, keyring, master-pubkey) are kept out of the config.
	Storage = Kind{Sets: []string{"content", "nodes"}, Stored: numbers("create-base-path", "create-subdirs", "disable",
		"fuse", "krbd", "max-protected-backups", "mkdir", "nocow", "nowritecache", "port", "saferemove",
		"saferemove-stepsize", "shared", "skip-cert-verification", "snapshot-as-volume-chain", "sparse", "tagged_only")}
	// qemu-server: lock is set and cleared by tasks (backup, snapshot,
	// migrate, clone, suspended); parent is the snapshot pointer; vmstate,
	// runningmachine, runningcpu and running-nets-host-mtu are written when a
	// VM hibernates and removed when it resumes (QemuConfig.pm
	// __snapshot_save_vmstate, QemuServer.pm vm_start and
	// remove_left_over_vmstate_opts). The API already drops the snapshot,
	// pending and special (cloud-init, fleecing) sections (AbstractConfig.pm
	// and QemuConfig.pm load_current_config). A value is kept as sent.
	QemuConfig = Kind{Ignore: []string{"lock", "parent", "vmstate", "runningmachine", "runningcpu", "running-nets-host-mtu"}}
	// pve-container: lock and parent, as for a VM.
	LXCConfig = Kind{Ignore: []string{"lock", "parent"}}
)
