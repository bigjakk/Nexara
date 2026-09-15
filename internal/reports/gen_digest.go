package reports

import (
	"context"
	"fmt"
	"sort"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Cluster digest: the one-page "how was the period" for a reader who will not
// open six reports. It runs the other generators into scratch reports and
// lifts their headline figures and findings, so every number here is computed
// exactly as the report it came from computes it, then adds the two things
// no other report covers: alerts and tasks.

func (g *Generator) generateClusterDigest(ctx context.Context, gc genCtx, data *ReportData) error {
	data.Subtitle = "One page across availability, load, backups, alerts, tasks, snapshots and patching for the period."

	sub := func(rt ReportType, fn func(context.Context, genCtx, *ReportData) error) (*ReportData, error) {
		d := g.newReport(rt, gc)
		if err := fn(ctx, gc, d); err != nil {
			return nil, fmt.Errorf("%s: %w", rt.Name(), err)
		}
		sortFindings(d.Findings)
		return d, nil
	}
	uptime, err := sub(TypeUptimeSummary, g.generateUptimeSummary)
	if err != nil {
		return err
	}
	resources, err := sub(TypeResourceUtilization, g.generateResourceUtilization)
	if err != nil {
		return err
	}
	backup, err := sub(TypeBackupCompliance, g.generateBackupCompliance)
	if err != nil {
		return err
	}
	snapshots, err := sub(TypeSnapshotInventory, g.generateSnapshotInventory)
	if err != nil {
		return err
	}
	patch, err := sub(TypePatchStatus, g.generatePatchStatus)
	if err != nil {
		return err
	}

	vms, err := g.queries.ListVMsByCluster(ctx, gc.cluster.ID)
	if err != nil {
		return fmt.Errorf("list VMs: %w", err)
	}
	var guests, running int
	for _, vm := range vms {
		if vm.Template {
			continue
		}
		guests++
		if vm.Status == "running" {
			running++
		}
	}

	alertRows, err := g.queries.CountAlertHistoryBySeverityInWindow(ctx, db.CountAlertHistoryBySeverityInWindowParams{ClusterID: gc.cluster.ID, Since: gc.since, Until: gc.now})
	if err != nil {
		return fmt.Errorf("count alerts: %w", err)
	}
	topRules, err := g.queries.ListTopAlertRulesInWindow(ctx, db.ListTopAlertRulesInWindowParams{ClusterID: gc.cluster.ID, Since: gc.since, Until: gc.now, RowLimit: 8})
	if err != nil {
		return fmt.Errorf("list top alert rules: %w", err)
	}
	taskCounts, err := g.queries.CountTaskHistoryByStatusInWindow(ctx, db.CountTaskHistoryByStatusInWindowParams{ClusterID: gc.cluster.ID, Since: gc.since, Until: gc.now})
	if err != nil {
		return fmt.Errorf("count tasks: %w", err)
	}
	failedTasks, err := g.queries.ListFailedTaskHistoryInWindow(ctx, db.ListFailedTaskHistoryInWindowParams{ClusterID: gc.cluster.ID, Since: gc.since, Until: gc.now, RowLimit: 10})
	if err != nil {
		return fmt.Errorf("list failed tasks: %w", err)
	}

	buildClusterDigest(digestInput{
		guests: guests, running: running,
		uptime: uptime, resources: resources, backup: backup, snapshots: snapshots, patch: patch,
		alerts: alertRows, topRules: topRules, taskCounts: taskCounts, failedTasks: failedTasks,
	}, data)
	return nil
}

type digestInput struct {
	guests, running                             int
	uptime, resources, backup, snapshots, patch *ReportData
	alerts                                      []db.CountAlertHistoryBySeverityInWindowRow
	topRules                                    []db.ListTopAlertRulesInWindowRow
	taskCounts                                  []db.CountTaskHistoryByStatusInWindowRow
	failedTasks                                 []db.TaskHistory
}

func buildClusterDigest(in digestInput, data *ReportData) {
	// Findings: every sub-report's, attributed to the report it came from.
	lift := func(d *ReportData) {
		for _, f := range d.Findings {
			where := d.Kicker
			if f.Where != "" {
				where += " · " + f.Where
			}
			data.Findings = append(data.Findings, Finding{Severity: f.Severity, Lead: f.Lead, Text: f.Text, Where: where})
		}
	}
	for _, d := range []*ReportData{in.uptime, in.resources, in.backup, in.snapshots, in.patch} {
		lift(d)
	}

	// Alerts.
	var fired, openAlerts, criticalFired int64
	alertRows := make([][]Cell, 0, len(in.alerts))
	for _, a := range in.alerts {
		fired += a.Fired
		openAlerts += a.OpenCount
		if a.Severity == "critical" {
			criticalFired += a.Fired
		}
		alertRows = append(alertRows, []Cell{pill(a.Severity, severityTone(a.Severity)), num(a.Fired), num(a.Resolved), num(a.OpenCount)})
	}
	if criticalFired > 0 {
		data.addFinding(SevSerious, fmt.Sprintf("%s fired in the period", plural(int(criticalFired), "critical alert", "critical alerts")), fmt.Sprintf("(%d of all alerts still open).", openAlerts), "Alerts")
	} else if openAlerts > 0 {
		data.addFinding(SevWarning, fmt.Sprintf("%s still open", pluralAre(int(openAlerts), "alert")), "at the end of the period.", "Alerts")
	}
	ruleRows := make([][]Cell, 0, len(in.topRules))
	for _, r := range in.topRules {
		ruleRows = append(ruleRows, []Cell{strong(r.Name), mute(r.Metric), pill(r.Severity, severityTone(r.Severity)), num(r.Fired)})
	}

	// Tasks.
	var tasksTotal, tasksFailed int64
	statusRows := make([][]Cell, 0, len(in.taskCounts))
	for _, c := range in.taskCounts {
		tasksTotal += c.N
		if c.Status == "failed" {
			tasksFailed = c.N
		}
		statusRows = append(statusRows, []Cell{statusPillTask(c.Status), num(c.N)})
	}
	if tasksFailed > 0 {
		names := make([]string, 0, len(in.failedTasks))
		for _, t := range in.failedTasks {
			if t.Description != "" {
				names = append(names, t.Description)
			} else {
				names = append(names, t.TaskType)
			}
		}
		data.addFinding(SevWarning, fmt.Sprintf("%d of %d tasks failed in the period:", tasksFailed, tasksTotal), nameList(dedupe(names), 5)+".", "Tasks")
	}
	failedRows := make([][]Cell, 0, len(in.failedTasks))
	for _, t := range in.failedTasks {
		when := t.StartedAt
		if t.FinishedAt.Valid {
			when = t.FinishedAt.Time
		}
		desc := t.Description
		if desc == "" {
			desc = t.TaskType
		}
		failedRows = append(failedRows, []Cell{mute(ts(when)), strong(desc), text(t.TaskType), text(t.Node), mute(t.ExitStatus)})
	}

	// KPIs: attention first, then one figure per domain lifted from the
	// report that owns it.
	var critical, serious int
	for _, f := range data.Findings {
		switch f.Severity {
		case SevCritical:
			critical++
		case SevSerious:
			serious++
		}
	}
	attention := KPI{Label: "Findings needing attention", Value: fmt.Sprint(critical + serious), Hero: true, Detail: fmt.Sprintf("%d critical · %d serious · %d in total", critical, serious, len(data.Findings))}
	switch {
	case critical > 0:
		attention.Tone = ToneCrit
	case serious > 0:
		attention.Tone = ToneWarn
	default:
		attention.Tone = ToneGood
	}
	data.KPIs = append(data.KPIs, attention)
	if k := heroKPI(in.uptime); k != nil {
		data.KPIs = append(data.KPIs, KPI{Label: "Nodes online", Value: k.Value, Tone: k.Tone})
	}
	data.KPIs = append(data.KPIs, KPI{Label: "Guests running", Value: fmt.Sprintf("%d/%d", in.running, in.guests)})
	if k := heroKPI(in.resources); k != nil {
		data.KPIs = append(data.KPIs, KPI{Label: "CPU, period average", Value: k.Value, Unit: k.Unit, Tone: k.Tone})
	}
	if k := heroKPI(in.backup); k != nil {
		// Warn only when there are targets to be behind on: a cluster with
		// no backup targets reads 0 % and that is not a warning.
		targets := kpiByLabel(in.backup, "Backup targets")
		tone := ""
		if targets != nil && targets.Value != "0" && k.Value != "100" {
			tone = ToneWarn
		}
		data.KPIs = append(data.KPIs, KPI{Label: "Backups current", Value: k.Value, Unit: k.Unit, Detail: "of backup targets", Tone: tone})
	}
	data.KPIs = append(data.KPIs,
		KPI{Label: "Alerts fired", Value: fmt.Sprint(fired), Detail: fmt.Sprintf("%d still open", openAlerts), Tone: toneIf(criticalFired > 0, ToneCrit)},
		KPI{Label: "Tasks failed", Value: fmt.Sprint(tasksFailed), Detail: fmt.Sprintf("of %d that ended", tasksTotal), Tone: toneIf(tasksFailed > 0, ToneWarn)},
	)
	if k := heroKPI(in.patch); k != nil && k.Value != "none" {
		data.KPIs = append(data.KPIs, KPI{Label: "Vulnerabilities", Value: k.Value, Tone: k.Tone})
	}

	// Sections.
	availability := []Block{}
	if c := firstChart(in.resources, ChartLines); c != nil {
		availability = append(availability, chartBlock("Cluster load per day", "Mean CPU and memory of the nodes that reported.", c))
	}
	if t := firstTable(in.uptime); t != nil {
		availability = append(availability, tableBlock("Nodes", "", t, "No nodes in this cluster."))
	}
	data.addSection("Availability and load", "", availability...)

	backupBlocks := []Block{}
	if c := firstChart(in.backup, ChartStackedBar); c != nil {
		backupBlocks = append(backupBlocks, chartBlock("Backup targets by coverage state", "", c))
	}
	backupBlocks = append(backupBlocks, tableBlock("Backup figures", "", kpiTable(in.backup), ""))
	data.addSection("Backups", "As the backup compliance report computes them.", backupBlocks...)

	data.addSection("Alerts in the period", "Alerts raised in the period, by severity, and the rules that raised them most.",
		tableBlock("By severity", "", &Table{Columns: []Column{col("Severity"), numCol("Fired"), numCol("Resolved"), numCol("Open")}, Rows: alertRows}, "No alerts fired in the period."),
		tableBlock("Noisiest rules", "", &Table{Columns: []Column{col("Rule"), col("Metric"), col("Severity"), numCol("Fired")}, Rows: ruleRows}, "No alerts fired in the period."))
	data.addSection("Tasks in the period", "Everything Proxmox ran on the cluster, by outcome, and the failures.",
		tableBlock("By outcome", "", &Table{Columns: []Column{col("Status"), numCol("Tasks")}, Rows: statusRows}, "No tasks ended in the period."),
		tableBlock("Failed tasks", "Newest first, up to ten.", &Table{Columns: []Column{nowrapCol("When (UTC)"), col("Task"), col("Type"), col("Node"), col("Exit")}, Rows: failedRows}, "No task failed in the period."))
	data.addSection("Snapshots and patching", "",
		tableBlock("Snapshots", "", kpiTable(in.snapshots), ""),
		tableBlock("Patch status", "", kpiTable(in.patch), ""))
	data.Notes = []NoteGroup{{Title: "How this digest was computed", Items: []string{
		"Every figure and finding is lifted from the corresponding report type, generated for the same period with the same parameters; run that report for the detail.",
		"Alerts are counted by the time they were raised; tasks by the time they ended.",
	}}}
}

func kpiByLabel(d *ReportData, label string) *KPI {
	if d == nil {
		return nil
	}
	for i := range d.KPIs {
		if d.KPIs[i].Label == label {
			return &d.KPIs[i]
		}
	}
	return nil
}

func heroKPI(d *ReportData) *KPI {
	if d == nil {
		return nil
	}
	for i := range d.KPIs {
		if d.KPIs[i].Hero {
			return &d.KPIs[i]
		}
	}
	return nil
}

func firstTable(d *ReportData) *Table {
	if d == nil {
		return nil
	}
	for _, s := range d.Sections {
		for _, b := range s.Blocks {
			if b.Kind == BlockTable && b.Table != nil {
				return b.Table
			}
		}
	}
	return nil
}

// kpiTable turns a report's headline strip into a two-column table, for the
// digest's per-domain summaries.
func kpiTable(d *ReportData) *Table {
	t := &Table{Columns: []Column{col("Figure"), numCol("Value")}}
	if d == nil {
		return t
	}
	for _, k := range d.KPIs {
		v := k.Value + k.Unit
		c := Cell{Text: v}
		if k.Detail != "" {
			c.Sub = k.Detail
		}
		t.Rows = append(t.Rows, []Cell{text(k.Label), c})
	}
	return t
}

func statusPillTask(status string) Cell {
	switch status {
	case "completed":
		return pill("completed", ToneGood)
	case "failed":
		return pill("failed", ToneCrit)
	case "running":
		return pill("running", ToneNeutral)
	}
	return pill(status, ToneNeutral)
}

func dedupe(names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
