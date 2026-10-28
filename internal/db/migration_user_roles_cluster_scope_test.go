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
// migration deletes the ones whose cluster is already gone, clears a scope_id from a
// global assignment (which carries none), and adds the check and the key that hold
// that; the first half is what the old schema allowed, so a fixture that could not
// insert such rows there proves nothing.
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
	count := func(where string) (n int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_roles WHERE user_id = ANY($1) AND `+where, []uuid.UUID{userA, userB}).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	violation := func(err error, code, constraint string) bool {
		var pgErr *pgconn.PgError
		return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == constraint
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
	// Global assignments that carry a scope_id, which the old schema also allows.
	assign("a stray id repeating a global assignment", userB, admin, "global", gone)
	assign("a stray id naming a cluster that is gone", userA, operator, "global", gone)
	assign("a stray id naming the live cluster", userB, operator, "global", live)
	assign("two stray ids on one user and role", userA, viewer, "global", live)
	assign("two stray ids on one user and role, the other", userA, viewer, "global", gone)
	if got := count("true"); got != 9 {
		t.Fatalf("seeded %d assignments, want 9", got)
	}

	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate over the constraints: %v", err)
	}
	// Left: the live cluster's, and four global ones — the plain one, the two stray-id ones
	// that had no twin, and one of the pair that were one grant.
	if got := count("true"); got != 5 {
		t.Errorf("%d assignments after the migration, want 5", got)
	}
	if got := count("scope_type = 'cluster' AND scope_id = '" + live.String() + "'"); got != 1 {
		t.Errorf("%d assignments on the live cluster, want its one", got)
	}
	if got := count("scope_type = 'global' AND scope_id IS NOT NULL"); got != 0 {
		t.Errorf("%d global assignments still carry a scope_id", got)
	}
	if got := count("scope_type = 'global'"); got != 4 {
		t.Errorf("%d global assignments, want 4: none deleted but the repeats", got)
	}
	// A pair that is one grant keeps one assignment of it, and the plain one stays alone.
	if got := count("user_id = '" + userA.String() + "' AND role_id = '" + viewer + "' AND scope_type = 'global'"); got != 1 {
		t.Errorf("%d global viewer assignments for the first user, want 1", got)
	}
	if got := count("user_id = '" + userB.String() + "' AND role_id = '" + admin + "' AND scope_type = 'global'"); got != 1 {
		t.Errorf("%d global admin assignments for the second user, want 1: the plain one, not the repeat", got)
	}

	// The check: a global assignment carries no scope_id, a cluster one always does.
	if err := assign("", userA, admin, "global", live); !violation(err, "23514", "user_roles_scope_check") {
		t.Errorf("a global assignment with a scope_id: %v, want a check violation of user_roles_scope_check", err)
	}
	if err := assign("", userB, viewer, "cluster", nil); !violation(err, "23514", "user_roles_scope_check") {
		t.Errorf("a cluster assignment with no scope_id: %v, want a check violation of user_roles_scope_check", err)
	}
	// The key: an assignment naming a cluster that does not exist is refused...
	if err := assign("", userA, operator, "cluster", gone); !violation(err, "23503", "user_roles_scope_id_fkey") {
		t.Errorf("an assignment on a cluster that does not exist: %v, want a foreign-key violation of user_roles_scope_id_fkey", err)
	}
	// ...and removing a cluster removes the assignments scoped to it, and no global one.
	exec("remove the live cluster", `DELETE FROM clusters WHERE id = $1`, live)
	if got, global := count("true"), count("scope_type = 'global'"); got != 4 || global != 4 {
		t.Errorf("%d assignments (%d global) after the cluster was removed, want the 4 global ones", got, global)
	}

	// Down: the constraints are gone, so the old schema's behaviour is back.
	if err := m.Migrate(before); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if err := assign("", userA, operator, "cluster", gone); err != nil {
		t.Errorf("after the down an assignment on a cluster that does not exist is refused: %v", err)
	}
	if err := assign("", userA, admin, "global", gone); err != nil {
		t.Errorf("after the down a global assignment with a scope_id is refused: %v", err)
	}
}
