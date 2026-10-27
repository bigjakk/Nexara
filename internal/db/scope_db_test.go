package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixture ids, fixed rather than random so a run that dies before its cleanup
// leaves rows the next run deletes instead of accumulating them.
var (
	scopeTestUserID   = uuid.MustParse("5c09e000-0000-4000-8000-000000000001")
	scopeTestClusterA = uuid.MustParse("5c09e000-0000-4000-8000-00000000000a")
	scopeTestClusterB = uuid.MustParse("5c09e000-0000-4000-8000-00000000000b")
	scopeTestClusterR = uuid.MustParse("5c09e000-0000-4000-8000-00000000000c") // removed after its entry was written
	scopeTestClusterU = uuid.MustParse("5c09e000-0000-4000-8000-00000000000d") // never a cluster, or its name was lost

	scopeTestRowA       = uuid.MustParse("5c09e000-0000-4000-8000-0000000000a1")
	scopeTestRowB       = uuid.MustParse("5c09e000-0000-4000-8000-0000000000b1")
	scopeTestRowRemoved = uuid.MustParse("5c09e000-0000-4000-8000-0000000000c1")
	scopeTestRowUnknown = uuid.MustParse("5c09e000-0000-4000-8000-0000000000d1")
	scopeTestRowGlobal  = uuid.MustParse("5c09e000-0000-4000-8000-0000000000f1")
)

// TestAuditScope_NullClusterRowsAreGlobal executes the real generated queries
// against Postgres and pins the behaviour that TestScopeSQL_ScopedClausesExcludeNullCluster
// can only pin the shape of.
//
// audit_log.cluster_id is nullable, unlike task_history's. A NULL cluster_id
// marks a global entry — a settings change, a login — that only a holder of
// global view:audit may read, and AuditHandler's per-row guards enforce exactly
// that. The SQL has to agree, and it does so through three-valued logic rather
// than a predicate of its own: `cluster_id = ANY(array)` evaluates to NULL, not
// true, for a NULL cluster_id, so the row fails a scoped caller's WHERE clause.
//
// The three scopes below are the three states clusterAccess.ScopedIDs can
// produce, and each is asserted against all three scoped queries:
//
//	nil  → global view:audit → every row, NULL-cluster entries included
//	{A}  → scoped to A       → A's rows only, NEVER the NULL-cluster entry
//	{}   → no grant anywhere  → nothing
//
// An entry also outlives a removed cluster (000122), keeping an id that names no
// clusters row. Only global view:audit reads it, labelled with the name kept for
// it ("(removed cluster)" when none was: the id of a cluster that never was).
// {A, R, U} is a scoped caller whose assignments for clusters that are gone were
// not deleted with them: their ids are in the array, and their entries still
// must not appear.
//
// Skipped unless NEXARA_TEST_DB_URL names a throwaway database (CI sets it;
// see .github/workflows/ci.yml). It seeds and deletes its own rows and does not
// migrate the schema down.
func TestAuditScope_NullClusterRowsAreGlobal(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	migrateUp(t, m)

	// Deliberately a defer rather than t.Cleanup: t.Cleanup runs after the test
	// function's defers, by which point env.Cleanup has closed the pool and
	// cancelled env.Ctx, so the delete would silently no-op and leak rows into
	// the next run. Registering after env.Cleanup's defer makes it run first
	// (LIFO), while the pool is still open. The up-front call clears rows left
	// by a run that aborted before its defers.
	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		// Deleted by id: a removed cluster keeps its audit rows since 000122, and
		// the NULL-cluster row never had a parent. The clusters go before their
		// removed_clusters rows, which their removal writes.
		clusters := []uuid.UUID{scopeTestClusterA, scopeTestClusterB}
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE id = ANY($1)`,
			[]uuid.UUID{scopeTestRowA, scopeTestRowB, scopeTestRowRemoved, scopeTestRowUnknown, scopeTestRowGlobal})
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, clusters)
		_, _ = pool.Exec(pctx, `DELETE FROM removed_clusters WHERE id = ANY($1)`, append(clusters, scopeTestClusterR))
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, scopeTestUserID)
	}
	purge()
	defer purge()

	seedAuditScopeFixture(t, env)

	queries := gen.New(pool)

	// What each entry's cluster is called, and whether it is gone, to a reader that sees it.
	labels := map[uuid.UUID]clusterLabel{
		scopeTestRowA:       {"audit-scope-test-a", false},
		scopeTestRowRemoved: {"audit-scope-test-r (removed)", true},
		scopeTestRowUnknown: {"(removed cluster)", true},
		scopeTestRowGlobal:  {"", false},
	}

	tests := []struct {
		name    string
		scope   []uuid.UUID
		wantIDs []uuid.UUID
	}{
		{
			name:    "global view:audit (NULL scope) sees every row",
			scope:   nil,
			wantIDs: []uuid.UUID{scopeTestRowA, scopeTestRowB, scopeTestRowRemoved, scopeTestRowUnknown, scopeTestRowGlobal},
		},
		{
			// The load-bearing case: the global entry must NOT appear.
			name:    "scoped to cluster A sees A's rows and no global entry",
			scope:   []uuid.UUID{scopeTestClusterA},
			wantIDs: []uuid.UUID{scopeTestRowA},
		},
		{
			// The other load-bearing case: the removed cluster's id is in the
			// caller's scope, and its entry must still NOT appear.
			name:    "scoped to A and to clusters that are gone sees A's rows only",
			scope:   []uuid.UUID{scopeTestClusterA, scopeTestClusterR, scopeTestClusterU},
			wantIDs: []uuid.UUID{scopeTestRowA},
		},
		{
			name:    "no grants ('{}') sees nothing",
			scope:   []uuid.UUID{},
			wantIDs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := queries.ListAuditLogAdvanced(ctx, gen.ListAuditLogAdvancedParams{
				Limit:                100,
				Offset:               0,
				ResourceType:         pgtype.Text{String: scopeTestResourceType, Valid: true},
				AccessibleClusterIds: tt.scope,
			})
			if err != nil {
				t.Fatalf("ListAuditLogAdvanced: %v", err)
			}
			got := make([]uuid.UUID, 0, len(rows))
			for _, r := range rows {
				got = append(got, r.ID)
				assertClusterLabel(t, "ListAuditLogAdvanced", r.ID, labels, r.ClusterName, r.ClusterRemoved)
			}
			assertSameIDs(t, "ListAuditLogAdvanced", got, tt.wantIDs)
			assertNoGlobalEntry(t, "ListAuditLogAdvanced", got, tt.wantIDs)

			// Items and Total must agree, which is the whole point of the fix:
			// the count runs the same filters, so it has to see the same rows.
			total, err := queries.CountAuditLogAdvanced(ctx, gen.CountAuditLogAdvancedParams{
				ResourceType:         pgtype.Text{String: scopeTestResourceType, Valid: true},
				AccessibleClusterIds: tt.scope,
			})
			if err != nil {
				t.Fatalf("CountAuditLogAdvanced: %v", err)
			}
			if total != int64(len(tt.wantIDs)) {
				t.Errorf("CountAuditLogAdvanced = %d, want %d — Total disagrees with Items, "+
					"which is exactly the cross-cluster count leak this scope closes",
					total, len(tt.wantIDs))
			}

			// The dashboard feed reads the same rows through its own query. It
			// takes no resource_type filter and caps at 50 across the whole
			// table, so on a test database holding 50+ newer entries the
			// fixture legitimately falls off the end. Asserted as a subset
			// rather than as set equality: a missing fixture row there means
			// "crowded out", but a global entry reaching a scoped caller means
			// the scope failed, and only the second is this test's business.
			recent, err := queries.ListRecentAuditLogEnriched(ctx, tt.scope)
			if err != nil {
				t.Fatalf("ListRecentAuditLogEnriched: %v", err)
			}
			for _, r := range recent {
				if r.ResourceType != scopeTestResourceType {
					continue
				}
				if !containsID(tt.wantIDs, r.ID) {
					t.Errorf("ListRecentAuditLogEnriched returned %v to a caller scoped to %v, "+
						"which must not see it", r.ID, tt.scope)
				}
				assertClusterLabel(t, "ListRecentAuditLogEnriched", r.ID, labels, r.ClusterName, r.ClusterRemoved)
			}
		})
	}
}

// scopeTestResourceType tags this test's rows so the assertions can filter to
// them without assuming an empty audit_log.
const scopeTestResourceType = "audit-scope-test"

// seedAuditScopeFixture inserts one user, two clusters, and five audit rows:
// one in cluster A, one in cluster B, one in a cluster R that is then removed
// (so its entry names no clusters row), one naming an id that was never a
// cluster, and one global entry with a NULL cluster_id — the shapes the scope
// clause has to tell apart.
func seedAuditScopeFixture(t *testing.T, env *migrationTestEnv) {
	t.Helper()

	ctx, pool := env.Ctx, env.Pool

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, display_name)
		 VALUES ($1, 'audit-scope-test@example.invalid', 'x', 'Audit Scope Test')`,
		scopeTestUserID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	for _, c := range []struct {
		id   uuid.UUID
		name string
	}{
		{scopeTestClusterA, "audit-scope-test-a"},
		{scopeTestClusterB, "audit-scope-test-b"},
		{scopeTestClusterR, "audit-scope-test-r"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			 VALUES ($1, $2, 'https://cluster.invalid:8006', 'test@pam!t', 'x')`,
			c.id, c.name); err != nil {
			t.Fatalf("seed cluster %s: %v", c.name, err)
		}
	}

	for _, r := range []struct {
		id        uuid.UUID
		clusterID *uuid.UUID
		action    string
	}{
		{scopeTestRowA, &scopeTestClusterA, "cluster-a-entry"},
		{scopeTestRowB, &scopeTestClusterB, "cluster-b-entry"},
		{scopeTestRowRemoved, &scopeTestClusterR, "removed-cluster-entry"},
		{scopeTestRowUnknown, &scopeTestClusterU, "unknown-cluster-entry"},
		{scopeTestRowGlobal, nil, "global-entry"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_log (id, cluster_id, user_id, resource_type, resource_id, action)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			r.id, r.clusterID, scopeTestUserID, scopeTestResourceType, r.id.String(), r.action,
		); err != nil {
			t.Fatalf("seed audit row %s: %v", r.action, err)
		}
	}

	// The removal is what leaves the entry naming a cluster that is gone.
	if _, err := pool.Exec(ctx, `DELETE FROM clusters WHERE id = $1`, scopeTestClusterR); err != nil {
		t.Fatalf("remove cluster R: %v", err)
	}
	var kept int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE id = $1`, scopeTestRowRemoved).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("the entry of the removed cluster was deleted with it (kept=%d err=%v): the fixture is not exercising the case", kept, err)
	}
}

// clusterLabel is what a reader shows for an entry's cluster: the name, and
// whether the cluster is gone.
type clusterLabel struct {
	name    string
	removed bool
}

// assertClusterLabel holds a returned fixture entry to its label; other tests'
// entries in the same table are not this test's business.
func assertClusterLabel(t *testing.T, label string, id uuid.UUID, want map[uuid.UUID]clusterLabel, name string, removed bool) {
	t.Helper()

	if w, ok := want[id]; ok && (name != w.name || removed != w.removed) {
		t.Errorf("%s labels %v as %q (removed=%v), want %q (removed=%v)", label, id, name, removed, w.name, w.removed)
	}
}

// assertSameIDs compares an id set order-independently — ORDER BY created_at
// cannot separate rows inserted in the same statement batch.
func assertSameIDs(t *testing.T, label string, got, want []uuid.UUID) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("%s returned %d rows, want %d (got %v, want %v)", label, len(got), len(want), got, want)
		return
	}
	gotSet := make(map[uuid.UUID]bool, len(got))
	for _, id := range got {
		gotSet[id] = true
	}
	for _, id := range want {
		if !gotSet[id] {
			t.Errorf("%s is missing %v (got %v)", label, id, got)
		}
	}
}

// assertNoGlobalEntry is the assertion this test exists for: a scoped caller
// must never receive the NULL-cluster row.
func assertNoGlobalEntry(t *testing.T, label string, got, want []uuid.UUID) {
	t.Helper()

	if containsID(got, scopeTestRowGlobal) && !containsID(want, scopeTestRowGlobal) {
		t.Errorf("%s returned the NULL-cluster global entry to a scoped caller — "+
			"every cluster-scoped user can now read settings changes and logins", label)
	}
}

func containsID(ids []uuid.UUID, want uuid.UUID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
