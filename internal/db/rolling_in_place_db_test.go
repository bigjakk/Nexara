package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixture ids, fixed rather than random so a run that dies before its cleanup
// leaves rows the next run deletes instead of accumulating them.
var (
	inPlaceUserID    = uuid.MustParse("102a0000-0000-4000-8000-000000000001")
	inPlaceClusterID = uuid.MustParse("102a0000-0000-4000-8000-00000000000c")
)

// TestMigration102_InPlaceDefaultsPreserveDrainingBehaviour is the upgrade-safety
// lock for migration 000102.
//
// The migration adds drain_guests to rolling_update_jobs and reboot_required to
// rolling_update_nodes. Both exist to let a job upgrade a node WITHOUT emptying
// it first — which is the only shape a single-node cluster can run, since the
// drain fails outright when there is no other online node to migrate to.
//
// The defaults are the whole safety argument for shipping this as an ordinary
// additive migration: every job that already exists, and every job created by a
// client that does not know the field, must keep draining exactly as before. A
// default that came out false would silently convert every scheduled rolling
// update on every existing install into an in-place one, which would reboot
// nodes under running guests. That is worth a test rather than a reading of the
// DDL.
//
// Skipped unless NEXARA_TEST_DB_URL names a throwaway database (CI sets it).
// Seeds and deletes its own rows; never migrates the schema down.
func TestMigration102_InPlaceDefaultsPreserveDrainingBehaviour(t *testing.T) {
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
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, inPlaceClusterID)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, inPlaceUserID)
	}
	purge()
	defer purge()

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, '')
		 ON CONFLICT (id) DO NOTHING`,
		inPlaceUserID, "rolling-in-place-test@nexara.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		 VALUES ($1, 'cluster01', 'https://invalid.local', 'tok', 'enc')
		 ON CONFLICT (id) DO NOTHING`, inPlaceClusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	queries := gen.New(pool)

	// A row written the way every pre-000102 row was written: without naming
	// the column at all.
	var legacyJobID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO rolling_update_jobs (cluster_id, created_by) VALUES ($1, $2) RETURNING id`,
		inPlaceClusterID, inPlaceUserID).Scan(&legacyJobID); err != nil {
		t.Fatalf("insert legacy-shaped job: %v", err)
	}
	legacy, err := queries.GetRollingUpdateJob(ctx, legacyJobID)
	if err != nil {
		t.Fatalf("GetRollingUpdateJob: %v", err)
	}
	if !legacy.DrainGuests {
		t.Fatal("drain_guests defaulted to false — every existing rolling update on " +
			"every install would silently become an in-place one, rebooting nodes " +
			"under their running guests")
	}

	// And the new shape round-trips.
	inPlace, err := queries.InsertRollingUpdateJob(ctx, gen.InsertRollingUpdateJobParams{
		ClusterID:       inPlaceClusterID,
		Parallelism:     1,
		PackageExcludes: []string{},
		HaPolicy:        "warn",
		HaWarnings:      []byte(`[]`),
		CreatedBy:       inPlaceUserID,
		DrainGuests:     false,
	})
	if err != nil {
		t.Fatalf("InsertRollingUpdateJob (in place): %v", err)
	}
	if inPlace.DrainGuests {
		t.Error("drain_guests = true on a job inserted with false — the flag is not " +
			"reaching the column, so every job would drain")
	}

	// reboot_required has the same argument: false is "nothing owed".
	node, err := queries.InsertRollingUpdateNode(ctx, gen.InsertRollingUpdateNodeParams{
		JobID:        inPlace.ID,
		NodeName:     "pve-01",
		NodeOrder:    0,
		PackagesJson: []byte(`[]`),
	})
	if err != nil {
		t.Fatalf("InsertRollingUpdateNode: %v", err)
	}
	if node.RebootRequired {
		t.Error("reboot_required defaulted to true; a fresh node owes no reboot")
	}

	// The terminal state an in-place upgrade reaches when apt asked for a
	// reboot it could not be given. It has to COMPLETE the node, not fail it:
	// the upgrade did land, and on an in-place job the running guests that
	// block the reboot are there because the operator asked for that.
	if err := queries.SetNodeUpgradeCompletedRebootPending(ctx, node.ID); err != nil {
		t.Fatalf("SetNodeUpgradeCompletedRebootPending: %v", err)
	}
	after, err := queries.GetRollingUpdateNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetRollingUpdateNode: %v", err)
	}
	if !after.RebootRequired {
		t.Error("reboot_required not set; the UI cannot tell the operator a reboot is owed")
	}
	if after.Step != "health_check" {
		t.Errorf("step = %q, want %q — health check with an empty guest snapshot is "+
			"what carries the node through to completed", after.Step, "health_check")
	}
	if after.FailureReason != "" {
		t.Errorf("failure_reason = %q, want empty; a deferred reboot is not a failure",
			after.FailureReason)
	}
}
