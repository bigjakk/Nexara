package handlers

import (
	"strings"
	"testing"
)

// TestNodeReportFilename pins the sanitising of a node name that is about to
// be interpolated into a Content-Disposition header.
//
// The node name comes from the URL, so a name carrying a quote or a CRLF would
// otherwise break out of the quoted filename and append headers of the
// attacker's choosing to the response. The function folds everything outside
// [A-Za-z0-9._-] to "-", so the assertions below are written as "no character
// outside the safe set survives" rather than as a list of blocked characters —
// a new dangerous character in a future header spec is already covered.
func TestNodeReportFilename(t *testing.T) {
	tests := []struct {
		name     string
		node     string
		contains string // expected in the middle of the filename
	}{
		{name: "ordinary node name", node: "pve-01", contains: "pve-01"},
		{name: "dots and underscores survive", node: "pve_01.lab", contains: "pve_01.lab"},
		{name: "quote is folded", node: `pve"01`, contains: "pve-01"},
		{name: "CRLF header injection is folded", node: "pve\r\nX-Evil: 1", contains: "pve--X-Evil--1"},
		{name: "path traversal is folded", node: "../../etc/passwd", contains: "etc-passwd"},
		{name: "spaces are folded", node: "my node", contains: "my-node"},
		{name: "unicode is folded", node: "pve-ölnöde", contains: "pve-"},
		{name: "empty falls back", node: "", contains: "node"},
		{name: "all-unsafe falls back", node: "///", contains: "node"},
	}

	const safe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_."

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nodeReportFilename(tt.node)

			for _, r := range got {
				if !strings.ContainsRune(safe, r) {
					t.Errorf("nodeReportFilename(%q) = %q: contains unsafe rune %q", tt.node, got, r)
				}
			}
			if !strings.HasPrefix(got, "nexara-report-") {
				t.Errorf("nodeReportFilename(%q) = %q: want prefix nexara-report-", tt.node, got)
			}
			if !strings.HasSuffix(got, ".txt") {
				t.Errorf("nodeReportFilename(%q) = %q: want suffix .txt", tt.node, got)
			}
			if !strings.Contains(got, tt.contains) {
				t.Errorf("nodeReportFilename(%q) = %q: want it to contain %q", tt.node, got, tt.contains)
			}
		})
	}
}

// TestNodeReportFilenameHasTimestampSuffix guards the timestamp. Downloading
// the same node's report twice must not produce two files the browser silently
// overwrites — the second is usually the one being compared against the first.
// This asserts the suffix's shape, not that two calls differ: the format has
// second granularity, so two calls in the same second legitimately collide.
func TestNodeReportFilenameHasTimestampSuffix(t *testing.T) {
	got := nodeReportFilename("pve-01")
	parts := strings.Split(strings.TrimSuffix(got, ".txt"), "-")
	// nexara, report, pve, 01, <date>, <time>
	if len(parts) < 3 {
		t.Fatalf("nodeReportFilename = %q: expected a timestamp suffix", got)
	}
	stamp := parts[len(parts)-2] + "-" + parts[len(parts)-1]
	if len(stamp) != len("20060102-150405") {
		t.Errorf("nodeReportFilename = %q: timestamp %q is not YYYYMMDD-HHMMSS", got, stamp)
	}
}
