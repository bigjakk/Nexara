package rolling

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestTick_CancelsJobsThatWereNeverStarted: every tick cancels the jobs created
// over unstartedJobTTL ago and never started (the query itself is tested in
// internal/db), and files each one under the job, so its history says why it
// ended.
func TestTick_CancelsJobsThatWereNeverStarted(t *testing.T) {
	t.Parallel()
	job, cluster := uuid.New(), uuid.New()
	d := &memberDB{t: t, audits: true, rowsFor: map[string][][]any{
		"ListRunningRollingUpdateJobs":        nil,
		"ListRollingUpdateJobsNeedingCleanup": nil,
		"ExpireUnstartedRollingUpdateJobs":    {{job, cluster}},
	}}
	o, _, _ := engineOn(t, d, nil)

	o.Tick(context.Background())

	args := d.queried["ExpireUnstartedRollingUpdateJobs"]
	if len(args) != 2 {
		t.Fatalf("the tick did not ask for the jobs never started (args %v)", args)
	}
	if cutoff, _ := args[1].(time.Time); time.Since(cutoff) < unstartedJobTTL || time.Since(cutoff) > unstartedJobTTL+time.Minute {
		t.Errorf("cancelled the jobs created before %s, want %s ago", cutoff, unstartedJobTTL)
	}
	if rows := d.rowsOf("rolling_update"); len(rows) != 1 || rows[0].resourceID != job.String() || rows[0].action != "rolling_update_expired" {
		t.Errorf("audit rows %+v, want the job's rolling_update_expired", rows)
	}
}
