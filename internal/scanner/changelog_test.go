package scanner

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"
)

// fakeChangelogs answers apt/changelog as Proxmox does: by "pkg=version", a
// failed `apt-get changelog` as a 200 ending "RC: <exit code>", and a package
// it has no entry for as a failed call.
type fakeChangelogs map[string]string

func (f fakeChangelogs) GetNodeAptChangelog(_ context.Context, _, pkg, version string) (string, error) {
	if body, ok := f[pkg+"="+version]; ok {
		return body, nil
	}
	return "", errors.New("500 command 'apt-get changelog' failed: got timeout")
}

// TestResolveSources reads each update's source package from its changelog
// and reports every changelog it could not read, the 200 carrying apt's
// failure included, on each node the gap affects. A failed fetch used to be
// one Debug line, with the scan reading completed.
func TestResolveSources(t *testing.T) {
	t.Parallel()
	f := newChangelogFetcher(fakeChangelogs{
		"libssl3=3.0.2-1": "openssl (3.0.2-1) bookworm-security; urgency=medium\n\n  * Fix CVE-2026-0001.\n",
		"libbar1=2.0-1":   "E: Failed to fetch changelog:/bar.changelog  404  Not Found\nRC: 100",
	}, slog.New(slog.DiscardHandler))
	updates := []AptUpdateInfo{
		{Package: "libssl3", NewVersion: "3.0.2-1"},
		{Package: "libbar1", NewVersion: "2.0-1"},
		{Package: "libbaz1", NewVersion: "1.1-1"},
	}

	unreadable, firstErr := f.resolveSources(context.Background(), "pve-01", updates, nil)
	if got := []string{updates[0].SourcePackage, updates[1].SourcePackage, updates[2].SourcePackage}; !reflect.DeepEqual(got, []string{"openssl", "", ""}) {
		t.Errorf("sources = %q, want openssl and two unknown", got)
	}
	if want := []string{"libbar1", "libbaz1"}; !reflect.DeepEqual(unreadable, want) || firstErr == nil {
		t.Fatalf("unreadable = %v (first error %v), want %v and an error", unreadable, firstErr, want)
	}
	if got, want := changelogGapNote(unreadable, len(updates)),
		"Changelog unavailable for 2 of 3 packages (libbar1, libbaz1); their CVEs may be incomplete"; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}

	// The next node is served the cached failure, and its result lacks the changelog too.
	again, _ := f.resolveSources(context.Background(), "pve-02", []AptUpdateInfo{{Package: "libbar1", NewVersion: "2.0-1"}}, nil)
	if !reflect.DeepEqual(again, []string{"libbar1"}) {
		t.Errorf("second node's unreadable = %v, want [libbar1]", again)
	}
}

func TestExtractCVEsFromChangelog(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "empty",
			in:   "",
			want: nil,
		},
		{
			name: "no CVEs",
			in:   "Just a normal changelog entry with no security refs.",
			want: nil,
		},
		{
			name: "single CVE in proxmox-kernel changelog",
			in: `proxmox-kernel-6.17 (6.17.13-6) trixie; urgency=high

  * crypto: algif_aead - fix copy_from_iter() return value
    handling (CVE-2026-31431)

 -- Proxmox Support Team <support@proxmox.com>  Wed, 30 Apr 2026 14:23`,
			want: []string{"CVE-2026-31431"},
		},
		{
			name: "multiple CVEs deduplicated and sorted",
			in: `linux (6.1.170-1) bookworm-security; urgency=high
  * Fix CVE-2026-31431 (Copy Fail kernel privilege escalation)
  * Fix CVE-2024-1086 (use-after-free in nf_tables)
  * Backport: see also CVE-2026-31431 in earlier release`,
			want: []string{"CVE-2024-1086", "CVE-2026-31431"},
		},
		{
			name: "case-insensitive normalization",
			in:   "fix for cve-2024-1086 and CVE-2026-31431",
			want: []string{"CVE-2024-1086", "CVE-2026-31431"},
		},
		{
			name: "ignores year out of range",
			in:   "CVE-1800-0001 should not match (pre-1999)",
			want: nil,
		},
		{
			name: "rejects too-short sequence number",
			in:   "CVE-2024-12 should not match (3 digits)",
			want: nil,
		},
		{
			name: "accepts long sequence numbers",
			in:   "CVE-2024-1234567 is allowed",
			want: []string{"CVE-2024-1234567"},
		},
		{
			name: "common embedded patterns",
			in: `* Update fixes (CVE-2023-12345). See bug
              tracker. Also CVE-2024-99999, CVE-2024-99999.`,
			want: []string{"CVE-2023-12345", "CVE-2024-99999"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractCVEsFromChangelog(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("extractCVEsFromChangelog(%q)\n  got:  %v\n  want: %v", c.name, got, c.want)
			}
		})
	}
}

func TestExtractCVEsFromChangelogRange(t *testing.T) {
	// Realistic-shape changelog spanning three versions, each fixing a
	// different CVE. Tests that we extract only the entries newer than
	// the installed version and ≤ the target.
	text := `proxmox-kernel-6.17 (6.17.13-6) trixie; urgency=high

  * crypto: algif_aead — fix copy_from_iter return value (CVE-2026-31431)

 -- Proxmox Support  Wed, 30 Apr 2026 14:23:45 +0000

proxmox-kernel-6.17 (6.17.13-4) trixie; urgency=medium

  * memory: fix UAF in mm/page_alloc (CVE-2025-50000)

 -- Proxmox Support  Mon, 14 Apr 2026 11:02:11 +0000

proxmox-kernel-6.17 (6.17.13-2) trixie; urgency=low

  * net: fix Dirty Pipe redux (CVE-2022-0847)

 -- Proxmox Support  Fri, 28 Mar 2026 09:00:00 +0000`

	cases := []struct {
		name              string
		installed, target string
		want              []string
	}{
		{
			name:      "installed at -2, target -6: gets -4 and -6 entries",
			installed: "6.17.13-2",
			target:    "6.17.13-6",
			want:      []string{"CVE-2025-50000", "CVE-2026-31431"},
		},
		{
			name:      "installed at -4: gets only -6 entry",
			installed: "6.17.13-4",
			target:    "6.17.13-6",
			want:      []string{"CVE-2026-31431"},
		},
		{
			name:      "installed already at target: nothing new",
			installed: "6.17.13-6",
			target:    "6.17.13-6",
			want:      nil,
		},
		{
			name:      "no installed version: fall back to all CVEs",
			installed: "",
			target:    "6.17.13-6",
			want:      []string{"CVE-2022-0847", "CVE-2025-50000", "CVE-2026-31431"},
		},
		{
			name:      "no target cap: includes everything past installed",
			installed: "6.17.13-2",
			target:    "",
			want:      []string{"CVE-2025-50000", "CVE-2026-31431"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractCVEsFromChangelogRange(text, c.installed, c.target)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestExtractCVEsFromChangelogRangeFallback(t *testing.T) {
	// A changelog that doesn't conform to the Debian header format
	// should fall back to extracting all CVEs rather than dropping them.
	text := "Free-form text mentioning CVE-2024-1234 with no proper header."
	got := extractCVEsFromChangelogRange(text, "1.0", "2.0")
	want := []string{"CVE-2024-1234"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected fallback to surface %v, got %v", want, got)
	}
}

func TestExtractCVEsRejectsMalformed(t *testing.T) {
	// These all lack a valid CVE-YYYY-NNNN+ pattern; the regex should
	// produce no match. ("NotACVE-2024-1234" intentionally omitted: the
	// regex isn't word-anchored, so the embedded "CVE-2024-1234" matches
	// — that's the desired behavior in real changelogs.)
	bad := []string{
		"CVE-XXXX-1234", // non-numeric year
		"CVE_2024_1086", // wrong separator
		"CVE-21-1234",   // 2-digit year
		"CVE-2024",      // missing sequence
		"CVE-2024-",     // trailing dash, no digits
		"CVE-2024-12",   // 2-digit sequence (we require 4+)
	}
	for _, in := range bad {
		if got := extractCVEsFromChangelog(in); len(got) > 0 {
			t.Errorf("expected no match for %q, got %v", in, got)
		}
	}
}
