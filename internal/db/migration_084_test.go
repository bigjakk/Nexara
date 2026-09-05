package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixed ids so a run that aborts before its purge leaves rows this run's
// up-front purge can find.
var (
	m084User    = uuid.MustParse("84000000-0000-4000-8000-000000000001")
	m084Cluster = uuid.MustParse("84000000-0000-4000-8000-000000000002")
	m084Node    = uuid.MustParse("84000000-0000-4000-8000-000000000003")
	m084LiveVM  = uuid.MustParse("84000000-0000-4000-8000-000000000004")
	// The UUID an audit row recorded before the collector churned the guest's
	// vms row. Nothing in vms points at it any more.
	m084DeadVMUUID = uuid.MustParse("84000000-0000-4000-8000-0000000000ff")
)

// TestMigration084_BackfillsAndResolvesGuestIdentity is the data lock for
// migration 000084, which denormalizes the guest VMID onto audit_log.
//
// The defect it fixes: resource_vmid / resource_name were derived at read time
// from `LEFT JOIN vms v ON v.id::text = a.resource_id`, and that join misses
// whenever the guest's vms row has been churned by the collector (it is
// deleted and re-inserted with a fresh UUID on resync), whenever the guest has
// been destroyed, and for the handlers that record a VMID rather than a UUID
// in resource_id. On a live install that left both fields empty on ~73% of
// entries — including every `destroy`, which is where the record matters most.
//
// Four properties, end to end:
//
//  1. The backfill resolves each of its four arms, and leaves genuinely
//     guest-less rows NULL rather than inventing a 0.
//  2. The read query answers for a DESTROYED guest — the headline case, which
//     no join against vms can ever satisfy.
//  3. The read query answers across vms.id CHURN, via (cluster_id, vmid).
//  4. New inserts derive vmid themselves, so app code cannot forget it, and
//     the down migration drops the column cleanly.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database — never the
// live nexara DB, since this round-trips the schema up and down).
func TestMigration084_BackfillsAndResolvesGuestIdentity(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		// audit_log.cluster_id is ON DELETE CASCADE, as are nodes and vms, so
		// dropping the cluster takes the guest rows and most audit rows with
		// it. The NULL-cluster row has to go by user.
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE user_id = $1`, m084User)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, m084Cluster)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, m084User)
	}
	purge()
	defer purge()

	// Step 1: sit at 83, before audit_log.vmid exists, and seed rows shaped
	// like the ones a real install accumulated.
	if err := m.Migrate(83); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to 83: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, display_name)
		 VALUES ($1, 'migration-084@example.invalid', 'x', 'Migration 084')`, m084User); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		 VALUES ($1, 'migration-084-test', 'https://cluster.invalid:8006', 'test@pam!t', 'x')`,
		m084Cluster); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`,
		m084Node, m084Cluster); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// The guest that still exists. VMID 125 is reachable through its own UUID
	// AND, after 084, through (cluster_id, vmid).
	if _, err := pool.Exec(ctx,
		`INSERT INTO vms (id, cluster_id, node_id, vmid, name, type)
		 VALUES ($1, $2, $3, 125, 'linux03', 'qemu')`,
		m084LiveVM, m084Cluster, m084Node); err != nil {
		t.Fatalf("seed vm: %v", err)
	}

	type seedRow struct {
		id       uuid.UUID
		resType  string
		resID    string
		action   string
		details  string
		cluster  *uuid.UUID
		wantVmid *int32
	}
	i := func(n int32) *int32 { return &n }
	rows := []seedRow{
		{
			// Arm 1: the handler recorded details.vmid explicitly.
			id: uuid.New(), resType: "vm", resID: uuid.New().String(), action: "destroy",
			details:  `{"node":"pve-03","vmid":121,"resource_name":"Veeam13-appliance02"}`,
			cluster:  &m084Cluster,
			wantVmid: i(121),
		},
		{
			// Arm 2: no details.vmid, but the UPID's id field carries it. This
			// is the shape every TrackTask entry had.
			id: uuid.New(), resType: "vm", resID: uuid.New().String(), action: "start",
			details:  `{"node":"pve-01","upid":"UPID:pve-01:001316BE:00B8B463:6A8CE407:qmstart:110:root@pam!nexara:","resource_name":"web01"}`,
			cluster:  &m084Cluster,
			wantVmid: i(110),
		},
		{
			// Arm 3: resource_id IS the vmid (what the VM create handler
			// records), so the UUID join could never have matched it.
			id: uuid.New(), resType: "vm", resID: "133", action: "create",
			details: `{"node":"pve-02"}`, cluster: &m084Cluster, wantVmid: i(133),
		},
		{
			// Arm 4: nothing in the row names the vmid, but resource_id still
			// resolves through vms — so freeze it now, before the churn.
			id: uuid.New(), resType: "vm", resID: m084LiveVM.String(), action: "migrate",
			details: `{"node":"pve-01"}`, cluster: &m084Cluster, wantVmid: i(125),
		},
		{
			// A node task: the UPID's id field is empty. Must stay NULL, not 0.
			id: uuid.New(), resType: "node", resID: "pve-01", action: "apt_update",
			details: `{"node":"pve-01","upid":"UPID:pve-01:0000A1B2:00000001:6A8CE407:aptupdate::root@pam:"}`,
			cluster: &m084Cluster, wantVmid: nil,
		},
		{
			// The gate's reason for existing: cephdestroyosd's worker id is the
			// OSD NUMBER, not a VMID, and ceph_osd.go dispatches exactly this
			// through TrackTask. It is numeric and would pass a digits-only
			// check — stamping "destroy OSD 125" as an action on VM 125, which
			// happens to exist in this fixture.
			id: uuid.New(), resType: "ceph_osd", resID: "125", action: "osd_destroy",
			details: `{"node":"pve-01","upid":"UPID:pve-01:0000A1B2:00000001:6A8CE407:cephdestroyosd:125:root@pam:"}`,
			cluster: &m084Cluster, wantVmid: nil,
		},
		{
			// A storage task: the id field is a non-numeric storage name.
			id: uuid.New(), resType: "storage", resID: "local", action: "backup",
			details: `{"upid":"UPID:pve-01:0000A1B2:00000001:6A8CE407:vzdump:local:root@pam:"}`,
			cluster: &m084Cluster, wantVmid: nil,
		},
		{
			// A global settings change — no cluster, no guest.
			id: uuid.New(), resType: "setting", resID: "branding.app_title",
			action: "setting_updated", details: `{"key":"branding.app_title"}`,
			cluster: nil, wantVmid: nil,
		},
	}
	for _, r := range rows {
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_log (id, cluster_id, user_id, resource_type, resource_id, action, details)
			 VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`,
			r.id, r.cluster, m084User, r.resType, r.resID, r.action, r.details); err != nil {
			t.Fatalf("seed audit row %s: %v", r.action, err)
		}
	}

	// The column must not exist yet, or the backfill assertions below would be
	// satisfied by a schema that never had the bug.
	if hasColumn(ctx, t, pool, "audit_log", "vmid") {
		t.Fatal("audit_log.vmid exists at version 83 — the fixture is not testing the backfill")
	}

	// Step 2: apply 084.
	if err := m.Migrate(84); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to 84: %v", err)
	}
	if !hasColumn(ctx, t, pool, "audit_log", "vmid") {
		t.Fatal("audit_log.vmid missing after migrating to 84")
	}

	// Property 1: every arm resolved, and the guest-less rows stayed NULL.
	for _, r := range rows {
		var got *int32
		if err := pool.QueryRow(ctx, `SELECT vmid FROM audit_log WHERE id = $1`, r.id).Scan(&got); err != nil {
			t.Fatalf("read vmid for %s: %v", r.action, err)
		}
		switch {
		case r.wantVmid == nil && got != nil:
			t.Errorf("%s (%s): vmid = %d, want NULL — a guest-less entry must not claim a guest",
				r.action, r.resType, *got)
		case r.wantVmid != nil && got == nil:
			t.Errorf("%s (%s): vmid = NULL, want %d — the backfill did not resolve this arm",
				r.action, r.resType, *r.wantVmid)
		case r.wantVmid != nil && got != nil && *got != *r.wantVmid:
			t.Errorf("%s (%s): vmid = %d, want %d", r.action, r.resType, *got, *r.wantVmid)
		}
	}

	// Step 3: churn the live guest exactly as the collector does — delete the
	// row and re-insert it under a fresh UUID, same (cluster_id, vmid) — and
	// point one audit row at a UUID that never comes back, standing in for an
	// entry written before an earlier churn.
	if _, err := pool.Exec(ctx, `DELETE FROM vms WHERE id = $1`, m084LiveVM); err != nil {
		t.Fatalf("churn delete: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO vms (id, cluster_id, node_id, vmid, name, type)
		 VALUES (gen_random_uuid(), $1, $2, 125, 'linux03', 'qemu')`,
		m084Cluster, m084Node); err != nil {
		t.Fatalf("churn re-insert: %v", err)
	}

	q := gen.New(pool)
	read := func(action string) gen.ListAuditLogAdvancedRow {
		t.Helper()
		got, err := q.ListAuditLogAdvanced(ctx, gen.ListAuditLogAdvancedParams{
			Limit: 50, Offset: 0,
			ClusterID: pgtype.UUID{Bytes: m084Cluster, Valid: true},
			Action:    pgtype.Text{String: action, Valid: true},
		})
		if err != nil {
			t.Fatalf("ListAuditLogAdvanced(%s): %v", action, err)
		}
		if len(got) != 1 {
			t.Fatalf("ListAuditLogAdvanced(%s) returned %d rows, want 1", action, len(got))
		}
		return got[0]
	}

	// Property 2: the destroyed guest. Its vms row never existed here, so the
	// UUID join and the (cluster_id, vmid) join both miss — the name can only
	// come from what the entry itself recorded.
	if row := read("destroy"); row.ResourceVmid != 121 || row.ResourceName != "Veeam13-appliance02" {
		t.Errorf("destroy: vmid=%d name=%q, want 121 / %q — a destroyed guest's identity must survive in the entry",
			row.ResourceVmid, row.ResourceName, "Veeam13-appliance02")
	}

	// Property 3: across churn. resource_id names a UUID nothing points at any
	// more, so the VMID can only come from the denormalized column.
	//
	// The NAME deliberately does not come back. Resolving it from whichever
	// guest currently holds (cluster_id, vmid) would be a guess: Proxmox reuses
	// VMIDs, so on a destroy entry that lookup returns the guest that took the
	// id afterwards — an audit row asserting an action against a guest that did
	// not exist when it happened. An empty name is the honest answer, and the
	// entry still carries the VMID, which is more than it had before 000084.
	if _, err := pool.Exec(ctx,
		`UPDATE audit_log SET resource_id = $1 WHERE action = 'migrate' AND user_id = $2`,
		m084DeadVMUUID.String(), m084User); err != nil {
		t.Fatalf("point migrate row at a churned UUID: %v", err)
	}
	if row := read("migrate"); row.ResourceVmid != 125 {
		t.Errorf("migrate: vmid=%d, want 125 — the churned UUID must still resolve through audit_log.vmid",
			row.ResourceVmid)
	}
	if row := read("migrate"); row.ResourceName != "" {
		t.Errorf("migrate: name=%q, want empty — a name must never be re-resolved from the guest currently holding the VMID",
			row.ResourceName)
	}

	// Property 4a: a fresh insert through the generated query derives vmid
	// from the UPID without the caller passing one.
	if err := q.InsertAuditLog(ctx, gen.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: m084Cluster, Valid: true},
		UserID:       pgtype.UUID{Bytes: m084User, Valid: true},
		ResourceType: "vm",
		ResourceID:   uuid.New().String(),
		Action:       "migration-084-fresh",
		Details: []byte(
			`{"upid":"UPID:pve-01:001316BE:00B8B463:6A8CE407:qmshutdown:142:root@pam!nexara:","resource_name":"fresh"}`),
	}); err != nil {
		t.Fatalf("InsertAuditLog: %v", err)
	}
	if row := read("migration-084-fresh"); row.ResourceVmid != 142 || row.ResourceName != "fresh" {
		t.Errorf("fresh insert: vmid=%d name=%q, want 142 / fresh — the insert must derive vmid itself",
			row.ResourceVmid, row.ResourceName)
	}

	// Property 4b: down drops the column, and the rows survive.
	if err := m.Migrate(83); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down to 83: %v", err)
	}
	if hasColumn(ctx, t, pool, "audit_log", "vmid") {
		t.Error("audit_log.vmid still present after the down migration")
	}
	var surviving int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE user_id = $1`, m084User).Scan(&surviving); err != nil {
		t.Fatalf("count surviving rows: %v", err)
	}
	if surviving != len(rows)+1 {
		t.Errorf("audit_log has %d seeded rows after the down migration, want %d — the down must not delete entries",
			surviving, len(rows)+1)
	}

	// Leave the schema at head so a later test in this package does not
	// inherit version 83.
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate back up: %v", err)
	}
}
