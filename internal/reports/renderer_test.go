package reports

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func TestEscapeCSVCell(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		// Empty / safe inputs are returned unchanged.
		{"empty", "", ""},
		{"plain text", "hello", "hello"},
		{"plain alphanumeric", "VM-42", "VM-42"},
		{"digits only", "12345", "12345"},
		{"interior equals is safe", "name=foo", "name=foo"},
		{"interior plus is safe", "1+2", "1+2"},
		{"interior minus is safe", "a-b-c", "a-b-c"},
		{"unicode prefix is safe", "ünikode=1", "ünikode=1"},

		// OWASP/reviewer trigger payloads — leading char is the formula trigger.
		{"reviewer payload cmd", `=cmd|'/c calc'!A1`, `'=cmd|'/c calc'!A1`},
		{"reviewer payload hyperlink", `=HYPERLINK("http://x","click")`, `'=HYPERLINK("http://x","click")`},
		{"plain formula", "=1+1", "'=1+1"},
		{"plus formula", "+1+1", "'+1+1"},
		{"minus formula", "-1-1", "'-1-1"},
		{"at command (DDE)", `@SUM(A1:A2)`, `'@SUM(A1:A2)`},
		{"tab prefix bypass", "\t=1+1", "'\t=1+1"},
		{"cr prefix bypass", "\r=1+1", "'\r=1+1"},

		// Already-quoted user input still gets a leading quote prepended — we don't
		// distinguish "already escaped by user" from "naturally starts with apostrophe".
		// Apostrophe is NOT a trigger so it is left alone (Excel treats a single leading
		// apostrophe as a text-coercion marker but does not evaluate it).
		{"leading apostrophe is left alone", "'hello", "'hello"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EscapeCSVCell(tc.in)
			if got != tc.want {
				t.Errorf("EscapeCSVCell(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestEscapeCSVCell_NeutralisesFormula round-trips a malicious cell through
// csv.Writer + csv.Reader and asserts that the parsed value still has the
// leading apostrophe (so a spreadsheet would render it as text rather than
// evaluate it).
func TestEscapeCSVCell_NeutralisesFormula(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := WriteSafeCSVRow(w, []string{"name", "=1+1", "=cmd|'/c calc'!A1"}); err != nil {
		t.Fatalf("WriteSafeCSVRow: %v", err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	r := csv.NewReader(strings.NewReader(buf.String()))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	row := rows[0]
	if row[0] != "name" {
		t.Errorf("col 0 = %q, want %q", row[0], "name")
	}
	if row[1] != "'=1+1" {
		t.Errorf("col 1 = %q, want %q (leading apostrophe must survive)", row[1], "'=1+1")
	}
	if row[2] != "'=cmd|'/c calc'!A1" {
		t.Errorf("col 2 = %q, want it to start with an apostrophe", row[2])
	}
}

// sampleReport is a small typed report carrying hostile strings in every
// place user data lands, so one fixture serves the HTML, CSV and digest tests.
func sampleReport() *ReportData {
	return &ReportData{
		Schema:      SchemaVersion,
		Title:       "Backup compliance · =evil() · 7 days to 2026-09-14",
		Kicker:      "Backup compliance",
		Heading:     "<script>alert(1)</script> · 7 days to 2026-09-14",
		Subtitle:    "Every guest checked.",
		ClusterName: "=evil()",
		ReportType:  string(TypeBackupCompliance),
		GeneratedAt: "2026-09-14 06:02 UTC",
		TimeRange:   TimeRange{StartTime: "2026-09-07T06:00:00Z", EndTime: "2026-09-14T06:00:00Z", Hours: 168},
		Meta:        []MetaItem{{Label: "Scope", Value: "3 nodes"}},
		KPIs: []KPI{
			{Label: "Current within 24 h", Value: "68", Unit: "%", Hero: true, Detail: "15 of 22"},
			{Label: "No backup", Value: "3", Tone: ToneCrit},
		},
		Findings: []Finding{
			{Severity: SevCritical, Lead: "3 targets have no backup:", Text: "win06 (119), <b>x</b>.", Where: "Guests"},
			{Severity: SevInfo, Lead: "Note", Text: "fine"},
		},
		Sections: []Section{{
			Title: "Coverage",
			Blocks: []Block{
				{Kind: BlockChart, Title: "By state", Chart: &Chart{Kind: ChartStackedBar, Total: 22, Legend: true, AriaLabel: "cov",
					Series: []Series{{Name: "Protected", Tone: ToneGood, Values: []float64{15}}, {Name: "Stale", Tone: ToneWarn, Values: []float64{4}}, {Name: "No backup", Tone: ToneCrit, Values: []float64{3}}}}},
				{Kind: BlockChart, Title: "Age", Chart: &Chart{Kind: ChartHist, Categories: []string{"< 6 h", "6–12 h", "never"}, Series: []Series{{Name: "Guests", Values: []float64{9, 5, 3}}}, Threshold: &Threshold{AfterIndex: 1, Label: "stale after 24 h"}}},
				{Kind: BlockTable, Title: "VMs", Wide: true, Table: &Table{
					Columns: []Column{col("Name"), numCol("Count"), col("Notes"), col("State"), col("Providers")},
					Rows: [][]Cell{
						{strong("=1+1"), num(2), text("+SUM(A1:A2)"), pill("Stale", ToneWarn), chips(Chip{Text: "PBS", Kind: ChipPBS}, Chip{Text: "name match", Kind: ChipFlag})},
						{strong("@cmd"), num(0), text("\t=evil"), pill("No backup", ToneCrit), mute("—")},
						{strong("normal-vm"), num(1), textSub("ok", "2026-09-14 03:00"), pill("Protected", ToneGood), chips(Chip{Text: "Veeam", Kind: ChipVeeam})},
					},
				}},
				{Kind: BlockMeters, Wide: true, Meters: []Meter{{Name: "store01 <b>", Used: 3.2, Total: 8, UsedText: "3.2 of 8.0 TB", Tone: ToneAccent, Projection: "+26 GB/day"}}},
			},
		}},
		Notes: []NoteGroup{{Title: "Method", Items: []string{"All times UTC."}}},
	}
}

func TestRenderCSV_EscapesUserDataAndCoversEveryBlock(t *testing.T) {
	t.Parallel()
	out, err := RenderCSV(sampleReport())
	if err != nil {
		t.Fatalf("RenderCSV: %v", err)
	}
	// Formula triggers are neutralised wherever they appear.
	for _, want := range []string{`'=evil()`, `'=1+1`, `'+SUM(A1:A2)`, `'@cmd`, "'\t=evil"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
	// Every block kind reaches the CSV: findings, KPIs, chart tables, meters.
	for _, want := range []string{"# Key figures", "# Findings", "Protected,15", "< 6 h,9", "# Coverage — VMs", "store01 <b>,,3.2 of 8.0 TB,40.0", "ok (2026-09-14 03:00)", "PBS name match"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
	// And the file still parses as CSV. Sections have different widths by
	// design, so the reader must not insist on one field count.
	r := csv.NewReader(strings.NewReader(out))
	r.FieldsPerRecord = -1
	if _, err := r.ReadAll(); err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
}

func TestRenderHTML_EscapesAndDraws(t *testing.T) {
	t.Parallel()
	out, err := RenderHTML(sampleReport())
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	// User strings never reach the document as markup.
	for _, bad := range []string{"<script>alert(1)</script>", "<b>x</b>", "store01 <b>"} {
		if strings.Contains(out, bad) {
			t.Errorf("unescaped user data %q in HTML", bad)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "&lt;b&gt;x&lt;/b&gt;", "store01 &lt;b&gt;"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected escaped %q in HTML", want)
		}
	}
	// No script anywhere: the preview iframe forbids it and email drops it.
	if strings.Contains(out, "<script") {
		t.Error("rendered HTML contains a script tag")
	}
	// Structure: hero, findings, chart SVGs with tooltips, pills, chips, meter.
	for _, want := range []string{`class="kpi hero"`, `<ol class="findings">`, `<svg viewBox='0 0 640 28'`, `<title>Protected 15 of 22</title>`, `<svg viewBox='0 0 640 196'`, `stale after 24 h`, `class="pill stale"`, `class="prov pbs"`, `class="flag"`, `class="meters"`, `@media print`} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in HTML", want)
		}
	}
	// Only the stacked bar needs a clip path: one id definition and one use.
	if strings.Count(out, "rpt-clip-") != 2 {
		t.Errorf("stacked bar clip id count = %d, want 2 occurrences", strings.Count(out, "rpt-clip-"))
	}
}

func TestStackedBar_LabelsOnlyWhereTheyFit(t *testing.T) {
	t.Parallel()
	c := &Chart{Kind: ChartStackedBar, Total: 22, Series: []Series{
		{Name: "Protected", Tone: ToneGood, Values: []float64{15}},
		{Name: "Stale", Tone: ToneWarn, Values: []float64{4}},
		{Name: "No backup", Tone: ToneCrit, Values: []float64{3}},
	}}
	svg := stackedBarSVG(c, 1)
	if !strings.Contains(svg, ">Protected 15</text>") || !strings.Contains(svg, ">Stale 4</text>") {
		t.Errorf("wide segments should carry their label: %s", svg)
	}
	// 3/22 of 640 is ~87 units; "No backup 3" needs ~91 with padding, so it is
	// left to the legend rather than clipped.
	if strings.Contains(svg, ">No backup 3</text>") {
		t.Errorf("narrow segment must not carry a label that does not fit: %s", svg)
	}
	if !strings.Contains(svg, "<title>No backup 3 of 22</title>") {
		t.Errorf("every segment keeps its tooltip: %s", svg)
	}
	// A zero-total chart draws nothing rather than dividing by zero.
	if got := stackedBarSVG(&Chart{Kind: ChartStackedBar, Series: []Series{{Name: "x", Values: []float64{0}}}}, 2); got != "" {
		t.Errorf("empty stacked bar rendered: %s", got)
	}
}

func TestColumnsAndBars_UseANiceAxisAndTooltips(t *testing.T) {
	t.Parallel()
	cols := columnsSVG(&Chart{Kind: ChartColumns, Categories: []string{"Sep 8", "Sep 9"}, Series: []Series{{Name: "ok", Tone: ToneGood, Values: []float64{3, 1}}, {Name: "fail", Tone: ToneCrit, Values: []float64{0, 1}}}})
	for _, want := range []string{">4</text>", "<title>Sep 8: 3 ok</title>", "<title>Sep 9: 1 fail</title>"} {
		if !strings.Contains(cols, want) {
			t.Errorf("columns: expected %q in %s", want, cols)
		}
	}
	if strings.Contains(cols, "<title>Sep 8: 0 fail</title>") {
		t.Error("columns: a zero segment must not be drawn")
	}
	bars := barsSVG(&Chart{Kind: ChartBars, Categories: []string{"linux01", "a-very-long-guest-name-that-keeps-going"}, Format: FormatPercent, Series: []Series{{Name: "cpu", Values: []float64{42.5, 7}}}})
	for _, want := range []string{">42.5%</text>", "<title>linux01: 42.5%</title>", "…"} {
		if !strings.Contains(bars, want) {
			t.Errorf("bars: expected %q in %s", want, bars)
		}
	}
	if niceMax(4) != 4 || niceMax(7) != 10 || niceMax(23) != 25 || niceMax(0) != 1 {
		t.Errorf("niceMax: %v %v %v %v", niceMax(4), niceMax(7), niceMax(23), niceMax(0))
	}
}

func TestRenderEmailDigest_IsTableBuiltAndHonest(t *testing.T) {
	t.Parallel()
	data := sampleReport()
	// Six findings: four shown, two counted.
	data.Findings = append(data.Findings,
		Finding{Severity: SevWarning, Lead: "w1"}, Finding{Severity: SevWarning, Lead: "w2"},
		Finding{Severity: SevSerious, Lead: "s1"}, Finding{Severity: SevInfo, Lead: "i2"})
	sortFindings(data.Findings)
	out := RenderEmailDigest(data, DigestOptions{ScheduleName: "Weekly review", RunID: "run-1", AttachmentNames: []string{"a.html", "a.csv"}})
	for _, bad := range []string{"<svg", "<script", "<style", "<script>alert(1)</script>"} {
		if strings.Contains(out, bad) {
			t.Errorf("digest must not contain %q", bad)
		}
	}
	// Sorted: critical, serious, warning, warning, info, info — four shown,
	// and the two left over are both info.
	for _, want := range []string{"&lt;script&gt;", "Weekly review", "run-1", "a.html, a.csv", "background:#059669", "+ 2 MORE", "2 info", "3 targets have no backup:", "68%"} {
		if !strings.Contains(out, want) {
			t.Errorf("digest: expected %q", want)
		}
	}
	// Coloured cells add up: the three segments' widths sum to 100 %.
	if !strings.Contains(out, "width:68.2%") || !strings.Contains(out, "width:18.2%") || !strings.Contains(out, "width:13.6%") {
		t.Errorf("digest bar widths wrong:\n%s", out)
	}
	clean := sampleReport()
	clean.Findings = nil
	if !strings.Contains(RenderEmailDigest(clean, DigestOptions{}), "Nothing needs your attention") {
		t.Error("a report with no findings should say so")
	}
}

// TestRenderSampleToFiles is a development aid, not an assertion: with
// NEXARA_REPORT_SAMPLE_DIR set it writes the HTML, CSV and email digest of a
// realistic backup compliance report into that directory so the rendering can
// be opened in a browser. Skipped otherwise.
func TestRenderSampleToFiles(t *testing.T) {
	dir := os.Getenv("NEXARA_REPORT_SAMPLE_DIR")
	if dir == "" {
		t.Skip("set NEXARA_REPORT_SAMPLE_DIR to write sample renderings")
	}
	in := sampleBackupInput()
	in.nodeNames = map[uuid.UUID]string{}
	g := &Generator{now: func() time.Time { return testNow }}
	data := g.newReport(TypeBackupCompliance, genCtx{cluster: db.Cluster{Name: "cluster01"}, now: testNow, since: in.since, hours: 168, params: in.params})
	buildBackupCompliance(in, data)
	sortFindings(data.Findings)
	html, err := RenderHTML(data)
	if err != nil {
		t.Fatal(err)
	}
	csvOut, err := RenderCSV(data)
	if err != nil {
		t.Fatal(err)
	}
	digest := RenderEmailDigest(data, DigestOptions{ScheduleName: "Weekly backup review", RunID: "sample", AttachmentNames: []string{AttachmentBaseName(data) + ".html"}})
	for name, content := range map[string]string{"sample-report.html": html, "sample-report.csv": csvOut, "sample-digest.html": digest} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
