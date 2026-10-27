package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/migrations"
)

// migrationByName finds a migration by the tail of its name, not its number, so
// the test follows it when the migration is renumbered. It returns the number
// of the migration that runs just before it, its own number, and its up SQL.
func migrationByName(t *testing.T, tail string) (before, version uint, up string) {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), tail+".up.sql") {
			v, _ := parseMigrationVersion(e.Name())
			version = uint(v)
			raw, err := fs.ReadFile(migrations.FS, e.Name())
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			up = string(raw)
		}
	}
	if version == 0 {
		t.Fatalf("no migration named *%s.up.sql", tail)
	}
	for _, e := range entries {
		if v, ok := parseMigrationVersion(e.Name()); ok && uint(v) < version && uint(v) > before {
			before = uint(v)
		}
	}
	return before, version, up
}

// auditIdentityID is a fixed id for the back-fill's fixture, so a run that aborts
// before its purge leaves rows the next run's up-front purge can find.
func auditIdentityID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("12100000-0000-4000-8000-%012d", n))
}

// Legacy audit rows of the shapes the dev install holds are seeded at the schema
// just before the back-fill. Each ends where today's identity would put it, with
// the originals in details.rekeyed_from, or — when the row says too little —
// exactly where it was. A second run of the same SQL changes nothing.
func TestMigrationAuditIdentityBackfill_RefilesWhatItCanResolve(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	before, version, upSQL := migrationByName(t, "audit_identity_backfill")
	var (
		user         = auditIdentityID(1)
		clusterA     = auditIdentityID(2)
		clusterB     = auditIdentityID(3)
		node         = auditIdentityID(4)
		nodeB        = auditIdentityID(5)
		poolLive     = auditIdentityID(6)
		poolOther    = auditIdentityID(7)
		deadPool     = auditIdentityID(8)
		srv1         = auditIdentityID(21) // cluster A: holds datastore01, datastore02 and datastore05, sync job sync01, verify job verify01
		srv2         = auditIdentityID(22) // cluster A: holds datastore02 too, so that name is ambiguous
		srvStandAlon = auditIdentityID(23) // no cluster: holds datastore03
		srvNew       = auditIdentityID(24) // cluster A: created after the row that names its datastore
		srvOther     = auditIdentityID(25) // cluster B: holds datastore05 and sync job sync01 as well, so those names are ambiguous
	)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// Audit rows first, by id: they are not necessarily removed with a cluster.
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE id::text LIKE '12100000-0000-4000-8000-%'`)
		_, _ = pool.Exec(pctx, `DELETE FROM pbs_servers WHERE id::text LIKE '12100000-0000-4000-8000-%'`)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{clusterA, clusterB})
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, user)
	}
	purge()
	defer purge()
	// Whatever the test leaves, the next one finds the schema at head.
	defer func() {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Errorf("restore to head: %v", err)
		}
	}()

	if err := m.Migrate(before); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to %d: %v", before, err)
	}
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	exec("user", `INSERT INTO users (id, email, password_hash, display_name) VALUES ($1, 'migration-audit-identity@example.invalid', 'x', 'Audit identity')`, user)
	for _, c := range []struct {
		id   uuid.UUID
		name string
	}{{clusterA, "cluster01"}, {clusterB, "cluster02"}} {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://cluster.invalid:8006', 'test@pam!t', 'x')`, c.id, c.name)
	}
	exec("node", `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01'), ($3, $4, 'pve-02')`, node, clusterA, nodeB, clusterB)
	exec("pools", `INSERT INTO storage_pools (id, cluster_id, node_id, storage) VALUES ($1, $2, $3, 'store01'), ($4, $5, $6, 'store02')`,
		poolLive, clusterA, node, poolOther, clusterB, nodeB)

	old := time.Now().Add(-48 * time.Hour)
	for _, s := range []struct {
		id      uuid.UUID
		name    string
		cluster any
		created time.Time
	}{
		{srv1, "pbs01", clusterA, old}, {srv2, "pbs02", clusterA, old},
		{srvStandAlon, "pbs03", nil, old}, {srvNew, "pbs04", clusterA, time.Now()},
		{srvOther, "pbs05", clusterB, old},
	} {
		exec("pbs server", `INSERT INTO pbs_servers (id, name, api_url, token_id, token_secret_encrypted, cluster_id, created_at)
			VALUES ($1, $2, 'https://pbs.invalid:8007', 'test@pbs!t', 'x', $3, $4)`, s.id, s.name, s.cluster, s.created)
	}
	for _, h := range []struct {
		server    uuid.UUID
		datastore string
	}{{srv1, "datastore01"}, {srv1, "datastore02"}, {srv2, "datastore02"}, {srvStandAlon, "datastore03"}, {srvNew, "datastore04"},
		{srv1, "datastore05"}, {srvOther, "datastore05"}, {srvStandAlon, "datastore07"}} {
		exec("snapshot", `INSERT INTO pbs_snapshots (pbs_server_id, datastore, backup_type, backup_id, backup_time)
			VALUES ($1, $2, 'vm', '101', 1700000000)`, h.server, h.datastore)
	}
	// Capacity samples are the only evidence of a datastore with no snapshots yet.
	exec("metrics", `INSERT INTO pbs_datastore_metrics (time, pbs_server_id, datastore) VALUES (now(), $1, 'datastore06'), (now(), $2, 'datastore07')`, srv2, srv1)
	exec("verify job", `INSERT INTO pbs_verify_jobs (pbs_server_id, job_id, store) VALUES ($1, 'verify01', 'datastore01'), ($2, 'verify09', 'datastore04'), ($3, 'verify08', 'datastore08')`, srv1, srvNew, srv2)
	exec("sync job of a datastore nothing else shows", `INSERT INTO pbs_sync_jobs (pbs_server_id, job_id, store) VALUES ($1, 'sync10', 'datastore10')`, srv1)
	for _, j := range []struct {
		server    uuid.UUID
		datastore string
	}{{srv1, "datastore01"}, {srvOther, "datastore05"}} {
		exec("sync job", `INSERT INTO pbs_sync_jobs (pbs_server_id, job_id, store) VALUES ($1, 'sync01', $2)`, j.server, j.datastore)
	}

	upid := func(typ, id string) string {
		return fmt.Sprintf("UPID:pve-01:0000A1B2:00000001:6A8CE407:%s:%s:root@pam:", typ, id)
	}
	type row struct {
		name                     string
		cluster                  *uuid.UUID
		source                   string
		typ, id, action, details string
		at                       time.Time
		wantTyp, wantID, wantAct string
		wantDetails              string
	}
	now := time.Now().Add(-time.Hour)
	// A row whose want* fields are empty must end exactly as it was seeded.
	rows := []row{
		{name: "update keyed by a live inventory row", cluster: &clusterA, typ: "storage", id: poolLive.String(), action: "update", details: `{}`,
			wantTyp: "storage", wantID: "store01", wantAct: "update", wantDetails: `{"storage":"store01"}`},
		{name: "a row that already carries rekeyed_from keeps that one", cluster: &clusterA, typ: "storage", id: poolLive.String(), action: "update",
			details: `{"rekeyed_from":{"resource_type":"network","resource_id":"cluster/rule/3","action":"firewall_rule_created"}}`,
			wantTyp: "storage", wantID: "store01", wantAct: "update",
			wantDetails: `{"storage":"store01","rekeyed_from":{"resource_type":"network","resource_id":"cluster/rule/3","action":"firewall_rule_created"}}`},
		{name: "upload keyed by a dead row that names its storage", cluster: &clusterA, typ: "storage", id: deadPool.String(), action: "upload",
			details: `{"storage":"store03","node":"pve-01"}`, wantTyp: "storage", wantID: "store03", wantAct: "upload", wantDetails: `{"storage":"store03","node":"pve-01"}`},
		{name: "update keyed by a dead row that names nothing", cluster: &clusterA, typ: "storage", id: deadPool.String(), action: "update", details: `{}`},
		{name: "create already keyed by name", cluster: &clusterA, typ: "storage", id: "store01", action: "create", details: `{"storage":"store01","type":"dir"}`},
		{name: "a live row of ANOTHER cluster", cluster: &clusterA, typ: "storage", id: poolOther.String(), action: "update", details: `{}`},
		{name: "virtio-win config with no storage", cluster: &clusterA, typ: "storage", id: "", action: "virtio_win_config_update", details: `{"storage":""}`,
			wantTyp: "cluster", wantID: clusterA.String(), wantAct: "virtio_win_config_update", wantDetails: `{"storage":""}`},
		{name: "virtio-win config with a storage", cluster: &clusterA, typ: "storage", id: "store01", action: "virtio_win_config_update", details: `{"storage":"store01"}`},

		{name: "ingested imgcopy", cluster: &clusterA, source: "proxmox", typ: "storage", id: "pve-01", action: "image_copy",
			details: `{"node":"pve-01","task_type":"imgcopy","resource_id":""}`, wantTyp: "node", wantID: "pve-01", wantAct: "image_copy",
			wantDetails: `{"node":"pve-01","task_type":"imgcopy","resource_id":""}`},
		{name: "ingested download", cluster: &clusterA, source: "proxmox", typ: "storage", id: `linux01\x2diso`, action: "download",
			details: `{"node":"pve-01","task_type":"download","resource_id":"linux01\\x2diso"}`, wantTyp: "node", wantID: "pve-01", wantAct: "download",
			wantDetails: `{"node":"pve-01","task_type":"download","resource_id":"linux01\\x2diso"}`},
		{name: "a Nexara row that carries a task_type is not an ingested one", cluster: &clusterA, source: "nexara", typ: "storage", id: "store01", action: "download_url",
			details: `{"storage":"store01","node":"pve-01","task_type":"download"}`},
		{name: "ingested imgdel of a volume with an owner", cluster: &clusterA, source: "proxmox", typ: "proxmox_task", id: "105@store02", action: "imgdel",
			details: fmt.Sprintf(`{"upid":%q,"task_type":"imgdel","resource_id":"105@store02"}`, upid("imgdel", "105@store02")),
			wantTyp: "storage", wantID: "store02", wantAct: "delete_content",
			wantDetails: fmt.Sprintf(`{"upid":%q,"task_type":"imgdel","resource_id":"105@store02","storage":"store02"}`, upid("imgdel", "105@store02"))},
		{name: "ingested imgdel of a volume without one", cluster: &clusterA, source: "proxmox", typ: "proxmox_task", id: "store02", action: "imgdel",
			details: fmt.Sprintf(`{"upid":%q,"task_type":"imgdel"}`, upid("imgdel", "store02")),
			wantTyp: "storage", wantID: "store02", wantAct: "delete_content",
			wantDetails: fmt.Sprintf(`{"upid":%q,"task_type":"imgdel","storage":"store02"}`, upid("imgdel", "store02"))},
		{name: "ingested prunebackups of one guest's backups", cluster: &clusterA, source: "proxmox", typ: "proxmox_task", id: "105@store02", action: "prunebackups",
			details: fmt.Sprintf(`{"upid":%q,"task_type":"prunebackups","resource_id":"105@store02"}`, upid("prunebackups", "105@store02")),
			wantTyp: "storage", wantID: "store02", wantAct: "prune_backups",
			wantDetails: fmt.Sprintf(`{"upid":%q,"task_type":"prunebackups","resource_id":"105@store02","storage":"store02"}`, upid("prunebackups", "105@store02"))},
		{name: "imgdel whose UPID disagrees with its resource_id", cluster: &clusterA, source: "proxmox", typ: "proxmox_task", id: "pve-01", action: "imgdel",
			details: fmt.Sprintf(`{"upid":%q,"task_type":"imgdel"}`, upid("imgdel", "store02"))},
		{name: "another ingested task", cluster: &clusterA, source: "proxmox", typ: "proxmox_task", id: "pveproxy", action: "srvstart",
			details: fmt.Sprintf(`{"upid":%q,"task_type":"srvstart"}`, upid("srvstart", "pveproxy"))},

		{name: "gc on a datastore one server holds", cluster: &clusterA, typ: "backup", id: "datastore01", action: "gc_triggered", details: `{}`,
			wantTyp: "pbs_server", wantID: srv1.String(), wantAct: "gc_triggered", wantDetails: `{"datastore":"datastore01","resource_name":"datastore01"}`},
		{name: "snapshot delete keeps its store key", cluster: &clusterA, typ: "backup", id: "datastore01/vm/101", action: "snapshot_deleted",
			details: `{"store":"datastore01","backup_type":"vm","backup_id":"101","backup_time":1700000000}`,
			wantTyp: "pbs_server", wantID: srv1.String(), wantAct: "snapshot_deleted",
			wantDetails: `{"datastore":"datastore01","resource_name":"datastore01/vm/101","store":"datastore01","backup_type":"vm","backup_id":"101","backup_time":1700000000}`},
		{name: "prune on a datastore two servers hold", cluster: &clusterA, typ: "backup", id: "datastore02", action: "datastore_pruned", details: `{"store":"datastore02"}`},
		{name: "gc on a datastore no server holds", cluster: &clusterA, typ: "backup", id: "datastore09", action: "gc_triggered", details: `{}`},
		{name: "gc on a standalone server's datastore", typ: "backup", id: "datastore03", action: "gc_triggered", details: `{}`,
			wantTyp: "pbs_server", wantID: srvStandAlon.String(), wantAct: "gc_triggered", wantDetails: `{"datastore":"datastore03","resource_name":"datastore03"}`},
		{name: "gc in a cluster the standalone holder is not in: servers move between clusters", cluster: &clusterA, typ: "backup", id: "datastore03", action: "gc_triggered", details: `{}`,
			wantTyp: "pbs_server", wantID: srvStandAlon.String(), wantAct: "gc_triggered", wantDetails: `{"datastore":"datastore03","resource_name":"datastore03"}`},
		{name: "gc on a datastore the servers of two clusters both hold", cluster: &clusterA, typ: "backup", id: "datastore05", action: "gc_triggered", details: `{}`},
		{name: "gc with no cluster, on a datastore only a clustered server holds", typ: "backup", id: "datastore01", action: "gc_triggered", details: `{}`,
			wantTyp: "pbs_server", wantID: srv1.String(), wantAct: "gc_triggered", wantDetails: `{"datastore":"datastore01","resource_name":"datastore01"}`},
		{name: "gc on a datastore only a capacity sample says a server holds", cluster: &clusterA, typ: "backup", id: "datastore06", action: "gc_triggered", details: `{}`,
			wantTyp: "pbs_server", wantID: srv2.String(), wantAct: "gc_triggered", wantDetails: `{"datastore":"datastore06","resource_name":"datastore06"}`},
		{name: "gc on a datastore only a verify job says a server holds", cluster: &clusterA, typ: "backup", id: "datastore08", action: "gc_triggered", details: `{}`,
			wantTyp: "pbs_server", wantID: srv2.String(), wantAct: "gc_triggered", wantDetails: `{"datastore":"datastore08","resource_name":"datastore08"}`},
		{name: "gc on a datastore only a sync job says a server holds", cluster: &clusterA, typ: "backup", id: "datastore10", action: "gc_triggered", details: `{}`,
			wantTyp: "pbs_server", wantID: srv1.String(), wantAct: "gc_triggered", wantDetails: `{"datastore":"datastore10","resource_name":"datastore10"}`},
		{name: "gc on a datastore a sample and a snapshot put on different servers", cluster: &clusterA, typ: "backup", id: "datastore07", action: "gc_triggered", details: `{}`},
		{name: "gc that predates the server", cluster: &clusterA, at: old, typ: "backup", id: "datastore04", action: "gc_triggered", details: `{}`},
		{name: "verify job one server has", cluster: &clusterA, typ: "backup", id: "verify01", action: "verify_job_triggered", details: `{}`,
			wantTyp: "pbs_server", wantID: srv1.String(), wantAct: "verify_job_triggered",
			wantDetails: `{"job_id":"verify01","resource_name":"verify01","datastore":"datastore01"}`},
		{name: "verify job that predates the server that has it", cluster: &clusterA, at: old, typ: "backup", id: "verify09", action: "verify_job_triggered", details: `{}`},
		{name: "sync job id that is only a verify job", cluster: &clusterA, typ: "backup", id: "verify01", action: "sync_job_triggered", details: `{}`},
		{name: "sync job id that two servers have", cluster: &clusterA, typ: "backup", id: "sync01", action: "sync_job_triggered", details: `{}`},
		{name: "a restore is a guest's row", cluster: &clusterA, typ: "backup", id: "100/vm", action: "backup_restored", details: `{"pbs_server_id":"x","datastore":"datastore01"}`},
		{name: "a created backup job", cluster: &clusterA, typ: "backup", id: "backup-job", action: "backup_job_created", details: `{"storage":"store01"}`},
	}
	for i := range rows {
		r := &rows[i]
		if r.at.IsZero() {
			r.at = now
		}
		if r.source == "" {
			r.source = "nexara"
		}
		if r.wantTyp == "" {
			r.wantTyp, r.wantID, r.wantAct, r.wantDetails = r.typ, r.id, r.action, r.details
		}
		exec(r.name, `INSERT INTO audit_log (id, cluster_id, user_id, resource_type, resource_id, action, details, source, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)`,
			auditIdentityID(100+i), r.cluster, user, r.typ, r.id, r.action, r.details, r.source, r.at)
	}

	fingerprint := func() string {
		var fp string
		if err := pool.QueryRow(ctx, `SELECT md5(string_agg(id::text || '|' || COALESCE(cluster_id::text, '') || '|' || resource_type || '|' ||
			resource_id || '|' || action || '|' || details::text, ';' ORDER BY id)) FROM audit_log WHERE id::text LIKE '12100000-0000-4000-8000-%'`).Scan(&fp); err != nil {
			t.Fatalf("fingerprint: %v", err)
		}
		return fp
	}
	seeded := fingerprint()

	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate over the back-fill: %v", err)
	}
	for i, r := range rows {
		var typ, id, action string
		var details []byte
		if err := pool.QueryRow(ctx, `SELECT resource_type, resource_id, action, details FROM audit_log WHERE id = $1`,
			auditIdentityID(100+i)).Scan(&typ, &id, &action, &details); err != nil {
			t.Fatalf("%s: read back: %v", r.name, err)
		}
		var got, want, seededDetails map[string]any
		if err := json.Unmarshal(details, &got); err != nil {
			t.Fatalf("%s: details: %v", r.name, err)
		}
		if err := json.Unmarshal([]byte(r.wantDetails), &want); err != nil {
			t.Fatalf("%s: want details: %v", r.name, err)
		}
		_ = json.Unmarshal([]byte(r.details), &seededDetails)
		// A row that moved records where it was, unless it already said so; a row
		// that did not move gains nothing.
		if _, has := seededDetails["rekeyed_from"]; !has && (r.wantTyp != r.typ || r.wantID != r.id || r.wantAct != r.action) {
			want["rekeyed_from"] = map[string]any{"resource_type": r.typ, "resource_id": r.id, "action": r.action}
		}
		if typ != r.wantTyp || id != r.wantID || action != r.wantAct || !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n  got  (%s, %q, %s) %s\n  want (%s, %q, %s) %s",
				r.name, typ, id, action, details, r.wantTyp, r.wantID, r.wantAct, r.wantDetails)
		}
	}
	afterFirst := fingerprint()
	if afterFirst == seeded {
		t.Fatal("the back-fill changed nothing: the fixture is not exercising it")
	}

	// A second run of the same SQL changes nothing.
	if _, err := pool.Exec(ctx, upSQL); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if again := fingerprint(); again != afterFirst {
		t.Error("a second run of the back-fill changed rows the first had already re-filed")
	}
}
