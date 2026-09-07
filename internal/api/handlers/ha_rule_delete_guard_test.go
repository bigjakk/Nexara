package handlers

import (
	"go/ast"
	"strings"
	"testing"
)

// Static-analysis guard, same shape as missing_object_guard_test.go: no
// database, no cluster, no request.
//
// It exists because ha_test.go tests findHARule and classifyHARuleDelete
// directly, and that proves the decision, not the wiring. Changing DeleteRule's
// AuditLog call back to the literal "deleted" leaves every one of those tests
// green — classifyHARuleDelete stays referenced from the test file, so `unused`
// does not fire on it either — while quietly restoring the row that claims a
// deletion PVE never performed. This checks the two lines that carry the fix
// into the audit log.
//
// WHAT IT DOES NOT CATCH: that the action is *correct*, only that it is
// computed rather than hardcoded; and it keys on the function name, so moving
// the audit call into a helper makes it fail loudly even when it is right.
func TestGuard_DeleteRuleAuditsAComputedAction(t *testing.T) {
	_, files := parseGoFiles(t, ".")

	var body *ast.BlockStmt
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Body != nil && qualifiedFuncName(fn) == "HAHandler.DeleteRule" {
				body = fn.Body
			}
		}
	}
	if body == nil {
		t.Fatal("HAHandler.DeleteRule not found — rename it here too, or the guard passes vacuously")
	}

	var (
		classified   bool
		flagged      bool
		auditActions []ast.Expr
	)
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == "classifyHARuleDelete" {
				classified = true
			}
			// AuditLog(c, queries, eventPub, clusterID, resourceType,
			// resourceID, action, details) — the action is argument 7.
			if fn.Name == "AuditLog" && len(call.Args) == 8 {
				auditActions = append(auditActions, call.Args[6])
			}
		}
		return true
	})
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if ok && strings.Contains(lit.Value, "prior_state_unknown") {
			flagged = true
		}
		return true
	})

	if !classified {
		t.Error("DeleteRule no longer calls classifyHARuleDelete — a PVE 200 does not mean a rule was deleted, " +
			"so the audit action has to be derived from the pre-delete snapshot")
	}
	if !flagged {
		t.Error("DeleteRule no longer sets prior_state_unknown — without it a row written after a failed " +
			"snapshot read is indistinguishable from a rule that carried no fields")
	}
	if len(auditActions) == 0 {
		t.Fatal("DeleteRule has no 8-argument AuditLog call; update this guard alongside the signature")
	}
	for _, arg := range auditActions {
		if lit, ok := arg.(*ast.BasicLit); ok {
			t.Errorf("DeleteRule audits the hardcoded action %s; it must pass the value from "+
				"classifyHARuleDelete, or an already-absent rule is logged as a deletion again", lit.Value)
		}
	}
}
