package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestTrackTaskKeepsNoURLCredentials follows a URL sent with credentials and a
// signed query (a download-url source) to every copy the audit path keeps of
// it: the row, its syslog line and the task's description each keep "(set)" in
// their place.
func TestTrackTaskKeepsNoURLCredentials(t *testing.T) {
	t.Parallel()
	const probe = "PROBE-URL-PASSWORD-NOT-A-REAL-VALUE"
	source := "https://user01:" + probe + "@mirror.example.com/x.iso?X-Amz-Signature=" + probe
	pub, read := newCollector(t)
	capture := &captureDBTX{}
	queries := db.New(capture)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Post("/", func(c fiber.Ctx) error {
		c.Locals("user_id", authEventActor)
		TrackTask(c, queries, pub, TrackTaskParams{
			ClusterID: uuid.New(), Node: "pve-01", ResourceType: "storage", ResourceID: "store01",
			Action: "download_url", UPID: "UPID:pve-01:00001234:00005678:65000000:download:x.iso:user01@pve:",
			Description: "Download " + source, Extra: map[string]any{"url": source},
		})
		return c.SendStatus(http.StatusNoContent)
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()

	const kept = "https://(set)@mirror.example.com/x.iso?(set)"
	rows := capture.auditInserts(t)
	if len(rows) != 1 || !strings.Contains(string(rows[0].Details), `"url":"`+kept+`"`) {
		t.Fatalf("audit rows %+v, want one whose url is %s", rows, kept)
	}
	var description string
	for _, q := range capture.calls {
		if strings.Contains(q.sql, "INSERT INTO task_history") {
			description = argAs[string](t, q.args, 3)
		}
	}
	if description != "Download "+kept {
		t.Errorf("task description %q, want %q", description, "Download "+kept)
	}
	if line := read(true); strings.Contains(line, probe) || !strings.Contains(line, kept) {
		t.Errorf("syslog line %s, want the url as %s", line, kept)
	}
}
