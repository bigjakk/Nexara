package handlers

import (
	"context"
	"fmt"
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
// arguments the insert was handed, and holds every node as a member — or, with
// members set, those alone. onRecord answers TaskUPIDOnRecord, and
// insertConflicts makes the insert find its UPID taken. With task set it is
// that task's row too: GetTaskByUpid answers it, and UpdateTaskHistory counts
// its writes and updates one row. audits counts the audit rows written.
type taskInsertDBTX struct {
	args            []any
	task            *db.TaskHistory
	updates         int
	members         []string
	onRecord        bool
	insertConflicts bool
	audits          int
}

func (d *taskInsertDBTX) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if d.task != nil && strings.Contains(sql, "-- name: UpdateTaskHistory :execrows") {
		d.updates++
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	if strings.Contains(sql, "-- name: InsertAuditLog :exec") {
		d.audits++
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.CommandTag{}, errCaptured
}

func (*taskInsertDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errCaptured
}

func (d *taskInsertDBTX) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if d.task != nil && strings.Contains(sql, "-- name: GetTaskByUpid :one") {
		return replayRow{row: *d.task}
	}
	if strings.Contains(sql, "-- name: GetNodeByClusterAndName :one") {
		// The node is one of the cluster's; Create checks before it inserts.
		if d.members != nil && !slices.Contains(d.members, fmt.Sprint(args[1])) {
			return failRow{err: pgx.ErrNoRows}
		}
		return replayRow{row: db.Node{Name: "pve-01"}}
	}
	if strings.Contains(sql, "-- name: TaskUPIDOnRecord :one") {
		return replayRow{row: struct{ OnRecord bool }{d.onRecord}}
	}
	if !strings.Contains(sql, "-- name: InsertTaskHistory :one") {
		return failRow{err: errCaptured}
	}
	if d.insertConflicts {
		return failRow{err: pgx.ErrNoRows}
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
		"status":          {Type: apischema.String, Optional: true, Default: "running", Enum: []string{"", "running"}},
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
		{"set", `,"status":"running"`, "running"},
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

// A task is filed once, running, under a node of the body's cluster — the
// UPID's own, whether the body names one or not — and the filing is audited.
func TestTaskCreateRefusesWhatIsNotANewRunningTaskOfTheCluster(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	mirror := compiledMirror(t, apischema.Properties{
		"task_cluster_id": {Type: apischema.String, Alias: "cluster_id", Format: "uuid"},
		"upid":            {Type: apischema.String},
		"description":     {Type: apischema.String, Optional: true},
		"status":          {Type: apischema.String, Optional: true},
		"node":            {Type: apischema.String, Optional: true},
		"task_type":       {Type: apischema.String, Optional: true},
	})
	for _, tt := range []struct {
		name   string
		body   string
		dbtx   taskInsertDBTX
		want   int
		audits int
	}{
		{"a new running task", `,"node":"pve-01"`, taskInsertDBTX{}, http.StatusCreated, 1},
		{"a status but running", `,"status":"failed"`, taskInsertDBTX{}, http.StatusBadRequest, 0},
		{"a UPID on record", ``, taskInsertDBTX{onRecord: true}, http.StatusConflict, 0},
		{"a UPID already filed", ``, taskInsertDBTX{insertConflicts: true}, http.StatusConflict, 0},
		{"another cluster's node, the body naming none", ``, taskInsertDBTX{members: []string{"pve-02"}}, http.StatusNotFound, 0},
	} {
		dbtx := tt.dbtx
		handler := NewTaskHandler(db.New(&dbtx), nil, 0)
		app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
		app.Use(func(c fiber.Ctx) error {
			c.Locals("user_id", uuid.New())
			c.Locals("rbac_engine", clusterGrantEngine{"manage", "task", clusterID})
			return c.Next()
		})
		app.Post("/tasks", withRequestParams(t, mirror, nil, handler.Create))
		req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"cluster_id":"`+clusterID.String()+
			`","upid":"UPID:pve-01:0000A:0001:66000001:qmcreate:105:root@pam:"`+tt.body+`}`))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tt.want || dbtx.audits != tt.audits {
			t.Errorf("%s: status %d, %d audit rows; want %d, %d", tt.name, resp.StatusCode, dbtx.audits, tt.want, tt.audits)
		}
	}
}

// A finished task's outcome, and a task the collector recorded from Proxmox,
// are the collector's: a guest's History starts where its lifecycle tasks say,
// so PUT /api/v1/tasks/:upid may rewrite only a running task Nexara recorded.
func TestTaskUpdateWritesOnlyARunningNexaraTask(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	mirror := compiledMirror(t, apischema.Properties{
		"upid":        {Type: apischema.String, MaxLength: apischema.Ptr(512)},
		"status":      {Type: apischema.String, Optional: true},
		"exit_status": {Type: apischema.String, Optional: true},
		"progress":    {Type: apischema.Number, Optional: true},
		"finished_at": {Type: apischema.String, Optional: true},
	})
	for _, tt := range []struct {
		status, source string
		want           int
	}{
		{"running", "nexara", http.StatusOK},
		{"completed", "nexara", http.StatusConflict},
		{"running", "proxmox", http.StatusConflict},
	} {
		dbtx := &taskInsertDBTX{task: &db.TaskHistory{ClusterID: clusterID, Status: tt.status, Source: tt.source}}
		handler := NewTaskHandler(db.New(dbtx), nil, 0)
		app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
		app.Use(func(c fiber.Ctx) error {
			c.Locals("user_id", uuid.New())
			c.Locals("rbac_engine", clusterGrantEngine{"manage", "task", clusterID})
			return c.Next()
		})
		app.Put("/tasks/:upid", withRequestParams(t, mirror, []string{"upid"}, handler.Update))
		req := httptest.NewRequest(http.MethodPut, "/tasks/UPID:pve-01:0000A:qmcreate:100:root@pam:",
			strings.NewReader(`{"status":"failed"}`))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tt.want || (dbtx.updates == 1) != (tt.want == http.StatusOK) {
			t.Errorf("a %s %s task: status %d after %d writes, want %d", tt.status, tt.source, resp.StatusCode, dbtx.updates, tt.want)
		}
	}
}
