package db

import (
	"regexp"
	"strings"
	"testing"
)

// ListCVEScanVulnsByNode once filtered on the caller-chosen scan_node_id alone, so a caller with
// view:cve_scan on one cluster could read another cluster's scan (queries/cve.sql says why
// scan_id is in the filter). A SQL-source guard: it cannot prove the filter works, only that
// every read carries it, including queries added later in any file; scope_db_test.go is where a
// behavioural test belongs.

// readsCVEScanVulnsRe identifies a query that READS cve_scan_vulns, through
// FROM or a JOIN, schema-qualified or quoted. An INSERT has neither, so it
// is never treated as a read — the same shape readsAuditLogRe uses.
var readsCVEScanVulnsRe = regexp.MustCompile(`(?i)\b(?:from|join)\s+(?:public\.)?"?cve_scan_vulns"?\b`)

// scanScopeRe matches the predicate that confines a read to one scan:
// `scan_id = $N`, optionally table-qualified, introduced by WHERE or AND.
//
// "scan_node_id" does not contain "scan_id" as a substring, so the node
// filter cannot satisfy this by accident — which is the whole point, since
// the node filter is exactly what was there instead.
var scanScopeRe = regexp.MustCompile(`(?:where|and) (?:[a-z_]+\.)?scan_id = \$\d+`)

// TestCVEVulnReadsAreScanScoped enumerates the cve_scan_vulns reads across
// queries/ rather than checking a fixed list, so a read added tomorrow is
// covered the day it lands.
//
// There is deliberately NO exemption map. audit_log's guard needs one
// because two of its reads are genuinely global; a vulnerability listing
// has no such case — every one of them answers a request about one scan —
// and an exemption slot nobody needs is a slot the next unscoped query
// gets written into.
func TestCVEVulnReadsAreScanScoped(t *testing.T) {
	t.Parallel()

	checked := 0
	for name, body := range namedSQLQueries(t) {
		if !readsCVEScanVulnsRe.MatchString(body) {
			continue
		}
		checked++
		normalized := strings.ToLower(strings.Join(strings.Fields(stripSQLComments(body)), " "))
		if scanScopeRe.MatchString(normalized) {
			continue
		}
		t.Errorf("%s reads cve_scan_vulns without confining the read to one scan.\n"+
			"\tAdd `scan_id = $N` to its WHERE. The endpoint that serves these rows authorizes\n"+
			"\tthe CLUSTER in its path and verifies the SCAN belongs to it; any other filter —\n"+
			"\tscan_node_id above all — is a value the caller chooses, so without the scan id the\n"+
			"\tquery answers with another cluster's vulnerabilities.\n\tquery:\n%s", name, body)
	}

	// The count is stated so the guard cannot pass by matching nothing —
	// a renamed table or a reworded FROM would otherwise turn it silently
	// vacuous. Raise it when a read is added; never lower it to make a
	// failure go away.
	const wantReads = 5
	if checked != wantReads {
		t.Errorf("found %d reads of cve_scan_vulns, want %d — if that is a real change, update "+
			"wantReads; if it is not, the FROM/JOIN detector has stopped seeing one", checked, wantReads)
	}
}

// TestScanScopeRe_RejectsTheNodeOnlyFilter proves the predicate this guard
// looks for cannot be satisfied by the filter that caused the leak, and IS
// satisfied by the fix. Without this, a regex typo would make the guard
// above pass on everything and report nothing.
func TestScanScopeRe_RejectsTheNodeOnlyFilter(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		where string
		want  bool
	}{
		{"the leak", "where scan_node_id = $1", false},
		{"the fix", "where scan_id = $1 and scan_node_id = $2", true},
		{"scan id second", "where severity = $1 and scan_id = $2", true},
		{"table qualified", "where v.scan_id = $1", true},
		{"a named parameter is not a positional one", "where scan_id = sqlc.arg('scan_id')", false},
		{"no filter at all", "order by package_name", false},
	} {
		if got := scanScopeRe.MatchString(tt.where); got != tt.want {
			t.Errorf("%s: scanScopeRe.MatchString(%q) = %v, want %v", tt.name, tt.where, got, tt.want)
		}
	}
}
