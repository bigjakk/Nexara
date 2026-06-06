package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestGuard_MetricsHistoricalRequireClusterPerm enforces that every historical-
// metrics endpoint gates on a cluster-scoped permission. These handlers read
// cluster/vm/node IDs straight from the URL and query the time-series tables, so
// without an explicit requireClusterPerm check any authenticated user could read
// another cluster's metrics (cross-cluster IDOR). Static analysis only — no DB.
func TestGuard_MetricsHistoricalRequireClusterPerm(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "metrics.go", nil, 0)
	if err != nil {
		t.Fatalf("parse metrics.go: %v", err)
	}

	want := map[string]bool{
		"GetClusterHistorical": false,
		"GetVMHistorical":      false,
		"GetNodeHistorical":    false,
	}

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		if _, tracked := want[fn.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "requireClusterPerm" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}

	for name, found := range want {
		if !found {
			t.Errorf("%s must call requireClusterPerm — historical metrics need a cluster-scope authz gate (cross-cluster IDOR guard)", name)
		}
	}
}
