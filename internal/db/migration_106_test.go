package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/auth"
	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	m106Active   = uuid.MustParse("a3060100-0000-4000-8000-000000000001")
	m106Inactive = uuid.MustParse("a3060100-0000-4000-8000-000000000002")
	m106Session  = uuid.MustParse("a3060100-0000-4000-8000-000000000003")
)

// TestMigration106_AddsTheAuthEpochAndKeepsEveryUserAndSession is the
// data-preservation lock for migration 000106, which adds users.auth_epoch.
//
// The migration is additive and a deployed install has users and live sessions when
// it runs, so what has to hold is that nobody is signed out, nobody is locked out,
// and the new column starts at 0 for everyone:
//
//  1. At 105 the column does not exist, and the users and the session are seeded
//     the way the previous release wrote them — which is what makes the assertions
//     below about an upgrade rather than about a fresh schema.
//  2. After 106 the column exists, is bigint NOT NULL with default 0, every seeded
//     user holds 0 and keeps its other columns (the inactive one included), and the
//     session is intact. The previous release's own statements — a user insert that
//     names no epoch, an unconditional session insert — still apply, which is the
//     guarantee for a rolling restart where the old binary is still serving after
//     the schema has moved.
//  3. The new statements work on those rows: a pre-upgrade user at epoch 0 can have
//     a session created at 0 and is refused at 1; the bump moves it; the inactive
//     user is refused whatever the epoch.
//  4. The down migration drops the column and leaves every user and the session
//     exactly as they were, and applying 106 again starts everyone at 0 once more.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database — never the live
// nexara DB, since this round-trips the schema up and down).
func TestMigration106_AddsTheAuthEpochAndKeepsEveryUserAndSession(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{m106Active, m106Inactive})
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

	columnInfo := func() (nullable, dataType, def string, found bool) {
		t.Helper()
		var d *string
		err := pool.QueryRow(ctx,
			`SELECT is_nullable, data_type, column_default FROM information_schema.columns
			  WHERE table_name = 'users' AND column_name = 'auth_epoch'`).Scan(&nullable, &dataType, &d)
		if err != nil {
			return "", "", "", false
		}
		if d != nil {
			def = *d
		}
		return nullable, dataType, def, true
	}

	// Step 1: the schema the previous release ran on, with users and a session in it.
	if err := m.Migrate(105); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to 105: %v", err)
	}
	if _, _, _, found := columnInfo(); found {
		t.Fatal("users.auth_epoch exists at version 105 — the fixture is not testing an upgrade")
	}

	for _, u := range []struct {
		id     uuid.UUID
		email  string
		active bool
	}{
		{m106Active, "migration-106-active@example.com", true},
		{m106Inactive, "migration-106-inactive@example.com", false},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO users (id, email, password_hash, display_name, is_active, role)
			 VALUES ($1, $2, 'hash-of-a-password', 'Migration 106', $3, 'admin')`, u.id, u.email, u.active); err != nil {
			t.Fatalf("seed user %s: %v", u.email, err)
		}
	}
	// Exactly the columns the unconditional CreateSession named.
	if _, err := pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, user_agent, ip_address, expires_at, user_role)
		 VALUES ($1, $2, $3, 'Mozilla/5.0', '192.0.2.10', now() + interval '1 day', 'admin')`,
		m106Session, m106Active, auth.HashToken("pre-upgrade-token")); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// Step 2: apply 106.
	if err := m.Migrate(106); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to 106: %v", err)
	}
	nullable, dataType, def, found := columnInfo()
	if !found {
		t.Fatal("users.auth_epoch missing after migrating to 106")
	}
	if nullable != "NO" {
		t.Errorf("users.auth_epoch is_nullable = %q, want NO: a nullable epoch would make `auth_epoch = $1` unknown for a NULL row, and the insert would refuse every sign-in of it", nullable)
	}
	if dataType != "bigint" {
		t.Errorf("users.auth_epoch type = %q, want bigint", dataType)
	}
	if def != "0" {
		t.Errorf("users.auth_epoch default = %q, want 0: every existing row gets it, and a new user starts at 0", def)
	}

	q := gen.New(pool)
	for _, u := range []struct {
		id     uuid.UUID
		active bool
	}{{m106Active, true}, {m106Inactive, false}} {
		got, err := q.GetUserByID(ctx, u.id)
		if err != nil {
			t.Fatalf("read the pre-upgrade user %v: %v", u.id, err)
		}
		if got.AuthEpoch != 0 {
			t.Errorf("a pre-upgrade user holds epoch %d, want 0", got.AuthEpoch)
		}
		if got.IsActive != u.active || got.PasswordHash != "hash-of-a-password" || got.Role != "admin" || got.AuthSource != "local" {
			t.Errorf("the migration changed user %v: active=%t hash=%q role=%q source=%q", u.id, got.IsActive, got.PasswordHash, got.Role, got.AuthSource)
		}
	}
	sess, err := q.GetSessionByID(ctx, m106Session)
	if err != nil {
		t.Fatalf("read the pre-upgrade session: %v", err)
	}
	if sess.TokenHash != auth.HashToken("pre-upgrade-token") || sess.IsRevoked || sess.UserRole != "admin" {
		t.Errorf("the session changed: hash=%q revoked=%t role=%q", sess.TokenHash, sess.IsRevoked, sess.UserRole)
	}

	// The previous release's own statements, written without the column, still apply.
	var oldRelease uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, display_name) VALUES ('migration-106-old-release@example.com', 'x', 'Old release') RETURNING id`).Scan(&oldRelease); err != nil {
		t.Fatalf("the previous release's user insert no longer applies: %v", err)
	}
	defer func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, oldRelease)
	}()
	if _, err := pool.Exec(ctx,
		`INSERT INTO sessions (user_id, token_hash, user_agent, ip_address, expires_at, device_name, device_type, device_id, user_role)
		 VALUES ($1, $2, 'Mozilla/5.0', '192.0.2.10', now() + interval '1 day', NULL, NULL, NULL, 'admin')`,
		oldRelease, auth.HashToken("old-release-token")); err != nil {
		t.Fatalf("the previous release's unconditional session insert no longer applies: %v", err)
	}

	// Step 3: the new statements on those rows.
	create := func(user uuid.UUID, epoch int64, token string) error {
		_, err := q.CreateSessionAtEpoch(ctx, epochParams(user, epoch, token))
		return err
	}
	if err := create(m106Active, 0, "post-upgrade-token"); err != nil {
		t.Fatalf("a session for a pre-upgrade user at epoch 0: %v", err)
	}
	if err := create(m106Active, 1, "too-far-ahead"); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("a session at epoch 1 for a user at 0: %v, want pgx.ErrNoRows", err)
	}
	if rows, err := q.BumpUserAuthEpoch(ctx, m106Active); err != nil || rows != 1 {
		t.Fatalf("bump: rows=%d err=%v, want 1", rows, err)
	}
	if err := create(m106Active, 0, "stale"); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("a session at the epoch before the bump: %v, want pgx.ErrNoRows", err)
	}
	if err := create(m106Active, 1, "after-the-bump"); err != nil {
		t.Errorf("a session at the epoch after the bump: %v", err)
	}
	if err := create(m106Inactive, 0, "deactivated"); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("a session for a deactivated pre-upgrade user: %v, want pgx.ErrNoRows", err)
	}

	// Step 4: down. The column goes; the users and the session stay.
	if err := m.Migrate(105); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down to 105: %v", err)
	}
	if _, _, _, found := columnInfo(); found {
		t.Error("users.auth_epoch still present after the down migration")
	}
	var email, hash string
	var active bool
	if err := pool.QueryRow(ctx, `SELECT email, password_hash, is_active FROM users WHERE id = $1`, m106Inactive).Scan(&email, &hash, &active); err != nil {
		t.Fatalf("the inactive user did not survive the down migration: %v", err)
	}
	if email != "migration-106-inactive@example.com" || hash != "hash-of-a-password" || active {
		t.Errorf("after down the inactive user holds email=%q hash=%q active=%t", email, hash, active)
	}
	var revoked bool
	if err := pool.QueryRow(ctx, `SELECT is_revoked FROM sessions WHERE id = $1`, m106Session).Scan(&revoked); err != nil {
		t.Fatalf("the session did not survive the down migration: %v", err)
	}
	if revoked {
		t.Error("the down migration revoked a session")
	}

	// And up again: everyone starts at 0, even the user whose epoch had moved.
	if err := m.Migrate(106); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up again to 106: %v", err)
	}
	got, err := q.GetUserByID(ctx, m106Active)
	if err != nil {
		t.Fatalf("read after up again: %v", err)
	}
	if got.AuthEpoch != 0 {
		t.Errorf("after down and up the user holds epoch %d, want 0: the counter is not carried across", got.AuthEpoch)
	}
}
