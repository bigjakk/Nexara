package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// validateScheduleFields tells its caller whether a notification channel exists and is an
// email channel, which view:notification_channel guards; a Deferred route has no
// middleware in front, so the order in the handler body IS the gate. CreateSchedule once
// validated before its requireClusterPerm; registryEnforcementGaps proves only that a
// Deferred handler reaches a check, not that it reaches it first.
const orderedGate = "validateScheduleFields"

// permissionGates are the calls that establish the caller may proceed.
var permissionGates = map[string]bool{
	"requireClusterPerm": true,
	"requirePerm":        true,
	"requirePBSPerm":     true,
}

// TestGuard_ScheduleValidationIsAuthorizedFirst fails any function that calls
// validateScheduleFields with no permission gate earlier in its body. A source-order check,
// because a request-level one needs Postgres and would skip without it. Source order is not
// reachability: a gate inside a condition that never holds would pass. It catches the shape
// that shipped, a gate written below the work.
func TestGuard_ScheduleValidationIsAuthorizedFirst(t *testing.T) {
	t.Parallel()

	// Glob + ParseFile rather than the deprecated parser.ParseDir/ast.Package,
	// matching tracktask_guard_test.go in this package.
	fset := token.NewFileSet()
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing the handlers package: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no Go files found; the guard would pass vacuously")
	}

	checked := 0
	for _, path := range matches {
		// Production files only. A behavioural test of
		// validateScheduleFields has no permission gate and no business
		// having one, and tripping on it would push someone to weaken
		// this guard rather than write the test.
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		{
			ast.Inspect(file, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					return true
				}

				gatePos, validatePos := token.NoPos, token.NoPos
				ast.Inspect(fn.Body, func(inner ast.Node) bool {
					call, ok := inner.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch f := call.Fun.(type) {
					case *ast.Ident:
						if permissionGates[f.Name] && !gatePos.IsValid() {
							gatePos = call.Pos()
						}
					}
					return true
				})

				// Any REFERENCE to the gated function, not only a direct
				// call: `f := h.validateScheduleFields` followed by `f(...)`
				// is a call the CallExpr walk above cannot see, and it
				// would otherwise slip the guard entirely.
				ast.Inspect(fn.Body, func(inner ast.Node) bool {
					sel, ok := inner.(*ast.SelectorExpr)
					if ok && sel.Sel.Name == orderedGate && !validatePos.IsValid() {
						validatePos = sel.Pos()
					}
					return true
				})

				if !validatePos.IsValid() {
					return true
				}
				checked++
				if !gatePos.IsValid() {
					t.Errorf("%s calls %s but never authorizes — it would answer "+
						"whether a notification channel exists to any caller",
						fn.Name.Name, orderedGate)
					return true
				}
				if gatePos > validatePos {
					t.Errorf("%s calls %s at %s BEFORE its permission gate at %s — "+
						"authorize first; the validation is a database read whose "+
						"error text distinguishes a missing channel from a wrong-typed one",
						fn.Name.Name, orderedGate,
						fset.Position(validatePos), fset.Position(gatePos))
				}
				return true
			})
		}
	}

	if checked == 0 {
		t.Fatalf("no caller of %s was examined; the guard would pass vacuously", orderedGate)
	}
}
