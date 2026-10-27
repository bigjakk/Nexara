package collector

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

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
	// Object and MemberOf name what a row is about, "<source>:<format>": the
	// UPID's id (upid), the row's resource_id (audit) or guest (vmid), or a
	// details key (details.<k>), put into format at %s. With Object, the
	// change's resource_id must be that name; with Member, the row explains
	// only that member joining or leaving a list (joined, left), or an object
	// removed with it as its last member (sole). Member with no MemberOf is any
	// member going that way.
	Object   string `json:"object"`
	Member   string `json:"member"`
	MemberOf string `json:"member_of"`
	// Counts says what a row does to a list's items, for a change that says
	// how many came and went: add1 one in, del1 one out, edit1 one in and one
	// out or one moved, adds any number in, dels any number out; "" anything.
	Counts string `json:"counts"`
	// FieldsFrom keeps a row to the fields its details name: config, a guest
	// config edit's keys and the keys it deleted or reverted (and the
	// unused<n> and boot a disk's removal moves); settings, the datacenter
	// options' settings and cleared.
	FieldsFrom string `json:"fields_from"`
}

// byAction is the rules a kind's own audit rows give: an audit row for the same
// object explains a change only by an action of the change's direction — an
// object created by a create, changed by an update, removed by a delete — so
// nothing done to an object in Nexara hides a different change made to it in
// Proxmox. A direction with no action has no rule.
func byAction(kind string, added, changed, removed []string) []explainRule {
	var rules []explainRule
	for _, d := range []struct {
		change  string
		actions []string
	}{{"added", added}, {"changed", changed}, {"removed", removed}} {
		if len(d.actions) > 0 {
			rules = append(rules, explainRule{Kind: kind, Change: d.change, AuditType: kind, MatchKey: "id",
				ActionRe: anyOf(d.actions...)})
		}
	}
	return rules
}

// crud is byAction for the kinds whose handlers file created, updated and
// deleted.
func crud(kind string) []explainRule {
	return byAction(kind, []string{"created"}, []string{"updated"}, []string{"deleted"})
}

// sdnCRUD is crud for an SDN kind, whose actions carry the kind's name.
func sdnCRUD(kind string) []explainRule {
	return byAction(kind, []string{kind + "_created"}, []string{kind + "_updated"}, []string{kind + "_deleted"})
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
	crud("pool"),
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
	byAction("storage", []string{"create", "create_import_source"}, []string{"update"},
		[]string{"delete", "delete_import_source"}),
	[]explainRule{
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
	// leaving, and the removal of a job it was the only guest of.
	byAction("backup", []string{"backup_job_created"}, []string{"backup_job_updated"}, []string{"backup_job_deleted"}),
	[]explainRule{
		{Kind: "backup", Change: "changed", MatchKey: "cluster", WorkerRe: `^(qmdestroy|vzdestroy)$`,
			FieldRe: `^vmid$`, Member: "left", MemberOf: "upid:vmid:%s"},
		{Kind: "backup", Change: "changed", MatchKey: "cluster", WorkerRe: `^(qmdestroy|vzdestroy)$`,
			FieldRe: `^exclude$`, Member: "left", MemberOf: "upid:exclude:%s"},
		{Kind: "backup", Change: "removed", MatchKey: "cluster", WorkerRe: `^(qmdestroy|vzdestroy)$`,
			Member: "sole", MemberOf: "upid:vmid:%s"},
	},

	// A mapping's delete that found it already gone is a no-op, which explains
	// no removal.
	crud("usb_mapping"),
	crud("pci_mapping"),
	crud("metric_server"),
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
	byAction("acme_config", nil, []string{"updated"}, nil),

	// A node's interfaces: their own rows, and a revert of the pending
	// changes, which is filed under the node and undoes any of them, in any
	// direction.
	byAction("network", []string{"interface_created"}, []string{"interface_updated"}, []string{"interface_deleted"}),
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
	// A node's firewall rules, filed as firewall_rule / node/<node>, are
	// edited through Nexara under the node (node_firewall.go), so its rows are
	// found by the change's node.
	[]explainRule{
		{Kind: "firewall_rule", Change: "changed", AuditType: "node", MatchKey: "node",
			ActionRe: `^create_firewall_rule$`, FieldRe: `^rules$`, Counts: "add1"},
		{Kind: "firewall_rule", Change: "changed", AuditType: "node", MatchKey: "node",
			ActionRe: `^update_firewall_rule$`, FieldRe: `^rules$`, Counts: "edit1"},
		{Kind: "firewall_rule", Change: "changed", AuditType: "node", MatchKey: "node",
			ActionRe: `^delete_firewall_rule$`, FieldRe: `^rules$`, Counts: "del1"},
	},

	// HA: a start or stop of an HA guest sets its resource's requested state,
	// and a guest destroyed with purge leaves the HA configuration — its
	// resource, and its place in every rule (HA/Config.pm
	// delete_service_from_config). The rolling update disables and restores
	// rules. A rule's delete that found it gone explains nothing.
	crud("ha_resource"),
	crud("ha_group"),
	crud("ha_rule"),
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

	sdnCRUD("sdn_zone"),
	sdnCRUD("sdn_vnet"),
	sdnCRUD("sdn_subnet"),
	sdnCRUD("sdn_controller"),
	sdnCRUD("sdn_ipam"),
	sdnCRUD("sdn_dns"),

	// The cluster firewall. Its options and rule sets always exist, so they
	// only change; a template applied adds to the rules. An alias renamed
	// through Nexara is filed under its old name with the new one in
	// details.rename: the old name's removal and the new one's addition are
	// both that update, and an update that renamed nothing removes nothing.
	byAction("firewall_options", nil, []string{"firewall_options_updated"}, nil),
	ruleSetRules("firewall_rule", "firewall_rule", "^rules$",
		"firewall_rule_created", "firewall_rule_updated", "firewall_rule_deleted"),
	[]explainRule{
		{Kind: "firewall_rule", Change: "changed", AuditType: "firewall_rule", MatchKey: "id",
			ActionRe: `^template_applied$`, FieldRe: `^rules$`, Counts: "adds"},
	},
	byAction("firewall_alias", []string{"created"}, []string{"updated"}, []string{"deleted"}),
	[]explainRule{
		{Kind: "firewall_alias", Change: "added", AuditType: "firewall_alias", MatchKey: "details:rename", ActionRe: `^updated$`},
		{Kind: "firewall_alias", Change: "removed", AuditType: "firewall_alias", MatchKey: "id", ActionRe: `^updated$`,
			DetailKey: "rename"},
	},
	byAction("firewall_ipset", []string{"created"}, nil, []string{"deleted"}),
	byAction("firewall_ipset_entry", []string{"created"}, nil, []string{"deleted"}),
	byAction("firewall_security_group", []string{"created"}, nil, []string{"deleted"}),
	ruleSetRules("firewall_security_group_rule", "firewall_security_group_rule", "^rules$",
		"rule_created", "rule_updated", "rule_deleted"),

	// Access. A user's own row explains the fields it says it set (enable,
	// expire, groups_changed: accessUserUpdateDetails) and the ones it never
	// records (names, e-mail, comment, keys); a token's likewise. A group's
	// update takes only its comment (pve-access-control API2/Group.pm). A user
	// joins or leaves a group by a user edit that set its groups, and leaves
	// with its own delete or the group's.
	byAction("pve_user", []string{"created"}, nil, []string{"deleted"}),
	byAction("pve_token", []string{"created"}, nil, []string{"deleted"}),
	byAction("pve_group", []string{"created"}, nil, []string{"deleted"}),
	crud("pve_role"),
	[]explainRule{
		{Kind: "pve_token", Change: "changed", AuditType: "pve_token", MatchKey: "id",
			ActionRe: anyOf("updated", "regenerated"), FieldRe: `^comment$`},
		{Kind: "pve_token", Change: "changed", AuditType: "pve_token", MatchKey: "id",
			ActionRe: anyOf("updated", "regenerated"), DetailKey: "expire", FieldRe: `^expire$`},
		{Kind: "pve_token", Change: "changed", AuditType: "pve_token", MatchKey: "id",
			ActionRe: anyOf("updated", "regenerated"), DetailKey: "privsep", FieldRe: `^privsep$`},
		{Kind: "pve_group", Change: "changed", AuditType: "pve_group", MatchKey: "id",
			ActionRe: `^updated$`, FieldRe: `^comment$`},
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

	// The ACL. Nexara's grant and revoke on a path explain only the kind of
	// grantee the row names (its users, groups or tokens): a grant brings
	// grantees in and changes their grants any way (a propagate flag is one
	// grant out and one in), a revoke only takes out. A path is added by its
	// first grant and removed with its last.
	byAction("pve_acl", []string{"granted"}, nil, []string{"revoked"}),
	aclGrantRules,
	// Anything deleted takes its ACL entries with it (AccessControl.pm
	// delete_user_acl, delete_group_acl and the like): only that grantee
	// leaving, and grants only going, never one arriving — a deletion never
	// hides a grant made meanwhile. A path goes with the grantee only if it
	// held no one else. A role's delete names no grantee, so any may leave.
	aclDeleteRules("pve_user", "users", "user_grants"),
	aclDeleteRules("pve_group", "groups", "group_grants"),
	aclDeleteRules("pve_token", "tokens", "token_grants"),
	[]explainRule{
		{Kind: "pve_acl", Change: "changed", AuditType: "pve_role", MatchKey: "cluster", ActionRe: `^deleted$`,
			FieldRe: `^(users|groups|tokens)$`, Member: "left"},
		{Kind: "pve_acl", Change: "changed", AuditType: "pve_role", MatchKey: "cluster", ActionRe: `^deleted$`,
			FieldRe: `^(user|group|token)_grants$`, Counts: "dels"},
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
			Object: "audit:/pool/%s"},
	},
	realmSyncRules,

	// ACME. An account registered or deactivated in the Proxmox GUI is a task
	// whose UPID names no account.
	byAction("acme_account", []string{"created"}, nil, []string{"deleted"}),
	[]explainRule{
		{Kind: "acme_account", Change: "added", MatchKey: "cluster", WorkerRe: `^acmeregister$`},
		{Kind: "acme_account", Change: "removed", MatchKey: "cluster", WorkerRe: `^acmedeactivate$`},
	},
	crud("acme_plugin"),
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
// it set, and those it never records, which any update may have set.
var accessUserUpdates = []explainRule{
	{Kind: "pve_user", Change: "changed", AuditType: "pve_user", MatchKey: "id", ActionRe: `^updated$`,
		FieldRe: `^(firstname|lastname|email|comment|keys)$`},
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

// aclGrantRules is Nexara's own grant and revoke on a path, for the grantees
// its row names: a grant brings them in and changes their grants any way, a
// revoke only takes out.
var aclGrantRules = func() []explainRule {
	rules := make([]explainRule, 0, 3*len(aclGrantees))
	for _, g := range aclGrantees {
		who := "details-list." + g.key + ":" + g.who + ":%s|" + g.grants + ":*:%s"
		fields := "^(" + g.who + "|" + g.grants + ")$"
		rules = append(rules,
			explainRule{Kind: "pve_acl", Change: "changed", AuditType: "pve_acl", MatchKey: "id", ActionRe: `^granted$`,
				FieldRe: fields, Member: "joined", MemberOf: who},
			explainRule{Kind: "pve_acl", Change: "changed", AuditType: "pve_acl", MatchKey: "id", ActionRe: `^granted$`,
				FieldRe: "^" + g.grants + "$", Member: "left", MemberOf: who},
			explainRule{Kind: "pve_acl", Change: "changed", AuditType: "pve_acl", MatchKey: "id", ActionRe: `^revoked$`,
				FieldRe: fields, Member: "left", MemberOf: who},
		)
	}
	return rules
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
// worker auth-realm-sync and the scheduled job's, both naming the realm): it
// creates, edits and removes that realm's users (user@realm) and the groups it
// syncs (name-realm, as Domains.pm names them), and with remove-vanished takes
// their ACL entries out. A dry run (auth-realm-sync-test) writes nothing.
var realmSyncRules = slices.Concat(
	inDirections(explainRule{Kind: "pve_user", MatchKey: "cluster", WorkerRe: `^auth-realm-sync$`,
		Object: "upid:*@%s"}, "added", "changed", "removed"),
	inDirections(explainRule{Kind: "pve_group", MatchKey: "cluster", WorkerRe: `^auth-realm-sync$`,
		Object: "upid:*-%s"}, "added", "changed", "removed"),
	[]explainRule{
		{Kind: "pve_acl", Change: "changed", MatchKey: "cluster", WorkerRe: `^auth-realm-sync$`,
			FieldRe: `^(users|user_grants|groups|group_grants)$`, Member: "left",
			MemberOf: "upid:users:*@%s|user_grants:*@%s|groups:*-%s|group_grants:*-%s"},
		{Kind: "pve_acl", Change: "removed", MatchKey: "cluster", WorkerRe: `^auth-realm-sync$`,
			Member: "sole", MemberOf: "upid:users:*@%s|user_grants:*@%s|groups:*-%s|group_grants:*-%s"},
	},
)

// observedActionsExplainingNothing are the actions handlers file under an
// observed kind that change none of what the observer compares: a run, a
// download, a read. TestObservedActionVocabulary holds every literal action to
// a rule or to this list.
var observedActionsExplainingNothing = map[string][]string{
	"storage": {"delete_content", "download", "download_appliance", "download_url", "image_copy", "pull_oci",
		"upload", "virtio_win_config_update", "virtio_win_download", "prune_backups"},
	"backup": {"backup", "backup_job_run", "backup_job_run_requested", "backup_restored", "backup_triggered",
		"datastore_pruned", "gc_triggered", "snapshot_deleted", "snapshot_notes_updated", "snapshot_protected",
		"snapshot_unprotected", "sync_job_triggered", "verify_job_triggered"},
	"network": {"network_applied"},
	"node": {"acme_register", "apt_update", "create_directory", "create_lvm", "create_lvmthin", "create_zfs_pool",
		"delete_lvm", "delete_lvmthin", "delete_zfs_pool", "download_report", "evacuate", "initialize_gpt",
		"node_maintenance_enter", "node_maintenance_exit", "read_memory_modules", "reboot", "service_reload",
		"shutdown", "start_all", "stop_all", "wipe_disk"},
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
	if _, err := s.queries.ClassifyObservedChanges(ctx, db.ClassifyObservedChangesParams{
		Rules:           observedChangeRulesJSON,
		LookbackSeconds: int32(observedLookback(s.objectObserveInterval).Seconds()),
		ClusterID:       cluster.ID,
		SettledBefore:   time.Now().Add(-statusChangeSettle),
		AllNodesListed:  allNodesListed,
		GiveUpBefore:    time.Now().Add(-statusChangeGiveUp),
	}); err != nil {
		s.logger.Warn("failed to classify observed object changes", "cluster_id", cluster.ID, "error", err)
	}
}

// classifyGlobalObjectChanges decides the settled changes of the global kinds
// (ClassifyGlobalObjectChanges), after each PBS pass.
func (s *Syncer) classifyGlobalObjectChanges(ctx context.Context) {
	if _, err := s.queries.ClassifyGlobalObjectChanges(ctx, db.ClassifyGlobalObjectChangesParams{
		Rules:           observedGlobalRulesJSON,
		LookbackSeconds: int32(observedLookback(s.objectObserveInterval).Seconds()),
		SettledBefore:   time.Now().Add(-statusChangeSettle),
	}); err != nil {
		s.logger.Warn("failed to classify global object changes", "error", err)
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
