package drs

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// Evaluate-level coverage for the three listings it must ABORT on rather than
// proceed past. Each of them is already tested at the level of the helper that
// reads it — unhealthyHANodes and importHARules both have suites — and that is
// exactly what was insufficient: a helper returning an error proves nothing
// about the caller, and Evaluate is the caller that ships.
//
// internal/rolling/ha_listing_guard_test.go covers reverting these call sites
// to `_`. It says itself what it does not cover: an error that is checked and
// then mishandled. The realistic regression is not the discard, it is
//
//	unhealthy, err := unhealthyHANodes(ctx, client, e.logger, clusterID)
//	if err != nil {
//	    e.logger.Warn("DRS: HA maintenance filter unavailable, proceeding", "error", err)
//	    unhealthy = map[string]struct{}{}
//	}
//
// which reads as careful handling, passes the static guard, and restores the
// original bug in full. These tests are what make that edit go red.
//
// They also pin the other half of the contract: an Evaluate that returns an
// error must leave nothing behind. There is no partial write to undo, and that
// is a property worth asserting rather than assuming — see
// assertStoppedAtTheGate.

// stubEvalQueries stands in for *db.Queries so Evaluate can run without a
// database. Both methods are reads; the call counts are what let the tests
// tell how FAR the evaluation got before it gave up.
type stubEvalQueries struct {
	cfg        db.DrsConfig
	rules      []db.DrsRule
	cfgCalls   int
	rulesCalls int
}

func (s *stubEvalQueries) GetDRSConfig(context.Context, uuid.UUID) (db.DrsConfig, error) {
	s.cfgCalls++
	return s.cfg, nil
}

func (s *stubEvalQueries) ListDRSRules(context.Context, uuid.UUID) ([]db.DrsRule, error) {
	s.rulesCalls++
	return s.rules, nil
}

const (
	nodesPath       = "/api2/json/nodes"
	node1QemuPath   = "/api2/json/nodes/pve-01/qemu"
	node1LxcPath    = "/api2/json/nodes/pve-01/lxc"
	node1VMCfgPath  = "/api2/json/nodes/pve-01/qemu/101/config"
	node2QemuPath   = "/api2/json/nodes/pve-02/qemu"
	node2LxcPath    = "/api2/json/nodes/pve-02/lxc"
	evalThreshold   = 0.15
	evalGuestVMID   = 101
	evalGuestName   = "linux01"
	evalNodeMaxCPU  = 8
	evalNodeMaxMem  = 16 << 30
	evalGuestMemory = 8 << 30
)

// evaluateHandlers returns the full read-only endpoint set a healthy Evaluate
// touches, every one of them answering successfully. Tests override a single
// entry to break exactly one listing, so a failure can only come from the gate
// under test.
//
// The cluster is deliberately IMBALANCED — one guest on pve-01, nothing on
// pve-02 — and not because the planner is interesting here. It is what makes
// the rule read in assertStoppedAtTheGate load-bearing: on a balanced fixture
// Evaluate returns at the imbalance check before ListDRSRules on every path it
// can take, so "the DRS rules were never read" would hold whether the gate
// fired or not. Imbalanced, the successful path DOES read them — the control
// test asserts exactly that — so the error paths not reaching them is a fact
// about the gate rather than a fact about the fixture.
//
// Mode is advisory so /cluster/options — the native-CRS coexistence check,
// which is deliberately fail-open and has its own test — stays off the path.
func evaluateHandlers() map[string]http.HandlerFunc {
	empty := func(w http.ResponseWriter, _ *http.Request) { haData(w, []struct{}{}) }
	return map[string]http.HandlerFunc{
		nodesPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.NodeListEntry{
				{Node: "pve-01", Status: "online", MaxCPU: evalNodeMaxCPU, MaxMem: evalNodeMaxMem},
				{Node: "pve-02", Status: "online", MaxCPU: evalNodeMaxCPU, MaxMem: evalNodeMaxMem},
			})
		},
		node1QemuPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.VirtualMachine{{
				VMID: evalGuestVMID, Name: evalGuestName, Status: "running",
				CPU: 0.5, CPUs: 4, Mem: evalGuestMemory, MaxMem: evalGuestMemory,
			}})
		},
		// detectPassthrough reads every running QEMU guest's config. Empty =
		// no hostpci/usb entries, so the guest stays migratable.
		node1VMCfgPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, proxmox.VMConfig{})
		},
		node1LxcPath:  empty,
		node2QemuPath: empty,
		node2LxcPath:  empty,
		haStatusPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.HAStatusEntry{
				{ID: "quorum", Type: "quorum", Status: "OK"},
				{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", Status: "pve-01 (idle, Wed Apr 29 07:55:26 2026)"},
				{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02", Status: "pve-02 (active, watchdog active, Wed Apr 29 07:55:26 2026)"},
			})
		},
		haRulesPath: func(w http.ResponseWriter, _ *http.Request) {
			haData(w, []proxmox.HARuleEntry{})
		},
	}
}

// newEvaluateEngine wires an Engine to handlers through the two seams that
// exist for this: evalReads for the config/rule reads, newClient for the
// Proxmox side. Returns the engine, the query stub, and the set of paths the
// server was asked for.
func newEvaluateEngine(t *testing.T, handlers map[string]http.HandlerFunc) (*Engine, *stubEvalQueries, *sync.Map) {
	t.Helper()

	// Every endpoint Evaluate reaches must be read with GET. Asserting it at
	// the wire is what makes "Evaluate writes nothing to Proxmox" a tested
	// claim rather than a reading of the code. Errorf, not Fatalf: this runs
	// on the server's goroutine, where FailNow is not allowed.
	guarded := make(map[string]http.HandlerFunc, len(handlers))
	for path, h := range handlers {
		guarded[path] = func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("Evaluate sent %s %s; evaluation must only read", r.Method, r.URL.Path)
			}
			h(w, r)
		}
	}

	srv, hit := haTestServer(t, guarded)
	client := newHAClient(t, srv.URL)
	q := &stubEvalQueries{cfg: db.DrsConfig{
		Enabled:            true,
		Mode:               "advisory",
		Weights:            json.RawMessage(`{"cpu":0.3,"memory":0.7}`),
		ImbalanceThreshold: evalThreshold,
		IncludeContainers:  true,
	}}
	e := &Engine{
		logger:    slog.New(slog.DiscardHandler),
		evalReads: q,
		newClient: func(context.Context, uuid.UUID) (*proxmox.Client, error) { return client, nil },
	}
	return e, q, hit
}

// assertOnlyReadEndpoints checks Evaluate asked Proxmox for nothing outside
// the read endpoints the fixture serves. haTestServer records a path before
// dispatching it, so a request to anything unregistered — a migration POST, or
// the deprecated /cluster/ha/groups fallback — shows up here even though it
// was answered with a 404.
func assertOnlyReadEndpoints(t *testing.T, hit *sync.Map, allowed map[string]http.HandlerFunc) {
	t.Helper()
	var unexpected []string
	hit.Range(func(k, _ any) bool {
		path, _ := k.(string)
		if _, ok := allowed[path]; !ok {
			unexpected = append(unexpected, path)
		}
		return true
	})
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		t.Errorf("Evaluate touched Proxmox endpoints outside the read set: %v", unexpected)
	}
}

// assertStoppedAtTheGate checks that a failed Evaluate stopped where it said it
// did and left nothing behind.
//
// Nothing to undo is a claim about two stores. Proxmox is covered by
// assertOnlyReadEndpoints: every call Evaluate makes is a GET, and a write
// would have to appear as a path the fixture does not serve. The database side
// has three surfaces — evalReads here, veeamOwned (nil on this engine, and
// gated off anyway with exclude_veeam_workers unset), and the concrete handle
// that createClient's fallback uses (bypassed by the newClient seam) — and all
// of them are reads, so there is no write for a failed evaluation to leave
// half-done in the first place.
//
// The rule read is therefore not aimed at the check-then-swallow shape the
// tests above exist for: that one returns no error at all, so their own err
// assertions fire first and this never runs. What it catches is the other way
// of losing a gate — recording the error, carrying on, and returning it at the
// end. Evaluate would still look correct from the outside while having planned
// against the very rule set the gate was there to refuse. See evaluateHandlers
// for why this fixture, and not a balanced one, is what lets that be seen.
func assertStoppedAtTheGate(t *testing.T, q *stubEvalQueries, hit *sync.Map, allowed map[string]http.HandlerFunc) {
	t.Helper()
	if q.rulesCalls != 0 {
		t.Errorf("Evaluate went on to read DRS rules (%d times) after a safety gate failed; it should have stopped at the gate", q.rulesCalls)
	}
	assertOnlyReadEndpoints(t, hit, allowed)
}

// TestNewEngine_WiresEvalReadsAndLeavesTheClientSeamUnset pins the two halves
// of the seam contract that the tests below cannot see, because they build the
// Engine by struct literal and never call NewEngine.
//
// newClient == nil is the load-bearing one. It is the only thing keeping the
// branch this file's fixture takes out of production: an Engine that shipped
// with it set would bypass the Proxmox client cache entirely — silently, since
// createClient would still hand back a working client.
func TestNewEngine_WiresEvalReadsAndLeavesTheClientSeamUnset(t *testing.T) {
	// A real (unconnected) *db.Queries, not a nil one: assigning a nil
	// pointer to an interface yields a non-nil interface, which would make
	// the evalReads check below pass without proving anything.
	e := NewEngine(db.New(nil), "", slog.New(slog.DiscardHandler))
	if e.evalReads == nil {
		t.Error("NewEngine left evalReads nil; Evaluate would nil-deref on every cluster")
	}
	if e.newClient != nil {
		t.Error("NewEngine assigned newClient; production would bypass the Proxmox client cache")
	}
}

// TestEvaluate_StopsOnAnUnreadableHAStatus is gate 1 at the caller: an
// unreadable /cluster/ha/status/current must end the evaluation. Swallowing it
// for an empty skip set makes every node look eligible, including the one
// Proxmox HA is evacuating for reboot.
func TestEvaluate_StopsOnAnUnreadableHAStatus(t *testing.T) {
	handlers := evaluateHandlers()
	handlers[haStatusPath] = func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}

	e, q, hit := newEvaluateEngine(t, handlers)
	result, err := e.Evaluate(context.Background(), uuid.New())
	if err == nil {
		t.Fatalf("an unreadable HA status was swallowed; Evaluate returned %+v and DRS would plan against a cleared maintenance filter", result)
	}
	if result != nil {
		t.Errorf("result = %+v alongside the error, want nil", result)
	}
	if !strings.Contains(err.Error(), "HA status") {
		t.Errorf("error %q does not name the HA status read it came from", err)
	}
	assertStoppedAtTheGate(t, q, hit, handlers)
}

// TestEvaluate_StopsWhenNoHALRMStateParses is gate 2 at the caller. The read
// succeeds here — this is the format-change signature, where every LRM entry
// decodes and none of them yields a state. classifyHAEntries counts an
// unparseable state as healthy, so all-unparseable produces a skip set that is
// byte-identical to a healthy cluster's; only Evaluate failing tells them
// apart.
func TestEvaluate_StopsWhenNoHALRMStateParses(t *testing.T) {
	handlers := evaluateHandlers()
	handlers[haStatusPath] = func(w http.ResponseWriter, _ *http.Request) {
		// Bare tokens: the "<node> (<state>, ...)" form gone.
		haData(w, []proxmox.HAStatusEntry{
			{ID: "lrm:pve-01", Type: "lrm", Node: "pve-01", Status: "active"},
			{ID: "lrm:pve-02", Type: "lrm", Node: "pve-02", Status: "maintenance"},
		})
	}

	e, q, hit := newEvaluateEngine(t, handlers)
	result, err := e.Evaluate(context.Background(), uuid.New())
	if err == nil {
		t.Fatalf("an HA status in which nothing parsed was read as a healthy cluster; Evaluate returned %+v", result)
	}
	if result != nil {
		t.Errorf("result = %+v alongside the error, want nil", result)
	}
	if !strings.Contains(err.Error(), "LRM") {
		t.Errorf("error %q does not name the unparseable LRM entries it came from", err)
	}
	assertStoppedAtTheGate(t, q, hit, handlers)
}

// TestEvaluate_StopsOnAnUnreadableHARuleListing is gate 3 at the caller. An
// empty rule set is what a cluster with no affinity rules looks like, so
// proceeding on an unreadable listing is how DRS migrates a guest off its
// node-affinity pin, or onto the node holding its anti-affinity partner, with
// nothing in the log.
func TestEvaluate_StopsOnAnUnreadableHARuleListing(t *testing.T) {
	handlers := evaluateHandlers()
	handlers[haRulesPath] = func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}

	e, q, hit := newEvaluateEngine(t, handlers)
	result, err := e.Evaluate(context.Background(), uuid.New())
	if err == nil {
		t.Fatalf("an unreadable HA rule listing was swallowed; Evaluate returned %+v and DRS would plan with every pin invisible to it", result)
	}
	if result != nil {
		t.Errorf("result = %+v alongside the error, want nil", result)
	}
	if !strings.Contains(err.Error(), "import HA rules") {
		t.Errorf("error %q does not name the HA rule import it came from", err)
	}
	// A 500 is not a PVE-too-old 501, so it must not fall through to the
	// deprecated groups endpoint — assertStoppedAtTheGate catches that, since
	// /cluster/ha/groups is not in the fixture's read set.
	assertStoppedAtTheGate(t, q, hit, handlers)
}

// TestEvaluate_CompletesWhenEveryListingReads is the control, and it carries
// two loads. Without it the three tests above could pass for any reason at all
// — a 404 on /nodes, a client never built — and would keep passing with the
// gates deleted; asserting both guarded listings were actually requested is
// what rules that out. It is also what makes the rule read in
// assertStoppedAtTheGate mean something: this fixture reaches ListDRSRules on
// the successful path, so the error paths not reaching it is the gate's doing.
func TestEvaluate_CompletesWhenEveryListingReads(t *testing.T) {
	handlers := evaluateHandlers()
	e, q, hit := newEvaluateEngine(t, handlers)

	result, err := e.Evaluate(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Evaluate on a fixture where every listing reads: %v", err)
	}
	if result == nil {
		t.Fatal("result = nil on a cluster with DRS enabled")
	}
	if q.cfgCalls != 1 {
		t.Errorf("GetDRSConfig called %d times, want 1", q.cfgCalls)
	}
	if q.rulesCalls != 1 {
		t.Fatalf("ListDRSRules called %d times, want 1; the fixture no longer reaches the rule read, which leaves assertStoppedAtTheGate asserting nothing", q.rulesCalls)
	}
	for _, path := range []string{haStatusPath, haRulesPath} {
		if _, queried := hit.Load(path); !queried {
			t.Errorf("%s was never requested; the gate under test is not on Evaluate's path", path)
		}
	}
	if result.Imbalance <= evalThreshold {
		t.Errorf("imbalance = %v, want above the %v threshold so the planner is reached", result.Imbalance, evalThreshold)
	}
	assertOnlyReadEndpoints(t, hit, handlers)
}
