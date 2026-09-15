package reports

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/backupcoverage"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

var testNow = time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)

func entry(vmid int32, name, kind, status string, hoursAgo float64, protection string, veeam *backupcoverage.Veeam) backupcoverage.Entry {
	e := backupcoverage.Entry{VMID: vmid, Name: name, Type: kind, Status: status, Eligibility: backupcoverage.Eligible, VeeamCapable: kind == "qemu", Protection: protection, Veeam: veeam}
	switch protection {
	case backupcoverage.ProtectionNone:
		e.CoverageStatus = backupcoverage.StatusNone
	default:
		e.Freshest = testNow.Add(-time.Duration(hoursAgo * float64(time.Hour))).Unix()
		if hoursAgo < 24 {
			e.CoverageStatus = backupcoverage.StatusRecent
		} else {
			e.CoverageStatus = backupcoverage.StatusStale
		}
		if protection == backupcoverage.ProtectionPBS || protection == backupcoverage.ProtectionBoth {
			e.BackupCount = 3
		}
	}
	return e
}

func sampleBackupInput() backupInput {
	in := backupInput{now: testNow, since: testNow.Add(-7 * 24 * time.Hour), params: DefaultParams(), includeVeeam: true, nodeCount: 3, nodeNames: map[uuid.UUID]string{}, runsRead: true}
	in.pbsServers = []string{"pbs01 (PBS, datastore store01)"}
	in.veeamServers = []string{"vbr01 (Veeam 13.1)"}
	in.entries = []backupcoverage.Entry{
		entry(100, "dc01", "qemu", "running", 3, backupcoverage.ProtectionBoth, &backupcoverage.Veeam{Protected: true, RestorePointCount: 30, MatchMethod: "smbios", MalwareStatus: "Clean"}),
		entry(101, "ca01", "qemu", "running", 5, backupcoverage.ProtectionPBS, nil),
		entry(105, "linux03", "lxc", "running", 6, backupcoverage.ProtectionPBS, nil),
		entry(107, "linux05", "qemu", "running", 9, backupcoverage.ProtectionVeeam, &backupcoverage.Veeam{Protected: true, RestorePointCount: 19, MatchMethod: "smbios", MalwareStatus: "Suspicious"}),
		entry(109, "win02", "qemu", "running", 7, backupcoverage.ProtectionVeeam, &backupcoverage.Veeam{Protected: true, RestorePointCount: 12, MatchMethod: "name", MalwareStatus: "Clean"}),
		entry(115, "linux09", "qemu", "running", 28, backupcoverage.ProtectionPBS, nil),
		entry(116, "win05", "qemu", "running", 124, backupcoverage.ProtectionVeeam, &backupcoverage.Veeam{Protected: true, RestorePointCount: 16, MatchMethod: "smbios", MalwareStatus: "Clean", LastRunFailed: true}),
		entry(119, "win06", "qemu", "running", 0, backupcoverage.ProtectionNone, nil),
		entry(120, "linux12", "qemu", "stopped", 0, backupcoverage.ProtectionNone, nil),
	}
	worker := entry(150, "veeam-worker01", "qemu", "stopped", 0, backupcoverage.ProtectionNotEligible, nil)
	worker.Eligibility = backupcoverage.VeeamWorker
	worker.CoverageStatus = backupcoverage.StatusNotEligible
	in.entries = append(in.entries, worker)
	in.runs = []backupRun{
		{When: testNow.Add(-3 * time.Hour), Job: "backup-daily", Provider: providerPBS, Detail: "vzdump · pve-01", Outcome: "fail", Guests: "linux09 (115)", Message: "job errors"},
		{When: testNow.Add(-27 * time.Hour), Job: "backup-daily", Provider: providerPBS, Detail: "vzdump · pve-01", Outcome: "ok", Message: "OK"},
		{When: testNow.Add(-4 * time.Hour), Job: "Proxmox Windows Daily", Provider: providerVeeam, Detail: "Veeam backup", Outcome: "warn", Message: "1 of 5 guests warned"},
		{When: testNow.Add(-28 * time.Hour), Job: "Proxmox Windows Daily", Provider: providerVeeam, Detail: "Veeam backup", Outcome: "ok"},
	}
	in.capacity = []capacityItem{
		{Name: "veeam-repo01", Detail: "Veeam repository", Provider: providerVeeam, Used: 5.9 * (1 << 40), Total: 7.3 * (1 << 40), History: []capSample{
			{T: testNow.Add(-7 * 24 * time.Hour), Used: 5.64 * (1 << 40)}, {T: testNow.Add(-3 * 24 * time.Hour), Used: 5.79 * (1 << 40)}, {T: testNow, Used: 5.9 * (1 << 40)}}},
		{Name: "store01", Detail: "PBS datastore · pbs01", Provider: providerPBS, Used: 3.2 * (1 << 40), Total: 8 * (1 << 40), History: []capSample{
			{T: testNow.Add(-7 * 24 * time.Hour), Used: 3.2 * (1 << 40)}, {T: testNow, Used: 3.2 * (1 << 40)}}},
		{Name: "store02", Detail: "zfspool · pve-01", Provider: providerLocal, Used: 0.6 * (1 << 40), Total: 2 * (1 << 40)},
	}
	in.orphans = []orphanedObject{{Name: "linux99", Points: 12, Bytes: 84 << 30, LastSeen: testNow.Add(-15 * 24 * time.Hour)}}
	return in
}

func findingLeads(d *ReportData) []string {
	out := make([]string, 0, len(d.Findings))
	for _, f := range d.Findings {
		out = append(out, f.Severity+": "+f.Lead+" "+f.Text)
	}
	return out
}

func hasFinding(d *ReportData, sev, substr string) bool {
	for _, f := range d.Findings {
		if f.Severity == sev && strings.Contains(f.Lead+" "+f.Text, substr) {
			return true
		}
	}
	return false
}

func TestBuildBackupCompliance_CountsAndFindings(t *testing.T) {
	t.Parallel()
	in := sampleBackupInput()
	data := &ReportData{}
	buildBackupCompliance(in, data)
	sortFindings(data.Findings)

	// Nine targets: 5 recent, 2 stale, 2 none; the worker excluded.
	hero := data.KPIs[0]
	if !hero.Hero || hero.Value != "56" || hero.Unit != "%" || !strings.Contains(hero.Detail, "5 of 9 backup targets") {
		t.Errorf("hero = %+v", hero)
	}
	kpi := map[string]KPI{}
	for _, k := range data.KPIs {
		kpi[k.Label] = k
	}
	if kpi["Backup targets"].Value != "9" || kpi["Backup targets"].Detail != "+1 excluded (Veeam infrastructure)" {
		t.Errorf("targets kpi = %+v", kpi["Backup targets"])
	}
	if kpi["Stale"].Value != "2" || kpi["Stale"].Detail != "oldest 5 d 4 h" || kpi["No backup"].Value != "2" {
		t.Errorf("stale/none kpis = %+v / %+v", kpi["Stale"], kpi["No backup"])
	}
	if kpi["Runs this period"].Value != "4" || kpi["Runs this period"].Detail != "2 ok · 1 warned · 1 failed" {
		t.Errorf("runs kpi = %+v", kpi["Runs this period"])
	}
	if kpi["Fullest repository"].Value != "81" || kpi["Fullest repository"].Tone != ToneWarn {
		t.Errorf("fullest kpi = %+v", kpi["Fullest repository"])
	}

	checks := []struct{ sev, substr string }{
		{SevCritical, "2 backup targets have no backup from any provider: win06 (119), linux12 (120)"},
		{SevSerious, "veeam-repo01 is 81 % full and at the last 7 days' growth rate it is full in about 38 days"},
		{SevSerious, "2 guests are stale (newest restore point older than 24 h): linux09 (115) (1 d 4 h), win05 (116) (5 d 4 h)"},
		{SevSerious, "Veeam flagged the newest restore point of 1 guest: linux05 (107) (Suspicious)"},
		{SevSerious, "Veeam's last run failed for 1 guest: win05 (116)"},
		{SevWarning, "1 of 4 backup runs failed this period (backup-daily)"},
		{SevWarning, "1 guest is linked to a Veeam backup by name only: win02 (109)"},
		{SevInfo, "84.0 GB of Veeam restore points belong to 1 guest that no longer exist on this cluster: linux99"},
	}
	for _, c := range checks {
		if !hasFinding(data, c.sev, c.substr) {
			t.Errorf("missing %s finding containing %q; have:\n%s", c.sev, c.substr, strings.Join(findingLeads(data), "\n"))
		}
	}
	// Severity order holds after sorting.
	last := 0
	for _, f := range data.Findings {
		if severityRank[f.Severity] < last {
			t.Errorf("findings not ordered by severity: %v", findingLeads(data))
		}
		last = severityRank[f.Severity]
	}
	// The repo projection: ~37 GB/day over 1.4 TB free → about 38 days.
	var repo Meter
	for _, s := range data.Sections {
		for _, b := range s.Blocks {
			for _, m := range b.Meters {
				if m.Name == "veeam-repo01" {
					repo = m
				}
			}
		}
	}
	if !strings.Contains(repo.Projection, "full in ~38 d") || !repo.ProjectionWarn || repo.Tone != ToneWarn {
		t.Errorf("repo meter = %+v", repo)
	}
}

func TestBuildBackupCompliance_SectionsAndGuestTable(t *testing.T) {
	t.Parallel()
	in := sampleBackupInput()
	data := &ReportData{}
	buildBackupCompliance(in, data)

	titles := make([]string, 0, len(data.Sections))
	for _, s := range data.Sections {
		titles = append(titles, s.Title)
	}
	want := []string{"Coverage", "Backup runs in the period", "Repository capacity", "Guests", "Veeam findings"}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Fatalf("sections = %v, want %v", titles, want)
	}

	// Coverage: stacked bar totals the targets; histogram buckets sum to them.
	cov := data.Sections[0].Blocks[0].Chart
	if cov.Kind != ChartStackedBar || cov.Total != 9 || cov.Series[0].Values[0] != 5 || cov.Series[1].Values[0] != 2 || cov.Series[2].Values[0] != 2 {
		t.Errorf("coverage chart = %+v", cov)
	}
	hist := data.Sections[0].Blocks[1].Chart
	sum := 0.0
	for _, v := range hist.Series[0].Values {
		sum += v
	}
	if sum != 9 || hist.Series[0].Values[len(hist.Series[0].Values)-1] != 2 || hist.Threshold.AfterIndex != 2 {
		t.Errorf("histogram = %+v", hist)
	}

	// Guests: problems first, worker last, notes explain.
	guests := data.Sections[3].Blocks[0].Table
	if len(guests.Rows) != 10 {
		t.Fatalf("guest rows = %d", len(guests.Rows))
	}
	first, last := guests.Rows[0], guests.Rows[len(guests.Rows)-1]
	if first[4].Text != "No backup" || last[4].Text != "Not a target" || last[1].Text != "veeam-worker01" {
		t.Errorf("guest ordering: first %q last %q", first[4].Text, last[1].Text)
	}
	// Both stale rows precede every protected row, oldest first.
	if guests.Rows[2][1].Text != "win05" || guests.Rows[3][1].Text != "linux09" {
		t.Errorf("stale ordering: %q, %q", guests.Rows[2][1].Text, guests.Rows[3][1].Text)
	}
	if !strings.Contains(guests.Rows[2][9].Text, "Last Veeam run for this guest failed") {
		t.Errorf("win05 note = %q", guests.Rows[2][9].Text)
	}
	// The name-matched guest carries the dashed flag chip.
	var win02 []Cell
	for _, r := range guests.Rows {
		if r[1].Text == "win02" {
			win02 = r
		}
	}
	flagged := false
	for _, ch := range win02[5].Chips {
		if ch.Kind == ChipFlag {
			flagged = true
		}
	}
	if !flagged {
		t.Errorf("win02 provider chips = %+v, want a name-match flag", win02[5].Chips)
	}
	// A container shows n/a in the Veeam column rather than a dash.
	for _, r := range guests.Rows {
		if r[1].Text == "linux03" && r[8].Text != "n/a" {
			t.Errorf("container Veeam column = %q, want n/a", r[8].Text)
		}
	}

	// Runs: jobs grouped per provider, failures listed.
	runs := data.Sections[1]
	jobs := runs.Blocks[1].Table
	if len(jobs.Rows) != 2 || jobs.Rows[0][0].Text != "Proxmox Windows Daily" || jobs.Rows[1][0].Text != "backup-daily" {
		t.Errorf("jobs table = %+v", jobs.Rows)
	}
	if jobs.Rows[1][6].Text != "Failed" || jobs.Rows[1][2].Text != "2" {
		t.Errorf("backup-daily row = %+v", jobs.Rows[1])
	}
	if len(runs.Blocks) != 3 || len(runs.Blocks[2].Table.Rows) != 2 {
		t.Errorf("failed/warned runs block = %+v", runs.Blocks)
	}
}

func TestBuildBackupCompliance_PBSOnlyEstate(t *testing.T) {
	t.Parallel()
	in := sampleBackupInput()
	in.includeVeeam = false
	in.veeamServers = nil
	in.orphans = nil
	for i := range in.entries {
		in.entries[i].Veeam = nil
	}
	data := &ReportData{}
	buildBackupCompliance(in, data)
	for _, s := range data.Sections {
		if s.Title == "Veeam findings" {
			t.Error("a PBS-only report must not carry a Veeam findings section")
		}
	}
	guests := data.Sections[3].Blocks[0].Table
	if len(guests.Columns) != 9 || guests.Columns[7].Name != "PBS" || guests.Columns[8].Name != "Notes" {
		t.Errorf("PBS-only guest columns = %+v", guests.Columns)
	}
	if !strings.Contains(data.Subtitle, "Proxmox Backup Server.") || strings.Contains(data.Subtitle, "Veeam") {
		t.Errorf("subtitle = %q", data.Subtitle)
	}
	for _, f := range data.Findings {
		if strings.Contains(f.Lead, "Veeam") {
			t.Errorf("Veeam finding in a PBS-only report: %+v", f)
		}
	}
}

func TestRunClassification(t *testing.T) {
	t.Parallel()
	ended := pgtype.Timestamptz{Time: testNow.Add(-time.Hour), Valid: true}
	cases := []struct {
		name string
		task db.TaskHistory
		want string
	}{
		{"ok", db.TaskHistory{Status: "completed", ExitStatus: "OK", FinishedAt: ended}, "ok"},
		{"warnings are a success that warned", db.TaskHistory{Status: "completed", ExitStatus: "WARNINGS: 1", FinishedAt: ended}, "warn"},
		{"failed", db.TaskHistory{Status: "failed", ExitStatus: "job errors", FinishedAt: ended}, "fail"},
		{"lost track", db.TaskHistory{Status: "failed", ExitStatus: "vanished", FinishedAt: ended}, "lost"},
		{"running", db.TaskHistory{Status: "running"}, "running"},
	}
	for _, c := range cases {
		r := pveBackupRun(c.task, nil)
		if r.Outcome != c.want {
			t.Errorf("%s: outcome = %q, want %q", c.name, r.Outcome, c.want)
		}
	}
	guest := pveBackupRun(db.TaskHistory{Status: "completed", ExitStatus: "OK", Vmid: pgtype.Int4{Int32: 100, Valid: true}, FinishedAt: ended}, []backupcoverage.Entry{{VMID: 100, Name: "dc01"}})
	if guest.Guests != "dc01 (100)" || !guest.When.Equal(ended.Time) {
		t.Errorf("guest resolution = %+v", guest)
	}

	veeam := []struct {
		name string
		s    db.VeeamSession
		want string
	}{
		{"success", db.VeeamSession{State: "Stopped", Result: "Success"}, "ok"},
		{"warning", db.VeeamSession{State: "Stopped", Result: "Warning"}, "warn"},
		{"failed", db.VeeamSession{State: "Stopped", Result: "Failed"}, "fail"},
		{"stopped from Nexara reads as cancelled, not failed", db.VeeamSession{State: "Stopped", Result: "Failed", NexaraStopped: true}, "cancelled"},
		{"in flight", db.VeeamSession{State: "Working", Result: "None"}, "running"},
	}
	for _, c := range veeam {
		if r := veeamBackupRun(c.s); r.Outcome != c.want {
			t.Errorf("%s: outcome = %q, want %q", c.name, r.Outcome, c.want)
		}
	}
}

func TestProjectFull(t *testing.T) {
	t.Parallel()
	const tb = float64(1 << 40)
	flat := &capacityItem{Used: 3 * tb, Total: 8 * tb, History: []capSample{{T: testNow.Add(-72 * time.Hour), Used: 3 * tb}, {T: testNow, Used: 3 * tb}}}
	if perDay, days, state := projectFull(flat); state != projectionFlat || perDay != 0 || days != 0 {
		t.Errorf("flat = %v %v %q", perDay, days, state)
	}
	growing := &capacityItem{Used: 6 * tb, Total: 8 * tb, History: []capSample{{T: testNow.Add(-48 * time.Hour), Used: 5.8 * tb}, {T: testNow, Used: 6 * tb}}}
	perDay, days, state := projectFull(growing)
	if state != projectionGrowing || perDay < 0.09*tb || perDay > 0.11*tb || days < 19 || days > 21 {
		t.Errorf("growing = %v/day %v days %q", perDay/tb, days, state)
	}
	short := &capacityItem{Used: 1, Total: 2, History: []capSample{{T: testNow.Add(-time.Hour), Used: 0}, {T: testNow, Used: 1}}}
	if _, _, state := projectFull(short); state != projectionNoHistory {
		t.Error("an hour of history must not project")
	}
	if _, _, state := projectFull(&capacityItem{Used: 1, Total: 2}); state != projectionNoHistory {
		t.Error("no history must not project")
	}
}

func TestPeriodLabelAndBuckets(t *testing.T) {
	t.Parallel()
	if got := periodLabel(168, testNow); got != "7 days to 2026-09-14" {
		t.Errorf("periodLabel(168) = %q", got)
	}
	if got := periodLabel(24, testNow); got != "24 hours to 2026-09-14" {
		t.Errorf("periodLabel(24) = %q", got)
	}
	if got := periodLabel(36, testNow); got != "36 h to 2026-09-14 06:00" {
		t.Errorf("periodLabel(36) = %q", got)
	}
	starts, labels, step := dayBuckets(testNow.Add(-7*24*time.Hour), testNow)
	if len(starts) != 8 || labels[0] != "Sep 7" || labels[7] != "Sep 14" || step != 24*time.Hour {
		t.Errorf("day buckets = %v %v %v", len(starts), labels, step)
	}
	if bucketIndex(starts, step, testNow.Add(-3*time.Hour)) != 7 || bucketIndex(starts, step, testNow.Add(-30*24*time.Hour)) != -1 {
		t.Error("bucketIndex placed a time in the wrong bucket")
	}
	_, wk, step := dayBuckets(testNow.Add(-90*24*time.Hour), testNow)
	if step != 7*24*time.Hour || !strings.HasPrefix(wk[0], "wk ") {
		t.Errorf("long windows should bucket by week: %v %v", step, wk[:2])
	}
	if formatAge(0) != "0 min" || formatAge(3*time.Hour) != "3 h" || formatAge(52*time.Hour) != "2 d 4 h" || formatAge(48*time.Hour) != "2 d" || formatAge(-time.Hour) != "0 min" {
		t.Errorf("formatAge: %q %q %q %q", formatAge(0), formatAge(3*time.Hour), formatAge(52*time.Hour), formatAge(-time.Hour))
	}
}

func TestProjectFull_NearFlatGrowthIsFlat(t *testing.T) {
	t.Parallel()
	const tb = float64(1 << 40)
	// A gigabyte a day on ten terabytes is 0.01 % a day: flat, never
	// "full in ~0 d".
	trickle := &capacityItem{Used: 3 * tb, Total: 10 * tb, History: []capSample{{T: testNow.Add(-72 * time.Hour), Used: 3 * tb}, {T: testNow, Used: 3*tb + 3*(1<<30)}}}
	if perDay, days, state := projectFull(trickle); state != projectionFlat || perDay != 0 || days != 0 {
		t.Errorf("trickle = %v %v %q, want flat", perDay, days, state)
	}
	full := &capacityItem{Used: 10 * tb, Total: 10 * tb, History: []capSample{{T: testNow.Add(-72 * time.Hour), Used: 9 * tb}, {T: testNow, Used: 10 * tb}}}
	if _, _, state := projectFull(full); state != projectionFull {
		t.Errorf("full = %q, want full", state)
	}
	in := sampleBackupInput()
	in.capacity = []capacityItem{*trickle}
	data := &ReportData{}
	buildBackupCompliance(in, data)
	for _, f := range data.Findings {
		if f.Where == "Repository capacity" {
			t.Errorf("a flat datastore produced a capacity finding: %+v", f)
		}
	}
}

func TestVeeamRun_NoResultIsNotASuccess(t *testing.T) {
	t.Parallel()
	if r := veeamBackupRun(db.VeeamSession{State: "Stopped", Result: "None"}); r.Outcome != "unknown" {
		t.Errorf("result None = %q, want unknown", r.Outcome)
	}
	if c := outcomePill("unknown"); c.Text != "No result" || c.Tone != ToneNeutral {
		t.Errorf("pill = %+v", c)
	}
}

func TestBucketIndex_Weekly(t *testing.T) {
	t.Parallel()
	since := testNow.Add(-90 * 24 * time.Hour)
	starts, labels, step := dayBuckets(since, testNow)
	if step != 7*24*time.Hour || len(starts) != 13 || len(labels) != 13 {
		t.Fatalf("weekly buckets = %d starts %d labels step %v", len(starts), len(labels), step)
	}
	if bucketIndex(starts, step, since.Add(8*24*time.Hour)) != 1 {
		t.Error("day 8 should fall in the second week")
	}
	if bucketIndex(starts, step, testNow) != 12 {
		t.Errorf("the window end falls in the last bucket, got %d", bucketIndex(starts, step, testNow))
	}
	if bucketIndex(starts, step, testNow.Add(24*time.Hour)) != -1 {
		t.Error("a time past the last bucket must not be placed")
	}
}
