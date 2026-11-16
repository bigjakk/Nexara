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

// TestGuard_HAListingErrorsAreNotDiscarded: startNode and resumeDrain cannot be unit-tested,
// and reverting to `haRulesList, _ := listHARules(...)` left the suite green. It matches helpers
// by name and selectors (client.GetHARules, rolling.LoadHAConstraints, e.importHARules) by their
// last identifier, across internal/ (not the repo root, where stale worktrees live). It cannot
// see an error checked and then mishandled; drs's evaluate_test.go covers that for Evaluate.
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
		// Reached as selectors (rolling.LoadHAConstraints, e.importHARules…), so they are listed here,
		// not in guardedFuncs. importHARulesLegacy is pre-emptive: its one use is a return, which this
		// assignment matcher never sees, so its silence is not proof that the guard works.
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
