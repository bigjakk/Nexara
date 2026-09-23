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

// TestGuestSnapshotListTreatsAnEmptyClusterFilterAsNone drives the real List
// handler with ?cluster_id= — the "no filter" spelling the listing has always
// taken — and checks it is not parsed as a uuid.
//
// The declaration accepts the empty value now, which makes this the handler's
// job to get right: once the declaration began letting the empty value
// through, a handler still parsing it would have sent "" to parseParamUUID and
// answered a request the route has always served with a 500. The stub engine grants
// no guest permission, so the handler returns its empty listing without a
// database; a non-empty filter, by contrast, is parsed and refused (403),
// which is the control that shows the filter is read at all.
func TestGuestSnapshotListTreatsAnEmptyClusterFilterAsNone(t *testing.T) {
	handler := NewGuestSnapshotHandler(db.New(pbsUPIDDBTX{}), "", nil)
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
