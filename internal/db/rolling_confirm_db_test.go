package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// rollingGuardUserID owns the jobs the rolling update DB tests seed. Fixed
// rather than random, so a run that dies before its cleanup leaves rows the
// next run deletes.
var rollingGuardUserID = uuid.MustParse("138a0000-0000-4000-8000-000000000001")

// seedRollingClusters gives the rolling update DB tests a user and clusters to
// hang jobs off. The returned purge deletes them, and with each cluster its
// jobs; defer it after env.Cleanup's defer (see setupMigration).
func seedRollingClusters(t *testing.T, env *migrationTestEnv, clusters ...uuid.UUID) (purge func()) {
	t.Helper()
	purge = func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = env.Pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, clusters)
		_, _ = env.Pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, rollingGuardUserID)
	}
	purge()
	if _, err := env.Pool.Exec(env.Ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, '')`,
		rollingGuardUserID, "rolling-guard-test@nexara.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	for i, id := range clusters {
		if _, err := env.Pool.Exec(env.Ctx, `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://192.0.2.1:8006', 'tok', 'enc')`, id, fmt.Sprintf("cluster%02d", i+1)); err != nil {
			t.Fatalf("seed cluster: %v", err)
		}
	}
	return purge
}

// TestConfirmNodeUpgrade_OnlyForARunningJob: the confirm a manual upgrade's
// reboot waits on matches a node only while its job is running, so a cancel
// that lands while ConfirmUpgrade checks the node is empty leaves nothing to
// reboot.
//
// Not parallel: it shares the migrated test database with the package.
func TestConfirmNodeUpgrade_OnlyForARunningJob(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	cluster := uuid.MustParse("138a0000-0000-4000-8000-00000000000c")
	defer seedRollingClusters(t, env, cluster)()
	ctx, q := env.Ctx, gen.New(env.Pool)

	for _, status := range []string{"running", "paused", "cancelled", "failed"} {
		var jobID, nodeID uuid.UUID
		if err := env.Pool.QueryRow(ctx, `INSERT INTO rolling_update_jobs (cluster_id, status, created_by)
			VALUES ($1, $2, $3) RETURNING id`, cluster, status, rollingGuardUserID).Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		if err := env.Pool.QueryRow(ctx, `INSERT INTO rolling_update_nodes (job_id, node_name, node_order, step)
			VALUES ($1, 'pve-01', 0, 'awaiting_upgrade') RETURNING id`, jobID).Scan(&nodeID); err != nil {
			t.Fatal(err)
		}
		rows, err := q.ConfirmNodeUpgrade(ctx, nodeID)
		if want := map[bool]int64{true: 1, false: 0}[status == "running"]; err != nil || rows != want {
			t.Errorf("%s job: confirmed %d rows (%v), want %d", status, rows, err, want)
		}
	}
}
