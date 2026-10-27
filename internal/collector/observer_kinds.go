package collector

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bigjakk/nexara/internal/auditkey"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The kinds the observer (observer.go) reads, each with the fields it compares.
// Every ignored field is one Proxmox changes with nobody editing anything, from
// the Proxmox source read on GitHub master (PVE 9.x); every set is a list
// Proxmox re-encodes from a Perl hash, in an order that changes per request.
// Every kind also ignores `digest`, a SHA1 of the whole config file, which one
// object's edit changes for all of them (newFieldSpec).
//
// The identity a change is filed under is the one the audit rows for the same
// object use (internal/api/handlers/common.go, the AuditLog key table), so a
// History finds both with one selector and the classification can match them.

// observedObject is one object as one read reported it: its key within its
// kind, the identity a change to it is filed under, and each compared field's
// canonical value. Nothing here is stored as it is but members: observer.go
// tags the rest.
type observedObject struct {
	id           string
	resourceType string
	resourceID   string
	vmid         int32               // a guest's VMID; 0 for every other object
	node         string              // the node it is on or belongs to; "" for a cluster's object
	fields       map[string]string   // field -> canonical value
	lists        map[string][]string // field -> canonical items of an ordered list (a rule set)
	members      map[string][]string // field -> the other objects' ids it lists, sorted (fieldSpec.withMembers)
	hardware     bool                // a physical NIC: it comes and goes with nobody editing
}

// kindReading is what one read of a kind produced. unread holds the objects it
// listed but could not read in full (a per-object read failed): they are left
// exactly as stored, neither compared nor removed. forget holds the stored
// objects that went with a parent that is gone (an IP set's entries), which are
// dropped without a change of their own: the parent's removal is the change.
// quiet names fields a change limited to which records nothing (Proxmox VE 9's
// HA group migration, observeHA).
type kindReading struct {
	kind    string
	objects []observedObject
	unread  map[string]bool
	forget  map[string]bool
	quiet   map[string]bool
}

// observeKind is one kind of a cluster's objects, read once per pass.
type observeKind struct {
	kind string
	// sysAudit: Proxmox answers this read in part without Sys.Audit on "/", so
	// the kind is skipped for a token without it rather than read short.
	sysAudit bool
	read     func(ctx context.Context, p *observePass) ([]kindReading, readOutcome)
}

// observeNodeKind is one kind of a node's objects, read once per pass for each
// online node. One read may give several kinds (a node's config file holds both
// its options and its ACME settings).
type observeNodeKind struct {
	kind string // the skip key; the readings name their own kinds
	read func(ctx context.Context, p *observePass, node db.Node) ([]kindReading, readOutcome)
}

// observeClusterKinds is read in this order every pass, after the HA kinds
// (observeHA), which are read together.
var observeClusterKinds = []observeKind{
	{kind: "pool", read: readPools},
	{kind: "storage", read: listKind("storage", proxmox.ObserveStorage(), "storage", storageSpec)},
	{kind: "backup", read: listKind("backup", proxmox.ObserveBackupJobs(), "id", backupSpec)},
	{kind: "usb_mapping", read: listKind("usb_mapping", proxmox.ObserveUSBMappings(), "id", mappingSpec)},
	{kind: "pci_mapping", read: listKind("pci_mapping", proxmox.ObservePCIMappings(), "id", mappingSpec)},
	{kind: "metric_server", read: readMetricServers},
	{kind: "cluster_options", sysAudit: true, read: readClusterOptions},
	{kind: "sdn_zone", read: sdnKind("sdn_zone", "zones", "zone", sdnZoneSpec)},
	{kind: "sdn_vnet", read: readSDNVNets},
	{kind: "sdn_controller", read: sdnKind("sdn_controller", "controllers", "controller", sdnSpec)},
	{kind: "sdn_ipam", read: sdnKind("sdn_ipam", "ipams", "ipam", sdnSpec)},
	{kind: "sdn_dns", read: sdnKind("sdn_dns", "dns", "dns", sdnSpec)},
	{kind: "firewall_options", read: readFirewallOptions},
	{kind: "firewall_rule", read: readFirewallRules},
	{kind: "firewall_alias", read: readFirewallAliases},
	{kind: "firewall_ipset", read: readFirewallIPSets},
	{kind: "firewall_security_group", read: readFirewallGroups},
	{kind: "pve_user", read: readAccessUsers},
	{kind: "pve_group", read: accessKind("pve_group", "groups", "groupid", accessGroupSpec)},
	{kind: "pve_role", read: accessKind("pve_role", "roles", "roleid", accessRoleSpec)},
	{kind: "pve_acl", read: readAccessACL},
	{kind: "acme_account", read: listKind("acme_account", proxmox.ObserveACMEAccounts(), "name", acmeAccountSpec)},
	{kind: "acme_plugin", read: listKind("acme_plugin", proxmox.ObserveACMEPlugins(), "plugin", acmePluginSpec)},
}

// observeNodeKinds is read in this order for each online node.
var observeNodeKinds = []observeNodeKind{
	{kind: "network", read: readNodeNetwork},
	{kind: "node_config", read: readNodeConfig},
	{kind: "node_dns", read: readNodeObject("node_dns", proxmox.ObserveNodeDNS, nodeDNSSpec)},
	{kind: "node_time", read: readNodeObject("node_time", proxmox.ObserveNodeTime, nodeTimeSpec)},
	{kind: "node_firewall", read: readNodeFirewall},
}

var (
	// pve-storage Storage/Plugin.pm encode_value: content via
	// content_hash_to_string and nodes via join(',', keys %$value), both unsorted.
	storageSpec = newFieldSpec(nil, []string{"content", "nodes"})
	// pve-manager API2/Backup.pm index: next-run is computed from now on every
	// read. vmid and exclude are comma lists of guests, kept as members: a
	// guest destroyed with purge leaves them (VZDump::Plugin
	// remove_vmid_from_backup_jobs), and only that guest's destroy explains it.
	backupSpec = newFieldSpec([]string{"next-run"}, nil).withMembers("vmid", "exclude")
	// pve-manager API2/Cluster/Mapping/{PCI,USB}.pm index: checks appear only
	// with check-node, never sent; map is one entry per node.
	mappingSpec = newFieldSpec([]string{"checks"}, []string{"map"})
	// pve-manager API2/Cluster.pm get_options: allowed-tags is derived per
	// caller from the tag settings and the token's privileges.
	clusterOptionsSpec = newFieldSpec([]string{"allowed-tags"}, nil)
	// pve-manager API2/Network.pm index (read_file('interfaces', 1), so the
	// pending interfaces.new): active and exists are the interface's run state,
	// altnames come from `ip link`, priority is the stanza's place in the file
	// and families is derived from method/method6.
	networkSpec = newFieldSpec([]string{"active", "exists", "altnames", "priority", "families"}, nil)
	nodeDNSSpec = newFieldSpec(nil, nil)
	// pve-manager API2/Nodes.pm time: time and localtime are the clock.
	nodeTimeSpec = newFieldSpec([]string{"time", "localtime"}, nil)
	// pve-firewall: pos is the rule's place, errors is validation against the
	// aliases and IP sets of the moment, ipversion is derived from the addresses
	// (Firewall.pm copy_list_with_digest and the rule parser).
	firewallRuleSpec = newFieldSpec([]string{"pos", "errors", "ipversion"}, nil)
	// qemu-server: lock is set and cleared by tasks (backup, snapshot,
	// migrate, clone, suspended); parent is the snapshot pointer; vmstate,
	// runningmachine, runningcpu and running-nets-host-mtu are written when a VM
	// hibernates and removed when it resumes (QemuConfig.pm
	// __snapshot_save_vmstate, QemuServer.pm vm_start and
	// remove_left_over_vmstate_opts). The API already drops the snapshot,
	// pending and special (cloud-init, fleecing) sections (AbstractConfig.pm
	// and QemuConfig.pm load_current_config).
	qemuConfigSpec = newFieldSpec([]string{"lock", "parent", "vmstate", "runningmachine", "runningcpu",
		"running-nets-host-mtu"}, nil)
	// pve-container: lock and parent, as for a VM.
	lxcConfigSpec = newFieldSpec([]string{"lock", "parent"}, nil)

	// pve-ha-manager API2/HA/Resources.pm index: errors is derived (a group
	// that does not exist).
	haResourceSpec = newFieldSpec([]string{"errors"}, nil)
	// API2/HA/Rules.pm index: order is the rule's place in rules.cfg, which
	// moves when a rule above it is deleted, and errors is the feasibility
	// check of the moment, derived from the nodes and the other rules.
	haRuleSpec = newFieldSpec([]string{"order", "errors"}, nil).withMembers("resources")
	// API2/HA/Groups.pm: nodes is re-encoded join(',', keys %$value), unsorted
	// (HA/Groups.pm encode_value).
	haGroupSpec = newFieldSpec([]string{"type"}, []string{"nodes"})
	// pve-network: the plain view carries no pending or state, which only the
	// pending view does; dropped should a release add them.
	sdnSpec = newFieldSpec([]string{"pending", "state"}, nil)
	// Zones/Plugin.pm encode_value: nodes and exitnodes join(',', keys %$value).
	sdnZoneSpec = newFieldSpec([]string{"pending", "state"}, []string{"nodes", "exitnodes"})
	// pve-firewall Firewall.pm parse_alias: ipversion is derived from the cidr.
	firewallAliasSpec = newFieldSpec([]string{"ipversion"}, nil)
	firewallPlainSpec = newFieldSpec(nil, nil)
	// pve-access-control API2/User.pm index: realm-type is derived from
	// domains.cfg, totp-locked and tfa-locked-until are TFA lockout state
	// (priv/tfa.cfg), and tokens are their own objects. A user's groups, and
	// a group's users, are kept as members: a group deleted leaves its users,
	// and only that group's delete explains it.
	accessUserSpec  = newFieldSpec([]string{"realm-type", "totp-locked", "tfa-locked-until", "tokens"}, nil).withMembers("groups")
	accessTokenSpec = newFieldSpec(nil, nil)
	accessGroupSpec = newFieldSpec(nil, nil).withMembers("users")
	// API2/Role.pm index: special is derived (a built-in role).
	accessRoleSpec  = newFieldSpec([]string{"special"}, []string{"privs"})
	acmeAccountSpec = newFieldSpec(nil, nil)
	// pve-manager API2/ACMEPlugin.pm: nodes is a list; data, the plugin's DNS
	// API credentials, is a value like any other and is never stored.
	acmePluginSpec = newFieldSpec(nil, []string{"nodes"})
)

// acmeConfigField is a node config key that belongs to its ACME settings, filed
// as acme_config / <node>: acme and acmedomain0..N (pve-manager NodeConfig.pm).
var acmeConfigField = regexp.MustCompile(`^(acme|acmedomain\d+)$`)

// listKind reads a list endpoint whose entries are the kind's objects, keyed by
// one field, filed under the kind with that key.
func listKind(kind string, target proxmox.ObserveTarget, idField string, spec fieldSpec) func(context.Context, *observePass) ([]kindReading, readOutcome) {
	return func(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
		items, out := p.readList(ctx, target)
		if out != readOK {
			return nil, out
		}
		reading := kindReading{kind: kind}
		for _, item := range items {
			id := stringField(item, idField)
			if !auditkey.ValidID(id) {
				continue
			}
			fields, members := spec.split(item)
			reading.objects = append(reading.objects, observedObject{
				id: id, resourceType: kind, resourceID: id, fields: fields, members: members,
			})
		}
		return []kindReading{reading}, readOK
	}
}

// readPools lists the pools, then reads each one's members: a pool's guests and
// storage, kept as the ids (members: a guest destroyed leaves its pool, and
// only that guest's destroy explains it), never the members' run state
// (pve-manager API2/Pool.pm index, API2Tools extract_vm_stats). A pool whose
// own read fails is unread.
func readPools(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	items, out := p.readList(ctx, proxmox.ObservePools())
	if out != readOK {
		return nil, out
	}
	reading := kindReading{kind: "pool", unread: map[string]bool{}}
	for _, item := range items {
		id := stringField(item, "poolid")
		if !auditkey.ValidID(id) {
			continue
		}
		target, err := proxmox.ObservePool(id)
		if err != nil {
			continue
		}
		detail, out := p.readList(ctx, target)
		if out != readOK || len(detail) != 1 {
			reading.unread[id] = true
			continue
		}
		reading.objects = append(reading.objects, poolObject(id, detail[0]))
	}
	return []kindReading{reading}, readOK
}

func poolObject(id string, entry map[string]json.RawMessage) observedObject {
	fields := map[string]string{}
	if v, ok := canonicalValue(entry["comment"]); ok {
		fields["comment"] = v
	}
	var listed []map[string]json.RawMessage
	_ = json.Unmarshal(entry["members"], &listed)
	var vms, storage []json.RawMessage
	for _, m := range listed {
		switch stringField(m, "type") {
		case "qemu", "lxc", "openvz":
			if v := m["vmid"]; v != nil {
				vms = append(vms, v)
			}
		case "storage":
			if v := m["storage"]; v != nil {
				storage = append(storage, v)
			}
		}
	}
	members := map[string][]string{}
	for field, list := range map[string][]json.RawMessage{"vms": vms, "storage": storage} {
		if ids, ok := memberIDs(jsonArray(list)); ok {
			members[field] = ids
		} else {
			fields[field], _ = canonicalSet(jsonArray(list))
		}
	}
	return observedObject{id: id, resourceType: "pool", resourceID: id, fields: fields, members: members}
}

// readMetricServers lists the servers, then reads each one whole: the list
// carries five fields of a configuration that has a dozen.
func readMetricServers(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	items, out := p.readList(ctx, proxmox.ObserveMetricServers())
	if out != readOK {
		return nil, out
	}
	spec := newFieldSpec(nil, nil)
	reading := kindReading{kind: "metric_server", unread: map[string]bool{}}
	for _, item := range items {
		id := stringField(item, "id")
		if !auditkey.ValidID(id) {
			continue
		}
		target, err := proxmox.ObserveMetricServer(id)
		if err != nil {
			continue
		}
		detail, out := p.readObject(ctx, target)
		if out != readOK {
			reading.unread[id] = true
			continue
		}
		reading.objects = append(reading.objects, observedObject{
			id: id, resourceType: "metric_server", resourceID: id, fields: spec.canonicalFields(detail),
		})
	}
	return []kindReading{reading}, readOK
}

// readClusterOptions reads datacenter.cfg as one object, filed under the
// cluster's own id in Nexara (cluster_options / <cluster uuid>).
func readClusterOptions(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	obj, out := p.readObject(ctx, proxmox.ObserveClusterOptions())
	if out != readOK {
		return nil, out
	}
	id := p.cluster.ID.String()
	return []kindReading{{kind: "cluster_options", objects: []observedObject{{
		id: id, resourceType: "cluster_options", resourceID: id, fields: clusterOptionsSpec.canonicalFields(obj),
	}}}}, readOK
}

// readNodeNetwork lists a node's interfaces, network / <node>/<iface>. A
// physical NIC is listed whether or not it is configured, so one appearing or
// vanishing is hardware, not an edit; its configuration is compared like any.
func readNodeNetwork(ctx context.Context, p *observePass, node db.Node) ([]kindReading, readOutcome) {
	target, err := proxmox.ObserveNodeNetwork(node.Name)
	if err != nil {
		return nil, readFailed
	}
	items, out := p.readList(ctx, target)
	if out != readOK {
		return nil, out
	}
	reading := kindReading{kind: "network"}
	for _, item := range items {
		iface := stringField(item, "iface")
		id := node.Name + "/" + iface
		if iface == "" || !auditkey.ValidID(id) {
			continue
		}
		reading.objects = append(reading.objects, observedObject{
			id: id, resourceType: "network", resourceID: id, node: node.Name,
			fields: networkSpec.canonicalFields(item), hardware: stringField(item, "type") == "eth",
		})
	}
	return []kindReading{reading}, readOK
}

// readNodeConfig reads a node's config file, which holds two objects: its
// options and notes, filed as node / <node>, and its ACME settings, filed as
// acme_config / <node> (pve-manager API2/NodeConfig.pm get_config).
func readNodeConfig(ctx context.Context, p *observePass, node db.Node) ([]kindReading, readOutcome) {
	target, err := proxmox.ObserveNodeConfig(node.Name)
	if err != nil {
		return nil, readFailed
	}
	obj, out := p.readObject(ctx, target)
	if out != readOK {
		return nil, out
	}
	options, acme := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	for k, v := range obj {
		if acmeConfigField.MatchString(k) {
			acme[k] = v
		} else {
			options[k] = v
		}
	}
	spec := newFieldSpec(nil, nil)
	return []kindReading{
		{kind: "node_options", objects: []observedObject{{
			id: node.Name, resourceType: "node", resourceID: node.Name, node: node.Name,
			fields: spec.canonicalFields(options),
		}}},
		{kind: "acme_config", objects: []observedObject{{
			id: node.Name, resourceType: "acme_config", resourceID: node.Name, node: node.Name,
			fields: spec.canonicalFields(acme),
		}}},
	}, readOK
}

// readNodeObject reads one object of a node, filed as node / <node>.
func readNodeObject(kind string, targetFor func(string) (proxmox.ObserveTarget, error), spec fieldSpec) func(context.Context, *observePass, db.Node) ([]kindReading, readOutcome) {
	return func(ctx context.Context, p *observePass, node db.Node) ([]kindReading, readOutcome) {
		target, err := targetFor(node.Name)
		if err != nil {
			return nil, readFailed
		}
		obj, out := p.readObject(ctx, target)
		if out != readOK {
			return nil, out
		}
		return []kindReading{{kind: kind, objects: []observedObject{{
			id: node.Name, resourceType: "node", resourceID: node.Name, node: node.Name,
			fields: spec.canonicalFields(obj),
		}}}}, readOK
	}
}

// readNodeFirewall reads a node's firewall rules as one ordered list, filed as
// firewall_rule / node/<node>, field rules: the scope of a rule set, as the
// cluster's is "cluster" and a guest's vm/<vmid> (the audit key table), since a
// rule has no id but its position, which moves. A node's firewall is not a
// node's object for who may read its History.
func readNodeFirewall(ctx context.Context, p *observePass, node db.Node) ([]kindReading, readOutcome) {
	target, err := proxmox.ObserveNodeFirewallRules(node.Name)
	if err != nil {
		return nil, readFailed
	}
	items, out := p.readList(ctx, target)
	if out != readOK {
		return nil, out
	}
	return []kindReading{{kind: "node_firewall", objects: []observedObject{{
		id: node.Name, resourceType: "firewall_rule", resourceID: "node/" + node.Name, node: node.Name,
		lists: map[string][]string{"rules": canonicalRules(items)},
	}}}}, readOK
}

// canonicalRules renders a rule list in order, each rule as canonical JSON of
// the fields it is compared on.
func canonicalRules(items []map[string]json.RawMessage) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		b, _ := json.Marshal(firewallRuleSpec.canonicalFields(item))
		out = append(out, string(b))
	}
	return out
}

// haMigrationFields are what Proxmox VE 9 rewrites on every HA resource when
// it migrates the HA groups to rules, with no task (pve-ha-manager Manager.pm
// $migrate_group_persistently: the group deleted, failback set).
var haMigrationFields = map[string]bool{"group": true, "failback": true}

// observeHA reads the HA resources, rules and groups, in that order, so the
// groups are read last. Proxmox VE 9 migrates its groups to rules once every
// node runs it: it writes the rules (which its rules listing already showed
// while the groups were there: API2/HA/Rules.pm $get_full_rules_config), takes
// the group off each resource, then deletes the groups, after which reading
// them is refused. When that refusal follows groups read before, the groups are
// forgotten with nothing recorded, and for this pass and the next a resource
// whose only changes are its group and failback records nothing either.
func (p *observePass) observeHA(ctx context.Context, sig string) {
	type haKind struct {
		kind, idField string
		target        proxmox.ObserveTarget
		spec          fieldSpec
	}
	kinds := []haKind{
		{"ha_resource", "sid", proxmox.ObserveHAResources(), haResourceSpec},
		{"ha_rule", "rule", proxmox.ObserveHARules(), haRuleSpec},
		{"ha_group", "group", proxmox.ObserveHAGroups(), haGroupSpec},
	}
	readAt := time.Now()
	readings := map[string]kindReading{}
	for _, k := range kinds {
		key := p.skipKey(k.kind, "")
		if p.o.skipped(key) {
			continue
		}
		raw, out, err := p.readWithError(ctx, k.target, p.o.gap)
		if k.kind == "ha_group" && proxmox.IsGroupsMigratedError(err) && len(p.state["ha_group"]) > 0 {
			p.o.startHAMigration(p.cluster.ID)
			p.forgetKind(ctx, "ha_group")
		}
		if !p.settle(key, out) {
			continue
		}
		items, ok := decodeList(raw)
		if !ok {
			continue
		}
		rd := kindReading{kind: k.kind}
		for _, item := range items {
			id := stringField(item, k.idField)
			if !auditkey.ValidID(id) {
				continue
			}
			fields, members := k.spec.split(item)
			rd.objects = append(rd.objects, observedObject{
				id: id, resourceType: k.kind, resourceID: id, fields: fields, members: members,
			})
		}
		readings[k.kind] = rd
	}
	if p.o.haMigrating(p.cluster.ID) {
		if rd, ok := readings["ha_resource"]; ok {
			rd.quiet = haMigrationFields
			readings["ha_resource"] = rd
		}
	}
	for _, k := range kinds {
		if rd, ok := readings[k.kind]; ok {
			p.apply(ctx, rd, "", sig, readAt, time.Now())
		}
	}
	p.o.endHAPass(p.cluster.ID)
}

// sdnKind reads one SDN list keyed by idField.
func sdnKind(kind, path, idField string, spec fieldSpec) func(context.Context, *observePass) ([]kindReading, readOutcome) {
	target, err := proxmox.ObserveSDN(path)
	if err != nil {
		panic(err)
	}
	return listKind(kind, target, idField, spec)
}

// readSDNVNets reads the vnets, then each vnet's subnets, filed as sdn_subnet
// / <vnet>/<subnet id> (auditkey.SDNSubnet). A vnet whose subnets cannot be
// read leaves them as stored; a vnet that is gone takes its subnets with it.
func readSDNVNets(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	target, _ := proxmox.ObserveSDN("vnets")
	items, out := p.readList(ctx, target)
	if out != readOK {
		return nil, out
	}
	vnets := kindReading{kind: "sdn_vnet"}
	subnets := kindReading{kind: "sdn_subnet", unread: map[string]bool{}, forget: map[string]bool{}}
	listed := map[string]bool{}
	for _, item := range items {
		vnet := stringField(item, "vnet")
		if !auditkey.ValidID(vnet) {
			continue
		}
		listed[vnet] = true
		vnets.objects = append(vnets.objects, observedObject{
			id: vnet, resourceType: "sdn_vnet", resourceID: vnet, fields: sdnSpec.canonicalFields(item),
		})
		t, err := proxmox.ObserveSDNSubnets(vnet)
		if err != nil {
			continue
		}
		entries, out := p.readList(ctx, t)
		if out != readOK {
			p.markChildren(subnets.unread, "sdn_subnet", vnet)
			continue
		}
		for _, e := range entries {
			id := auditkey.SDNSubnet(vnet, stringField(e, "subnet"))
			if stringField(e, "subnet") == "" || !auditkey.ValidID(id) {
				continue
			}
			subnets.objects = append(subnets.objects, observedObject{
				id: id, resourceType: "sdn_subnet", resourceID: id, fields: sdnSpec.canonicalFields(e),
			})
		}
	}
	p.forgetOrphans(subnets.forget, "sdn_subnet", listed)
	return []kindReading{vnets, subnets}, readOK
}

// readFirewallOptions reads the cluster firewall's options as one object,
// firewall_options / cluster.
func readFirewallOptions(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	target, _ := proxmox.ObserveFirewall("options")
	obj, out := p.readObject(ctx, target)
	if out != readOK {
		return nil, out
	}
	return []kindReading{{kind: "firewall_options", objects: []observedObject{{
		id: "cluster", resourceType: "firewall_options", resourceID: "cluster", fields: firewallPlainSpec.canonicalFields(obj),
	}}}}, readOK
}

// readFirewallRules reads the cluster's rule set as one ordered list,
// firewall_rule / cluster: a rule has no id but its position, which moves.
func readFirewallRules(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	target, _ := proxmox.ObserveFirewall("rules")
	items, out := p.readList(ctx, target)
	if out != readOK {
		return nil, out
	}
	return []kindReading{{kind: "firewall_rule", objects: []observedObject{{
		id: "cluster", resourceType: "firewall_rule", resourceID: "cluster",
		lists: map[string][]string{"rules": canonicalRules(items)},
	}}}}, readOK
}

// readFirewallAliases lists the aliases under the lower-case name Proxmox
// stores them by (auditkey.FirewallName); the name as typed is a field.
func readFirewallAliases(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	target, _ := proxmox.ObserveFirewall("aliases")
	items, out := p.readList(ctx, target)
	if out != readOK {
		return nil, out
	}
	rd := kindReading{kind: "firewall_alias"}
	for _, item := range items {
		id := auditkey.FirewallName(stringField(item, "name"))
		if !auditkey.ValidID(id) {
			continue
		}
		rd.objects = append(rd.objects, observedObject{
			id: id, resourceType: "firewall_alias", resourceID: id, fields: firewallAliasSpec.canonicalFields(item),
		})
	}
	return []kindReading{rd}, readOK
}

// readFirewallIPSets lists the IP sets, then each set's entries, filed as
// firewall_ipset_entry / <set>/<entry as the set stores it>
// (auditkey.IPSetEntry).
func readFirewallIPSets(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	target, _ := proxmox.ObserveFirewall("ipset")
	items, out := p.readList(ctx, target)
	if out != readOK {
		return nil, out
	}
	sets := kindReading{kind: "firewall_ipset"}
	entries := kindReading{kind: "firewall_ipset_entry", unread: map[string]bool{}, forget: map[string]bool{}}
	listed := map[string]bool{}
	for _, item := range items {
		name := stringField(item, "name")
		id := auditkey.FirewallName(name)
		if name == "" || !auditkey.ValidID(id) {
			continue
		}
		listed[id] = true
		sets.objects = append(sets.objects, observedObject{
			id: id, resourceType: "firewall_ipset", resourceID: id, fields: firewallPlainSpec.canonicalFields(item),
		})
		t, err := proxmox.ObserveFirewallIPSet(name)
		if err != nil {
			continue
		}
		list, out := p.readList(ctx, t)
		if out != readOK {
			p.markChildren(entries.unread, "firewall_ipset_entry", id)
			continue
		}
		for _, e := range list {
			cidr := stringField(e, "cidr")
			key := auditkey.IPSetEntry(name, cidr)
			if cidr == "" || !auditkey.ValidID(key) {
				continue
			}
			entries.objects = append(entries.objects, observedObject{
				id: key, resourceType: "firewall_ipset_entry", resourceID: key, fields: firewallPlainSpec.canonicalFields(e),
			})
		}
	}
	p.forgetOrphans(entries.forget, "firewall_ipset_entry", listed)
	return []kindReading{sets, entries}, readOK
}

// readFirewallGroups lists the security groups, then each group's rules as
// one ordered list, firewall_security_group_rule / <group>.
func readFirewallGroups(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	target, _ := proxmox.ObserveFirewall("groups")
	items, out := p.readList(ctx, target)
	if out != readOK {
		return nil, out
	}
	groups := kindReading{kind: "firewall_security_group"}
	rules := kindReading{kind: "firewall_security_group_rule", unread: map[string]bool{}, forget: map[string]bool{}}
	listed := map[string]bool{}
	for _, item := range items {
		name := stringField(item, "group")
		id := auditkey.FirewallName(name)
		if name == "" || !auditkey.ValidID(id) {
			continue
		}
		listed[id] = true
		groups.objects = append(groups.objects, observedObject{
			id: id, resourceType: "firewall_security_group", resourceID: id, fields: firewallPlainSpec.canonicalFields(item),
		})
		t, err := proxmox.ObserveFirewallGroup(name)
		if err != nil {
			continue
		}
		list, out := p.readList(ctx, t)
		if out != readOK {
			rules.unread[id] = true
			continue
		}
		rules.objects = append(rules.objects, observedObject{
			id: id, resourceType: "firewall_security_group_rule", resourceID: id,
			lists: map[string][]string{"rules": canonicalRules(list)},
		})
	}
	for id := range p.state["firewall_security_group_rule"] {
		if !listed[id] {
			rules.forget[id] = true
		}
	}
	return []kindReading{groups, rules}, readOK
}

// readAccessUsers reads the users and, from the same listing, their API
// tokens, pve_token / user@realm!tokenid.
func readAccessUsers(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	items, out := p.readList(ctx, proxmox.ObserveAccessUsers())
	if out != readOK {
		return nil, out
	}
	users := kindReading{kind: "pve_user"}
	tokens := kindReading{kind: "pve_token"}
	for _, item := range items {
		userid := stringField(item, "userid")
		if !auditkey.ValidID(userid) {
			continue
		}
		fields, members := accessUserSpec.split(item)
		users.objects = append(users.objects, observedObject{
			id: userid, resourceType: "pve_user", resourceID: userid, fields: fields, members: members,
		})
		var list []map[string]json.RawMessage
		_ = json.Unmarshal(item["tokens"], &list)
		for _, tok := range list {
			id := userid + "!" + stringField(tok, "tokenid")
			if stringField(tok, "tokenid") == "" || !auditkey.ValidID(id) {
				continue
			}
			tokens.objects = append(tokens.objects, observedObject{
				id: id, resourceType: "pve_token", resourceID: id, fields: accessTokenSpec.canonicalFields(tok),
			})
		}
	}
	return []kindReading{users, tokens}, readOK
}

// accessKind reads one access list keyed by idField.
func accessKind(kind, part, idField string, spec fieldSpec) func(context.Context, *observePass) ([]kindReading, readOutcome) {
	target, err := proxmox.ObserveAccess(part)
	if err != nil {
		panic(err)
	}
	return listKind(kind, target, idField, spec)
}

// aclGrantee is the field an ACL entry's grantee is listed in, by its type,
// and the list its grants are compared in.
var aclGrantee = map[string][2]string{
	"user": {"users", "user_grants"}, "group": {"groups", "group_grants"}, "token": {"tokens", "token_grants"},
}

// readAccessACL reads the ACL as one object per path, pve_acl / <path as
// Proxmox normalises it> (auditkey.ACLPath). Each kind of grantee is two member
// fields: who is granted something there (users, groups, tokens), and each
// grant (user_grants ...) as "<tag of role and propagate>:<grantee>", so a
// change says which grantee came, went or had a grant changed — a user
// deleted explains only that user's grants going — and never which role.
func readAccessACL(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	target, _ := proxmox.ObserveAccess("acl")
	items, out := p.readList(ctx, target)
	if out != readOK {
		return nil, out
	}
	paths := map[string]map[string][]json.RawMessage{}
	for _, item := range items {
		path := auditkey.ACLPath(stringField(item, "path"))
		fields, ok := aclGrantee[stringField(item, "type")]
		ugid := stringField(item, "ugid")
		if !auditkey.ValidID(path) || !ok || ugid == "" {
			continue
		}
		if paths[path] == nil {
			paths[path] = map[string][]json.RawMessage{}
		}
		role, _ := canonicalValue(jsonArray([]json.RawMessage{item["roleid"], item["propagate"]}))
		tag, err := p.o.tags.tag(p.cluster.ID, "pve_acl", path, fields[1], role)
		if err != nil {
			continue
		}
		who, _ := json.Marshal(ugid)
		grant, _ := json.Marshal(strings.NewReplacer("+", "-", "/", "_").Replace(tag) + ":" + ugid)
		paths[path][fields[0]] = append(paths[path][fields[0]], who)
		paths[path][fields[1]] = append(paths[path][fields[1]], grant)
	}
	rd := kindReading{kind: "pve_acl"}
	for path, byField := range paths {
		obj := observedObject{id: path, resourceType: "pve_acl", resourceID: path,
			fields: map[string]string{}, members: map[string][]string{}}
		for f, list := range byField {
			if ids, ok := memberIDs(jsonArray(list)); ok {
				obj.members[f] = ids
			} else {
				obj.fields[f], _ = canonicalSet(jsonArray(list))
			}
		}
		rd.objects = append(rd.objects, obj)
	}
	return []kindReading{rd}, readOK
}

// markChildren marks the stored objects of kind under parent unread.
func (p *observePass) markChildren(unread map[string]bool, kind, parent string) {
	for id := range p.state[kind] {
		if strings.HasPrefix(id, parent+"/") {
			unread[id] = true
		}
	}
}

// forgetOrphans marks the stored objects of kind whose parent is no longer
// listed to be forgotten. A parent's name holds no "/" (pve-firewall and
// pve-network both refuse one), so it is what precedes the first.
func (p *observePass) forgetOrphans(forget map[string]bool, kind string, listed map[string]bool) {
	for id := range p.state[kind] {
		if parent, _, _ := strings.Cut(id, "/"); !listed[parent] {
			forget[id] = true
		}
	}
}

// guestObject is a guest's configuration as one object: vm or container / its
// VMID, with the VMID beside it, as the guest History selects it.
func guestObject(guest db.Vm, node string, config map[string]json.RawMessage) observedObject {
	resourceType, spec := "vm", qemuConfigSpec
	if guest.Type == "lxc" {
		resourceType, spec = "container", lxcConfigSpec
	}
	id := strconv.Itoa(int(guest.Vmid))
	return observedObject{
		id: id, resourceType: resourceType, resourceID: id, vmid: guest.Vmid, node: node,
		fields: spec.canonicalFields(config),
	}
}

// guestKind is the state kind a guest's configuration is kept under, which is
// also the resource_type its changes are filed under.
func guestKind(guestType string) string {
	if guestType == "lxc" {
		return "container"
	}
	return "vm"
}

func stringField(item map[string]json.RawMessage, key string) string {
	v, _ := canonicalValue(item[key])
	return v
}

func jsonArray(items []json.RawMessage) json.RawMessage {
	if items == nil {
		return json.RawMessage("[]")
	}
	b, _ := json.Marshal(items)
	return b
}
