package db

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func userRolesScopeID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("12300000-0000-4000-8000-%012d", n))
}

// Removing a cluster used to leave the role assignments scoped to it behind. The
// migration deletes the ones whose cluster is already gone and adds the key that
// ties the rest to theirs; the first half is what the old schema allowed, so a
// fixture that could not insert a dangling assignment there proves nothing.
func TestMigrationUserRolesClusterScope_AssignmentsFollowTheirCluster(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	const admin, viewer, operator = "a0000000-0000-0000-0000-000000000001", "a0000000-0000-0000-0000-000000000003", "a0000000-0000-0000-0000-000000000002"
	before, version, _ := migrationByName(t, "user_roles_cluster_scope")
	var (
		userA = userRolesScopeID(1)
		userB = userRolesScopeID(2)
		live  = userRolesScopeID(3)
		gone  = userRolesScopeID(4) // a cluster that was removed: nothing in clusters names it
	)
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM user_roles WHERE user_id = ANY($1)`, []uuid.UUID{userA, userB})
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, live)
		_, _ = pool.Exec(pctx, `DELETE FROM removed_clusters WHERE id = $1`, live)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{userA, userB})
	}
	purge()
	defer purge()
	defer func() {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Errorf("restore to head: %v", err)
		}
	}()

	if err := m.Migrate(before); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to %d: %v", before, err)
	}
	exec := func(what, sql string, args ...any) error {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		if err != nil && what != "" {
			t.Fatalf("%s: %v", what, err)
		}
		return err
	}
	assign := func(what string, user uuid.UUID, role, scopeType string, scope any) error {
		t.Helper()
		return exec(what, `INSERT INTO user_roles (user_id, role_id, scope_type, scope_id) VALUES ($1, $2, $3, $4)`, user, role, scopeType, scope)
	}
	count := func() (n int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_roles WHERE user_id = ANY($1)`, []uuid.UUID{userA, userB}).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	for i, u := range []uuid.UUID{userA, userB} {
		exec("user", `INSERT INTO users (id, email, password_hash, display_name) VALUES ($1, $2, 'x', 'Scope test')`, u, fmt.Sprintf("migration-roles-%d@example.invalid", i))
	}
	exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		VALUES ($1, 'cluster01', 'https://cluster.invalid:8006', 'test@pam!t', 'x')`, live)
	assign("on the live cluster", userA, admin, "cluster", live)
	assign("on a cluster that is gone (the old schema allows it)", userA, viewer, "cluster", gone)
	assign("on a cluster that is gone, another user", userB, viewer, "cluster", gone)
	assign("global", userB, admin, "global", nil)
	if got := count(); got != 4 {
		t.Fatalf("seeded %d assignments, want 4", got)
	}

	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate over the key: %v", err)
	}
	if got := count(); got != 2 {
		t.Errorf("%d assignments after the migration, want 2: the two on a cluster that is gone deleted, the live and the global kept", got)
	}
	var kept int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_roles WHERE user_id = ANY($1) AND scope_id IS DISTINCT FROM $2 AND scope_type = 'cluster'`,
		[]uuid.UUID{userA, userB}, live).Scan(&kept); err != nil || kept != 0 {
		t.Errorf("assignments scoped to some other cluster remain: %d (err %v)", kept, err)
	}

	// The key: an assignment naming a cluster that does not exist is refused...
	err := assign("", userA, operator, "cluster", gone)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" || pgErr.ConstraintName != "user_roles_scope_id_fkey" {
		t.Errorf("an assignment on a cluster that does not exist: %v, want a foreign-key violation of user_roles_scope_id_fkey", err)
	}
	// ...and removing a cluster removes the assignments scoped to it.
	exec("remove the live cluster", `DELETE FROM clusters WHERE id = $1`, live)
	if got := count(); got != 1 {
		t.Errorf("%d assignments after the cluster was removed, want 1: only the global one", got)
	}

	// Down: the key is gone, so the old schema's behaviour is back.
	if err := m.Migrate(before); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if err := assign("", userA, operator, "cluster", gone); err != nil {
		t.Errorf("after the down an assignment on a cluster that does not exist is refused: %v", err)
	}
}
