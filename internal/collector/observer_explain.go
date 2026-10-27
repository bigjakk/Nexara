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
	Change    string `json:"change"`     // added, removed or changed; "" any
	AuditType string `json:"audit_type"` // audit_log.resource_type; "" any (vmid, sid and cluster keys only)
	MatchKey  string `json:"match_key"`  // id, node, vmid, sid, cluster or details:<key>
	Untasked  bool   `json:"untasked"`   // only an audit row with no task
	ActionRe  string `json:"action_re"`  // the row's action; "" any
	ExceptRe  string `json:"except_re"`  // an action that never explains; "" none
	WorkerRe  string `json:"worker_re"`  // the row's UPID worker type; "" any
	FieldRe   string `json:"field_re"`   // the fields it explains; "" any
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

const (
	qemuDiskFields = `^((ide|sata|scsi|virtio)[0-9]+|efidisk0|tpmstate0|unused[0-9]+)$`
	lxcDiskFields  = `^(rootfs|mp[0-9]+|unused[0-9]+)$`
)

// guestConfigActions are Nexara's own edits of a guest's configuration that run
// no task, so no worker type says what they are. Every action Nexara files for
// a guest with no task is either here or in guestOtherActions, which
// TestObservedGuestActionVocabulary holds to the handlers.
var guestConfigActions = []string{
	"config_update", "disk_attach", "disk_detach", "disk_resize", "media_mount", "media_eject",
	"guest_tools_stage", "guest_tools_update", "guest_tools_cancel", "guest_tools_auto_cancel",
}

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
	// disks, and Nexara's own untasked edits.
	[]explainRule{
		{Kind: "vm", Change: "changed", MatchKey: "vmid",
			WorkerRe: anyOf("qmconfig", "qmrollback", "qmtemplate", "qmcreate", "qmrestore", "qmdestroy")},
		{Kind: "vm", Change: "changed", MatchKey: "vmid",
			WorkerRe: anyOf("qmmove", "resize", "qmigrate"), FieldRe: qemuDiskFields},
		{Kind: "vm", Change: "changed", AuditType: "vm", MatchKey: "vmid", Untasked: true,
			ActionRe: anyOf(guestConfigActions...)},
		{Kind: "container", Change: "changed", MatchKey: "vmid",
			WorkerRe: anyOf("vzrollback", "vztemplate", "vzcreate", "vzrestore", "vzdestroy")},
		{Kind: "container", Change: "changed", MatchKey: "vmid",
			WorkerRe: anyOf("move_volume", "resize", "vzmigrate"), FieldRe: lxcDiskFields},
		{Kind: "container", Change: "changed", AuditType: "container", MatchKey: "vmid", Untasked: true,
			ActionRe: anyOf(guestConfigActions...)},
	},

	// A pool's members move when a guest is created into it, cloned into it,
	// restored, destroyed, or moved from a guest's page; a storage removed
	// leaves every pool it was in (AccessControl.pm remove_storage_access).
	crud("pool"),
	[]explainRule{
		{Kind: "pool", Change: "changed", MatchKey: "cluster",
			WorkerRe: `^(qm|vz)(create|restore|clone|destroy)$`, FieldRe: `^vms$`},
		{Kind: "pool", Change: "changed", AuditType: "vm", MatchKey: "details:old_pool",
			ActionRe: `^set_pool$`, FieldRe: `^vms$`},
		{Kind: "pool", Change: "changed", AuditType: "vm", MatchKey: "details:new_pool",
			ActionRe: `^set_pool$`, FieldRe: `^vms$`},
		{Kind: "pool", Change: "changed", AuditType: "storage", MatchKey: "cluster",
			ActionRe: `^(delete|delete_import_source)$`, FieldRe: `^storage$`},
		{Kind: "pool", Change: "changed", MatchKey: "cluster",
			WorkerRe: `^ceph(destroypool|destroyfs)$`, FieldRe: `^storage$`},
	},

	// A storage, created, edited and removed through Nexara — an ESXi import
	// source is one, and enabling import content edits one — and by the disk
	// and Ceph tasks that add or clean one up.
	byAction("storage", []string{"create", "create_import_source"}, []string{"update", "enable_import_content"},
		[]string{"delete", "delete_import_source"}),
	[]explainRule{
		{Kind: "storage", Change: "added", MatchKey: "cluster",
			WorkerRe: anyOf("dircreate", "lvmcreate", "lvmthincreate", "zfscreate", "cephfscreate", "cephcreatepool")},
		{Kind: "storage", Change: "removed", MatchKey: "cluster",
			WorkerRe: anyOf("dirremove", "lvmremove", "lvmthinremove", "zfsremove", "cephdestroyfs", "cephdestroypool")},
	},

	// A backup job; a guest destroyed with purge leaves every job it was in
	// (VZDump::Plugin remove_vmid_from_backup_jobs).
	byAction("backup", []string{"backup_job_created"}, []string{"backup_job_updated"}, []string{"backup_job_deleted"}),
	[]explainRule{
		{Kind: "backup", Change: "changed", MatchKey: "cluster", WorkerRe: `^(qmdestroy|vzdestroy)$`, FieldRe: `^vmid$`},
	},

	// A mapping's delete that found it already gone is a no-op, which explains
	// no removal.
	crud("usb_mapping"),
	crud("pci_mapping"),
	crud("metric_server"),
	// The datacenter options, which the rolling update also edits (it pauses
	// and restores the CRS option).
	byAction("cluster_options", nil, []string{"updated", "description_updated", "tags_updated"}, nil),
	byAction("acme_config", nil, []string{"updated"}, nil),

	// A node's interfaces: their own rows, and a revert of the pending
	// changes, which is filed under the node and undoes any of them.
	byAction("network", []string{"interface_created"}, []string{"interface_updated"}, []string{"interface_deleted"}),
	[]explainRule{
		{Kind: "network", AuditType: "network", MatchKey: "node", ActionRe: `^network_reverted$`},
	},

	// A node's own objects all file as node / <node>, beside everything else
	// done to the node, so each action explains only its own fields.
	[]explainRule{
		{Kind: "node", Change: "changed", AuditType: "node", MatchKey: "id",
			ActionRe: `^set_dns$`, FieldRe: `^(search|dns1|dns2|dns3)$`},
		{Kind: "node", Change: "changed", AuditType: "node", MatchKey: "id",
			ActionRe: `^set_timezone$`, FieldRe: `^timezone$`},
		{Kind: "node", Change: "changed", AuditType: "node", MatchKey: "id",
			ActionRe: `^set_options$`, FieldRe: `^(description|wakeonlan|startall-onboot-delay|ballooning-target)$`},
		{Kind: "node", Change: "changed", AuditType: "node", MatchKey: "id",
			ActionRe: `^(create|update|delete)_firewall_rule$`, FieldRe: `^firewall_rules$`},
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
		{Kind: "ha_rule", MatchKey: "cluster", WorkerRe: `^(qmdestroy|vzdestroy)$`, FieldRe: `^resources$`},
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
	// both that update.
	byAction("firewall_options", nil, []string{"firewall_options_updated"}, nil),
	byAction("firewall_rule", nil,
		[]string{"firewall_rule_created", "firewall_rule_updated", "firewall_rule_deleted", "template_applied"}, nil),
	byAction("firewall_alias", []string{"created"}, []string{"updated"}, []string{"deleted", "updated"}),
	[]explainRule{
		{Kind: "firewall_alias", Change: "added", AuditType: "firewall_alias", MatchKey: "details:rename", ActionRe: `^updated$`},
	},
	byAction("firewall_ipset", []string{"created"}, nil, []string{"deleted"}),
	byAction("firewall_ipset_entry", []string{"created"}, nil, []string{"deleted"}),
	byAction("firewall_security_group", []string{"created"}, nil, []string{"deleted"}),
	byAction("firewall_security_group_rule", nil, []string{"rule_created", "rule_updated", "rule_deleted"}, nil),

	// Access: a user's groups move when a group's members are edited, and a
	// group's members when a user's groups are; a user deleted takes its
	// tokens, and anything deleted takes its ACL entries with it
	// (AccessControl.pm delete_user_acl and the like), as a guest or a storage
	// removed does (remove_vm_access, remove_storage_access). An ACL path is
	// added by its first grant and removed with its last.
	crud("pve_user"),
	byAction("pve_token", []string{"created"}, []string{"updated", "regenerated"}, []string{"deleted"}),
	crud("pve_group"),
	crud("pve_role"),
	byAction("pve_acl", []string{"granted"}, []string{"granted", "revoked"}, []string{"revoked"}),
	[]explainRule{
		{Kind: "pve_user", Change: "changed", AuditType: "pve_group", MatchKey: "cluster",
			ActionRe: anyOf("created", "updated", "deleted"), FieldRe: `^groups$`},
		{Kind: "pve_group", Change: "changed", AuditType: "pve_user", MatchKey: "cluster",
			ActionRe: anyOf("created", "updated", "deleted"), FieldRe: `^users$`},
		{Kind: "pve_token", Change: "removed", AuditType: "pve_user", MatchKey: "cluster", ActionRe: `^deleted$`},
		{Kind: "pve_acl", MatchKey: "cluster", WorkerRe: `^(qmdestroy|vzdestroy)$`},
		{Kind: "pve_acl", AuditType: "pve_user", MatchKey: "cluster", ActionRe: `^deleted$`},
		{Kind: "pve_acl", AuditType: "pve_group", MatchKey: "cluster", ActionRe: `^deleted$`},
		{Kind: "pve_acl", AuditType: "pve_token", MatchKey: "cluster", ActionRe: `^deleted$`},
		{Kind: "pve_acl", AuditType: "pve_role", MatchKey: "cluster", ActionRe: `^deleted$`},
		{Kind: "pve_acl", AuditType: "storage", MatchKey: "cluster", ActionRe: `^(delete|delete_import_source)$`},
	},

	// ACME. An account registered or deactivated in the Proxmox GUI is a task
	// whose UPID names no account.
	byAction("acme_account", []string{"created"}, nil, []string{"deleted"}),
	[]explainRule{
		{Kind: "acme_account", Change: "added", MatchKey: "cluster", WorkerRe: `^acmeregister$`},
		{Kind: "acme_account", Change: "removed", MatchKey: "cluster", WorkerRe: `^acmedeactivate$`},
	},
	crud("acme_plugin"),
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
// Nexara started on the same server must not hide it.
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
