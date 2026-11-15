package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	outcomeCluster = uuid.MustParse("143a0000-0000-4000-8000-000000000001")
	outcomeOther   = uuid.MustParse("143a0000-0000-4000-8000-000000000002")
)

// TestListAuditTaskOutcomes: an audit entry's task outcome comes from its task
// row, or from the archive once the row is archived, and only from a task of
// the entry's own cluster; an entry with no task row has none.
// Not parallel: it writes to the shared schema. Skipped without NEXARA_TEST_DB_URL.
func TestListAuditTaskOutcomes(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	clusters := []uuid.UUID{outcomeCluster, outcomeOther}
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE cluster_id = ANY($1)`, clusters)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, clusters)
	}
	purge()
	defer purge() // A defer, not t.Cleanup: the pool must still be open (see setupMigration).

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	for i, id := range clusters {
		exec(`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://192.0.2.10:8006', 'tok', 'enc')`, id, fmt.Sprintf("cluster0%d", i+1))
	}
	upid := func(n int) string { return fmt.Sprintf("UPID:pve-01:%08X:00000001:65000000:qmstart:100:root@pam:", n) }
	entry := func(cluster uuid.UUID, details string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, action, details)
			VALUES ($1, $2, 'vm', '100', 'qmstart', $3::jsonb) RETURNING id`, cluster, systemUserID, details).Scan(&id); err != nil {
			t.Fatalf("audit row: %v", err)
		}
		return id
	}
	exec(`INSERT INTO task_history (cluster_id, user_id, upid, status, exit_status) VALUES ($1, $2, $3, 'failed', 'boom')`,
		outcomeCluster, systemUserID, upid(1))
	exec(`INSERT INTO task_history_archive (upid, cluster_id, vmid, status, exit_status, started_at)
		VALUES ($1, $2, 100, 'completed', 'OK', now())`, upid(2), outcomeCluster)
	live := entry(outcomeCluster, fmt.Sprintf(`{"upid":%q}`, upid(1)))
	archived := entry(outcomeCluster, fmt.Sprintf(`{"upid":%q}`, upid(2)))
	foreign := entry(outcomeOther, fmt.Sprintf(`{"upid":%q}`, upid(1)))
	plain := entry(outcomeCluster, `{}`)

	rows, err := gen.New(pool).ListAuditTaskOutcomes(ctx, []uuid.UUID{live, archived, foreign, plain})
	if err != nil {
		t.Fatalf("ListAuditTaskOutcomes: %v", err)
	}
	got := map[uuid.UUID]string{}
	for _, r := range rows {
		got[r.ID] = r.TaskStatus + "/" + r.TaskExitStatus
	}
	want := map[uuid.UUID]string{live: "failed/boom", archived: "completed/OK"}
	if len(got) != len(want) || got[live] != want[live] || got[archived] != want[archived] {
		t.Errorf("outcomes = %v, want %v (live, archived; none for another cluster's or a plain entry)", got, want)
	}
}
