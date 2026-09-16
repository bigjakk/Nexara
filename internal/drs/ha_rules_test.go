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
	haStatusPath    = "/api2/json/cluster/ha/status/current"
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
	rules, err := e.importHARules(context.Background(), newHAClient(t, srv.URL), nil)
	if err != nil {
		t.Fatalf("importHARules: %v", err)
	}

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
	rules, err := e.importHARules(context.Background(), newHAClient(t, srv.URL), nil)
	if err != nil {
		t.Fatalf("importHARules: %v", err)
	}

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
	rules, err := e.importHARules(context.Background(), newHAClient(t, srv.URL), nil)
	if err != nil {
		t.Fatalf("importHARules: %v", err)
	}

	if _, queried := hit.Load(haGroupsPath); !queried {
		t.Fatal("expected fallback to /cluster/ha/groups on PVE 8")
	}
	if len(rules) != 1 || rules[0].Type != RuleTypePin || len(rules[0].VMIDs) != 1 || rules[0].VMIDs[0] != 100 {
		t.Fatalf("expected a legacy pin rule for VM 100, got %+v", rules)
	}
}

// groupsMigrated answers the way PVE 9 soft-disables the groups endpoint.
func groupsMigrated(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data":    nil,
		"message": "cannot index groups: ha groups have been migrated to rules\n",
	})
}

// TestImportHARules_GroupsMigratedErrorIsQuiet verifies that a legacy path
// reached against a PVE reporting groups as migrated to rules handles that 500
// quietly rather than logging a warning on every evaluation. It is PVE saying
// there are no groups here, which is a complete answer.
//
// It reaches the legacy path through a 501 now, not through a blipped rules
// call: a rules listing that merely failed no longer falls through at all.
//
// Which makes the cluster it models defensive rather than reachable — one node
// would have to answer 501 for /cluster/ha/rules AND "migrated to rules" for
// /cluster/ha/groups, and those are mutually exclusive pve-ha-manager versions
// (failover picks the endpoint once per client, so both calls hit the same
// node). The branch is kept as cheap insurance and this pins it; do not read a
// green run here as evidence a real PVE takes this path.
func TestImportHARules_GroupsMigratedErrorIsQuiet(t *testing.T) {
	wc := &warnCounter{}
	srv, _ := haTestServer(t, map[string]http.HandlerFunc{
		haRulesPath: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not implemented", http.StatusNotImplemented)
		},
		haResourcesPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.HAResource{})
		},
		haGroupsPath: groupsMigrated,
	})
	e := &Engine{logger: slog.New(wc)}
	rules, err := e.importHARules(context.Background(), newHAClient(t, srv.URL), nil)
	if err != nil {
		t.Fatalf("importHARules: %v", err)
	}

	if len(rules) != 0 {
		t.Fatalf("expected no rules, got %+v", rules)
	}
	if wc.warns != 0 {
		t.Fatalf("the 'migrated to rules' response must not warn, got %d", wc.warns)
	}
}

// TestImportHARules_UnreadableRulesAreSurfaced is the regression test for the
// three-outcome collapse.
//
// An HA listing answers one of three things: here are the rules, this PVE has
// none, or nobody could look. Only the middle one is a fallback. This used to
// fall back on ANY error — and the legacy path then returned nil rules — so a
// transient 500 on a PVE 9 cluster produced an empty rule set, which the
// planner cannot tell from a cluster with no affinity rules configured. DRS
// went on to live-migrate guests with every node-affinity pin and
// anti-affinity pairing invisible to it.
//
// The status table matters as much as the outcome:
//
//   - 500/503: the cluster could not answer. Surface it.
//   - 404: a bare ErrNotFound with the body discarded. Nexara sits behind
//     nginx/Traefik/Caddy, so a proxy rewrite or a misrouted path arrives
//     exactly like this, and no real PVE answers 404 here — admitting it as
//     "PVE 8" would only ever admit impostors.
//   - 403: an API token missing the HA privilege, not a PVE without rules.
//
// Each case also asserts /cluster/ha/groups was never touched: reaching the
// legacy path at all is the mechanism by which the rule set came back empty.
func TestImportHARules_UnreadableRulesAreSurfaced(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
	}{
		{"transient server error", http.StatusInternalServerError},
		{"cluster not quorate", http.StatusServiceUnavailable},
		{"a proxy or rewrite answering 404", http.StatusNotFound},
		{"token lacks the HA privilege", http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, hit := haTestServer(t, map[string]http.HandlerFunc{
				haRulesPath: func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "nope", tt.status)
				},
				haResourcesPath: func(w http.ResponseWriter, _ *http.Request) {
					haData(w, []proxmox.HAResource{})
				},
				haGroupsPath: groupsMigrated,
			})
			e := &Engine{logger: slog.Default()}
			rules, err := e.importHARules(context.Background(), newHAClient(t, srv.URL), nil)
			if err == nil {
				t.Fatalf("a %d on /cluster/ha/rules read as a cluster with no HA rules (got %+v); DRS would migrate unconstrained", tt.status, rules)
			}
			if rules != nil {
				t.Errorf("rules = %+v alongside the error, want nil", rules)
			}
			if _, queried := hit.Load(haGroupsPath); queried {
				t.Error("fell back to /cluster/ha/groups on a listing that merely failed — only a 501 means this PVE has no rules endpoint")
			}
		})
	}
}

// TestImportHARulesLegacy_UnreadableListingsAreSurfaced holds the same line one
// layer down.
//
// On a genuine PVE 8 the legacy resources + groups pair IS the rule source, and
// it used to warn-and-return-nil on any failure — the identical collapse, with
// the identical consequence: an empty rule set that reads as a cluster with no
// restricted HA groups. "migrated to rules" stays benign because it is an
// answer, not a failure.
func TestImportHARulesLegacy_UnreadableListingsAreSurfaced(t *testing.T) {
	pve8Rules := func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
	for _, tt := range []struct {
		name      string
		resources http.HandlerFunc
		groups    http.HandlerFunc
		wantErr   bool
	}{
		{
			name:      "resources listing fails",
			resources: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) },
			groups: func(w http.ResponseWriter, _ *http.Request) {
				haData(w, []proxmox.HAGroup{})
			},
			wantErr: true,
		},
		{
			name: "groups listing fails for a reason other than the migration",
			resources: func(w http.ResponseWriter, _ *http.Request) {
				haData(w, []proxmox.HAResource{{SID: "vm:100", Group: "restricted-grp"}})
			},
			groups:  func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) },
			wantErr: true,
		},
		{
			name: "groups migrated to rules is an answer, not a failure",
			resources: func(w http.ResponseWriter, _ *http.Request) {
				haData(w, []proxmox.HAResource{})
			},
			groups:  groupsMigrated,
			wantErr: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := haTestServer(t, map[string]http.HandlerFunc{
				haRulesPath:     pve8Rules,
				haResourcesPath: tt.resources,
				haGroupsPath:    tt.groups,
			})
			e := &Engine{logger: slog.Default()}
			rules, err := e.importHARules(context.Background(), newHAClient(t, srv.URL), nil)
			if tt.wantErr && err == nil {
				t.Fatalf("an unreadable legacy listing read as a cluster with no HA groups (got %+v)", rules)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("importHARules: %v", err)
			}
			if len(rules) != 0 {
				t.Errorf("rules = %+v, want none", rules)
			}
		})
	}
}
