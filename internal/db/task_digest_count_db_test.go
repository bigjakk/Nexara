package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var digestCountCluster = uuid.MustParse("143d0000-0000-4000-8000-000000000001")

// TestCountTaskHistoryByStatusInWindow_CountsAnHAActionOnce: the digest counts
// an HA request its worker is linked to as no task of its own, so the action
// counts once with the worker's outcome; an unpaired request still counts.
// Not parallel: it writes to the shared schema. Skipped without NEXARA_TEST_DB_URL.
func TestCountTaskHistoryByStatusInWindow_CountsAnHAActionOnce(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE cluster_id = $1`, digestCountCluster)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, digestCountCluster)
	}
	purge()
	defer purge() // A defer, not t.Cleanup: the pool must still be open (see setupMigration).

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		VALUES ($1, 'cluster01', 'https://192.0.2.10:8006', 'tok', 'enc')`, digestCountCluster)
	start := time.Now().Add(-time.Hour)
	task := func(n int, taskType, status, links string) string {
		upid := fmt.Sprintf("UPID:pve-01:%08X:00000001:%08X:%s:100:root@pam:", n, start.Unix(), taskType)
		exec(`INSERT INTO task_history (cluster_id, user_id, upid, description, status, node, task_type, started_at, finished_at)
			VALUES ($1, $2, $3, '', $4, 'pve-01', $5, $6, $6)`, digestCountCluster, systemUserID, upid, status, taskType, start)
		exec(`INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, action, details, source, created_at)
			VALUES ($1, $2, 'vm', '100', $3, jsonb_build_object('upid', $4::text) || $5::jsonb, 'proxmox', $6)`,
			digestCountCluster, systemUserID, taskType, upid, links, start)
		return upid
	}
	task(1, "hastart", "completed", `{"ha_worker_upid":"UPID:pve-01:00000002"}`)
	task(2, "qmstart", "failed", `{}`)
	task(3, "hastop", "completed", `{}`)
	task(4, "qmshutdown", "completed", `{}`)

	rows, err := gen.New(pool).CountTaskHistoryByStatusInWindow(ctx, gen.CountTaskHistoryByStatusInWindowParams{
		ClusterID: digestCountCluster, Since: start.Add(-time.Minute), Until: time.Now(),
	})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Status] = r.N
	}
	if got["completed"] != 2 || got["failed"] != 1 {
		t.Errorf("counts = %v, want completed 2 (unpaired request, shutdown) and failed 1 (the worker)", got)
	}
}
