package collector

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// giveUpBranchRe finds the branch of ReconcileDispatchedScheduledTasks that
// words the collector's give-up for a schedule: the exit_status it matches and
// the hours its message states.
var giveUpBranchRe = regexp.MustCompile(`WHEN '([^']*)' THEN '[^']*after (\d+) hours[^']*'`)

// TestScheduleReconcileWordsTheCollectorsGiveUp pins the two facts the
// scheduled-task reconcile restates from this package: the exit_status a task
// is given up with (vanishedExitStatus) and how long the collector waits first
// (staleTaskGrace). The query turns the first into a last_error for the
// schedule that states the second — "… stopped following it after 24 hours".
//
// Both are copies sqlc cannot take from Go, and both drift silently: rename
// the sentinel and the schedule shows the bare word again; change the grace
// and the message states a wait that no longer happens.
func TestScheduleReconcileWordsTheCollectorsGiveUp(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../queries/scheduled_tasks.sql")
	if err != nil {
		t.Fatalf("read scheduled_tasks.sql: %v", err)
	}
	_, body, found := strings.Cut(string(raw), "-- name: ReconcileDispatchedScheduledTasks ")
	if !found {
		t.Fatal("ReconcileDispatchedScheduledTasks not found in queries/scheduled_tasks.sql — " +
			"if it was renamed, follow it here")
	}
	if next := strings.Index(body, "-- name:"); next >= 0 {
		body = body[:next]
	}
	// The statement's code only: a comment may quote either fact.
	var code strings.Builder
	for _, line := range strings.Split(body, "\n") {
		line, _, _ = strings.Cut(line, "--")
		code.WriteString(line + "\n")
	}

	m := giveUpBranchRe.FindStringSubmatch(code.String())
	if m == nil {
		t.Fatalf("ReconcileDispatchedScheduledTasks words no give-up (WHEN '<sentinel>' THEN "+
			"'… after N hours …'):\n%s", code.String())
	}
	if m[1] != vanishedExitStatus {
		t.Errorf("the reconcile words exit_status %q, but the collector gives up with %q — "+
			"the schedule would show the bare sentinel", m[1], vanishedExitStatus)
	}
	hours, err := strconv.Atoi(m[2])
	if err != nil || time.Duration(hours)*time.Hour != staleTaskGrace {
		t.Errorf("the reconcile's message says the collector gave up after %s hours, but "+
			"staleTaskGrace is %v", m[2], staleTaskGrace)
	}
}
