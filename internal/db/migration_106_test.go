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

// TestMigration106_AddsTheAuthEpochAndKeepsEveryUserAndSession: seeded at 105 as the previous
// release wrote them, after 106 every user holds auth_epoch 0 (bigint NOT NULL default 0) with
// everything else and the session intact, the old release's statements still apply (a rolling
// restart), and the new ones work on those rows; down and up again restore it all. Skipped
// without NEXARA_TEST_DB_URL (a throwaway database: this round-trips the schema).
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
		// Read by hand, not with GetUserByID: the generated query names every column
		// users has at head, and this runs at version 106.
		var got gen.User
		err := pool.QueryRow(ctx, `SELECT auth_epoch, is_active, password_hash, role, auth_source FROM users WHERE id = $1`, u.id).
			Scan(&got.AuthEpoch, &got.IsActive, &got.PasswordHash, &got.Role, &got.AuthSource)
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
	var epoch int64
	if err := pool.QueryRow(ctx, `SELECT auth_epoch FROM users WHERE id = $1`, m106Active).Scan(&epoch); err != nil {
		t.Fatalf("read after up again: %v", err)
	}
	if epoch != 0 {
		t.Errorf("after down and up the user holds epoch %d, want 0: the counter is not carried across", epoch)
	}
}
