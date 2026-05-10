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

// TestMigration057_RoundTripPreservesChannelIds is the data-preservation
// check that 4.8a's stricter rollback bar requires (REMEDIATION_PLAN.md
// 2026-05-08): seed a cve_notification_configs row with channel_ids =
// [u1, u2], migrate up to 57 (creates join table + backfills), assert the
// join table holds exactly the same two pairs the array did, migrate back
// down to 56 (drops join table), assert the array column is untouched.
//
// A generic empty-DB harness can't catch this — it would only verify the
// schema round-trips, not that real seed data survives the up + down cycle
// losslessly.
//
// Skipped unless NEXARA_TEST_DB_URL points at a Postgres instance the test
// can create/drop schema in. The test runs every migration up from 0 to 57
// and back down to 56, so it expects an empty database (or one whose state
// it's allowed to overwrite). The dev stack precedent (REMEDIATION_PLAN.md
// 2.3 "fresh DB CLI test") used a throwaway nexara_freshtest database in
// the same Postgres instance — same shape applies here.
func TestMigration057_RoundTripPreservesChannelIds(t *testing.T) {
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

	t.Cleanup(func() {
		// Best-effort cleanup so a re-run on the same DB doesn't trip on FK
		// constraints. Order matches the FK dependency tree.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM cve_notification_configs WHERE cluster_id = $1`, clusterID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM notification_channels WHERE id IN ($1, $2)`, channelA, channelB)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM clusters WHERE id = $1`, clusterID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, userID)
	})

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
