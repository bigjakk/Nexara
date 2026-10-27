package db

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

func clusterDeleteRolesID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("12800000-0000-4000-8000-%012d", n))
}

// Removing a cluster deletes, in the same statement, the role assignments scoped
// to it, and answers whose they were — one user id per assignment — so that the
// caller can clear those users' cached permissions. Another cluster's assignment
// and a global one stay.
func TestDeleteClusterAndScopedRoles_TakesOnlyTheAssignmentsScopedToIt(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	const admin, viewer = "a0000000-0000-0000-0000-000000000001", "a0000000-0000-0000-0000-000000000003"
	var (
		users     = []uuid.UUID{clusterDeleteRolesID(1), clusterDeleteRolesID(2), clusterDeleteRolesID(3), clusterDeleteRolesID(4)}
		clusterA  = clusterDeleteRolesID(11)
		clusterB  = clusterDeleteRolesID(12)
		u1, u2    = users[0], users[1]
		u3, u4    = users[2], users[3]
		wantUsers = []uuid.UUID{u1, u1, u2}
	)
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM user_roles WHERE user_id = ANY($1)`, users)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{clusterA, clusterB})
		_, _ = pool.Exec(pctx, `DELETE FROM removed_clusters WHERE id = ANY($1)`, []uuid.UUID{clusterA, clusterB})
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = ANY($1)`, users)
	}
	purge()
	defer purge()

	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	for i, u := range users {
		exec("user", `INSERT INTO users (id, email, password_hash, display_name) VALUES ($1, $2, 'x', 'Delete roles')`, u, fmt.Sprintf("cluster-delete-roles-%d@example.invalid", i))
	}
	for i, c := range []uuid.UUID{clusterA, clusterB} {
		exec("cluster", `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://cluster.invalid:8006', 'test@pam!t', 'x')`, c, fmt.Sprintf("cluster0%d", i+1))
	}
	for _, a := range []struct {
		user        uuid.UUID
		role, scope string
		cluster     any
		whatItIsFor string
	}{
		{u1, admin, "cluster", clusterA, "u1's first on A"},
		{u1, viewer, "cluster", clusterA, "u1's second on A"},
		{u2, viewer, "cluster", clusterA, "u2 on A"},
		{u3, viewer, "cluster", clusterB, "u3 on B"},
		{u4, admin, "global", nil, "u4 everywhere"},
	} {
		exec(a.whatItIsFor, `INSERT INTO user_roles (user_id, role_id, scope_type, scope_id) VALUES ($1, $2, $3, $4)`, a.user, a.role, a.scope, a.cluster)
	}

	got, err := gen.New(pool).DeleteClusterAndScopedRoles(ctx, clusterA)
	if err != nil {
		t.Fatalf("DeleteClusterAndScopedRoles: %v", err)
	}
	slices.SortFunc(got, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	if !slices.Equal(got, wantUsers) {
		t.Errorf("answered users %v, want %v: one per assignment removed", got, wantUsers)
	}

	var remaining []uuid.UUID
	rows, err := pool.Query(ctx, `SELECT user_id FROM user_roles WHERE user_id = ANY($1) ORDER BY user_id`, users)
	if err != nil {
		t.Fatalf("remaining assignments: %v", err)
	}
	for rows.Next() {
		var u uuid.UUID
		if err := rows.Scan(&u); err != nil {
			t.Fatalf("scan: %v", err)
		}
		remaining = append(remaining, u)
	}
	rows.Close()
	if want := []uuid.UUID{u3, u4}; !slices.Equal(remaining, want) {
		t.Errorf("assignments left for %v, want only %v: another cluster's and the global one", remaining, want)
	}

	var a, b int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE id = $1), count(*) FILTER (WHERE id = $2) FROM clusters`, clusterA, clusterB).Scan(&a, &b); err != nil {
		t.Fatalf("clusters: %v", err)
	}
	if a != 0 || b != 1 {
		t.Errorf("clusters left: A %d, B %d; want the removed one gone and the other kept", a, b)
	}
	var kept string
	if err := pool.QueryRow(ctx, `SELECT name FROM removed_clusters WHERE id = $1`, clusterA).Scan(&kept); err != nil || kept != "cluster01" {
		t.Errorf("removed_clusters names the removed cluster %q (err %v), want cluster01: the trigger runs inside the same statement", kept, err)
	}
}
