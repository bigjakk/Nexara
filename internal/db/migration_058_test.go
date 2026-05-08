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

	"github.com/bigjakk/nexara/migrations"
)

// TestMigration058_DropArrayRoundTripsViaJoinTable is the data-preservation
// regression lock for Phase 4.8c — the data-loss release that drops
// cve_notification_configs.channel_ids.
//
// Per the stricter rollback bar set 2026-05-08, every migration that
// moves user data ships a Go test that round-trips real seed rows. The
// 4.8c down migration rebuilds the dropped column via array_agg over the
// join table; if the join table held the same channels at the moment
// 4.8c was applied (which the dual-write of 4.8a guarantees, and which
// 4.8b's read-flip locks in), the rebuild on rollback is exact. This
// test exercises that round-trip end-to-end.
//
// Sequence:
//
//   1. migrate up to 57 (the 4.8a state — both representations exist)
//   2. seed config + 2 channels + populate BOTH the array and the join
//      table with [u1, u2] (mimics what 4.8a's transactional dual-write
//      would have produced)
//   3. migrate up to 58 (the 4.8c state — array column dropped, join
//      table is the only source of truth). Assert the column is gone
//      and the join table still holds [u1, u2].
//   4. migrate down to 57 (the rollback path). Assert the column is
//      back AND its contents match [u1, u2] — rebuilt by the .down.sql
//      via array_agg over the join table.
//
// Skipped unless NEXARA_TEST_DB_URL is set.
func TestMigration058_DropArrayRoundTripsViaJoinTable(t *testing.T) {
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

	// Seed UUIDs that don't collide with the 057 test's fixtures.
	userID := uuid.MustParse("99999999-9999-4999-8999-999999999999")
	clusterID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	channelA := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	channelB := uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM cve_notification_config_channels WHERE config_id = $1`, clusterID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM cve_notification_configs WHERE cluster_id = $1`, clusterID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM notification_channels WHERE id IN ($1, $2)`, channelA, channelB)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM clusters WHERE id = $1`, clusterID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, userID)
	})

	// Step 1: migrate up to 57 (post-4.8a, pre-4.8c).
	if err := m.Migrate(57); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up to 57: %v", err)
	}

	// Step 2: seed parents and the dual-written state (array AND join).
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
	if _, err := pool.Exec(ctx, `
		INSERT INTO notification_channels (id, name, channel_type, config_encrypted, created_by)
		VALUES ($1, 'mig58-A', 'webhook', '', $3),
		       ($2, 'mig58-B', 'webhook', '', $3)
		ON CONFLICT (id) DO NOTHING`,
		channelA, channelB, userID); err != nil {
		t.Fatalf("seed channels: %v", err)
	}

	// Dual-write seed: the row's array column AND the join table both
	// have the same two UUIDs — exactly what a 4.8a or 4.8b binary
	// would have produced via the handler's transactional upsert.
	if _, err := pool.Exec(ctx, `
		INSERT INTO cve_notification_configs (cluster_id, enabled, channel_ids)
		VALUES ($1, TRUE, ARRAY[$2, $3]::UUID[])`,
		clusterID, channelA, channelB); err != nil {
		t.Fatalf("seed cve_notification_configs row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cve_notification_config_channels (config_id, channel_id)
		VALUES ($1, $2), ($1, $3)`,
		clusterID, channelA, channelB); err != nil {
		t.Fatalf("seed join rows: %v", err)
	}

	// Step 3: migrate up to 58. The column should be gone and the join
	// table should still hold both channels.
	if err := m.Migrate(58); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up to 58: %v", err)
	}

	var hasChannelIDsColumn bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'cve_notification_configs'
			  AND column_name = 'channel_ids'
		)`).Scan(&hasChannelIDsColumn); err != nil {
		t.Fatalf("probe channel_ids column post-up: %v", err)
	}
	if hasChannelIDsColumn {
		t.Fatalf("channel_ids column still exists after migrate up to 58")
	}

	wantSorted := sortedUUIDs([]uuid.UUID{channelA, channelB})
	var joinChannels []uuid.UUID
	rows, err := pool.Query(ctx, `
		SELECT channel_id FROM cve_notification_config_channels
		WHERE config_id = $1 ORDER BY channel_id`, clusterID)
	if err != nil {
		t.Fatalf("query join table post-up: %v", err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan join row: %v", err)
		}
		joinChannels = append(joinChannels, id)
	}
	rows.Close()
	if !uuidSlicesEqual(sortedUUIDs(joinChannels), wantSorted) {
		t.Fatalf("join table after migrate up to 58: got %v, want %v",
			sortedUUIDs(joinChannels), wantSorted)
	}

	// Step 4: migrate back down to 57. The column should be back AND
	// its contents must match — rebuilt via array_agg over the join
	// table. This is the load-bearing rollback assertion: if the down
	// migration's array_agg shape ever drifts, real installations
	// rolling back from 4.8c lose data.
	if err := m.Migrate(57); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down to 57: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'cve_notification_configs'
			  AND column_name = 'channel_ids'
		)`).Scan(&hasChannelIDsColumn); err != nil {
		t.Fatalf("probe channel_ids column post-down: %v", err)
	}
	if !hasChannelIDsColumn {
		t.Fatalf("channel_ids column not restored after migrate down to 57")
	}

	var rebuiltArray []uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT channel_ids FROM cve_notification_configs WHERE cluster_id = $1`,
		clusterID).Scan(&rebuiltArray); err != nil {
		t.Fatalf("read rebuilt array post-down: %v", err)
	}
	if !uuidSlicesEqual(sortedUUIDs(rebuiltArray), wantSorted) {
		t.Fatalf("rebuilt channel_ids after migrate down: got %v, want %v (data lost on rollback!)",
			sortedUUIDs(rebuiltArray), wantSorted)
	}
}
