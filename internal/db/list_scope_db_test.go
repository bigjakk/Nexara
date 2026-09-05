package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixture ids, fixed rather than random so a run that dies before its cleanup
// leaves rows the next run deletes instead of accumulating them. Distinct from
// the audit fixture's so the two tests never collide.
var (
	listScopeUserID   = uuid.MustParse("715ce000-0000-4000-8000-000000000001")
	listScopeClusterA = uuid.MustParse("715ce000-0000-4000-8000-00000000000a")
	listScopeClusterB = uuid.MustParse("715ce000-0000-4000-8000-00000000000b")

	ruleA      = uuid.MustParse("715ce000-0000-4000-8000-0000000000a1")
	ruleB      = uuid.MustParse("715ce000-0000-4000-8000-0000000000b1")
	ruleGlobal = uuid.MustParse("715ce000-0000-4000-8000-0000000000f1")

	jobAtoB = uuid.MustParse("715ce000-0000-4000-8000-0000000000c1")
	jobBtoA = uuid.MustParse("715ce000-0000-4000-8000-0000000000c2")
	jobBtoB = uuid.MustParse("715ce000-0000-4000-8000-0000000000c3")
)

// TestListScope_AlertRulesAndMigrationJobs executes the two genuinely novel
// scope shapes against Postgres. The audit clause is covered by
// TestAuditScope_NullClusterRowsAreGlobal; these two are different:
//
//   - alert_rules.cluster_id is NULLABLE like audit_log's, and a NULL marks a
//     GLOBAL rule that only a holder of global view:alert may see. The clause
//     has to exclude it from a scoped caller through three-valued logic alone.
//   - migration_jobs has TWO NOT NULL cluster columns, and a job is visible if
//     EITHER end is accessible. That OR across two columns is the one shape in
//     this change set that no other query uses, so it is the one most worth
//     executing rather than reasoning about.
//
// Skipped unless NEXARA_TEST_DB_URL names a throwaway database (CI sets it).
// Seeds and deletes its own rows; never migrates the schema down.
func TestListScope_AlertRulesAndMigrationJobs(t *testing.T) {
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
		_, _ = pool.Exec(pctx, `DELETE FROM migration_jobs WHERE id = ANY($1)`,
			[]uuid.UUID{jobAtoB, jobBtoA, jobBtoB})
		// The NULL-cluster rule has no parent to cascade from, so delete by id.
		_, _ = pool.Exec(pctx, `DELETE FROM alert_rules WHERE id = ANY($1)`,
			[]uuid.UUID{ruleA, ruleB, ruleGlobal})
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`,
			[]uuid.UUID{listScopeClusterA, listScopeClusterB})
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, listScopeUserID)
	}
	purge()
	defer purge()

	seedListScopeFixture(t, env)

	queries := gen.New(pool)

	t.Run("alert rules exclude the NULL-cluster global rule from a scoped caller", func(t *testing.T) {
		tests := []struct {
			name    string
			scope   []uuid.UUID
			wantIDs []uuid.UUID
		}{
			{
				name:    "global view:alert (NULL scope) sees every rule",
				scope:   nil,
				wantIDs: []uuid.UUID{ruleA, ruleB, ruleGlobal},
			},
			{
				// The load-bearing case: ruleGlobal must NOT appear.
				name:    "scoped to cluster A sees A's rule only",
				scope:   []uuid.UUID{listScopeClusterA},
				wantIDs: []uuid.UUID{ruleA},
			},
			{
				name:    "no grants ('{}') sees nothing",
				scope:   []uuid.UUID{},
				wantIDs: nil,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rows, err := queries.ListAlertRules(ctx, gen.ListAlertRulesParams{
					Limit:                100,
					Offset:               0,
					AccessibleClusterIds: tt.scope,
				})
				if err != nil {
					t.Fatalf("ListAlertRules: %v", err)
				}
				got := fixtureIDs(rows, func(r gen.AlertRule) uuid.UUID { return r.ID },
					map[uuid.UUID]bool{ruleA: true, ruleB: true, ruleGlobal: true})
				assertSameIDs(t, "ListAlertRules", got, tt.wantIDs)

				if containsID(got, ruleGlobal) && !containsID(tt.wantIDs, ruleGlobal) {
					t.Errorf("ListAlertRules returned the NULL-cluster global rule to a caller "+
						"scoped to %v — every cluster-scoped user can now see global rules", tt.scope)
				}
			})
		}
	})

	t.Run("migration jobs are visible from either end", func(t *testing.T) {
		tests := []struct {
			name    string
			scope   []uuid.UUID
			wantIDs []uuid.UUID
		}{
			{
				name:    "global view:migration (NULL scope) sees every job",
				scope:   nil,
				wantIDs: []uuid.UUID{jobAtoB, jobBtoA, jobBtoB},
			},
			{
				// The either-end rule: A→B matches on source, B→A on target,
				// and B→B on neither.
				name:    "scoped to cluster A sees jobs touching A at either end",
				scope:   []uuid.UUID{listScopeClusterA},
				wantIDs: []uuid.UUID{jobAtoB, jobBtoA},
			},
			{
				name:    "scoped to cluster B sees every job, since B is an end of each",
				scope:   []uuid.UUID{listScopeClusterB},
				wantIDs: []uuid.UUID{jobAtoB, jobBtoA, jobBtoB},
			},
			{
				name:    "no grants ('{}') sees nothing",
				scope:   []uuid.UUID{},
				wantIDs: nil,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rows, err := queries.ListMigrationJobs(ctx, gen.ListMigrationJobsParams{
					Limit:                100,
					Offset:               0,
					AccessibleClusterIds: tt.scope,
				})
				if err != nil {
					t.Fatalf("ListMigrationJobs: %v", err)
				}
				got := fixtureIDs(rows, func(j gen.MigrationJob) uuid.UUID { return j.ID },
					map[uuid.UUID]bool{jobAtoB: true, jobBtoA: true, jobBtoB: true})
				assertSameIDs(t, "ListMigrationJobs", got, tt.wantIDs)
			})
		}
	})
}

// fixtureIDs pulls this test's own rows out of a result set, so the assertions
// hold on a database that already has data in these tables.
func fixtureIDs[T any](rows []T, id func(T) uuid.UUID, fixture map[uuid.UUID]bool) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if fixture[id(r)] {
			out = append(out, id(r))
		}
	}
	return out
}

// seedListScopeFixture inserts a user, two clusters, three alert rules (cluster
// A, cluster B, and a global rule with a NULL cluster_id) and three migration
// jobs (A→B, B→A, B→B) — the shapes the two scope clauses have to tell apart.
func seedListScopeFixture(t *testing.T, env *migrationTestEnv) {
	t.Helper()

	ctx, pool := env.Ctx, env.Pool

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, display_name)
		 VALUES ($1, 'list-scope-test@example.invalid', 'x', 'List Scope Test')`,
		listScopeUserID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	for _, c := range []struct {
		id   uuid.UUID
		name string
	}{
		{listScopeClusterA, "list-scope-test-a"},
		{listScopeClusterB, "list-scope-test-b"},
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
		name      string
	}{
		{ruleA, &listScopeClusterA, "list-scope-test-rule-a"},
		{ruleB, &listScopeClusterB, "list-scope-test-rule-b"},
		{ruleGlobal, nil, "list-scope-test-rule-global"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO alert_rules (id, name, metric, operator, threshold, cluster_id, created_by)
			 VALUES ($1, $2, 'cpu_usage', '>', 90, $3, $4)`,
			r.id, r.name, r.clusterID, listScopeUserID); err != nil {
			t.Fatalf("seed alert rule %s: %v", r.name, err)
		}
	}

	for _, j := range []struct {
		id     uuid.UUID
		source uuid.UUID
		target uuid.UUID
	}{
		{jobAtoB, listScopeClusterA, listScopeClusterB},
		{jobBtoA, listScopeClusterB, listScopeClusterA},
		{jobBtoB, listScopeClusterB, listScopeClusterB},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO migration_jobs (id, source_cluster_id, target_cluster_id, source_node, vmid)
			 VALUES ($1, $2, $3, 'pve1', 100)`,
			j.id, j.source, j.target); err != nil {
			t.Fatalf("seed migration job %v: %v", j.id, err)
		}
	}
}
