package handlers

import (
	"go/ast"
	"path/filepath"
	"sort"
	"testing"
)

// Static-analysis guard, in the spirit of guest_cluster_scope_guard_test.go. A rolling update job is
// looked up by uuid, which says nothing about its cluster, while the gate authorizes the PATH's
// cluster (the declared Check in registry_rolling_update.go), so a handler that loads a job by uuid
// and acts on it WITHOUT checking the row belongs to the path's cluster serves cross-cluster. Seven
// of the eight did exactly that (only CancelJob compared; PauseJob and ResumeJob did not even LOAD
// the job, they issued the guarded UPDATE from the path id). The comparison now lives in ONE place,
// jobInCluster, and this guard proves nothing routes around it. It matches only the literal
// `x.queries.GetRollingUpdateJob(...)` shape and proves the choke point is USED, not that its result
// is acted on. It sweeps the whole package, since a choke point must be watched at every door;
// internal/rolling's orchestrator calls the same query on jobs it already owns.
const (
	rollingJobLookup     = "GetRollingUpdateJob"
	rollingJobChokePoint = "RollingUpdateHandler.jobInCluster"
)

// rollingJobScopedHandlers are the routes that name a job in their path. Every
// one of them must reach the row through the choke point.
//
// Listing them explicitly, rather than deriving them from "whatever calls the
// lookup", is what makes the test fail when a handler stops calling it at all —
// which is the regression, not merely calling it wrongly.
var rollingJobScopedHandlers = []string{
	"RollingUpdateHandler.GetJob",
	"RollingUpdateHandler.StartJob",
	"RollingUpdateHandler.CancelJob",
	"RollingUpdateHandler.PauseJob",
	"RollingUpdateHandler.ResumeJob",
	"RollingUpdateHandler.ListNodes",
	"RollingUpdateHandler.ConfirmUpgrade",
	"RollingUpdateHandler.SkipNode",
}

func TestGuard_RollingUpdateJobLookupsAreClusterScoped(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	callsChokePoint := map[string]bool{}
	callsLookup := map[string]bool{}
	declaredIn := map[string]string{}
	var chokePointBody *ast.BlockStmt

	for _, path := range paths {
		file, parseErr := guardParsed(path)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := guardFuncKey(fn)
			declaredIn[name] = path
			if name == rollingJobChokePoint {
				chokePointBody = fn.Body
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case rollingJobLookup:
					callsLookup[name] = true
				case "jobInCluster":
					callsChokePoint[name] = true
				}
				return true
			})
		}
	}

	var findings []string

	// 1. The choke point exists and makes the comparison.
	if chokePointBody == nil {
		t.Fatalf("%s is not declared; the guard has nothing to anchor on", rollingJobChokePoint)
	}
	if !callsLookup[rollingJobChokePoint] {
		findings = append(findings, rollingJobChokePoint+" no longer loads the job it is supposed to scope")
	}
	if !comparesClusterID(chokePointBody) {
		findings = append(findings, rollingJobChokePoint+" makes no ClusterID comparison — it is the one "+
			"place that check lives, so without it every route in this domain serves cross-cluster")
	}

	// 2. Every per-job route goes through it.
	for _, name := range rollingJobScopedHandlers {
		if declaredIn[name] == "" {
			findings = append(findings, name+" is listed as a per-job route but is not declared in this package")
			continue
		}
		if !callsChokePoint[name] {
			findings = append(findings, name+" names a job in its path but does not go through jobInCluster — "+
				"a caller holding the permission on one cluster can act on another cluster's job")
		}
	}

	// 3. And nothing ELSE reaches the lookup directly, which is what keeps the
	//    choke point a choke point rather than a convention.
	for name := range callsLookup {
		if name == rollingJobChokePoint {
			continue
		}
		findings = append(findings, name+" ("+declaredIn[name]+") calls "+rollingJobLookup+
			" directly; route it through "+rollingJobChokePoint+
			" so the cluster comparison cannot be forgotten")
	}

	sort.Strings(findings)
	for _, f := range findings {
		t.Error(f)
	}
}
