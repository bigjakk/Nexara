package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// installTestRoleMiddleware reads X-Test-Role on every request and sets
// role + user_id locals, mirroring what production auth middleware does.
// The stub engine is then installed via installStubEngineMiddleware so
// the handlers under test go through the exact same engineFromContext
// path as production.
func installTestRoleMiddleware(app *fiber.App) {
	app.Use(func(c fiber.Ctx) error {
		if role := c.Get("X-Test-Role"); role != "" {
			c.Locals("role", role)
			c.Locals("user_id", uuid.New())
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)
}

// TestRequireClusterPerm_StubEngine covers the production flow with the
// stub permissionEngine: admin globally grants every action, non-admin
// is denied with 403.
func TestRequireClusterPerm_StubEngine(t *testing.T) {
	app := fiber.New()
	installTestRoleMiddleware(app)
	app.Get("/probe", func(c fiber.Ctx) error {
		if err := requireClusterPerm(c, "manage", "cluster", uuid.New()); err != nil {
			return err
		}
		return c.SendStatus(http.StatusOK)
	})

	tests := []struct {
		name     string
		role     string
		wantCode int
	}{
		{"admin allowed", "admin", http.StatusOK},
		{"non-admin denied", "operator", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			req.Header.Set("X-Test-Role", tt.role)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.wantCode {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantCode)
			}
		})
	}
}

// TestRequireClusterPerm_NoEngine asserts that a request reaching the
// permission check without an engine wired (no auth middleware ran)
// fails with 500 rather than silently degrading to a role check. This
// is the load-bearing test for 5.1: production must NOT fall back.
func TestRequireClusterPerm_NoEngine(t *testing.T) {
	app := fiber.New()
	app.Get("/probe", func(c fiber.Ctx) error {
		if err := requireClusterPerm(c, "manage", "cluster", uuid.New()); err != nil {
			return err
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// TestAccessibleClusters_StubEngine verifies that with the stub
// engine, admin gets HasGlobal=true and non-admin gets an empty access
// set. Mirrors the previous legacy-fallback test against the new path.
func TestAccessibleClusters_StubEngine(t *testing.T) {
	app := fiber.New()
	installTestRoleMiddleware(app)

	app.Get("/probe-admin", func(c fiber.Ctx) error {
		access, err := accessibleClusters(c, "view", "cluster")
		if err != nil {
			return err
		}
		if !access.HasGlobal {
			t.Error("expected HasGlobal=true for admin")
		}
		return c.SendStatus(http.StatusOK)
	})

	app.Get("/probe-non-admin", func(c fiber.Ctx) error {
		access, err := accessibleClusters(c, "view", "cluster")
		if err != nil {
			return err
		}
		if access.HasGlobal {
			t.Error("expected HasGlobal=false for non-admin")
		}
		if len(access.Allowed) != 0 {
			t.Errorf("expected empty Allowed set, got %v", access.Allowed)
		}
		return c.SendStatus(http.StatusOK)
	})

	cases := []struct{ path, role string }{
		{"/probe-admin", "admin"},
		{"/probe-non-admin", "operator"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("X-Test-Role", tc.role)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		_ = resp.Body.Close()
	}
}

// TestClusterAccess_PermitsCluster covers the pure-logic permit helper —
// global trumps per-cluster, and missing entries deny.
func TestClusterAccess_PermitsCluster(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()

	tests := []struct {
		name   string
		access clusterAccess
		check  uuid.UUID
		want   bool
	}{
		{
			name:   "global access permits any cluster",
			access: clusterAccess{HasGlobal: true},
			check:  clusterA,
			want:   true,
		},
		{
			name: "scoped access permits only listed clusters",
			access: clusterAccess{Allowed: map[uuid.UUID]bool{
				clusterA: true,
			}},
			check: clusterA,
			want:  true,
		},
		{
			name: "scoped access denies unlisted clusters",
			access: clusterAccess{Allowed: map[uuid.UUID]bool{
				clusterA: true,
			}},
			check: clusterB,
			want:  false,
		},
		{
			name:   "empty access denies everything",
			access: clusterAccess{},
			check:  clusterA,
			want:   false,
		},
		{
			name: "global takes precedence over Allowed map",
			access: clusterAccess{
				HasGlobal: true,
				Allowed:   map[uuid.UUID]bool{clusterA: true},
			},
			check: clusterB,
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.access.PermitsCluster(tt.check); got != tt.want {
				t.Errorf("PermitsCluster() = %v, want %v", got, tt.want)
			}
		})
	}
}
