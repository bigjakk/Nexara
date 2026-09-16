package rolling

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Static-analysis guard, in the spirit of the handlers package's
// missing_object_guard_test.go: no database, no cluster, so it fails the moment
// someone discards an error these functions exist to report.
//
// It exists because the decisions are tested and the wiring is not. startNode
// and resumeDrain cannot be unit-tested — stubOrchestrator passes nil queries,
// so failNode nil-derefs — and reverting the drain site to
// `haRulesList, _ := listHARules(...)`, which is the exact bug this package was
// fixed for, left the entire suite green.
//
// It watches two spellings, because the regression has two. The helpers are
// matched by name; the raw client methods they wrap are matched as selectors,
// since reverting PAST the helpers to `haRules, _ := client.GetHARules(ctx)` is
// the literal pre-fix line and an earlier version of this guard sailed straight
// over it.
//
// What it does NOT catch: an error that is checked and then mishandled — logged
// at warn and proceeded past, say — or a fourth listing added through neither
// spelling. The first of those is the likelier future regression, since it
// restores the original unsafe behaviour while looking careful.
func TestGuard_HAListingErrorsAreNotDiscarded(t *testing.T) {
	guardedFuncs := map[string]bool{"listHARules": true, "loadHAConstraints": true}
	guardedMethods := map[string]bool{
		"GetHARules":     true,
		"GetHAResources": true,
		"GetHAGroups":    true,
	}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || len(assign.Rhs) != 1 {
				return true
			}
			call, ok := assign.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			var name string
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if guardedFuncs[fun.Name] {
					name = fun.Name
				}
			case *ast.SelectorExpr:
				if guardedMethods[fun.Sel.Name] {
					name = fun.Sel.Name
				}
			}
			if name == "" {
				return true
			}
			// The error is the last result of both.
			last := assign.Lhs[len(assign.Lhs)-1]
			if blank, ok := last.(*ast.Ident); ok && blank.Name == "_" {
				t.Errorf("%s: %s's error is assigned to _ — an unreadable listing would read as an empty one",
					fset.Position(assign.Pos()), name)
			}
			return true
		})
	}
}
