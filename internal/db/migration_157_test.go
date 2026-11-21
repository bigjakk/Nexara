package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Fixed ids, so a run that dies before its cleanup leaves rows the next deletes.
var (
	m157Owner   = uuid.MustParse("15700000-0000-4000-8000-0000000000c1")
	m157Channel = uuid.MustParse("15700000-0000-4000-8000-0000000000a1")
	m157Later   = uuid.MustParse("15700000-0000-4000-8000-0000000000a2")
	m157Config  = uuid.MustParse("15700000-0000-4000-8000-0000000000b1")
)

// TestMigration157_GrandfathersCarrierGradeNAT: a notification channel and an OIDC
// configuration that exist when 000157 runs keep carrier-grade NAT, a channel made
// after it does not, and the down step drops both columns. Not parallel: it migrates
// the shared schema.
func TestMigration157_GrandfathersCarrierGradeNAT(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	migrateUp(t, m)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM notification_channels WHERE id = ANY($1)`, []uuid.UUID{m157Channel, m157Later})
		_, _ = pool.Exec(pctx, `DELETE FROM oidc_configs WHERE id = $1`, m157Config)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, m157Owner)
	}
	purge()
	defer purge()
	if err := m.Migrate(156); err != nil {
		t.Fatalf("migrate to 156: %v", err)
	}
	channel := func(id uuid.UUID) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO notification_channels (id, name, channel_type, config_encrypted, created_by)
		        VALUES ($1, 'hook01', 'webhook', 'x', $2)`, id, m157Owner); err != nil {
			t.Fatalf("channel: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, auth_source)
	        VALUES ($1, 'm157@example.com', '', 'local')`, m157Owner); err != nil {
		t.Fatalf("user: %v", err)
	}
	channel(m157Channel)
	if _, err := pool.Exec(ctx, `INSERT INTO oidc_configs (id, issuer_url, client_id)
	        VALUES ($1, 'https://idp.example.com/realms/main', 'nexara')`, m157Config); err != nil {
		t.Fatalf("oidc config: %v", err)
	}
	migrateUp(t, m)
	channel(m157Later)

	grandfathered := func(table string, id uuid.UUID) bool {
		t.Helper()
		var got bool
		if err := pool.QueryRow(ctx, `SELECT cgnat_grandfathered FROM `+table+` WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatalf("%s %s: %v", table, id, err)
		}
		return got
	}
	if !grandfathered("notification_channels", m157Channel) || !grandfathered("oidc_configs", m157Config) {
		t.Error("a channel or configuration from before 000157 does not keep carrier-grade NAT")
	}
	if grandfathered("notification_channels", m157Later) {
		t.Error("a channel made after 000157 keeps carrier-grade NAT")
	}

	if err := m.Migrate(156); err != nil {
		t.Fatalf("down to 156: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
	        WHERE column_name = 'cgnat_grandfathered' AND table_name IN ('notification_channels', 'oidc_configs')`).Scan(&left); err != nil || left != 0 {
		t.Errorf("after the down step %d cgnat_grandfathered columns remain (%v), want none", left, err)
	}
	migrateUp(t, m)
}
