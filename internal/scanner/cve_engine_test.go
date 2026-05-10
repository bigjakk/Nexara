package scanner

import (
	"testing"
)

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
