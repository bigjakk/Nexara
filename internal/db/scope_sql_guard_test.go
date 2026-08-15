package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// queriesGlob covers every sqlc source file, not just audit_log.sql: an
// unscoped read of audit_log is no safer for living in another file, and one
// already does (proxmox_task_sync.sql). These guards read the .sql sources
// rather than the generated constants because those are what a future change
// edits, and because the generated package's query strings are unexported.
const queriesGlob = "../../queries/*.sql"

// scopeClauseRe matches the exact predicate an RBAC-scoped read must carry:
// the NULL guard followed by one or more `<col>cluster_id = ANY(<same narg>)`
// disjuncts and nothing else inside the parens, introduced by WHERE or AND.
//
// Matching the whole shape rather than searching for fragments is what makes
// this a guard. `COALESCE(cluster_id, …) = ANY(…)`, `cluster_id IS NOT DISTINCT
// FROM ANY(…)` and an added `cluster_id IS NULL` arm all fail to match, so a
// query "repaired" into any of them reads as unscoped and the guard fires.
// The one-or-more repetition admits migration_jobs, which tests two columns
// because a job straddles a source and a target cluster.
//
// Comparison is against a whitespace-normalized, lowercased body, so reflowing
// or re-casing the SQL is fine; changing its structure is not. What it does NOT
// verify is that the clause sits at the top level of the WHERE — a clause
// nested inside an OR branch would still match. Pinning that needs a parser;
// the DB-backed tests in scope_db_test.go are what hold the behaviour.
var scopeClauseRe = regexp.MustCompile(
	`(?:where|and) \(sqlc\.narg\('accessible_cluster_ids'\)::uuid\[\] is null` +
		`(?: or [a-z_]*\.?[a-z_]*cluster_id = any\(sqlc\.narg\('accessible_cluster_ids'\)::uuid\[\]\))+\)`)

// auditReadsExemptFromScope names every query that reads audit_log WITHOUT the
// caller's RBAC scope, and why that is safe. Keys are "file.sql.QueryName".
// The guard below admits a read only if it carries the clause or appears here,
// so omitting the scope stops being something a new query can do by
// inattention: it becomes a line someone has to write down and justify.
var auditReadsExemptFromScope = map[string]string{
	// Column projections rather than entry reads — they return the set of
	// distinct actions and users, never a row's cluster, resource or details.
	// Both are gated on GLOBAL view:audit by requirePerm in ListActions /
	// ListUsers, and a cluster-scoped grant never satisfies a global check
	// (internal/auth/rbac.go), so no scoped caller reaches either one.
	"audit_log.sql.ListDistinctAuditActions": "global view:audit only; returns action names, not entries",
	"audit_log.sql.ListDistinctAuditUsers":   "global view:audit only; returns user identities, not entries",

	// Not served to a caller at all: the collector's batch dedup, which asks
	// which of the UPIDs it is holding have already been recorded. It returns
	// only UPIDs the caller supplied, runs on the background sync path with no
	// user in scope, and there is no RBAC context to apply.
	"proxmox_task_sync.sql.ListExistingAuditLogUPIDs": "collector-internal dedup; echoes back caller-supplied UPIDs, no user context",
}

// readsAuditLogRe identifies a query that reads audit_log, through FROM or a
// JOIN, schema-qualified or quoted. INSERTs have no FROM/JOIN and so are never
// treated as reads.
var readsAuditLogRe = regexp.MustCompile(`(?i)\b(?:from|join)\s+(?:public\.)?"?audit_log"?\b`)

// bareNullClusterRe finds a bare `cluster_id IS NULL` column test. The leading
// class keeps it off the unrelated `sqlc.narg('cluster_id')::uuid IS NULL`
// filter guards, where the identifier is followed by `')` rather than space.
var bareNullClusterRe = regexp.MustCompile(`(?i)(?:^|[\s(])(?:a\.)?cluster_id\s+is\s+null`)

// TestAuditScopeSQL_EveryReadIsScopedOrExempt is the guard that survives
// growth: it enumerates the audit_log reads across queries/ rather than
// checking a fixed list, so a query added tomorrow — in any query file — is
// covered the day it lands.
func TestAuditScopeSQL_EveryReadIsScopedOrExempt(t *testing.T) {
	t.Parallel()

	for name, body := range namedSQLQueries(t) {
		if !readsAuditLogRe.MatchString(body) {
			continue // INSERT, or a query against some other table
		}
		scoped := hasScopeClause(body)
		reason, exempt := auditReadsExemptFromScope[name]

		switch {
		case scoped && exempt:
			t.Errorf("%s carries the accessible_cluster_ids scope AND is listed in "+
				"auditReadsExemptFromScope (%q). Drop the exemption — it now reads as "+
				"permission to remove a clause that is doing real work.", name, reason)
		case !scoped && !exempt:
			t.Errorf("%s reads audit_log without the caller's RBAC scope.\n"+
				"\tAdd\n\t\tAND (sqlc.narg('accessible_cluster_ids')::uuid[] IS NULL\n"+
				"\t\t     OR cluster_id = ANY(sqlc.narg('accessible_cluster_ids')::uuid[]))\n"+
				"\tand feed it from clusterAccess.ScopedIDs(), or record in "+
				"auditReadsExemptFromScope why this read is safe unscoped.\n\nquery:\n%s",
				name, body)
		}
	}
}

// TestScopeSQL_ScopedClausesExcludeNullCluster pins the property that makes
// every scope clause correct on a NULLABLE cluster column — the way audit_log,
// alert_rules and alert_history differ from task_history, migration_jobs and
// the report tables, whose columns are NOT NULL.
//
// A NULL cluster_id marks a GLOBAL row: an audit entry for a settings change or
// a login, an alert rule that watches every cluster, an alert fired by one.
// Only a holder of the global view permission may read those, and each
// handler's per-row guard says so by requiring access.HasGlobal. In SQL it
// falls out of three-valued logic for free: `cluster_id = ANY(array)` evaluates
// to NULL, not true, for such a row, so a scoped caller's WHERE clause drops it
// without a predicate of its own.
//
// Applied to EVERY query carrying the clause, not just the audit ones: the same
// "repair" is available in each file, and on the nullable tables it has the same
// effect — handing every global row to every cluster-scoped user. On the NOT
// NULL tables the check simply never has anything to complain about.
//
// This pins the clause's SHAPE. The tests in scope_db_test.go execute the real
// queries against Postgres and pin the BEHAVIOUR; they need NEXARA_TEST_DB_URL,
// which CI sets and a local run usually does not, so both exist.
func TestScopeSQL_ScopedClausesExcludeNullCluster(t *testing.T) {
	t.Parallel()

	queries := namedSQLQueries(t)

	scopedFound := 0
	for name, body := range queries {
		if !hasScopeClause(body) {
			continue
		}
		scopedFound++
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stripped := stripSQLComments(body)
			if loc := bareNullClusterRe.FindStringIndex(stripped); loc != nil {
				t.Fatalf("%s carries the RBAC scope clause and also tests %q.\n"+
					"\tA NULL cluster column marks a GLOBAL row, readable only by a holder of\n"+
					"\tthe global view permission. `cluster_id = ANY(...)` already yields NULL\n"+
					"\tfor those rows, so a scoped caller never matches one. Adding an IS NULL\n"+
					"\tdisjunct hands every global row to every cluster-scoped user.",
					name, strings.TrimSpace(stripped[loc[0]:loc[1]]))
			}
		})
	}

	if scopedFound == 0 {
		t.Fatal("no query carried the scope clause — either the matcher broke or the " +
			"scoping was reverted wholesale; either way this guard was passing vacuously")
	}

	// The clause is only worth pinning if the reads that must carry it do. Each
	// backs a list endpoint that serves cluster-scoped callers.
	for _, name := range []string{
		"audit_log.sql.ListAuditLogAdvanced",
		"audit_log.sql.CountAuditLogAdvanced",
		"audit_log.sql.ListRecentAuditLogEnriched",
		"tasks.sql.ListTaskHistoryFiltered",
		"tasks.sql.CountTaskHistoryFiltered",
		"alerts.sql.ListAlertRules",
		"alerts.sql.ListAlertHistoryFiltered",
		"migrations.sql.ListMigrationJobs",
		"reports.sql.ListReportSchedules",
		"reports.sql.ListReportRuns",
	} {
		if body, ok := queries[name]; !ok || !hasScopeClause(body) {
			t.Errorf("%s must carry the accessible_cluster_ids scope clause — it backs a "+
				"list endpoint that serves cluster-scoped callers", name)
		}
	}
}

// hasScopeClause reports whether a query body carries the scope predicate in
// its canonical shape.
//
// Comments are stripped first, so a one-line `-- AND (…)` comment-out reads as
// unscoped rather than as scoped — the failure direction that matters.
func hasScopeClause(body string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(stripSQLComments(body)), " "))
	return scopeClauseRe.MatchString(normalized)
}

// stripSQLComments removes `--`-to-end-of-line comments. String literals
// containing `--` would be truncated too; none exist in queries/, and the
// error is in the safe direction — losing text can only make a clause look
// absent, never present.
func stripSQLComments(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

// namedSQLQueries indexes every sqlc query in queries/ by
// "file.sql.QueryName". A body runs from the `-- name:` annotation to the
// terminating semicolon, so the prose block introducing the NEXT query is not
// read as part of this one — which matters here, since those blocks quote the
// very SQL these assertions search for.
func namedSQLQueries(t *testing.T) map[string]string {
	t.Helper()

	paths, err := filepath.Glob(queriesGlob)
	if err != nil {
		t.Fatalf("glob %s: %v", queriesGlob, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no query files matched %s", queriesGlob)
	}

	nameRe := regexp.MustCompile(`^--\s*name:\s*(\S+)`)
	queries := make(map[string]string)

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		prefix := filepath.Base(path) + "."

		current := ""
		var body strings.Builder
		flush := func() {
			if current != "" {
				queries[prefix+current] = body.String()
			}
			body.Reset()
			current = ""
		}

		for _, line := range strings.Split(string(raw), "\n") {
			if m := nameRe.FindStringSubmatch(line); m != nil {
				flush()
				current = m[1]
				continue
			}
			if current == "" {
				continue
			}
			if end := statementEnd(line); end >= 0 {
				body.WriteString(line[:end+1])
				flush()
				continue
			}
			body.WriteString(line)
			body.WriteString("\n")
		}
		flush()
	}

	if len(queries) == 0 {
		t.Fatalf("no `-- name:` annotations found under %s", queriesGlob)
	}
	return queries
}

// statementEnd returns the index of the semicolon that terminates a statement
// on this line, or -1. Semicolons inside single-quoted literals are skipped —
// otherwise `WHERE action = 'a;b'` would truncate the body early and the query
// could drop out of the guard's view entirely.
func statementEnd(line string) int {
	inLiteral := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\'':
			// '' inside a literal is an escaped quote, which this toggle
			// handles correctly: it closes and immediately reopens.
			inLiteral = !inLiteral
		case '-':
			if !inLiteral && i+1 < len(line) && line[i+1] == '-' {
				return -1 // rest of the line is a comment
			}
		case ';':
			if !inLiteral {
				return i
			}
		}
	}
	return -1
}
