package rolling

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
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
// fixed for, left the entire suite green. The same holds in internal/drs, where
// there is no Evaluate test at all: reverting its importHARules call to discard
// the error would restore unconstrained balancing with nothing going red.
//
// It watches two spellings, because the regression has two. Package-local
// helpers are matched by name; anything reached through a selector — the raw
// client methods, `e.importHARules(...)`, and `rolling.LoadHAConstraints(...)`
// from another package — is matched on the selector's final identifier, since
// reverting PAST the helpers to `haRules, _ := client.GetHARules(ctx)` is the
// literal pre-fix line and an earlier version of this guard sailed straight
// over it.
//
// It walks all of internal/, not just this package. Two of the guarded names
// have no call site in internal/rolling at all — LoadHAConstraints exists FOR
// internal/api/handlers, and the importHARules family lives in internal/drs —
// so a guard scoped to its own directory would list them, never fire, and read
// as coverage that isn't there. That was the first version of this change.
//
// It deliberately does not walk the repo root: stale agent worktrees live under
// .claude/worktrees/ with a full copy of the tree inside, and a root walk finds
// their pre-fix sources and fails on code nobody is shipping.
//
// What it does NOT catch: an error that is checked and then mishandled — logged
// at warn and proceeded past, say — or a fourth listing added through neither
// spelling. The first of those is the likelier future regression, since it
// restores the original unsafe behaviour while looking careful.
func TestGuard_HAListingErrorsAreNotDiscarded(t *testing.T) {
	guardedFuncs := map[string]bool{
		"listHARules":       true,
		"loadHAConstraints": true,
		// internal/drs, called as a bare identifier from Evaluate. Its empty
		// skip set reads as "no node is in HA maintenance", so a discarded
		// error clears the filter that keeps DRS off a node HA is evacuating.
		"unhealthyHANodes": true,
	}
	guardedMethods := map[string]bool{
		"GetHARules":     true,
		"GetHAResources": true,
		"GetHAGroups":    true,
		"GetHAStatus":    true,
		// Reached as rolling.LoadHAConstraints / e.importHARules…, so these are
		// selectors even though they are plain functions or methods at their
		// definition. Listing them under guardedFuncs matches only a bare
		// identifier and never fires.
		//
		// importHARulesLegacy is pre-emptive rather than active: its only use
		// today is `return e.importHARulesLegacy(...)`, a ReturnStmt, which
		// propagates by construction and which this AssignStmt matcher never
		// sees. It is listed so that a future caller which assigns the result
		// is covered from the start. That is a different thing from an entry
		// that CANNOT fire — see the note above about scoping — so do not read
		// its silence as proof the guard works.
		"LoadHAConstraints":   true,
		"importHARules":       true,
		"importHARulesPVE9":   true,
		"importHARulesLegacy": true,
	}

	fset := token.NewFileSet()
	// internal/ — this file lives in internal/rolling.
	root := ".."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Generated sqlc output is large, never calls these, and parsing it
			// on every run is pure cost.
			if d.Name() == "generated" {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
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
			var matched string
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if guardedFuncs[fun.Name] {
					matched = fun.Name
				}
			case *ast.SelectorExpr:
				if guardedMethods[fun.Sel.Name] {
					matched = fun.Sel.Name
				}
			}
			if matched == "" {
				return true
			}
			// The error is the last result of every guarded name.
			last := assign.Lhs[len(assign.Lhs)-1]
			if blank, ok := last.(*ast.Ident); ok && blank.Name == "_" {
				t.Errorf("%s: %s's error is assigned to _ — an unreadable listing would read as an empty one",
					fset.Position(assign.Pos()), matched)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}
