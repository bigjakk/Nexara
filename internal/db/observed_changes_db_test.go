package db

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixture ids, fixed so a run that dies before its purge leaves rows the next
// run's up-front purge deletes.
var (
	observedCluster = uuid.MustParse("128a0000-0000-4000-8000-00000000000a")
	observedOther   = uuid.MustParse("128a0000-0000-4000-8000-00000000000b")
)

// TestObservedChanges runs the SQL behind the observer (migrations/000128,
// queries/cluster_object_state.sql) against Postgres: a reading written in one
// statement, and the rules that decide whether something on record explains a
// change it filed.
//
// Not parallel: it writes rows the package's other DB tests share a schema with.
// Skipped unless NEXARA_TEST_DB_URL names a throwaway database.
func TestObservedChanges(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Audit rows first: an audit log may outlive its cluster.
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE cluster_id = ANY($1)`, []uuid.UUID{observedCluster, observedOther})
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{observedCluster, observedOther})
	}
	purge()
	defer purge()
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	for i, id := range []uuid.UUID{observedCluster, observedOther} {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://192.0.2.10:8006', 'tok', 'enc')`, id, "cluster0"+string(rune('1'+i)))
	}
	now := time.Now().UTC().Truncate(time.Second)
	ago := func(m float64) time.Time { return now.Add(-time.Duration(m * float64(time.Minute))) }
	jsonOf := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	// ── One reading, written whole: a change, two objects stored, the kind marked.
	record := func(arg gen.RecordObservedObjectsParams) {
		t.Helper()
		arg.ClusterID, arg.Kind = observedCluster, "storage"
		for _, s := range []*[]string{&arg.Removed, &arg.Touched} {
			if *s == nil {
				*s = []string{}
			}
		}
		if arg.Changes == nil {
			arg.Changes = json.RawMessage(`[]`)
		}
		if arg.States == nil {
			arg.States = json.RawMessage(`[]`)
		}
		if err := q.RecordObservedObjects(ctx, arg); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	state := func(id, tag string, seen time.Time) map[string]any {
		return map[string]any{"object_id": id, "fields": map[string]any{"nodes": tag}, "node": "",
			"signature": "sig1", "last_seen_at": seen}
	}
	record(gen.RecordObservedObjectsParams{
		Changes: jsonOf([]map[string]any{{"resource_type": "storage", "resource_id": "store01", "vmid": nil,
			"change": "changed", "fields": []string{"nodes"}, "detail": map[string]any{}, "node": "",
			"last_seen_at": ago(9), "observed_at": ago(4)}}),
		States: jsonOf([]map[string]any{state("store01", "t1", ago(5)), state("store02", "t2", ago(5))}),
		SeenAt: ago(5), Baseline: "sig1",
	})
	var fields []string
	var observedBy string
	var vmid pgtype.Int4
	if err := pool.QueryRow(ctx, `SELECT fields, observed_by, vmid FROM cluster_object_changes
		WHERE cluster_id = $1 AND resource_id = 'store01'`, observedCluster).Scan(&fields, &observedBy, &vmid); err != nil {
		t.Fatalf("read the change: %v", err)
	}
	if len(fields) != 1 || fields[0] != "nodes" || observedBy != "observer" || vmid.Valid {
		t.Fatalf("change filed as %v %s %v", fields, observedBy, vmid)
	}
	marks, err := q.ListClusterObjectBaselines(ctx, observedCluster)
	if err != nil || len(marks) != 1 || marks[0].Signature != "sig1" || !marks[0].LastReadAt.Equal(ago(5)) {
		t.Fatalf("marks %+v, %v", marks, err)
	}

	// The next reading touches one, drops the other, and marks nothing.
	record(gen.RecordObservedObjectsParams{Touched: []string{"store01"}, Removed: []string{"store02"}, SeenAt: ago(1)})
	rows, err := q.ListClusterObjectState(ctx, observedCluster)
	if err != nil || len(rows) != 1 || rows[0].ObjectID != "store01" || !rows[0].LastSeenAt.Equal(ago(1)) ||
		string(rows[0].Fields) != `{"nodes": "t1"}` {
		t.Fatalf("stored after the second reading: %+v, %v", rows, err)
	}

	// ── What explains a change: the rules the observer passes, applied here.
	exec("forget the readings' change", `DELETE FROM cluster_object_changes WHERE cluster_id = $1`, observedCluster)
	seed := func(cluster uuid.UUID, rtype, rid string, guest int32, change, node string, fields []string, observed time.Time) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		v := pgtype.Int4{Int32: guest, Valid: guest != 0}
		if fields == nil {
			fields = []string{}
		}
		if err := pool.QueryRow(ctx, `INSERT INTO cluster_object_changes
				(cluster_id, resource_type, resource_id, vmid, change, fields, node, observed_by, last_seen_at, observed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'observer', $8, $9) RETURNING id`,
			cluster, rtype, rid, v, change, fields, node, observed.Add(-5*time.Minute), observed).Scan(&id); err != nil {
			t.Fatalf("seed %s/%s: %v", rtype, rid, err)
		}
		return id
	}
	audit := func(cluster uuid.UUID, at time.Time, rtype, rid string, guest int32, action string, details map[string]any) {
		t.Helper()
		v := pgtype.Int4{Int32: guest, Valid: guest != 0}
		exec("audit", `INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, vmid, action, details, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, cluster, systemUserID, rtype, rid, v, action, jsonOf(details), at)
	}
	task := func(cluster uuid.UUID, upid string, guest int32, started, finished time.Time) {
		t.Helper()
		exec("task", `INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type, started_at, finished_at, source, vmid)
			VALUES ($1, $2, $3, 'stopped', 'pve-01', split_part($3, ':', 6), $4, $5, 'proxmox', $6)`,
			cluster, systemUserID, upid, started, finished, pgtype.Int4{Int32: guest, Valid: guest != 0})
	}
	upid := func(worker string, n int) string {
		return fmt.Sprintf("UPID:pve-01:%08X:00000001:00000001:%s:%d:root@pam:", n, worker, n)
	}

	at := ago(60)              // when each change below was seen
	in := at.Add(-time.Minute) // inside its window
	byOwnRow := seed(observedCluster, "storage", "store01", 0, "changed", "", []string{"nodes"}, at)
	audit(observedCluster, in, "storage", "store01", 0, "update", nil)
	byRunOnly := seed(observedCluster, "backup", "backup-01", 0, "changed", "", []string{"schedule"}, at)
	audit(observedCluster, in, "backup", "backup-01", 0, "run", nil)
	tooEarly := seed(observedCluster, "storage", "store02", 0, "changed", "", []string{"nodes"}, at)
	audit(observedCluster, at.Add(-9*time.Minute), "storage", "store02", 0, "update", nil)
	otherCluster := seed(observedCluster, "storage", "store03", 0, "changed", "", []string{"nodes"}, at)
	audit(observedOther, in, "storage", "store03", 0, "update", nil)

	// A guest: a disk move explains the disk, not the memory changed beside it,
	// unless a config task explains that too; Nexara's own untasked edit counts,
	// a long task counts while it runs, and an untasked row of another action
	// does not.
	diskOnly := seed(observedCluster, "vm", "100", 100, "changed", "pve-01", []string{"scsi0"}, at)
	audit(observedCluster, in, "proxmox_task", "100", 100, "qmmove", map[string]any{"upid": upid("qmmove", 1)})
	diskAndMemory := seed(observedCluster, "vm", "101", 101, "changed", "pve-01", []string{"memory", "scsi0"}, at)
	audit(observedCluster, in, "proxmox_task", "101", 101, "qmmove", map[string]any{"upid": upid("qmmove", 2)})
	bothCovered := seed(observedCluster, "vm", "102", 102, "changed", "pve-01", []string{"memory", "scsi0"}, at)
	audit(observedCluster, in, "proxmox_task", "102", 102, "qmmove", map[string]any{"upid": upid("qmmove", 3)})
	audit(observedCluster, in, "vm", "102", 102, "config_change", map[string]any{"upid": upid("qmconfig", 4)})
	byNexaraEdit := seed(observedCluster, "vm", "103", 103, "changed", "pve-01", []string{"memory"}, at)
	audit(observedCluster, in, "vm", uuid.NewString(), 103, "config_update",
		map[string]any{"fields": map[string]any{"memory": "4096"}})
	byOtherUntasked := seed(observedCluster, "vm", "104", 104, "changed", "pve-01", []string{"memory"}, at)
	audit(observedCluster, in, "vm", uuid.NewString(), 104, "drs_migrate", nil)
	byTaskedEdit := seed(observedCluster, "vm", "107", 107, "changed", "pve-01", []string{"memory"}, at)
	audit(observedCluster, in, "vm", uuid.NewString(), 107, "config_update", map[string]any{"upid": upid("resize", 10)})
	byLongTask := seed(observedCluster, "vm", "105", 105, "changed", "pve-01", []string{"memory"}, at)
	audit(observedCluster, at.Add(-3*time.Hour), "vm", "105", 105, "config_change", map[string]any{"upid": upid("qmconfig", 5)})
	task(observedCluster, upid("qmconfig", 5), 105, at.Add(-3*time.Hour), at.Add(-2*time.Minute))
	byFinishedTask := seed(observedCluster, "vm", "106", 106, "changed", "pve-01", []string{"memory"}, at)
	audit(observedCluster, at.Add(-3*time.Hour), "vm", "106", 106, "config_change", map[string]any{"upid": upid("qmconfig", 6)})
	task(observedCluster, upid("qmconfig", 6), 106, at.Add(-3*time.Hour), at.Add(-2*time.Hour))

	// The other match keys. A rule matching any row of the cluster would
	// explain every change of its kind in the window, so those cases are an
	// hour and more apart.
	bySid := seed(observedCluster, "ha_resource", "vm:110", 0, "changed", "", []string{"state"}, at)
	audit(observedCluster, in, "proxmox_task", "110", 110, "hastart", map[string]any{"upid": upid("hastart", 7)})
	besideSid := seed(observedCluster, "ha_resource", "vm:113", 0, "changed", "", []string{"state"}, at)
	members := func(id uuid.UUID, joined, left []string) {
		exec("members", `UPDATE cluster_object_changes SET members_joined = $2, members_left = $3 WHERE id = $1`,
			id, joined, left)
	}
	// A guest's destroy explains that guest leaving a pool, not another one
	// leaving, and a job's removal only when the guest was all it held.
	byClusterTask := seed(observedCluster, "pool", "web", 0, "changed", "", []string{"vms"}, at)
	members(byClusterTask, []string{}, []string{"vms:111"})
	audit(observedCluster, in, "vm", "111", 111, "destroy", map[string]any{"upid": "UPID:pve-01:00000008:00000001:00000001:qmdestroy:111:root@pam:"})
	besideTask := seed(observedCluster, "pool", "ops", 0, "changed", "", []string{"vms"}, at)
	members(besideTask, []string{}, []string{"vms:111", "vms:120"})
	jobEmptied := seed(observedCluster, "backup", "backup-02", 0, "removed", "", nil, at)
	members(jobEmptied, []string{}, []string{"vmid:111"})
	jobNotEmptied := seed(observedCluster, "backup", "backup-03", 0, "removed", "", nil, at)
	members(jobNotEmptied, []string{}, []string{"vmid:111", "vmid:121"})
	byResourceDelete := seed(observedCluster, "ha_rule", "apart", 0, "changed", "", []string{"resources"}, at)
	members(byResourceDelete, []string{}, []string{"resources:vm:160"})
	audit(observedCluster, in, "ha_resource", "vm:160", 160, "deleted", nil)

	// A config edit explains the keys it set or deleted (and the unused<n> a
	// disk's removal leaves), not another key; the options' edit its settings.
	configOther := seed(observedCluster, "vm", "180", 180, "changed", "pve-01", []string{"description", "hookscript"}, at)
	configUnused := seed(observedCluster, "vm", "181", 181, "changed", "pve-01", []string{"description", "unused0"}, at)
	configDeleted := seed(observedCluster, "vm", "182", 182, "changed", "pve-01", []string{"net1"}, at)
	for vmid, fields := range map[int32]map[string]any{180: {"description": "x"}, 181: {"description": "x"},
		182: {"delete": "net1"}} {
		audit(observedCluster, in, "vm", uuid.NewString(), vmid, "config_update", map[string]any{"fields": fields})
	}
	optionsSet := seed(observedCluster, "cluster_options", observedCluster.String(), 0, "changed", "", []string{"crs"}, at)
	optionsOther := seed(observedCluster, "cluster_options", observedCluster.String(), 0, "changed", "", []string{"migration"}, at)
	audit(observedCluster, in, "cluster_options", observedCluster.String(), 0, "updated",
		map[string]any{"settings": []string{"crs"}, "cleared": []string{}})
	// A rule set's own rows by count: an update one in and one out, a create
	// one in and never a rule moved.
	counted := func(id uuid.UUID, counts map[string]int) {
		exec("counts", `UPDATE cluster_object_changes SET detail = $2 WHERE id = $1`, id,
			jsonOf(map[string]any{"counts": map[string]any{"rules": counts}}))
	}
	ruleEdited := seed(observedCluster, "firewall_rule", "cluster", 0, "changed", "", []string{"rules"}, at)
	counted(ruleEdited, map[string]int{"added": 1, "removed": 1})
	ruleEditedAndAdded := seed(observedCluster, "firewall_rule", "cluster", 0, "changed", "", []string{"rules"}, at)
	counted(ruleEditedAndAdded, map[string]int{"added": 2, "removed": 1})
	audit(observedCluster, in, "firewall_rule", "cluster", 0, "firewall_rule_updated", nil)
	groupRuleAdded := seed(observedCluster, "firewall_security_group_rule", "web", 0, "changed", "", []string{"rules"}, at)
	counted(groupRuleAdded, map[string]int{"added": 1, "removed": 0})
	groupRuleMoved := seed(observedCluster, "firewall_security_group_rule", "web", 0, "changed", "", []string{"rules"}, at)
	counted(groupRuleMoved, map[string]int{"added": 1, "removed": 0, "moved": 1})
	audit(observedCluster, in, "firewall_security_group_rule", "web", 0, "rule_created", nil)

	// What Nexara filed with no id of the object explains only what it can:
	// a job created with its id unknown, a job added anywhere; a subnet filed
	// under its CIDR, the subnet with that CIDR.
	jobAddedUnknown := seed(observedCluster, "backup", "backup-11", 0, "added", "", nil, ago(250))
	audit(observedCluster, ago(251), "backup", "backup-job", 0, "backup_job_created", nil)
	jobAddedBeside := seed(observedCluster, "backup", "backup-13", 0, "added", "", nil, ago(350))
	audit(observedCluster, ago(351), "backup", "backup-12", 0, "backup_job_created", nil)
	subnetByCIDR := seed(observedCluster, "sdn_subnet", "vnet01/zone01-192.0.2.0-24", 0, "added", "", nil, at)
	members(subnetByCIDR, []string{"cidr:vnet01/192.0.2.0/24"}, []string{})
	subnetOther := seed(observedCluster, "sdn_subnet", "vnet01/zone01-198.51.100.0-24", 0, "added", "", nil, at)
	members(subnetOther, []string{"cidr:vnet01/198.51.100.0/24"}, []string{})
	audit(observedCluster, in, "sdn_subnet", "vnet01/192.0.2.0/24", 0, "sdn_subnet_created", nil)

	// Access: a user's delete explains its own grants going, never another's
	// arriving; Nexara's grant only the grantees its row names; a realm's sync
	// only that realm's users.
	aclByDelete := seed(observedCluster, "pve_acl", "/vms/170", 0, "changed", "", []string{"user_grants", "users"}, at)
	members(aclByDelete, []string{}, []string{"user_grants:T1:user01@pve", "users:user01@pve"})
	aclEscalated := seed(observedCluster, "pve_acl", "/vms/171", 0, "changed", "", []string{"user_grants", "users"}, at)
	members(aclEscalated, []string{"user_grants:T2:user09@pve"}, []string{"user_grants:T1:user01@pve", "users:user01@pve"})
	audit(observedCluster, in, "pve_user", "user01@pve", 0, "deleted", nil)
	aclByGrant := seed(observedCluster, "pve_acl", "/", 0, "changed", "", []string{"user_grants", "users"}, at)
	members(aclByGrant, []string{"user_grants:T3:user02@pve", "users:user02@pve"}, []string{})
	aclBesideGrant := seed(observedCluster, "pve_acl", "/", 0, "changed", "", []string{"user_grants"}, at)
	members(aclBesideGrant, []string{"user_grants:T4:user09@pve"}, []string{})
	audit(observedCluster, in, "pve_acl", "/", 0, "granted", map[string]any{"users": "user02@pve,user03@pve"})
	byRealmSync := seed(observedCluster, "pve_user", "user04@ldap01", 0, "changed", "", []string{"email"}, at)
	besideRealmSync := seed(observedCluster, "pve_user", "user05@pve", 0, "changed", "", []string{"email"}, at)
	audit(observedCluster, in, "proxmox_task", "ldap01", 0, "auth-realm-sync",
		map[string]any{"upid": "UPID:pve-01:0000000D:00000001:00000001:auth-realm-sync:ldap01:root@pam:"})
	// A task reaching another object counts from an hour before the window,
	// not the day a task on the object itself does.
	byLongRestore := seed(observedCluster, "pool", "db2", 0, "changed", "", []string{"vms"}, ago(600))
	members(byLongRestore, []string{"vms:140"}, []string{})
	audit(observedCluster, ago(600).Add(-90*time.Minute), "proxmox_task", "140", 140, "qmrestore",
		map[string]any{"upid": "UPID:pve-01:0000000C:00000001:00000001:qmrestore:140:root@pam:"})
	task(observedCluster, "UPID:pve-01:0000000C:00000001:00000001:qmrestore:140:root@pam:", 140,
		ago(600).Add(-90*time.Minute), ago(600).Add(-time.Minute))
	byDetails := seed(observedCluster, "pool", "db", 0, "changed", "", []string{"vms"}, ago(200))
	audit(observedCluster, ago(201), "vm", uuid.NewString(), 112, "set_pool", map[string]any{"new_pool": "db"})
	byClone := seed(observedCluster, "pool", "lab", 0, "changed", "", []string{"vms"}, ago(200))
	audit(observedCluster, ago(201), "vm", uuid.NewString(), 113, "clone", map[string]any{"new_id": 150})
	members(byDetails, []string{"vms:112"}, []string{})
	members(byClone, []string{"vms:150"}, []string{})
	besideDetails := seed(observedCluster, "pool", "app", 0, "changed", "", []string{"vms"}, ago(200))
	byNode := seed(observedCluster, "network", "pve-01/vmbr1", 0, "removed", "pve-01", nil, at)
	audit(observedCluster, in, "network", "pve-01", 0, "network_reverted", nil)
	besideNode := seed(observedCluster, "network", "pve-02/vmbr1", 0, "removed", "pve-02", nil, at)
	addedByTask := seed(observedCluster, "storage", "store04", 0, "added", "", nil, ago(300))
	audit(observedCluster, ago(301), "node", "pve-01", 0, "create_directory",
		map[string]any{"upid": "UPID:pve-01:00000009:00000001:00000001:dircreate:store04:root@pam:"})
	addedBesideTask := seed(observedCluster, "storage", "store11", 0, "added", "", nil, ago(300))
	addedUnexplained := seed(observedCluster, "storage", "store05", 0, "added", "", nil, ago(400))
	renamedAway := seed(observedCluster, "firewall_alias", "web", 0, "removed", "", nil, at)
	audit(observedCluster, in, "firewall_alias", "web", 0, "updated", map[string]any{"rename": "web2"})
	removedBesideUpdate := seed(observedCluster, "firewall_alias", "db", 0, "removed", "", nil, at)
	audit(observedCluster, in, "firewall_alias", "db", 0, "updated", map[string]any{"name": "db"})

	// The look-back: a change first seen after an outage opens its window an
	// hour before it was seen at the most, however long the gap.
	afterOutage := seed(observedCluster, "storage", "store09", 0, "changed", "", []string{"nodes"}, ago(500))
	exec("an outage's gap", `UPDATE cluster_object_changes SET last_seen_at = $2 WHERE id = $1`, afterOutage, ago(800))
	audit(observedCluster, ago(700), "storage", "store09", 0, "update", nil)
	afterOutageRecent := seed(observedCluster, "storage", "store10", 0, "changed", "", []string{"nodes"}, ago(500))
	exec("an outage's gap", `UPDATE cluster_object_changes SET last_seen_at = $2 WHERE id = $1`, afterOutageRecent, ago(800))
	audit(observedCluster, ago(530), "storage", "store10", 0, "update", nil)

	// Timing and scope.
	fresh := seed(observedCluster, "storage", "store06", 0, "changed", "", []string{"nodes"}, ago(1))
	nothingOnRecord := seed(observedCluster, "storage", "store07", 0, "changed", "", []string{"nodes"}, ago(90))
	gaveUp := seed(observedCluster, "storage", "store08", 0, "changed", "", []string{"nodes"}, now.Add(-26*time.Hour))
	notMine := seed(observedCluster, "replication", "100-0", 0, "changed", "", []string{"schedule"}, at)
	exec("a trigger's row", `UPDATE cluster_object_changes SET observed_by = 'trigger' WHERE id = $1`, notMine)

	rule := func(kind, change, auditType, key string, untasked bool, actionRe, exceptRe, workerRe, fieldRe string) map[string]any {
		return map[string]any{"kind": kind, "change": change, "audit_type": auditType, "match_key": key,
			"untasked": untasked, "action_re": actionRe, "except_re": exceptRe, "worker_re": workerRe, "field_re": fieldRe,
			"detail_key": "", "object": "", "member": "", "member_of": "", "counts": "", "fields_from": "",
			"audit_id_is": ""}
	}
	with := func(r map[string]any, key, value string) map[string]any { r[key] = value; return r }
	rules := jsonOf([]map[string]any{
		rule("storage", "", "storage", "id", false, "", "", "", ""),
		rule("backup", "", "backup", "id", false, "", "^run$", "", ""),
		with(rule("storage", "added", "", "cluster", false, "", "", "^dircreate$", ""), "object", "upid:%s"),
		with(rule("firewall_alias", "removed", "firewall_alias", "id", false, "^updated$", "", "", ""), "detail_key", "rename"),
		rule("vm", "changed", "", "vmid", false, "", "", "^qmmove$", "^scsi[0-9]+$"),
		rule("vm", "changed", "", "vmid", false, "", "", "^qmconfig$", ""),
		with(rule("vm", "changed", "vm", "vmid", true, "^config_update$", "", "", ""), "fields_from", "config"),
		with(rule("cluster_options", "changed", "cluster_options", "id", false, "^updated$", "", "", ""),
			"fields_from", "settings"),
		with(rule("firewall_rule", "changed", "firewall_rule", "id", false, "^firewall_rule_updated$", "", "", "^rules$"),
			"counts", "edit1"),
		with(rule("firewall_security_group_rule", "changed", "firewall_security_group_rule", "id", false,
			"^rule_created$", "", "", "^rules$"), "counts", "add1"),
		rule("ha_resource", "changed", "", "sid", false, "", "", "^hastart$", "^state$"),
		with(with(rule("pool", "changed", "", "cluster", false, "", "", "^qmdestroy$", "^vms$"), "member", "left"),
			"member_of", "upid:vms:%s"),
		with(with(rule("backup", "removed", "", "cluster", false, "", "", "^qmdestroy$", ""), "member", "sole"),
			"member_of", "upid:vmid:%s"),
		with(with(rule("pool", "changed", "", "cluster", false, "", "", "^qmrestore$", "^vms$"), "member", "joined"),
			"member_of", "upid:vms:%s"),
		with(with(rule("ha_rule", "changed", "ha_resource", "cluster", false, "^deleted$", "", "", "^resources$"),
			"member", "left"), "member_of", "audit:resources:%s"),
		with(with(rule("pve_acl", "changed", "pve_user", "cluster", false, "^deleted$", "", "", "^(users|user_grants)$"),
			"member", "left"), "member_of", "audit:users:%s|user_grants:*:%s"),
		with(with(rule("pve_acl", "changed", "pve_acl", "id", false, "^granted$", "", "", "^(users|user_grants)$"),
			"member", "joined"), "member_of", "details-list.users:users:%s|user_grants:*:%s"),
		with(rule("pve_user", "changed", "", "cluster", false, "", "", "^auth-realm-sync$", ""), "object", "upid:*@%s"),
		with(rule("backup", "added", "backup", "cluster", false, "^backup_job_created$", "", "", ""), "audit_id_is", "backup-job"),
		with(with(rule("sdn_subnet", "added", "sdn_subnet", "cluster", false, "^sdn_subnet_created$", "", "", ""),
			"member", "joined"), "member_of", "audit:cidr:%s"),
		with(with(rule("pool", "changed", "vm", "details:new_pool", false, "^set_pool$", "", "", "^vms$"), "member", "joined"),
			"member_of", "vmid:vms:%s"),
		with(with(rule("pool", "changed", "vm", "cluster", false, "^clone$", "", "", "^vms$"), "member", "joined"),
			"member_of", "details.new_id:vms:%s"),
		rule("network", "", "network", "node", false, "^network_reverted$", "", "", ""),
	})
	classify := func(allListed bool, maxRows int32) int64 {
		t.Helper()
		n, err := q.ClassifyObservedChanges(ctx, gen.ClassifyObservedChangesParams{
			Rules: rules, LookbackSeconds: 3600, ClusterID: observedCluster, SettledBefore: ago(5),
			AllNodesListed: allListed, GiveUpBefore: now.Add(-24 * time.Hour), MaxRows: maxRows,
		})
		if err != nil {
			t.Fatalf("classify: %v", err)
		}
		return n
	}
	explained := func(id uuid.UUID) string {
		t.Helper()
		var e pgtype.Bool
		if err := pool.QueryRow(ctx, `SELECT explained FROM cluster_object_changes WHERE id = $1`, id).Scan(&e); err != nil {
			t.Fatalf("read explained: %v", err)
		}
		switch {
		case !e.Valid:
			return "waiting"
		case e.Bool:
			return "explained"
		}
		return "shown"
	}

	// A node's task list unread: only what has waited past the give-up is
	// decided, at most max_rows a call.
	if n := classify(false, 1); n != 1 {
		t.Fatalf("one call decided %d, want its one", n)
	}
	classify(false, 1000)
	for name, id := range map[string]uuid.UUID{"recent, nodes unlisted": byOwnRow, "past the give-up": gaveUp} {
		want := map[bool]string{true: "shown", false: "waiting"}[id == gaveUp]
		if got := explained(id); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	classify(true, 1000)
	for _, tc := range []struct {
		name string
		id   uuid.UUID
		want string
	}{
		{"its own audit row in the window", byOwnRow, "explained"},
		{"its own row, of an action the rule excepts", byRunOnly, "shown"},
		{"its own row from before the window", tooEarly, "shown"},
		{"another cluster's row", otherCluster, "shown"},
		{"a disk move, the disk changed", diskOnly, "explained"},
		{"a disk move, the disk and the memory changed", diskAndMemory, "shown"},
		{"a disk move and a config task, both changed", bothCovered, "explained"},
		{"Nexara's own untasked edit", byNexaraEdit, "explained"},
		{"an untasked row of another action", byOtherUntasked, "shown"},
		{"an edit's row that ran a task the rule does not take", byTaskedEdit, "shown"},
		{"a task started long before, running into the window", byLongTask, "explained"},
		{"a task that ended long before", byFinishedTask, "shown"},
		{"an HA start of the sid's guest", bySid, "explained"},
		{"an HA start of another guest", besideSid, "shown"},
		{"a guest's destroy, that guest leaving the pool", byClusterTask, "explained"},
		{"a guest's destroy, another guest leaving too", besideTask, "shown"},
		{"a job removed with its last guest's destroy", jobEmptied, "explained"},
		{"a job removed that held another guest too", jobNotEmptied, "shown"},
		{"an HA resource deleted, its sid leaving a rule", byResourceDelete, "explained"},
		{"a config edit, another key changed too", configOther, "shown"},
		{"a config edit, the disk it left unused", configUnused, "explained"},
		{"a config edit that deleted the key", configDeleted, "explained"},
		{"an options edit of that setting", optionsSet, "explained"},
		{"an options edit, another setting", optionsOther, "shown"},
		{"a rule updated, one in and one out", ruleEdited, "explained"},
		{"a rule updated, and another added", ruleEditedAndAdded, "shown"},
		{"a rule created, one in", groupRuleAdded, "explained"},
		{"a rule created, and one moved", groupRuleMoved, "shown"},
		{"a job created with its id unknown", jobAddedUnknown, "explained"},
		{"a job added beside another's create", jobAddedBeside, "shown"},
		{"a subnet created, filed under its CIDR", subnetByCIDR, "explained"},
		{"a subnet added beside another CIDR's create", subnetOther, "shown"},
		{"a user's delete, its grants going", aclByDelete, "explained"},
		{"a user's delete, and another's grant arriving", aclEscalated, "shown"},
		{"Nexara's grant to the users its row names", aclByGrant, "explained"},
		{"Nexara's grant, and another user's arriving", aclBesideGrant, "shown"},
		{"a realm's sync, a user of that realm", byRealmSync, "explained"},
		{"a realm's sync, a user of another", besideRealmSync, "shown"},
		{"a restore started more than an hour before", byLongRestore, "shown"},
		{"a guest moved into the pool from its page", byDetails, "explained"},
		{"a guest cloned into the pool through Nexara", byClone, "explained"},
		{"a guest moved into another pool", besideDetails, "shown"},
		{"a revert of the node's pending network", byNode, "explained"},
		{"a revert on another node", besideNode, "shown"},
		{"added by a disk task", addedByTask, "explained"},
		{"added beside a disk task that named another", addedBesideTask, "shown"},
		{"renamed away by an update", renamedAway, "explained"},
		{"removed beside an update that renamed nothing", removedBesideUpdate, "shown"},
		{"added, nothing on record", addedUnexplained, "shown"},
		{"after an outage, by a row from the gap's start", afterOutage, "shown"},
		{"after an outage, by a row within the look-back", afterOutageRecent, "explained"},
		{"seen too recently", fresh, "waiting"},
		{"settled, nothing on record", nothingOnRecord, "shown"},
		{"another writer's row", notMine, "waiting"},
	} {
		if got := explained(tc.id); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}

	// What nothing could classify in a day and an hour is shown; another
	// writer's rows and what is merely recent are left alone.
	stuck := seed(observedCluster, "storage", "store12", 0, "changed", "", []string{"nodes"}, now.Add(-30*time.Hour))
	if _, err := q.ShowUnclassifiedObservedChanges(ctx, now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		id   uuid.UUID
		want string
	}{"stuck": {stuck, "shown"}, "recent": {fresh, "waiting"}, "another writer's": {notMine, "waiting"}} {
		if got := explained(tc.id); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// Fixture ids for the global kinds' test.
var (
	observedPBS     = uuid.MustParse("128a0000-0000-4000-8000-0000000000a1")
	observedPBSGone = uuid.MustParse("128a0000-0000-4000-8000-0000000000a2")
)

// TestObservedGlobalChanges runs migrations 000129 and 000130 against Postgres:
// only a global kind's row may name no cluster, the PBS job triggers record an
// edit, a job added after the server's jobs were read and a job removed — never
// a server's own removal, nor the first read of its jobs — and the global
// classification reads audit rows by type and id alone.
//
// Not parallel, for the reason TestObservedChanges gives.
func TestObservedGlobalChanges(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		servers := []string{observedPBS.String(), observedPBSGone.String()}
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE resource_type = 'pbs_server' AND resource_id = ANY($1)`, servers)
		_, _ = pool.Exec(pctx, `DELETE FROM pbs_servers WHERE id = ANY($1)`, []uuid.UUID{observedPBS, observedPBSGone})
		_, _ = pool.Exec(pctx, `DELETE FROM cluster_object_changes WHERE cluster_id IS NULL
			AND resource_id = ANY($1)`, servers)
	}
	purge()
	defer purge()
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	for _, id := range []uuid.UUID{observedPBS, observedPBSGone} {
		exec("pbs server", `INSERT INTO pbs_servers (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, 'pbs01', 'https://192.0.2.20:8007', 'tok', 'enc')`, id)
	}

	// Only a global kind may name no cluster.
	if _, err := pool.Exec(ctx, `INSERT INTO cluster_object_changes (resource_type, resource_id, change, observed_by, last_seen_at)
		VALUES ('storage', 'store01', 'changed', 'observer', now())`); err == nil {
		t.Fatal("a storage change with no cluster was accepted")
	}

	type row struct {
		Change string
		Fields []string
		Detail map[string]any
	}
	changes := func(server uuid.UUID) []row {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT change, fields, detail FROM cluster_object_changes
			WHERE cluster_id IS NULL AND resource_type = 'pbs_server' AND resource_id = $1 ORDER BY observed_at, change`,
			server.String())
		if err != nil {
			t.Fatalf("read changes: %v", err)
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			var detail []byte
			if err := rows.Scan(&r.Change, &r.Fields, &detail); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if err := json.Unmarshal(detail, &r.Detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}
	syncJob := func(server uuid.UUID, id, schedule, remoteStore, state string) {
		t.Helper()
		if _, err := q.UpsertPBSSyncJob(ctx, gen.UpsertPBSSyncJobParams{PbsServerID: server, JobID: id,
			Store: "datastore01", Remote: "remote01", RemoteStore: remoteStore, Schedule: schedule,
			LastRunState: state}); err != nil {
			t.Fatalf("upsert sync job %s: %v", id, err)
		}
	}

	syncJob(observedPBS, "s-01", "hourly", "datastore02", "OK")
	syncJob(observedPBS, "s-01", "hourly", "datastore02", "ERROR") // run state only
	if got := changes(observedPBS); len(got) != 0 {
		t.Fatalf("the first read of a server's jobs recorded %+v", got)
	}
	if err := q.MarkPBSJobBaseline(ctx, gen.MarkPBSJobBaselineParams{PbsServerID: observedPBS, JobType: "sync"}); err != nil {
		t.Fatal(err)
	}
	syncJob(observedPBS, "s-01", "daily", "datastore03", "OK")
	syncJob(observedPBS, "s-02", "daily", "datastore02", "")
	exec("prune", `DELETE FROM pbs_sync_jobs WHERE pbs_server_id = $1 AND job_id = 's-02'`, observedPBS)
	got := changes(observedPBS)
	if len(got) != 3 || got[0].Change != "changed" || fmt.Sprint(got[0].Fields) != "[remote-store schedule]" ||
		got[0].Detail["job"] != "s-01" || got[0].Detail["job_type"] != "sync" || got[0].Detail["datastore"] != "datastore01" ||
		got[1].Change != "added" || got[2].Change != "removed" {
		t.Fatalf("recorded %+v, want s-01 changed (remote-store, schedule), s-02 added and removed", got)
	}
	// A job moved to another datastore says which it left: both are its identity.
	exec("move", `UPDATE pbs_sync_jobs SET store = 'datastore04' WHERE pbs_server_id = $1 AND job_id = 's-01'`, observedPBS)
	if got := changes(observedPBS); len(got) != 4 || fmt.Sprint(got[3].Fields) != "[store]" ||
		got[3].Detail["datastore"] != "datastore04" || got[3].Detail["from_datastore"] != "datastore01" {
		t.Fatalf("a job moved: recorded %+v", got)
	}

	// A server removed takes its jobs with it, and that is no job's removal.
	if err := q.MarkPBSJobBaseline(ctx, gen.MarkPBSJobBaselineParams{PbsServerID: observedPBSGone, JobType: "sync"}); err != nil {
		t.Fatal(err)
	}
	syncJob(observedPBSGone, "s-09", "daily", "datastore02", "")
	exec("remove the server", `DELETE FROM pbs_servers WHERE id = $1`, observedPBSGone)
	if got := changes(observedPBSGone); len(got) != 1 || got[0].Change != "added" {
		t.Fatalf("a removed server's jobs recorded %+v, want only the add", got)
	}

	// The global classification: an audit row of the server, in any cluster or
	// none, explains a change of it, the fields its rule covers; with nothing on
	// record it shows.
	exec("settle", `UPDATE cluster_object_changes SET observed_at = now() - interval '1 hour',
			last_seen_at = now() - interval '70 minutes'
		WHERE cluster_id IS NULL AND resource_id = ANY($1)`, []string{observedPBS.String(), observedPBSGone.String()})
	exec("audit", `INSERT INTO audit_log (user_id, resource_type, resource_id, action, created_at)
		VALUES ($1, 'pbs_server', $2, 'pbs_updated', now() - interval '65 minutes')`, systemUserID, observedPBS.String())
	rules, _ := json.Marshal([]map[string]any{{"kind": "pbs_server", "change": "", "audit_type": "pbs_server",
		"match_key": "id", "untasked": false, "action_re": "", "except_re": "", "worker_re": "", "field_re": "^schedule$"}})
	if _, err := q.ClassifyGlobalObjectChanges(ctx, gen.ClassifyGlobalObjectChangesParams{
		Rules: rules, LookbackSeconds: 3600, SettledBefore: time.Now().Add(-5 * time.Minute), MaxRows: 1000,
	}); err != nil {
		t.Fatalf("classify: %v", err)
	}
	var explained, shown []string
	if err := pool.QueryRow(ctx, `SELECT
			coalesce(array_agg(change ORDER BY change) FILTER (WHERE explained AND resource_id = $1), '{}'),
			coalesce(array_agg(change ORDER BY change) FILTER (WHERE NOT explained), '{}')
		FROM cluster_object_changes WHERE cluster_id IS NULL AND resource_id = ANY($2)`,
		observedPBS.String(), []string{observedPBS.String(), observedPBSGone.String()}).Scan(&explained, &shown); err != nil {
		t.Fatal(err)
	}
	// The changed job names remote-store too, and the moved one store, which
	// the rule does not cover.
	if fmt.Sprint(explained) != "[added removed]" || fmt.Sprint(shown) != "[added changed changed]" {
		t.Fatalf("explained %v of the server's changes, shown %v of both servers'", explained, shown)
	}
}
