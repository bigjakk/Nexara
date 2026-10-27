package collector

import (
	"context"
	"encoding/json"
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

// sameKey is the rule every kind starts from.
func sameKey(kind string) explainRule {
	return explainRule{Kind: kind, AuditType: kind, MatchKey: "id"}
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

// observedChangeRules is every rule, for the cluster-scoped kinds.
var observedChangeRules = []explainRule{
	// A VM's configuration: tasks that rewrite it, tasks that rewrite its
	// disks, and Nexara's own untasked edits.
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

	// A pool's members move when a guest is created into it, cloned into it,
	// restored, destroyed, or moved from a guest's page.
	sameKey("pool"),
	{Kind: "pool", Change: "changed", MatchKey: "cluster",
		WorkerRe: `^(qm|vz)(create|restore|clone|destroy)$`, FieldRe: `^vms$`},
	{Kind: "pool", Change: "changed", AuditType: "vm", MatchKey: "details:old_pool",
		ActionRe: `^set_pool$`, FieldRe: `^vms$`},
	{Kind: "pool", Change: "changed", AuditType: "vm", MatchKey: "details:new_pool",
		ActionRe: `^set_pool$`, FieldRe: `^vms$`},
	// A storage removed leaves every pool it was in (AccessControl.pm
	// remove_storage_access).
	{Kind: "pool", Change: "changed", AuditType: "storage", MatchKey: "cluster",
		ActionRe: `^(delete|deleted)$`, FieldRe: `^storage$`},
	{Kind: "pool", Change: "changed", MatchKey: "cluster",
		WorkerRe: `^ceph(destroypool|destroyfs)$`, FieldRe: `^storage$`},

	// A storage is added by the disk and Ceph tasks that create one with
	// add_storage, and removed by the ones that clean it up.
	sameKey("storage"),
	{Kind: "storage", Change: "added", MatchKey: "cluster",
		WorkerRe: anyOf("dircreate", "lvmcreate", "lvmthincreate", "zfscreate", "cephfscreate", "cephcreatepool")},
	{Kind: "storage", Change: "removed", MatchKey: "cluster",
		WorkerRe: anyOf("dirremove", "lvmremove", "lvmthinremove", "zfsremove", "cephdestroyfs", "cephdestroypool")},

	sameKey("backup"),
	{Kind: "backup", Change: "changed", MatchKey: "cluster",
		WorkerRe: `^(qmdestroy|vzdestroy)$`, FieldRe: `^vmid$`},

	sameKey("usb_mapping"),
	sameKey("pci_mapping"),
	sameKey("metric_server"),
	sameKey("cluster_options"),
	sameKey("acme_config"),

	// A node's interfaces: their own rows, and a revert of the pending
	// changes, which is filed under the node.
	sameKey("network"),
	{Kind: "network", AuditType: "network", MatchKey: "node", ActionRe: `^network_reverted$`},

	// A node's own objects all file as node / <node>, beside everything else
	// done to the node, so each action explains only its own fields.
	{Kind: "node", Change: "changed", AuditType: "node", MatchKey: "id",
		ActionRe: `^set_dns$`, FieldRe: `^(search|dns1|dns2|dns3)$`},
	{Kind: "node", Change: "changed", AuditType: "node", MatchKey: "id",
		ActionRe: `^set_timezone$`, FieldRe: `^timezone$`},
	{Kind: "node", Change: "changed", AuditType: "node", MatchKey: "id",
		ActionRe: `^set_options$`, FieldRe: `^(description|wakeonlan|startall-onboot-delay|ballooning-target)$`},
	{Kind: "node", Change: "changed", AuditType: "node", MatchKey: "id",
		ActionRe: `^(create|update|delete)_firewall_rule$`, FieldRe: `^firewall_rules$`},
}

var observedChangeRulesJSON = mustRulesJSON(observedChangeRules)

func mustRulesJSON(rules []explainRule) json.RawMessage {
	b, err := json.Marshal(rules)
	if err != nil {
		panic(err)
	}
	return b
}

// classifyObservedChanges decides the observer's settled changes on a cluster
// (ClassifyObservedChanges). It runs on every slow pass, after the task ingest,
// so a task that explains a change made in the Proxmox GUI is on record first;
// a change waits for a pass whose ingest listed every online node, the day at
// most. A failure is only logged: the changes stay unclassified, and hidden.
func (s *Syncer) classifyObservedChanges(ctx context.Context, cluster db.Cluster, allNodesListed bool) {
	if _, err := s.queries.ClassifyObservedChanges(ctx, db.ClassifyObservedChangesParams{
		Rules:          observedChangeRulesJSON,
		ClusterID:      cluster.ID,
		SettledBefore:  time.Now().Add(-statusChangeSettle),
		AllNodesListed: allNodesListed,
		GiveUpBefore:   time.Now().Add(-statusChangeGiveUp),
	}); err != nil {
		s.logger.Warn("failed to classify observed object changes", "cluster_id", cluster.ID, "error", err)
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
