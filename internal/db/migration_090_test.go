package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	m090ClusterA = uuid.MustParse("90000000-0000-4000-8000-000000000001")
	m090ClusterB = uuid.MustParse("90000000-0000-4000-8000-000000000002")
	m090ClusterC = uuid.MustParse("90000000-0000-4000-8000-000000000003")
	m090Node     = uuid.MustParse("90000000-0000-4000-8000-000000000004")
	m090Server   = uuid.MustParse("90000000-0000-4000-8000-000000000005")
)

// TestMigration090_ResolvesVeeamOwnGuests is the behaviour lock for
// ResolveVeeamInfrastructureGuests, which decides which guests coverage will
// EXCLUDE. Both directions of a wrong answer are bad in ways nothing else
// catches: excluding too much hides a real machine that has no backups, and
// excluding too little produces the false alarms this whole eligibility model
// exists to remove.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database).
func TestMigration090_ResolvesVeeamOwnGuests(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool := env.Ctx, env.Pool

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM veeam_servers WHERE id = $1`, m090Server)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`,
			[]uuid.UUID{m090ClusterA, m090ClusterB, m090ClusterC})
	}
	purge()
	defer purge()

	migrateUp(t, env.Migrate)

	seedCluster := func(id uuid.UUID, name string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			 VALUES ($1, $2, 'https://cluster.invalid:8006', 'test@pam!t', 'x')`, id, name); err != nil {
			t.Fatalf("seed cluster %s: %v", name, err)
		}
	}
	seedCluster(m090ClusterA, "migration-090-a")
	seedCluster(m090ClusterB, "migration-090-b")
	seedCluster(m090ClusterC, "migration-090-unmapped")

	if _, err := pool.Exec(ctx,
		`INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`,
		m090Node, m090ClusterA); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// Guests live on clusters B and C too; nodes belong to A, and the vms FK
	// is on cluster_id, so one node row is enough for every guest here.
	if _, err := pool.Exec(ctx,
		`INSERT INTO veeam_servers (id, name, base_url, username, password_encrypted)
		 VALUES ($1, 'migration-090-vbr', 'https://vbr.invalid:9419', 'u', 'x')`,
		m090Server); err != nil {
		t.Fatalf("seed veeam server: %v", err)
	}
	// TWO platforms onto the SAME cluster A. A Veeam server can genuinely
	// protect one cluster through more than one connection, and joining the
	// platform table instead of testing EXISTS would count every guest on A
	// twice and fail the uniqueness test for no reason.
	for _, plat := range []uuid.UUID{uuid.New(), uuid.New()} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO veeam_platforms (veeam_server_id, platform_id, cluster_id) VALUES ($1, $2, $3)`,
			m090Server, plat, m090ClusterA); err != nil {
			t.Fatalf("seed platform: %v", err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO veeam_platforms (veeam_server_id, platform_id, cluster_id) VALUES ($1, $2, $3)`,
		m090Server, uuid.New(), m090ClusterB); err != nil {
		t.Fatalf("seed platform B: %v", err)
	}

	seedGuest := func(vmid int32, name, guestType string, cluster uuid.UUID) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO vms (id, cluster_id, node_id, vmid, name, type)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			uuid.New(), cluster, m090Node, vmid, name, guestType); err != nil {
			t.Fatalf("seed guest %d: %v", vmid, err)
		}
	}
	// Case differs from what Veeam reports — the capture really does mix
	// "vbr01-worker01" with "Vbr01-worker02".
	seedGuest(103, "Vbr01-worker01", "qemu", m090ClusterA)
	// Ambiguous: the same name on two clusters the server is mapped to.
	seedGuest(132, "vbr01-worker02", "qemu", m090ClusterA)
	seedGuest(232, "vbr01-worker02", "qemu", m090ClusterB)
	// On a cluster with no mapped platform.
	seedGuest(333, "vbr01-worker03", "qemu", m090ClusterC)
	// A container that shares the backup server's name.
	seedGuest(124, "vbr01.example.com", "lxc", m090ClusterA)
	seedGuest(150, "renameme", "qemu", m090ClusterA)
	// Exactly one unnamed guest, which is the shape that makes a blank
	// Veeam-side name resolve rather than merely being ambiguous.
	seedGuest(151, "", "qemu", m090ClusterA)

	type infraSpec struct{ key, role, name string }
	rows := []infraSpec{
		{"worker-hit", "worker", "vbr01-worker01"},
		{"worker-ambiguous", "worker", "Vbr01-worker02"},
		{"worker-unmapped-cluster", "worker", "vbr01-worker03"},
		{"worker-absent", "worker", "vbr01-worker99"},
		{"vbr-is-a-container", "backup_server", "vbr01.example.com"},
		{"renamed", "worker", "renameme"},
		// A blank name. vms.name is NOT NULL DEFAULT '', so without a guard
		// this matches every unnamed guest — and where exactly one exists,
		// silently excludes a real machine from coverage.
		{"blank-name", "worker", ""},
	}
	ids := make(map[string]uuid.UUID, len(rows))
	for _, r := range rows {
		id := uuid.New()
		ids[r.key] = id
		if _, err := pool.Exec(ctx,
			`INSERT INTO veeam_infrastructure (id, veeam_server_id, veeam_ref, role, name)
			 VALUES ($1, $2, $3, $4, $5)`,
			id, m090Server, uuid.New(), r.role, r.name); err != nil {
			t.Fatalf("seed infrastructure %s: %v", r.key, err)
		}
	}

	q := gen.New(pool)
	if _, err := q.ResolveVeeamInfrastructureGuests(ctx, m090Server); err != nil {
		t.Fatalf("ResolveVeeamInfrastructureGuests: %v", err)
	}

	assert := func(key string, wantCluster *uuid.UUID, wantVmid *int32) {
		t.Helper()
		var gotCluster *uuid.UUID
		var gotVmid *int32
		if err := pool.QueryRow(ctx,
			`SELECT cluster_id, vmid FROM veeam_infrastructure WHERE id = $1`,
			ids[key]).Scan(&gotCluster, &gotVmid); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		switch {
		case wantCluster == nil && gotCluster != nil:
			t.Errorf("%s: cluster_id = %v, want NULL", key, *gotCluster)
		case wantCluster != nil && gotCluster == nil:
			t.Errorf("%s: cluster_id = NULL, want %v", key, *wantCluster)
		case wantCluster != nil && *gotCluster != *wantCluster:
			t.Errorf("%s: cluster_id = %v, want %v", key, *gotCluster, *wantCluster)
		}
		switch {
		case wantVmid == nil && gotVmid != nil:
			t.Errorf("%s: vmid = %d, want NULL", key, *gotVmid)
		case wantVmid != nil && gotVmid == nil:
			t.Errorf("%s: vmid = NULL, want %d", key, *wantVmid)
		case wantVmid != nil && *gotVmid != *wantVmid:
			t.Errorf("%s: vmid = %d, want %d", key, *gotVmid, *wantVmid)
		}
	}
	i32 := func(n int32) *int32 { return &n }

	// Matched despite the case difference, and despite cluster A carrying two
	// platform mappings.
	assert("worker-hit", &m090ClusterA, i32(103))
	// The name identifies two guests, so it identifies neither. Excluding one
	// on a coin flip would hide a real guest's lack of backups.
	assert("worker-ambiguous", nil, nil)
	// Cluster C has no mapped platform, so its guests are out of scope.
	assert("worker-unmapped-cluster", nil, nil)
	assert("worker-absent", nil, nil)
	// Veeam cannot run on an LXC container, and a container that happens to
	// share the name is not the backup server.
	assert("vbr-is-a-container", nil, nil)
	assert("renamed", &m090ClusterA, i32(150))
	assert("blank-name", nil, nil)

	// A guest renamed out from under a resolved row must LOSE its resolution.
	// Nothing on the Veeam side changes when that happens, which is why the
	// resolve runs every pass rather than only when a row was just written.
	if _, err := pool.Exec(ctx,
		`UPDATE vms SET name = 'renamed-away' WHERE cluster_id = $1 AND vmid = 150`, m090ClusterA); err != nil {
		t.Fatalf("rename guest: %v", err)
	}
	if _, err := q.ResolveVeeamInfrastructureGuests(ctx, m090Server); err != nil {
		t.Fatalf("ResolveVeeamInfrastructureGuests after rename: %v", err)
	}
	assert("renamed", nil, nil)

	// Settled means settled: the statement runs every sync tick and fires an
	// updated_at trigger for each row it touches.
	changed, err := q.ResolveVeeamInfrastructureGuests(ctx, m090Server)
	if err != nil {
		t.Fatalf("ResolveVeeamInfrastructureGuests settled: %v", err)
	}
	if changed != 0 {
		t.Errorf("a settled resolution changed %d rows, want 0", changed)
	}

	// And the eligibility feed sees exactly the resolved guest.
	guests, err := q.ListVeeamInfrastructureGuestsForCluster(ctx, m090ClusterA)
	if err != nil {
		t.Fatalf("ListVeeamInfrastructureGuestsForCluster: %v", err)
	}
	if len(guests) != 1 || guests[0].Vmid != 103 {
		t.Errorf("eligibility feed = %+v, want only vmid 103", guests)
	}
}
