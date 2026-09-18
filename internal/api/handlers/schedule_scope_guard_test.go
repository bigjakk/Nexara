package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"testing"
)

// Static-analysis guard, in the shape of rolling_job_scope_guard_test.go: no
// database, no running server.
//
// A scheduled task is looked up by its Nexara uuid, and a uuid says nothing
// about which cluster the task belongs to. The permission gate authorizes the
// cluster named in the PATH — that is what the declared
// clusterCheck("manage","schedule") in internal/api/registry_schedules.go
// resolves — so a handler that reaches a task by uuid and then acts on it
// WITHOUT checking that the row belongs to the path's cluster serves
// cross-cluster.
//
// That is not hypothetical here. Until this change, UpdateScheduledTask and
// DeleteScheduledTask were `WHERE id = $1` with no cluster predicate and
// neither handler re-read the row, so manage:schedule on ANY cluster authorized
// a write to EVERY scheduled task in the install. The task uuids are not secret:
// schedule_created and schedule_updated audit rows carry them alongside their
// real cluster, and view:audit is a default Viewer grant. Rewriting another
// cluster's reboot task to `* * * * *` with enabled=true was a request away —
// and the audit row stamps the PATH's cluster, so the operator who owns the task
// never sees it in their own log.
//
// The comparison now lives in ONE place, taskInCluster, and this guard is what
// stops the next lookup reopening the hole. That shape is deliberate and is the
// lesson of the opt-in-guard class: a validator in the CALLER is one the next
// caller silently skips, so the check goes at the choke point and the guard
// proves nothing routes around it.
//
// BE CLEAR ABOUT WHAT THIS DOES NOT CATCH, in the same terms its sibling uses.
// It matches only the literal `x.queries.GetScheduledTask(…)` shape, so hoisting
// the receiver walks past it; and it proves the choke point is USED, not that
// its result is acted on. The SQL predicate in queries/scheduled_tasks.sql is
// the independent second layer for exactly that reason — it cannot be removed
// by an edit to this package.
//
// It sweeps the whole package rather than schedules.go alone, which is the
// difference between "the two routes are scoped" and "nothing anywhere can reach
// the row unscoped". A lookup from OUTSIDE this package is out of reach —
// internal/scheduler reads the same table — but it operates on tasks it claimed
// itself rather than on an id a request supplied, which is the distinction that
// matters here.
const (
	scheduledTaskLookup     = "GetScheduledTask"
	scheduledTaskChokePoint = "ScheduleHandler.taskInCluster"
)

// scheduledTaskScopedHandlers are the routes that name a task in their path.
// Every one of them must reach the row through the choke point.
//
// Listing them explicitly, rather than deriving them from "whatever calls the
// lookup", is what makes the test fail when a handler stops calling it at all —
// which is the regression, not merely calling it wrongly.
var scheduledTaskScopedHandlers = []string{
	"ScheduleHandler.Update",
	"ScheduleHandler.Delete",
}

func TestGuard_ScheduledTaskLookupsAreClusterScoped(t *testing.T) {
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
			if name == scheduledTaskChokePoint {
				chokePointBody = fn.Body
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case scheduledTaskLookup:
					callsLookup[name] = true
				case "taskInCluster":
					callsChokePoint[name] = true
				}
				return true
			})
		}
	}

	var findings []string

	// 1. The choke point exists and makes the comparison.
	if chokePointBody == nil {
		t.Fatalf("%s is not declared; the guard has nothing to anchor on", scheduledTaskChokePoint)
	}
	if !callsLookup[scheduledTaskChokePoint] {
		findings = append(findings, scheduledTaskChokePoint+" no longer loads the task it is supposed to scope")
	}
	if !comparesClusterID(chokePointBody) {
		findings = append(findings, scheduledTaskChokePoint+" makes no ClusterID comparison — it is the one "+
			"place that check lives, so without it both per-task routes serve cross-cluster")
	}

	// 2. Every per-task route goes through it.
	for _, name := range scheduledTaskScopedHandlers {
		if declaredIn[name] == "" {
			findings = append(findings, name+" is listed as a per-task route but is not declared in this package")
			continue
		}
		if !callsChokePoint[name] {
			findings = append(findings, name+" names a task in its path but does not go through taskInCluster — "+
				"a caller holding manage:schedule on one cluster can act on another cluster's task")
		}
	}

	// 3. And nothing ELSE reaches the lookup directly, which is what keeps the
	//    choke point a choke point rather than a convention.
	for name := range callsLookup {
		if name == scheduledTaskChokePoint {
			continue
		}
		findings = append(findings, name+" ("+declaredIn[name]+") calls "+scheduledTaskLookup+
			" directly; route it through "+scheduledTaskChokePoint+
			" so the cluster comparison cannot be forgotten")
	}

	sort.Strings(findings)
	for _, f := range findings {
		t.Error(f)
	}
}

// TestGuard_ScheduledTaskWritesCarryTheClusterPredicate is the second layer,
// checked from the Go side because that is where a caller could drop it.
//
// queries/scheduled_tasks.sql scopes both writes on cluster_id, and sqlc turns
// that into a ClusterID field on each params struct. A caller that stops filling
// it in passes uuid.Nil, which matches no row — so the failure is a silent
// no-op rather than a cross-cluster write, and the rows==0 check turns it into a
// 404. This asserts the field is set at all, which is what makes that true.
func TestGuard_ScheduledTaskWritesCarryTheClusterPredicate(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "schedules.go", nil, 0)
	if err != nil {
		t.Fatalf("parse schedules.go: %v", err)
	}

	want := map[string]bool{
		"UpdateScheduledTaskParams": false,
		"DeleteScheduledTaskParams": false,
	}

	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, tracked := want[sel.Sel.Name]; !tracked {
			return true
		}
		for _, elt := range lit.Elts {
			kv, isKV := elt.(*ast.KeyValueExpr)
			if !isKV {
				continue
			}
			if key, isIdent := kv.Key.(*ast.Ident); isIdent && key.Name == "ClusterID" {
				want[sel.Sel.Name] = true
			}
		}
		return true
	})

	for name, set := range want {
		if !set {
			t.Errorf("schedules.go builds a %s without setting ClusterID — the statement is scoped on "+
				"cluster_id, so an unset one is uuid.Nil and the write silently matches nothing", name)
		}
	}
}
