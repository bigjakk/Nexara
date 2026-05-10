package reports

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
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

func TestRenderCSV_EscapesUserData(t *testing.T) {
	t.Parallel()

	data := &ReportData{
		Title:       "Resource Utilization",
		ClusterName: "=evil()",
		GeneratedAt: "2026-05-07T12:00:00Z",
		TimeRange: TimeRange{
			StartTime: "2026-05-01T00:00:00Z",
			EndTime:   "2026-05-07T00:00:00Z",
			Hours:     168,
		},
		Sections: []ReportSection{{
			Title:   "VMs",
			Headers: []string{"Name", "Notes"},
			Rows: []map[string]string{
				{"Name": "=1+1", "Notes": "+SUM(A1:A2)"},
				{"Name": "@cmd", "Notes": "\t=evil"},
				{"Name": "normal-vm", "Notes": "ok"},
			},
		}},
	}

	out, err := RenderCSV(data)
	if err != nil {
		t.Fatalf("RenderCSV: %v", err)
	}

	// Cluster header value was a formula trigger and must be escaped.
	if !strings.Contains(out, `'=evil()`) {
		t.Errorf("cluster name not escaped, output:\n%s", out)
	}
	// Each malicious row cell must be escaped.
	for _, want := range []string{`'=1+1`, `'+SUM(A1:A2)`, `'@cmd`, `'\t=evil`} {
		// csv.Writer does not encode \t literally, it writes the tab byte directly,
		// so for the tab case check the byte form.
		if want == `'\t=evil` {
			if !strings.Contains(out, "'\t=evil") {
				t.Errorf("tab-prefixed cell not escaped, output:\n%s", out)
			}
			continue
		}
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
	// And benign data must still come through unchanged.
	if !strings.Contains(out, "normal-vm") || !strings.Contains(out, ",ok\n") {
		t.Errorf("benign cell missing or mangled, output:\n%s", out)
	}
}
