package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/audit/audittest"
)

// TestScheduledTaskReachesSyslog: the entry for a task a schedule started goes
// through audit.Write, so it reaches a configured collector.
func TestScheduledTaskReachesSyslog(t *testing.T) {
	t.Parallel()
	pub, next := audittest.Collector(t)
	s := newRecordingScheduler(&taskRunRecorder{})
	s.eventPub = pub

	s.trackTask(context.Background(), trackTaskParams{
		ClusterID: uuid.New(), Node: "pve-01", ResourceType: "vm", ResourceID: "100",
		Action: "snapshot", UPID: "UPID:pve-01:00000001:00000002:00000003:qmsnapshot:100:root@pam:",
	})

	if line := next(5 * time.Second); !strings.Contains(line, `action="snapshot"`) {
		t.Errorf("collector received %q, want the snapshot entry", line)
	}
}
