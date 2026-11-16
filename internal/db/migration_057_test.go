package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bigjakk/nexara/migrations"
)

// TestMigration057_RoundTripPreservesChannelIds: a cve_notification_configs row with channel_ids
// [u1, u2] goes up to 57 (join table backfilled with exactly those pairs) and down to 56 (array
// untouched), with real data, which an empty-database round trip cannot check. Skipped without
// NEXARA_TEST_DB_URL: it runs 0 to 57 and back to 56, so it needs a throwaway database.
func TestMigration057_RoundTripPreservesChannelIds(t *testing.T) {
	dbURL := testDBURL(t)

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

	// Walk forward to v56 (the schema state pre-this-migration). 4.8a is
	// migration 57, so migrating to 56 first establishes a clean
	// "pre-this-release" baseline we seed against.
	if err := m.Migrate(56); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up to 56: %v", err)
	}

	// Seed: one user (notification_channels.created_by FK), one cluster
	// (cve_notification_configs.cluster_id FK), two notification channels.
	// We use stable hardcoded UUIDs so a failed test leaves data identifiable
	// for cleanup, and the test's own teardown step removes them.
	userID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	clusterID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	channelA := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	channelB := uuid.MustParse("44444444-4444-4444-8444-444444444444")

	// Best-effort cleanup in FK order. A defer, not t.Cleanup: cleanups run after `defer
	// pool.Close()`, so every delete failed silently and fixtures leaked into the next run; this
	// defer runs first, on its own context. Same shape as migration_068_test.go and 075's.
	purge := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM cve_notification_configs WHERE cluster_id = $1`, clusterID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM notification_channels WHERE id IN ($1, $2)`, channelA, channelB)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM clusters WHERE id = $1`, clusterID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, userID)
	}
	// Up front as well as deferred: the seeds below use fixed UUIDs, so rows
	// left by a run that aborted before its defers would collide on the primary
	// key and fail this test for a reason that has nothing to do with the
	// migration. Matches migration_068_test.go / migration_075_test.go.
	purge()
	defer purge()

	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash) VALUES ($1, $2, '')
		ON CONFLICT (id) DO NOTHING`,
		userID, "migration-057-test@nexara.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		VALUES ($1, 'migration-057-test', 'https://invalid.local', 'tok', 'enc')
		ON CONFLICT (id) DO NOTHING`,
		clusterID); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO notification_channels (id, name, channel_type, config_encrypted, created_by)
		VALUES ($1, 'mig57-A', 'webhook', '', $3),
		       ($2, 'mig57-B', 'webhook', '', $3)
		ON CONFLICT (id) DO NOTHING`,
		channelA, channelB, userID); err != nil {
		t.Fatalf("seed channels: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO cve_notification_configs (cluster_id, enabled, channel_ids)
		VALUES ($1, TRUE, ARRAY[$2, $3]::UUID[])`,
		clusterID, channelA, channelB); err != nil {
		t.Fatalf("seed cve_notification_configs row: %v", err)
	}

	// Apply 57. This is the load-bearing step: the up migration creates
	// the join table AND backfills it via unnest(channel_ids).
	if err := m.Migrate(57); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up to 57: %v", err)
	}

	// Backfill assertion: the join table holds exactly {(clusterID, A),
	// (clusterID, B)} — no more, no less, in any order. We sort-compare
	// rather than exact-order match so the migration's INSERT … SELECT
	// order doesn't get baked into the test.
	var joinChannels []uuid.UUID
	rows, err := pool.Query(ctx, `
		SELECT channel_id FROM cve_notification_config_channels
		WHERE config_id = $1 ORDER BY channel_id`, clusterID)
	if err != nil {
		t.Fatalf("query join table: %v", err)
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

	wantSorted := sortedUUIDs([]uuid.UUID{channelA, channelB})
	gotSorted := sortedUUIDs(joinChannels)
	if !uuidSlicesEqual(gotSorted, wantSorted) {
		t.Fatalf("join table after migrate up: got %v, want %v", gotSorted, wantSorted)
	}

	// The array on the original row must still hold the same two UUIDs —
	// 4.8a is dual-storage, not a move. This is what makes the release
	// "fully reversible" without data loss.
	var arrayChannels []uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT channel_ids FROM cve_notification_configs WHERE cluster_id = $1`,
		clusterID).Scan(&arrayChannels); err != nil {
		t.Fatalf("read array post-up: %v", err)
	}
	if !uuidSlicesEqual(sortedUUIDs(arrayChannels), wantSorted) {
		t.Fatalf("array column after migrate up: got %v, want %v",
			sortedUUIDs(arrayChannels), wantSorted)
	}

	// Migrate back down to 56. The down migration drops the join table; the
	// array column is the source of truth in 4.8a so it must survive.
	if err := m.Migrate(56); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down to 56: %v", err)
	}

	// Join table must be gone entirely.
	var joinExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_name = 'cve_notification_config_channels'
		)`).Scan(&joinExists); err != nil {
		t.Fatalf("probe join table existence: %v", err)
	}
	if joinExists {
		t.Fatalf("cve_notification_config_channels still exists after migrate down")
	}

	// Array must be intact.
	var arrayAfterDown []uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT channel_ids FROM cve_notification_configs WHERE cluster_id = $1`,
		clusterID).Scan(&arrayAfterDown); err != nil {
		t.Fatalf("read array post-down: %v", err)
	}
	if !uuidSlicesEqual(sortedUUIDs(arrayAfterDown), wantSorted) {
		t.Fatalf("array column after migrate down: got %v, want %v (data lost on rollback!)",
			sortedUUIDs(arrayAfterDown), wantSorted)
	}
}

func sortedUUIDs(in []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, len(in))
	copy(out, in)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].String() > out[j].String(); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func uuidSlicesEqual(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
