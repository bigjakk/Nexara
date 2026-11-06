package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// DRS's back-off is computed from this query: only failures in a row count, so a
// completed migration clears the ones before it, and neither an old run of
// failures nor another cluster's may hold a guest back.
//
// Not parallel: it shares the migrated test database with the package.
func TestListDRSMigrationFailures(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	ctx, pool, q := env.Ctx, env.Pool, gen.New(env.Pool)

	cluster := uuid.MustParse("d5000000-0000-4000-8000-000000000001")
	other := uuid.MustParse("d5000000-0000-4000-8000-000000000002")
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{cluster, other})
	}
	purge()
	defer purge()
	for name, id := range map[string]uuid.UUID{"cluster01": cluster, "cluster02": other} {
		if _, err := pool.Exec(ctx, `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://192.0.2.1:8006', 'tok', 'enc')`, id, name); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().Truncate(time.Second)
	for _, row := range []struct {
		cluster uuid.UUID
		vmid    int
		status  string
		age     time.Duration
		noTime  bool // executed_at left NULL
	}{
		// Two failures after a completed migration, the first of them before it.
		{cluster, 101, "failed", 48 * time.Hour, false},
		{cluster, 101, "completed", 24 * time.Hour, false},
		{cluster, 101, "failed", time.Hour, false},
		{cluster, 101, "timeout", 30 * time.Minute, false},
		// Cleared by the migration that followed it.
		{cluster, 102, "failed", 2 * time.Hour, false},
		{cluster, 102, "completed", time.Hour, false},
		// Aged out.
		{cluster, 103, "failed", 8 * 24 * time.Hour, false},
		// Not failures.
		{cluster, 104, "pending", time.Hour, false},
		{cluster, 104, "advisory", time.Hour, false},
		{cluster, 104, "cancelled", time.Hour, false},
		{cluster, 104, "interrupted", time.Hour, false},
		// Another cluster's guest, and another cluster's migration of the same VMID.
		{other, 105, "failed", time.Hour, false},
		{other, 101, "completed", 10 * time.Minute, false},
		// A row with no execution time reads as its creation.
		{cluster, 106, "failed", 3 * time.Hour, true},
	} {
		// An attempt's row is created when it starts and timed when it ends.
		at := now.Add(-row.age)
		createdAt := at.Add(-time.Minute)
		var executedAt *time.Time
		if !row.noTime {
			executedAt = &at
		}
		if _, err := pool.Exec(ctx, `INSERT INTO drs_history
			(cluster_id, source_node, target_node, vm_id, reason, score_before, score_after, status, executed_at, created_at)
			VALUES ($1, 'pve-01', 'pve-02', $2, 'test', 0, 0, $3, $4, $5)`,
			row.cluster, row.vmid, row.status, executedAt, createdAt); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := q.ListDRSMigrationFailures(ctx, cluster)
	if err != nil {
		t.Fatal(err)
	}
	type failure struct {
		count int32
		last  time.Time
	}
	got := make(map[int32]failure, len(rows))
	for _, r := range rows {
		got[r.VmID] = failure{r.Failures, r.LastFailedAt.UTC()}
	}
	want := map[int32]failure{
		101: {2, now.Add(-30 * time.Minute).UTC()},
		106: {1, now.Add(-3*time.Hour - time.Minute).UTC()},
	}
	if len(got) != len(want) {
		t.Errorf("got guests %v, want %v", got, want)
	}
	for vmid, w := range want {
		if got[vmid] != w {
			t.Errorf("VM %d = %+v, want %+v", vmid, got[vmid], w)
		}
	}
}
