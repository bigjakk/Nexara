package handlers

import (
	"go/ast"
	"sort"
	"testing"
)

// Static-analysis guard, in the same spirit as tracktask_guard_test.go: no
// database, no running server, so it fails CI the moment a handler calls one of
// these Proxmox endpoints without routing the error through the mapper that
// turns PVE's "the object is not there" die into a 404.
//
// It exists because the unit tests around mapMissingObjectError call the
// mappers directly. That proves the mapping, not the wiring — reverting a
// handler to mapProxmoxError left every one of those tests green while quietly
// putting the 502 back on the operator's screen.
//
// The PVE die strings behind each entry are cited on the phrase-set var next to
// each mapper (haRuleMissingPhrases in ha.go, metricServerMissingPhrases in
// metric_servers.go).
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
var missingObjectMappers = map[string]string{
	"UpdateHARule":       "mapHARuleError",
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

func TestGuard_MissingObjectEndpointsMapTo404(t *testing.T) {
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
					if _, guarded := missingObjectMappers[node.Sel.Name]; guarded {
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
				want := missingObjectMappers[method]
				seen[method] = true
				if !mapped[want] {
					unmapped = append(unmapped, site{qualifiedFuncName(fn), method, want})
				}
			}
		}
	}

	sort.Slice(unmapped, func(i, j int) bool { return unmapped[i].fn < unmapped[j].fn })
	for _, u := range unmapped {
		t.Errorf("%s calls %s but never %s — a stale list would get a 502 instead of a 404",
			u.fn, u.method, u.want)
	}

	// A method nobody calls any more makes its entry — and the mapper it names —
	// dead weight that silently passes. Fail loudly instead of vacuously.
	for method := range missingObjectMappers {
		if !seen[method] {
			t.Errorf("no handler calls %s; drop it from missingObjectMappers or wire its caller", method)
		}
	}
}
