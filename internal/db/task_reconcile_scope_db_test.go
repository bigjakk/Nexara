package db

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/migration"
)

// A cross-cluster migration hands Proxmox the target cluster's decrypted API
// token inside the `target-endpoint` property string, and PVE may echo a
// rejected parameter back in the worker's die message. The migration
// orchestrator scrubs the secret out of the exit_status it writes; the
// collector's reconciler, which finalizes the same row through the same
// `upid = $1 AND status = 'running'` predicate, writes PVE's text raw and has
// no way to scrub it.
//
// Identically-scoped writers make that first-write-wins: the losing write
// updates zero rows and reports no error. So "the scrubbed value is in the
// column" is exactly the assertion that can pass by luck — it holds whenever
// the orchestrator happened to get there first, which at the shipped intervals
// (5s poll vs. a 30s sync tick) is most of the time.
//
// These two tests pin the property that removes the race instead of racing it:
// the collector's listing does not return the row at all, and the SQL literal
// that decides which rows those are is the same string the Go code means by
// TypeCrossCluster.

// crossClusterFilterRe matches the anti-join that withholds a cross-cluster
// migration's task row from ListRunningTaskHistoryByCluster. Deliberately
// insensitive to the alias and the whitespace, and deliberately NOT to the
// table: keying the exclusion on migration_jobs is the part that cannot be
// forgotten by a future dispatch path, because the job row carries the UPID
// before the task row exists.
var crossClusterFilterRe = regexp.MustCompile(`(?is)not\s+exists\s*\(\s*select\s+1\s+from\s+migration_jobs\b`)

// crossClusterJoinRe pins the column the anti-join correlates on. Without it
// the guard above passes for `mj.upid = th.node`, which parses, runs, matches
// nothing and leaves the exclusion silently off. The DB test catches that too,
// but only where NEXARA_TEST_DB_URL is set; this runs everywhere.
var crossClusterJoinRe = regexp.MustCompile(`(?i)\bmj\.upid\s*=\s*th\.upid\b`)

// crossClusterLiteralRe pulls the migration_type value the filter compares
// against, so the guard can check it against the Go constant rather than
// against a second hard-coded copy of the same string.
var crossClusterLiteralRe = regexp.MustCompile(`(?i)migration_type\s*=\s*'([^']*)'`)

// TestGuard_RunningTaskListNamesTheCrossClusterType keeps the SQL literal and
// migration.TypeCrossCluster from drifting apart.
//
// The two can only be compared from a test, since sqlc has no way to
// interpolate a Go constant into a query. A drift would be silent in the worst
// possible way: the filter would still parse, still run, and match nothing —
// the collector would quietly resume reconciling migration rows and writing
// their unscrubbed exit status, with the query still looking guarded.
//
// Needs no database, so it runs on every push rather than only where
// NEXARA_TEST_DB_URL is set.
func TestGuard_RunningTaskListNamesTheCrossClusterType(t *testing.T) {
	t.Parallel()

	const queryName = "tasks.sql.ListRunningTaskHistoryByCluster"
	body, ok := namedSQLQueries(t)[queryName]
	if !ok {
		t.Fatalf("%s not found in %s — if it was renamed, this guard must follow it",
			queryName, queriesGlob)
	}

	assertNamesCrossClusterType(t, queryName, body)

	if !crossClusterFilterRe.MatchString(body) {
		t.Fatalf("%s no longer excludes rows owned by a migration_jobs row.\n"+
			"That filter is what stops the collector reconciler persisting the "+
			"target cluster's API token out of a cross-cluster migration's die "+
			"message (view:task, and view:audit via the task join). Query body:\n%s",
			queryName, body)
	}

	if !crossClusterJoinRe.MatchString(body) {
		t.Errorf("%s does not correlate the anti-join on mj.upid = th.upid. "+
			"Correlating on any other column matches no rows, which turns the "+
			"exclusion off while leaving the query looking guarded. Query body:\n%s",
			queryName, body)
	}
}

// assertNamesCrossClusterType is the half of the guard that applies to BOTH
// migration_jobs-keyed credential guards, so neither can be left out of it.
//
// Shared rather than duplicated because the check is about one fact — the SQL
// literal equals migration.TypeCrossCluster — and each caller reports its own
// query name, so a drift in either is attributable. This is not the kind of
// second copy that masks a guard: it is one assertion applied to two subjects,
// not two implementations of one guard.
func assertNamesCrossClusterType(t *testing.T, queryName, body string) {
	t.Helper()

	m := crossClusterLiteralRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s compares no migration_type literal, so it selects EVERY migration "+
			"rather than only the credential-bearing ones. Query body:\n%s", queryName, body)
	}
	if m[1] != migration.TypeCrossCluster {
		t.Errorf("%s filters on migration_type = %q, but migration.TypeCrossCluster is %q — "+
			"the filter matches no rows and the guard is silently off",
			queryName, m[1], migration.TypeCrossCluster)
	}
}

// TestGuard_IngestDedupNamesTheCrossClusterType is the same drift guard for the
// sibling credential boundary: ListCrossClusterMigrationUPIDs, which stops
// ingestTask INSERTing PVE's die message for a cross-cluster migration it has
// not yet recorded.
//
// It needs its own test rather than a shared table because the failure it
// describes is a different one — and because the collector-side test that
// exercises this guard is mock-driven, so nothing else anywhere compares this
// query's literal to the Go constant. Without this, typing 'cross_cluster' here
// leaves every test green and the ingest door open.
func TestGuard_IngestDedupNamesTheCrossClusterType(t *testing.T) {
	t.Parallel()

	const queryName = "proxmox_task_sync.sql.ListCrossClusterMigrationUPIDs"
	body, ok := namedSQLQueries(t)[queryName]
	if !ok {
		t.Fatalf("%s not found in %s — if it was renamed, this guard must follow it; "+
			"if it was deleted, ingestTask persists the target cluster's API token "+
			"whenever a sync tick beats the orchestrator's own inserts",
			queryName, queriesGlob)
	}

	assertNamesCrossClusterType(t, queryName, body)

	if !strings.Contains(strings.ToLower(body), "from migration_jobs") {
		t.Errorf("%s no longer reads migration_jobs, which is the only table that "+
			"identifies a cross-cluster migration's UPID before its task row "+
			"exists. Query body:\n%s", queryName, body)
	}
}

// Fixture ids, fixed rather than random so a run that dies before its cleanup
// leaves rows the next run deletes instead of accumulating them.
var (
	reconcileScopeUserID      = uuid.MustParse("103b0000-0000-4000-8000-000000000001")
	reconcileScopeSourceClust = uuid.MustParse("103b0000-0000-4000-8000-00000000000c")
	reconcileScopeTargetClust = uuid.MustParse("103b0000-0000-4000-8000-00000000000d")
)

// TestListRunningTaskHistoryByCluster_WithholdsCrossClusterMigrations proves the
// exclusion against the real schema and the real generated query, because that
// is the only place it exists — there is no second copy in Go to unit-test, and
// a second copy is exactly what would make neither killable.
//
// Five rows, the same shape apart from the thing under test. Each is withheld
// or returned by exactly ONE predicate, so a failure names the predicate that
// broke:
//
//	cross     source cluster, running, cross-cluster job  → withheld
//	intra     source cluster, running, intra-cluster job  → returned
//	plain     source cluster, running, no job at all      → returned
//	finished  source cluster, completed, no job at all    → withheld
//	offsite   TARGET cluster, running, no job at all      → withheld
//
// The intra and plain rows are not padding. Without them the test would pass
// just as well against a query that returned nothing, and "returns nothing" is
// the shape a mistyped literal or a botched anti-join actually produces.
//
// DO NOT give the finished row a migration job. It has none on purpose: it is
// the only thing pinning the pre-existing status filter, and it can only do
// that while the anti-join has no opinion about it. The first draft gave it a
// cross-cluster job, which made the assertion vacuous — the anti-join withheld
// the row whatever the status filter did, so deleting `AND th.status =
// 'running'` from the query left this test green. A fixture that satisfies two
// predicates at once cannot discriminate between them; that is a recorded
// defect class here, and this is where it would come back.
//
// The offsite row is the same defence for the cluster_id filter, which nothing
// else here would notice the loss of: every other row sits on the cluster being
// queried.
//
// Skipped unless NEXARA_TEST_DB_URL names a throwaway database (CI sets it).
// Seeds and deletes its own rows; never migrates the schema down.
func TestListRunningTaskHistoryByCluster_WithholdsCrossClusterMigrations(t *testing.T) {
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
		// task_history and migration_jobs both cascade from clusters.
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`,
			[]uuid.UUID{reconcileScopeSourceClust, reconcileScopeTargetClust})
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, reconcileScopeUserID)
	}
	purge()
	defer purge()

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, '')
		 ON CONFLICT (id) DO NOTHING`,
		reconcileScopeUserID, "task-reconcile-scope-test@nexara.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	for _, c := range []struct {
		id   uuid.UUID
		name string
	}{
		{reconcileScopeSourceClust, "cluster01"},
		{reconcileScopeTargetClust, "cluster02"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			 VALUES ($1, $2, 'https://invalid.local', 'tok', 'enc')
			 ON CONFLICT (id) DO NOTHING`, c.id, c.name); err != nil {
			t.Fatalf("seed cluster %s: %v", c.name, err)
		}
	}

	const (
		crossUPID    = "UPID:pve-01:00001001:00000001:66000001:qmigrate:100:nexara@pve!api:"
		intraUPID    = "UPID:pve-01:00001002:00000002:66000002:qmigrate:101:nexara@pve!api:"
		plainUPID    = "UPID:pve-01:00001003:00000003:66000003:qmmove:102:nexara@pve!api:"
		finishedUPID = "UPID:pve-01:00001004:00000004:66000004:qmigrate:103:nexara@pve!api:"
		offsiteUPID  = "UPID:pve-03:00001005:00000005:66000005:qmmove:104:nexara@pve!api:"
	)

	// The migration jobs come first, in the order production writes them:
	// SetMigrationJobStarted records the UPID on the job row before
	// startAndPollMigration inserts the task_history row, which is what makes
	// the anti-join safe to key on migration_jobs.upid.
	for _, j := range []struct {
		upid    string
		jobType string
	}{
		{crossUPID, migration.TypeCrossCluster},
		{intraUPID, migration.TypeIntraCluster},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO migration_jobs
			     (source_cluster_id, target_cluster_id, source_node, target_node,
			      vmid, migration_type, upid, status)
			 VALUES ($1, $2, 'pve-01', 'pve-02', 100, $3, $4, 'migrating')`,
			reconcileScopeSourceClust, reconcileScopeTargetClust, j.jobType, j.upid); err != nil {
			t.Fatalf("seed %s migration job: %v", j.jobType, err)
		}
	}

	for _, r := range []struct {
		upid    string
		status  string
		cluster uuid.UUID
	}{
		{crossUPID, "running", reconcileScopeSourceClust},
		{intraUPID, "running", reconcileScopeSourceClust},
		{plainUPID, "running", reconcileScopeSourceClust},
		{finishedUPID, "completed", reconcileScopeSourceClust},
		{offsiteUPID, "running", reconcileScopeTargetClust},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO task_history (cluster_id, user_id, upid, status, node, task_type)
			 VALUES ($1, $2, $3, $4, 'pve-01', 'qmigrate')`,
			r.cluster, reconcileScopeUserID, r.upid, r.status); err != nil {
			t.Fatalf("seed task_history %s: %v", r.upid, err)
		}
	}

	queries := gen.New(pool)
	rows, err := queries.ListRunningTaskHistoryByCluster(ctx, reconcileScopeSourceClust)
	if err != nil {
		t.Fatalf("ListRunningTaskHistoryByCluster: %v", err)
	}

	got := make(map[string]bool, len(rows))
	for _, row := range rows {
		got[row.Upid] = true
	}

	if got[crossUPID] {
		t.Error("the collector reconciler was handed a cross-cluster migration's task row; " +
			"it would poll PVE and write the unscrubbed die message into exit_status, " +
			"and the orchestrator's scrubbed write would then update nothing")
	}
	if got[finishedUPID] {
		t.Error("a completed row was returned; the status='running' filter has stopped " +
			"carrying its own weight and the reconciler would re-finalize terminal rows")
	}
	if !got[intraUPID] {
		t.Error("an INTRA-cluster migration's task row was withheld. Its die message carries no " +
			"credential, and the collector is the only thing that finalizes it if the " +
			"orchestrator dies — the exclusion is over-broad")
	}
	if !got[plainUPID] {
		t.Error("a task row belonging to no migration job was withheld — the exclusion is over-broad")
	}
	if got[offsiteUPID] {
		t.Error("a row on ANOTHER cluster was returned; the cluster_id filter is gone and " +
			"each cluster's sync tick would reconcile every other cluster's tasks")
	}

	// The sibling guard, against the same fixtures and the same real schema.
	//
	// ListCrossClusterMigrationUPIDs is what stops ingestTask INSERTing PVE's
	// die message for a migration nothing has recorded yet. Its collector-side
	// test is mock-driven, so this is the only place its SQL meets a database —
	// and these three rows are already exactly the input it needs: one UPID on
	// a cross-cluster job, one on an intra-cluster job, one on no job at all.
	// Assertions name the query, so a failure here is not mistaken for the
	// reconcile listing's.
	mjUPIDs, err := queries.ListCrossClusterMigrationUPIDs(ctx,
		[]string{crossUPID, intraUPID, plainUPID})
	if err != nil {
		t.Fatalf("ListCrossClusterMigrationUPIDs: %v", err)
	}
	withheld := make(map[string]bool, len(mjUPIDs))
	for _, u := range mjUPIDs {
		withheld[u] = true
	}
	if !withheld[crossUPID] {
		t.Error("ListCrossClusterMigrationUPIDs did not return the cross-cluster " +
			"migration's UPID, so ingestTask would record the task itself and " +
			"persist PVE's unscrubbed die message")
	}
	if withheld[intraUPID] {
		t.Error("ListCrossClusterMigrationUPIDs returned an INTRA-cluster migration's " +
			"UPID — external ingest would stop recording ordinary migrations")
	}
	if withheld[plainUPID] {
		t.Error("ListCrossClusterMigrationUPIDs returned a UPID with no migration job at all")
	}
}
