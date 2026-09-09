package notifications

import (
	"strings"
	"testing"
	"time"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func TestTaskFailureLabel(t *testing.T) {
	tests := []struct {
		name     string
		taskType string
		stats    db.GetClusterFailedTaskStatsRow
		window   time.Duration
		want     string
	}{
		{
			name:     "backups, plural, with a named list",
			taskType: backupTaskType,
			stats: db.GetClusterFailedTaskStatsRow{
				FailedCount: 2,
				FailedNames: "backup linux01, backup win02",
			},
			window: 24 * time.Hour,
			want:   "cluster01 — 2 backups failed or were cancelled in the last 24h: backup linux01, backup win02",
		},
		{
			// Singular has to agree in both halves — "1 backups ... were" is
			// the kind of thing that makes an alert look untrustworthy.
			name:     "backups, singular agrees in noun and verb",
			taskType: backupTaskType,
			stats:    db.GetClusterFailedTaskStatsRow{FailedCount: 1, FailedNames: "backup linux01"},
			window:   24 * time.Hour,
			want:     "cluster01 — 1 backup failed or was cancelled in the last 24h: backup linux01",
		},
		{
			name:     "any task type uses the generic noun",
			taskType: "",
			stats:    db.GetClusterFailedTaskStatsRow{FailedCount: 3, FailedNames: "migrate win02"},
			window:   time.Hour,
			want:     "cluster01 — 3 Proxmox tasks failed or were cancelled in the last 1h: migrate win02",
		},
		{
			// The cap must announce itself. "8 backups failed: A, B, C" with no
			// ellipsis reads as the complete list, and five more go unworked.
			name:     "a truncated list says how many it left out",
			taskType: backupTaskType,
			stats: db.GetClusterFailedTaskStatsRow{
				FailedCount:  8,
				FailedNames:  "backup linux01, backup linux02, backup linux03",
				UnnamedCount: 5,
			},
			window: 24 * time.Hour,
			want:   "cluster01 — 8 backups failed or were cancelled in the last 24h: backup linux01, backup linux02, backup linux03 and 5 more",
		},
		{
			name:     "no names still yields a usable sentence",
			taskType: backupTaskType,
			stats:    db.GetClusterFailedTaskStatsRow{FailedCount: 0},
			window:   24 * time.Hour,
			want:     "cluster01 — 0 backups failed or were cancelled in the last 24h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := taskFailureLabel("cluster01", tt.taskType, tt.stats, tt.window)
			if got != tt.want {
				t.Errorf("taskFailureLabel()\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestTaskFailureLabelAdmitsCancellation pins the wording. PVE records an
// operator-cancelled task identically to one that broke — task_history cannot
// tell them apart — so the copy must not claim more than the data supports.
func TestTaskFailureLabelAdmitsCancellation(t *testing.T) {
	for _, taskType := range []string{"", backupTaskType} {
		for _, count := range []int64{1, 2} {
			label := taskFailureLabel("cluster01", taskType,
				db.GetClusterFailedTaskStatsRow{FailedCount: count}, time.Hour)
			if !strings.Contains(label, "cancelled") {
				t.Errorf("label %q drops the cancellation caveat", label)
			}
		}
	}
}

func TestHumanizeWindow(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{24 * time.Hour, "24h"},
		{48 * time.Hour, "2d"},
		{7 * 24 * time.Hour, "7d"},
		{time.Hour, "1h"},
		{6 * time.Hour, "6h"},
		{5 * time.Minute, "5m"},
		{2 * time.Minute, "2m"},
		// 25h is a day and an hour, and neither unit divides it — falling back
		// beats rendering "1d" and losing an hour of window.
		{25 * time.Hour, "25h"},
		{90 * time.Second, "1m30s"},
	}
	for _, tt := range tests {
		if got := humanizeWindow(tt.in); got != tt.want {
			t.Errorf("humanizeWindow(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestPendingDurationZeroForWindowedMetrics is the regression test for the bug
// that made these metrics unable to fire at all.
//
// duration_seconds does double duty in the engine: for a level metric it is how
// long the threshold must hold, and handleRuleResult only transitions
// pending→firing once time.Since(PendingAt) reaches it. The windowed metrics
// reuse the same field as their measurement WINDOW — so with a non-zero pending
// timer, the alert goes pending at the tick that spots the failure and would
// fire D later, by which point the failure has already left the window
// (pending > finished, so pending+D > finished+D). The condition is false by
// then and the alert auto-resolves, having never fired.
//
// This shipped past a green test suite and was caught in the browser: the alert
// row sat state=pending with current_value=1 and fired_at NULL, for a rule
// whose duration was 30 days.
func TestPendingDurationZeroForWindowedMetrics(t *testing.T) {
	for _, metric := range []string{MetricPVETaskFailed, MetricPVEBackupFailed} {
		rule := db.AlertRule{Metric: metric, DurationSeconds: 86400}
		if got := pendingDuration(rule); got != 0 {
			t.Errorf("pendingDuration(%s, 86400s) = %v, want 0 — a windowed "+
				"metric that waits out its own window can never fire", metric, got)
		}
	}
}

// TestPendingDurationUnchangedForLevelMetrics pins the other half: the fix must
// not turn every CPU rule into an instant page, which is exactly what the
// pending delay exists to prevent.
func TestPendingDurationLevelMetricsUnchanged(t *testing.T) {
	tests := []struct {
		metric  string
		seconds int32
		want    time.Duration
	}{
		{"cpu_usage", 300, 5 * time.Minute},
		{"mem_percent", 86400, 24 * time.Hour},
		{"veeam_job_failed", 600, 10 * time.Minute},
		{"snapshot_age_days", 0, 0},
	}
	for _, tt := range tests {
		rule := db.AlertRule{Metric: tt.metric, DurationSeconds: tt.seconds}
		if got := pendingDuration(rule); got != tt.want {
			t.Errorf("pendingDuration(%s, %ds) = %v, want %v", tt.metric, tt.seconds, got, tt.want)
		}
	}
}
