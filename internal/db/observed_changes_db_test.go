package db

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
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

// TestObservedChanges runs the SQL behind the observer (migrations/000124,
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
		for _, s := range []*[]string{&arg.Removed, &arg.Touched, &arg.PresenceRemoved} {
			if *s == nil {
				*s = []string{}
			}
		}
		for _, j := range []*json.RawMessage{&arg.Changes, &arg.States, &arg.Presence} {
			if *j == nil {
				*j = json.RawMessage(`[]`)
			}
		}
		if err := q.RecordObservedObjects(ctx, arg); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	state := func(id, tag string, seen time.Time) map[string]any {
		return map[string]any{"object_id": id, "fields": map[string]any{"nodes": tag}, "node": "",
			"signature": "sig1", "last_seen_at": seen}
	}
	changeID := uuid.New()
	record(gen.RecordObservedObjectsParams{
		Changes: jsonOf([]map[string]any{{"id": changeID, "resource_type": "storage", "resource_id": "store01", "vmid": nil,
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
	// Filed under the id the observer gave it, which marks it a bulk after.
	if err := q.MarkObservedBulk(ctx, gen.MarkObservedBulkParams{Ids: []uuid.UUID{changeID}, Bulk: "b1", BulkSize: 60}); err != nil {
		t.Fatal(err)
	}
	var bulk string
	var bulkSize int
	if err := pool.QueryRow(ctx, `SELECT detail->>'bulk', (detail->>'bulk_size')::int FROM cluster_object_changes
		WHERE id = $1`, changeID).Scan(&bulk, &bulkSize); err != nil || bulk != "b1" || bulkSize != 60 {
		t.Fatalf("marked a bulk: %q of %d, %v", bulk, bulkSize, err)
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

	// Past the cap: an object held by presence, and the notice gates, in the
	// reading's own statement; then gone.
	record(gen.RecordObservedObjectsParams{SeenAt: ago(1), Notices: `{"too_many_objects/storage/":{}}`,
		Presence: jsonOf([]map[string]any{{"object_id": "store03", "node": "", "tag": "w1", "signature": "sig1"}})})
	held, err := q.ListClusterObjectPresence(ctx, observedCluster)
	marks, merr := q.ListClusterObjectBaselines(ctx, observedCluster)
	if err != nil || merr != nil || len(held) != 1 || held[0].ObjectID != "store03" || held[0].Tag != "w1" ||
		!slices.ContainsFunc(marks, func(m gen.ListClusterObjectBaselinesRow) bool {
			return m.ResourceType == "observer_notices" && m.Signature == `{"too_many_objects/storage/":{}}`
		}) {
		t.Fatalf("held past the cap %+v (%v), marks %+v (%v)", held, err, marks, merr)
	}
	record(gen.RecordObservedObjectsParams{SeenAt: ago(1), PresenceRemoved: []string{"store03"}})
	if held, err := q.ListClusterObjectPresence(ctx, observedCluster); err != nil || len(held) != 0 {
		t.Fatalf("held after it went: %+v, %v", held, err)
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
	audit := func(cluster uuid.UUID, at time.Time, rtype, rid string, guest int32, action string, details map[string]any) uuid.UUID {
		t.Helper()
		v := pgtype.Int4{Int32: guest, Valid: guest != 0}
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, vmid, action,
				details, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, cluster, systemUserID, rtype, rid, v, action,
			jsonOf(details), at).Scan(&id); err != nil {
			t.Fatalf("audit: %v", err)
		}
		return id
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
	byOwnRowBy := audit(observedCluster, in, "storage", "store01", 0, "update", nil)
	byRunOnly := seed(observedCluster, "backup", "backup-01", 0, "changed", "", []string{"schedule"}, at)
	audit(observedCluster, in, "backup", "backup-01", 0, "run", nil)
	tooEarly := seed(observedCluster, "storage", "store02", 0, "changed", "", []string{"nodes"}, at)
	audit(observedCluster, at.Add(-9*time.Minute), "storage", "store02", 0, "update", nil)
	otherCluster := seed(observedCluster, "storage", "store03", 0, "changed", "", []string{"nodes"}, at)
	audit(observedOther, in, "storage", "store03", 0, "update", nil)
	// A notice is never explained, even by a row that would explain an edit.
	notice := seed(observedCluster, "storage", "store13", 0, "changed", "", []string{"storage"}, at)
	exec("a notice", `UPDATE cluster_object_changes SET detail = '{"notice":"too_many_objects"}' WHERE id = $1`, notice)
	audit(observedCluster, in, "storage", "store13", 0, "update", nil)

	// A guest: a disk move explains the disk, not the memory changed beside it,
	// unless a config task explains that too; Nexara's own untasked edit counts,
	// a long task counts while it runs, and an untasked row of another action
	// does not.
	diskOnly := seed(observedCluster, "vm", "100", 100, "changed", "pve-01", []string{"scsi0"}, at)
	audit(observedCluster, in, "proxmox_task", "100", 100, "qmmove", map[string]any{"upid": upid("qmmove", 1)})
	diskAndMemory := seed(observedCluster, "vm", "101", 101, "changed", "pve-01", []string{"memory", "scsi0"}, at)
	audit(observedCluster, in, "proxmox_task", "101", 101, "qmmove", map[string]any{"upid": upid("qmmove", 2)})
	bothCovered := seed(observedCluster, "vm", "102", 102, "changed", "pve-01", []string{"memory", "scsi0"}, at)
	bothCoveredBy := audit(observedCluster, in.Add(30*time.Second), "proxmox_task", "102", 102, "qmmove",
		map[string]any{"upid": upid("qmmove", 3)})
	audit(observedCluster, in, "vm", "102", 102, "config_change", map[string]any{"upid": upid("qmconfig", 4)})
	byNexaraEdit := seed(observedCluster, "vm", "103", 103, "changed", "pve-01", []string{"memory"}, at)
	byNexaraEditBy := audit(observedCluster, in, "vm", uuid.NewString(), 103, "config_update",
		map[string]any{"fields": map[string]any{"memory": "4096"}})
	audit(observedCluster, in.Add(30*time.Second), "proxmox_task", "103", 103, "qmmove", map[string]any{"upid": upid("qmmove", 11)})
	byOtherUntasked := seed(observedCluster, "vm", "104", 104, "changed", "pve-01", []string{"memory"}, at)
	audit(observedCluster, in, "vm", uuid.NewString(), 104, "drs_migrate", nil)
	byTaskedEdit := seed(observedCluster, "vm", "107", 107, "changed", "pve-01", []string{"memory"}, at)
	audit(observedCluster, in, "vm", uuid.NewString(), 107, "config_update",
		map[string]any{"upid": upid("resize", 10), "fields": map[string]any{"memory": "4096"}})
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
	// The destroy's details as TrackTask writes them: its node and vmid too.
	byClusterTaskBy := audit(observedCluster, in, "vm", "111", 111, "destroy", map[string]any{
		"upid": "UPID:pve-01:00000008:00000001:00000001:qmdestroy:111:root@pam:", "node": "pve-01", "vmid": 111})
	jobLeft := seed(observedCluster, "backup", "backup-04", 0, "changed", "", []string{"vmid"}, at)
	members(jobLeft, []string{}, []string{"vmid:111"})
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
	// disk's removal leaves, if it named a disk), not another key; the
	// options' edit its settings.
	configOther := seed(observedCluster, "vm", "180", 180, "changed", "pve-01", []string{"description", "hookscript"}, at)
	configUnused := seed(observedCluster, "vm", "181", 181, "changed", "pve-01", []string{"scsi1", "unused0"}, at)
	configDeleted := seed(observedCluster, "vm", "182", 182, "changed", "pve-01", []string{"net1"}, at)
	configNoDisk := seed(observedCluster, "vm", "183", 183, "changed", "pve-01", []string{"description", "unused0"}, at)
	for vmid, fields := range map[int32]map[string]any{180: {"description": "x"}, 181: {"delete": "scsi1"},
		182: {"delete": "net1"}, 183: {"description": "x"}} {
		audit(observedCluster, in, "vm", uuid.NewString(), vmid, "config_update", map[string]any{"fields": fields})
	}
	// A row that says what its edit changed explains only that (changed), not
	// another key it sent; one that could not read the config first, nothing.
	changedSaid := seed(observedCluster, "vm", "184", 184, "changed", "pve-01", []string{"memory"}, at)
	changedSent := seed(observedCluster, "vm", "185", 185, "changed", "pve-01", []string{"cores"}, at)
	changedUnverified := seed(observedCluster, "vm", "186", 186, "changed", "pve-01", []string{"memory"}, at)
	for vmid, extra := range map[int32]map[string]any{184: nil, 185: nil, 186: {"fields_unverified": true}} {
		details := map[string]any{"fields": map[string]any{"memory": "4096", "cores": "2"}, "changed": []string{"memory"}}
		maps.Copy(details, extra)
		audit(observedCluster, in, "vm", uuid.NewString(), vmid, "config_update", details)
	}
	haKeysSet := seed(observedCluster, "ha_rule", "rule01", 0, "changed", "", []string{"comment"}, at)
	audit(observedCluster, in, "ha_rule", "rule01", 0, "updated", map[string]any{"rule": "rule01", "comment": "x"})
	haKeysOther := seed(observedCluster, "ha_rule", "rule02", 0, "changed", "", []string{"comment", "nodes"}, at)
	audit(observedCluster, in, "ha_rule", "rule02", 0, "updated", map[string]any{"rule": "rule02", "comment": "x"})
	optionsSet := seed(observedCluster, "cluster_options", observedCluster.String(), 0, "changed", "", []string{"crs"}, at)
	optionsOther := seed(observedCluster, "cluster_options", observedCluster.String(), 0, "changed", "", []string{"migration"}, at)
	audit(observedCluster, in, "cluster_options", observedCluster.String(), 0, "updated",
		map[string]any{"settings": []string{"crs"}, "cleared": []string{}})
	// An update that lists the fields it sent explains those and what the
	// rule says Proxmox derives from them, by name and from any update.
	sentDerived := seed(observedCluster, "network", "pve-03/vmbr2", 0, "changed", "pve-03",
		[]string{"address", "cidr", "method", "netmask"}, at)
	sentOther := seed(observedCluster, "network", "pve-03/vmbr3", 0, "changed", "pve-03", []string{"mtu"}, at)
	for _, iface := range []string{"pve-03/vmbr2", "pve-03/vmbr3"} {
		audit(observedCluster, in, "network", iface, 0, "interface_updated", map[string]any{"fields": []string{"cidr"}})
	}
	// Two edits between passes: the earlier names the cidr it set, whose
	// address and netmask Proxmox derives, the later the mtu, beside the method
	// every edit sets.
	twoEdits := seed(observedCluster, "network", "pve-03/vmbr5", 0, "changed", "pve-03",
		[]string{"address", "cidr", "method", "mtu", "netmask"}, at)
	twoEditsEarlier := audit(observedCluster, in, "network", "pve-03/vmbr5", 0, "interface_updated",
		map[string]any{"fields": []string{"cidr"}})
	twoEditsLater := audit(observedCluster, in.Add(30*time.Second), "network", "pve-03/vmbr5", 0, "interface_updated",
		map[string]any{"fields": []string{"mtu"}})
	// One that could not read its object first names all it sent, unverified,
	// and explains none of it, nor what any update derives.
	sentUnverified := seed(observedCluster, "network", "pve-03/vmbr4", 0, "changed", "pve-03", []string{"cidr"}, at)
	sentUnverifiedDerived := seed(observedCluster, "network", "pve-03/vmbr6", 0, "changed", "pve-03", []string{"method"}, at)
	for _, iface := range []string{"pve-03/vmbr4", "pve-03/vmbr6"} {
		audit(observedCluster, in, "network", iface, 0, "interface_updated",
			map[string]any{"fields": []string{"cidr"}, "fields_unverified": true})
	}
	// A field a rule takes only where it appeared: a zone's mac filled in.
	macFilled := seed(observedCluster, "sdn_zone", "zone01", 0, "changed", "", []string{"mac"}, at)
	exec("an appeared field", `UPDATE cluster_object_changes SET detail = '{"fields_appeared":["mac"]}' WHERE id = $1`, macFilled)
	macChanged := seed(observedCluster, "sdn_zone", "zone02", 0, "changed", "", []string{"mac"}, at)
	for _, zone := range []string{"zone01", "zone02"} {
		audit(observedCluster, in, "sdn_zone", zone, 0, "sdn_zone_updated", map[string]any{"fields": []string{"mtu"}})
	}
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
	ruleEditedTwice := seed(observedCluster, "firewall_rule", "node/pve-01", 0, "changed", "", []string{"rules"}, at)
	counted(ruleEditedTwice, map[string]int{"added": 2, "removed": 2})
	audit(observedCluster, in, "firewall_rule", "node/pve-01", 0, "firewall_rule_updated", nil)
	ruleEditedTwiceBy := audit(observedCluster, in.Add(30*time.Second), "firewall_rule", "node/pve-01", 0,
		"firewall_rule_updated", nil)
	groupRuleAdded := seed(observedCluster, "firewall_security_group_rule", "web", 0, "changed", "", []string{"rules"}, at)
	counted(groupRuleAdded, map[string]int{"added": 1, "removed": 0})
	groupRuleMoved := seed(observedCluster, "firewall_security_group_rule", "web", 0, "changed", "", []string{"rules"}, at)
	counted(groupRuleMoved, map[string]int{"added": 1, "removed": 0, "moved": 1})
	audit(observedCluster, in, "firewall_security_group_rule", "web", 0, "rule_created", nil)

	// What Nexara filed with no id of the object explains only what it can:
	// a job created with its id unknown, the one job added in its window and
	// none if two were; a subnet filed under its CIDR, the subnet with that CIDR.
	jobAddedUnknown := seed(observedCluster, "backup", "backup-11", 0, "added", "", nil, ago(250))
	audit(observedCluster, ago(251), "backup", "backup-job", 0, "backup_job_created", nil)
	jobAddedBeside := seed(observedCluster, "backup", "backup-13", 0, "added", "", nil, ago(350))
	audit(observedCluster, ago(351), "backup", "backup-12", 0, "backup_job_created", nil)
	twoJobsAdded := seed(observedCluster, "backup", "backup-14", 0, "added", "", nil, ago(450))
	seed(observedCluster, "backup", "backup-15", 0, "added", "", nil, ago(450))
	audit(observedCluster, ago(451), "backup", "backup-job", 0, "backup_job_created", nil)
	subnetByCIDR := seed(observedCluster, "sdn_subnet", "vnet01/zone01-192.0.2.0-24", 0, "added", "", nil, at)
	members(subnetByCIDR, []string{"cidr:vnet01/192.0.2.0/24"}, []string{})
	subnetOther := seed(observedCluster, "sdn_subnet", "vnet01/zone01-198.51.100.0-24", 0, "added", "", nil, at)
	members(subnetOther, []string{"cidr:vnet01/198.51.100.0/24"}, []string{})
	subnetNoCIDR := seed(observedCluster, "sdn_subnet", "vnet01/zone01-203.0.113.0-24", 0, "added", "", nil, at)
	audit(observedCluster, in, "sdn_subnet", "vnet01/192.0.2.0/24", 0, "sdn_subnet_created", nil)

	// Access: a user's delete explains its own grants going, never another's
	// arriving; Nexara's grant only the grantees its row names; a realm's sync
	// only that realm's users.
	aclByDelete := seed(observedCluster, "pve_acl", "/vms/170", 0, "changed", "", []string{"user_grants", "users"}, at)
	members(aclByDelete, []string{}, []string{"user_grants:Role01:1:user01@pve", "users:user01@pve"})
	aclEscalated := seed(observedCluster, "pve_acl", "/vms/171", 0, "changed", "", []string{"user_grants", "users"}, at)
	members(aclEscalated, []string{"user_grants:Role02:1:user09@pve"}, []string{"user_grants:Role01:1:user01@pve", "users:user01@pve"})
	audit(observedCluster, in, "pve_user", "user01@pve", 0, "deleted", nil)
	aclByGrant := seed(observedCluster, "pve_acl", "/", 0, "changed", "", []string{"user_grants", "users"}, at)
	members(aclByGrant, []string{"user_grants:Role01:1:user02@pve", "users:user02@pve"}, []string{})
	aclBesideGrant := seed(observedCluster, "pve_acl", "/", 0, "changed", "", []string{"user_grants"}, at)
	members(aclBesideGrant, []string{"user_grants:Role01:1:user09@pve"}, []string{})
	audit(observedCluster, in, "pve_acl", "/", 0, "granted", map[string]any{"roles": "Role01", "users": "user02@pve,user03@pve"})
	// A grant names its roles, its grantees and the propagate it sent (1 when
	// it sent none): another role arriving shows; a propagate flipped by it
	// does not, the old grant going with it.
	aclGrantOtherRole := seed(observedCluster, "pve_acl", "/vms/177", 0, "changed", "", []string{"user_grants"}, at)
	members(aclGrantOtherRole, []string{"user_grants:Role02:1:user17@pve"}, []string{})
	audit(observedCluster, in, "pve_acl", "/vms/177", 0, "granted", map[string]any{"roles": "Role01", "users": "user17@pve"})
	aclPropagateFlip := seed(observedCluster, "pve_acl", "/vms/178", 0, "changed", "", []string{"user_grants"}, at)
	members(aclPropagateFlip, []string{"user_grants:Role01:0:user18@pve"}, []string{"user_grants:Role01:1:user18@pve"})
	audit(observedCluster, in, "pve_acl", "/vms/178", 0, "granted",
		map[string]any{"roles": "Role01", "users": "user18@pve", "propagate": false})
	aclPropagateOther := seed(observedCluster, "pve_acl", "/vms/179", 0, "changed", "", []string{"user_grants"}, at)
	members(aclPropagateOther, []string{"user_grants:Role01:1:user19@pve"}, []string{})
	audit(observedCluster, in, "pve_acl", "/vms/179", 0, "granted",
		map[string]any{"roles": "Role01", "users": "user19@pve", "propagate": false})
	// A grant naming only a group names no user: a user arriving shows.
	aclByGroupGrant := seed(observedCluster, "pve_acl", "/storage/store03", 0, "changed", "", []string{"user_grants", "users"}, at)
	members(aclByGroupGrant, []string{"user_grants:Role01:1:user06@pve", "users:user06@pve"}, []string{})
	audit(observedCluster, in, "pve_acl", "/storage/store03", 0, "granted",
		map[string]any{"roles": "Role01", "users": "", "groups": "group01", "tokens": ""})
	// A path's first grant names every grantee it brought, whatever its kind.
	aclAddedByGrant := seed(observedCluster, "pve_acl", "/sdn/zones/zone01", 0, "added", "", nil, at)
	members(aclAddedByGrant, []string{"users:user12@pve", "user_grants:Role01:1:user12@pve", "groups:group02",
		"group_grants:Role01:1:group02"}, []string{})
	audit(observedCluster, in, "pve_acl", "/sdn/zones/zone01", 0, "granted",
		map[string]any{"roles": "Role01", "users": "user12@pve", "groups": "group02", "tokens": ""})
	aclAddedBeside := seed(observedCluster, "pve_acl", "/sdn/zones/zone02", 0, "added", "", nil, at)
	members(aclAddedBeside, []string{"users:user12@pve", "user_grants:Role01:1:user12@pve", "users:user13@pve",
		"user_grants:Role01:1:user13@pve"}, []string{})
	audit(observedCluster, in, "pve_acl", "/sdn/zones/zone02", 0, "granted",
		map[string]any{"roles": "Role01", "users": "user12@pve", "groups": "", "tokens": ""})
	// A role's delete takes grants out, wherever and whoever's; never one in.
	aclRoleGone := seed(observedCluster, "pve_acl", "/vms/172", 0, "changed", "", []string{"user_grants"}, at)
	members(aclRoleGone, []string{}, []string{"user_grants:Role01:0:user10@pve"})
	aclRoleBeside := seed(observedCluster, "pve_acl", "/vms/173", 0, "changed", "", []string{"user_grants"}, at)
	members(aclRoleBeside, []string{"user_grants:Role02:1:user11@pve"}, []string{"user_grants:Role01:0:user11@pve"})
	aclOtherRoleGone := seed(observedCluster, "pve_acl", "/vms/174", 0, "changed", "", []string{"user_grants"}, at)
	members(aclOtherRoleGone, []string{}, []string{"user_grants:Role03:1:user14@pve"})
	audit(observedCluster, in, "pve_role", "Role01", 0, "deleted", nil)
	// A user's keys are explained only by an update that says it set them.
	userKeysSet := seed(observedCluster, "pve_user", "user20@pve", 0, "changed", "", []string{"keys"}, at)
	audit(observedCluster, in, "pve_user", "user20@pve", 0, "updated", map[string]any{"keys_changed": true})
	userKeysOther := seed(observedCluster, "pve_user", "user21@pve", 0, "changed", "", []string{"keys"}, at)
	audit(observedCluster, in, "pve_user", "user21@pve", 0, "updated", map[string]any{"enable": true})
	// A rule that matches by field alone names nothing, a detail of the name too;
	// a row reaching another object names none of its fields, even by its flag.
	userByFieldAlone := seed(observedCluster, "pve_user", "user22@pve", 0, "changed", "", []string{"email"}, at)
	userByFieldAloneBy := audit(observedCluster, in, "pve_user", "user22@pve", 0, "updated", map[string]any{"email": "x"})
	groupByUserFlag := seed(observedCluster, "pve_group", "group03", 0, "changed", "", []string{"comment"}, at)
	groupByUserFlagBy := audit(observedCluster, in, "pve_user", "user24@pve", 0, "updated", map[string]any{"comment": "x"})
	// A role's update: the privileges it lists join; without append, those it
	// does not list leave.
	roleJoined := seed(observedCluster, "pve_role", "Role05", 0, "changed", "", []string{"privs"}, at)
	members(roleJoined, []string{"privs:VM.Audit"}, []string{})
	audit(observedCluster, in, "pve_role", "Role05", 0, "updated", map[string]any{"privs": "VM.Audit,Sys.Audit", "append": true})
	roleReplaced := seed(observedCluster, "pve_role", "Role06", 0, "changed", "", []string{"privs"}, at)
	members(roleReplaced, []string{}, []string{"privs:Sys.Audit"})
	audit(observedCluster, in, "pve_role", "Role06", 0, "updated", map[string]any{"privs": "VM.Audit", "append": false})
	roleAppended := seed(observedCluster, "pve_role", "Role07", 0, "changed", "", []string{"privs"}, at)
	members(roleAppended, []string{}, []string{"privs:Sys.Audit"})
	audit(observedCluster, in, "pve_role", "Role07", 0, "updated", map[string]any{"privs": "VM.Audit", "append": true})
	roleKeptListed := seed(observedCluster, "pve_role", "Role08", 0, "changed", "", []string{"privs"}, at)
	members(roleKeptListed, []string{}, []string{"privs:VM.Audit"})
	audit(observedCluster, in, "pve_role", "Role08", 0, "updated", map[string]any{"privs": "VM.Audit", "append": false})
	// A user id holding a * names that one user, never a pattern of them.
	groupBesideStar := seed(observedCluster, "pve_group", "admins", 0, "changed", "", []string{"users"}, at)
	members(groupBesideStar, []string{"users:user07@pve"}, []string{})
	audit(observedCluster, in, "pve_user", "*@pve", 0, "created", map[string]any{"groups_set": true})
	byRealmSync := seed(observedCluster, "pve_user", "user04@ldap01", 0, "changed", "", []string{"email"}, at)
	// enable, expire and keys a sync sets only where its realm maps them.
	syncMapped := seed(observedCluster, "pve_user", "user10@ldap01", 0, "changed", "", []string{"enable"}, at)
	syncUnmapped := seed(observedCluster, "pve_user", "user11@ldap01", 0, "changed", "", []string{"keys"}, at)
	realmFields := jsonOf(map[string][]string{"ldap01": {"enable"}, "ldap02": {"enable"}})
	besideRealmSync := seed(observedCluster, "pve_user", "user05@pve", 0, "changed", "", []string{"email"}, at)
	syncJoinedSynced := seed(observedCluster, "pve_user", "user08@ldap01", 0, "changed", "", []string{"groups"}, at)
	members(syncJoinedSynced, []string{"groups:staff-ldap01"}, []string{})
	syncJoinedOther := seed(observedCluster, "pve_user", "user09@ldap01", 0, "changed", "", []string{"groups"}, at)
	members(syncJoinedOther, []string{"groups:admins"}, []string{})
	// A user joins a group by a sync only if its log added or replaced it, and
	// is added by one only if its log says so; a replaced user keeps its
	// enable, whatever the realm maps, and with the log unread none is its.
	syncJoinedUnlogged := seed(observedCluster, "pve_user", "user12@ldap01", 0, "changed", "", []string{"groups"}, at)
	members(syncJoinedUnlogged, []string{"groups:staff-ldap01"}, []string{})
	// On either side, a user joins a sync's group by it only as its log names
	// that user: added, replaced whole or updated in place.
	syncJoinedUpdated := seed(observedCluster, "pve_user", "user21@ldap01", 0, "changed", "", []string{"groups"}, at)
	members(syncJoinedUpdated, []string{"groups:staff-ldap01"}, []string{})
	syncGroupJoined := seed(observedCluster, "pve_group", "staff-ldap01", 0, "changed", "", []string{"users"}, at)
	members(syncGroupJoined, []string{"users:user08@ldap01", "users:user21@ldap01"}, []string{})
	syncGroupJoinedUnlogged := seed(observedCluster, "pve_group", "dev-ldap01", 0, "changed", "", []string{"users"}, at)
	members(syncGroupJoinedUnlogged, []string{"users:user12@ldap01"}, []string{})
	syncAdded := seed(observedCluster, "pve_user", "user13@ldap01", 0, "added", "", nil, at)
	syncAddedUnlogged := seed(observedCluster, "pve_user", "user14@ldap01", 0, "added", "", nil, at)
	syncEnableOverwritten := seed(observedCluster, "pve_user", "user20@ldap01", 0, "changed", "", []string{"enable"}, at)
	syncLogUnread := seed(observedCluster, "pve_user", "user01@ldap02", 0, "changed", "", []string{"enable"}, at)
	const unreadSyncUPID = "UPID:pve-01:0000000E:00000001:00000001:auth-realm-sync:ldap02:root@pam:"
	audit(observedCluster, in, "proxmox_task", "ldap02", 0, "auth-realm-sync", map[string]any{"upid": unreadSyncUPID})
	// A sync removes, and purges the ACL of, only what its log names: user15
	// it removed and purged, user19 it purged and kept; user16's ACL and
	// user17 went beside it.
	const syncUPID = "UPID:pve-01:0000000D:00000001:00000001:auth-realm-sync:ldap01:root@pam:"
	taskLogs := jsonOf(map[string]any{syncUPID: map[string]any{
		"removed":     map[string][]string{"users": {"user15@ldap01"}},
		"purged":      map[string][]string{"users": {"user15@ldap01", "user19@ldap01"}},
		"overwritten": map[string][]string{"users": {"user20@ldap01", "user08@ldap01", "user09@ldap01"}},
		"added":       map[string][]string{"users": {"user13@ldap01"}},
		"updated":     map[string][]string{"users": {"user21@ldap01"}}}})
	// A user it replaced whole: its keys, which its realm does not map.
	syncOverwritten := seed(observedCluster, "pve_user", "user20@ldap01", 0, "changed", "", []string{"keys"}, at)
	syncACLRemoved := seed(observedCluster, "pve_acl", "/vms/175", 0, "changed", "", []string{"user_grants", "users"}, at)
	members(syncACLRemoved, []string{}, []string{"user_grants:Role01:1:user15@ldap01", "users:user15@ldap01"})
	syncUserRemoved := seed(observedCluster, "pve_user", "user15@ldap01", 0, "removed", "", nil, at)
	syncACLKept := seed(observedCluster, "pve_acl", "/vms/176", 0, "changed", "", []string{"user_grants", "users"}, at)
	members(syncACLKept, []string{}, []string{"user_grants:Role01:1:user16@ldap01", "users:user16@ldap01"})
	syncUserBeside := seed(observedCluster, "pve_user", "user17@ldap01", 0, "removed", "", nil, at)
	syncACLPurgedOnly := seed(observedCluster, "pve_acl", "/vms/177", 0, "changed", "", []string{"user_grants", "users"}, at)
	members(syncACLPurgedOnly, []string{}, []string{"user_grants:Role01:1:user19@ldap01", "users:user19@ldap01"})
	audit(observedCluster, in, "proxmox_task", "ldap01", 0, "auth-realm-sync", map[string]any{"upid": syncUPID})
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
	cloneMade := seed(observedCluster, "vm", "150", 150, "changed", "pve-01", []string{"memory"}, ago(200))
	cloneBeside := seed(observedCluster, "vm", "151", 151, "changed", "pve-01", []string{"memory"}, ago(200))
	besideDetails := seed(observedCluster, "pool", "app", 0, "changed", "", []string{"vms"}, ago(200))
	// A pool's own update explains the members its row lists joining, or with
	// delete leaving, and no other.
	poolUpdate := func(pool string, joined, left []string, details map[string]any) uuid.UUID {
		id := seed(observedCluster, "pool", pool, 0, "changed", "", []string{"vms"}, ago(200))
		members(id, joined, left)
		audit(observedCluster, ago(201), "pool", pool, 0, "updated", details)
		return id
	}
	poolJoined := poolUpdate("pool02", []string{"vms:199"}, []string{}, map[string]any{"poolid": "pool02", "vms": "199"})
	poolBeside := poolUpdate("pool03", []string{"vms:100"}, []string{}, map[string]any{"poolid": "pool03", "vms": "199"})
	poolLeft := poolUpdate("pool04", []string{}, []string{"vms:198"}, map[string]any{"poolid": "pool04", "vms": "198", "delete": "1"})
	poolRemovalJoined := poolUpdate("pool05", []string{"vms:197"}, []string{},
		map[string]any{"poolid": "pool05", "vms": "197", "delete": "1"})
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
	// Filed back under the cap, a change came at some time since its object was
	// last compared, before the crossing: its own rows count from then, a row
	// reaching it from elsewhere from the look-back still.
	backUnder := func(id uuid.UUID) uuid.UUID {
		exec("back under", `UPDATE cluster_object_changes SET last_seen_at = $2,
			detail = detail || '{"while_over_cap": true}' WHERE id = $1`, id, ago(800))
		return id
	}
	backUnderOwn := backUnder(seed(observedCluster, "storage", "store13", 0, "changed", "", []string{"nodes"}, ago(500)))
	audit(observedCluster, ago(700), "storage", "store13", 0, "update", nil)
	backUnderOther := backUnder(seed(observedCluster, "pool", "web3", 0, "changed", "", []string{"vms"}, ago(500)))
	members(backUnderOther, []string{}, []string{"vms:132"})
	audit(observedCluster, ago(700), "vm", "132", 132, "destroy", map[string]any{
		"upid": "UPID:pve-01:00000013:00000001:00000001:qmdestroy:132:root@pam:", "node": "pve-01", "vmid": 132})

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
	derived := func(r map[string]any, d map[string][]string) map[string]any { r["derived"] = d; return r }
	appeared := func(r map[string]any) map[string]any { r["appeared"] = true; return r }
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
		with(rule("ha_rule", "changed", "ha_rule", "id", false, "^updated$", "", "", ""), "fields_from", "keys"),
		derived(with(rule("network", "changed", "network", "id", false, "^interface_updated$", "", "", ""),
			"fields_from", "fields"), map[string][]string{"cidr": {"address", "netmask"}, "*": {"method"}}),
		appeared(rule("sdn_zone", "changed", "sdn_zone", "id", false, "^sdn_zone_updated$", "", "", "^mac$")),
		with(rule("firewall_rule", "changed", "firewall_rule", "id", false, "^firewall_rule_updated$", "", "", "^rules$"),
			"counts", "edit1"),
		with(rule("firewall_security_group_rule", "changed", "firewall_security_group_rule", "id", false,
			"^rule_created$", "", "", "^rules$"), "counts", "add1"),
		rule("ha_resource", "changed", "", "sid", false, "", "", "^hastart$", "^state$"),
		with(with(rule("pool", "changed", "", "cluster", false, "", "", "^qmdestroy$", "^vms$"), "member", "left"),
			"member_of", "upid:vms:%s"),
		with(with(rule("backup", "removed", "", "cluster", false, "", "", "^qmdestroy$", ""), "member", "sole"),
			"member_of", "upid:vmid:%s"),
		with(with(rule("backup", "changed", "", "cluster", false, "", "", "^qmdestroy$", "^vmid$"), "member", "left"),
			"member_of", "upid:vmid:%s"),
		with(with(rule("pool", "changed", "", "cluster", false, "", "", "^qmrestore$", "^vms$"), "member", "joined"),
			"member_of", "upid:vms:%s"),
		with(with(rule("ha_rule", "changed", "ha_resource", "cluster", false, "^deleted$", "", "", "^resources$"),
			"member", "left"), "member_of", "audit:resources:%s"),
		with(with(rule("pve_acl", "changed", "pve_user", "cluster", false, "^deleted$", "", "", "^(users|user_grants)$"),
			"member", "left"), "member_of", "audit:users:%s|user_grants:*:%s"),
		with(with(rule("pve_acl", "changed", "pve_acl", "id", false, "^granted$", "", "", "^users$"),
			"member", "joined"), "member_of", "details-list.users:users:%s"),
		with(with(rule("pve_acl", "changed", "pve_acl", "id", false, "^granted$", "", "", "^user_grants$"),
			"member", "joined"), "member_of", "grants.users:user_grants:%1$s:%2$s:%3$s"),
		with(with(rule("pve_acl", "changed", "pve_acl", "id", false, "^granted$", "", "", "^user_grants$"),
			"member", "left"), "member_of", "grants.users:user_grants:%1$s:*:%3$s"),
		with(rule("pve_user", "changed", "", "cluster", false, "", "", "^auth-realm-sync$",
			"^(firstname|lastname|email|comment)$"), "object", "upid:*@%s"),
		with(with(rule("pve_user", "changed", "", "cluster", false, "", "", "^auth-realm-sync$",
			"^(expire|keys)$"), "object", "upid:*@%s"), "fields_from", "realm"),
		with(with(with(rule("pve_user", "changed", "", "cluster", false, "", "", "^auth-realm-sync$",
			"^enable$"), "object", "upid:*@%s"), "fields_from", "realm"), "log_names", "!overwritten:users"),
		with(with(with(with(rule("pve_user", "changed", "", "cluster", false, "", "", "^auth-realm-sync$", "^groups$"),
			"object", "upid:*@%s"), "member", "joined"), "member_of", "upid:groups:*-%s"), "log_names", "overwritten:users"),
		with(with(with(with(rule("pve_user", "changed", "", "cluster", false, "", "", "^auth-realm-sync$", "^groups$"),
			"object", "upid:*@%s"), "member", "joined"), "member_of", "upid:groups:*-%s"), "log_names", "updated:users"),
		with(with(with(rule("pve_group", "changed", "", "cluster", false, "", "", "^auth-realm-sync$", "^users$"),
			"object", "upid:*-%s"), "member", "joined"), "member_of", "log.added,updated,overwritten.users:users:%s"),
		with(with(rule("pve_user", "added", "", "cluster", false, "", "", "^auth-realm-sync$", ""), "object", "upid:*@%s"),
			"log_names", "added:users"),
		with(with(with(rule("pve_acl", "changed", "", "cluster", false, "", "", "^auth-realm-sync$",
			"^(users|user_grants)$"), "member", "left"), "member_of", "upid:users:*@%s|user_grants:*@%s"), "log_names", "purged"),
		with(with(rule("pve_user", "removed", "", "cluster", false, "", "", "^auth-realm-sync$", ""), "object", "upid:*@%s"),
			"log_names", "removed:users"),
		with(with(rule("pve_user", "changed", "", "cluster", false, "", "", "^auth-realm-sync$",
			"^(firstname|lastname|email|comment|expire|keys)$"), "object", "upid:*@%s"), "log_names", "overwritten:users"),
		with(with(rule("pve_acl", "added", "pve_acl", "id", false, "^granted$", "", "", ""), "member", "joined"),
			"member_of", "details-list.users:users:%s+grants.users:user_grants:%1$s:%2$s:%3$s+"+
				"details-list.groups:groups:%s+grants.groups:group_grants:%1$s:%2$s:%3$s"),
		rule("pve_user", "changed", "pve_user", "id", false, "^updated$", "", "", "^(firstname|lastname|email|comment)$"),
		with(rule("pve_group", "changed", "pve_user", "cluster", false, "^updated$", "", "", "^comment$"), "detail_key", "comment"),
		with(rule("pve_user", "changed", "pve_user", "id", false, "^updated$", "", "", "^keys$"), "detail_key", "keys_changed"),
		with(with(rule("pve_role", "changed", "pve_role", "id", false, "^updated$", "", "", "^privs$"), "member", "joined"),
			"member_of", "details-list.privs:privs:%s"),
		with(with(with(with(rule("pve_role", "changed", "pve_role", "id", false, "^updated$", "", "", "^privs$"),
			"member", "left_unlisted"), "member_of", "details-list.privs:privs:%s"), "detail_key", "append"),
			"detail_value", "false"),
		with(with(rule("pve_acl", "changed", "pve_role", "cluster", false, "^deleted$", "", "", "^(user|group|token)_grants$"),
			"member", "left"), "member_of", "audit:user_grants:%s:*|group_grants:%s:*|token_grants:%s:*"),
		with(with(with(rule("pve_group", "changed", "pve_user", "cluster", false, "^created$", "", "", "^users$"),
			"detail_key", "groups_set"), "member", "joined"), "member_of", "audit:users:%s"),
		with(rule("backup", "added", "backup", "cluster", false, "^backup_job_created$", "", "", ""), "audit_id_is", "backup-job"),
		with(with(rule("sdn_subnet", "added", "sdn_subnet", "cluster", false, "^sdn_subnet_created$", "", "", ""),
			"member", "joined"), "member_of", "audit:cidr:%s"),
		with(with(rule("pool", "changed", "vm", "details:new_pool", false, "^set_pool$", "", "", "^vms$"), "member", "joined"),
			"member_of", "vmid:vms:%s"),
		with(with(rule("pool", "changed", "vm", "cluster", false, "^clone$", "", "", "^vms$"), "member", "joined"),
			"member_of", "details.new_id:vms:%s"),
		with(with(with(rule("pool", "changed", "pool", "id", false, "^updated$", "", "", "^vms$"), "detail_key", "!delete"),
			"member", "joined"), "member_of", "details-list.vms:vms:%s"),
		with(with(with(rule("pool", "changed", "pool", "id", false, "^updated$", "", "", "^vms$"), "detail_key", "delete"),
			"member", "left"), "member_of", "details-list.vms:vms:%s"),
		rule("vm", "changed", "vm", "details:new_id", false, "^clone$", "", "", ""),
		rule("network", "", "network", "node", false, "^network_reverted$", "", "", ""),
	})
	classify := func(allListed bool, maxRows int32) int64 {
		t.Helper()
		n, err := q.ClassifyObservedChanges(ctx, gen.ClassifyObservedChangesParams{
			Rules: rules, LookbackSeconds: 3600, ClusterID: observedCluster, SettledBefore: ago(5),
			AllNodesListed: allListed, GiveUpBefore: now.Add(-24 * time.Hour), MaxRows: maxRows, TaskLogs: taskLogs,
			RealmFields: realmFields,
		})
		if err != nil {
			t.Fatalf("classify: %v", err)
		}
		return n
	}
	explained := func(id uuid.UUID) string {
		t.Helper()
		var e pgtype.Bool
		var by pgtype.UUID
		if err := pool.QueryRow(ctx, `SELECT explained, explained_by FROM cluster_object_changes WHERE id = $1`, id).
			Scan(&e, &by); err != nil {
			t.Fatalf("read explained: %v", err)
		}
		if by.Valid != (e.Valid && e.Bool) {
			t.Errorf("change %s: explained %v, by %v", id, e, by)
		}
		switch {
		case !e.Valid:
			return "waiting"
		case e.Bool:
			return "explained"
		}
		return "shown"
	}

	// One call decides at most max_rows, the oldest first; with a node's task
	// list unread, only what has waited past the give-up.
	if n := classify(true, 2); n != 2 {
		t.Fatalf("one call decided %d, want 2", n)
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
		{"a notice, beside a row that would explain an edit", notice, "shown"},
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
		{"a guest's destroy, that guest leaving a job", jobLeft, "explained"},
		{"an HA resource deleted, its sid leaving a rule", byResourceDelete, "explained"},
		{"a config edit, another key changed too", configOther, "shown"},
		{"a config edit, the disk it left unused", configUnused, "explained"},
		{"a config edit naming no disk, a disk left unused", configNoDisk, "shown"},
		{"a config edit that deleted the key", configDeleted, "explained"},
		{"a config edit that says it changed the key", changedSaid, "explained"},
		{"a config edit that sent the key and says it changed another", changedSent, "shown"},
		{"a config edit that could not read the config first", changedUnverified, "shown"},
		{"an options edit of that setting", optionsSet, "explained"},
		{"an options edit, another setting", optionsOther, "shown"},
		{"an update of the field its row records", haKeysSet, "explained"},
		{"an update, and a field its row does not record", haKeysOther, "shown"},
		{"an update of a field it sent, and of those derived from it", sentDerived, "explained"},
		{"two edits between passes", twoEdits, "explained"},
		{"an update, and a field it did not send", sentOther, "shown"},
		{"an update that could not read its object first", sentUnverified, "shown"},
		{"an update that could not read its object first, what any update derives", sentUnverifiedDerived, "shown"},
		{"a field filled in where there was none", macFilled, "explained"},
		{"the same field changed", macChanged, "shown"},
		{"a rule updated, one in and one out", ruleEdited, "explained"},
		{"a rule updated, and another added", ruleEditedAndAdded, "shown"},
		{"a rule created, one in", groupRuleAdded, "explained"},
		{"a rule created, and one moved", groupRuleMoved, "shown"},
		{"a job created with its id unknown", jobAddedUnknown, "explained"},
		{"a job added beside another's create", jobAddedBeside, "shown"},
		{"one job created with its id unknown, two added", twoJobsAdded, "shown"},
		{"a subnet created, filed under its CIDR", subnetByCIDR, "explained"},
		{"a subnet added beside another CIDR's create", subnetOther, "shown"},
		{"a subnet added that lists no CIDR to name", subnetNoCIDR, "shown"},
		{"a user's delete, its grants going", aclByDelete, "explained"},
		{"a user's delete, and another's grant arriving", aclEscalated, "shown"},
		{"Nexara's grant to the users its row names", aclByGrant, "explained"},
		{"Nexara's grant, and another user's arriving", aclBesideGrant, "shown"},
		{"a grant naming only a group, a user arriving", aclByGroupGrant, "shown"},
		{"a grant of one role, another arriving", aclGrantOtherRole, "shown"},
		{"a grant that flipped its role's propagate", aclPropagateFlip, "explained"},
		{"a grant at propagate 0, one at 1 arriving", aclPropagateOther, "shown"},
		{"a path's first grant, to a user and a group", aclAddedByGrant, "explained"},
		{"a path's first grant, and another user on it", aclAddedBeside, "shown"},
		{"a role's delete, its grant going", aclRoleGone, "explained"},
		{"a role's delete, and another role arriving", aclRoleBeside, "shown"},
		{"a role's delete, another role's grant going", aclOtherRoleGone, "shown"},
		{"a user created as *@pve, another user joining", groupBesideStar, "shown"},
		{"a user's update that set its keys", userKeysSet, "explained"},
		{"a user's update that did not, its keys changed", userKeysOther, "shown"},
		{"a role's update, a privilege it lists joining", roleJoined, "explained"},
		{"a role's update replacing its list, one it left out leaving", roleReplaced, "explained"},
		{"a role's update appending, one leaving", roleAppended, "shown"},
		{"a role's update replacing its list, one it lists leaving", roleKeptListed, "shown"},
		{"a realm's sync, a user of that realm", byRealmSync, "explained"},
		{"a realm's sync, a field its realm maps", syncMapped, "explained"},
		{"a realm's sync, a field its realm does not map", syncUnmapped, "shown"},
		{"a realm's sync, that field of a user it overwrote", syncOverwritten, "explained"},
		{"a realm's sync, a user of another", besideRealmSync, "shown"},
		{"a realm's sync, its user joining a group it syncs", syncJoinedSynced, "explained"},
		{"a realm's sync, its user joining another group", syncJoinedOther, "shown"},
		{"a realm's sync, a user it did not add or replace joining", syncJoinedUnlogged, "shown"},
		{"a realm's sync, a user it updated joining a group it syncs", syncJoinedUpdated, "explained"},
		{"a realm's sync, its group joined by users its log names", syncGroupJoined, "explained"},
		{"a realm's sync, its group joined by a user its log does not name", syncGroupJoinedUnlogged, "shown"},
		{"a realm's sync, a user its log says it added", syncAdded, "explained"},
		{"a realm's sync, a user added beside it", syncAddedUnlogged, "shown"},
		{"a realm's sync, the enable of a user it replaced", syncEnableOverwritten, "shown"},
		{"a realm's sync whose log is unread, a field its realm maps", syncLogUnread, "shown"},
		{"a realm's sync, the ACL of a user it removed", syncACLRemoved, "explained"},
		{"a realm's sync, a user its log says it removed", syncUserRemoved, "explained"},
		{"a realm's sync, the ACL of a user it did not purge", syncACLKept, "shown"},
		{"a realm's sync, a user of the realm removed beside it", syncUserBeside, "shown"},
		{"a realm's sync, the ACL it purged of a user it kept", syncACLPurgedOnly, "explained"},
		{"a restore started more than an hour before", byLongRestore, "shown"},
		{"a guest moved into the pool from its page", byDetails, "explained"},
		{"a guest cloned into the pool through Nexara", byClone, "explained"},
		{"a clone Nexara made, by its new id", cloneMade, "explained"},
		{"a guest beside Nexara's clone", cloneBeside, "shown"},
		{"a guest moved into another pool", besideDetails, "shown"},
		{"a pool's update, the guest it added joining", poolJoined, "explained"},
		{"a pool's update adding one guest, another joining", poolBeside, "shown"},
		{"a pool's update removing a guest, it leaving", poolLeft, "explained"},
		{"a pool's update removing a guest, it joining", poolRemovalJoined, "shown"},
		{"a revert of the node's pending network", byNode, "explained"},
		{"a revert on another node", besideNode, "shown"},
		{"added by a disk task", addedByTask, "explained"},
		{"added beside a disk task that named another", addedBesideTask, "shown"},
		{"renamed away by an update", renamedAway, "explained"},
		{"removed beside an update that renamed nothing", removedBesideUpdate, "shown"},
		{"added, nothing on record", addedUnexplained, "shown"},
		{"after an outage, by a row from the gap's start", afterOutage, "shown"},
		{"after an outage, by a row within the look-back", afterOutageRecent, "explained"},
		{"back under the cap, by its own row since its last reading", backUnderOwn, "explained"},
		{"back under the cap, by a row from elsewhere before the look-back", backUnderOther, "shown"},
		{"seen too recently", fresh, "waiting"},
		{"settled, nothing on record", nothingOnRecord, "shown"},
		{"another writer's row", notMine, "waiting"},
	} {
		if got := explained(tc.id); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	// explained_by names, of the latest row explaining each thing, the latest.
	for name, tc := range map[string]struct{ change, by uuid.UUID }{
		"its own row":                       {byOwnRow, byOwnRowBy},
		"the later of the two that explain": {bothCovered, bothCoveredBy},
		"not a later row explaining none":   {byNexaraEdit, byNexaraEditBy},
		"the later of a list's two":         {ruleEditedTwice, ruleEditedTwiceBy},
	} {
		var by uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT explained_by FROM cluster_object_changes WHERE id = $1`, tc.change).
			Scan(&by); err != nil || by != tc.by {
			t.Errorf("%s: explained by %s (%v), want %s", name, by, err, tc.by)
		}
	}

	// explained_unnamed: by explaining row, what it covered without naming it,
	// each row its own, a consequence under the object it reached.
	for name, tc := range map[string]struct {
		change uuid.UUID
		want   map[string][]string
	}{
		"a row naming its settings records nothing": {optionsSet, nil},
		"a change shown records nothing":            {diskAndMemory, nil},
		"two edits, each row its own unnamed fields": {twoEdits,
			map[string][]string{twoEditsEarlier.String(): {"address", "netmask"}, twoEditsLater.String(): {"method"}}},
		"a destroy, the pool's vms under the pool's own change": {byClusterTask,
			map[string][]string{byClusterTaskBy.String(): {"vms"}}},
		"a destroy, the job's vmid, which its own details name": {jobLeft,
			map[string][]string{byClusterTaskBy.String(): {"vmid"}}},
		"a flag naming its field, keys_changed, records nothing": {userKeysSet, nil},
		"a rule matching by field alone names nothing": {userByFieldAlone,
			map[string][]string{userByFieldAloneBy.String(): {"email"}}},
		"another object's row names nothing of this one": {groupByUserFlag,
			map[string][]string{groupByUserFlagBy.String(): {"comment"}}},
	} {
		var raw []byte
		var got map[string][]string
		if err := pool.QueryRow(ctx, `SELECT explained_unnamed FROM cluster_object_changes WHERE id = $1`, tc.change).
			Scan(&raw); err != nil || raw != nil && json.Unmarshal(raw, &got) != nil || fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: explained_unnamed %s (%v), want %v", name, raw, err, tc.want)
		}
	}

	// A sync is one an audit row carries: a task row anyone may file names
	// no task Proxmox ran.
	const forgedSyncUPID = "UPID:pve-01:0000000F:00000001:00000001:auth-realm-sync:ldap01:root@pam:"
	task(observedCluster, syncUPID, 0, in, in.Add(time.Second))
	task(observedCluster, forgedSyncUPID, 0, in, in.Add(time.Second))
	if syncs, err := q.ListRealmSyncTasks(ctx, gen.ListRealmSyncTasksParams{ClusterID: observedCluster, Since: ago(600)}); err != nil ||
		len(syncs) != 1 || syncs[0].Upid != syncUPID {
		t.Errorf("realm syncs listed %+v (%v), want the one an audit row carries", syncs, err)
	}

	// What nothing could classify in a day and an hour is shown; another
	// writer's rows and what is merely recent are left alone.
	stuck := seed(observedCluster, "storage", "store12", 0, "changed", "", []string{"nodes"}, now.Add(-30*time.Hour))
	notMineOld := seed(observedCluster, "replication", "100-1", 0, "changed", "", []string{"schedule"}, now.Add(-30*time.Hour))
	exec("a trigger's row", `UPDATE cluster_object_changes SET observed_by = 'trigger' WHERE id = $1`, notMineOld)
	if _, err := q.ShowUnclassifiedObservedChanges(ctx, now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		id   uuid.UUID
		want string
	}{"stuck": {stuck, "shown"}, "recent": {fresh, "waiting"}, "another writer's": {notMineOld, "waiting"}} {
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

// TestObservedGlobalChanges runs migrations 000125 and 000126 against Postgres:
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
	serverRow := func(action string, minutesAgo int) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO audit_log (user_id, resource_type, resource_id, action, created_at)
			VALUES ($1, 'pbs_server', $2, $3, now() - make_interval(mins => $4::int)) RETURNING id`,
			systemUserID, observedPBS.String(), action, minutesAgo).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	classify := func(actionAndFieldRes ...string) { // pairs: one 'id' rule each
		t.Helper()
		rules := make([]map[string]any, 0, len(actionAndFieldRes)/2)
		for i := 0; i+1 < len(actionAndFieldRes); i += 2 {
			rules = append(rules, map[string]any{"kind": "pbs_server", "change": "", "audit_type": "pbs_server",
				"match_key": "id", "untasked": false, "action_re": actionAndFieldRes[i], "except_re": "",
				"worker_re": "", "field_re": actionAndFieldRes[i+1]})
		}
		b, _ := json.Marshal(rules)
		if _, err := q.ClassifyGlobalObjectChanges(ctx, gen.ClassifyGlobalObjectChangesParams{
			Rules: b, LookbackSeconds: 3600, SettledBefore: time.Now().Add(-5 * time.Minute), MaxRows: 1000,
		}); err != nil {
			t.Fatalf("classify: %v", err)
		}
	}
	byAudit := serverRow("pbs_updated", 65)
	classify("", "^schedule$")
	var explained, shown []string
	if err := pool.QueryRow(ctx, `SELECT
			coalesce(array_agg(change ORDER BY change) FILTER (WHERE explained AND resource_id = $1 AND explained_by = $3), '{}'),
			coalesce(array_agg(change ORDER BY change) FILTER (WHERE NOT explained AND explained_by IS NULL), '{}')
		FROM cluster_object_changes WHERE cluster_id IS NULL AND resource_id = ANY($2)`,
		observedPBS.String(), []string{observedPBS.String(), observedPBSGone.String()}, byAudit).Scan(&explained, &shown); err != nil {
		t.Fatal(err)
	}
	// The changed job names remote-store too, and the moved one store, which
	// the rule does not cover.
	if fmt.Sprint(explained) != "[added removed]" || fmt.Sprint(shown) != "[added changed changed]" {
		t.Fatalf("explained %v of the server's changes, shown %v of both servers'", explained, shown)
	}

	// explained_by: of the latest row explaining each field, the latest.
	var twoFields, by uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO cluster_object_changes (resource_type, resource_id, change, fields,
			observed_by, last_seen_at, observed_at)
		VALUES ('pbs_server', $1, 'changed', '{comment,schedule}', 'trigger', now() - interval '70 minutes',
			now() - interval '1 hour') RETURNING id`, observedPBS.String()).Scan(&twoFields); err != nil {
		t.Fatal(err)
	}
	serverRow("schedule_set", 64)
	comment := serverRow("comment_set", 63)
	latest := serverRow("schedule_set", 62)
	classify("^schedule_set$", "^schedule$", "^comment_set$", "^comment$")
	var raw []byte
	var unnamed map[string][]string
	if err := pool.QueryRow(ctx, `SELECT explained_by, explained_unnamed FROM cluster_object_changes WHERE id = $1`, twoFields).
		Scan(&by, &raw); err != nil || by != latest || json.Unmarshal(raw, &unnamed) != nil ||
		fmt.Sprint(unnamed) != fmt.Sprint(map[string][]string{latest.String(): {"schedule"}, comment.String(): {"comment"}}) {
		t.Errorf("explained by %s, unnamed %s (%v), want the later of the schedule's two rows, %s, and each row its own field",
			by, raw, err, latest)
	}
}
