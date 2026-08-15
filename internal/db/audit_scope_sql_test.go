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

// auditScopeClause is the exact predicate an RBAC-scoped audit read must
// carry, in the two spellings the file uses (aliased in the JOIN queries, bare
// in the count). Matching the whole shape rather than searching for fragments
// is deliberate: it pins that the clause has exactly two disjuncts and that
// membership is tested by a bare `cluster_id = ANY(...)`, so
// `COALESCE(cluster_id, ...) = ANY(...)`, `cluster_id IS NOT DISTINCT FROM
// ANY(...)` and an added `cluster_id IS NULL` arm each fail here instead of
// sliding past a looser check.
//
// Comparison is whitespace-normalized and case-insensitive, so reflowing or
// re-casing the SQL is fine; changing its structure is not. What it does NOT
// verify is that the clause sits at the top level of the WHERE — a clause
// nested inside an OR branch or a subquery would still match. Pinning that
// needs a parser; the DB-backed test is what actually holds the behaviour.
var auditScopeClause = []string{
	"(sqlc.narg('accessible_cluster_ids')::uuid[] is null " +
		"or cluster_id = any(sqlc.narg('accessible_cluster_ids')::uuid[]))",
	"(sqlc.narg('accessible_cluster_ids')::uuid[] is null " +
		"or a.cluster_id = any(sqlc.narg('accessible_cluster_ids')::uuid[]))",
}

// auditReadsExemptFromScope names every query that reads audit_log WITHOUT the
// caller's RBAC scope, and why that is safe. Keys are "file.sql.QueryName".
// The guard below admits a read only if it carries the clause or appears here,
// so omitting the scope stops being something a new query can do by
// inattention: it becomes a line someone has to write down and justify.
var auditReadsExemptFromScope = map[string]string{
	// No caller outside internal/db/generated as of this writing. Left unscoped
	// rather than fixed, because scoping a query nothing runs mostly teaches the
	// next reader that it is load-bearing. Wiring any of these to a handler
	// means scoping it and deleting its line here — the exemption is a record
	// that the query is unreachable, not a licence to read across clusters.
	"audit_log.sql.ListAuditLog":          "unused; no caller outside internal/db/generated",
	"audit_log.sql.ListAuditLogByCluster": "unused; AuditHandler.ListByCluster runs ListAuditLogAdvanced instead",
	"audit_log.sql.ListAuditLogFiltered":  "unused; superseded by ListAuditLogAdvanced",
	"audit_log.sql.ListAuditLogEnriched":  "unused; superseded by ListAuditLogAdvanced",
	"audit_log.sql.CountAuditLog":         "unused; superseded by CountAuditLogAdvanced",

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
		scoped := hasAuditScopeClause(body)
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

// TestAuditScopeSQL_ExcludesNullCluster pins the property that makes the scope
// clause correct on a NULLABLE cluster_id — the way audit_log differs from
// task_history, whose column is NOT NULL.
//
// A NULL cluster_id marks a global entry (a settings change, a login) that
// only a holder of global view:audit may read; AuditHandler's per-row guards
// show them to access.HasGlobal alone. In SQL that falls out of three-valued
// logic for free: `cluster_id = ANY(array)` evaluates to NULL, not true, for a
// NULL cluster_id, so a scoped caller's WHERE clause drops those rows without
// a predicate of its own.
//
// This test pins the clause's SHAPE. TestAuditScope_NullClusterRowsAreGlobal
// (audit_scope_db_test.go) executes the real queries against Postgres and pins
// the BEHAVIOUR; it needs NEXARA_TEST_DB_URL, which CI sets and a local run
// usually does not, so both exist.
//
// The failure mode guarded here is someone reading the bare clause as an
// oversight and "repairing" it into `(cluster_id IS NULL OR cluster_id =
// ANY(...))`, which hands every global entry to every cluster-scoped user.
func TestAuditScopeSQL_ExcludesNullCluster(t *testing.T) {
	t.Parallel()

	queries := namedSQLQueries(t)

	for name, body := range queries {
		if !readsAuditLogRe.MatchString(body) || !hasAuditScopeClause(body) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stripped := stripSQLComments(body)
			if loc := bareNullClusterRe.FindStringIndex(stripped); loc != nil {
				t.Fatalf("%s tests cluster_id IS NULL (%q).\n"+
					"\taudit_log rows with a NULL cluster_id are GLOBAL entries, readable only\n"+
					"\tby a holder of global view:audit. `cluster_id = ANY(...)` already yields\n"+
					"\tNULL for them, so a scoped caller never matches one. Adding an IS NULL\n"+
					"\tdisjunct hands every global entry to every cluster-scoped user.",
					name, strings.TrimSpace(stripped[loc[0]:loc[1]]))
			}
		})
	}

	// The clause is only worth pinning if the reads that must carry it do.
	for _, name := range []string{
		"audit_log.sql.ListAuditLogAdvanced",
		"audit_log.sql.CountAuditLogAdvanced",
		"audit_log.sql.ListRecentAuditLogEnriched",
	} {
		if body, ok := queries[name]; !ok || !hasAuditScopeClause(body) {
			t.Errorf("%s must carry the scope clause — it backs a handler that serves "+
				"cluster-scoped callers", name)
		}
	}
}

// hasAuditScopeClause reports whether a query body carries the scope predicate
// in its canonical shape, introduced by WHERE or AND.
//
// Comments are stripped first, so a one-line `-- AND (…)` comment-out reads as
// unscoped rather than as scoped — the failure direction that matters.
func hasAuditScopeClause(body string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(stripSQLComments(body)), " "))
	for _, clause := range auditScopeClause {
		if strings.Contains(normalized, "where "+clause) ||
			strings.Contains(normalized, "and "+clause) {
			return true
		}
	}
	return false
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
