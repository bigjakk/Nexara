package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// These tests drive the real Update and Delete handlers against a stand-in DBTX
// that serves ONE scheduled task with a cluster of the test's choosing, so the
// cross-cluster case can be exercised without a database.
//
// The hazard: a scheduled task is addressed by its uuid, the permission gate
// resolves the cluster from the PATH, and until this change nothing tied the two
// together — so manage:schedule on any one cluster authorized a write to every
// task in the install. The 404-on-both-branches shape is what keeps the fix from
// becoming an existence oracle in its own right.

// scheduleDBTX serves GetScheduledTask from a fixed row and records every
// statement a handler sends, so a test can assert both the status a caller sees
// and whether the write was reached at all.
type scheduleDBTX struct {
	task  db.ScheduledTask
	found bool

	// rowsAffected is what the scoped UPDATE/DELETE reports. Zero is the
	// answer a cluster_id predicate gives for a row that is not the path's,
	// which is the second layer's behaviour.
	rowsAffected int64

	statements []string
}

func (s *scheduleDBTX) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	s.statements = append(s.statements, sql)
	// pgconn builds a CommandTag from the wire string; "UPDATE n" / "DELETE n"
	// is what RowsAffected parses.
	verb := "UPDATE"
	if strings.Contains(sql, "DELETE FROM scheduled_tasks") {
		verb = "DELETE"
	}
	return pgconn.NewCommandTag(verb + " " + strconv.FormatInt(s.rowsAffected, 10)), nil
}

func (s *scheduleDBTX) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	s.statements = append(s.statements, sql)
	return nil, errCaptured
}

func (s *scheduleDBTX) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	s.statements = append(s.statements, sql)
	if strings.Contains(sql, "FROM scheduled_tasks WHERE id") {
		if !s.found {
			return failRow{err: pgx.ErrNoRows}
		}
		return scheduledTaskRow{task: s.task}
	}
	return failRow{err: errCaptured}
}

func (s *scheduleDBTX) ran(fragment string) bool {
	for _, sql := range s.statements {
		if strings.Contains(sql, fragment) {
			return true
		}
	}
	return false
}

// scheduledTaskRow replays one row in the column order GetScheduledTask scans.
type scheduledTaskRow struct{ task db.ScheduledTask }

func (r scheduledTaskRow) Scan(dest ...any) error {
	values := []any{
		r.task.ID, r.task.ClusterID, r.task.ResourceType, r.task.ResourceID,
		r.task.Node, r.task.Action, r.task.Schedule, r.task.Params, r.task.Enabled,
		r.task.LastRunAt, r.task.NextRunAt, r.task.LastStatus, r.task.LastError,
		r.task.CreatedAt, r.task.UpdatedAt,
	}
	for i, d := range dest {
		if i >= len(values) {
			break
		}
		switch target := d.(type) {
		case *uuid.UUID:
			*target = values[i].(uuid.UUID)
		case *string:
			*target = values[i].(string)
		case *bool:
			*target = values[i].(bool)
		case *json.RawMessage:
			*target = values[i].(json.RawMessage)
		case *time.Time:
			*target = values[i].(time.Time)
		default:
			// The nullable columns are not read by taskInCluster; leaving
			// them at their zero value is correct for this fake.
		}
	}
	return nil
}

// newScheduleScopeApp mounts the two per-task routes the way the registry does:
// the validated parameters the declaration produces, and the handler behind
// them. The permission middleware is NOT mounted — these tests are about the
// cluster the row belongs to, which is a separate check from the grant, and
// mounting a gate that always passes would only obscure which one refused.
func newScheduleScopeApp(t *testing.T, dbtx db.DBTX) *fiber.App {
	t.Helper()
	handler := NewScheduleHandler(db.New(dbtx), nil)

	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Put("/clusters/:cluster_id/schedules/:id",
		withRequestParams(t, scheduleUpdateMirror(t), []string{"cluster_id", "id"}, handler.Update))
	app.Delete("/clusters/:cluster_id/schedules/:id",
		withRequestParams(t, schedulePathMirror(t), []string{"cluster_id", "id"}, handler.Delete))
	return app
}

// scheduleUpdateMirror and schedulePathMirror restate the schemas
// registry_schedules.go declares, for the reason withRequestParams' own doc
// comment gives: package api imports this package, not the other way round.
func schedulePathMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"cluster_id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"id":         {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
	})
}

func scheduleUpdateMirror(t *testing.T) apischema.Properties {
	t.Helper()
	props := schedulePathMirror(t)
	props["schedule"] = apischema.Property{Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(256)}
	props["params"] = apischema.Property{Type: apischema.Object, Optional: true}
	props["enabled"] = apischema.Property{Type: apischema.Boolean, Optional: true, Default: false}
	return compiledMirror(t, props)
}

// TestScheduleWritesRefuseAnotherClustersTask is the regression test for the
// hole this change closes.
//
// The "wrong cluster" rows are the vulnerability: before the fix they returned
// 200 and the statement went through with no cluster predicate at all. They now
// answer 404 — the SAME answer as a task that does not exist, which is what
// stops the endpoint confirming that some other cluster owns that uuid.
func TestScheduleWritesRefuseAnotherClustersTask(t *testing.T) {
	pathCluster := uuid.New()
	otherCluster := uuid.New()
	taskID := uuid.New()

	const validBody = `{"schedule":"0 3 * * *","enabled":true}`

	tests := []struct {
		name        string
		method      string
		body        string
		taskCluster uuid.UUID
		found       bool
		wantStatus  int
		wantWrite   bool
	}{
		{
			name:   "update: a task in the path's cluster proceeds",
			method: http.MethodPut, body: validBody,
			taskCluster: pathCluster, found: true,
			wantStatus: http.StatusOK, wantWrite: true,
		},
		{
			name:   "update: a task in ANOTHER cluster is refused before the write",
			method: http.MethodPut, body: validBody,
			taskCluster: otherCluster, found: true,
			wantStatus: http.StatusNotFound, wantWrite: false,
		},
		{
			name:   "update: a task that does not exist answers the same 404",
			method: http.MethodPut, body: validBody,
			found:      false,
			wantStatus: http.StatusNotFound, wantWrite: false,
		},
		{
			name:        "delete: a task in the path's cluster proceeds",
			method:      http.MethodDelete,
			taskCluster: pathCluster, found: true,
			wantStatus: http.StatusOK, wantWrite: true,
		},
		{
			name:        "delete: a task in ANOTHER cluster is refused before the write",
			method:      http.MethodDelete,
			taskCluster: otherCluster, found: true,
			wantStatus: http.StatusNotFound, wantWrite: false,
		},
		{
			name:       "delete: a task that does not exist answers the same 404",
			method:     http.MethodDelete,
			found:      false,
			wantStatus: http.StatusNotFound, wantWrite: false,
		},
	}

	writeFragment := map[string]string{
		http.MethodPut:    "UPDATE scheduled_tasks",
		http.MethodDelete: "DELETE FROM scheduled_tasks",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbtx := &scheduleDBTX{
				task: db.ScheduledTask{
					ID: taskID, ClusterID: tt.taskCluster,
					ResourceType: "vm", ResourceID: "101", Node: "pve-01",
					Action: "snapshot", Schedule: "0 3 * * *",
					Params: json.RawMessage(`{}`),
				},
				found:        tt.found,
				rowsAffected: 1,
			}
			app := newScheduleScopeApp(t, dbtx)

			target := "/clusters/" + pathCluster.String() + "/schedules/" + taskID.String()
			var req *http.Request
			if tt.body == "" {
				req = httptest.NewRequest(tt.method, target, nil)
			} else {
				req = httptest.NewRequest(tt.method, target, strings.NewReader(tt.body))
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", resp.StatusCode, tt.wantStatus, body)
			}
			if got := dbtx.ran(writeFragment[tt.method]); got != tt.wantWrite {
				t.Errorf("the write statement ran = %v, want %v — a refused request must not reach it", got, tt.wantWrite)
			}
			if tt.wantStatus == http.StatusNotFound && !strings.Contains(string(body), "Schedule not found") {
				t.Errorf("body = %s, want the same \"Schedule not found\" both branches give — a distinct "+
					"message for \"exists but not yours\" is an existence oracle", body)
			}
		})
	}
}

// TestScheduleWriteScopesTheStatementItself covers the SECOND layer on its own.
//
// Even with the handler's comparison satisfied, the UPDATE and DELETE carry a
// cluster_id predicate — so a row that moved between the read and the write
// matches nothing. The handler has to notice that rather than report success for
// a row it never touched, which is why the queries are :execrows.
func TestScheduleWriteScopesTheStatementItself(t *testing.T) {
	cluster := uuid.New()
	taskID := uuid.New()

	for _, tt := range []struct {
		name   string
		method string
		body   string
	}{
		{"update", http.MethodPut, `{"schedule":"0 3 * * *","enabled":true}`},
		{"delete", http.MethodDelete, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dbtx := &scheduleDBTX{
				task: db.ScheduledTask{
					ID: taskID, ClusterID: cluster,
					ResourceType: "vm", ResourceID: "101", Node: "pve-01",
					Action: "snapshot", Schedule: "0 3 * * *",
					Params: json.RawMessage(`{}`),
				},
				found: true,
				// The statement matched nothing: the predicate refused it even
				// though the handler's read had said yes.
				rowsAffected: 0,
			}
			app := newScheduleScopeApp(t, dbtx)

			target := "/clusters/" + cluster.String() + "/schedules/" + taskID.String()
			var req *http.Request
			if tt.body == "" {
				req = httptest.NewRequest(tt.method, target, nil)
			} else {
				req = httptest.NewRequest(tt.method, target, strings.NewReader(tt.body))
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 — a zero-row write must not report success (body: %s)",
					resp.StatusCode, body)
			}
		})
	}
}
