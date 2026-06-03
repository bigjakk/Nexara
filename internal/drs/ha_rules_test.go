package drs

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// warnCounter is a slog.Handler that counts records at WARN level or above. It
// backs the assertion that the PVE 9 HA-rules path does not log a warning on
// every DRS evaluation (the original "HA groups migrated to rules" spam).
type warnCounter struct {
	mu    sync.Mutex
	warns int
}

func (h *warnCounter) Enabled(context.Context, slog.Level) bool { return true }
func (h *warnCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		h.mu.Lock()
		h.warns++
		h.mu.Unlock()
	}
	return nil
}
func (h *warnCounter) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *warnCounter) WithGroup(string) slog.Handler      { return h }

const (
	haRulesPath     = "/api2/json/cluster/ha/rules"
	haGroupsPath    = "/api2/json/cluster/ha/groups"
	haResourcesPath = "/api2/json/cluster/ha/resources"
)

// haTestServer serves the Proxmox HA endpoints from handlers and records which
// paths were requested in the returned sync.Map.
func haTestServer(t *testing.T, handlers map[string]http.HandlerFunc) (*httptest.Server, *sync.Map) {
	t.Helper()
	hit := &sync.Map{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(r.URL.Path, true)
		if h, ok := handlers[r.URL.Path]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, hit
}

// haData writes v inside the Proxmox {"data": ...} envelope.
func haData(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": v})
}

func newHAClient(t *testing.T, baseURL string) *proxmox.Client {
	t.Helper()
	c, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:     baseURL,
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestImportHARules_PVE9RulesUsedAndGroupsNotQueried verifies that when the
// PVE 9+ rules API returns rules, they are imported and the deprecated groups
// endpoint is never touched.
func TestImportHARules_PVE9RulesUsedAndGroupsNotQueried(t *testing.T) {
	srv, hit := haTestServer(t, map[string]http.HandlerFunc{
		haRulesPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.HARuleEntry{
				{Rule: "pin-100", Type: "node-affinity", Resources: "vm:100", Nodes: "pve1", Strict: 1},
			})
		},
	})
	e := &Engine{logger: slog.Default()}
	rules := e.importHARules(context.Background(), newHAClient(t, srv.URL), nil)

	if len(rules) != 1 || rules[0].Type != RuleTypePin {
		t.Fatalf("expected 1 pin rule, got %+v", rules)
	}
	if len(rules[0].VMIDs) != 1 || rules[0].VMIDs[0] != 100 {
		t.Fatalf("expected VMID 100, got %+v", rules[0].VMIDs)
	}
	if _, queried := hit.Load(haGroupsPath); queried {
		t.Fatal("legacy /cluster/ha/groups must not be queried on PVE 9")
	}
}

// TestImportHARules_EmptyPVE9RulesDoNotFallBackToGroups is the regression test
// for the dispatch bug: an available-but-empty rules API must not fall through
// to /cluster/ha/groups (which PVE 9 answers with a 500 on every cycle).
func TestImportHARules_EmptyPVE9RulesDoNotFallBackToGroups(t *testing.T) {
	wc := &warnCounter{}
	srv, hit := haTestServer(t, map[string]http.HandlerFunc{
		haRulesPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.HARuleEntry{}) // PVE 9, no rules defined
		},
	})
	e := &Engine{logger: slog.New(wc)}
	rules := e.importHARules(context.Background(), newHAClient(t, srv.URL), nil)

	if len(rules) != 0 {
		t.Fatalf("expected no rules, got %+v", rules)
	}
	if _, queried := hit.Load(haGroupsPath); queried {
		t.Fatal("an available-but-empty rules API must NOT fall back to /cluster/ha/groups")
	}
	if wc.warns != 0 {
		t.Fatalf("expected no warnings on an empty PVE 9 rule set, got %d", wc.warns)
	}
}

// TestImportHARules_FallsBackToGroupsOnPVE8 verifies the legacy path still runs
// when the rules endpoint is genuinely unavailable (PVE 8).
func TestImportHARules_FallsBackToGroupsOnPVE8(t *testing.T) {
	srv, hit := haTestServer(t, map[string]http.HandlerFunc{
		haRulesPath: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not implemented", http.StatusNotImplemented) // PVE 8: no rules API
		},
		haResourcesPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.HAResource{{SID: "vm:100", Group: "restricted-grp"}})
		},
		haGroupsPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.HAGroup{{Group: "restricted-grp", Nodes: "pve1:100,pve2", Restricted: 1}})
		},
	})
	e := &Engine{logger: slog.Default()}
	rules := e.importHARules(context.Background(), newHAClient(t, srv.URL), nil)

	if _, queried := hit.Load(haGroupsPath); !queried {
		t.Fatal("expected fallback to /cluster/ha/groups on PVE 8")
	}
	if len(rules) != 1 || rules[0].Type != RuleTypePin || len(rules[0].VMIDs) != 1 || rules[0].VMIDs[0] != 100 {
		t.Fatalf("expected a legacy pin rule for VM 100, got %+v", rules)
	}
}

// TestImportHARules_GroupsMigratedErrorIsQuiet verifies that if the legacy path
// is reached on PVE 9 (e.g. the rules call blipped) the "migrated to rules"
// 500 is handled quietly rather than logged as a warning.
func TestImportHARules_GroupsMigratedErrorIsQuiet(t *testing.T) {
	wc := &warnCounter{}
	srv, _ := haTestServer(t, map[string]http.HandlerFunc{
		haRulesPath: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError) // rules momentarily unavailable
		},
		haResourcesPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.HAResource{})
		},
		haGroupsPath: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data":    nil,
				"message": "cannot index groups: ha groups have been migrated to rules\n",
			})
		},
	})
	e := &Engine{logger: slog.New(wc)}
	rules := e.importHARules(context.Background(), newHAClient(t, srv.URL), nil)

	if len(rules) != 0 {
		t.Fatalf("expected no rules, got %+v", rules)
	}
	if wc.warns != 0 {
		t.Fatalf("the 'migrated to rules' response must not warn, got %d", wc.warns)
	}
}
