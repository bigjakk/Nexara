package rolling

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func ago(d time.Duration) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Now().Add(-d), Valid: true}
}

// TestStepTimeouts_CountFromTheLastResume: each step's timeout counts from the
// later of the step's start and the job's last resume, so a pause longer than
// the timeout no longer fails a healthy node when the job is resumed (or, for a
// restore, ends it with guests not moved back); a step that has really run that
// long still times out, and a resume from before the step began changes
// nothing. A node that came back during the pause is seen as back: the uptime
// check keeps the real time since the reboot.
func TestStepTimeouts_CountFromTheLastResume(t *testing.T) {
	t.Parallel()
	recently, never := ago(time.Minute), pgtype.Timestamptz{}
	for i, tt := range []struct {
		step       string
		resumed    pgtype.Timestamptz
		started    time.Duration // ago; three hours is past every step's timeout
		wantFailed bool
		wantWrites []string
	}{
		{"draining", recently, 3 * time.Hour, false, nil},
		{"draining", never, 3 * time.Hour, true, nil},
		{"draining", ago(4 * time.Hour), 30 * time.Minute, false, nil},
		{"upgrading", recently, 3 * time.Hour, false, nil},
		{"upgrading", never, 3 * time.Hour, true, nil},
		{"rebooting", recently, 3 * time.Hour, false, []string{"SetNodeRebootCompleted", "SetNodeHealthCheckPassed"}},
		{"rebooting", never, 3 * time.Hour, true, nil},
		{"restoring", recently, 3 * time.Hour, false, nil},
		{"restoring", never, 3 * time.Hour, false, []string{"SetNodeRestoreCompleted"}},
	} {
		t.Run(fmt.Sprintf("%s/%d", tt.step, i), func(t *testing.T) {
			t.Parallel()
			d := &memberDB{t: t, accept: map[string]int64{
				"SetNodeRestoreCompleted": 1, "SetNodeRebootCompleted": 1, "SetNodeHealthCheckPassed": 1}}
			// Back up for half an hour, inside the three hours since the reboot.
			o, client, _ := engineOn(t, d, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api2/json/nodes/pve-01/status" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"uptime":1800}}`))
					return
				}
				http.Error(w, "unexpected", http.StatusInternalServerError)
			})
			ctx := context.Background()
			job := db.RollingUpdateJob{ID: uuid.New(), Status: "running", ResumedAt: tt.resumed}
			// A fresh updated_at keeps each step's stall check from acting.
			started := ago(tt.started)
			node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01", Step: tt.step, UpdatedAt: time.Now()}
			switch tt.step {
			case "draining":
				node.DrainStartedAt = started
				o.advanceDraining(ctx, client, job, node)
			case "upgrading":
				node.UpgradeStartedAt = started
				o.advanceUpgrading(ctx, client, job, node)
			case "rebooting":
				node.RebootStartedAt = started
				o.advanceRebooting(ctx, client, job, node)
			case "restoring":
				node.RestoreStartedAt = started
				o.advanceRestoring(ctx, client, job, node)
			}
			if (len(d.failures) > 0) != tt.wantFailed || !slices.Equal(d.writes, tt.wantWrites) {
				t.Errorf("failed with %q, wrote %v; want failed %v, writes %v", d.failures, d.writes, tt.wantFailed, tt.wantWrites)
			}
		})
	}
}
