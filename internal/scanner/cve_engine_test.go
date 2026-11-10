package scanner

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// TestMatchUpdates is runScan's matching step end to end: a Debian binary is
// matched through the source the tracker's binary list gives it even when its
// own changelog cannot be read, and the node's note names only the changelog
// that mattered, a Proxmox build's.
func TestMatchUpdates(t *testing.T) {
	t.Parallel()
	e := NewEngine(nil, "encryption-key-32-bytes-padding!", slog.New(slog.DiscardHandler), nil)
	e.cveClient.trackerData = map[string]map[string]debianCVEEntry{"openssl": {"CVE-2026-0001": {
		Releases: map[string]debianRelease{"bookworm": {Status: "open", Urgency: "high"}},
	}}}
	e.cveClient.trackerLoadedAt = time.Now()
	e.cveClient.sourcesByRelease["bookworm"] = releaseSources{
		sources: map[string]string{"libssl3": "openssl"}, checked: time.Now(), ok: true,
	}
	every := newChangelogFetcher(fakeChangelogs{}, slog.New(slog.DiscardHandler)) // every fetch fails

	vulns, note := e.matchUpdates(context.Background(), "pve-01", "bookworm", []AptUpdateInfo{
		{Package: "libssl3", Origin: "Debian", OldVersion: "3.0.1-1", NewVersion: "3.0.2-1"},
		{Package: "libbar1", Origin: "Proxmox", OldVersion: "2.0-1", NewVersion: "2.0-2"},
	}, every)
	if len(vulns) != 1 || vulns[0].PackageName != "libssl3" || vulns[0].CVEID != "CVE-2026-0001" || vulns[0].Severity != "high" {
		t.Errorf("vulns = %+v, want libssl3's CVE-2026-0001 at the tracker's urgency", vulns)
	}
	if want := "Changelog unavailable for 1 of 2 packages (libbar1); their CVEs may be incomplete"; note != want {
		t.Errorf("note = %q, want %q", note, want)
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
