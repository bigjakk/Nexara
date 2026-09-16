package handlers

import (
	"go/ast"
	"sort"
	"testing"
)

// Static-analysis guard, in the same spirit as tracktask_guard_test.go: no
// database, no running server, so it fails CI the moment a handler calls one of
// these Proxmox endpoints without routing the error through the mapper that
// turns PVE's bare die() string into the status it deserves — 404 for "the
// object is not there", 409 for "the config changed under you".
//
// It exists because the unit tests around mapMissingObjectError call the
// mappers directly. That proves the mapping, not the wiring — reverting a
// handler to mapProxmoxError left every one of those tests green while quietly
// putting the 502 back on the operator's screen.
//
// The PVE die strings behind each entry are cited on the phrase-set var next to
// each mapper: haRuleMissingPhrases in ha.go, metricServerMissingPhrases in
// metric_servers.go, firewallRuleMissingPhrases in networks.go, and
// staleDigestPhrases in acme.go.
//
// BE CLEAR ABOUT WHAT THIS DOES NOT CATCH. It checks that the mapper is called
// somewhere in the same function, not that it is called on the right error; it
// keys on the method name, so a handler reaching the same PVE endpoint through
// a differently-named client method is invisible to it; and a handler that
// delegates the client call to a helper fails loudly here even when it is
// correct, since the two halves land in different functions.
//
// Deliberately absent: DeleteHARule. PVE's delete_rule runs an unconditional
// `delete $rules->{ids}->{$ruleid}`, and deleting an absent key is a no-op in
// Perl, so an already-deleted rule answers 200 and there is no error to map.
// Adding it here would demand unreachable code.
//
// That 200 is not nothing, though — it just is not an error. DeleteRule reads
// the case off its pre-delete snapshot instead (classifyHARuleDelete in ha.go),
// so the audit row records a no-op as a no-op rather than as a deletion.
var dieStringMappers = map[string]string{
	"UpdateHARule": "mapHARuleError",

	// Not a 404 like the rest: a digest mismatch means the node config moved
	// under the caller, which is 409. Same shape of bug though — PVE dies with
	// a plain 500 and no rejection map, so mapProxmoxError alone reports the
	// cluster as unreachable.
	"SetNodeACMEConfig":  "mapNodeConfigError",
	"GetMetricServer":    "mapMetricServerError",
	"UpdateMetricServer": "mapMetricServerError",
	"DeleteMetricServer": "mapMetricServerError",

	// The firewall rule writers, which established the 404 precedent this
	// generalises. Only the mutating ones: the client has no single-rule
	// getter, so a stale position can only be discovered by writing to it.
	"UpdateClusterFirewallRule": "mapFirewallRuleError",
	"DeleteClusterFirewallRule": "mapFirewallRuleError",
	"UpdateNodeFirewallRule":    "mapFirewallRuleError",
	"DeleteNodeFirewallRule":    "mapFirewallRuleError",
	"UpdateVMFirewallRule":      "mapFirewallRuleError",
	"DeleteVMFirewallRule":      "mapFirewallRuleError",
	"UpdateSecurityGroupRule":   "mapFirewallRuleError",
	"DeleteSecurityGroupRule":   "mapFirewallRuleError",
}

func TestGuard_DieStringEndpointsMapPastThe502(t *testing.T) {
	_, files := parseGoFiles(t, ".")

	type site struct{ fn, method, want string }
	var unmapped []site
	seen := map[string]bool{}

	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}

			// Which guarded client methods does this function call, and does it
			// call the mappers?
			called := map[string]bool{}
			mapped := map[string]bool{}
			// Selectors are counted wherever they appear, not only as a
			// call's Fun: `upd := pxClient.UpdateHARule; upd(...)` reaches the
			// same endpoint, and a check that only looked at CallExpr.Fun would
			// not see it.
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.SelectorExpr:
					if _, guarded := dieStringMappers[node.Sel.Name]; guarded {
						called[node.Sel.Name] = true
					}
				case *ast.CallExpr:
					if ident, ok := node.Fun.(*ast.Ident); ok {
						mapped[ident.Name] = true
					}
				}
				return true
			})

			for method := range called {
				want := dieStringMappers[method]
				seen[method] = true
				if !mapped[want] {
					unmapped = append(unmapped, site{qualifiedFuncName(fn), method, want})
				}
			}
		}
	}

	sort.Slice(unmapped, func(i, j int) bool { return unmapped[i].fn < unmapped[j].fn })
	for _, u := range unmapped {
		t.Errorf("%s calls %s but never %s — the operator would get a 502 instead",
			u.fn, u.method, u.want)
	}

	// A method nobody calls any more makes its entry — and the mapper it names —
	// dead weight that silently passes. Fail loudly instead of vacuously.
	for method := range dieStringMappers {
		if !seen[method] {
			t.Errorf("no handler calls %s; drop it from dieStringMappers or wire its caller", method)
		}
	}
}

// preflightFolders guards a different contract from dieStringMappers above: not
// "map PVE's die string to a status", but "a pre-flight check that could not
// run must not read as one that passed". foldPreflight is what turns a failed
// check into a blocking conflict, and without it the check contributes nothing
// and the strict policy waves the job through.
//
// It has its own map because the consequence is different — a gate that passes,
// not a 502 — and because these are internal/rolling analyzers rather than
// Proxmox client methods, so none of dieStringMappers' phrase-set reasoning
// applies to them.
//
// The swallow has been reintroduced twice, once in each handler, while every
// unit test stayed green: foldPreflight is exhaustively tested as a pure
// function and nothing proved it was called.
//
// KNOWN FALSE NEGATIVES, named rather than left to be inferred. It checks that
// foldPreflight is called somewhere in the same function, so all of these pass:
// passing a literal nil where the analyzer's error belongs (which reinstates
// the original bug exactly); discarding the hasErrors it returns; folding and
// never assigning the result; or a token call with all-nil arguments. What it
// does catch is the whole call being removed or reverted, which is what
// happened both times.
var preflightFolders = map[string]string{
	"AnalyzeHAConstraints": "foldPreflight",
	"AnalyzeCapacity":      "foldPreflight",
}

func TestGuard_PreflightChecksAreFolded(t *testing.T) {
	_, files := parseGoFiles(t, ".")

	seen := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			called := map[string]bool{}
			folded := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.SelectorExpr:
					if _, guarded := preflightFolders[node.Sel.Name]; guarded {
						called[node.Sel.Name] = true
					}
				case *ast.CallExpr:
					if ident, ok := node.Fun.(*ast.Ident); ok {
						folded[ident.Name] = true
					}
				}
				return true
			})
			for analyzer := range called {
				want := preflightFolders[analyzer]
				seen[analyzer] = true
				if !folded[want] {
					t.Errorf("%s calls %s but never %s — a pre-flight that could not run would read as one that passed, and the strict policy would let the job through",
						qualifiedFuncName(fn), analyzer, want)
				}
			}
		}
	}
	for analyzer := range preflightFolders {
		if !seen[analyzer] {
			t.Errorf("no handler calls %s; drop it from preflightFolders or wire its caller", analyzer)
		}
	}
}
