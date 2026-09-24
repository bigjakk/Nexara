package handlers

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strconv"
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
//
// Each handler maps to the path parameter that carries its rule name, which
// TestGuard_HARuleDeletesDecodeTheRuleName traces into the two calls that use
// it. The registry declares those parameters (registry_ha.go, registry_drs.go);
// a handler test cannot import package api to read them, so a renamed
// parameter fails that guard until the name here follows it.
var haRuleDeleteHandlers = map[string]string{
	"HAHandler.DeleteRule":    "rule",      // the HA tab
	"DRSHandler.DeleteHARule": "rule_name", // the DRS page
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

// TestGuard_HARuleDeletesDecodeTheRuleName requires every handler in
// haRuleDeleteHandlers to hand findHARule and the client's DeleteHARule the
// rule name as decodeParamValue(p.String("<its parameter>")) returns it —
// passed inline, or through a local that is assigned from nothing else.
//
// The SPA builds these paths with apiPath (frontend/src/lib/api-path.ts),
// which percent-encodes every segment, and Fiber hands the handler the
// segment undecoded. The declared rule admits no character the SPA would
// encode today, so a handler reading the value raw behaves identically — and
// that is exactly why nothing else would catch one that stopped decoding: the
// day the rule widens, the name would reach Proxmox still encoded, with every
// behavioural test still green.
//
// It traces the VALUE rather than looking for a decodeParamValue call
// anywhere in the body, because a body that decodes only for a log line —
// ruleName := p.String("rule_name") passed to both calls, decoded inside
// slog.Warn — has a decodeParamValue call and sends the raw name.
//
// WHAT IT DOES NOT CATCH: it matches a local by name, not by object, so a
// write through a pointer (&ruleName) is invisible to it. Every other binding
// of that name in the body — a second assignment, a closure's included, a
// range variable, a function-literal parameter, a var with no value — is
// refused rather than reasoned about.
func TestGuard_HARuleDeletesDecodeTheRuleName(t *testing.T) {
	_, files := parseGoFiles(t, ".")

	found := map[string]bool{}
	var problems []string
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := qualifiedFuncName(fn)
			key, want := haRuleDeleteHandlers[name]
			if !want {
				continue
			}
			found[name] = true
			for _, problem := range untracedRuleNames(fn, key) {
				problems = append(problems, name+" "+problem)
			}
		}
	}
	for name := range haRuleDeleteHandlers {
		if !found[name] {
			problems = append(problems, "no function named "+name+" was found; update haRuleDeleteHandlers")
		}
	}

	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// untracedRuleNames checks one handler for TestGuard_HARuleDeletesDecodeTheRuleName
// and returns what is wrong with it, or nothing.
func untracedRuleNames(fn *ast.FuncDecl, key string) []string {
	params := paramsVarName(fn)
	if params == "" {
		return []string{"has no *apischema.Params parameter to read its rule name from; " +
			"update this guard alongside the handler signature"}
	}
	want := fmt.Sprintf("decodeParamValue(%s.String(%q))", params, key)

	var problems []string
	finds, deletes := 0, 0
	check := func(callee string, arg ast.Expr) {
		if why := untracedName(fn.Body, arg, params, key); why != "" {
			problems = append(problems, fmt.Sprintf("hands %s the rule name %s, %s; it must be %s, "+
				"or the SPA's percent-encoded segment reaches Proxmox undecoded once the declared rule "+
				"admits a character the SPA encodes", callee, types.ExprString(arg), why, want))
		}
	}
	calledDeletes := map[*ast.SelectorExpr]bool{}
	calledFinds := map[*ast.Ident]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			// findHARule(ctx, client, name)
			if fun.Name == "findHARule" && len(call.Args) == 3 {
				finds++
				calledFinds[fun] = true
				check("findHARule", call.Args[2])
			}
		case *ast.SelectorExpr:
			// client.DeleteHARule(ctx, name)
			if fun.Sel.Name == "DeleteHARule" && len(call.Args) == 2 {
				deletes++
				calledDeletes[fun] = true
				check("DeleteHARule", call.Args[1])
			}
		}
		return true
	})
	// `del := client.DeleteHARule; del(ctx, raw)` reaches the endpoint with an
	// argument this guard never sees, and `find := findHARule; find(ctx,
	// client, raw)` has the audit snapshot look up a name the delete never
	// sent — both beside a traced direct call that keeps the count above
	// satisfied. So a reference to either that is not the callee of a direct
	// call is refused rather than skipped.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch ref := n.(type) {
		case *ast.SelectorExpr:
			if ref.Sel.Name == "DeleteHARule" && !calledDeletes[ref] {
				problems = append(problems, "uses "+types.ExprString(ref)+" other than as a direct call, "+
					"so the rule name it is given cannot be traced")
			}
		case *ast.Ident:
			if ref.Name == "findHARule" && !calledFinds[ref] {
				problems = append(problems, "uses findHARule other than as a direct call, "+
					"so the rule name it is given cannot be traced")
			}
		}
		return true
	})
	if finds == 0 {
		problems = append(problems, "never calls findHARule(ctx, client, name); the audit snapshot must "+
			"look up the same decoded name the delete sends — update this guard if that moved")
	}
	if deletes == 0 {
		problems = append(problems, "never calls DeleteHARule(ctx, name) directly; update this guard "+
			"if the delete moved")
	}
	return problems
}

// paramsVarName returns the name of fn's *apischema.Params parameter, or an
// empty string when it has none.
func paramsVarName(fn *ast.FuncDecl) string {
	for _, field := range fn.Type.Params.List {
		star, ok := field.Type.(*ast.StarExpr)
		if !ok || len(field.Names) != 1 {
			continue
		}
		if types.ExprString(star.X) == "apischema.Params" {
			return field.Names[0].Name
		}
	}
	return ""
}

// isDecodedRead reports whether e is exactly decodeParamValue(params.String("key")).
func isDecodedRead(e ast.Expr, params, key string) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	if fun, ok := call.Fun.(*ast.Ident); !ok || fun.Name != "decodeParamValue" {
		return false
	}
	read, ok := ast.Unparen(call.Args[0]).(*ast.CallExpr)
	if !ok || len(read.Args) != 1 {
		return false
	}
	if types.ExprString(read.Fun) != params+".String" {
		return false
	}
	lit, ok := read.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	got, err := strconv.Unquote(lit.Value)
	return err == nil && got == key
}

// untracedName returns why arg cannot be traced to the decoded read, or an
// empty string when it can: arg is the decoded read itself, or a local whose
// every binding in body assigns exactly that read.
func untracedName(body *ast.BlockStmt, arg ast.Expr, params, key string) string {
	if isDecodedRead(arg, params, key) {
		return ""
	}
	id, ok := ast.Unparen(arg).(*ast.Ident)
	if !ok {
		return "an expression that is not the decoded read"
	}
	bindings := 0
	var bad []string
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				if l, ok := lhs.(*ast.Ident); !ok || l.Name != id.Name {
					continue
				}
				bindings++
				if (s.Tok != token.DEFINE && s.Tok != token.ASSIGN) || len(s.Lhs) != len(s.Rhs) ||
					!isDecodedRead(s.Rhs[i], params, key) {
					bad = append(bad, "an assignment that is not the decoded read")
				}
			}
		case *ast.ValueSpec:
			for i, n := range s.Names {
				if n.Name != id.Name {
					continue
				}
				bindings++
				if len(s.Values) != len(s.Names) || !isDecodedRead(s.Values[i], params, key) {
					bad = append(bad, "a var declaration that is not the decoded read")
				}
			}
		case *ast.RangeStmt:
			for _, e := range []ast.Expr{s.Key, s.Value} {
				if l, ok := e.(*ast.Ident); ok && l.Name == id.Name {
					bindings++
					bad = append(bad, "a range variable")
				}
			}
		case *ast.FuncLit:
			for _, field := range s.Type.Params.List {
				for _, n := range field.Names {
					if n.Name == id.Name {
						bindings++
						bad = append(bad, "a function-literal parameter")
					}
				}
			}
		}
		return true
	})
	switch {
	case bindings == 0:
		return "a name the handler body never assigns"
	case len(bad) > 0:
		return "a local bound by " + strings.Join(bad, " and by ")
	}
	return ""
}
