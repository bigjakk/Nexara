package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestMatchUpdates is runScan's matching step end to end, from what Proxmox's
// apt/update gives, the tracker and the changelogs together.
func TestMatchUpdates(t *testing.T) {
	t.Parallel()
	libssl3 := proxmox.AptUpdate{Package: "libssl3", Origin: "Debian", OldVersion: "3.0.1-1", NewVersion: "3.0.2-1"}
	cases := []struct {
		name       string
		changelogs fakeChangelogs // a package with none fails its fetch
		updates    []proxmox.AptUpdate
		want       []string // "package cve severity fixed in"
		wantNote   string
	}{
		{
			// Through the source the tracker's binary list gives it, though
			// its changelog cannot be read; the note names only the changelog
			// that mattered, a Proxmox build's.
			name:     "a Debian binary is matched through its source",
			updates:  []proxmox.AptUpdate{libssl3, {Package: "libbar1", Origin: "Proxmox", OldVersion: "2.0-1", NewVersion: "2.0-2"}},
			want:     []string{`libssl3 CVE-2026-0001 high fixed in ""`},
			wantNote: "Changelog unavailable for 1 of 2 packages (libbar1); their CVEs may be incomplete",
		},
		{
			// The tracker's copy can predate the fix.
			name:       "an open CVE the pending changelog names is fixed in the pending version",
			changelogs: fakeChangelogs{"libssl3=3.0.2-1": "openssl (3.0.2-1) bookworm; urgency=high\n\n  * Fix CVE-2026-0001.\n"},
			updates:    []proxmox.AptUpdate{libssl3},
			want:       []string{`libssl3 CVE-2026-0001 high fixed in "3.0.2-1"`},
		},
		{
			// A node that skipped the fix: the upgrade's changelog names it too.
			name: "a CVE the tracker has a fix for keeps that version",
			changelogs: fakeChangelogs{"libfoo1=1.0-3": "foo (1.0-3) bookworm; urgency=medium\n\n  * Point release.\n\n" +
				"foo (1.0-2) bookworm-security; urgency=high\n\n  * Fix CVE-2026-0002.\n"},
			updates: []proxmox.AptUpdate{{Package: "libfoo1", Origin: "Debian", OldVersion: "1.0-1", NewVersion: "1.0-3"}},
			want:    []string{`libfoo1 CVE-2026-0002 high fixed in "1.0-2"`},
		},
		// apt/update gives Debian's security archive the Origin "Debian", like
		// the main archive, and a Priority "required" was taken for security:
		// a security update is an upload to <release>-security since the
		// installed version, read from the changelog.
		{
			name: "an ordinary update of a required package is not a security update",
			changelogs: fakeChangelogs{"libqux1=2.0-1+deb12u3": "qux (2.0-1+deb12u3) bookworm; urgency=medium\n\n  * Point release.\n\n" +
				"qux (2.0-1+deb12u2) bookworm-security; urgency=high\n\n  * Security fix, installed already.\n"},
			updates: []proxmox.AptUpdate{{Package: "libqux1", Origin: "Debian", Priority: "required", OldVersion: "2.0-1+deb12u2", NewVersion: "2.0-1+deb12u3"}},
		},
		{
			name:       "a security upload the tracker has no entry for",
			changelogs: fakeChangelogs{"libbaz1=1.1-1+deb12u1": "baz (1.1-1+deb12u1) bookworm-security; urgency=high\n\n  * Security fix.\n"},
			updates:    []proxmox.AptUpdate{{Package: "libbaz1", Origin: "Debian", Priority: "optional", OldVersion: "1.1-1", NewVersion: "1.1-1+deb12u1"}},
			want:       []string{`libbaz1 UNKNOWN-libbaz1 medium fixed in "1.1-1+deb12u1"`},
		},
		{
			name:       "not a security upload of Debian's kernel, which no Proxmox node runs",
			changelogs: fakeChangelogs{"linux-libc-dev=6.1.119-1": "linux (6.1.119-1) bookworm-security; urgency=high\n\n  * New upstream stable update.\n"},
			updates:    []proxmox.AptUpdate{{Package: "linux-libc-dev", Origin: "Debian", Priority: "optional", OldVersion: "6.1.115-1", NewVersion: "6.1.119-1"}},
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

			vulns, note := e.matchUpdates(context.Background(), "pve-01", "bookworm", aptUpdateInfos(tc.updates), changelogs)
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
