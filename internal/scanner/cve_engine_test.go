package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"testing"
	"time"
)

// TestMatchUpdates is runScan's matching step end to end, the tracker and the
// changelogs together.
func TestMatchUpdates(t *testing.T) {
	t.Parallel()
	libssl3 := AptUpdateInfo{Package: "libssl3", Origin: "Debian", OldVersion: "3.0.1-1", NewVersion: "3.0.2-1"}
	cases := []struct {
		name       string
		changelogs fakeChangelogs // a package with none fails its fetch
		updates    []AptUpdateInfo
		want       []string // "package cve severity fixed in"
		wantNote   string
	}{
		{
			// Through the source the tracker's binary list gives it, though
			// its changelog cannot be read; the note names only the changelog
			// that mattered, a Proxmox build's.
			name:     "a Debian binary is matched through its source",
			updates:  []AptUpdateInfo{libssl3, {Package: "libbar1", Origin: "Proxmox", OldVersion: "2.0-1", NewVersion: "2.0-2"}},
			want:     []string{`libssl3 CVE-2026-0001 high fixed in ""`},
			wantNote: "Changelog unavailable for 1 of 2 packages (libbar1); their CVEs may be incomplete",
		},
		{
			// The tracker's copy can predate the fix.
			name:       "an open CVE the pending changelog names is fixed in the pending version",
			changelogs: fakeChangelogs{"libssl3=3.0.2-1": "openssl (3.0.2-1) bookworm; urgency=high\n\n  * Fix CVE-2026-0001.\n"},
			updates:    []AptUpdateInfo{libssl3},
			want:       []string{`libssl3 CVE-2026-0001 high fixed in "3.0.2-1"`},
		},
		{
			// A node that skipped the fix: the upgrade's changelog names it too.
			name: "a CVE the tracker has a fix for keeps that version",
			changelogs: fakeChangelogs{"libfoo1=1.0-3": "foo (1.0-3) bookworm; urgency=medium\n\n  * Point release.\n\n" +
				"foo (1.0-2) bookworm-security; urgency=high\n\n  * Fix CVE-2026-0002.\n"},
			updates: []AptUpdateInfo{{Package: "libfoo1", Origin: "Debian", OldVersion: "1.0-1", NewVersion: "1.0-3"}},
			want:    []string{`libfoo1 CVE-2026-0002 high fixed in "1.0-2"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := NewEngine(nil, "encryption-key-32-bytes-padding!", slog.New(slog.DiscardHandler), nil)
			e.cveClient.trackerData = map[string]map[string]debianCVEEntry{
				"openssl": {"CVE-2026-0001": {Releases: map[string]debianRelease{"bookworm": {Status: "open", Urgency: "high"}}}},
				"foo":     {"CVE-2026-0002": {Releases: map[string]debianRelease{"bookworm": {Status: "resolved", FixedVersion: "1.0-2", Urgency: "high"}}}},
			}
			e.cveClient.trackerLoadedAt = time.Now()
			e.cveClient.sourcesByRelease["bookworm"] = releaseSources{
				sources: map[string]string{"libssl3": "openssl"}, checked: time.Now(), ok: true,
			}
			changelogs := newChangelogFetcher(tc.changelogs, slog.New(slog.DiscardHandler))

			vulns, note := e.matchUpdates(context.Background(), "pve-01", "bookworm", tc.updates, changelogs)
			var got []string
			for _, v := range vulns {
				got = append(got, fmt.Sprintf("%s %s %s fixed in %q", v.PackageName, v.CVEID, v.Severity, v.FixedVersion))
			}
			if !reflect.DeepEqual(got, tc.want) || note != tc.wantNote {
				t.Errorf("got %v with note %q, want %v with note %q", got, note, tc.want, tc.wantNote)
			}
		})
	}
}

// TestNewEngine_ConstructsLongLivedClients locks down the contract that
// the engine builds the per-feed clients exactly once. Before the Phase
// 3.8 refactor, runScan constructed a fresh CVEClient (with empty
// trackerData) on every cluster scan, defeating the cacheTTL.
func TestNewEngine_ConstructsLongLivedClients(t *testing.T) {
	t.Parallel()
	e := NewEngine(nil, "encryption-key-32-bytes-padding!", nil, nil)
	if e == nil {
		t.Fatal("NewEngine returned nil")
	}
	if e.httpClient == nil {
		t.Fatal("expected shared httpClient on engine")
	}
	if e.cveClient == nil || e.kevClient == nil || e.epssClient == nil {
		t.Fatal("expected per-feed clients constructed on engine")
	}
	if e.cveClient.httpClient != e.httpClient {
		t.Error("expected CVEClient to share engine httpClient")
	}
	if e.kevClient.httpClient != e.httpClient {
		t.Error("expected KEVClient to share engine httpClient")
	}
	if e.epssClient.httpClient != e.httpClient {
		t.Error("expected EPSSClient to share engine httpClient")
	}
}

func TestNewEngine_KEVClientAccessor(t *testing.T) {
	t.Parallel()
	e := NewEngine(nil, "encryption-key-32-bytes-padding!", nil, nil)
	got := e.KEVClient()
	if got == nil {
		t.Fatal("KEVClient() returned nil")
	}
	if got != e.kevClient {
		t.Fatal("KEVClient() returned a different instance than the engine's field")
	}
}
