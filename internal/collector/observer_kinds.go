package collector

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bigjakk/nexara/internal/auditkey"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/pvefield"
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
	unsettled    bool                // a guest still being made: its reading is no baseline (guestObject)
	// shallow: past the cap, read by what its listing says of it alone (its
	// fields), and not its own objects (a vnet's subnets) or own read.
	shallow bool
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
	listed  int // past the cap, how many its listing held (capListing); 0 otherwise
	// childKind is the kind of a parent kind's own objects, childParentOf
	// their parent: a parent gone takes them with it, and its removal says how
	// many (observePass.childrenOf).
	childKind     string
	childParentOf func(id string) string
	// parentOf, for a kind of a parent's objects (an IP set's entries, a
	// group's rules, a vnet's subnets, a user's tokens), is the parent an id
	// belongs to: the cap counts each parent's objects apart. parents are the
	// parents listed, true for those whose objects were read; parentKind is
	// theirs when past that kind's cap they are not read (a parent read
	// shallow), so one of a parent read whole again appeared while it could
	// not be seen. A user's tokens come with the users' listing, read always.
	parentOf   func(id string) string
	parents    map[string]bool
	parentKind string
}

// shallowObjects is a listing's objects past the cap, read by what the listing
// says of them alone, keyed by idField.
func shallowObjects(kind, idField string, items []map[string]json.RawMessage) []observedObject {
	var out []observedObject
	for _, item := range items {
		if id := stringField(item, idField); auditkey.ValidID(id) {
			out = append(out, observedObject{id: id, resourceType: kind, resourceID: id, fields: listingSpec.canonicalFields(item), shallow: true})
		}
	}
	return out
}

// beforeSep is the parent of an id that is "<parent><sep><rest>": up to the
// first sep (an IP set or a vnet name holds no "/"), or with last up to the
// last (a user name may hold a "!", a token's own id may not).
func beforeSep(sep string, last bool) func(string) string {
	return func(id string) string {
		if i := strings.LastIndex(id, sep); last && i >= 0 {
			return id[:i]
		}
		parent, _, _ := strings.Cut(id, sep)
		return parent
	}
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
	{kind: "sdn_controller", read: sdnKind("sdn_controller", "controllers", "controller", sdnControllerSpec)},
	{kind: "sdn_ipam", read: sdnKind("sdn_ipam", "ipams", "ipam", sdnIPAMSpec)},
	{kind: "sdn_dns", read: sdnKind("sdn_dns", "dns", "dns", sdnDNSSpec)},
	{kind: "firewall_options", read: readFirewallOptions},
	{kind: "firewall_rule", read: readFirewallRules},
	{kind: "firewall_alias", read: readFirewallAliases},
	{kind: "firewall_ipset", read: readFirewallIPSets},
	{kind: "firewall_security_group", read: readFirewallGroups},
	{kind: "pve_user", read: readAccessUsers},
	{kind: "pve_group", read: accessKind("pve_group", "groups", "groupid", accessGroupSpec)},
	{kind: "pve_role", read: readAccessRoles},
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
	// pve-manager API2/Cluster/Mapping/{PCI,USB}.pm index: checks appear only
	// with check-node, never sent; map is one entry per node.
	mappingSpec = newFieldSpec([]string{"checks"}, []string{"map"})
	// pve-manager API2/Cluster.pm get_options: allowed-tags is derived per
	// caller from the tag settings and the token's privileges.
	clusterOptionsSpec = newFieldSpec([]string{"allowed-tags"}, nil)
	nodeDNSSpec        = newFieldSpec(nil, nil)
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
	// pve-firewall Firewall.pm parse_alias: ipversion is derived from the cidr.
	firewallAliasSpec = newFieldSpec([]string{"ipversion"}, nil)
	firewallPlainSpec = newFieldSpec(nil, nil)
	// An IP set entry's errors is the check of the moment against the aliases
	// it names (pve-firewall IPSet.pm), derived like a rule's.
	firewallIPSetEntrySpec = newFieldSpec([]string{"errors"}, nil)
	// pve-access-control API2/User.pm index: realm-type is derived from
	// domains.cfg, totp-locked and tfa-locked-until are TFA lockout state
	// (priv/tfa.cfg), and tokens are their own objects. A user's groups, and
	// a group's users, are kept as members: a group deleted leaves its users,
	// and only that group's delete explains it.
	accessUserSpec  = newFieldSpec([]string{"realm-type", "totp-locked", "tfa-locked-until", "tokens"}, nil).withMembers("groups")
	accessTokenSpec = newFieldSpec(nil, nil)
	accessGroupSpec = newFieldSpec(nil, nil).withMembers("users")
	// API2/Role.pm index: special is derived (a built-in role). privs are the
	// role's privileges, kept as members, so an update explains the ones its
	// row lists joining and, replacing the list, the ones it does not leaving.
	accessRoleSpec  = newFieldSpec([]string{"special"}, nil).withMembers("privs")
	acmeAccountSpec = newFieldSpec(nil, nil)
	// listingSpec reads what a listing says of an object read object by
	// object, its whole tag past the cap.
	listingSpec    = newFieldSpec(nil, nil)
	nodeConfigSpec = newFieldSpec(nil, nil)

	// The kinds an update handler diffs before it writes are pvefield's, so
	// the observer and the handler read their fields alike.
	backupSpec        = specOf(pvefield.Backup)
	networkSpec       = specOf(pvefield.Network)
	sdnZoneSpec       = specOf(pvefield.SDNZone)
	sdnVNetSpec       = specOf(pvefield.SDNVNet)
	sdnSubnetSpec     = specOf(pvefield.SDNSubnet)
	sdnControllerSpec = specOf(pvefield.SDNController)
	sdnIPAMSpec       = specOf(pvefield.SDNIPAM)
	sdnDNSSpec        = specOf(pvefield.SDNDNS)
	metricServerSpec  = specOf(pvefield.MetricServer)
	acmePluginSpec    = specOf(pvefield.ACMEPlugin)
)

// specOf is the observer's spec of one of pvefield's kinds.
func specOf(k pvefield.Kind) fieldSpec {
	return newFieldSpec(k.Ignore, k.Sets).withMembers(k.Members...)
}

// observeKindSchemas names how each kind's fields are read — its fieldSpec's
// digest, or how a reader builds the object — for the kind's signature
// (observePass.kindSig). TestObserveKindSchemas holds every kind to one.
var observeKindSchemas = map[string]string{
	"pool":                         "pool: comment; members vms, storage",
	"storage":                      storageSpec.digest(),
	"backup":                       backupSpec.digest(),
	"usb_mapping":                  mappingSpec.digest(),
	"pci_mapping":                  mappingSpec.digest(),
	"metric_server":                metricServerSpec.digest(),
	"cluster_options":              clusterOptionsSpec.digest(),
	"sdn_zone":                     sdnZoneSpec.digest(),
	"sdn_vnet":                     sdnVNetSpec.digest(),
	"sdn_subnet":                   "subnet: members cidr; " + sdnSubnetSpec.digest(),
	"sdn_controller":               sdnControllerSpec.digest(),
	"sdn_ipam":                     sdnIPAMSpec.digest(),
	"sdn_dns":                      sdnDNSSpec.digest(),
	"firewall_options":             firewallPlainSpec.digest(),
	"firewall_rule":                "rules: list; " + firewallRuleSpec.digest(),
	"firewall_alias":               firewallAliasSpec.digest(),
	"firewall_ipset":               firewallPlainSpec.digest(),
	"firewall_ipset_entry":         firewallIPSetEntrySpec.digest(),
	"firewall_security_group":      firewallPlainSpec.digest(),
	"firewall_security_group_rule": "rules: list; " + firewallRuleSpec.digest(),
	"pve_user":                     accessUserSpec.digest(),
	"pve_token":                    accessTokenSpec.digest(),
	"pve_group":                    accessGroupSpec.digest(),
	"pve_role":                     accessRoleSpec.digest(),
	"pve_acl":                      "acl: members users, groups, tokens and their grants as role:propagate:grantee",
	"acme_account":                 acmeAccountSpec.digest(),
	"acme_plugin":                  acmePluginSpec.digest(),
	"ha_resource":                  haResourceSpec.digest(),
	"ha_rule":                      haRuleSpec.digest(),
	"ha_group":                     haGroupSpec.digest(),
	"network":                      networkSpec.digest(),
	"node_options":                 nodeConfigSpec.digest(),
	"acme_config":                  "acme: " + nodeConfigSpec.digest(),
	"node_dns":                     nodeDNSSpec.digest(),
	"node_time":                    nodeTimeSpec.digest(),
	"node_firewall":                "rules: list; " + firewallRuleSpec.digest(),
	"vm":                           qemuConfigSpec.digest(),
	"container":                    lxcConfigSpec.digest(),
}

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
	items, shallow, listed := p.capListing("pool", items, func(it map[string]json.RawMessage) string { return stringField(it, "poolid") })
	reading.listed = listed
	reading.objects = shallowObjects("pool", "poolid", shallow)
	for _, item := range items {
		id := stringField(item, "poolid")
		if !auditkey.ValidID(id) {
			continue
		}
		target, err := proxmox.ObservePool(id)
		if err != nil {
			continue
		}
		raw, out, rerr := p.readWithError(ctx, target, p.o.gap)
		if apiErr := (*proxmox.APIError)(nil); errors.As(rerr, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
			// Before Proxmox VE 8.1 a pool is read at /pools/{poolid} only, and
			// the query form is refused as an unknown parameter.
			return nil, readUnsupported
		}
		detail, ok := decodeList(raw)
		if out != readOK || !ok || len(detail) != 1 {
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
	spec := metricServerSpec
	reading := kindReading{kind: "metric_server", unread: map[string]bool{}}
	items, shallow, listed := p.capListing("metric_server", items, func(it map[string]json.RawMessage) string { return stringField(it, "id") })
	reading.listed = listed
	reading.objects = shallowObjects("metric_server", "id", shallow)
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
	spec := nodeConfigSpec
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
	vnets := kindReading{kind: "sdn_vnet", childKind: "sdn_subnet", childParentOf: beforeSep("/", false)}
	subnets := kindReading{kind: "sdn_subnet", unread: map[string]bool{}, forget: map[string]bool{},
		parentOf: beforeSep("/", false), parents: map[string]bool{}, parentKind: "sdn_vnet"}
	listed := map[string]bool{}
	for _, item := range items {
		listed[stringField(item, "vnet")] = true
		subnets.parents[stringField(item, "vnet")] = false
	}
	held := p.heldChildren("sdn_subnet")
	// Past the cap only the vnets held field by field are read on: the
	// others' subnets are left as held until they are.
	items, shallow, count := p.capListing("sdn_vnet", items, func(it map[string]json.RawMessage) string { return stringField(it, "vnet") })
	vnets.listed = count
	for _, item := range shallow {
		vnet := stringField(item, "vnet")
		if !auditkey.ValidID(vnet) {
			continue
		}
		vnets.objects = append(vnets.objects, observedObject{
			id: vnet, resourceType: "sdn_vnet", resourceID: vnet, fields: sdnVNetSpec.canonicalFields(item), shallow: true,
		})
		markChildren(subnets.unread, held, vnet)
	}
	for _, item := range items {
		vnet := stringField(item, "vnet")
		if !auditkey.ValidID(vnet) {
			continue
		}
		listed[vnet] = true
		vnets.objects = append(vnets.objects, observedObject{
			id: vnet, resourceType: "sdn_vnet", resourceID: vnet, fields: sdnVNetSpec.canonicalFields(item),
		})
		t, err := proxmox.ObserveSDNSubnets(vnet)
		if err != nil {
			continue
		}
		entries, out := p.readList(ctx, t)
		if out != readOK {
			markChildren(subnets.unread, held, vnet)
			continue
		}
		subnets.parents[vnet] = true
		for _, e := range entries {
			id := auditkey.SDNSubnet(vnet, stringField(e, "subnet"))
			if stringField(e, "subnet") == "" || !auditkey.ValidID(id) {
				continue
			}
			obj := observedObject{id: id, resourceType: "sdn_subnet", resourceID: id}
			// The subnet's CIDR, as Nexara files a create whose id it could
			// not read back (<vnet>/<cidr>), so that row can name it.
			if cidr := vnet + "/" + stringField(e, "cidr"); stringField(e, "cidr") != "" && pvefield.IsMemberID(cidr) {
				rest := maps.Clone(e)
				delete(rest, "cidr")
				obj.fields, obj.members = sdnSubnetSpec.canonicalFields(rest), map[string][]string{"cidr": {cidr}}
			} else {
				obj.fields = sdnSubnetSpec.canonicalFields(e)
			}
			subnets.objects = append(subnets.objects, obj)
		}
	}
	forgetOrphans(subnets.forget, held, listed)
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
	sets := kindReading{kind: "firewall_ipset", childKind: "firewall_ipset_entry", childParentOf: beforeSep("/", false)}
	entries := kindReading{kind: "firewall_ipset_entry", unread: map[string]bool{}, forget: map[string]bool{},
		parentOf: beforeSep("/", false), parents: map[string]bool{}, parentKind: "firewall_ipset"}
	listed := map[string]bool{}
	for _, item := range items {
		listed[auditkey.FirewallName(stringField(item, "name"))] = true
		entries.parents[auditkey.FirewallName(stringField(item, "name"))] = false
	}
	held := p.heldChildren("firewall_ipset_entry")
	ipsetID := func(it map[string]json.RawMessage) string { return auditkey.FirewallName(stringField(it, "name")) }
	items, shallow, count := p.capListing("firewall_ipset", items, ipsetID)
	sets.listed = count
	for _, item := range shallow {
		if id := ipsetID(item); stringField(item, "name") != "" && auditkey.ValidID(id) {
			sets.objects = append(sets.objects, observedObject{
				id: id, resourceType: "firewall_ipset", resourceID: id, fields: firewallPlainSpec.canonicalFields(item), shallow: true,
			})
			markChildren(entries.unread, held, id)
		}
	}
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
			markChildren(entries.unread, held, id)
			continue
		}
		entries.parents[id] = true
		for _, e := range list {
			// Keyed as the set lists it (history-cluster's IPSetListed, how
			// a delete names it): a hand-edited config keeps an entry's host
			// bits, so 192.0.2.5/24 and 192.0.2.0/24 are two entries, which
			// one normalised key would collide.
			cidr := stringField(e, "cidr")
			key := auditkey.FirewallName(name) + "/" + cidr
			if cidr == "" || !auditkey.ValidID(key) {
				continue
			}
			entries.objects = append(entries.objects, observedObject{
				id: key, resourceType: "firewall_ipset_entry", resourceID: key, fields: firewallIPSetEntrySpec.canonicalFields(e),
			})
		}
	}
	forgetOrphans(entries.forget, held, listed)
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
	groups := kindReading{kind: "firewall_security_group", childKind: "firewall_security_group_rule",
		childParentOf: func(id string) string { return id }}
	// Each group's rules are one object, its own parent's.
	rules := kindReading{kind: "firewall_security_group_rule", unread: map[string]bool{}, forget: map[string]bool{},
		parentOf: func(id string) string { return id }, parents: map[string]bool{}, parentKind: "firewall_security_group"}
	listed := map[string]bool{}
	for _, item := range items {
		listed[auditkey.FirewallName(stringField(item, "group"))] = true
		rules.parents[auditkey.FirewallName(stringField(item, "group"))] = false
	}
	groupID := func(it map[string]json.RawMessage) string { return auditkey.FirewallName(stringField(it, "group")) }
	items, shallow, count := p.capListing("firewall_security_group", items, groupID)
	groups.listed = count
	for _, item := range shallow {
		if id := groupID(item); stringField(item, "group") != "" && auditkey.ValidID(id) {
			groups.objects = append(groups.objects, observedObject{
				id: id, resourceType: "firewall_security_group", resourceID: id, fields: firewallPlainSpec.canonicalFields(item), shallow: true,
			})
			rules.unread[id] = true
		}
	}
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
		rules.parents[id] = true
		rules.objects = append(rules.objects, observedObject{
			id: id, resourceType: "firewall_security_group_rule", resourceID: id,
			lists: map[string][]string{"rules": canonicalRules(list)},
		})
	}
	for _, id := range p.heldIDs("firewall_security_group_rule") {
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
	users := kindReading{kind: "pve_user", childKind: "pve_token", childParentOf: beforeSep("!", true)}
	tokens := kindReading{kind: "pve_token", parentOf: beforeSep("!", true), parents: map[string]bool{}}
	for _, item := range items {
		userid := stringField(item, "userid")
		if !auditkey.ValidID(userid) {
			continue
		}
		fields, members := accessUserSpec.split(item)
		users.objects = append(users.objects, observedObject{
			id: userid, resourceType: "pve_user", resourceID: userid, fields: fields, members: members,
		})
		tokens.parents[userid] = true
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
	// A user gone takes its tokens with it: its removal says how many.
	tokens.forget = map[string]bool{}
	for _, id := range p.heldIDs("pve_token") {
		if !tokens.parents[tokens.parentOf(id)] {
			tokens.forget[id] = true
		}
	}
	return []kindReading{users, tokens}, readOK
}

// readAccessRoles reads the roles Proxmox does not build in: a built-in role
// (special 1) changes its privileges only with a pve-access-control upgrade,
// which is no edit (AccessControl.pm's predefined roles).
func readAccessRoles(ctx context.Context, p *observePass) ([]kindReading, readOutcome) {
	target, _ := proxmox.ObserveAccess("roles")
	items, out := p.readList(ctx, target)
	if out != readOK {
		return nil, out
	}
	reading := kindReading{kind: "pve_role"}
	for _, item := range items {
		id := stringField(item, "roleid")
		if special, _ := canonicalValue(item["special"]); special == "1" || !auditkey.ValidID(id) {
			continue
		}
		fields, members := accessRoleSpec.split(item)
		reading.objects = append(reading.objects, observedObject{
			id: id, resourceType: "pve_role", resourceID: id, fields: fields, members: members,
		})
	}
	return []kindReading{reading}, readOK
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
// grant (user_grants ...) as "<role>:<propagate>:<grantee>", so a change says
// which grant came or went — a user deleted explains only that user's grants
// going, a role deleted only that role's. Role names and grantees are
// identities, kept in clear like every member; no reader selects them.
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
		who, _ := json.Marshal(ugid)
		grant, _ := json.Marshal(stringField(item, "roleid") + ":" + stringField(item, "propagate") + ":" + ugid)
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

// heldIDs is every id of kind the cluster holds, field by field or by presence.
func (p *observePass) heldIDs(kind string) []string {
	ids := slices.Collect(maps.Keys(p.state[kind]))
	for id := range p.presence[kind] {
		if _, kept := p.state[kind][id]; !kept {
			ids = append(ids, id) // kept field by field past the cap too
		}
	}
	return ids
}

// heldChildren is the held objects of a kind of a parent's objects, by
// parent: a parent's name holds no "/" (pve-firewall and pve-network both
// refuse one), so it is what precedes the first. Built once a reading, so a
// listing of many parents is not walked once for each.
func (p *observePass) heldChildren(kind string) map[string][]string {
	out := map[string][]string{}
	for _, id := range p.heldIDs(kind) {
		parent, _, _ := strings.Cut(id, "/")
		out[parent] = append(out[parent], id)
	}
	return out
}

// markChildren marks a parent's held objects unread.
func markChildren(unread map[string]bool, held map[string][]string, parent string) {
	for _, id := range held[parent] {
		unread[id] = true
	}
}

// forgetOrphans marks the held objects of the parents no longer listed to be
// forgotten: a parent gone takes them with it, and its removal says how many.
func forgetOrphans(forget map[string]bool, held map[string][]string, listed map[string]bool) {
	for parent, ids := range held {
		if !listed[parent] {
			for _, id := range ids {
				forget[id] = true
			}
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
	lock, _ := canonicalValue(config["lock"])
	return observedObject{
		id: id, resourceType: resourceType, resourceID: id, vmid: guest.Vmid, node: node,
		fields: spec.canonicalFields(config),
		// A clone's config is written with lock clone and rewritten after each
		// drive (qemu-server API2/Qemu.pm clone_vm), a container's made with
		// lock create (pve-container create_and_lock_config): read then, it is
		// half a guest, and the task names the clone's source, not it.
		unsettled: lock == "clone" || lock == "create",
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
