package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/proxdash/proxdash/internal/auth"
)

// requirePerm checks that the authenticated user has the given global permission.
// Falls back to legacy role check if RBAC engine is not available.
func requirePerm(c *fiber.Ctx, action, resource string) error {
	// Try RBAC engine first
	rbac, _ := c.Locals("rbac_engine").(*auth.RBACEngine)
	userID, _ := c.Locals("user_id").(uuid.UUID)

	if rbac != nil && userID != uuid.Nil {
		ok, err := rbac.HasGlobalPermission(c.Context(), userID, action, resource)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Permission check failed")
		}
		if ok {
			return nil
		}
		return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}

	// Fallback to legacy role check
	return requireAdmin(c)
}

// requirePermScoped checks that the authenticated user has the given permission
// within a specific scope (e.g., cluster).
func requirePermScoped(c *fiber.Ctx, action, resource, scopeType string, scopeID uuid.UUID) error {
	rbac, _ := c.Locals("rbac_engine").(*auth.RBACEngine)
	userID, _ := c.Locals("user_id").(uuid.UUID)

	if rbac != nil && userID != uuid.Nil {
		ok, err := rbac.HasPermission(c.Context(), userID, action, resource, scopeType, scopeID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Permission check failed")
		}
		if ok {
			return nil
		}
		return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}

	// Fallback to legacy role check
	return requireAdmin(c)
}

// clusterIDFromParam extracts and parses the cluster_id URL parameter.
func clusterIDFromParam(c *fiber.Ctx) (uuid.UUID, error) {
	raw := c.Params("cluster_id")
	if raw == "" {
		raw = c.Params("id")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	return id, nil
}
