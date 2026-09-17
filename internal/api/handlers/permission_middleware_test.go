package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
)

// A permission check has three outcomes, not two: allowed, denied, and
// could-not-look. These tests exist for the third, because that is the
// one a gate gets wrong silently — an engine that is missing or that
// cannot answer must fail CLOSED with a 500, never fall through to the
// handler and never be reported as a plain 403 that hides the outage.

// brokenEngine satisfies permissionEngine and cannot answer anything.
type brokenEngine struct{}

var errEngineDown = errors.New("permission store unreachable")

func (brokenEngine) HasPermission(context.Context, uuid.UUID, string, string, string, uuid.UUID) (bool, error) {
	return false, errEngineDown
}

func (brokenEngine) HasGlobalPermission(context.Context, uuid.UUID, string, string) (bool, error) {
	return false, errEngineDown
}

func (brokenEngine) LoadUserPermissions(context.Context, uuid.UUID) (*auth.UserPermissions, error) {
	return nil, errEngineDown
}

// probeApp mounts gate ahead of a handler that records whether it ran.
func probeApp(gate fiber.Handler, install func(fiber.Ctx)) (*fiber.App, *bool) {
	reached := false
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		if install != nil {
			install(c)
		}
		return c.Next()
	})
	app.Get("/probe/:cluster_id", gate, func(c fiber.Ctx) error {
		reached = true
		return c.SendStatus(http.StatusOK)
	})
	return app, &reached
}

func probe(t *testing.T, app *fiber.App, clusterID string) int {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/probe/"+clusterID, nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

func installAdmin(c fiber.Ctx) {
	c.Locals("user_id", uuid.New())
	c.Locals("rbac_engine", &stubEngine{role: "admin"})
}

func installBroken(c fiber.Ctx) {
	c.Locals("user_id", uuid.New())
	c.Locals("rbac_engine", brokenEngine{})
}

func TestPermissionMiddlewareFailsClosed(t *testing.T) {
	clusterID := uuid.New().String()

	gates := map[string]fiber.Handler{
		"RequirePermission":        RequirePermission("manage", "cluster"),
		"RequireClusterPermission": RequireClusterPermission("manage", "cluster"),
		"RequireAnyPermission": RequireAnyPermission(
			PermissionRef{Action: "view", Resource: "vm", ClusterScoped: true},
			PermissionRef{Action: "view", Resource: "container", ClusterScoped: true},
		),
	}

	for name, gate := range gates {
		t.Run(name+" with no engine", func(t *testing.T) {
			// Absence of the engine in production means the request
			// bypassed auth entirely. 500 is the loud answer; passing
			// through would serve the request unauthorized.
			app, reached := probeApp(gate, nil)
			if got := probe(t, app, clusterID); got != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500 when the RBAC engine is not configured", got)
			}
			if *reached {
				t.Error("the handler ran with no RBAC engine present")
			}
		})

		t.Run(name+" with an engine that cannot answer", func(t *testing.T) {
			app, reached := probeApp(gate, installBroken)
			if got := probe(t, app, clusterID); got != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500 — an engine failure is not a denial, and reporting it "+
					"as 403 would hide the outage behind a permissions complaint", got)
			}
			if *reached {
				t.Error("the handler ran despite an unanswerable permission check")
			}
		})
	}
}

func TestPermissionMiddlewareAllowsAndContinues(t *testing.T) {
	clusterID := uuid.New().String()

	for name, gate := range map[string]fiber.Handler{
		"RequirePermission":        RequirePermission("manage", "cluster"),
		"RequireClusterPermission": RequireClusterPermission("manage", "cluster"),
		"RequireAnyPermission": RequireAnyPermission(
			PermissionRef{Action: "manage", Resource: "cluster", ClusterScoped: true},
		),
	} {
		t.Run(name, func(t *testing.T) {
			app, reached := probeApp(gate, installAdmin)
			if got := probe(t, app, clusterID); got != http.StatusOK {
				t.Fatalf("status = %d, want 200", got)
			}
			if !*reached {
				t.Error("the gate allowed the request but never called c.Next()")
			}
		})
	}
}

func TestRequireClusterPermissionRejectsAMalformedClusterID(t *testing.T) {
	app, reached := probeApp(RequireClusterPermission("manage", "cluster"), installAdmin)
	if got := probe(t, app, "not-a-uuid"); got != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", got)
	}
	if *reached {
		t.Error("the handler ran with an unparseable cluster id")
	}
}

// TestRequireAnyPermissionNamesEveryAlternative keeps the 403 body
// useful: an operator building a role has to be told which grants would
// have worked, not merely that something was missing.
func TestRequireAnyPermissionNamesEveryAlternative(t *testing.T) {
	gate := RequireAnyPermission(
		PermissionRef{Action: "view", Resource: "vm", ClusterScoped: true},
		PermissionRef{Action: "view", Resource: "container", ClusterScoped: true},
	)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uuid.New())
		c.Locals("rbac_engine", &stubEngine{role: "operator"})
		return c.Next()
	})
	app.Get("/probe/:cluster_id", gate, func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/probe/"+uuid.New().String(), nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	for _, want := range []string{"view:vm", "view:container"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body = %q, want it to name %q", body, want)
		}
	}
}
