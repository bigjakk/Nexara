package reports

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// --- Snapshot inventory ---

// generateSnapshotInventory reports the cluster's guest snapshot inventory
// (collected by the snapshot sync loop): an age distribution plus the oldest
// snapshots, so forgotten snapshots surface in scheduled digests. It is a
// point-in-time report — the period is context only. Ages come from
// snap_time (unix seconds); rows with snap_time = 0 have unknown age and are
// counted separately, never aged from epoch 0.
func (g *Generator) generateSnapshotInventory(ctx context.Context, gc genCtx, data *ReportData) error {
	rows, err := g.queries.ListGuestSnapshotsForReport(ctx, gc.cluster.ID)
	if err != nil {
		return fmt.Errorf("list guest snapshots: %w", err)
	}
	buildSnapshotInventory(gc, rows, data)
	return nil
}

func buildSnapshotInventory(gc genCtx, rows []db.ListGuestSnapshotsForReportRow, data *ReportData) {
	warnDays := gc.params.SnapshotWarnDays
	if warnDays <= 0 {
		warnDays = 7
	}
	now := gc.now.Unix()
	guests := map[int32]bool{}
	var overWarn, over30, unknown, ram int
	buckets := []struct {
		label string
		upTo  float64
	}{{"< 1 d", 1}, {"1–7 d", 7}, {"7–30 d", 30}, {"30–90 d", 90}, {"> 90 d", 1e9}}
	hist := make([]float64, len(buckets)+1)
	var oldNames []string
	byGuest := map[int32]int{}
	for _, row := range rows {
		guests[row.Vmid] = true
		byGuest[row.Vmid]++
		if row.Vmstate {
			ram++
		}
		if row.SnapTime <= 0 {
			unknown++
			hist[len(hist)-1]++
			continue
		}
		days := float64(now-row.SnapTime) / 86400
		if days < 0 {
			days = 0
		}
		for i, b := range buckets {
			if days < b.upTo {
				hist[i]++
				break
			}
		}
		if days > 30 {
			over30++
		}
		if days > float64(warnDays) {
			overWarn++
			if len(oldNames) < 6 {
				oldNames = append(oldNames, fmt.Sprintf("%s on %s (%dd)", row.Name, snapshotGuest(row), int(days)))
			}
		}
	}
	var crowded []string
	for vmid, n := range byGuest {
		if n >= 5 {
			crowded = append(crowded, fmt.Sprintf("#%d (%d)", vmid, n))
		}
	}
	sort.Strings(crowded)

	data.Subtitle = "Every guest snapshot on the cluster, by age, with the ones most likely to have been forgotten."
	data.addMeta("Scope", fmt.Sprintf("%s across %s", plural(len(rows), "snapshot", "snapshots"), plural(len(guests), "guest", "guests")))
	data.addMeta("Age rule", fmt.Sprintf("Flagged after %d days (parameter snapshot_warn_days)", warnDays))
	data.KPIs = []KPI{
		{Label: "Snapshots", Value: fmt.Sprint(len(rows)), Hero: true, Detail: fmt.Sprintf("on %s", plural(len(guests), "guest", "guests"))},
		{Label: fmt.Sprintf("Older than %d days", warnDays), Value: fmt.Sprint(overWarn), Tone: toneIf(overWarn > 0, ToneWarn)},
		{Label: "Older than 30 days", Value: fmt.Sprint(over30), Tone: toneIf(over30 > 0, ToneCrit)},
		{Label: "With RAM state", Value: fmt.Sprint(ram), Detail: "QEMU snapshots that include memory"},
		{Label: "Unknown age", Value: fmt.Sprint(unknown), Detail: "Proxmox reported no creation time"},
	}
	if over30 > 0 {
		data.addFinding(SevSerious, fmt.Sprintf("%s older than 30 days.", pluralAre(over30, "snapshot")), "Long-lived snapshots slow the guest and grow until they are removed.", "Oldest snapshots")
	}
	if overWarn > over30 {
		data.addFinding(SevWarning, fmt.Sprintf("%s older than %d days:", pluralAre(overWarn-over30, "snapshot"), warnDays), nameList(oldNames, 5)+".", "Oldest snapshots")
	} else if overWarn > 0 {
		data.addFinding(SevInfo, "Oldest snapshots:", nameList(oldNames, 5)+".", "Oldest snapshots")
	}
	if len(crowded) > 0 {
		data.addFinding(SevInfo, fmt.Sprintf("%s five or more snapshots:", pluralHave(len(crowded), "guest")), nameList(crowded, 6)+".", "Oldest snapshots")
	}

	labels := make([]string, 0, len(buckets)+1)
	for _, b := range buckets {
		labels = append(labels, b.label)
	}
	labels = append(labels, "unknown")
	thresholdIdx := 1
	for i, b := range buckets {
		if b.upTo <= float64(warnDays) {
			thresholdIdx = i
		}
	}
	oldest := &Table{Columns: []Column{nowrapCol("Guest"), nowrapCol("VMID"), col("Type"), col("Snapshot"), numCol("Age"), col("RAM"), col("Node"), col("Description")}}
	const maxOldestRows = 25
	for i, row := range rows {
		if i >= maxOldestRows {
			break
		}
		age := mute("unknown")
		if row.SnapTime > 0 {
			days := float64(now-row.SnapTime) / 86400
			if days < 0 {
				days = 0
			}
			// Floor, not round: 7.4 days must display as 7d so the bucket and
			// the visible age agree at the boundary.
			age = num(fmt.Sprintf("%dd", int(days)))
		}
		ramCell := mute("no")
		if row.Vmstate {
			ramCell = text("yes")
		}
		oldest.Rows = append(oldest.Rows, []Cell{strong(snapshotGuest(row)), idCell(row.Vmid), chips(Chip{Text: guestKind(row.GuestType), Kind: ChipType}), text(row.Name), age, ramCell, text(row.Node), mute(row.Description)})
	}
	data.addSection("Snapshot age",
		fmt.Sprintf("Snapshots per age bucket. The line marks the %d-day flag threshold.", warnDays),
		chartBlock("Snapshots by age", "", &Chart{Kind: ChartHist, Categories: labels, Series: []Series{{Name: "Snapshots", Tone: ToneAccent, Values: hist}},
			Threshold: &Threshold{AfterIndex: thresholdIdx, Label: fmt.Sprintf("flagged after %d d", warnDays)}, Footnote: fmt.Sprintf("snapshots · %d total", len(rows)), AriaLabel: "Snapshots per age bucket"}),
		tableBlock("Oldest snapshots", fmt.Sprintf("The %d oldest, unknown ages last.", min(maxOldestRows, len(rows))), oldest, "No snapshots on this cluster."))
	data.Notes = []NoteGroup{{Title: "How this report was computed", Items: []string{
		"Ages come from the snapshot's own creation time as Proxmox reports it; a snapshot with none is listed as unknown and never counted as old.",
		"Ages are floored to whole days so a snapshot reads as 7 d on the day it crosses a 7-day rule.",
	}}}
}

func snapshotGuest(row db.ListGuestSnapshotsForReportRow) string {
	if row.VmName.Valid && row.VmName.String != "" {
		return row.VmName.String
	}
	return fmt.Sprintf("#%d", row.Vmid)
}

func guestKind(t string) string {
	if t == "lxc" {
		return "CT"
	}
	return "VM"
}

// --- Patch status ---

func (g *Generator) generatePatchStatus(ctx context.Context, gc genCtx, data *ReportData) error {
	data.Subtitle = "The latest vulnerability scan of the cluster's nodes, and what needs patching first."
	scan, err := g.queries.GetLatestCVEScan(ctx, gc.cluster.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		// Only "no scan yet" is a report; a database error is a failed run.
		return fmt.Errorf("latest CVE scan: %w", err)
	}
	if err != nil {
		data.KPIs = []KPI{{Label: "Vulnerability scan", Value: "none", Hero: true, Detail: "No CVE scan has run for this cluster", Tone: ToneNeutral}}
		data.addFinding(SevInfo, "No CVE scan data is available for this cluster.", "Run a scan from the Security page to populate this report.", "Security")
		data.addSection("Patch status", "", Block{Kind: BlockText, Text: "No CVE scan data available for this cluster.", Wide: true})
		return nil
	}
	scanNodes, err := g.queries.ListCVEScanNodes(ctx, scan.ID)
	if err != nil {
		return fmt.Errorf("list scan nodes: %w", err)
	}
	kev, kErr := g.queries.ListCVEScanVulnsKEV(ctx, scan.ID)
	if kErr != nil {
		g.logger.Warn("report: KEV list unreadable", "scan_id", scan.ID, "error", kErr)
	}

	data.addMeta("Scan", fmt.Sprintf("%s · %s · %s", ts(scan.CreatedAt), scan.Status, plural(len(scanNodes), "node", "nodes")))
	total := int(scan.TotalVulns)
	data.KPIs = []KPI{
		{Label: "Vulnerabilities", Value: fmt.Sprint(total), Hero: true, Detail: "across all nodes, latest scan " + scan.CreatedAt.Format("2006-01-02"), Tone: toneIf(scan.CriticalCount > 0, ToneCrit)},
		{Label: "Critical", Value: fmt.Sprint(scan.CriticalCount), Tone: toneIf(scan.CriticalCount > 0, ToneCrit)},
		{Label: "High", Value: fmt.Sprint(scan.HighCount), Tone: toneIf(scan.HighCount > 0, ToneWarn)},
		{Label: "Medium", Value: fmt.Sprint(scan.MediumCount)},
		{Label: "Low", Value: fmt.Sprint(scan.LowCount)},
		{Label: "Known exploited", Value: fmt.Sprint(len(kev)), Detail: "on CISA's KEV list", Tone: toneIf(len(kev) > 0, ToneCrit)},
	}
	if len(kev) > 0 {
		ids := make([]string, 0, len(kev))
		for _, v := range kev {
			ids = append(ids, v.CveID)
		}
		data.addFinding(SevCritical, fmt.Sprintf("%s on CISA's known-exploited list:", pluralAre(len(kev), "vulnerability")), nameList(ids, 5)+". Patch these before anything else.", "Known exploited")
	}
	if scan.CriticalCount > 0 {
		data.addFinding(SevSerious, fmt.Sprintf("%s rated critical", plural(int(scan.CriticalCount), "vulnerability is", "vulnerabilities are")), "in the latest scan.", "Per-node breakdown")
	}
	if age := gc.now.Sub(scan.CreatedAt); age > 14*24*time.Hour {
		data.addFinding(SevWarning, fmt.Sprintf("The latest scan is %s old.", formatAge(age)), "These figures may no longer reflect the nodes.", "Scan")
	}

	rows := make([][]Cell, 0, len(scanNodes))
	cats := make([]string, 0, len(scanNodes))
	vals := make([]float64, 0, len(scanNodes))
	nodeOfScanNode := make(map[uuid.UUID]string, len(scanNodes))
	for _, n := range scanNodes {
		nodeOfScanNode[n.ID] = n.NodeName
		posture := 0.0
		if n.PostureScore.Valid {
			posture = float64(n.PostureScore.Float32)
		}
		rows = append(rows, []Cell{strong(n.NodeName), num(n.VulnsFound), num(fmt.Sprintf("%.0f", posture)), statusPill(n.Status)})
		cats = append(cats, n.NodeName)
		vals = append(vals, float64(n.VulnsFound))
		if posture > 0 && posture < 50 {
			data.addFinding(SevWarning, fmt.Sprintf("%s has a posture score of %.0f", n.NodeName, posture), "out of 100.", "Per-node breakdown")
		}
	}
	kevRows := make([][]Cell, 0, len(kev))
	for _, v := range kev {
		kevRows = append(kevRows, []Cell{strong(v.CveID), text(v.PackageName), pill(v.Severity, severityTone(v.Severity)), text(nodeOfScanNode[v.ScanNodeID])})
	}
	data.addSection("Per-node breakdown", "Vulnerabilities found per node in the latest scan.",
		chartBlock("Vulnerabilities per node", "", &Chart{Kind: ChartBars, Categories: cats, Series: []Series{{Name: "Vulnerabilities", Tone: ToneAccent, Values: vals}}, AriaLabel: "Vulnerabilities per node"}),
		tableBlock("", "", &Table{Columns: []Column{nowrapCol("Node"), numCol("Vulnerabilities"), numCol("Posture score"), col("Scan status")}, Rows: rows}, "No nodes in the scan."))
	data.addSection("Known exploited", "CVEs on CISA's Known Exploited Vulnerabilities list: attackers are using these today.",
		wideTable("", &Table{Columns: []Column{nowrapCol("CVE"), col("Package"), col("Severity"), col("Node")}, Rows: kevRows}, "No known-exploited vulnerabilities in the latest scan."))
	data.Notes = []NoteGroup{{Title: "How this report was computed", Items: []string{
		"Figures are the latest completed CVE scan for the cluster; the report does not scan.",
		"Severity follows the scanner's own rating; the KEV list is CISA's, matched by CVE id.",
	}}}
	return nil
}

func severityTone(s string) string {
	switch s {
	case "critical", "CRITICAL":
		return ToneCrit
	case "high", "HIGH":
		return ToneWarn
	}
	return ToneNeutral
}

// --- Uptime summary ---

func (g *Generator) generateUptimeSummary(ctx context.Context, gc genCtx, data *ReportData) error {
	nodes, err := g.queries.ListNodesByCluster(ctx, gc.cluster.ID)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	buildUptimeSummary(gc, nodes, data)
	return nil
}

func buildUptimeSummary(gc genCtx, nodes []db.Node, data *ReportData) {
	data.Subtitle = "Which nodes are up right now, for how long, and which rebooted inside the period."
	window := time.Duration(gc.hours) * time.Hour
	var online, rebooted int
	var maxUptime int64
	var rebootedNames, offlineNames []string
	rows := make([][]Cell, 0, len(nodes))
	for _, n := range nodes {
		uptime := time.Duration(n.Uptime) * time.Second
		if n.Status == "online" {
			online++
		} else {
			offlineNames = append(offlineNames, n.Name)
		}
		if n.Uptime > maxUptime {
			maxUptime = n.Uptime
		}
		note := mute("")
		if n.Status == "online" && uptime < window && n.Uptime > 0 {
			rebooted++
			rebootedNames = append(rebootedNames, fmt.Sprintf("%s (%s ago)", n.Name, formatAge(uptime)))
			note = mute("Booted inside the period")
		}
		rows = append(rows, []Cell{strong(n.Name), statusPill(n.Status), num(formatDuration(uptime)), text(n.PveVersion), note})
	}
	data.addMeta("Scope", plural(len(nodes), "node", "nodes"))
	pct := 0.0
	if len(nodes) > 0 {
		pct = float64(online) / float64(len(nodes)) * 100
	}
	data.KPIs = []KPI{
		{Label: "Nodes online now", Value: fmt.Sprintf("%d/%d", online, len(nodes)), Hero: true, Detail: fmt.Sprintf("%.0f %% of the cluster", pct), Tone: toneIf(online < len(nodes), ToneCrit)},
		{Label: "Rebooted in the period", Value: fmt.Sprint(rebooted), Tone: toneIf(rebooted > 0, ToneWarn)},
		{Label: "Longest uptime", Value: formatDuration(time.Duration(maxUptime) * time.Second)},
	}
	for _, name := range offlineNames {
		data.addFinding(SevCritical, fmt.Sprintf("%s is not online.", name), "", "Nodes")
	}
	if rebooted > 0 {
		data.addFinding(SevInfo, fmt.Sprintf("%s inside the period:", pluralRebooted(rebooted)), nameList(rebootedNames, 5)+".", "Nodes")
	}
	data.addSection("Nodes", "", wideTable("", &Table{Columns: []Column{nowrapCol("Node"), col("Status"), numCol("Uptime"), col("PVE version"), col("Note")}, Rows: rows}, "No nodes in this cluster."))
	data.Notes = []NoteGroup{{Title: "How this report was computed", Items: []string{
		"Nexara records each node's current uptime, not an availability history, so this report says what is up now and infers reboots from an uptime shorter than the period.",
		"\"Nodes online now\" was previously labelled SLA %; the figure is unchanged.",
	}}}
}

func pluralRebooted(n int) string {
	if n == 1 {
		return "1 node rebooted"
	}
	return fmt.Sprintf("%d nodes rebooted", n)
}
