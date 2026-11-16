package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestGuestSnapshotListTreatsAnEmptyClusterFilterAsNone: the declaration lets ?cluster_id=
// through empty, so the handler must not parse it (a 500 on a request the route has always
// served). The stub engine grants no guest permission, so the answer is the empty listing
// without a database; a non-empty filter is parsed and refused (403), the control that the
// filter is read at all.
func TestGuestSnapshotListTreatsAnEmptyClusterFilterAsNone(t *testing.T) {
	handler := NewGuestSnapshotHandler(db.New(&pbsUPIDDBTX{}), "", nil)
	mirror := compiledMirror(t, apischema.Properties{
		"filter_cluster_id": {
			Type:     apischema.String,
			Alias:    "cluster_id",
			Optional: true,
			Pattern:  apischema.Rule("uuid-or-empty"),
		},
	})

	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uuid.New())
		c.Locals("role", "viewer")
		return c.Next()
	})
	installStubEngineMiddleware(app)
	app.Get("/guest-snapshots", withRequestParams(t, mirror, nil, handler.List))

	for _, tt := range []struct {
		query string
		want  int
	}{
		{"", http.StatusOK},
		{"?cluster_id=", http.StatusOK},
		{"?cluster_id=" + uuid.NewString(), http.StatusForbidden},
	} {
		t.Run(tt.query, func(t *testing.T) {
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/guest-snapshots"+tt.query, nil))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.want {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want %d (body: %s)", resp.StatusCode, tt.want, body)
			}
		})
	}
}
