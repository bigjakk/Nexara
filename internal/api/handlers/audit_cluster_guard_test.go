package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"testing"
)

// The cluster argument's position differs between the two audit entry points:
// AuditLog(c, queries, pub, clusterID, ...) and
// AuditLogAs(c, queries, pub, actor, clusterID, ...).
var auditClusterArgIndex = map[string]int{
	"AuditLog":   3,
	"AuditLogAs": 4,
}

// auditClusterExempt names every audit call site that deliberately records a
// NULL cluster, and why that is right. Keys are "file.go.EnclosingFunc".
//
// This map exists because a NULL cluster_id is not "unknown" — it is load
// bearing. It marks a GLOBAL audit entry, and the scoped audit reads added in
// 33d68f2 hand those only to a holder of global view:audit (`cluster_id =
// ANY(array)` yields NULL, not true, for a NULL column, so the clause excludes
// them from every cluster-scoped caller). That is exactly right for a login or
// a settings change. For an action on a resource that belongs to one cluster it
// is a disappearing act: the operator who can see and manage the resource
// cannot find their own action in the audit page, the filters, the CSV/JSON
// export or the dashboard feed.
//
// So the rule is: audit with the resource's cluster. Passing NULL is still
// available, but it stops being something a new handler can do by inattention —
// it becomes a line someone has to write down and justify here.
//
// Every entry below is a resource that belongs to the install rather than to a
// cluster, and each is gated on a GLOBAL permission — so the global-only
// readership of a NULL-cluster row matches exactly who is allowed to perform
// the action. That pairing is the test to apply when adding an entry: if a
// cluster-scoped operator can do it, the row must name their cluster.
var auditClusterExempt = map[string]string{
	// Session and credential events for one user. There is no cluster in scope
	// at any of these points — several run before or after the session exists.
	"auth.go.Register":       "registration is install-global; no cluster in scope",
	"auth.go.issueTokens":    "login event; no cluster in scope",
	"auth.go.Refresh":        "session refresh; no cluster in scope",
	"auth.go.Logout":         "session teardown; no cluster in scope",
	"auth.go.LogoutAll":      "session teardown; no cluster in scope",
	"auth.go.ChangePassword": "credential change for one user; install-global",
	"auth.go.UpdateProfile":  "profile change for one user; install-global",
	"auth.go.WSToken":        "the event-stream token is not bound to a cluster (unlike ConsoleToken, which is)",

	"auth_sessions.go.RevokeSessionByID": "sessions belong to a user account, not a cluster; same scope as Logout/LogoutAll",

	"totp.go.ConfirmSetup":            "MFA enrolment for one user; install-global",
	"totp.go.Disable":                 "MFA change for one user; install-global",
	"totp.go.RegenerateRecoveryCodes": "MFA change for one user; install-global",
	"totp.go.AdminReset":              "MFA change for one user; install-global",
	"api_keys.go.Create":              "api_keys is user-owned; no cluster column",
	"api_keys.go.Revoke":              "api_keys is user-owned; no cluster column",
	"api_keys.go.RevokeAll":           "api_keys is user-owned; no cluster column",
	"api_keys.go.AdminRevoke":         "api_keys is user-owned; no cluster column",
	"users.go.Update":                 "users is install-global; no cluster column",
	"users.go.Delete":                 "users is install-global; no cluster column",

	// Roles and their assignments. An assignment may target one cluster via
	// scope_id, but that is the grant's reach, not the audit row's cluster:
	// assigning is gated on GLOBAL manage:role, so only an install admin can do
	// it and only an install admin needs to read it.
	"rbac.go.CreateRole":     "roles is install-global; no cluster column",
	"rbac.go.UpdateRole":     "roles is install-global; no cluster column",
	"rbac.go.DeleteRole":     "roles is install-global; no cluster column",
	"rbac.go.AssignUserRole": "gated on global manage:role; scope_id is the grant's reach, not the audit cluster",
	"rbac.go.RevokeUserRole": "gated on global manage:role; scope_id is the grant's reach, not the audit cluster",

	// Install-wide configuration: applies to every cluster at once.
	"oidc.go.Create":                "OIDC config is install-global",
	"oidc.go.Update":                "OIDC config is install-global",
	"oidc.go.Delete":                "OIDC config is install-global",
	"ldap.go.Create":                "LDAP config is install-global",
	"ldap.go.Update":                "LDAP config is install-global",
	"ldap.go.Delete":                "LDAP config is install-global",
	"ldap.go.Sync":                  "LDAP sync is install-global",
	"oidc.go.Callback":              "OIDC login callback is an auth event; no cluster in scope",
	"settings.go.auditSettingWrite": "settings are install-global",
	"virtio_win.go.SetMirror":       "the virtio-win download source is one instance-wide setting row, gated on global manage:settings",
	"audit.go.UpdateSyslogConfig":   "syslog forwarding is an install-global setting",
	"audit.go.TestSyslog":           "syslog forwarding is an install-global setting",

	// Resources whose tables have no cluster_id column at all.
	"alerts.go.CreateChannel": "notification_channels has no cluster_id column",
	"alerts.go.UpdateChannel": "notification_channels has no cluster_id column",
	"alerts.go.DeleteChannel": "notification_channels has no cluster_id column",
	"alerts.go.TestChannel":   "notification_channels has no cluster_id column",
	// A Veeam server can protect several Proxmox clusters at once, so
	// veeam_servers has no cluster_id column and naming any one cluster would
	// be wrong rather than merely incomplete. Every route on the registry is
	// gated on GLOBAL view/manage/delete:veeam, so the global-only readership
	// of a NULL-cluster row is exactly the set of people who can perform the
	// action. Per-cluster Veeam data (jobs, sessions, restore points) is a
	// different resource and audits with its cluster.
	"veeam_servers.go.audit": "veeam_servers spans clusters and has no cluster_id column; gated on global manage/delete:veeam",

	"firewall_templates.go.CreateTemplate": "firewall_templates has no cluster_id column",
	"firewall_templates.go.UpdateTemplate": "firewall_templates has no cluster_id column",
	"firewall_templates.go.DeleteTemplate": "firewall_templates has no cluster_id column",

	// Deleting a cluster. audit_log.cluster_id is ON DELETE CASCADE, so a row
	// naming the cluster it records the deletion of cannot survive: written
	// before the delete it is cascaded away, written after it violates the FK.
	// NULL is the only way this action gets recorded at all; the cluster id and
	// name travel in the details body. Gated on delete:cluster, and the
	// credential-revocation half additionally on global manage:cluster.
	"clusters.go.Delete": "audit_log.cluster_id CASCADEs with the cluster; NULL is the only way the row survives its own event",

	// A failed cluster onboarding: there is no cluster row, because creating
	// one is what failed. Gated on global manage:cluster, so the global-only
	// readership of a NULL-cluster row is exactly who can attempt it.
	"clusters_bootstrap.go.auditBootstrapFailure": "onboarding failed before a cluster existed; gated on global manage:cluster",

	// Genuinely cross-cluster: deletes completed task history for EVERY cluster
	// in one statement, and is gated on global manage:task for that reason, so
	// no single cluster owns the row.
	"tasks.go.ClearCompleted": "deletes completed tasks across every cluster; gated on global manage:task",
}

// auditInsertParams are the sqlc params structs that write an audit row
// directly, bypassing AuditLog/AuditLogAs. The background packages (drs,
// rolling, migration, scheduler, collector) have no fiber.Ctx and so must use
// these — which means a guard that watched only the helpers would leave the
// obvious way around itself open.
var auditInsertParams = map[string]bool{
	"InsertAuditLogParams":           true,
	"InsertAuditLogWithSourceParams": true,
}

// nullClusterInsert reports whether a db.InsertAuditLog*Params literal writes a
// NULL cluster — either by setting ClusterID to a NULL pgtype.UUID or by
// omitting the field, which zero-values to the same thing.
func nullClusterInsert(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "ClusterID" {
			continue
		}
		return nullClusterArg(kv.Value)
	}
	return true // omitted entirely — the zero pgtype.UUID is SQL NULL
}

// isAuditParams reports whether lit is one of the audit insert structs,
// qualified or not.
func isAuditParams(lit *ast.CompositeLit) bool {
	switch t := lit.Type.(type) {
	case *ast.SelectorExpr:
		return auditInsertParams[t.Sel.Name]
	case *ast.Ident:
		return auditInsertParams[t.Name]
	}
	return false
}

// nullClusterArg reports whether expr is a pgtype.UUID composite literal that
// evaluates to SQL NULL — `pgtype.UUID{}`, or one that spells out Valid: false.
// A literal is the only shape this can judge: a variable may hold either, and
// the nullable cases (an alert or a PBS server that legitimately has no
// cluster) are exactly the ones that must stay allowed.
func nullClusterArg(expr ast.Expr) bool {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return false
	}
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "UUID" {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "pgtype" {
		return false
	}
	if len(lit.Elts) == 0 {
		return true
	}
	// Spelled out: NULL unless some element sets Valid to true.
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Valid" {
			continue
		}
		if val, ok := kv.Value.(*ast.Ident); ok && val.Name == "true" {
			return false
		}
	}
	return true
}

// TestNullClusterArg pins the guard's own detector. A guard that silently stops
// recognising the shape it hunts is worse than no guard: it reports success
// over a repo full of the defect. The false cases matter as much as the true
// ones — flagging `alert.ClusterID` or `ClusterUUID(x)` would push authors to
// paper over the guard rather than fix anything.
func TestNullClusterArg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{"empty literal is NULL", "pgtype.UUID{}", true},
		{"explicit Valid false is NULL", "pgtype.UUID{Valid: false}", true},
		{"Bytes without Valid is NULL", "pgtype.UUID{Bytes: b}", true},
		{"Valid true is not NULL", "pgtype.UUID{Bytes: b, Valid: true}", false},
		{"Valid true first is not NULL", "pgtype.UUID{Valid: true, Bytes: b}", false},
		{"helper call is not a literal", "ClusterUUID(x.ClusterID)", false},
		{"field access is not a literal", "alert.ClusterID", false},
		{"plain identifier is not a literal", "cluster", false},
		{"a different pgtype zero value is not a cluster UUID", "pgtype.Text{}", false},
		{"a same-named type from another package is not ours", "other.UUID{}", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			expr, err := parser.ParseExpr(tt.expr)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.expr, err)
			}
			if got := nullClusterArg(expr); got != tt.want {
				t.Errorf("nullClusterArg(%s) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

// TestGuard_AuditCallsCarryResourceCluster fails when a handler audits with a
// literal NULL cluster without saying why in auditClusterExempt.
//
// It is a static guard for the same reason TestGuard_ScopedParamsCarryClusterScope
// is: handlers hold a concrete *db.Queries, so no test can observe the audit row
// a handler actually writes. Structure is what is left.
func TestGuard_AuditCallsCarryResourceCluster(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	var findings []string
	seen := map[string]bool{}

	for _, path := range goSourceFiles(t) {
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		base := filepath.Base(path)

		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				var pos token.Pos

				switch node := n.(type) {
				case *ast.CallExpr:
					ident, ok := node.Fun.(*ast.Ident)
					if !ok {
						return true
					}
					idx, audits := auditClusterArgIndex[ident.Name]
					if !audits || len(node.Args) <= idx {
						return true
					}
					if !nullClusterArg(node.Args[idx]) {
						return true
					}
					pos = node.Pos()

				case *ast.CompositeLit:
					if !isAuditParams(node) || !nullClusterInsert(node) {
						return true
					}
					pos = node.Pos()

				default:
					return true
				}

				key := base + "." + fn.Name.Name
				seen[key] = true
				if _, exempt := auditClusterExempt[key]; exempt {
					return true
				}
				findings = append(findings, fset.Position(pos).String()+
					": "+fn.Name.Name+" audits with a NULL cluster.\n"+
					"\tA NULL cluster_id marks a GLOBAL audit entry, readable only with\n"+
					"\tglobal view:audit — so a cluster-scoped operator cannot see their own\n"+
					"\taction. Pass the resource's cluster (ClusterUUID(x.ClusterID) for a\n"+
					"\tNOT NULL column, x.ClusterID for a nullable one). If the resource\n"+
					"\ttruly belongs to no cluster, add \""+key+"\" to\n"+
					"\tauditClusterExempt with the reason.")
				return true
			})
		}
	}

	sort.Strings(findings)
	for _, f := range findings {
		t.Error(f)
	}

	// A stale exemption is its own hazard: it silently blesses whatever that
	// function does next. Fail when one no longer matches a NULL-cluster call.
	var stale []string
	for key := range auditClusterExempt {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("auditClusterExempt has %q, which no longer audits with a NULL cluster — "+
			"delete the entry so it cannot bless a future change", key)
	}
}
