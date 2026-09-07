package handlers

import (
	"go/ast"
	"sort"
	"strings"
	"testing"
)

// Static-analysis guard, same shape as missing_object_guard_test.go: no
// database, no cluster, no request.
//
// It exists because ha_test.go tests findHARule, classifyHARuleDelete and
// haRuleDeleteDetails directly, and that proves the decision, not the wiring.
// Changing a handler's AuditLog call back to a literal "deleted" leaves every
// one of those tests green — the helpers stay referenced from the test file, so
// `unused` does not fire on them either — while quietly restoring the row that
// claims a deletion PVE never performed.
//
// It covers EVERY handler that reaches PVE's DELETE /cluster/ha/rules, not one
// named handler. That generality is the point: the DRS page shipped its own
// delete for months with an unconditional "ha_rule_deleted" row, and a guard
// pinned to HAHandler.DeleteRule said nothing about it. A third door onto the
// same endpoint is caught the day it is written.
//
// WHAT IT DOES NOT CATCH: that the action is *correct*, only that it is
// computed rather than hardcoded; and it keys on the client method name, so a
// handler reaching the endpoint some other way is invisible to it.
var haRuleDeleteHandlers = map[string]bool{
	"HAHandler.DeleteRule":    false, // the HA tab
	"DRSHandler.DeleteHARule": false, // the DRS page
}

func TestGuard_HARuleDeletesAuditAComputedAction(t *testing.T) {
	_, files := parseGoFiles(t, ".")

	seen := map[string]bool{}
	var problems []string
	detailBuilderFlagsUnknown := false

	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}

			// The shared detail builder is where the "we could not check" flag
			// now lives, having moved out of the handlers when they started
			// sharing it.
			if fn.Name.Name == "haRuleDeleteDetails" {
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && strings.Contains(lit.Value, "prior_state_unknown") {
						detailBuilderFlagsUnknown = true
					}
					return true
				})
			}

			var (
				deletesRule   bool
				classifies    bool
				buildsDetails bool
				auditActions  []ast.Expr
				auditDetails  []ast.Expr
			)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.SelectorExpr:
					// Counted wherever it appears, not only as a call's Fun:
					// `del := client.DeleteHARule; del(...)` reaches the same
					// endpoint.
					if node.Sel.Name == "DeleteHARule" {
						deletesRule = true
					}
				case *ast.CallExpr:
					ident, ok := node.Fun.(*ast.Ident)
					if !ok {
						return true
					}
					if ident.Name == "classifyHARuleDelete" {
						classifies = true
					}
					if ident.Name == "haRuleDeleteDetails" {
						buildsDetails = true
					}
					// AuditLog(c, queries, eventPub, clusterID, resourceType,
					// resourceID, action, details) — the action is argument 7.
					if ident.Name == "AuditLog" && len(node.Args) == 8 {
						auditActions = append(auditActions, node.Args[6])
						auditDetails = append(auditDetails, node.Args[7])
					}
				}
				return true
			})
			if !deletesRule {
				continue
			}

			name := qualifiedFuncName(fn)
			seen[name] = true
			if !classifies {
				problems = append(problems, name+" deletes an HA rule but never calls classifyHARuleDelete — "+
					"a PVE 200 does not mean a rule was removed, so the audit action has to be derived "+
					"from the pre-delete snapshot")
			}
			if len(auditActions) == 0 {
				problems = append(problems, name+" deletes an HA rule but has no 8-argument AuditLog call; "+
					"update this guard alongside the signature")
			}
			for _, arg := range auditActions {
				if lit, ok := arg.(*ast.BasicLit); ok {
					problems = append(problems, name+" audits the hardcoded action "+lit.Value+
						"; it must pass the value from classifyHARuleDelete, or an already-absent rule "+
						"is logged as a deletion again")
				}
			}
			// The detail argument needs its own assertion. Checking only the
			// action left `AuditLog(..., action, nil)` passing, which silently
			// drops prior_state_unknown — the single field separating "we could
			// not check" from "the rule held nothing" — along with every field
			// describing the rule that was removed.
			//
			// Two halves rather than one, because the handlers assign the
			// builder's result to a local before passing it, and a check that
			// demanded the call inline at the AuditLog site would be dictating
			// style rather than catching the bug: the body must call the shared
			// builder, and the argument must not be nil or a literal.
			if !buildsDetails {
				problems = append(problems, name+" does not build its audit detail with haRuleDeleteDetails; "+
					"a hand-rolled map lets the two delete endpoints describe the same event differently")
			}
			for _, arg := range auditDetails {
				switch a := arg.(type) {
				case *ast.Ident:
					if a.Name == "nil" {
						problems = append(problems, name+" audits a nil detail — that drops prior_state_unknown, "+
							"and a row written after a failed snapshot read becomes indistinguishable from a "+
							"rule that carried no fields")
					}
				case *ast.BasicLit:
					problems = append(problems, name+" audits the hardcoded detail "+a.Value+
						" instead of the value from haRuleDeleteDetails")
				}
			}
		}
	}

	// A handler that stopped matching makes its entry — and the coverage it
	// stands for — dead weight that silently passes. Fail per entry, not on a
	// total, so one going quiet cannot hide behind the others.
	for name := range haRuleDeleteHandlers {
		if !seen[name] {
			problems = append(problems, "no function named "+name+" deletes an HA rule any more; "+
				"it was renamed or removed — update haRuleDeleteHandlers so this guard keeps covering it")
		}
	}
	if !detailBuilderFlagsUnknown {
		problems = append(problems, "haRuleDeleteDetails no longer sets prior_state_unknown — without it a "+
			"row written after a failed snapshot read is indistinguishable from a rule that carried no fields")
	}

	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}
