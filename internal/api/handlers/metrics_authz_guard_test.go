package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestGuard_MetricsHistoricalCheckClusterMembership enforces the half of the
// cross-cluster IDOR defence that lives in this package.
//
// The GATE moved out of it. All three routes are declared with a cluster-scoped
// Check in internal/api/registry_metrics.go, and
// TestMetricsRoutesDeclareTheSamePermissionTheyEnforced compares the ACTION AND
// THE RESOURCE of each one against what the handler enforced before the
// migration. That is strictly stronger than what this file used to do: the
// previous version searched for a requireClusterPerm call and could not see
// which permission it named, so all three could have collapsed onto
// view:cluster and still passed — and, being a reachability check, it could not
// tell a call that runs from one behind a condition.
//
// What a declaration cannot express is the second half, and it is the half the
// IDOR actually turns on: GetVMHistorical and GetNodeHistorical query the
// time-series tables by the GUEST's or NODE's row id alone, so a caller
// authorized on cluster A can name a row that lives in cluster B and the gate
// would never notice. Each has to re-read the row and compare its ClusterID
// against the one the gate authorized. This is that comparison.
func TestGuard_MetricsHistoricalCheckClusterMembership(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "metrics.go", nil, 0)
	if err != nil {
		t.Fatalf("parse metrics.go: %v", err)
	}

	// GetClusterHistorical is absent on purpose: its query keys on the
	// cluster_id the gate already resolved, so there is no second identifier to
	// cross-check. Listing it here would make this guard demand a comparison
	// that has nothing to compare.
	want := map[string]bool{
		"GetVMHistorical":   false,
		"GetNodeHistorical": false,
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
			bin, ok := n.(*ast.BinaryExpr)
			if !ok || bin.Op != token.NEQ {
				return true
			}
			if comparesClusterIDAgainst(bin, "clusterID") {
				want[fn.Name.Name] = true
			}
			return true
		})
	}

	for name, found := range want {
		if !found {
			t.Errorf("%s must compare the loaded row's ClusterID against the clusterID from the path — "+
				"the metric query keys on the row id alone, so without it a caller authorized on one "+
				"cluster can read another's series (cross-cluster IDOR)", name)
		}
	}
}

// comparesClusterIDAgainst reports whether bin is `x.ClusterID != <ident>` or
// `<ident> != x.ClusterID`, in either order, so the guard does not depend on
// which side the author happened to write.
func comparesClusterIDAgainst(bin *ast.BinaryExpr, ident string) bool {
	isClusterIDField := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "ClusterID"
	}
	isIdent := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == ident
	}
	return (isClusterIDField(bin.X) && isIdent(bin.Y)) || (isIdent(bin.X) && isClusterIDField(bin.Y))
}
