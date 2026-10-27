package db

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// A PBS's stale rows are pruned by what its listing showed: what a listing
// showed but failed to write stays, a datastore that could not be listed keeps
// its rows for a day and no longer, and the rest that is stale — a datastore
// no longer listed included — goes. Nil lists spare nothing.
//
// Not parallel: it shares the migrated test database with the package.
func TestDeleteStalePBSRowsKeepWhatTheListingShowed(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	ctx, pool, q := env.Ctx, env.Pool, gen.New(env.Pool)
	server := uuid.MustParse("128b0000-0000-4000-8000-0000000000b1")
	purge := func() { _, _ = pool.Exec(context.Background(), `DELETE FROM pbs_servers WHERE id = $1`, server) }
	purge()
	defer purge()
	// backup_time names each row: 2 was seen just now; datastore02 cannot be
	// listed, 6 a day and more ago; datastore03 is no longer listed; 5 was
	// listed but failed to write.
	if _, err := pool.Exec(ctx, `WITH s AS (INSERT INTO pbs_servers (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, 'pbs01', 'https://192.0.2.20:8007', 'tok', 'enc') RETURNING id),
		snaps AS (INSERT INTO pbs_snapshots (pbs_server_id, datastore, backup_type, backup_id, backup_time, last_seen_at)
			SELECT s.id, v.store, 'vm', '100', v.at, now() - v.age::interval
			FROM s, (VALUES ('datastore01', 1, '1 hour'), ('datastore01', 2, '0 s'), ('datastore02', 3, '1 hour'),
				('datastore03', 4, '1 hour'), ('datastore01', 5, '1 hour'), ('datastore02', 6, '30 hours'))
				AS v(store, at, age))
		INSERT INTO pbs_sync_jobs (pbs_server_id, job_id, store, last_seen_at)
		SELECT s.id, j, 'datastore01', now() - interval '1 hour' FROM s, unnest(ARRAY['s-01', 's-02']) AS j`, server); err != nil {
		t.Fatal(err)
	}
	left := func(sql string) string {
		t.Helper()
		var got []string
		if err := pool.QueryRow(ctx, sql, server).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(got)
	}
	snapshots := `SELECT coalesce(array_agg(backup_time::text ORDER BY backup_time), '{}') FROM pbs_snapshots WHERE pbs_server_id = $1`
	for _, step := range []struct {
		kept       string
		unlistable []string
		want       string
	}{
		{`[{"datastore":"datastore01","backup_type":"vm","backup_id":"100","backup_time":5}]`, []string{"datastore02"}, "[2 3 5]"},
		{"", nil, "[2]"},
	} {
		var kept json.RawMessage
		if step.kept != "" {
			kept = json.RawMessage(step.kept)
		}
		if err := q.DeleteStalePBSSnapshots(ctx, gen.DeleteStalePBSSnapshotsParams{
			PbsServerID: server, GraceSeconds: 600, Kept: kept, Unlistable: step.unlistable,
		}); err != nil {
			t.Fatal(err)
		}
		if got := left(snapshots); got != step.want {
			t.Fatalf("keeping %s and %v: left %s, want %s", step.kept, step.unlistable, got, step.want)
		}
	}
	if err := q.DeleteStalePBSSyncJobs(ctx, gen.DeleteStalePBSSyncJobsParams{
		PbsServerID: server, Kept: []string{"s-01"}, GraceSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}
	if got := left(`SELECT coalesce(array_agg(job_id ORDER BY job_id), '{}') FROM pbs_sync_jobs WHERE pbs_server_id = $1`); got != "[s-01]" {
		t.Fatalf("sync jobs left %s, want the one kept", got)
	}
}
