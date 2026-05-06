package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
)

// requirePerm checks that the authenticated user has the given global permission.
// Falls back to legacy role check if RBAC engine is not available.
//
// Use this for handlers that operate on global resources (users, settings, RBAC,
// system-wide reports). For anything tied to a specific cluster, prefer
// requireClusterPerm so a user with a cluster-scoped role grant is properly
// gated on the cluster they actually have access to.
func requirePerm(c *fiber.Ctx, action, resource string) error {
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

// requireClusterPerm checks that the authenticated user has the given permission
// at the specified cluster scope. Global-scoped permissions cover all clusters,
// so an admin or any user with a global grant passes. A user with only a
// cluster-scoped grant on a different cluster is rejected with 403.
//
// Falls back to legacy admin check when the RBAC engine is unavailable —
// matches the requirePerm fallback so test fixtures and mis-bootstrapped
// servers don't silently fail open for non-admins.
func requireClusterPerm(c *fiber.Ctx, action, resource string, clusterID uuid.UUID) error {
	rbac, _ := c.Locals("rbac_engine").(*auth.RBACEngine)
	userID, _ := c.Locals("user_id").(uuid.UUID)

	if rbac != nil && userID != uuid.Nil {
		ok, err := rbac.HasPermission(c.Context(), userID, action, resource, "cluster", clusterID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Permission check failed")
		}
		if ok {
			return nil
		}
		return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}

	return requireAdmin(c)
}

// clusterAccess captures which clusters a user can act on for a given
// action+resource pair. If HasGlobal is true, the user can act on every
// cluster (and the Allowed map is empty/unused). Otherwise Allowed contains
// the specific cluster IDs the user has been granted scope on.
type clusterAccess struct {
	HasGlobal bool
	Allowed   map[uuid.UUID]bool
}

// PermitsCluster reports whether the access set permits action on the given cluster.
func (a clusterAccess) PermitsCluster(id uuid.UUID) bool {
	if a.HasGlobal {
		return true
	}
	return a.Allowed[id]
}

// accessibleClusters returns the set of cluster IDs the user can perform
// (action, resource) against, used to filter top-level list endpoints
// (e.g. /clusters, /search, /migrations) to entries the user is allowed to see.
//
// When the RBAC engine is unavailable, falls back to the legacy admin role:
// admins get HasGlobal=true, everyone else gets an empty access set.
//
// All current callers pass action="view"; the parameter is kept for symmetry
// with requireClusterPerm and to support future filters keyed on a different
// action verb (e.g. listing only clusters the user can manage).
func accessibleClusters(c *fiber.Ctx, action, resource string) (clusterAccess, error) { //nolint:unparam // action always "view" today; preserved for future filters
	rbac, _ := c.Locals("rbac_engine").(*auth.RBACEngine)
	userID, _ := c.Locals("user_id").(uuid.UUID)

	if rbac == nil || userID == uuid.Nil {
		role, _ := c.Locals("role").(string)
		return clusterAccess{HasGlobal: role == "admin"}, nil
	}

	perms, err := rbac.LoadUserPermissions(c.Context(), userID)
	if err != nil {
		return clusterAccess{}, fiber.NewError(fiber.StatusInternalServerError, "Permission check failed")
	}

	access := clusterAccess{Allowed: make(map[uuid.UUID]bool)}
	for _, p := range perms.Permissions {
		if p.Action != action || p.Resource != resource {
			continue
		}
		if p.ScopeType == "global" {
			access.HasGlobal = true
			access.Allowed = nil
			return access, nil
		}
		if p.ScopeType == "cluster" && p.ScopeID != "" {
			id, parseErr := uuid.Parse(p.ScopeID)
			if parseErr == nil {
				access.Allowed[id] = true
			}
		}
	}
	return access, nil
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
