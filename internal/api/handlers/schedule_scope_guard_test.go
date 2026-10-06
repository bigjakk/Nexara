package handlers

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"testing"
)

// Static-analysis guard, in the shape of rolling_job_scope_guard_test.go. A scheduled task is looked
// up by uuid, which says nothing about its cluster, while the gate authorizes the PATH's cluster
// (clusterCheck("manage","schedule") in registry_schedules.go). Until this change Update/Delete were
// `WHERE id = $1` with no cluster predicate, so manage:schedule on ANY cluster authorized a write to
// EVERY task in the install; the uuids are not secret (schedule_* audit rows carry them, and view:audit
// is a default Viewer grant) and the audit row stamps the PATH's cluster. The comparison now lives in
// ONE place, taskInCluster, and this guard proves nothing routes around it. It matches only the
// literal `x.queries.GetScheduledTask(...)` shape and proves the choke point is USED, not that its
// result is acted on; queries/scheduled_tasks.sql is the independent second layer. It sweeps the whole
// package (internal/scheduler reads the same table, but on tasks it claimed itself).
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

// TestGuard_ScheduledTaskWritesCarryTheClusterPredicate is the second layer, checked from the Go side
// where a caller could drop it: queries/scheduled_tasks.sql scopes both writes on cluster_id, and a
// caller that stops filling the params field passes uuid.Nil, which matches no row (a silent no-op,
// turned into a 404 by the rows==0 check). It asserts the VALUE, not merely that the field is set:
// `ClusterID: task.ClusterID` compiles and is a no-op today only because taskInCluster proved the two
// equal, and would be authorization by self-assertion the moment that comparison is relaxed. The
// path's clusterID is the only value carrying the caller's authority, so that is the one pinned.
func TestGuard_ScheduledTaskWritesCarryTheClusterPredicate(t *testing.T) {
	// The identifier schedules.go binds the path's :cluster_id to.
	const wantClusterIDIdent = "clusterID"

	file, err := guardParsed("schedules.go")
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
			key, isIdent := kv.Key.(*ast.Ident)
			if !isIdent || key.Name != "ClusterID" {
				continue
			}
			// Must be the bare path-derived identifier. A selector such as
			// task.ClusterID is exactly the regression described above.
			val, isIdent := kv.Value.(*ast.Ident)
			if !isIdent || val.Name != wantClusterIDIdent {
				t.Errorf("schedules.go builds a %s with ClusterID from %s, want the bare %q the "+
					"path supplied — scoping a write on a value read out of the row being written "+
					"authorizes it against itself",
					sel.Sel.Name, types.ExprString(kv.Value), wantClusterIDIdent)
				continue
			}
			want[sel.Sel.Name] = true
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
