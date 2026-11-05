package db

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	feedTestUser     = uuid.MustParse("5c09f000-0000-4000-8000-000000000001")
	feedTestClusterA = uuid.MustParse("5c09f000-0000-4000-8000-00000000000a")
	feedTestClusterB = uuid.MustParse("5c09f000-0000-4000-8000-00000000000b")
)

const (
	feedTestResourceType = "recent-feed-test"
	feedTestFiller       = "filler"
)

// TestRecentFeed_KeepsLongTasks protects the activity drawer: a task's entry is
// written when it starts, so a long one must survive 50 newer entries, and only
// for callers scoped to its cluster. Not parallel: it seeds the shared database.
// Skipped unless NEXARA_TEST_DB_URL names a throwaway database.
func TestRecentFeed_KeepsLongTasks(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		clusters := []uuid.UUID{feedTestClusterA, feedTestClusterB}
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE resource_type = $1`, feedTestResourceType)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, clusters)
		_, _ = pool.Exec(pctx, `DELETE FROM removed_clusters WHERE id = ANY($1)`, clusters)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, feedTestUser)
	}
	purge()
	defer purge() // A defer, not t.Cleanup: the pool must still be open (see TestAuditScope_NullClusterRowsAreGlobal).

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, display_name)
		 VALUES ($1, 'recent-feed-test@example.invalid', 'x', 'Recent Feed Test')`, feedTestUser); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	for _, c := range []uuid.UUID{feedTestClusterA, feedTestClusterB} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			 VALUES ($1, $2, 'https://cluster.invalid:8006', 'test@pam!t', 'x')`,
			c, "recent-feed-test-"+c.String()); err != nil {
			t.Fatalf("seed cluster: %v", err)
		}
	}

	const offlineNode = "offline-node"
	if _, err := pool.Exec(ctx,
		`INSERT INTO nodes (cluster_id, name, status) VALUES ($1, $2, 'offline')`,
		feedTestClusterA, offlineNode); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	now := time.Now()
	for _, task := range []struct {
		key         string
		cluster     uuid.UUID
		status      string
		startedAgo  time.Duration
		finishedAgo time.Duration // Zero means it has not finished.
		node        string
	}{
		{"running", feedTestClusterA, "running", 2 * time.Hour, 0, ""},
		{"running-other-cluster", feedTestClusterB, "running", 2 * time.Hour, 0, ""},
		{"finished-recently", feedTestClusterA, "failed", 3 * time.Hour, 10 * time.Minute, ""},
		{"finished-recently-on-offline-node", feedTestClusterA, "failed", 3 * time.Hour, 10 * time.Minute, offlineNode},
		{"finished-long-ago", feedTestClusterA, "completed", 3 * time.Hour, 2 * time.Hour, ""},
		{"left-running-by-a-kill", feedTestClusterA, "running", 8 * 24 * time.Hour, 0, ""},
		{"running-on-offline-node", feedTestClusterA, "running", 2 * time.Hour, 0, offlineNode},
	} {
		started := now.Add(-task.startedAgo)
		var finished *time.Time
		if task.finishedAgo != 0 {
			at := now.Add(-task.finishedAgo)
			finished = &at
		}
		upid := "UPID:recent-feed-test:" + task.key
		if _, err := pool.Exec(ctx,
			`INSERT INTO task_history (cluster_id, user_id, upid, status, started_at, finished_at, node)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			task.cluster, feedTestUser, upid, task.status, started, finished, task.node); err != nil {
			t.Fatalf("seed task %s: %v", task.key, err)
		}
		details, err := json.Marshal(map[string]string{"upid": upid})
		if err != nil {
			t.Fatalf("marshal details: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, action, details, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			task.cluster, feedTestUser, feedTestResourceType, task.key, task.key, details, started); err != nil {
			t.Fatalf("seed entry %s: %v", task.key, err)
		}
	}

	// 50 newer entries push every task above out of the newest 50.
	if _, err := pool.Exec(ctx,
		`INSERT INTO audit_log (cluster_id, user_id, resource_type, resource_id, action)
		 SELECT $1::uuid, $2::uuid, $3, $4, $4 FROM generate_series(1, 50)`,
		feedTestClusterA, feedTestUser, feedTestResourceType, feedTestFiller); err != nil {
		t.Fatalf("seed newer entries: %v", err)
	}

	queries := gen.New(pool)
	for _, tt := range []struct {
		name  string
		scope []uuid.UUID
		want  []string
	}{
		{"global caller", nil, []string{"finished-recently", "finished-recently-on-offline-node", "running", "running-other-cluster"}},
		{"caller scoped to the cluster", []uuid.UUID{feedTestClusterA}, []string{"finished-recently", "finished-recently-on-offline-node", "running"}},
		{"caller with no grants", []uuid.UUID{}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := queries.ListRecentAuditLogEnriched(ctx, tt.scope)
			if err != nil {
				t.Fatalf("ListRecentAuditLogEnriched: %v", err)
			}
			var got []string
			for _, r := range rows {
				if r.ResourceType != feedTestResourceType || r.Action == feedTestFiller {
					continue
				}
				got = append(got, r.Action)
				if r.Action == "running" && r.TaskStatus != "running" {
					t.Errorf("task_status = %q, want running: the row lost its task", r.TaskStatus)
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("feed holds %v, want %v", got, tt.want)
			}
		})
	}
}
