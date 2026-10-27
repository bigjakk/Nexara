package db

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// The stale-snapshot prune spares the datastores named as not written whole and
// prunes the server's other stale rows, a datastore no longer listed's too;
// with none named, nil included, it prunes them all.
//
// Not parallel: it shares the migrated test database with the package.
func TestDeleteStalePBSSnapshotsSparesUnwrittenDatastores(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	ctx, pool, q := env.Ctx, env.Pool, gen.New(env.Pool)
	server := uuid.MustParse("128b0000-0000-4000-8000-0000000000b1")
	purge := func() { _, _ = pool.Exec(context.Background(), `DELETE FROM pbs_servers WHERE id = $1`, server) }
	purge()
	defer purge()
	// backup_time names each row: 2 was seen just now, datastore03 is no longer listed.
	if _, err := pool.Exec(ctx, `WITH s AS (INSERT INTO pbs_servers (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, 'pbs01', 'https://192.0.2.20:8007', 'tok', 'enc') RETURNING id)
		INSERT INTO pbs_snapshots (pbs_server_id, datastore, backup_type, backup_id, backup_time, last_seen_at)
		SELECT s.id, v.store, 'vm', '100', v.at, now() - v.age::interval
		FROM s, (VALUES ('datastore01', 1, '1 hour'), ('datastore01', 2, '0 s'), ('datastore02', 3, '1 hour'),
			('datastore03', 4, '1 hour')) AS v(store, at, age)`, server); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		unwritten []string
		want      string
	}{{[]string{"datastore02"}, "[2 3]"}, {nil, "[2]"}} {
		if err := q.DeleteStalePBSSnapshots(ctx, gen.DeleteStalePBSSnapshotsParams{
			PbsServerID: server, Unwritten: step.unwritten, GraceSeconds: 600,
		}); err != nil {
			t.Fatal(err)
		}
		var left []int64
		if err := pool.QueryRow(ctx, `SELECT coalesce(array_agg(backup_time ORDER BY backup_time), '{}')
			FROM pbs_snapshots WHERE pbs_server_id = $1`, server).Scan(&left); err != nil || fmt.Sprint(left) != step.want {
			t.Fatalf("sparing %v: left %v (%v), want %s", step.unwritten, left, err, step.want)
		}
	}
}
