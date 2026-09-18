package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"testing"
)

// Static-analysis guard, in the spirit of guest_cluster_scope_guard_test.go: no
// database, no running server.
//
// A rolling update job is looked up by its Nexara uuid, and a uuid says nothing
// about which cluster the job belongs to. The permission gate authorizes the
// cluster named in the PATH — that is what the declared Check in
// internal/api/registry_rolling_update.go resolves — so a handler that loads a
// job by uuid and then acts on it WITHOUT checking that the row belongs to the
// path's cluster serves cross-cluster: a caller holding manage:rolling_update on
// cluster A puts A in the path and B's job id in it.
//
// SEVEN of the eight did exactly that. CancelJob alone carried the comparison,
// with a comment saying why ("the permission check above covered the URL
// cluster; make sure the job actually belongs to it"); GetJob, StartJob,
// PauseJob, ResumeJob, ListNodes, ConfirmUpgrade and SkipNode did not, so
// another cluster's job could be read, started, paused, resumed, confirmed or
// skipped. PauseJob and ResumeJob did not even LOAD the job — they issued the
// guarded UPDATE straight from the path id.
//
// The comparison now lives in ONE place, jobInCluster, and this guard is what
// stops the next direct lookup reopening the hole. That shape is deliberate and
// is the lesson of the opt-in-guard class: a validator in the CALLER is one the
// next caller silently skips, so the check goes at the choke point and the guard
// proves nothing routes around it.
//
// BE CLEAR ABOUT WHAT THIS DOES NOT CATCH. It matches only the literal
// `x.queries.GetRollingUpdateJob(…)` shape, so hoisting the receiver walks past
// it; and it proves the choke point is USED, not that its result is acted on.
// It catches the shape that actually occurred — a direct lookup with no
// comparison at all — and should not be trusted further than that.
//
// It DOES sweep the whole package rather than rolling_update.go alone, which is
// the difference between "the eight routes are scoped" and "nothing anywhere can
// reach the row unscoped". A guard for a choke point has to watch every door, or
// the next caller is simply written in a different file. A lookup from OUTSIDE
// this package is out of reach — internal/rolling's orchestrator calls the same
// query — but it operates on jobs it already owns rather than on an id a request
// supplied, which is the distinction that matters here.
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

	fset := token.NewFileSet()
	callsChokePoint := map[string]bool{}
	callsLookup := map[string]bool{}
	declaredIn := map[string]string{}
	var chokePointBody *ast.BlockStmt

	for _, path := range paths {
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
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
