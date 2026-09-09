package notifications

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// PVE task-failure alert metrics.
//
// Nexara already collects every Proxmox task into task_history with the node,
// type and terminal status the collector derived from PVE's own exitstatus.
// Until these metrics existed, a failed vzdump landed in that table, appeared
// in the Tasks page and the activity feed, and told nobody — the whole
// notification pipeline was built and simply had nothing wired to it. These
// close that, reusing every channel, acknowledgement and maintenance window the
// engine already has.
//
// Two metrics rather than one metric with a task-type filter, because that is
// how the Veeam metrics are shaped and it needs no schema for the filter:
// pve_backup_failed is the low-noise signal almost everyone wants, and
// pve_task_failed is the broad one for an operator who wants to see any task
// failing. Both run the same evaluator.

// evaluatePVETaskFailedRule handles pve_task_failed and pve_backup_failed. The
// taskType filter is "" for any task, or a PVE worker type ("vzdump") to narrow
// it.
//
// Cluster scope only, deliberately. task_history does carry a node, so a node
// scope is buildable — but the rule stores a node UUID while the task row
// stores the node's name, and the dedupe dimension would have to be threaded
// through to keep one alert stream per node. Not worth it until someone wants
// it: "this cluster is failing tasks" is the signal, and the alert names the
// failures, so the node is one click away.
func (e *Engine) evaluatePVETaskFailedRule(
	ctx context.Context,
	rule db.AlertRule,
	windows []db.MaintenanceWindow,
	taskType string,
) error {
	if rule.ScopeType != "cluster" {
		return fmt.Errorf("%s does not support scope_type %q", rule.Metric, rule.ScopeType)
	}
	if !rule.ClusterID.Valid {
		return fmt.Errorf("%s rule %s has no cluster_id", rule.Metric, rule.ID)
	}
	if e.isInMaintenanceWindow(rule.ClusterID, pgtype.UUID{}, windows) {
		return nil
	}
	clusterID := uuidFromPgtype(rule.ClusterID)

	// The window is the rule's own duration_seconds. metricLookback floors it at
	// two minutes, which for a count-in-a-window metric means a rule left at the
	// form's default sees only the last few minutes — fine for "something is
	// failing right now", far too short for a nightly backup. The metric's help
	// text is where that gets said; here it is simply honoured.
	since := time.Now().Add(-metricLookback(rule))

	stats, err := e.queries.GetClusterFailedTaskStats(ctx, db.GetClusterFailedTaskStatsParams{
		ClusterID: clusterID,
		Since:     since,
		TaskType:  taskType,
	})
	if err != nil {
		return fmt.Errorf("cluster failed task stats: %w", err)
	}

	failed := float64(stats.FailedCount)
	label := taskFailureLabel(e.clusterLabel(ctx, clusterID), taskType, stats, metricLookback(rule))

	conditionMet := compareValue(failed, rule.Operator, rule.Threshold)
	return e.handleRuleResult(ctx, rule, conditionMet, failed, label, pgtype.UUID{}, pgtype.UUID{})
}

// taskFailureLabel builds the human sentence the alert carries.
//
// "failed or was cancelled", never a bare "failed". PVE records an operator
// who cancels a migration exactly as it records one that broke — the dev
// cluster's own history has a `qmigrate` whose entire exit status is
// "migration aborted" — and nothing in task_history distinguishes them. The
// honest phrasing is the one that admits the ambiguity; a confident "failed"
// would be wrong every time somebody pressed stop.
func taskFailureLabel(cluster, taskType string, stats db.GetClusterFailedTaskStatsRow, window time.Duration) string {
	noun, verb := "Proxmox tasks", "were cancelled"
	if stats.FailedCount == 1 {
		noun, verb = "Proxmox task", "was cancelled"
	}
	if taskType == backupTaskType {
		noun = "backups"
		if stats.FailedCount == 1 {
			noun = "backup"
		}
	}

	label := fmt.Sprintf("%s — %d %s failed or %s in the last %s",
		cluster, stats.FailedCount, noun, verb, humanizeWindow(window))

	if stats.FailedNames != "" {
		label += ": " + stats.FailedNames
		// The cap is stated, not implied. Without this, "8 backups failed: A, B,
		// C" reads as the complete list and an operator works three while five
		// more sit in the same state.
		if stats.UnnamedCount > 0 {
			label += fmt.Sprintf(" and %d more", stats.UnnamedCount)
		}
	}
	return label
}

// humanizeWindow renders the lookback the way the alert's reader thinks about
// it — "24h", not "24h0m0s", and not 86400 seconds they have to divide.
func humanizeWindow(d time.Duration) string {
	switch {
	// Days only from two upwards: "in the last 1d" reads worse than "24h", and
	// 86400 is the value the form suggests, so it is the one this renders most.
	// "7d" still beats "168h" at the other end.
	case d >= 48*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	default:
		return d.String()
	}
}
