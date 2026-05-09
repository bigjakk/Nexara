package handlers

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
)

// stubEngine satisfies permissionEngine for tests. It grants every
// permission to callers tagged with role="admin" via c.Locals("role")
// and denies everyone else. This lets handler tests exercise the same
// permission gates that production code runs without spinning up
// Postgres + Redis to back a real *auth.RBACEngine.
//
// The stub never appears in production: the only path that installs
// it is installStubEngineMiddleware, which lives in this _test.go file.
type stubEngine struct {
	role string
}

func (s *stubEngine) HasPermission(_ context.Context, _ uuid.UUID, _, _, _ string, _ uuid.UUID) (bool, error) {
	return s.role == "admin", nil
}

func (s *stubEngine) HasGlobalPermission(_ context.Context, _ uuid.UUID, _, _ string) (bool, error) {
	return s.role == "admin", nil
}

func (s *stubEngine) LoadUserPermissions(_ context.Context, _ uuid.UUID) (*auth.UserPermissions, error) {
	if s.role != "admin" {
		return &auth.UserPermissions{}, nil
	}
	return &auth.UserPermissions{
		Permissions: []auth.ScopedPermission{
			{Action: "view", Resource: "cluster", ScopeType: "global"},
			{Action: "manage", Resource: "cluster", ScopeType: "global"},
			{Action: "view", Resource: "pbs_server", ScopeType: "global"},
			{Action: "manage", Resource: "pbs_server", ScopeType: "global"},
		},
	}, nil
}

// installStubEngineMiddleware wires the stub onto each request based on
// the role + user_id locals an upstream test middleware has already set
// (typically from an X-Test-Role header). Calls without a user_id pass
// through unchanged so unauthenticated paths still hit a 500 from
// engineFromContext, matching production behaviour.
func installStubEngineMiddleware(app *fiber.App) {
	app.Use(func(c *fiber.Ctx) error {
		if uid, _ := c.Locals("user_id").(uuid.UUID); uid != uuid.Nil {
			role, _ := c.Locals("role").(string)
			c.Locals("rbac_engine", &stubEngine{role: role})
		}
		return c.Next()
	})
}
