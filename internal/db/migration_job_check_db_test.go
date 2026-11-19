package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var jobCheckClusterID = uuid.MustParse("b5000000-0000-4000-8000-00000000000d")

// A pre-flight check wrote "checking" and then its result over any status: it
// put a cancelled job back to pending and wrote pending over a migrating one.
// It starts only on a job not yet started and saves, or gives up, only over
// its own status. Not parallel: it writes to the shared schema. Skipped
// without NEXARA_TEST_DB_URL.
func TestMigrationJobCheck_WritesOnlyOverItsOwnStatus(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, jobCheckClusterID) // its jobs cascade
	}
	purge()
	defer purge()
	if _, err := pool.Exec(ctx, `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
	        VALUES ($1, 'cluster01', 'https://invalid.local', 'tok', 'enc')`, jobCheckClusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	q := gen.New(pool)
	steps := []struct {
		name  string
		from  func(status string) bool // the statuses it writes over
		to    string
		write func(id uuid.UUID) (int64, error)
	}{
		{"begin", func(s string) bool { return s == "pending" || s == "checking" }, "checking",
			func(id uuid.UUID) (int64, error) { return q.BeginMigrationJobCheck(ctx, id) }},
		{"save", func(s string) bool { return s == "checking" }, "failed",
			func(id uuid.UUID) (int64, error) {
				return q.SaveMigrationJobChecks(ctx, gen.SaveMigrationJobChecksParams{
					ID: id, CheckResults: []byte(`{"passed":false}`), Status: "failed"})
			}},
		{"abandon", func(s string) bool { return s == "checking" }, "pending",
			func(id uuid.UUID) (int64, error) { return -1, q.AbandonMigrationJobCheck(ctx, id) }},
	}
	for _, status := range []string{"pending", "checking", "migrating", "completed", "failed", "cancelled"} {
		for _, step := range steps {
			var id uuid.UUID
			if err := pool.QueryRow(ctx, `INSERT INTO migration_jobs (source_cluster_id, target_cluster_id,
			        source_node, vmid, status) VALUES ($1, $1, 'pve-01', 100, $2) RETURNING id`,
				jobCheckClusterID, status).Scan(&id); err != nil {
				t.Fatalf("seed a %s job: %v", status, err)
			}
			rows, err := step.write(id)
			if err != nil {
				t.Fatalf("%s over %s: %v", step.name, status, err)
			}
			want, wantRows := status, int64(0)
			if step.from(status) {
				want, wantRows = step.to, 1
			}
			got, err := q.GetMigrationJob(ctx, id)
			if err != nil || got.Status != want || (rows != -1 && rows != wantRows) {
				t.Errorf("%s over a %s job: status %q, %d rows (err %v); want %q, %d", step.name, status,
					got.Status, rows, err, want, wantRows)
			}
		}
	}
}
