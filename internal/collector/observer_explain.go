package collector

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// What explains a change the observer files (observer.go). The rules are
// applied by ClassifyObservedChanges (queries/cluster_object_state.sql), which
// documents each column; they live here so a kind's rules sit beside the kinds,
// and are tested as data.
//
// Each rule names audit rows. An audit row for the same object explains any
// change to it (sameKey) — Nexara's own, or a Proxmox task the collector
// ingested, which files under the same key. The other rules are what reaches
// one object from another's action: a guest's tasks reach its configuration,
// and a guest destroyed takes itself out of its pool and its backup jobs
// (qemu-server API2/Qemu.pm destroy_vm: remove_vm_access, and with purge
// remove_vmid_from_backup_jobs). A task explains only the fields it can write:
// a disk move explains the disk, not the memory someone changed in the GUI
// while it ran. An unknown action or worker explains nothing, so the change
// shows — the side to err on for a list of edits nobody recorded.

// explainRule is one rule, as ClassifyObservedChanges decodes it.
type explainRule struct {
	Kind      string `json:"kind"`       // the change's resource_type
	Change    string `json:"change"`     // added, removed or changed: every rule names one
	AuditType string `json:"audit_type"` // audit_log.resource_type; "" any (vmid, sid and cluster keys only)
	MatchKey  string `json:"match_key"`  // id, node, vmid, sid, cluster or details:<key>
	Untasked  bool   `json:"untasked"`   // only an audit row with no task
	ActionRe  string `json:"action_re"`  // the row's action; "" any
	ExceptRe  string `json:"except_re"`  // an action that never explains; "" none
	WorkerRe  string `json:"worker_re"`  // the row's UPID worker type; "" any
	FieldRe   string `json:"field_re"`   // the fields it explains; "" any
	DetailKey string `json:"detail_key"` // only a row whose details carry this key, not empty; "" any
	// DetailValue, with DetailKey, is the text that key must have ("" any).
	DetailValue string `json:"detail_value"`
	// Object and MemberOf name what a row is about, "<source>:<format>": the
	// UPID's id (upid), the row's resource_id (audit) or guest (vmid), or a
	// details key (details.<k>, or details-list.<k> for a comma list), put into
	// format at %s; or an ACL row's grants (grants.<list>: role, propagate and
	// grantee as %1$s, %2$s, %3$s). A MemberOf may join several sources with +.
	// Member left_unlisted is a member leaving that the row does NOT name: what
	// a list it replaced whole no longer holds. With Object, the
	// change's resource_id must be that name; with Member, the row explains
	// only that member joining or leaving a list (joined, left), or an object
	// removed with it as its last member (sole). Member with no MemberOf is any
	// member going that way; a MemberOf whose row names nobody (an empty list)
	// is none, and explains no object that came or went with no members.
	Object   string `json:"object"`
	Member   string `json:"member"`
	MemberOf string `json:"member_of"`
	// AuditIDIs keeps a rule to audit rows with this resource_id: the marker a
	// row carries when its object's id could not be found ("" any).
	AuditIDIs string `json:"audit_id_is"`
	// Counts says what a row does to a list's items, for a change that says
	// how many came and went: add1 one in, del1 one out, edit1 one in and one
	// out or one moved, adds any number in, dels any number out; "" anything.
	Counts string `json:"counts"`
	// GranteeRemoved keeps a row's ACL grantees leaving to those whose own
	// removal (pve_user, pve_group or pve_token removed) its task overlaps.
	GranteeRemoved bool `json:"grantee_removed"`
	// Appeared keeps a rule to fields the object did not have before
	// (detail.fields_appeared): a value Proxmox fills in where none was.
	Appeared bool `json:"appeared"`
	// FieldsFrom keeps a row to the fields its details name: config, a guest
	// config edit's keys and the keys it deleted or reverted (and, if it named
	// a disk, the unused<n> and boot a disk's change or removal moves);
	// settings, the settings and cleared it lists (the datacenter options, a
	// node's ACME); keys, its details' own keys (an HA or pool update);
	// fields, the names of the fields it sent (handlers' auditFieldNames).
	FieldsFrom string `json:"fields_from"`
	// Derived, with FieldsFrom fields, is what Proxmox rewrites with a field
	// sent, by its name, and with any update, at "*".
	Derived map[string][]string `json:"derived,omitempty"`
}

// byAction is the rules a kind's own audit rows give for an object made or
// taken away: an audit row for the same object explains one only by an action
// of the change's direction — an object created by a create, removed by a
// delete — so nothing done to an object in Nexara hides a different change
// made to it in Proxmox. An update's rules are a kind's own, each kept to what
// the update set. A direction with no action has no rule.
func byAction(kind string, added, removed []string) []explainRule {
	var rules []explainRule
	for _, d := range []struct {
		change  string
		actions []string
	}{{"added", added}, {"removed", removed}} {
		if len(d.actions) > 0 {
			rules = append(rules, explainRule{Kind: kind, Change: d.change, AuditType: kind, MatchKey: "id",
				ActionRe: anyOf(d.actions...)})
		}
	}
	return rules
}

// sentFields is byAction for a kind whose update row lists the fields it sent
// (auditFieldNames): the update explains those, and what Proxmox derives from
// them (derived, as explainRule.Derived).
func sentFields(kind, created, updated, deleted string, derived map[string][]string) []explainRule {
	return append(byAction(kind, []string{created}, []string{deleted}),
		explainRule{Kind: kind, Change: "changed", AuditType: kind, MatchKey: "id", ActionRe: "^" + updated + "$",
			FieldsFrom: "fields", Derived: derived})
}

// crudKeys is byAction for a kind whose update row records each field it set
// as a key of its details (ha.go, pools.go): the update explains only those.
func crudKeys(kind string) []explainRule {
	return append(byAction(kind, []string{"created"}, []string{"deleted"}),
		explainRule{Kind: kind, Change: "changed", AuditType: kind, MatchKey: "id", ActionRe: `^updated$`, FieldsFrom: "keys"})
}

// mappingRules is a resource mapping's own rows. Its update writes the whole
// map, and records the description only when it set or removed one, and a PCI
// mapping's mdev flag likewise (resource_mappings.go's update details).
func mappingRules(kind string, mdev bool) []explainRule {
	rule := func(detailKey, fieldRe string) explainRule {
		return explainRule{Kind: kind, Change: "changed", AuditType: kind, MatchKey: "id", ActionRe: `^updated$`,
			DetailKey: detailKey, FieldRe: fieldRe}
	}
	rules := append(byAction(kind, []string{"created"}, []string{"deleted"}),
		rule("", `^map$`), rule("description", `^description$`), rule("description_removed", `^description$`))
	if mdev {
		rules = append(rules, rule("mdev", `^mdev$`))
	}
	return rules
}

// sdnFields is sentFields for an SDN kind, whose actions carry the kind's
// name, and from whose fields Proxmox derives none.
func sdnFields(kind string) []explainRule {
	return sentFields(kind, kind+"_created", kind+"_updated", kind+"_deleted", nil)
}

// inDirections is one rule for each direction named: an action that can change
// an object or take it away, but never create one, gets two.
func inDirections(r explainRule, changes ...string) []explainRule {
	rules := make([]explainRule, 0, len(changes))
	for _, change := range changes {
		r.Change = change
		rules = append(rules, r)
	}
	return rules
}

// The tasks that define, extend or remove a storage (pve-storage
// API2/Disks/*.pm, pve-manager API2/Ceph/{Pool,FS}.pm). Each names the disk
// object it made or removed in its UPID, which is the storage's id when the
// task defined the storage. lvmthinremove names "<vg>-<lv>", which no format
// of a storage id is, so it explains nothing. A disk created on another node
// of a storage with a node list adds the node (API2/Storage/Config.pm
// create_or_update); a disk removed with cleanup-config takes the node out, or
// the storage with its last node (cleanup_storages_for_node), and with it the
// storage's ACL and its place in every pool (AccessControl.pm
// remove_storage_access).
var (
	diskCreateWorkers = []string{"dircreate", "lvmcreate", "lvmthincreate", "zfscreate"}
	cephCreateWorkers = []string{"cephfscreate", "cephcreatepool"}
	diskRemoveWorkers = []string{"dirremove", "lvmremove", "zfsremove"}
	cephRemoveWorkers = []string{"cephdestroyfs", "cephdestroypool"}
	storageRemovals   = slices.Concat(diskRemoveWorkers, cephRemoveWorkers)
)

const (
	qemuDiskFields = `^((ide|sata|scsi|virtio)[0-9]+|efidisk0|tpmstate0|unused[0-9]+)$`
	lxcDiskFields  = `^(rootfs|mp[0-9]+|unused[0-9]+)$`
)

// guestConfigActions are Nexara's own edits of a guest's configuration that run
// no task, so no worker type says what they are. Every action Nexara files for
// a guest with no task is either here or in guestOtherActions, which
// TestObservedActionVocabulary holds to the handlers. A config edit names the
// keys it set (vms.go SetVMConfig, containers.go SetContainerConfig); the rest
// write a disk or a CD-ROM drive and nothing else (guesttools engine.go).
var (
	guestConfigActions = slices.Concat([]string{"config_update"}, guestDiskActions)
	guestDiskActions   = []string{"disk_attach", "disk_detach", "disk_resize", "media_mount", "media_eject",
		"guest_tools_stage", "guest_tools_update", "guest_tools_cancel", "guest_tools_auto_cancel"}
)

// guestOtherActions are Nexara's guest rows with no task that write nothing in
// the guest's configuration: the pool is user.cfg, the policy Nexara's own, the
// migration and DRS rows bookkeeping around the tasks that do the moving.
var guestOtherActions = []string{
	"set_pool", "guest_tools_policy_update", "migrate_created", "migrate_started", "migrate_cancelled",
	"migrate_failed", "drs_migrate", "drs_migrate_failed", "drs_advisory", "snapshot_resync",
}

func anyOf(words ...string) string { return "^(" + strings.Join(words, "|") + ")$" }

// observedChangeRules is every rule, for the cluster-scoped kinds. The actions
// are the ones the handlers file each kind under (internal/api/handlers), the
// rolling update's (internal/rolling) and the tasks the collector ingests;
// TestObservedActionVocabulary holds every literal action a handler files for
// an observed kind to this table or to observedActionsExplainingNothing.
var observedChangeRules = slices.Concat(
	// A VM's configuration: tasks that rewrite it, tasks that rewrite its
	// disks, and Nexara's own untasked edits, each only the fields it names.
	// qmconfig is the asynchronous config API, which can write any key, and
	// its task names none: a GUI edit through it explains every field. A
	// snapshot's delete drops the disks it alone held into unused<n>. The
	// target side of a remote migration (qmtunnel, vzmtunnel) names the guest
	// it brings in; Nexara's clone names the clone in details.new_id.
	[]explainRule{
		{Kind: "vm", Change: "changed", MatchKey: "vmid",
			WorkerRe: anyOf("qmconfig", "qmrollback", "qmtemplate", "qmcreate", "qmrestore", "qmdestroy", "qmtunnel")},
		{Kind: "vm", Change: "changed", AuditType: "vm", MatchKey: "details:new_id", ActionRe: `^clone$`},
		{Kind: "vm", Change: "changed", MatchKey: "vmid",
			WorkerRe: anyOf("qmmove", "resize", "qmigrate"), FieldRe: qemuDiskFields},
		{Kind: "vm", Change: "changed", MatchKey: "vmid", WorkerRe: `^qmdelsnapshot$`, FieldRe: `^unused[0-9]+$`},
		{Kind: "vm", Change: "changed", AuditType: "vm", MatchKey: "vmid", Untasked: true,
			ActionRe: `^config_update$`, FieldsFrom: "config"},
		{Kind: "vm", Change: "changed", AuditType: "vm", MatchKey: "vmid", Untasked: true,
			ActionRe: anyOf(guestDiskActions...), FieldRe: qemuDiskFields},
		{Kind: "container", Change: "changed", MatchKey: "vmid",
			WorkerRe: anyOf("vzrollback", "vztemplate", "vzcreate", "vzrestore", "vzdestroy", "vzmtunnel")},
		{Kind: "container", Change: "changed", AuditType: "container", MatchKey: "details:new_id", ActionRe: `^clone$`},
		{Kind: "container", Change: "changed", MatchKey: "vmid",
			WorkerRe: anyOf("move_volume", "resize", "vzmigrate"), FieldRe: lxcDiskFields},
		{Kind: "container", Change: "changed", MatchKey: "vmid", WorkerRe: `^vzdelsnapshot$`, FieldRe: `^unused[0-9]+$`},
		{Kind: "container", Change: "changed", AuditType: "container", MatchKey: "vmid", Untasked: true,
			ActionRe: `^config_update$`, FieldsFrom: "config"},
		{Kind: "container", Change: "changed", AuditType: "container", MatchKey: "vmid", Untasked: true,
			ActionRe: anyOf(guestDiskActions...), FieldRe: lxcDiskFields},
	},

	// A pool's members move when a guest is created or restored into it,
	// destroyed, cloned into it through Nexara, or moved from a guest's page;
	// a storage removed leaves every pool it was in (AccessControl.pm
	// remove_storage_access). Each explains only the member it names: a
	// guest's destroy that guest leaving, never another. A clone in the
	// Proxmox GUI names its source in its UPID, so the clone's joining shows.
	crudKeys("pool"),
	[]explainRule{
		{Kind: "pool", Change: "changed", MatchKey: "cluster", WorkerRe: `^(qm|vz)(create|restore)$`,
			FieldRe: `^vms$`, Member: "joined", MemberOf: "upid:vms:%s"},
		{Kind: "pool", Change: "changed", MatchKey: "cluster", WorkerRe: `^(qmdestroy|vzdestroy)$`,
			FieldRe: `^vms$`, Member: "left", MemberOf: "upid:vms:%s"},
		{Kind: "pool", Change: "changed", AuditType: "vm", MatchKey: "cluster", ActionRe: `^clone$`,
			FieldRe: `^vms$`, Member: "joined", MemberOf: "details.new_id:vms:%s"},
		{Kind: "pool", Change: "changed", AuditType: "container", MatchKey: "cluster", ActionRe: `^clone$`,
			FieldRe: `^vms$`, Member: "joined", MemberOf: "details.new_id:vms:%s"},
		{Kind: "pool", Change: "changed", AuditType: "vm", MatchKey: "details:old_pool",
			ActionRe: `^set_pool$`, FieldRe: `^vms$`, Member: "left", MemberOf: "vmid:vms:%s"},
		{Kind: "pool", Change: "changed", AuditType: "vm", MatchKey: "details:new_pool",
			ActionRe: `^set_pool$`, FieldRe: `^vms$`, Member: "joined", MemberOf: "vmid:vms:%s"},
		{Kind: "pool", Change: "changed", AuditType: "storage", MatchKey: "cluster",
			ActionRe: `^(delete|delete_import_source)$`, FieldRe: `^storage$`, Member: "left", MemberOf: "audit:storage:%s"},
		{Kind: "pool", Change: "changed", MatchKey: "cluster", WorkerRe: anyOf(storageRemovals...),
			FieldRe: `^storage$`, Member: "left", MemberOf: "upid:storage:%s"},
	},

	// A storage, created, edited and removed through Nexara — an ESXi import
	// source is one, and enabling import content edits its content — and by
	// the disk and Ceph tasks that define, extend or remove the one they name.
	// An edit writes the fields it sent and deletes those it cleared, and no
	// plugin writes another (pve-storage API2/Storage/Config.pm update; the
	// on_update_hook of PBSPlugin.pm stores the encryption-key and
	// master-pubkey it was sent, the others keep secrets out of the config).
	byAction("storage", []string{"create", "create_import_source"}, []string{"delete", "delete_import_source"}),
	[]explainRule{
		{Kind: "storage", Change: "changed", AuditType: "storage", MatchKey: "id", ActionRe: `^update$`, FieldsFrom: "fields"},
		{Kind: "storage", Change: "changed", AuditType: "storage", MatchKey: "id",
			ActionRe: `^enable_import_content$`, FieldRe: `^content$`},
		{Kind: "storage", Change: "added", MatchKey: "cluster",
			WorkerRe: anyOf(slices.Concat(diskCreateWorkers, cephCreateWorkers)...), Object: "upid:%s"},
		{Kind: "storage", Change: "changed", MatchKey: "cluster",
			WorkerRe: anyOf(slices.Concat(diskCreateWorkers, diskRemoveWorkers)...), FieldRe: `^nodes$`, Object: "upid:%s"},
		{Kind: "storage", Change: "removed", MatchKey: "cluster", WorkerRe: anyOf(storageRemovals...), Object: "upid:%s"},
	},

	// A backup job; a guest destroyed with purge leaves every job it was in,
	// and a job it was the last guest of is deleted (VZDump::Plugin
	// remove_vmid_from_backup_jobs): that guest's destroy explains that guest
	// leaving, and the removal of a job it was the only guest of. An edit
	// writes what it sent and what Proxmox derives from it (pve-manager
	// API2/Backup.pm update_job): a vmid takes out all, exclude and pool; all
	// takes out vmid and pool; a pool takes out vmid, all and exclude; an
	// exclude without a pool sets all; and any edit of a job still in
	// vzdump.cron moves it to jobs.cfg, where it carries a type.
	sentFields("backup", "backup_job_created", "backup_job_updated", "backup_job_deleted", map[string][]string{
		"vmid": {"all", "exclude", "pool"}, "all": {"vmid", "pool"}, "pool": {"vmid", "all", "exclude"},
		"exclude": {"all"}, "*": {"type"},
	}),
	[]explainRule{
		// A job created through Nexara whose id Proxmox did not give back is
		// filed as backup / backup-job (history-backup's backupJobIDUnknown):
		// nothing names the job, so it explains the one job added in its
		// window, and none when two were (ClassifyObservedChanges).
		{Kind: "backup", Change: "added", AuditType: "backup", MatchKey: "cluster",
			ActionRe: `^backup_job_created$`, AuditIDIs: "backup-job"},
		{Kind: "backup", Change: "changed", MatchKey: "cluster", WorkerRe: `^(qmdestroy|vzdestroy)$`,
			FieldRe: `^vmid$`, Member: "left", MemberOf: "upid:vmid:%s"},
		{Kind: "backup", Change: "changed", MatchKey: "cluster", WorkerRe: `^(qmdestroy|vzdestroy)$`,
			FieldRe: `^exclude$`, Member: "left", MemberOf: "upid:exclude:%s"},
		{Kind: "backup", Change: "removed", MatchKey: "cluster", WorkerRe: `^(qmdestroy|vzdestroy)$`,
			Member: "sole", MemberOf: "upid:vmid:%s"},
	},

	// A mapping's delete that found it already gone is a no-op, which explains
	// no removal.
	mappingRules("usb_mapping", false),
	mappingRules("pci_mapping", true),
	// A metric server's edit writes what it sent; a token is kept apart, and
	// never read (pve-manager API2/Cluster/MetricServer.pm update,
	// Status/InfluxDB.pm on_update_hook).
	sentFields("metric_server", "created", "updated", "deleted", nil),
	// The datacenter options, which the rolling update also edits (it pauses
	// and restores the CRS option). The notes and the tag settings have routes
	// of their own, which write nothing else.
	[]explainRule{
		{Kind: "cluster_options", Change: "changed", AuditType: "cluster_options", MatchKey: "id",
			ActionRe: `^updated$`, FieldsFrom: "settings"},
		{Kind: "cluster_options", Change: "changed", AuditType: "cluster_options", MatchKey: "id",
			ActionRe: `^description_updated$`, FieldRe: `^description$`},
		{Kind: "cluster_options", Change: "changed", AuditType: "cluster_options", MatchKey: "id",
			ActionRe: `^tags_updated$`, FieldRe: `^(registered-tags|user-tag-access|tag-style)$`},
	},
	[]explainRule{{Kind: "acme_config", Change: "changed", AuditType: "acme_config", MatchKey: "id",
		ActionRe: `^updated$`, FieldsFrom: "settings"}},

	// A node's interfaces: their own rows, and a revert of the pending
	// changes, which is filed under the node and undoes any of them, in any
	// direction. An edit writes what it sent and what Proxmox derives from it
	// (pve-manager API2/Network.pm update_network): every edit sets method and
	// method6, by whether it sent an address; a cidr is split into address
	// and netmask, and cleared takes them along. The file holds a netmask only
	// beside its address, and the reading derives cidr from both and folds
	// comments6 into comments (pve-common Network/Interfaces.pm).
	sentFields("network", "interface_created", "interface_updated", "interface_deleted", map[string][]string{
		"*":    {"method", "method6"},
		"cidr": {"address", "netmask"}, "address": {"cidr", "netmask"}, "netmask": {"cidr"},
		"cidr6": {"address6", "netmask6"}, "address6": {"cidr6", "netmask6"}, "netmask6": {"cidr6"},
		"comments6": {"comments"},
	}),
	inDirections(explainRule{Kind: "network", AuditType: "network", MatchKey: "node", ActionRe: `^network_reverted$`},
		"added", "changed", "removed"),

	// A node's own objects all file as node / <node>, beside everything else
	// done to the node, so each action explains only its own fields.
	[]explainRule{
		{Kind: "node", Change: "changed", AuditType: "node", MatchKey: "id",
			ActionRe: `^set_dns$`, FieldRe: `^(search|dns1|dns2|dns3)$`},
		{Kind: "node", Change: "changed", AuditType: "node", MatchKey: "id",
			ActionRe: `^set_timezone$`, FieldRe: `^timezone$`},
		{Kind: "node", Change: "changed", AuditType: "node", MatchKey: "id",
			ActionRe: `^set_options$`, FieldRe: `^(description|wakeonlan|startall-onboot-delay|ballooning-target)$`},
	},

	// HA: a start or stop of an HA guest sets its resource's requested state,
	// and a guest destroyed with purge leaves the HA configuration — its
	// resource, and its place in every rule (HA/Config.pm
	// delete_service_from_config). The rolling update disables and restores
	// rules. A rule's delete that found it gone explains nothing.
	crudKeys("ha_resource"),
	crudKeys("ha_group"),
	crudKeys("ha_rule"),
	[]explainRule{
		{Kind: "ha_resource", Change: "changed", MatchKey: "sid", WorkerRe: anyOf("hastart", "hastop"), FieldRe: `^state$`},
		{Kind: "ha_resource", Change: "removed", MatchKey: "sid", WorkerRe: `^(qmdestroy|vzdestroy)$`},
		{Kind: "ha_rule", Change: "changed", MatchKey: "cluster", WorkerRe: `^qmdestroy$`,
			FieldRe: `^resources$`, Member: "left", MemberOf: "upid:resources:vm:%s"},
		{Kind: "ha_rule", Change: "changed", MatchKey: "cluster", WorkerRe: `^vzdestroy$`,
			FieldRe: `^resources$`, Member: "left", MemberOf: "upid:resources:ct:%s"},
		{Kind: "ha_rule", Change: "removed", MatchKey: "cluster", WorkerRe: `^qmdestroy$`,
			Member: "sole", MemberOf: "upid:resources:vm:%s"},
		{Kind: "ha_rule", Change: "removed", MatchKey: "cluster", WorkerRe: `^vzdestroy$`,
			Member: "sole", MemberOf: "upid:resources:ct:%s"},
		// An HA resource's delete purges by default (pve-ha-manager
		// API2/HA/Resources.pm delete: purge // 1), which Nexara leaves as it
		// is: the sid leaves every rule, and a rule it was the last of goes.
		{Kind: "ha_rule", Change: "changed", AuditType: "ha_resource", MatchKey: "cluster", ActionRe: `^deleted$`,
			FieldRe: `^resources$`, Member: "left", MemberOf: "audit:resources:%s"},
		{Kind: "ha_rule", Change: "removed", AuditType: "ha_resource", MatchKey: "cluster", ActionRe: `^deleted$`,
			Member: "sole", MemberOf: "audit:resources:%s"},
	},

	// SDN objects. An edit writes what it sent (pve-network
	// API2/Network/SDN/*.pm update, whose hooks check and write no field but
	// one): an EVPN zone left with no mac is given one (Zones/EvpnPlugin.pm
	// on_update_hook), so a zone's edit explains a mac appearing, never one
	// changed.
	sdnFields("sdn_zone"),
	[]explainRule{{Kind: "sdn_zone", Change: "changed", AuditType: "sdn_zone", MatchKey: "id",
		ActionRe: `^sdn_zone_updated$`, FieldRe: `^mac$`, Appeared: true}},
	sdnFields("sdn_vnet"),
	sdnFields("sdn_subnet"),
	[]explainRule{
		// A subnet created through Nexara whose id could not be read back is
		// filed under its CIDR (sdn.go sdnSubnetKey), which the subnet lists.
		{Kind: "sdn_subnet", Change: "added", AuditType: "sdn_subnet", MatchKey: "cluster",
			ActionRe: `^sdn_subnet_created$`, Member: "joined", MemberOf: "audit:cidr:%s"},
	},
	sdnFields("sdn_controller"),
	sdnFields("sdn_ipam"),
	sdnFields("sdn_dns"),

	// The cluster firewall. Its options and rule sets always exist, so they
	// only change; a template applied adds to the rules. A node's rule set is
	// the same kind under its scope, node/<node>, and Nexara files its edits
	// there too (node_firewall.go), so one key serves both. An alias renamed
	// through Nexara is filed under its old name with the new one in
	// details.rename: the old name's removal and the new one's addition are
	// both that update, and an update that renamed nothing removes nothing.
	[]explainRule{{Kind: "firewall_options", Change: "changed", AuditType: "firewall_options", MatchKey: "id",
		ActionRe: `^firewall_options_updated$`, FieldsFrom: "keys"}},
	ruleSetRules("firewall_rule", "firewall_rule", "^rules$",
		"firewall_rule_created", "firewall_rule_updated", "firewall_rule_deleted"),
	[]explainRule{
		{Kind: "firewall_rule", Change: "changed", AuditType: "firewall_rule", MatchKey: "id",
			ActionRe: `^template_applied$`, FieldRe: `^rules$`, Counts: "adds"},
	},
	// An alias's edit writes it whole (pve-firewall API2/Firewall/Aliases.pm
	// update_alias): its name as the request spelled it, the cidr it must
	// send, and a comment only if it sent one.
	sentFields("firewall_alias", "created", "updated", "deleted", map[string][]string{"*": {"name", "cidr", "comment"}}),
	[]explainRule{
		{Kind: "firewall_alias", Change: "added", AuditType: "firewall_alias", MatchKey: "details:rename", ActionRe: `^updated$`},
		{Kind: "firewall_alias", Change: "removed", AuditType: "firewall_alias", MatchKey: "id", ActionRe: `^updated$`,
			DetailKey: "rename"},
	},
	byAction("firewall_ipset", []string{"created"}, []string{"deleted"}),
	byAction("firewall_ipset_entry", []string{"created"}, []string{"deleted"}),
	byAction("firewall_security_group", []string{"created"}, []string{"deleted"}),
	ruleSetRules("firewall_security_group_rule", "firewall_security_group_rule", "^rules$",
		"rule_created", "rule_updated", "rule_deleted"),

	// Access. A user's own row explains the fields it says it set (enable,
	// expire, groups_changed: accessUserUpdateDetails) and the ones it never
	// records (names, e-mail, comment, keys); a token's likewise. A group's
	// update takes only its comment (pve-access-control API2/Group.pm). A user
	// joins or leaves a group by a user edit that set its groups, and leaves
	// with its own delete or the group's.
	byAction("pve_user", []string{"created"}, []string{"deleted"}),
	byAction("pve_token", []string{"created"}, []string{"deleted"}),
	byAction("pve_group", []string{"created"}, []string{"deleted"}),
	byAction("pve_role", []string{"created"}, []string{"deleted"}),
	[]explainRule{
		{Kind: "pve_token", Change: "changed", AuditType: "pve_token", MatchKey: "id",
			ActionRe: anyOf("updated", "regenerated"), FieldRe: `^comment$`},
		{Kind: "pve_token", Change: "changed", AuditType: "pve_token", MatchKey: "id",
			ActionRe: anyOf("updated", "regenerated"), DetailKey: "expire", FieldRe: `^expire$`},
		{Kind: "pve_token", Change: "changed", AuditType: "pve_token", MatchKey: "id",
			ActionRe: anyOf("updated", "regenerated"), DetailKey: "privsep", FieldRe: `^privsep$`},
		{Kind: "pve_group", Change: "changed", AuditType: "pve_group", MatchKey: "id",
			ActionRe: `^updated$`, FieldRe: `^comment$`},
		// A role's privileges are members. Its update records the list it sent
		// and append: what joins is on the list, and without append the list
		// replaced the role's, so what leaves is what the list does not hold.
		{Kind: "pve_role", Change: "changed", AuditType: "pve_role", MatchKey: "id",
			ActionRe: `^updated$`, FieldRe: `^privs$`, Member: "joined", MemberOf: "details-list.privs:privs:%s"},
		{Kind: "pve_role", Change: "changed", AuditType: "pve_role", MatchKey: "id", ActionRe: `^updated$`,
			DetailKey: "append", DetailValue: "false", FieldRe: `^privs$`, Member: "left_unlisted",
			MemberOf: "details-list.privs:privs:%s"},
		{Kind: "pve_user", Change: "changed", AuditType: "pve_group", MatchKey: "cluster",
			ActionRe: `^deleted$`, FieldRe: `^groups$`, Member: "left", MemberOf: "audit:groups:%s"},
		{Kind: "pve_group", Change: "changed", AuditType: "pve_user", MatchKey: "cluster", ActionRe: `^created$`,
			DetailKey: "groups_set", FieldRe: `^users$`, Member: "joined", MemberOf: "audit:users:%s"},
		{Kind: "pve_group", Change: "changed", AuditType: "pve_user", MatchKey: "cluster", ActionRe: `^updated$`,
			DetailKey: "groups_changed", FieldRe: `^users$`, Member: "joined", MemberOf: "audit:users:%s"},
		{Kind: "pve_group", Change: "changed", AuditType: "pve_user", MatchKey: "cluster", ActionRe: `^updated$`,
			DetailKey: "groups_changed", FieldRe: `^users$`, Member: "left", MemberOf: "audit:users:%s"},
		{Kind: "pve_group", Change: "changed", AuditType: "pve_user", MatchKey: "cluster", ActionRe: `^deleted$`,
			FieldRe: `^users$`, Member: "left", MemberOf: "audit:users:%s"},
		// A user deleted takes its tokens (user@realm!tokenid).
		{Kind: "pve_token", Change: "removed", AuditType: "pve_user", MatchKey: "cluster",
			ActionRe: `^deleted$`, Object: "audit:%s!*"},
	},
	accessUserUpdates,

	// The ACL. Nexara's grant and revoke on a path explain only the grantees
	// the row names (its users, groups and tokens): a grant brings them in and
	// changes their grants any way (a propagate flag is one grant out and one
	// in), a revoke only takes out. A path is added by its first grant and
	// removed with its last, and only when every grantee that came or went is
	// one the row names.
	aclGrantRules,
	// Anything deleted takes its ACL entries with it (AccessControl.pm
	// delete_user_acl, delete_group_acl and the like): only that grantee
	// leaving, and grants only going, never one arriving — a deletion never
	// hides a grant made meanwhile. A path goes with the grantee only if it
	// held no one else. A role's delete takes out that role's grants alone,
	// and none arrive.
	aclDeleteRules("pve_user", "users", "user_grants"),
	aclDeleteRules("pve_group", "groups", "group_grants"),
	aclDeleteRules("pve_token", "tokens", "token_grants"),
	[]explainRule{
		// Proxmox drops a deleted role from every ACL entry: its grants go,
		// and a grantee that held no other leaves (the grants pin that).
		{Kind: "pve_acl", Change: "changed", AuditType: "pve_role", MatchKey: "cluster", ActionRe: `^deleted$`,
			FieldRe: `^(user|group|token)_grants$`, Member: "left",
			MemberOf: "audit:user_grants:%s:*|group_grants:%s:*|token_grants:%s:*"},
		{Kind: "pve_acl", Change: "changed", AuditType: "pve_role", MatchKey: "cluster", ActionRe: `^deleted$`,
			FieldRe: `^(users|groups|tokens)$`, Member: "left"},
		// A guest, a storage or a pool removed takes its own path
		// (remove_vm_access, remove_storage_access, delete_pool_acl).
		{Kind: "pve_acl", Change: "removed", MatchKey: "cluster", WorkerRe: `^(qmdestroy|vzdestroy)$`, Object: "upid:/vms/%s"},
		{Kind: "pve_acl", Change: "removed", MatchKey: "cluster", WorkerRe: anyOf(storageRemovals...),
			Object: "upid:/storage/%s"},
		{Kind: "pve_acl", Change: "removed", AuditType: "storage", MatchKey: "cluster",
			ActionRe: `^(delete|delete_import_source)$`, Object: "audit:/storage/%s"},
		{Kind: "pve_acl", Change: "removed", AuditType: "pool", MatchKey: "cluster", ActionRe: `^deleted$`,
			Object: "audit:/pool/%s"},
		{Kind: "pve_acl", Change: "removed", AuditType: "pool", MatchKey: "cluster", ActionRe: `^deleted$`,
			Object: "audit:/pool/%s/*"},
		{Kind: "pve_acl", Change: "changed", AuditType: "pool", MatchKey: "cluster", ActionRe: `^deleted$`,
			Member: "left", Object: "audit:/pool/%s"},
	},
	realmSyncRules,

	// ACME. An account registered or deactivated in the Proxmox GUI is a task
	// whose UPID names no account.
	byAction("acme_account", []string{"created"}, []string{"deleted"}),
	[]explainRule{
		{Kind: "acme_account", Change: "added", MatchKey: "cluster", WorkerRe: `^acmeregister$`},
		{Kind: "acme_account", Change: "removed", MatchKey: "cluster", WorkerRe: `^acmedeactivate$`},
	},
	// An ACME plugin's edit writes what it sent (pve-manager
	// API2/ACMEPlugin.pm update_plugin).
	sentFields("acme_plugin", "created", "updated", "deleted", nil),
)

// ruleSetRules is what a rule set's own rows explain, by count: a rule created
// one in, one updated one in and one out or one moved (an edit, or a move),
// one deleted one out (ClassifyObservedChanges' listed). Firewall rules apply
// in order, so a create elsewhere never explains a rule moved.
func ruleSetRules(kind, auditType, fieldRe, created, updated, deleted string) []explainRule {
	rule := func(action, counts string) explainRule {
		return explainRule{Kind: kind, Change: "changed", AuditType: auditType, MatchKey: "id",
			ActionRe: "^" + action + "$", FieldRe: fieldRe, Counts: counts}
	}
	return []explainRule{rule(created, "add1"), rule(updated, "edit1"), rule(deleted, "del1")}
}

// accessUserUpdates is what a user's update explains: the fields its row says
// it set (enable, expire, groups_changed, keys_changed), and the ones it never
// records, its names, e-mail and comment, which any update may have set.
var accessUserUpdates = []explainRule{
	{Kind: "pve_user", Change: "changed", AuditType: "pve_user", MatchKey: "id", ActionRe: `^updated$`,
		FieldRe: `^(firstname|lastname|email|comment)$`},
	{Kind: "pve_user", Change: "changed", AuditType: "pve_user", MatchKey: "id", ActionRe: `^updated$`,
		DetailKey: "keys_changed", FieldRe: `^keys$`},
	{Kind: "pve_user", Change: "changed", AuditType: "pve_user", MatchKey: "id", ActionRe: `^updated$`,
		DetailKey: "enable", FieldRe: `^enable$`},
	{Kind: "pve_user", Change: "changed", AuditType: "pve_user", MatchKey: "id", ActionRe: `^updated$`,
		DetailKey: "expire", FieldRe: `^expire$`},
	{Kind: "pve_user", Change: "changed", AuditType: "pve_user", MatchKey: "id", ActionRe: `^updated$`,
		DetailKey: "groups_changed", FieldRe: `^groups$`},
}

// aclGrantees are the three kinds of grantee: the key a Nexara ACL row names
// them under (UpdateACL's details), and the two fields of an ACL path
// (readAccessACL).
var aclGrantees = []struct{ key, who, grants string }{
	{"users", "users", "user_grants"}, {"groups", "groups", "group_grants"}, {"tokens", "tokens", "token_grants"},
}

// aclGrantRules is Nexara's own grant and revoke on a path, for what its row
// names: each grantee of its lists (users, groups, tokens) with each of its
// roles, at the propagate it sent or Proxmox's default (grants.<list>, ACL.pm
// update_acl). A grant brings those grants in, and takes out the same role of
// the same grantee at the other propagate; a revoke takes out that role of
// that grantee whatever its propagate. A path that came or went with them is
// theirs only if nothing else came or went with it (all three, joined by +).
var aclGrantRules = func() []explainRule {
	rules := make([]explainRule, 0, 5*len(aclGrantees)+2)
	added := make([]string, 0, 2*len(aclGrantees))
	removed := make([]string, 0, 2*len(aclGrantees))
	for _, g := range aclGrantees {
		who := "details-list." + g.key + ":" + g.who + ":%s"
		exact := "grants." + g.key + ":" + g.grants + ":%1$s:%2$s:%3$s"
		anyPropagate := "grants." + g.key + ":" + g.grants + ":%1$s:*:%3$s"
		added, removed = append(added, who, exact), append(removed, who, anyPropagate)
		rule := func(action, field, member, memberOf string) explainRule {
			return explainRule{Kind: "pve_acl", Change: "changed", AuditType: "pve_acl", MatchKey: "id",
				ActionRe: "^" + action + "$", FieldRe: "^" + field + "$", Member: member, MemberOf: memberOf}
		}
		rules = append(rules,
			rule("granted", g.who, "joined", who),
			rule("granted", g.grants, "joined", exact),
			rule("granted", g.grants, "left", anyPropagate),
			rule("revoked", g.who, "left", who),
			rule("revoked", g.grants, "left", anyPropagate),
		)
	}
	return append(rules,
		explainRule{Kind: "pve_acl", Change: "added", AuditType: "pve_acl", MatchKey: "id", ActionRe: `^granted$`,
			Member: "joined", MemberOf: strings.Join(added, "+")},
		explainRule{Kind: "pve_acl", Change: "removed", AuditType: "pve_acl", MatchKey: "id", ActionRe: `^revoked$`,
			Member: "left", MemberOf: strings.Join(removed, "+")},
	)
}()

// aclDeleteRules is what a deleted grantee (a user, a group, a token) does to
// the ACL: it leaves every path, with its grants, and a path it alone was
// granted on goes.
func aclDeleteRules(auditType, who, grants string) []explainRule {
	named := "audit:" + who + ":%s|" + grants + ":*:%s"
	return []explainRule{
		{Kind: "pve_acl", Change: "changed", AuditType: auditType, MatchKey: "cluster", ActionRe: `^deleted$`,
			FieldRe: "^(" + who + "|" + grants + ")$", Member: "left", MemberOf: named},
		{Kind: "pve_acl", Change: "removed", AuditType: auditType, MatchKey: "cluster", ActionRe: `^deleted$`,
			Member: "sole", MemberOf: named},
	}
}

// realmSyncRules is a realm sync (pve-access-control API2/Domains.pm sync,
// worker auth-realm-sync and the scheduled job's, both naming the realm). It
// adds and removes that realm's users (user@realm) and the groups it syncs
// (name-realm, LDAP.pm get_groups), and with remove-vanished takes their ACL
// entries out. A group's edit is its comment, which overwriting clears, and its
// users, which it replaces whole (update_groups): that realm's users join it,
// and whoever was in it may leave. So a user joins only a group the sync
// makes, and may leave one whoever it is. A dry run (auth-realm-sync-test)
// writes nothing.
//
// What a sync writes on a user it already has, transcribed (LDAP.pm get_users,
// Domains.pm update_users): every attribute the directory returns that maps to
// one of enable, expire, firstname, lastname, email, comment and keys
// overwrites it. The default map takes an LDAP attribute of each of those
// names, and sync_attributes only adds mappings to the same seven, so the
// realm's config could not narrow them. With remove-vanished=properties the
// record is replaced by what the directory returned, so any of them but enable
// (kept from the old record; enable-new is only for a user it creates) may be
// cleared. All seven are its, enable too: verify_sync_attribute_value takes it
// "for backwards compat".
var realmSyncRules = func() []explainRule {
	const sync = `^auth-realm-sync$`
	return slices.Concat(
		inDirections(explainRule{Kind: "pve_user", MatchKey: "cluster", WorkerRe: sync, Object: "upid:*@%s"},
			"added", "removed"),
		inDirections(explainRule{Kind: "pve_group", MatchKey: "cluster", WorkerRe: sync, Object: "upid:*-%s"},
			"added", "removed"),
		[]explainRule{
			{Kind: "pve_user", Change: "changed", MatchKey: "cluster", WorkerRe: sync, Object: "upid:*@%s",
				FieldRe: `^(enable|expire|firstname|lastname|email|comment|keys)$`},
			{Kind: "pve_user", Change: "changed", MatchKey: "cluster", WorkerRe: sync, Object: "upid:*@%s",
				FieldRe: `^groups$`, Member: "joined", MemberOf: "upid:groups:*-%s"},
			{Kind: "pve_user", Change: "changed", MatchKey: "cluster", WorkerRe: sync,
				FieldRe: `^groups$`, Member: "left", MemberOf: "upid:groups:*-%s"},
			{Kind: "pve_group", Change: "changed", MatchKey: "cluster", WorkerRe: sync, Object: "upid:*-%s",
				FieldRe: `^comment$`},
			{Kind: "pve_group", Change: "changed", MatchKey: "cluster", WorkerRe: sync, Object: "upid:*-%s",
				FieldRe: `^users$`, Member: "joined", MemberOf: "upid:users:*@%s"},
			{Kind: "pve_group", Change: "changed", MatchKey: "cluster", WorkerRe: sync, Object: "upid:*-%s",
				FieldRe: `^users$`, Member: "left"},
			// With remove-vanished acl a sync purges the ACL of what vanished
			// from the directory: a grantee leaving is this sync's only when
			// the same sync removed that user or group.
			{Kind: "pve_acl", Change: "changed", MatchKey: "cluster", WorkerRe: sync,
				FieldRe: `^(users|user_grants|groups|group_grants)$`, Member: "left",
				MemberOf: "upid:users:*@%s|user_grants:*@%s|groups:*-%s|group_grants:*-%s", GranteeRemoved: true},
			{Kind: "pve_acl", Change: "removed", MatchKey: "cluster", WorkerRe: sync, Member: "sole",
				MemberOf: "upid:users:*@%s|user_grants:*@%s|groups:*-%s|group_grants:*-%s", GranteeRemoved: true},
		},
	)
}()

// observedActionsExplainingNothing are the actions handlers and the task
// ingest (proxmoxTaskMap) file under an observed kind that change none of what
// the observer compares: a run, a download, a read. TestObservedActionVocabulary
// holds every literal action to a rule or to this list. A PBS's datastore and
// job actions are its server's (pbs_server), which observedGlobalRules decides.
var observedActionsExplainingNothing = map[string][]string{
	"storage": {"delete_content", "download_appliance", "download_url", "pull_oci",
		"upload", "virtio_win_config_update", "virtio_win_download", "prune_backups"},
	"backup":  {"backup", "backup_job_run", "backup_job_run_requested", "backup_restored", "backup_triggered"},
	"network": {"network_applied"},
	"node": {"acme_register", "apt_update", "create_directory", "create_lvm", "create_lvmthin", "create_zfs_pool",
		"delete_lvm", "delete_lvmthin", "delete_zfs_pool", "download", "download_report", "evacuate", "image_copy",
		"initialize_gpt", "node_maintenance_enter", "node_maintenance_exit", "read_memory_modules", "reboot",
		"service_reload", "shutdown", "start_all", "stop_all", "wipe_disk"},
	"usb_mapping": {"already_deleted"},
	"pci_mapping": {"already_deleted"},
	"ha_rule":     {"already_deleted"},
	// An account's details are root@pam's alone, so only its name is read: an
	// update of its contact changes nothing the observer sees.
	"acme_account": {"updated"},
}

// observedGlobalRules is the rules for the global kinds' rows. A PBS sync or
// verify job is never edited by Nexara — it only runs them — so nothing on
// record explains one edited on the PBS, and a run or a garbage collection
// Nexara started on the same server must not hide it. Their classifier reads
// 'id' rules only, without detail_key or upid_object.
var observedGlobalRules = []explainRule{}

var (
	observedChangeRulesJSON = mustRulesJSON(observedChangeRules)
	observedGlobalRulesJSON = mustRulesJSON(observedGlobalRules)
)

func mustRulesJSON(rules []explainRule) json.RawMessage {
	b, err := json.Marshal(rules)
	if err != nil {
		panic(err)
	}
	return b
}

// observedLookback caps how long before a change was seen an audit row may lie
// and still explain it. An edit is found at most one cycle after it is made —
// one OBJECT_OBSERVE_INTERVAL, longer on a cluster of thousands of guests,
// about nine minutes at 5,000 — so the row of whoever made it is never older
// than that. Without a cap, a change first seen after an outage has the whole
// gap for its window, and one row from the gap's start explains everything
// found at its end. An hour covers the default cycle many times over; a longer
// interval gets three cycles. A change whose explanation lies further back
// shows: the side to err on.
func observedLookback(interval time.Duration) time.Duration {
	return max(time.Hour, 3*interval)
}

// classifyObservedChanges decides the observer's settled changes on a cluster
// (ClassifyObservedChanges). It runs on every slow pass, after the task ingest,
// so a task that explains a change made in the Proxmox GUI is on record first;
// a change waits for a pass whose ingest listed every online node, the day at
// most. A failure is only logged: the changes stay unclassified, and hidden.
func (s *Syncer) classifyObservedChanges(ctx context.Context, cluster db.Cluster, allNodesListed bool) {
	ctx, cancel := context.WithTimeout(ctx, observeClassifyTimeout)
	defer cancel()
	for ctx.Err() == nil {
		batch := s.classifyBatch(cluster.ID)
		n, err := s.queries.ClassifyObservedChanges(ctx, db.ClassifyObservedChangesParams{
			Rules:           observedChangeRulesJSON,
			LookbackSeconds: int32(observedLookback(s.objectObserveInterval).Seconds()),
			ClusterID:       cluster.ID,
			SettledBefore:   time.Now().Add(-statusChangeSettle),
			AllNodesListed:  allNodesListed,
			GiveUpBefore:    time.Now().Add(-statusChangeGiveUp),
			MaxRows:         batch,
		})
		s.settleBatch(cluster.ID, batch, err != nil && (ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded)))
		if err != nil {
			s.logger.Warn("failed to classify observed object changes", "cluster_id", cluster.ID, "error", err)
			return
		}
		if n < int64(batch) {
			return
		}
	}
}

// classifyBatch is how many changes one classification statement of a
// cluster's (uuid.Nil: the global kinds') decides.
func (s *Syncer) classifyBatch(key uuid.UUID) int32 {
	if v, ok := s.observeBatches.Load(key); ok {
		return v.(int32)
	}
	return observeClassifyBatch
}

// settleBatch halves the next statement's batch after one ran out of time —
// down to observeClassifyMinBatch — so a pass does not retry the same oldest
// rows at the size that timed out, and doubles it back after one that did not.
func (s *Syncer) settleBatch(key uuid.UUID, batch int32, timedOut bool) {
	next := min(2*batch, observeClassifyBatch)
	if timedOut {
		next = max(batch/2, observeClassifyMinBatch)
	}
	if next == observeClassifyBatch {
		s.observeBatches.Delete(key)
	} else {
		s.observeBatches.Store(key, next)
	}
}

// The classification's bounds: it decides at most observeClassifyBatch changes
// a statement, the oldest first, and gives a pass observeClassifyTimeout; what
// is left waits for the next pass, and what waits past the give-up is shown
// (ShowUnclassifiedObservedChanges).
const (
	observeClassifyBatch    int32 = 500
	observeClassifyMinBatch int32 = 25
	observeClassifyTimeout        = 30 * time.Second
)

// classifyGlobalObjectChanges decides the settled changes of the global kinds
// (ClassifyGlobalObjectChanges), after each PBS pass.
func (s *Syncer) classifyGlobalObjectChanges(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, observeClassifyTimeout)
	defer cancel()
	for ctx.Err() == nil {
		batch := s.classifyBatch(uuid.Nil)
		n, err := s.queries.ClassifyGlobalObjectChanges(ctx, db.ClassifyGlobalObjectChangesParams{
			Rules:           observedGlobalRulesJSON,
			LookbackSeconds: int32(observedLookback(s.objectObserveInterval).Seconds()),
			SettledBefore:   time.Now().Add(-statusChangeSettle),
			MaxRows:         batch,
		})
		s.settleBatch(uuid.Nil, batch, err != nil && (ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded)))
		if err != nil {
			s.logger.Warn("failed to classify global object changes", "error", err)
			break
		}
		if n < int64(batch) {
			break
		}
	}
	cancel()
	// A PBS's rows are classified here even with the observer off; with a
	// context of their own, as the classification may have spent its time.
	showCtx, showCancel := context.WithTimeout(parent, observeClassifyTimeout)
	defer showCancel()
	showUnclassified(showCtx, s.queries, s.logger)
}

// showUnclassified shows the changes no classification decided in a day and
// an hour: the classifier's own give-up is a day, so this finds only what it
// could not reach.
func showUnclassified(ctx context.Context, q interface {
	ShowUnclassifiedObservedChanges(ctx context.Context, before time.Time) (int64, error)
}, logger *slog.Logger) {
	if n, err := q.ShowUnclassifiedObservedChanges(ctx, time.Now().Add(-statusChangeGiveUp-time.Hour)); err != nil {
		logger.Warn("failed to show the observed changes nothing classified", "error", err)
	} else if n > 0 {
		logger.Info("observed changes nothing could classify are shown", "changes", n)
	}
}

// everyOnlineNodeListed reports whether a pass's task ingest listed every node
// the cluster reported online.
func everyOnlineNodeListed(nodes []proxmox.NodeListEntry, listed []string) bool {
	got := make(map[string]bool, len(listed))
	for _, n := range listed {
		got[n] = true
	}
	for _, n := range nodes {
		if n.Status == "online" && !got[n.Node] {
			return false
		}
	}
	return true
}
