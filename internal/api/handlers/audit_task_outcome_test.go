package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The audit page carries each task entry's outcome, read for the page's own
// rows after the page is listed; an entry with no task row carries none.
func TestAuditListCarriesTheTaskOutcome(t *testing.T) {
	t.Parallel()
	task, plain := uuid.New(), uuid.New()
	dbtx := &auditRowsDBTX{
		rowsFor: map[string][]any{
			"ListAuditLogAdvanced": {
				db.ListAuditLogAdvancedRow{ID: task, Details: []byte(`{"upid":"UPID:pve-01:00000001:00000001:65000000:qmstart:100:alice@pve:"}`)},
				db.ListAuditLogAdvancedRow{ID: plain, Details: []byte(`{}`)},
			},
			"ListAuditTaskOutcomes": {db.ListAuditTaskOutcomesRow{ID: task, TaskStatus: "failed", TaskExitStatus: "start failed"}},
		},
		rowFor: map[string]any{"CountAuditLogAdvanced": struct{ N int64 }{2}},
	}
	handler := NewAuditHandler(db.New(dbtx), nil)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uuid.New())
		c.Locals("rbac_engine", newGrantEngine("view:audit"))
		return c.Next()
	})
	app.Get("/audit-log", withRequestParams(t, auditListMirror(t), nil, handler.List))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/audit-log", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var got struct {
		Items []struct {
			ID         uuid.UUID `json:"id"`
			TaskStatus *string   `json:"task_status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &got); err != nil || len(got.Items) != 2 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if s := got.Items[0].TaskStatus; s == nil || *s != "failed" {
		t.Errorf("task entry: task_status = %v, want failed", s)
	}
	if s := got.Items[1].TaskStatus; s != nil {
		t.Errorf("entry with no task: task_status = %q, want none", *s)
	}
}
