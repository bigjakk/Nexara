package db

import (
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// TestExpireUnstartedRollingUpdateJobs: a job created before the cut-off and
// never started is cancelled, with the reason, and stops blocking its cluster;
// a newer pending job, and a running or paused one however old, are left alone.
//
// Not parallel: it shares the migrated test database with the package.
func TestExpireUnstartedRollingUpdateJobs(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	orphaned := uuid.MustParse("138b0000-0000-4000-8000-00000000000a")
	busy := uuid.MustParse("138b0000-0000-4000-8000-00000000000b")
	defer seedRollingClusters(t, env, orphaned, busy)()
	ctx, q := env.Ctx, gen.New(env.Pool)

	job := func(cluster uuid.UUID, status string, age time.Duration) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := env.Pool.QueryRow(ctx, `INSERT INTO rolling_update_jobs (cluster_id, status, created_by, created_at)
			VALUES ($1, $2, $3, $4) RETURNING id`, cluster, status, rollingGuardUserID, time.Now().Add(-age)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	stale := job(orphaned, "pending", 25*time.Hour)
	kept := map[uuid.UUID]string{
		job(busy, "pending", time.Hour):    "pending",
		job(busy, "running", 25*time.Hour): "running",
		job(busy, "paused", 25*time.Hour):  "paused",
		job(busy, "failed", 25*time.Hour):  "failed", // its reason stays its own
	}

	expired, err := q.ExpireUnstartedRollingUpdateJobs(ctx, gen.ExpireUnstartedRollingUpdateJobsParams{
		Reason: "never started", CreatedBefore: time.Now().Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].ID != stale || expired[0].ClusterID != orphaned {
		t.Fatalf("expired %+v, want only the stale pending job", expired)
	}
	got, err := q.GetRollingUpdateJob(ctx, stale)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "cancelled" || got.FailureReason != "never started" || !got.CompletedAt.Valid || got.CleanupPending {
		t.Errorf("stale job = %s %q, completed_at set %v, cleanup_pending %v; want cancelled with the reason, "+
			"completed, nothing to clean up", got.Status, got.FailureReason, got.CompletedAt.Valid, got.CleanupPending)
	}
	if blocked, err := q.HasRunningJobForCluster(ctx, orphaned); err != nil || blocked {
		t.Errorf("the cluster is still blocked (%v)", err)
	}
	for id, want := range kept {
		if j, err := q.GetRollingUpdateJob(ctx, id); err != nil || j.Status != want {
			t.Errorf("a %s job became %s (%v)", want, j.Status, err)
		}
	}
}
