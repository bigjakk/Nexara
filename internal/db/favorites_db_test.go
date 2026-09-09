package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixture ids, fixed rather than random so a run that dies before its cleanup
// leaves rows the next run deletes instead of accumulating them.
var (
	favUserID    = uuid.MustParse("fa000000-0000-4000-8000-000000000001")
	favClusterID = uuid.MustParse("fa000000-0000-4000-8000-00000000000c")
	favNodeID    = uuid.MustParse("fa000000-0000-4000-8000-0000000000b1")
	favVMOldID   = uuid.MustParse("fa000000-0000-4000-8000-0000000000a1")
	favVMNewID   = uuid.MustParse("fa000000-0000-4000-8000-0000000000a2")
)

const (
	favNodeName = "pve-01"
	favVMID     = 101
)

// TestFavorites_SurviveVMRowChurn is the reason the table is keyed the way it
// is, executed rather than reasoned about.
//
// The collector prunes and re-inserts a vms row whenever Proxmox transiently
// stops listing a guest — most often during a live migration — and the
// re-insert draws a fresh gen_random_uuid(). Migration 000068 had to unpick
// exactly this for folder memberships, which were surrogate-keyed with an
// ON DELETE CASCADE and silently vanished. user_favorites keys on
// (cluster_id, vmid) with no reference to vms at all, so:
//
//  1. deleting the vms row must NOT delete the favorite, and
//  2. the listing must re-resolve target_id to the NEW row id, so the sidebar
//     link keeps working rather than 404ing.
//
// A test that only asserted (1) would pass against a design that stored a stale
// target_id, which is why (2) is asserted on the actual generated query.
//
// Skipped unless NEXARA_TEST_DB_URL names a throwaway database (CI sets it).
// Seeds and deletes its own rows; never migrates the schema down.
func TestFavorites_SurviveVMRowChurn(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	migrateUp(t, m)

	// Deferred rather than t.Cleanup: t.Cleanup runs after the test function's
	// defers, by which point env.Cleanup has closed the pool. Registering after
	// env.Cleanup's defer makes this run first (LIFO), while it is still open.
	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, favClusterID)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, favUserID)
	}
	purge()
	defer purge()

	seedFavoritesFixture(t, env)
	queries := gen.New(pool)

	listVMs := func(t *testing.T) []gen.ListFavoriteVMsRow {
		t.Helper()
		rows, err := queries.ListFavoriteVMs(ctx, gen.ListFavoriteVMsParams{UserID: favUserID})
		if err != nil {
			t.Fatalf("ListFavoriteVMs: %v", err)
		}
		return rows
	}

	// Baseline: the guest favorite resolves to the original row id.
	rows := listVMs(t)
	if len(rows) != 1 {
		t.Fatalf("baseline: got %d guest favorites, want 1", len(rows))
	}
	if rows[0].VmID != favVMOldID {
		t.Fatalf("baseline target id = %v, want %v", rows[0].VmID, favVMOldID)
	}

	// THE CHURN. Delete the vms row exactly as the collector's stale prune does.
	if _, err := pool.Exec(ctx, `DELETE FROM vms WHERE id = $1`, favVMOldID); err != nil {
		t.Fatalf("churn: delete vms row: %v", err)
	}

	// (1) The row itself must still be there. Read the table directly — the
	// listing deliberately drops unresolvable favorites, so it cannot tell
	// "still stored, currently invisible" from "deleted".
	var stored int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_favorites
		  WHERE user_id = $1 AND cluster_id = $2 AND resource_type = 'vm' AND resource_ref = '101'`,
		favUserID, favClusterID).Scan(&stored); err != nil {
		t.Fatalf("probe stored favorite: %v", err)
	}
	if stored != 1 {
		t.Fatalf("favorite did NOT survive vms-row deletion (got %d rows, want 1) — "+
			"something has coupled its lifetime back to vms.id", stored)
	}

	// While unresolvable it is correctly absent from the listing.
	if got := listVMs(t); len(got) != 0 {
		t.Fatalf("listing returned %d unresolvable favorites, want 0", len(got))
	}

	// (2) Re-insert the same guest with a brand-new surrogate id, as the next
	// collector sync does, and the star must come back pointing at the new row.
	if _, err := pool.Exec(ctx,
		`INSERT INTO vms (id, cluster_id, node_id, vmid, name, type)
		 VALUES ($1, $2, $3, $4, 'linux01', 'qemu')`,
		favVMNewID, favClusterID, favNodeID, favVMID); err != nil {
		t.Fatalf("churn: reinsert vms row with new id: %v", err)
	}

	rows = listVMs(t)
	if len(rows) != 1 {
		t.Fatalf("after re-insert: got %d guest favorites, want 1 — the favorite did not come back", len(rows))
	}
	if rows[0].VmID != favVMNewID {
		t.Fatalf("after re-insert: target id = %v, want the NEW row id %v — "+
			"the listing is handing the SPA a stale id that will 404", rows[0].VmID, favVMNewID)
	}
	if rows[0].NodeName != favNodeName {
		t.Fatalf("after re-insert: node_name = %q, want %q", rows[0].NodeName, favNodeName)
	}
}

// TestFavorites_CountsOnlyResolvableRows locks the cap to what the user can
// actually see and remove.
//
// The listing only returns favorites whose target resolves, and the only unstar
// affordance is the context menu on the target's own row. So a favorite whose
// guest has been destroyed is invisible AND unremovable through the UI. If the
// cap counted those, an install could reach "Favorite limit reached" with a
// near-empty sidebar and no way to clear it.
func TestFavorites_CountsOnlyResolvableRows(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	migrateUp(t, m)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, favClusterID)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, favUserID)
	}
	purge()
	defer purge()

	seedFavoritesFixture(t, env)
	queries := gen.New(pool)

	// The fixture stars one cluster, one node and one guest.
	count, err := queries.CountResolvableFavorites(ctx, favUserID)
	if err != nil {
		t.Fatalf("CountResolvableFavorites: %v", err)
	}
	if count != 3 {
		t.Fatalf("baseline count = %d, want 3", count)
	}

	// Strand every non-cluster favorite: destroy the guest and rename the node,
	// the two ways a target stops resolving without the row going away.
	if _, err := pool.Exec(ctx, `DELETE FROM vms WHERE id = $1`, favVMOldID); err != nil {
		t.Fatalf("destroy guest: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE nodes SET name = 'pve-02' WHERE id = $1`, favNodeID); err != nil {
		t.Fatalf("rename node: %v", err)
	}

	count, err = queries.CountResolvableFavorites(ctx, favUserID)
	if err != nil {
		t.Fatalf("CountResolvableFavorites after stranding: %v", err)
	}
	if count != 1 {
		t.Fatalf("count after stranding = %d, want 1 (the cluster favorite alone) — "+
			"stranded rows are being counted toward a cap the user cannot clear", count)
	}

	// The stranded rows are still stored, and RemoveFavorite can still reach
	// them: they are hidden, not lost.
	var stored int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_favorites WHERE user_id = $1`, favUserID).Scan(&stored); err != nil {
		t.Fatalf("probe stored favorites: %v", err)
	}
	if stored != 3 {
		t.Fatalf("stored rows = %d, want 3 — something is pruning unresolvable "+
			"favorites, which would permanently unstar a guest for migrating", stored)
	}
}

// TestFavorites_TargetExistsGatesTheInsert covers what actually bounds the
// table.
//
// CountResolvableFavorites deliberately ignores favorites whose target has gone,
// so that a destroyed guest cannot lock a user out of their own cap. The cost of
// that choice is that a row which NEVER resolves would never be counted either —
// so refs matching nothing could be inserted without limit, and the cap would
// bound nothing at all. AddFavorite closes it by refusing a target that is not
// there, which is this query.
func TestFavorites_TargetExistsGatesTheInsert(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	migrateUp(t, m)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, favClusterID)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, favUserID)
	}
	purge()
	defer purge()

	seedFavoritesFixture(t, env)
	queries := gen.New(pool)

	tests := []struct {
		name     string
		typ      string
		nodeName string
		vmid     int32
		want     bool
	}{
		{"the seeded cluster", "cluster", "", 0, true},
		{"the seeded node", "node", favNodeName, 0, true},
		{"the seeded guest", "vm", "", favVMID, true},
		// The abuse case: a ref that matches nothing. Each of these would be a
		// distinct primary key, so without the gate they could be minted
		// endlessly and never counted.
		{"a node that does not exist", "node", "pve-99", 0, false},
		{"a guest that does not exist", "vm", "", 4242, false},
		// The branches must not leak into each other: a node named for the
		// guest's VMID is still not that guest.
		{"a node named like the guest's vmid", "node", "101", 0, false},
		{"a guest numbered like nothing, with the node name set", "vm", favNodeName, 4242, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := queries.FavoriteTargetExists(ctx, gen.FavoriteTargetExistsParams{
				ResourceType: tt.typ,
				ClusterID:    favClusterID,
				NodeName:     tt.nodeName,
				Vmid:         tt.vmid,
			})
			if err != nil {
				t.Fatalf("FavoriteTargetExists: %v", err)
			}
			if got != tt.want {
				t.Errorf("exists = %v, want %v", got, tt.want)
			}
		})
	}

	// A cluster the caller names but that is not there. The FK would catch this
	// one on insert anyway; the gate turns it into a 404 instead of a 500.
	got, err := queries.FavoriteTargetExists(ctx, gen.FavoriteTargetExistsParams{
		ResourceType: "cluster",
		ClusterID:    uuid.MustParse("fa000000-0000-4000-8000-0000000000ff"),
	})
	if err != nil {
		t.Fatalf("FavoriteTargetExists (absent cluster): %v", err)
	}
	if got {
		t.Error("exists = true for a cluster that is not in the table")
	}
}

// TestFavorites_CheckConstraintsRejectMalformedRows executes the three CHECKs
// in 000101 against Postgres.
//
// Each one is load-bearing for something the Go layer alone does not guarantee:
// the type list, the cluster/empty-ref biconditional that stops a cluster being
// starred an unbounded number of times, and the 9-digit bound that keeps
// ListFavoriteVMs' ::int from overflowing.
func TestFavorites_CheckConstraintsRejectMalformedRows(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	migrateUp(t, m)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, favClusterID)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, favUserID)
	}
	purge()
	defer purge()

	seedFavoritesFixture(t, env)

	tests := []struct {
		name       string
		typ, ref   string
		constraint string
	}{
		{"unknown type", "storage", "store01", "user_favorites_type_check"},
		{"cluster with a ref", "cluster", "junk", "user_favorites_cluster_ref_check"},
		{"node without a ref", "node", "", "user_favorites_cluster_ref_check"},
		{"guest ref that is a name", "vm", "pve-01", "user_favorites_vm_ref_check"},
		// 11 digits satisfies "^[0-9]+$" and overflows int. One such row would
		// make ListFavoriteVMs raise "value out of range" for this user forever.
		{"guest ref that overflows int", "vm", "99999999999", "user_favorites_vm_ref_check"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(ctx,
				`INSERT INTO user_favorites (user_id, cluster_id, resource_type, resource_ref)
				 VALUES ($1, $2, $3, $4)`,
				favUserID, favClusterID, tt.typ, tt.ref)
			if err == nil {
				t.Fatalf("INSERT (%q, %q) succeeded; want a %s violation", tt.typ, tt.ref, tt.constraint)
			}
			if !strings.Contains(err.Error(), tt.constraint) {
				t.Fatalf("INSERT (%q, %q) failed with %v; want a %s violation",
					tt.typ, tt.ref, err, tt.constraint)
			}
		})
	}
}

// seedFavoritesFixture creates one cluster, one node and one guest, and stars
// all three for favUserID.
func seedFavoritesFixture(t *testing.T, env *migrationTestEnv) {
	t.Helper()
	ctx, pool := env.Ctx, env.Pool

	exec := func(what, sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}

	exec("user", `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, '')
	              ON CONFLICT (id) DO NOTHING`, favUserID, "favorites-db-test@nexara.test")
	exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
	                 VALUES ($1, 'cluster01', 'https://invalid.local', 'tok', 'enc')
	                 ON CONFLICT (id) DO NOTHING`, favClusterID)
	exec("node", `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, $3)
	              ON CONFLICT (id) DO NOTHING`, favNodeID, favClusterID, favNodeName)
	exec("vm", `INSERT INTO vms (id, cluster_id, node_id, vmid, name, type)
	            VALUES ($1, $2, $3, $4, 'linux01', 'qemu')`,
		favVMOldID, favClusterID, favNodeID, favVMID)

	exec("favorites", `INSERT INTO user_favorites (user_id, cluster_id, resource_type, resource_ref)
	                   VALUES ($1, $2, 'cluster', ''), ($1, $2, 'node', $3), ($1, $2, 'vm', '101')`,
		favUserID, favClusterID, favNodeName)
}
