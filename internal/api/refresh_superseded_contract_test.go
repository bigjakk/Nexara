package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// TestRefreshSupersededCodeIsDocumentedWhereItIsPromised keeps the places that
// TELL a client about the refresh_superseded code in step with the handler that
// sends it: the /auth/refresh declaration the in-app API docs are rendered from,
// and docs/api-reference.md. A client author reads one of those, and a code that
// was renamed in the handler only would leave them writing against a response
// that no longer exists. (The client's own source is checked by
// TestGuard_TheSPAMatchesTheSupersededContract in internal/api/handlers.)
func TestRefreshSupersededCodeIsDocumentedWhereItIsPromised(t *testing.T) {
	code := handlers.RefreshSupersededCode

	reg := NewRegistry()
	registerAuthEndpoints(reg, handlers.NewAuthHandler(nil, nil, nil, nil, nil, nil))
	var description string
	for _, e := range reg.Endpoints() {
		if e.Method == fiber.MethodPost && e.Path == authScope+"/refresh" {
			description = e.Description
		}
	}
	if description == "" {
		t.Fatal("the registry declares no description for POST /auth/refresh")
	}
	if !strings.Contains(description, code) {
		t.Errorf("the /auth/refresh description does not name the error code %q a concurrent refresh is answered with: %q",
			code, description)
	}

	reference, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}
	if !strings.Contains(string(reference), "`"+code+"`") {
		t.Errorf("%s does not name the error code `%s` a concurrent refresh is answered with", apiReferencePath(), code)
	}
}

// TestAConflictReturnedAsAnErrorLosesTheRefreshSupersededCode pins the premise
// the handler's design rests on, so that the comment in refuseStaleRefresh cannot
// go stale unnoticed. errorHandler derives the envelope's error code from the
// STATUS, and for 409 that is "conflict": a handler that returned the superseded
// answer as fiber.NewError(409, …) would send {"error":"conflict"}, and the SPA's
// exact match on the code would never fire.
//
// That is why Refresh writes the answer itself. If errorHandler ever learns to
// carry a handler's own code, this test fails and the workaround can be retired
// on purpose rather than left behind.
func TestAConflictReturnedAsAnErrorLosesTheRefreshSupersededCode(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Post("/conflict", func(c fiber.Ctx) error {
		return fiber.NewError(fiber.StatusConflict, "This session was refreshed by another request a moment ago")
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/conflict", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if strings.Contains(string(body), handlers.RefreshSupersededCode) {
		t.Errorf("errorHandler now carries a handler's own code (%s); Refresh no longer needs to write its own "+
			"envelope — retire the workaround on purpose", body)
	}
	if !strings.Contains(string(body), `"error":"conflict"`) {
		t.Errorf("a 409 through errorHandler no longer reads {\"error\":\"conflict\"}: %s", body)
	}
}
