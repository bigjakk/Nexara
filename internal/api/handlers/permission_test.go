package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// TestRequireClusterPerm_LegacyAdmin covers the fallback path where no RBAC
// engine is wired (test fixtures, mis-bootstrapped servers): admin passes,
// any other role is denied.
//
// The RBAC-engine code path is exercised end-to-end by the integration suite
// because the engine reads from Postgres and Redis; mocking it would require
// introducing an interface seam that the rest of the codebase doesn't need.
func TestRequireClusterPerm_LegacyAdmin(t *testing.T) {
	app := fiber.New()
	app.Get("/admin", func(c *fiber.Ctx) error {
		c.Locals("role", "admin")
		if err := requireClusterPerm(c, "manage", "cluster", uuid.New()); err != nil {
			return err
		}
		return c.SendStatus(http.StatusOK)
	})
	app.Get("/operator", func(c *fiber.Ctx) error {
		c.Locals("role", "operator")
		if err := requireClusterPerm(c, "manage", "cluster", uuid.New()); err != nil {
			return err
		}
		return c.SendStatus(http.StatusOK)
	})

	tests := []struct {
		name     string
		path     string
		wantCode int
	}{
		{"admin allowed", "/admin", http.StatusOK},
		{"non-admin denied", "/operator", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
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

// TestAccessibleClusters_LegacyFallback verifies that without an RBAC engine,
// admin users get HasGlobal=true and non-admins get an empty access set.
func TestAccessibleClusters_LegacyFallback(t *testing.T) {
	app := fiber.New()

	app.Get("/probe-admin", func(c *fiber.Ctx) error {
		c.Locals("role", "admin")
		access, err := accessibleClusters(c, "view", "cluster")
		if err != nil {
			return err
		}
		if !access.HasGlobal {
			t.Error("expected HasGlobal=true for admin in legacy fallback")
		}
		return c.SendStatus(http.StatusOK)
	})

	app.Get("/probe-non-admin", func(c *fiber.Ctx) error {
		c.Locals("role", "operator")
		access, err := accessibleClusters(c, "view", "cluster")
		if err != nil {
			return err
		}
		if access.HasGlobal {
			t.Error("expected HasGlobal=false for non-admin in legacy fallback")
		}
		if len(access.Allowed) != 0 {
			t.Errorf("expected empty Allowed set, got %v", access.Allowed)
		}
		return c.SendStatus(http.StatusOK)
	})

	for _, path := range []string{"/probe-admin", "/probe-non-admin"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
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
