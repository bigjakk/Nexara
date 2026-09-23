package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// taskInsertDBTX answers InsertTaskHistory with a row and records the
// arguments the insert was handed.
type taskInsertDBTX struct {
	args []any
}

func (*taskInsertDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errCaptured
}

func (*taskInsertDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errCaptured
}

func (d *taskInsertDBTX) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if !strings.Contains(sql, "-- name: InsertTaskHistory :one") {
		return failRow{err: errCaptured}
	}
	d.args = args
	return replayRow{row: db.TaskHistory{ID: uuid.New()}}
}

// TestTaskCreateFilesAnEmptyStatusAsRunning drives the real Create with the
// status omitted, sent EMPTY and set, and checks what reaches the insert.
//
// Before the registry the handler substituted "running" for an empty status
// as well as an absent one. The declaration's Default covers the absent case,
// but a Default never applies to a key the caller sent, so an explicit "" has
// to be substituted by the handler — or it is written into the row, which
// then carries no state the listing's status filter can match.
//
// Every other string the insert takes is sent non-empty, so "no empty string
// reached the insert" can only be about the status.
func TestTaskCreateFilesAnEmptyStatusAsRunning(t *testing.T) {
	clusterID := uuid.New()
	mirror := compiledMirror(t, apischema.Properties{
		"task_cluster_id": {Type: apischema.String, Alias: "cluster_id", Format: "uuid"},
		"upid":            {Type: apischema.String, MaxLength: apischema.Ptr(512)},
		"description":     {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(512)},
		"status": {Type: apischema.String, Optional: true, Default: "running",
			Enum: []string{"", "running", "completed", "failed", "stopped"}},
		"node": {Type: apischema.String, Optional: true, Pattern: apischema.Rule("node-name-or-empty"),
			MaxLength: apischema.Ptr(63)},
		"task_type": {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(128)},
	})

	for _, tt := range []struct {
		name string
		// status is the JSON member the body carries for it, if any.
		status string
		want   string
	}{
		{"omitted", ``, "running"},
		{"empty", `,"status":""`, "running"},
		{"set", `,"status":"completed"`, "completed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dbtx := &taskInsertDBTX{}
			handler := NewTaskHandler(db.New(dbtx), nil, 0)
			var seen *apischema.Params
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Use(func(c fiber.Ctx) error {
				c.Locals("user_id", uuid.New())
				c.Locals("rbac_engine", clusterGrantEngine{"manage", "task", clusterID})
				return c.Next()
			})
			app.Post("/tasks", withRequestParams(t, mirror, nil, func(c fiber.Ctx, p *apischema.Params) error {
				seen = p
				return handler.Create(c, p)
			}))

			body := `{"cluster_id":"` + clusterID.String() + `","upid":"UPID:pve-01:0000A:qmstart::root@pam:",` +
				`"description":"start guest","node":"pve-01","task_type":"qmstart"` + tt.status + `}`
			req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body))
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			respBody, _ := io.ReadAll(resp.Body)

			if seen == nil {
				t.Fatalf("precondition: the handler never ran (%d %s)", resp.StatusCode, respBody)
			}
			if supplied := seen.Has("status"); supplied != (tt.status != "") {
				t.Fatalf("precondition: status reached the handler as supplied=%v, want %v", supplied, tt.status != "")
			}
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("status = %d (%s), want 201", resp.StatusCode, respBody)
			}
			if dbtx.args == nil {
				t.Fatal("the insert never ran")
			}
			if !slices.Contains(dbtx.args, any(tt.want)) || slices.Contains(dbtx.args, any("")) {
				t.Errorf("the insert was handed %v; want the state %q and no empty string", dbtx.args, tt.want)
			}
		})
	}
}
