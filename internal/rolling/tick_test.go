package rolling

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestTick_AClusterThatHangsHoldsBackNoOther: the tick runs each running job's
// pass, and each cleanup release, on its cluster and waits for none of them, so
// one cluster that hangs (a drain waits up to 30 minutes on a task) holds back
// no other cluster's job. A busy cluster is not started again, and a job that
// its last pass ended after the listing is not passed again.
func TestTick_AClusterThatHangsHoldsBackNoOther(t *testing.T) {
	t.Parallel()
	jobOn := func(status string) db.RollingUpdateJob {
		return db.RollingUpdateJob{ID: uuid.New(), ClusterID: uuid.New(), Status: status, DrsWasEnabled: true}
	}
	hangs, answers, ended, held := jobOn("running"), jobOn("running"), jobOn("failed"), jobOn("failed")
	held.DrsWasEnabled, held.NativeCrsPaused = false, true // so its release needs its cluster
	listed := ended
	listed.Status = "running"

	release := make(chan struct{})
	reached := make(chan uuid.UUID, 16)
	var mu sync.Mutex
	asked := map[uuid.UUID]int{}
	d := &memberDB{t: t,
		rowsFor: map[string][][]any{
			"ListRunningRollingUpdateJobs":        {columnsOf(hangs), columnsOf(answers), columnsOf(listed)},
			"ListRollingUpdateJobsNeedingCleanup": {columnsOf(held)},
			"ListRollingUpdateNodes":              nil,
			"ExpireUnstartedRollingUpdateJobs":    nil,
		},
		rowFor: map[string]any{
			"GetRollingUpdateJob": func(args []any) pgx.Row {
				for _, j := range []db.RollingUpdateJob{hangs, answers, ended, held} {
					if j.ID == args[0] {
						return structRow{j}
					}
				}
				return rowErr{pgx.ErrNoRows}
			},
			"IncrementJobCleanupAttempts": struct{ Attempts int32 }{1},
			// No client can be built; two clusters first hang until released.
			"GetCluster": func(args []any) pgx.Row {
				cluster, _ := args[0].(uuid.UUID)
				mu.Lock()
				asked[cluster]++
				mu.Unlock()
				reached <- cluster
				if cluster != answers.ClusterID {
					<-release
				}
				return rowErr{errors.New("unreachable")}
			},
		},
	}
	o, _, _ := engineOn(t, d, nil)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() { unblock(); o.passes.Wait() })

	tick := func() {
		t.Helper()
		done := make(chan struct{})
		go func() { o.Tick(context.Background()); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the tick waited for a cluster that hangs")
		}
	}

	tick()
	for c := uuid.Nil; c != answers.ClusterID; {
		select {
		case c = <-reached:
		case <-time.After(10 * time.Second):
			t.Fatal("the cluster that answers was not passed while the others hang")
		}
	}
	tick() // Both still hang.
	unblock()
	o.passes.Wait()
	tick()
	o.passes.Wait()

	mu.Lock()
	defer mu.Unlock()
	if asked[hangs.ClusterID] != 2 || asked[held.ClusterID] != 2 || asked[answers.ClusterID] < 2 || asked[ended.ClusterID] != 0 {
		t.Errorf("passes per cluster: hung job %d, hung release %d (want 2 each: not again while busy), answering %d (want 2+), ended %d (want 0)",
			asked[hangs.ClusterID], asked[held.ClusterID], asked[answers.ClusterID], asked[ended.ClusterID])
	}
}
