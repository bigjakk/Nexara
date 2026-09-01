package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixed ids so a run that aborts before its purge leaves rows this run's
// up-front purge can find.
var (
	parkCluster = uuid.MustParse("96000000-0000-4000-8000-000000000001")
	parkTask    = uuid.MustParse("96000000-0000-4000-8000-000000000002")
)

// TestDisableScheduledTaskForBadSchedule_StopsTheClaimLoop locks the reason
// this table needs its own remedy for an unusable cron.
//
// scheduled_tasks matches `next_run_at IS NULL OR next_run_at <= now()`, so a
// NULL next_run_at means DUE NOW here — the opposite of report_schedules,
// whose bare `next_run_at <= now()` makes NULL inert. That asymmetry is what
// makes "just write NULL and move on" wrong on this table: a schedule that can
// never fire has no future timestamp to write, so the row would be claimed,
// RUN, and re-queued on every tick, repeating whatever action it carries — a
// snapshot, or a reboot.
//
// Two properties, in the order that makes the second meaningful:
//
//  1. A NULL next_run_at really is claimed. Without this the second assertion
//     would pass against a row that was never due to begin with.
//  2. After the disable, the same row is not claimed — and the reason why is
//     recorded on it rather than left for someone to infer.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database — never the
// live nexara DB).
func TestDisableScheduledTaskForBadSchedule_StopsTheClaimLoop(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		// scheduled_tasks is ON DELETE CASCADE from clusters.
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, parkCluster)
	}
	purge()
	defer purge()

	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		 VALUES ($1, 'park-fixture', 'https://pve.invalid:8006', 'root@pam!t', 'enc')`,
		parkCluster); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	q := gen.New(pool)

	// "0 8 31 4 *" is the shape at issue: it parses, so it reached the database
	// before ValidateCron learned to reject it, and it can never fire — so
	// there is no future next_run_at to write for it.
	if _, err := pool.Exec(ctx,
		`INSERT INTO scheduled_tasks
		   (id, cluster_id, resource_type, resource_id, node, action, schedule, params, enabled, next_run_at)
		 VALUES ($1, $2, 'vm', '100', 'pve1', 'reboot', '0 8 31 4 *', '{}', true, NULL)`,
		parkTask, parkCluster); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	claim := func() []gen.ScheduledTask {
		t.Helper()
		rows, err := q.ClaimDueTasks(ctx, gen.ClaimDueTasksParams{
			StaleSeconds: 600,
			GuardSeconds: 600,
		})
		if err != nil {
			t.Fatalf("ClaimDueTasks: %v", err)
		}
		var mine []gen.ScheduledTask
		for _, r := range rows {
			if r.ID == parkTask {
				mine = append(mine, r)
			}
		}
		return mine
	}

	// Property 1: NULL is due-now on this table. This is the trap.
	if got := claim(); len(got) != 1 {
		t.Fatalf("a task with next_run_at NULL was claimed %d times, want 1 — "+
			"if this is 0 the due predicate changed and the rest of this test proves nothing", len(got))
	}

	// The claim bumped next_run_at to now()+guard, which is what the real
	// completion path then overwrites. Put it back to the state an unusable
	// schedule leaves behind, so the disable is exercised against it.
	if _, err := pool.Exec(ctx,
		`UPDATE scheduled_tasks SET next_run_at = NULL, last_status = NULL WHERE id = $1`,
		parkTask); err != nil {
		t.Fatalf("reset next_run_at: %v", err)
	}

	// Property 2: after parking, the same row is inert.
	// 'success' on purpose: the run that triggered the parking may have done
	// its job perfectly and still have an expression that can never come round
	// again. last_status describes the RUN, so recording 'failed' here would
	// point the operator at the wrong half of the problem.
	if err := q.DisableScheduledTaskForBadSchedule(ctx, gen.DisableScheduledTaskForBadScheduleParams{
		ID:         parkTask,
		LastStatus: pgtype.Text{String: "success", Valid: true},
		LastError:  pgtype.Text{String: `disabled: cron expression never comes round: "0 8 31 4 *"`, Valid: true},
	}); err != nil {
		t.Fatalf("DisableScheduledTaskForBadSchedule: %v", err)
	}

	if got := claim(); len(got) != 0 {
		t.Errorf("a parked task was claimed %d times, want 0 — it would re-run its action every tick", len(got))
	}

	row, err := q.GetScheduledTask(ctx, parkTask)
	if err != nil {
		t.Fatalf("GetScheduledTask: %v", err)
	}
	if row.Enabled {
		t.Error("parked task is still enabled")
	}
	if !row.LastError.Valid || row.LastError.String == "" {
		t.Error("parked task carries no last_error; the operator has no way to see why it stopped")
	}
	if row.LastStatus.String != "success" {
		t.Errorf("parked task last_status = %q, want the run's own outcome (%q) — "+
			"parking is about the schedule, not about how the run went",
			row.LastStatus.String, "success")
	}
	// NULL rather than a far-future sentinel: while disabled it cannot match
	// the due predicate anyway, and it means that fixing the expression and
	// re-enabling runs the task once, promptly, rather than waiting for a slot
	// the old expression never had.
	if row.NextRunAt.Valid {
		t.Errorf("parked task next_run_at = %v, want NULL", row.NextRunAt.Time)
	}
}
