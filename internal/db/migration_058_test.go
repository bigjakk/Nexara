package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	gendb "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/migrations"
)

// TestMigration058_JoinTableIsAuthoritativeAfterArrayStales is the
// data-preservation regression lock for Phase 4.8b — the read-flip release.
//
// Scenario: 4.8a's UAC exposed the pre-existing array-staleness bug — when
// a notification_channel is deleted, the FK on the join table cascades the
// row out, but the cve_notification_configs.channel_ids array (which has
// no FK) still references the deleted UUID. Reads from the array would
// dispatch (or attempt to dispatch) to the deleted channel.
//
// 4.8b's read-flip closes that window. This test simulates the staleness
// scenario at the SQL level (no need to involve the dispatcher itself):
//   - seed an array with [u1, u2] AND a join table with only [u2] (the
//     state the system enters when channel u1 is deleted via the API)
//   - assert the join-table SELECT returns exactly {u2}, not {u1, u2}
//
// Without the read-flip the dispatch path would see both UUIDs and try to
// dispatch to the dead one. This test is the regression lock — if a
// future commit reverts a read site to cfg.ChannelIds, the dispatcher
// behaviour diverges from what this test implies and the bug returns.
//
// Skipped unless NEXARA_TEST_DB_URL is set (same gating as
// TestMigration057_RoundTripPreservesChannelIds).
func TestMigration058_JoinTableIsAuthoritativeAfterArrayStales(t *testing.T) {
	dbURL := os.Getenv("NEXARA_TEST_DB_URL")
	if dbURL == "" {
		t.Skip("NEXARA_TEST_DB_URL not set; skipping migration round-trip test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	defer pool.Close()

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("init iofs source: %v", err)
	}
	defer src.Close()

	m, err := migrate.NewWithSourceInstance("iofs", src, toPgx5URL(dbURL))
	if err != nil {
		t.Fatalf("init migrate: %v", err)
	}
	defer m.Close()

	// Walk all the way forward — 4.8b is code-only, no migration of its
	// own, so the schema state we run against is the post-57 join table.
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}

	// Different UUIDs from the 057 test so a parallel run doesn't conflict.
	userID := uuid.MustParse("55555555-5555-4555-8555-555555555555")
	clusterID := uuid.MustParse("66666666-6666-4666-8666-666666666666")
	liveChannel := uuid.MustParse("77777777-7777-4777-8777-777777777777")
	staleChannel := uuid.MustParse("88888888-8888-4888-8888-888888888888")

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM cve_notification_config_channels WHERE config_id = $1`, clusterID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM cve_notification_configs WHERE cluster_id = $1`, clusterID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM notification_channels WHERE id = $1`, liveChannel)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM clusters WHERE id = $1`, clusterID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, userID)
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash) VALUES ($1, $2, '')
		ON CONFLICT (id) DO NOTHING`,
		userID, "migration-058-test@nexara.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		VALUES ($1, 'migration-058-test', 'https://invalid.local', 'tok', 'enc')
		ON CONFLICT (id) DO NOTHING`,
		clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	// Only the LIVE channel exists in notification_channels. The stale
	// UUID never had (or has had and lost) a real channel — that's the
	// key staleness condition: the array references a UUID that the join
	// table can't legally reference.
	if _, err := pool.Exec(ctx, `
		INSERT INTO notification_channels (id, name, channel_type, config_encrypted, created_by)
		VALUES ($1, 'mig58-live', 'webhook', '', $2)
		ON CONFLICT (id) DO NOTHING`,
		liveChannel, userID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	// Seed the divergent state: array holds BOTH UUIDs, join table holds
	// only the live one. This is exactly the post-cascade-delete state.
	if _, err := pool.Exec(ctx, `
		INSERT INTO cve_notification_configs (cluster_id, enabled, channel_ids)
		VALUES ($1, TRUE, ARRAY[$2, $3]::UUID[])`,
		clusterID, liveChannel, staleChannel); err != nil {
		t.Fatalf("seed cve_notification_configs row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cve_notification_config_channels (config_id, channel_id) VALUES ($1, $2)`,
		clusterID, liveChannel); err != nil {
		t.Fatalf("seed join row: %v", err)
	}

	// 4.8b's load-bearing check: ListCVENotificationConfigChannels (the
	// query the dispatch path now uses) returns ONLY the live channel.
	// Pre-4.8b code would have read cfg.ChannelIds and dispatched to both,
	// landing on the stale UUID's missing-channel error path on every scan.
	queries := gendb.New(pool)
	got, err := queries.ListCVENotificationConfigChannels(ctx, clusterID)
	if err != nil {
		t.Fatalf("ListCVENotificationConfigChannels: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d channels from join table, want 1 (live only). got=%v", len(got), got)
	}
	if got[0] != liveChannel {
		t.Fatalf("got channel %v, want liveChannel %v", got[0], liveChannel)
	}

	// Sanity check: the array column is unchanged (still holds both UUIDs).
	// 4.8b is dual-write/single-read; the array stays around as a fallback
	// until 4.8c drops it. If this assertion ever flips, the dual-write
	// invariant has been silently violated.
	var arrayChannels []uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT channel_ids FROM cve_notification_configs WHERE cluster_id = $1`,
		clusterID).Scan(&arrayChannels); err != nil {
		t.Fatalf("read array: %v", err)
	}
	if len(arrayChannels) != 2 {
		t.Fatalf("array column lost data: got %v, want 2 entries", arrayChannels)
	}
}
