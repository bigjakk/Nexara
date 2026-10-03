package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	m105User        = uuid.MustParse("a3050000-0000-4000-8000-000000000001")
	m105LiveSession = uuid.MustParse("a3050000-0000-4000-8000-000000000002")
	m105Revoked     = uuid.MustParse("a3050000-0000-4000-8000-000000000003")
)

// TestMigration105_AddsThePreviousTokenColumnsAndKeepsEverySession is the
// data-preservation lock for migration 000105, which adds sessions.previous_token_hash
// and sessions.rotated_at.
//
// The migration is additive and a deployed install is mid-session when it runs, so
// what has to hold is that nobody is signed out and nothing is revoked by it:
//
//  1. At 104 the columns do not exist and the sessions are seeded the way the
//     previous release wrote them — which is what makes the assertions below about
//     an upgrade rather than about a fresh schema.
//  2. After 105 both columns exist and are nullable, every seeded session is intact
//     (hash, revoked flag, role) with NULL in the new columns, and the previous
//     release's own statement shape — a rotation that names neither column — still
//     works. The partial index exists.
//  3. The new statements work on those rows: a live pre-upgrade session rotates and
//     then has a previous hash; a revoked one does not rotate.
//  4. The down migration drops the columns and the index and leaves the sessions
//     exactly as they were, current hash included.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database — never the live
// nexara DB, since this round-trips the schema up and down).
func TestMigration105_AddsThePreviousTokenColumnsAndKeepsEverySession(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, m105User)
	}
	purge()
	defer purge()
	// Registered after env.Cleanup's defer, so it runs first and leaves the schema
	// at head for whichever test comes next, even if this one failed halfway.
	defer func() {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Errorf("migrate back up to head: %v", err)
		}
	}()

	columnInfo := func(column string) (nullable string, dataType string, found bool) {
		t.Helper()
		err := pool.QueryRow(ctx,
			`SELECT is_nullable, data_type FROM information_schema.columns
			  WHERE table_name = 'sessions' AND column_name = $1`, column).Scan(&nullable, &dataType)
		if err != nil {
			return "", "", false
		}
		return nullable, dataType, true
	}
	indexDef := func() string {
		t.Helper()
		var def string
		err := pool.QueryRow(ctx,
			`SELECT indexdef FROM pg_indexes
			  WHERE tablename = 'sessions' AND indexname = 'idx_sessions_previous_token_hash'`).Scan(&def)
		if err != nil {
			return ""
		}
		return def
	}

	// Step 1: the schema the previous release ran on, with sessions in it.
	if err := m.Migrate(104); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to 104: %v", err)
	}
	for _, col := range []string{"previous_token_hash", "rotated_at"} {
		if _, _, found := columnInfo(col); found {
			t.Fatalf("sessions.%s exists at version 104 — the fixture is not testing an upgrade", col)
		}
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, display_name)
		 VALUES ($1, 'migration-105@example.com', 'x', 'Migration 105')`, m105User); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// Exactly the columns CreateSession named before 000105.
	seed := func(id uuid.UUID, token string, revoked bool) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO sessions (id, user_id, token_hash, user_agent, ip_address, expires_at, user_role, is_revoked)
			 VALUES ($1, $2, $3, 'Mozilla/5.0', '192.0.2.10', now() + interval '1 day', 'admin', $4)`,
			id, m105User, auth.HashToken(token), revoked); err != nil {
			t.Fatalf("seed session %v: %v", id, err)
		}
	}
	seed(m105LiveSession, "pre-upgrade-live-token", false)
	seed(m105Revoked, "pre-upgrade-revoked-token", true)

	// Step 2: apply 105.
	if err := m.Migrate(105); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to 105: %v", err)
	}
	for col, wantType := range map[string]string{
		"previous_token_hash": "text",
		"rotated_at":          "timestamp with time zone",
	} {
		nullable, dataType, found := columnInfo(col)
		if !found {
			t.Fatalf("sessions.%s missing after migrating to 105", col)
		}
		if nullable != "YES" {
			t.Errorf("sessions.%s is_nullable = %q, want YES: a NOT NULL column would need a backfill this migration does not do", col, nullable)
		}
		if dataType != wantType {
			t.Errorf("sessions.%s type = %q, want %q", col, dataType, wantType)
		}
	}
	if def := indexDef(); def == "" {
		t.Error("idx_sessions_previous_token_hash missing after migrating to 105")
	} else if !strings.Contains(def, "previous_token_hash") || !strings.Contains(def, "IS NOT NULL") {
		t.Errorf("index definition %q is not the partial index on previous_token_hash", def)
	}

	q := gen.New(pool)
	live, err := q.GetSessionByID(ctx, m105LiveSession)
	if err != nil {
		t.Fatalf("read the pre-upgrade live session: %v", err)
	}
	if live.TokenHash != auth.HashToken("pre-upgrade-live-token") || live.IsRevoked || live.UserRole != "admin" {
		t.Errorf("live session changed by the migration: hash=%q revoked=%t role=%q", live.TokenHash, live.IsRevoked, live.UserRole)
	}
	if live.PreviousTokenHash.Valid || live.RotatedAt.Valid {
		t.Errorf("a pre-upgrade session has previous_token_hash=%+v rotated_at=%+v, want both NULL", live.PreviousTokenHash, live.RotatedAt)
	}
	revoked, err := q.GetSessionByID(ctx, m105Revoked)
	if err != nil {
		t.Fatalf("read the pre-upgrade revoked session: %v", err)
	}
	if !revoked.IsRevoked {
		t.Error("the migration un-revoked a session")
	}

	// The previous release's own statement, written without either new column,
	// still applies — which is the guarantee for a rolling restart where the old
	// binary is still serving after the schema has moved.
	if _, err := pool.Exec(ctx,
		`UPDATE sessions SET token_hash = $2, user_role = $3, last_used_at = now() WHERE id = $1`,
		m105LiveSession, auth.HashToken("rotated-by-the-old-release"), "admin"); err != nil {
		t.Fatalf("the previous release's rotation no longer applies: %v", err)
	}

	// Step 3: the new statements on those rows.
	rows, err := q.RotateSessionToken(ctx, gen.RotateSessionTokenParams{
		ID:           m105LiveSession,
		OldTokenHash: auth.HashToken("rotated-by-the-old-release"),
		NewTokenHash: auth.HashToken("rotated-by-this-release"),
		UserRole:     "admin",
	})
	if err != nil || rows != 1 {
		t.Fatalf("rotating a pre-upgrade session: rows=%d err=%v, want 1", rows, err)
	}
	live, err = q.GetSessionByID(ctx, m105LiveSession)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if live.PreviousTokenHash.String != auth.HashToken("rotated-by-the-old-release") || !live.RotatedAt.Valid {
		t.Errorf("after a rotation previous_token_hash=%+v rotated_at=%+v, want the old hash and a time", live.PreviousTokenHash, live.RotatedAt)
	}
	rows, err = q.RotateSessionToken(ctx, gen.RotateSessionTokenParams{
		ID:           m105Revoked,
		OldTokenHash: auth.HashToken("pre-upgrade-revoked-token"),
		NewTokenHash: auth.HashToken("should-never-be-written"),
		UserRole:     "admin",
	})
	if err != nil || rows != 0 {
		t.Errorf("rotating a pre-upgrade REVOKED session: rows=%d err=%v, want 0", rows, err)
	}

	// Step 4: down. The columns and the index go; the sessions stay.
	if err := m.Migrate(104); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down to 104: %v", err)
	}
	for _, col := range []string{"previous_token_hash", "rotated_at"} {
		if _, _, found := columnInfo(col); found {
			t.Errorf("sessions.%s still present after the down migration", col)
		}
	}
	if def := indexDef(); def != "" {
		t.Errorf("idx_sessions_previous_token_hash still present after the down migration: %s", def)
	}
	var hash string
	var isRevoked bool
	if err := pool.QueryRow(ctx, `SELECT token_hash, is_revoked FROM sessions WHERE id = $1`, m105LiveSession).Scan(&hash, &isRevoked); err != nil {
		t.Fatalf("the live session did not survive the down migration: %v", err)
	}
	if hash != auth.HashToken("rotated-by-this-release") || isRevoked {
		t.Errorf("after down the live session holds hash=%q revoked=%t, want its current hash and still live", hash, isRevoked)
	}
	if err := pool.QueryRow(ctx, `SELECT is_revoked FROM sessions WHERE id = $1`, m105Revoked).Scan(&isRevoked); err != nil {
		t.Fatalf("the revoked session did not survive the down migration: %v", err)
	}
	if !isRevoked {
		t.Error("the down migration un-revoked a session")
	}
}
