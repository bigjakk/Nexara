package db

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixed ids, so a run that dies before its cleanup leaves rows the next deletes.
var (
	m152Carol  = uuid.MustParse("15200000-0000-4000-8000-0000000000c1")
	m152Dave   = uuid.MustParse("15200000-0000-4000-8000-0000000000d1")
	m152Local  = uuid.MustParse("15200000-0000-4000-8000-0000000000e1")
	m152Config = uuid.MustParse("15200000-0000-4000-8000-0000000000f1")
	m152Other  = uuid.MustParse("15200000-0000-4000-8000-0000000000f2")
)

// TestMigration152_UsersLDAPEntry: an LDAP account from before 000152 reads as not
// bound; a binding is written once, only on an LDAP account, one entry per account and
// configuration (the same entry under another configuration is another binding, and a
// binding is never moved to another configuration nor listed by its sync); an
// address follows the entry unless another account has it in another case; a reset
// clears it; deleting the configuration unbinds its accounts. The down step is
// reversible. Not parallel: it migrates the shared schema.
func TestMigration152_UsersLDAPEntry(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate
	migrateUp(t, m)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{m152Carol, m152Dave, m152Local})
		_, _ = pool.Exec(pctx, `DELETE FROM ldap_configs WHERE id = ANY($1)`, []uuid.UUID{m152Config, m152Other})
	}
	purge()
	defer purge()
	if err := m.Migrate(151); err != nil {
		t.Fatalf("migrate to 151: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, auth_source) VALUES
	        ($1, 'carol@example.com', '', 'ldap'), ($2, 'dave@example.com', '', 'ldap'), ($3, 'Erin@example.com', '', 'local')`,
		m152Carol, m152Dave, m152Local); err != nil {
		t.Fatalf("users: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ldap_configs (id, server_url, search_base_dn) VALUES
	        ($1, 'ldap://192.0.2.10', 'dc=example,dc=com'), ($2, 'ldap://192.0.2.11', 'dc=example,dc=com')`,
		m152Config, m152Other); err != nil {
		t.Fatalf("configs: %v", err)
	}
	migrateUp(t, m)

	q := gen.New(pool)
	carol, err := q.GetUserByID(ctx, m152Carol)
	if err != nil || carol.LdapConfigID.Valid || carol.LdapEntryID != "" {
		t.Fatalf("an existing account = %v %q, %v; want not bound", carol.LdapConfigID, carol.LdapEntryID, err)
	}
	const entry = "entryUUID:6c0a1f6e-0000-4000-8000-0000000000a1"
	link := func(id, cfg uuid.UUID) error {
		_, err := q.LinkLDAPEntry(ctx, gen.LinkLDAPEntryParams{ConfigID: cfg, EntryID: entry, ID: id})
		return err
	}
	if err := link(m152Local, m152Config); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("binding a local account = %v, want no row", err)
	}
	if err := link(m152Carol, m152Config); err != nil {
		t.Fatalf("binding carol: %v", err)
	}
	for _, cfg := range []uuid.UUID{m152Config, m152Other} {
		if err := link(m152Carol, cfg); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("binding a bound account again (config %v) = %v, want no row", cfg, err)
		}
	}
	if rows, err := q.ListLDAPUsers(ctx, m152Other); err != nil || slices.ContainsFunc(rows, func(r gen.ListLDAPUsersRow) bool { return r.ID == m152Carol }) {
		t.Errorf("another configuration's sync lists carol, bound under the first: %v", err)
	}
	var pgErr *pgconn.PgError
	if err := link(m152Dave, m152Config); !errors.As(err, &pgErr) || pgErr.ConstraintName != "users_ldap_entry_key" {
		t.Errorf("binding a second account to the entry = %v, want users_ldap_entry_key", err)
	}
	if err := link(m152Dave, m152Other); err != nil {
		t.Errorf("the entry under another configuration: %v", err)
	}
	if u, err := q.GetUserByLDAPEntry(ctx, gen.GetUserByLDAPEntryParams{ConfigID: m152Config, EntryID: entry}); err != nil || u.ID != m152Carol {
		t.Errorf("GetUserByLDAPEntry = %v, %v; want carol", u.ID, err)
	}
	follow := func(email string) error {
		_, err := q.FollowLDAPEntryAddress(ctx, gen.FollowLDAPEntryAddressParams{Email: email, ID: m152Carol, ConfigID: m152Config})
		return err
	}
	if err := follow("erin@EXAMPLE.com"); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("following to an address another account has in another case = %v, want no row", err)
	}
	if err := follow("carol.moved@example.com"); err != nil {
		t.Errorf("following to a free address: %v", err)
	}
	if n, err := q.ClearLDAPEntry(ctx, m152Carol); err != nil || n != 1 {
		t.Errorf("ClearLDAPEntry = %d, %v; want 1", n, err)
	}
	if n, err := q.ClearLDAPEntry(ctx, m152Carol); err != nil || n != 0 {
		t.Errorf("ClearLDAPEntry of an unbound account = %d, %v; want 0", n, err)
	}
	if err := q.DeleteLDAPConfig(ctx, m152Other); err != nil {
		t.Fatalf("DeleteLDAPConfig: %v", err)
	}
	if dave, err := q.GetUserByID(ctx, m152Dave); err != nil || dave.LdapConfigID != (pgtype.UUID{}) {
		t.Errorf("after its configuration was deleted, dave = %v, %v; want not bound", dave.LdapConfigID, err)
	}

	if err := m.Migrate(151); err != nil {
		t.Fatalf("down to 151: %v", err)
	}
	migrateUp(t, m)
}
