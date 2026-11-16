package db

import (
	"regexp"
	"strings"
	"testing"
)

// drsRulesStatementRe finds a statement on drs_rules, through FROM, JOIN,
// UPDATE or INTO.
var drsRulesStatementRe = regexp.MustCompile(`(?i)\b(?:from|join|update|into)\s+(?:public\.)?"?drs_rules"?\b`)

// drsRuleWhereRe captures a statement's WHERE clause, up to whatever may
// legitimately follow it.
var drsRuleWhereRe = regexp.MustCompile(`(?is)\bwhere\b(.*?)(?:\breturning\b|\border\s+by\b|\blimit\b|;|$)`)

// drsRuleByIDRe finds the row id compared in a WHERE clause — `id = $1`,
// `r.id=@rule_id`, `id = sqlc.arg(...)`, `id IN (...)`, `id = ANY(...)`. The
// leading class keeps it off cluster_id, where "id" follows an underscore.
var drsRuleByIDRe = regexp.MustCompile(`(?i)(?:^|[^\w.])(?:\w+\.)?id\s*(?:=|\bin\b)`)

// drsRuleClusterRe finds the cluster predicate, in any of those spellings.
var drsRuleClusterRe = regexp.MustCompile(`(?i)(?:^|[^\w.])(?:\w+\.)?cluster_id\s*=`)

// drsRuleOrRe finds an OR, which is what would let the cluster predicate be
// present and still not bind: `id = $1 OR cluster_id = $2`.
var drsRuleOrRe = regexp.MustCompile(`(?i)\bor\b`)

// TestDRSRuleSQL_EveryByIDStatementNamesTheCluster: routes authorize the cluster in the path, so
// a statement keyed on a rule id alone lets manage:drs on one cluster reach another's
// (DeleteDRSRule was). Every by-id WHERE must carry `cluster_id =` and no OR, in any parameter
// spelling sqlc accepts, aliases allowed. It reads queries/drs.sql, so a later route cannot pick
// up an unscoped statement.
func TestDRSRuleSQL_EveryByIDStatementNamesTheCluster(t *testing.T) {
	t.Parallel()

	checked := 0
	for name, body := range namedSQLQueries(t) {
		sql := stripSQLComments(body)
		if !drsRulesStatementRe.MatchString(sql) {
			continue
		}
		where := drsRuleWhereRe.FindStringSubmatch(sql)
		if where == nil || !drsRuleByIDRe.MatchString(where[1]) {
			continue
		}
		checked++
		clause := strings.Join(strings.Fields(where[1]), " ")
		if !drsRuleClusterRe.MatchString(clause) {
			t.Errorf("%s addresses a drs_rules row by id with no cluster_id predicate: WHERE %s", name, clause)
		}
		if drsRuleOrRe.MatchString(clause) {
			t.Errorf("%s has an OR in its WHERE clause, so its cluster_id predicate need not bind: WHERE %s",
				name, clause)
		}
	}
	// Three today — Get, Update and Delete. Zero would mean the file moved or
	// the patterns stopped matching, and every row above passed by default.
	if checked == 0 {
		t.Fatal("no by-id drs_rules statement found; this guard is reading nothing")
	}
}

// TestDRSRuleSQLGuardRecognisesEveryShape drives the guard's own patterns with
// the statement shapes a future query could take — the parameter spellings
// sqlc accepts, an alias, a RETURNING tail — so a pattern that stops
// recognising one fails here rather than letting that query through unread.
func TestDRSRuleSQLGuardRecognisesEveryShape(t *testing.T) {
	t.Parallel()

	byID := func(sql string) bool {
		w := drsRuleWhereRe.FindStringSubmatch(sql)
		return w != nil && drsRuleByIDRe.MatchString(w[1])
	}
	scoped := func(sql string) bool {
		w := drsRuleWhereRe.FindStringSubmatch(sql)
		return w != nil && drsRuleClusterRe.MatchString(w[1]) && !drsRuleOrRe.MatchString(w[1])
	}

	for _, tt := range []struct {
		sql            string
		byID, isScoped bool
	}{
		{"DELETE FROM drs_rules WHERE id = $1 AND cluster_id = $2;", true, true},
		{"DELETE FROM drs_rules WHERE id=$1 AND cluster_id=$2 RETURNING *;", true, true},
		{"SELECT * FROM drs_rules r WHERE r.id = @rule_id AND r.cluster_id = @cluster_id;", true, true},
		{"UPDATE drs_rules SET enabled = $3 WHERE id = sqlc.arg(rule_id) AND cluster_id = sqlc.arg(cluster_id);", true, true},
		{"DELETE FROM drs_rules WHERE id = ANY($1::uuid[]);", true, false},
		{"DELETE FROM drs_rules WHERE id IN ($1, $2);", true, false},
		{"DELETE FROM drs_rules WHERE id = $1 OR cluster_id = $2;", true, false},
		{"DELETE FROM drs_rules WHERE id = sqlc.arg(rule_id);", true, false},
		{"SELECT * FROM drs_rules WHERE cluster_id = $1 ORDER BY created_at;", false, true},
	} {
		if got := byID(tt.sql); got != tt.byID {
			t.Errorf("by-id(%q) = %v, want %v", tt.sql, got, tt.byID)
		}
		if tt.byID {
			if got := scoped(tt.sql); got != tt.isScoped {
				t.Errorf("scoped(%q) = %v, want %v", tt.sql, got, tt.isScoped)
			}
		}
	}
}
