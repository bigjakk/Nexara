package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// DELETE /clusters/:cluster_id/drs/rules/:rule_id authorizes the PATH's
// cluster, and a rule id says nothing about which cluster the rule belongs to.
// Until the delete named the cluster as well, manage:drs on one cluster
// deleted any cluster's rule — and the audit row filed it under the caller's
// cluster, so the owning cluster's log never showed it.

// drsRuleDBTX stands in for a drs_rules table holding one rule. The DELETE
// matches only when the statement names BOTH that rule's id and its cluster,
// which is what the cluster_id predicate in queries/drs.sql does — so a case
// fails if the handler stops sending the path's cluster, not only if the SQL
// text changes.
type drsRuleDBTX struct {
	ruleID      uuid.UUID
	ruleCluster uuid.UUID

	statements []string
	deleteSQL  string
	deleteArgs []any
}

func (d *drsRuleDBTX) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.statements = append(d.statements, sql)
	switch {
	case strings.Contains(sql, "DELETE FROM drs_rules"):
		d.deleteSQL, d.deleteArgs = sql, args
		matched := 0
		if len(args) == 2 && args[0] == d.ruleID && args[1] == d.ruleCluster {
			matched = 1
		}
		return pgconn.NewCommandTag("DELETE " + strconv.Itoa(matched)), nil
	case strings.Contains(sql, "INSERT INTO audit_log"):
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.CommandTag{}, errCaptured
}

func (d *drsRuleDBTX) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.statements = append(d.statements, sql)
	return nil, errCaptured
}

func (d *drsRuleDBTX) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	d.statements = append(d.statements, sql)
	return failRow{err: errCaptured}
}

func (d *drsRuleDBTX) audited() bool {
	for _, sql := range d.statements {
		if strings.Contains(sql, "INSERT INTO audit_log") {
			return true
		}
	}
	return false
}

// drsRulePathMirror restates the parameters registry_drs.go declares on the
// route, for the reason withRequestParams' doc gives: package api imports this
// package, not the other way round.
func drsRulePathMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"cluster_id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"rule_id":    {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
	})
}

// TestDRSRuleDeleteRefusesAnotherClustersRule is the regression test for the
// unscoped delete.
//
// The "another cluster" row is the vulnerability: it used to answer 200, delete
// the rule and write an audit row under the wrong cluster. It now answers the
// SAME 404 as a rule that does not exist, so the route does not confirm that
// some other cluster owns the id, and it writes no audit row for a delete that
// did not happen.
func TestDRSRuleDeleteRefusesAnotherClustersRule(t *testing.T) {
	pathCluster := uuid.New()
	otherCluster := uuid.New()
	ruleID := uuid.New()

	tests := []struct {
		name        string
		ruleCluster uuid.UUID
		target      uuid.UUID
		wantStatus  int
	}{
		{"a rule in the path's cluster is deleted", pathCluster, ruleID, http.StatusOK},
		{"a rule in ANOTHER cluster is refused", otherCluster, ruleID, http.StatusNotFound},
		{"a rule that does not exist answers the same 404", pathCluster, uuid.New(), http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbtx := &drsRuleDBTX{ruleID: ruleID, ruleCluster: tt.ruleCluster}
			handler := NewDRSHandler(db.New(dbtx), "", nil, nil)

			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Use(func(c fiber.Ctx) error {
				c.Locals("user_id", uuid.New())
				return c.Next()
			})
			app.Delete("/clusters/:cluster_id/drs/rules/:rule_id",
				withRequestParams(t, drsRulePathMirror(t), []string{"cluster_id", "rule_id"}, handler.DeleteRule))

			req := httptest.NewRequest(http.MethodDelete,
				"/clusters/"+pathCluster.String()+"/drs/rules/"+tt.target.String(), nil)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", resp.StatusCode, tt.wantStatus, body)
			}

			// The statement itself carries the predicate and the PATH's
			// cluster. The emulated table above already refuses a statement
			// that names the wrong cluster, but it ASSUMES the two conditions
			// are joined by AND; this pins that the SQL says so. A substring
			// check was not enough — `id = $1 OR cluster_id = $2` contains it,
			// and would delete the rule from any cluster plus every rule in
			// the path's.
			if got := strings.Join(strings.Fields(dbtx.deleteSQL), " "); !strings.HasSuffix(got,
				"WHERE id = $1 AND cluster_id = $2") {
				t.Errorf("the DELETE was %q; it must end in WHERE id = $1 AND cluster_id = $2", got)
			}
			if len(dbtx.deleteArgs) != 2 || dbtx.deleteArgs[1] != pathCluster {
				t.Errorf("the DELETE named cluster %v, want the path's %v", dbtx.deleteArgs, pathCluster)
			}

			wantAudit := tt.wantStatus == http.StatusOK
			if got := dbtx.audited(); got != wantAudit {
				t.Errorf("audit row written = %v, want %v — a refused delete must not record one", got, wantAudit)
			}
			if tt.wantStatus == http.StatusNotFound && !strings.Contains(string(body), "DRS rule not found") {
				t.Errorf("body = %s, want the same \"DRS rule not found\" on both branches — a distinct "+
					"answer for \"exists but not yours\" is an existence oracle", body)
			}
		})
	}
}
