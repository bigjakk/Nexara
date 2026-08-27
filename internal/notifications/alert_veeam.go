package notifications

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The Veeam alert metrics. All three are inventory-backed — they read the
// tables the Veeam sync fills rather than the metric hypertables — so
// evaluateRule dispatches to them before its scope switch, the way
// snapshot_age_days does.
//
// veeam_job_failed is deliberately absent, and belongs with job control rather
// than here. A job cancelled through the API is recorded by Veeam as result
// "Failed" with isCanceled false and an empty session log — there is nothing
// in the data that distinguishes it from a real failure. Shipping the rule now
// would mean it fires every time an operator stops a job from Nexara, so it
// waits for the phase that records nexara_initiated on the session and can
// suppress those.

// inclusiveOperator reports whether a rule's comparison includes the threshold
// itself, so the "N over threshold" figure in an alert message is counted the
// same way the alert fired.
func inclusiveOperator(operator string) bool {
	return operator == ">=" || operator == "<=" || operator == "==" || operator == "!="
}

// exceedsThreshold reports whether the rule asks "is this too HIGH". Only such
// a rule can be satisfied by a state that is worse than any number — a guest
// Veeam knows about with no restore points left has no age to compare, but it
// is unambiguously past any "older than" threshold.
func exceedsThreshold(operator string) bool {
	return operator == ">" || operator == ">="
}

// evaluateVeeamRPORule handles veeam_rpo_hours: how long ago the newest
// restore point was taken.
//
// Only guests Veeam actually has a backup object for are evaluated. A guest
// Veeam was never meant to protect has no recovery point objective, and
// measuring one for it would fire this rule for every unrelated VM on the
// cluster — "should this guest be backed up at all" is the coverage report's
// question, not this one's.
func (e *Engine) evaluateVeeamRPORule(ctx context.Context, rule db.AlertRule, windows []db.MaintenanceWindow) error {
	if !rule.ClusterID.Valid {
		return fmt.Errorf("veeam_rpo_hours rule %s has no cluster_id", rule.ID)
	}
	if e.isInMaintenanceWindow(rule.ClusterID, pgtype.UUID{}, windows) {
		return nil
	}
	clusterID := uuidFromPgtype(rule.ClusterID)

	switch rule.ScopeType {
	case "cluster":
		stats, err := e.queries.GetClusterVeeamRPOStats(ctx, db.GetClusterVeeamRPOStatsParams{
			ClusterID:      clusterID,
			ThresholdHours: rule.Threshold,
			Inclusive:      inclusiveOperator(rule.Operator),
		})
		if err != nil {
			return fmt.Errorf("cluster veeam rpo stats: %w", err)
		}
		// Veeam protects nothing on this cluster, so there is no RPO to
		// judge. Condition false, which also resolves a firing alert once an
		// operator removes the last job — rather than pinning it at whatever
		// the final reading was.
		if stats.ProtectedCount == 0 {
			return e.handleRuleResult(ctx, rule, false, 0, e.clusterLabel(ctx, clusterID), pgtype.UUID{}, pgtype.UUID{})
		}

		label := fmt.Sprintf("%s — oldest recovery point %.1fh on guest %d, %d of %d guests over threshold",
			e.clusterLabel(ctx, clusterID), stats.WorstRpoHours, stats.WorstVmid,
			stats.OverCount, stats.ProtectedCount)

		conditionMet := compareValue(stats.WorstRpoHours, rule.Operator, rule.Threshold)
		if stats.UnrecoverableCount > 0 {
			// A guest Veeam knows about whose restore points have ALL been
			// pruned is worse than any RPO, and it has no age to compare — so
			// it satisfies an "older than" rule outright.
			//
			// This is not a nicety. Without it, a cluster where two of three
			// protected guests have lost everything reports the surviving
			// guest's healthy 2h and the alert stays green; and if ALL of them
			// lose their points, worst_rpo_hours COALESCEs to 0 and the
			// dashboard reads a perfect zero-hour RPO over a cluster where
			// nothing is restorable at all.
			//
			// Only for "too high" comparisons: an operator asking for an RPO
			// BELOW some value has not asked about this at all.
			if exceedsThreshold(rule.Operator) {
				conditionMet = true
			}
			label += fmt.Sprintf("; %d have NO restore points left at all", stats.UnrecoverableCount)
		}
		return e.handleRuleResult(ctx, rule, conditionMet, stats.WorstRpoHours, label, pgtype.UUID{}, pgtype.UUID{})

	case "vm":
		if !rule.VmVmid.Valid {
			return fmt.Errorf("veeam_rpo_hours vm rule %s has no vmid", rule.ID)
		}
		// Resolve the live vms row from the stable (cluster_id, vmid)
		// identity at evaluation time — the row's UUID is minted anew
		// whenever the collector churns it.
		vm, err := e.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
			ClusterID: clusterID,
			Vmid:      rule.VmVmid.Int32,
		})
		if err != nil {
			e.logger.Debug("veeam rpo rule target not in inventory",
				"rule_id", rule.ID, "vmid", rule.VmVmid.Int32)
			return nil
		}
		vmID := pgtype.UUID{Bytes: vm.ID, Valid: true}

		stats, err := e.queries.GetGuestVeeamRPO(ctx, db.GetGuestVeeamRPOParams{
			ClusterID: clusterID,
			Vmid:      rule.VmVmid.Int32,
		})
		if err != nil {
			return fmt.Errorf("guest veeam rpo: %w", err)
		}
		if stats.ProtectedCount == 0 {
			// Veeam has no backup object for this guest at all, so there is no
			// recovery point objective to judge and firing "RPO exceeded"
			// would be a category error.
			//
			// A KNOWN gap, stated rather than stumbled into: a guest dropped
			// from its Veeam job goes quiet here, which is exactly what a
			// vm-scoped rule was named at it to catch. The alternative —
			// firing forever for a guest an operator deliberately removed from
			// Veeam — produces an alert nothing can clear. Coverage reports
			// the guest as unprotected either way, and that is the surface
			// built to answer "should this be backed up at all".
			return e.handleRuleResult(ctx, rule, false, 0, vm.Name, pgtype.UUID{}, vmID)
		}
		if !stats.HasRestorePoint {
			// Veeam knows the guest and can restore NOTHING. No age to
			// compare, and worse than any threshold — see the cluster arm.
			label := fmt.Sprintf("%s (vmid %d) — Veeam holds NO restore points for this guest", vm.Name, vm.Vmid)
			return e.handleRuleResult(ctx, rule, exceedsThreshold(rule.Operator), 0, label, pgtype.UUID{}, vmID)
		}

		label := fmt.Sprintf("%s (vmid %d) — newest Veeam restore point %.1fh old", vm.Name, vm.Vmid, stats.RpoHours)
		conditionMet := compareValue(stats.RpoHours, rule.Operator, rule.Threshold)
		return e.handleRuleResult(ctx, rule, conditionMet, stats.RpoHours, label, pgtype.UUID{}, vmID)

	default:
		return fmt.Errorf("veeam_rpo_hours does not support scope_type %q", rule.ScopeType)
	}
}

// evaluateVeeamMalwareRule handles veeam_malware_status.
//
// The verdict is read from the NEWEST restore point only. Veeam records one
// per point, and an old "Suspicious" that a later clean backup superseded is
// history — alerting on it would keep a resolved finding firing forever with
// nothing an operator could do to clear it.
func (e *Engine) evaluateVeeamMalwareRule(ctx context.Context, rule db.AlertRule, windows []db.MaintenanceWindow) error {
	if !rule.ClusterID.Valid {
		return fmt.Errorf("veeam_malware_status rule %s has no cluster_id", rule.ID)
	}
	if e.isInMaintenanceWindow(rule.ClusterID, pgtype.UUID{}, windows) {
		return nil
	}
	clusterID := uuidFromPgtype(rule.ClusterID)

	switch rule.ScopeType {
	case "cluster":
		stats, err := e.queries.GetClusterVeeamMalwareStats(ctx, db.GetClusterVeeamMalwareStatsParams{
			ClusterID:         clusterID,
			ThresholdSeverity: rule.Threshold,
			Inclusive:         inclusiveOperator(rule.Operator),
		})
		if err != nil {
			return fmt.Errorf("cluster veeam malware stats: %w", err)
		}
		if stats.ScannedCount == 0 {
			return e.handleRuleResult(ctx, rule, false, 0, e.clusterLabel(ctx, clusterID), pgtype.UUID{}, pgtype.UUID{})
		}
		label := fmt.Sprintf("%s — worst verdict %q on guest %d, %d of %d guests at or over threshold",
			e.clusterLabel(ctx, clusterID), stats.WorstStatus, stats.WorstVmid,
			stats.OverCount, stats.ScannedCount)
		conditionMet := compareValue(stats.WorstSeverity, rule.Operator, rule.Threshold)
		return e.handleRuleResult(ctx, rule, conditionMet, stats.WorstSeverity, label, pgtype.UUID{}, pgtype.UUID{})

	case "vm":
		if !rule.VmVmid.Valid {
			return fmt.Errorf("veeam_malware_status vm rule %s has no vmid", rule.ID)
		}
		vm, err := e.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
			ClusterID: clusterID,
			Vmid:      rule.VmVmid.Int32,
		})
		if err != nil {
			e.logger.Debug("veeam malware rule target not in inventory",
				"rule_id", rule.ID, "vmid", rule.VmVmid.Int32)
			return nil
		}
		vmID := pgtype.UUID{Bytes: vm.ID, Valid: true}

		stats, err := e.queries.GetGuestVeeamMalware(ctx, db.GetGuestVeeamMalwareParams{
			ClusterID: clusterID,
			Vmid:      rule.VmVmid.Int32,
		})
		if err != nil {
			return fmt.Errorf("guest veeam malware: %w", err)
		}
		if !stats.Scanned {
			return e.handleRuleResult(ctx, rule, false, 0, vm.Name, pgtype.UUID{}, vmID)
		}
		label := fmt.Sprintf("%s (vmid %d) — newest Veeam restore point verdict %q", vm.Name, vm.Vmid, stats.Status)
		conditionMet := compareValue(stats.Severity, rule.Operator, rule.Threshold)
		return e.handleRuleResult(ctx, rule, conditionMet, stats.Severity, label, pgtype.UUID{}, vmID)

	default:
		return fmt.Errorf("veeam_malware_status does not support scope_type %q", rule.ScopeType)
	}
}

// evaluateVeeamRepoRule handles veeam_repo_used_percent, the only GLOBAL-scope
// metric.
//
// One repository holds the backups of every cluster its Veeam server protects,
// so there is no cluster to attribute the number to. The alert it raises
// carries a NULL cluster_id, which the alert-history read path already treats
// as visible to holders of global view:alert only.
func (e *Engine) evaluateVeeamRepoRule(ctx context.Context, rule db.AlertRule) error {
	if rule.ScopeType != "global" {
		return fmt.Errorf("veeam_repo_used_percent does not support scope_type %q", rule.ScopeType)
	}

	stats, err := e.queries.GetVeeamRepositoryUsageStats(ctx, db.GetVeeamRepositoryUsageStatsParams{
		ThresholdPercent: rule.Threshold,
		Inclusive:        inclusiveOperator(rule.Operator),
	})
	if err != nil {
		return fmt.Errorf("veeam repository usage: %w", err)
	}
	// No repository reports a capacity — Veeam does that for targets whose
	// size it cannot measure, such as an object store. Nothing to judge.
	if stats.MeasuredCount == 0 {
		return e.handleRuleResult(ctx, rule, false, 0, "Veeam repositories", pgtype.UUID{}, pgtype.UUID{})
	}

	label := fmt.Sprintf("%s — %.1f%% used, %d of %d repositories over threshold",
		stats.FullestName, stats.FullestPercent, stats.OverCount, stats.MeasuredCount)
	conditionMet := compareValue(stats.FullestPercent, rule.Operator, rule.Threshold)
	return e.handleRuleResult(ctx, rule, conditionMet, stats.FullestPercent, label, pgtype.UUID{}, pgtype.UUID{})
}

// clusterLabel resolves a cluster's name for an alert message, falling back to
// its id. A lookup failure must not lose the alert.
func (e *Engine) clusterLabel(ctx context.Context, clusterID uuid.UUID) string {
	if cluster, err := e.queries.GetCluster(ctx, clusterID); err == nil {
		return cluster.Name
	}
	return clusterID.String()
}
