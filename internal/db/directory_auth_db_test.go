package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

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
