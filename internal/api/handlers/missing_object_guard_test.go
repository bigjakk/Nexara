package handlers

import (
	"go/ast"
	"sort"
	"testing"
)

// Static-analysis guard, in the spirit of tracktask_guard_test.go: it fails when a handler calls one of
// these Proxmox endpoints without routing the error through the mapper that turns PVE's bare die()
// string into 404 ("not there") or 409 ("changed under you"). The mapMissingObjectError unit tests
// prove the mapping, not the wiring: reverting a handler to mapProxmoxError left them green and put
// the 502 back on the operator's screen. The die strings are cited on each mapper's phrase-set var
// (haRuleMissingPhrases, metricServerMissingPhrases, firewallRuleMissingPhrases, staleDigestPhrases,
// mappingTakenPhrases, usbMappingMissingPhrases, pciMappingMissingPhrases). It checks only that the
// mapper is called somewhere in the function, keys on the client method name, and fails loudly for a
// handler that delegates the call to a helper. DeleteHARule is absent on purpose: deleting an absent
// key is a no-op in PVE's Perl, so there is no error to map (DeleteRule classifies the no-op itself).
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

	// 409 like SetNodeACMEConfig, and for the same die: the options write is
	// the same PUT /nodes/{node}/config against the same file and digest, so a
	// stale one dies with assert_if_modified's plain 500.
	"SetNodeOptions": "mapNodeConfigError",

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

	// 409, like SetNodeACMEConfig: a taken mapping id dies as a plain 500,
	// for either kind.
	"CreateUSBMapping": "mapMappingCreateError",
	"CreatePCIMapping": "mapMappingCreateError",
	// The firewall rule shape: 409 for a stale usb.cfg digest, 404 for a
	// mapping that is gone. Not DeleteUSBMapping: like DeleteHARule, it
	// succeeds for a missing id, so there is no die to map.
	"UpdateUSBMapping": "mapUSBMappingUpdateError",
	// The same for pci.cfg; DeletePCIMapping is left out for the same reason.
	"UpdatePCIMapping": "mapPCIMappingUpdateError",
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

// preflightFolders guards a different contract: "a pre-flight check that could not run must not read as
// one that passed". foldPreflight turns a failed check into a blocking conflict; without it the
// check contributes nothing and the strict policy waves the job through. It has its own map because
// the consequence is a gate that passes, not a 502, and these are internal/rolling analyzers. The
// swallow was reintroduced twice with every unit test green (foldPreflight is tested as a pure
// function, nothing proved it was called). Known false negatives: it checks foldPreflight is called
// in the function, so a literal nil where the analyzer's error belongs, a discarded hasErrors, or an
// unassigned result all pass; what it catches is the call being removed or reverted.
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
