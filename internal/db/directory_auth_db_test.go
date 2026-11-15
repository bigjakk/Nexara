package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// directoryTx runs fn in a transaction that is always rolled back, after migrating up:
// these tests read the queries' answers over rows of their own and leave nothing behind.
func directoryTx(t *testing.T, fn func(ctx context.Context, tx pgx.Tx, q *gen.Queries)) {
	t.Helper()
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	tx, err := env.Pool.Begin(env.Ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	fn(env.Ctx, tx, gen.New(tx))
}

// TestEnabledDirectoryConfig_TheOldestIsUsed: with two enabled LDAP (or OIDC) configs,
// sign-in uses the oldest, whatever order the rows sit in. The newer row is inserted
// first, so a LIMIT 1 without an ORDER BY returns it from a sequential scan.
func TestEnabledDirectoryConfig_TheOldestIsUsed(t *testing.T) {
	directoryTx(t, func(ctx context.Context, tx pgx.Tx, q *gen.Queries) {
		older := time.Now().Add(-time.Hour)
		for _, stmt := range []string{
			`UPDATE ldap_configs SET enabled = false`,
			`UPDATE oidc_configs SET enabled = false`,
			`INSERT INTO ldap_configs (name, enabled, server_url, search_base_dn, created_at) VALUES ('newer', true, 'ldap://192.0.2.10', 'dc=example,dc=com', $1::timestamptz + interval '1 minute')`,
			`INSERT INTO ldap_configs (name, enabled, server_url, search_base_dn, created_at) VALUES ('older', true, 'ldap://192.0.2.11', 'dc=example,dc=com', $1)`,
			`INSERT INTO oidc_configs (name, enabled, issuer_url, client_id, created_at) VALUES ('newer', true, 'https://sso.example.com', 'a', $1::timestamptz + interval '1 minute')`,
			`INSERT INTO oidc_configs (name, enabled, issuer_url, client_id, created_at) VALUES ('older', true, 'https://sso.example.com', 'b', $1)`,
		} {
			args := []any{older}
			if stmt[0] == 'U' {
				args = nil
			}
			if _, err := tx.Exec(ctx, stmt, args...); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		ldap, err := q.GetEnabledLDAPConfig(ctx)
		if err != nil || ldap.Name != "older" {
			t.Errorf("GetEnabledLDAPConfig = %q, %v; want the oldest, older", ldap.Name, err)
		}
		oidc, err := q.GetEnabledOIDCConfig(ctx)
		if err != nil || oidc.Name != "older" {
			t.Errorf("GetEnabledOIDCConfig = %q, %v; want the oldest, older", oidc.Name, err)
		}
	})
}

// TestLDAPSyncState_SQL: the sync's statements against Postgres. A sync deactivates only
// an active LDAP account and re-enables only one a sync deactivated; an administrator's
// change of is_active makes the deactivation theirs; the lease admits one sync at a time
// and lets the next one in once released or run out.
func TestLDAPSyncState_SQL(t *testing.T) {
	directoryTx(t, func(ctx context.Context, tx pgx.Tx, q *gen.Queries) {
		var userID, cfgID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO users (email, password_hash, display_name, auth_source) VALUES ('carol@example.com', '', 'Carol', 'ldap') RETURNING id`).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO ldap_configs (server_url, search_base_dn) VALUES ('ldap://192.0.2.10', 'dc=example,dc=com') RETURNING id`).Scan(&cfgID); err != nil {
			t.Fatal(err)
		}
		state := func() (active, byDirectory bool) {
			if err := tx.QueryRow(ctx, `SELECT is_active, deactivated_by_directory FROM users WHERE id = $1`, userID).Scan(&active, &byDirectory); err != nil {
				t.Fatal(err)
			}
			return active, byDirectory
		}
		must := func(n int64, err error, want int64, what string) {
			t.Helper()
			if err != nil || n != want {
				t.Errorf("%s: %d rows, %v; want %d", what, n, err, want)
			}
		}
		n, err := q.ReactivateLDAPUserFromDirectory(ctx, userID)
		must(n, err, 0, "reactivating an active account")
		n, err = q.DeactivateLDAPUserForDirectory(ctx, userID)
		must(n, err, 1, "a sync's deactivation")
		n, err = q.DeactivateLDAPUserForDirectory(ctx, userID)
		must(n, err, 0, "deactivating an inactive account")
		if a, d := state(); a || !d {
			t.Errorf("after a sync's deactivation: active %t, by directory %t", a, d)
		}
		if _, err := q.UpdateUserProfile(ctx, gen.UpdateUserProfileParams{ID: userID, IsActive: pgtype.Bool{Bool: false, Valid: true}}); err != nil {
			t.Fatal(err)
		}
		n, err = q.ReactivateLDAPUserFromDirectory(ctx, userID)
		must(n, err, 0, "reactivating an account an administrator deactivated")
		if _, err := q.UpdateUserProfile(ctx, gen.UpdateUserProfileParams{ID: userID, IsActive: pgtype.Bool{Bool: true, Valid: true}}); err != nil {
			t.Fatal(err)
		}
		n, err = q.DeactivateLDAPUserForDirectory(ctx, userID)
		must(n, err, 1, "a sync's deactivation again")
		n, err = q.ReactivateLDAPUserFromDirectory(ctx, userID)
		must(n, err, 1, "a sync's re-enable")
		if a, d := state(); !a || d {
			t.Errorf("after a sync's re-enable: active %t, by directory %t", a, d)
		}

		first, second := uuid.New(), uuid.New()
		claim := func(token uuid.UUID, secs float64) error {
			_, err := q.ClaimLDAPSyncLease(ctx, gen.ClaimLDAPSyncLeaseParams{ID: cfgID, Token: token, LeaseSeconds: secs})
			return err
		}
		if err := claim(first, 3600); err != nil {
			t.Fatalf("the first claim: %v", err)
		}
		if err := claim(second, 3600); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("a second claim while the first holds the lease: %v, want no row", err)
		}
		if err := q.ReleaseLDAPSyncLease(ctx, gen.ReleaseLDAPSyncLeaseParams{ID: cfgID, Token: second}); err != nil {
			t.Fatal(err)
		}
		if err := claim(second, 3600); !errors.Is(err, pgx.ErrNoRows) {
			t.Error("a release by a sync that does not hold the lease freed it")
		}
		if err := q.ReleaseLDAPSyncLease(ctx, gen.ReleaseLDAPSyncLeaseParams{ID: cfgID, Token: first}); err != nil {
			t.Fatal(err)
		}
		if err := claim(second, 0); err != nil {
			t.Errorf("a claim after the release: %v", err)
		}
		if err := claim(first, 3600); err != nil {
			t.Errorf("a claim after a lease ran out: %v", err)
		}
	})
}
