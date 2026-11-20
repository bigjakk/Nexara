package db

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixed ids so a run that aborts before its purge leaves rows this run's
// up-front purge can find.
var (
	claimBusyCluster = uuid.MustParse("97000000-0000-4000-8000-000000000001")
	claimFreeCluster = uuid.MustParse("97000000-0000-4000-8000-000000000002")
	claimBusyTask    = uuid.MustParse("97000000-0000-4000-8000-000000000003")
	claimFreeTask    = uuid.MustParse("97000000-0000-4000-8000-000000000004")
)

// TestClaimDueTasks_LeavesBusyClustersOut: the scheduler dispatches a cluster's runs one after
// another and claims again without waiting, so a batch on a cluster that stops answering can
// outlast the stale window. A claim must leave out the clusters still dispatching, or it would
// take that batch's unstarted rows again and run them twice; and leaving none out must take all.
func TestClaimDueTasks_LeavesBusyClustersOut(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		// scheduled_tasks is ON DELETE CASCADE from clusters.
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{claimBusyCluster, claimFreeCluster})
	}
	purge()
	defer purge()

	for _, seed := range []struct {
		name          string
		cluster, task uuid.UUID
	}{
		{"claim-busy-fixture", claimBusyCluster, claimBusyTask},
		{"claim-free-fixture", claimFreeCluster, claimFreeTask},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			 VALUES ($1, $2, 'https://pve.invalid:8006', 'root@pam!t', 'enc')`,
			seed.cluster, seed.name); err != nil {
			t.Fatalf("seed cluster: %v", err)
		}
		// next_run_at NULL is due now on this table.
		if _, err := pool.Exec(ctx,
			`INSERT INTO scheduled_tasks
			   (id, cluster_id, resource_type, resource_id, node, action, schedule, params, enabled, next_run_at)
			 VALUES ($1, $2, 'vm', '100', 'pve-01', 'reboot', '0 2 * * *', '{}', true, NULL)`,
			seed.task, seed.cluster); err != nil {
			t.Fatalf("seed task: %v", err)
		}
	}

	q := gen.New(pool)
	claim := func(busy []uuid.UUID) []uuid.UUID {
		t.Helper()
		rows, err := q.ClaimDueTasks(ctx, gen.ClaimDueTasksParams{StaleSeconds: 600, GuardSeconds: 600, BusyClusterIds: busy})
		if err != nil {
			t.Fatalf("ClaimDueTasks: %v", err)
		}
		var mine []uuid.UUID
		for _, r := range rows {
			if r.ID == claimBusyTask || r.ID == claimFreeTask {
				mine = append(mine, r.ID)
			}
		}
		return mine
	}

	if got := claim([]uuid.UUID{claimBusyCluster}); !slices.Equal(got, []uuid.UUID{claimFreeTask}) {
		t.Fatalf("a claim leaving the busy cluster out took %v, want only the other cluster's task %v", got, claimFreeTask)
	}
	if got := claim(nil); !slices.Equal(got, []uuid.UUID{claimBusyTask}) {
		t.Errorf("a claim leaving nothing out took %v, want the busy cluster's task still due %v", got, claimBusyTask)
	}
}
