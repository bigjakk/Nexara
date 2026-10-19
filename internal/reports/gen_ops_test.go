package reports

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func testCtx() genCtx {
	return genCtx{cluster: db.Cluster{ID: testCluster, Name: "cluster01"}, now: testNow, since: testNow.Add(-7 * 24 * time.Hour), hours: 168, params: DefaultParams()}
}

func TestBuildUptimeSummary(t *testing.T) {
	t.Parallel()
	nodes := []db.Node{
		{Name: "pve-01", Status: "online", Uptime: 30 * 24 * 3600, PveVersion: "9.2"},
		{Name: "pve-02", Status: "online", Uptime: 3600, PveVersion: "9.2"}, // rebooted inside the period
		{Name: "pve-03", Status: "offline", Uptime: 0, PveVersion: "9.2"},
	}
	data := &ReportData{}
	buildUptimeSummary(testCtx(), nodes, data)
	sortFindings(data.Findings)
	if data.KPIs[0].Value != "2/3" || data.KPIs[0].Tone != ToneCrit || data.KPIs[1].Value != "1" {
		t.Errorf("kpis = %+v", data.KPIs)
	}
	if len(data.Findings) != 2 || data.Findings[0].Severity != SevCritical || !strings.Contains(data.Findings[0].Lead, "pve-03") {
		t.Errorf("findings = %+v", data.Findings)
	}
	if !strings.Contains(data.Findings[1].Text, "pve-02") {
		t.Errorf("reboot finding = %+v", data.Findings[1])
	}
}

func TestBuildSnapshotInventory(t *testing.T) {
	t.Parallel()
	now := testNow.Unix()
	rows := []db.ListGuestSnapshotsForReportRow{
		{Vmid: 100, Name: "before-upgrade", GuestType: "qemu", Node: "pve-01", SnapTime: now - 45*86400, Vmstate: true, VmName: pgtype.Text{String: "dc01", Valid: true}},
		{Vmid: 100, Name: "pre-patch", GuestType: "qemu", Node: "pve-01", SnapTime: now - 10*86400, VmName: pgtype.Text{String: "dc01", Valid: true}},
		{Vmid: 105, Name: "clean", GuestType: "lxc", Node: "pve-03", SnapTime: now - 3600},
		{Vmid: 106, Name: "mystery", GuestType: "lxc", Node: "pve-03", SnapTime: 0},
	}
	data := &ReportData{}
	buildSnapshotInventory(testCtx(), rows, data)
	sortFindings(data.Findings)
	kpi := map[string]string{}
	for _, k := range data.KPIs {
		kpi[k.Label] = k.Value
	}
	if kpi["Snapshots"] != "4" || kpi["Older than 7 days"] != "2" || kpi["Older than 30 days"] != "1" || kpi["With RAM state"] != "1" || kpi["Unknown age"] != "1" {
		t.Errorf("kpis = %v", kpi)
	}
	if len(data.Findings) < 2 || data.Findings[0].Severity != SevSerious || !strings.Contains(data.Findings[1].Text, "pre-patch on dc01 (10d)") {
		t.Errorf("findings = %+v", data.Findings)
	}
	hist := data.Sections[0].Blocks[0].Chart
	sum := 0.0
	for _, v := range hist.Series[0].Values {
		sum += v
	}
	if sum != 4 || hist.Series[0].Values[len(hist.Series[0].Values)-1] != 1 {
		t.Errorf("histogram = %+v", hist.Series[0].Values)
	}
	oldest := data.Sections[0].Blocks[1].Table
	if oldest.Rows[0][1].Text != "100" || oldest.Rows[0][3].Text != "before-upgrade" || oldest.Rows[len(oldest.Rows)-1][3].Text != "mystery" {
		t.Errorf("oldest table order = %v … %v", oldest.Rows[0][3].Text, oldest.Rows[len(oldest.Rows)-1][3].Text)
	}
	// A guest with no name shows its VMID.
	if oldest.Rows[2][0].Text != "#105" && oldest.Rows[1][0].Text != "#105" {
		t.Errorf("unnamed guest label missing: %v %v", oldest.Rows[1][0].Text, oldest.Rows[2][0].Text)
	}
}

func TestBuildClusterDigest_LiftsFindingsAndFigures(t *testing.T) {
	t.Parallel()
	backup := &ReportData{Kicker: "Backup compliance", KPIs: []KPI{{Label: "Current within 24 h", Value: "50", Unit: "%", Hero: true}, {Label: "Backup targets", Value: "2"}},
		Findings: []Finding{{Severity: SevCritical, Lead: "1 backup target has no backup", Where: "Guests table"}},
		Sections: []Section{{Title: "Coverage", Blocks: []Block{{Kind: BlockChart, Chart: &Chart{Kind: ChartStackedBar, Total: 2, Series: []Series{{Name: "Protected", Values: []float64{1}}, {Name: "No backup", Values: []float64{1}}}}}}}}}
	uptime := &ReportData{Kicker: "Uptime summary", KPIs: []KPI{{Label: "Nodes online now", Value: "3/3", Hero: true}},
		Sections: []Section{{Title: "Nodes", Blocks: []Block{{Kind: BlockTable, Table: &Table{Columns: []Column{col("Node")}, Rows: [][]Cell{{text("pve-01")}}}}}}}}
	resources := &ReportData{Kicker: "Resource utilisation", KPIs: []KPI{{Label: "Cluster CPU, period average", Value: "12.5", Unit: "%", Hero: true}},
		Findings: []Finding{{Severity: SevWarning, Lead: "pve-02 averaged 82 % memory"}}}
	empty := &ReportData{Kicker: "Snapshot inventory", KPIs: []KPI{{Label: "Snapshots", Value: "0", Hero: true}}}
	patch := &ReportData{Kicker: "Patch status", KPIs: []KPI{{Label: "Vulnerability scan", Value: "none", Hero: true}}}

	data := &ReportData{}
	buildClusterDigest(digestInput{
		guests: 4, running: 3,
		uptime: uptime, resources: resources, backup: backup, snapshots: empty, patch: patch,
		alerts:      []db.CountAlertHistoryBySeverityInWindowRow{{Severity: "critical", Fired: 2, Resolved: 1, OpenCount: 1}, {Severity: "warning", Fired: 5, Resolved: 5}},
		topRules:    []db.ListTopAlertRulesInWindowRow{{Name: "CPU high", Metric: "cpu_usage", Severity: "warning", Fired: 5}},
		taskCounts:  []db.CountTaskHistoryByStatusInWindowRow{{Status: "completed", N: 40}, {Status: "failed", N: 2}},
		failedTasks: []db.TaskHistory{{Description: "vzdump", TaskType: "vzdump", Node: "pve-01", ExitStatus: "job errors", StartedAt: testNow}},
	}, data)
	sortFindings(data.Findings)

	// Findings are lifted with their report of origin, then the digest's own.
	if len(data.Findings) != 4 {
		t.Fatalf("findings = %+v", data.Findings)
	}
	if data.Findings[0].Where != "Backup compliance · Guests table" || data.Findings[0].Severity != SevCritical {
		t.Errorf("lifted finding = %+v", data.Findings[0])
	}
	if !hasFinding(data, SevSerious, "2 critical alerts fired") || !hasFinding(data, SevWarning, "2 of 42 tasks failed") {
		t.Errorf("digest's own findings missing: %+v", data.Findings)
	}
	kpi := map[string]KPI{}
	for _, k := range data.KPIs {
		kpi[k.Label] = k
	}
	if kpi["Findings needing attention"].Value != "2" || kpi["Findings needing attention"].Tone != ToneCrit {
		t.Errorf("attention kpi = %+v", kpi["Findings needing attention"])
	}
	if kpi["Nodes online"].Value != "3/3" || kpi["Guests running"].Value != "3/4" || kpi["CPU, period average"].Value != "12.5" || kpi["Backups current"].Value != "50" || kpi["Backups current"].Tone != ToneWarn {
		t.Errorf("lifted kpis = %+v", data.KPIs)
	}
	if kpi["Alerts fired"].Value != "7" || kpi["Tasks failed"].Value != "2" {
		t.Errorf("alert/task kpis = %+v / %+v", kpi["Alerts fired"], kpi["Tasks failed"])
	}
	if _, present := kpi["Vulnerabilities"]; present {
		t.Error("a 'none' scan must not become a vulnerabilities figure")
	}
	// The coverage chart travelled into the Backups section.
	if c := firstChart(data, ChartStackedBar); c == nil || c.Total != 2 {
		t.Errorf("coverage chart not lifted: %+v", c)
	}

	// No targets: the backups figure is not a warning.
	backup.KPIs[1].Value = "0"
	data = &ReportData{}
	buildClusterDigest(digestInput{uptime: uptime, resources: resources, backup: backup, snapshots: empty, patch: patch}, data)
	for _, k := range data.KPIs {
		if k.Label == "Backups current" && k.Tone != "" {
			t.Errorf("zero-target backups figure carries tone %q", k.Tone)
		}
	}
}

// Each node CPU finding at its boundary, in the percent the loaders produce.
func TestBuildResourceUtilization_CPUFindingBoundaries(t *testing.T) {
	t.Parallel()
	node := func(name string, cpu float64) nodeUsage {
		return nodeUsage{name: name, status: "online", days: 7, cpuAvg: cpu, cpuMax: cpu}
	}
	cases := []struct {
		name  string
		nodes []nodeUsage
		want  []string
	}{
		{"80 % is serious", []nodeUsage{node("pve-01", 80)}, []string{"serious: pve-01 averaged 80 % CPU"}},
		{"under 80 % is a warning", []nodeUsage{node("pve-01", 79.4)}, []string{"warning: pve-01 averaged 79 % CPU"}},
		{"60 % is a warning", []nodeUsage{node("pve-01", 60)}, []string{"warning: pve-01 averaged 60 % CPU"}},
		{"under 60 % is nothing", []nodeUsage{node("pve-01", 59.9)}, nil},
		{"over twice the cluster average", []nodeUsage{node("pve-01", 30), node("pve-02", 5), node("pve-03", 5)},
			[]string{"info: pve-01 carries twice the cluster's average CPU load"}},
		{"exactly twice is not", []nodeUsage{node("pve-01", 20), node("pve-02", 5), node("pve-03", 5)}, nil},
		{"not on a cluster averaging 5 % or less", []nodeUsage{node("pve-01", 12), node("pve-02", 1.5), node("pve-03", 1.5)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := &ReportData{}
			buildResourceUtilization(testCtx(), tc.nodes, data)
			var got []string
			for _, f := range data.Findings {
				if strings.Contains(f.Lead, "CPU") {
					got = append(got, f.Severity+": "+f.Lead)
				}
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("CPU findings = %q, want %q", got, tc.want)
			}
		})
	}
}

// The guest CPU-bound finding and the idle test at their boundaries, in percent.
func TestBuildVMResourceUsage_CPUBoundAndIdleBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cpu            float64
		cpuBound, idle bool
	}{
		{90, true, false},
		{89.9, false, false},
		{2, false, false},
		{1.9, false, true},
	}
	for _, c := range cases {
		data := &ReportData{}
		buildVMResourceUsage(testCtx(), []vmReportStats{{name: "linux01", vmid: 101, vmType: "qemu", status: "running", hasMetrics: true, cpuAvg: c.cpu}}, data)
		if got := hasFinding(data, SevWarning, "it is CPU-bound"); got != c.cpuBound {
			t.Errorf("cpu %v: CPU-bound finding = %v, want %v", c.cpu, got, c.cpuBound)
		}
		if got := kpiByLabel(data, "Idle while running").Value == "1"; got != c.idle {
			t.Errorf("cpu %v: idle = %v, want %v", c.cpu, got, c.idle)
		}
	}
}

// The swap warning names page cache only where the figure counts it: a Linux
// VM's own total-minus-free does, a container's cgroup usage does not.
func TestBuildVMResourceUsage_SwapWarningWordingFollowsTheGuestType(t *testing.T) {
	t.Parallel()
	data := &ReportData{}
	buildVMResourceUsage(testCtx(), []vmReportStats{
		{name: "linux01", vmid: 101, vmType: "qemu", status: "running", hasMetrics: true, cpuAvg: 5, memPct: 96, memSource: memSourceGuest},
		{name: "linux02", vmid: 102, vmType: "lxc", status: "running", hasMetrics: true, cpuAvg: 5, memPct: 96, memSource: memSourceGuest},
	}, data)
	if !hasFinding(data, SevWarning, "linux01 (101) averaged 96 % of its memory by its own count, which for a Linux guest includes page cache") ||
		!hasFinding(data, SevWarning, "linux02 (102) averaged 96 % of its memory by its own count; it may be swapping") {
		t.Errorf("swap warnings = %v", findingLeads(data))
	}
}

func TestMemSourceOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		guest, host int64
		pct         float64
		want        memSource
	}{
		{"no reading records a source", 0, 0, 50, memSourceUnknown},
		{"all the guest's own", 100, 0, 96, memSourceGuest},
		{"a boot's host readings among the guest's", 90, 10, 60, memSourceGuest},
		{"more than one in ten from the host", 89, 11, 60, memSourceMixed},
		{"all the host's", 0, 100, 100.4, memSourceHost},
		{"an own figure past its allocation is not the guest's", 95, 5, 100.2, memSourceMixed},
	}
	for _, c := range cases {
		if got := memSourceOf(c.guest, c.host, c.pct); got != c.want {
			t.Errorf("%s: memSourceOf(%d, %d, %v) = %v, want %v", c.name, c.guest, c.host, c.pct, got, c.want)
		}
	}
}
