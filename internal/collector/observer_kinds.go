package collector

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"

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
// canonical value. Nothing here is stored as it is: observer.go tags it.
type observedObject struct {
	id           string
	resourceType string
	resourceID   string
	vmid         int32               // a guest's VMID; 0 for every other object
	node         string              // the node it is on or belongs to; "" for a cluster's object
	fields       map[string]string   // field -> canonical value
	lists        map[string][]string // field -> canonical items of an ordered list (a rule set)
	hardware     bool                // a physical NIC: it comes and goes with nobody editing
}

// kindReading is what one read of a kind produced. unread holds the objects it
// listed but could not read in full (a per-object read failed): they are left
// exactly as stored, neither compared nor removed.
type kindReading struct {
	kind    string
	objects []observedObject
	unread  map[string]bool
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

// observeClusterKinds is read in this order every pass.
var observeClusterKinds = []observeKind{
	{kind: "pool", read: readPools},
	{kind: "storage", read: listKind("storage", proxmox.ObserveStorage(), "storage", storageSpec)},
	{kind: "backup", read: listKind("backup", proxmox.ObserveBackupJobs(), "id", backupSpec)},
	{kind: "usb_mapping", read: listKind("usb_mapping", proxmox.ObserveUSBMappings(), "id", mappingSpec)},
	{kind: "pci_mapping", read: listKind("pci_mapping", proxmox.ObservePCIMappings(), "id", mappingSpec)},
	{kind: "metric_server", read: readMetricServers},
	{kind: "cluster_options", sysAudit: true, read: readClusterOptions},
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
	// read. vmid and exclude are comma lists of guests.
	backupSpec = newFieldSpec([]string{"next-run"}, []string{"vmid", "exclude"})
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
			reading.objects = append(reading.objects, observedObject{
				id: id, resourceType: kind, resourceID: id, fields: spec.canonicalFields(item),
			})
		}
		return []kindReading{reading}, readOK
	}
}

// readPools lists the pools, then reads each one's members: a pool's guests and
// storage, compared as sets of ids, never the members' run state (pve-manager
// API2/Pool.pm index, API2Tools extract_vm_stats). A pool whose own read fails
// is unread.
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
	var members []map[string]json.RawMessage
	_ = json.Unmarshal(entry["members"], &members)
	var vms, storage []json.RawMessage
	for _, m := range members {
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
	fields["vms"], _ = canonicalSet(jsonArray(vms))
	fields["storage"], _ = canonicalSet(jsonArray(storage))
	return observedObject{id: id, resourceType: "pool", resourceID: id, fields: fields}
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
// node / <node>, field firewall_rules, as the node's own audit rows are: a rule
// has no id but its position, which moves.
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
		id: node.Name, resourceType: "node", resourceID: node.Name, node: node.Name,
		lists: map[string][]string{"firewall_rules": canonicalRules(items)},
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
