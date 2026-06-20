package handlers

import (
	"context"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
)

// permissionEngine captures the subset of *auth.RBACEngine that handlers
// need for gating. Carved out as an interface so tests can wire a stub
// (see permission_stub_test.go) without spinning up Postgres + Redis.
//
// Production has exactly one implementation: *auth.RBACEngine. Auth
// middleware installs it on every authenticated request via
// c.Locals("rbac_engine", *auth.RBACEngine).
type permissionEngine interface {
	HasPermission(ctx context.Context, userID uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error)
	HasGlobalPermission(ctx context.Context, userID uuid.UUID, action, resource string) (bool, error)
	LoadUserPermissions(ctx context.Context, userID uuid.UUID) (*auth.UserPermissions, error)
}

// engineFromContext fetches the engine + user_id installed by auth
// middleware. The bool reports whether both are present; absence in
// production means the request bypassed auth, which the caller turns
// into a 500 to fail loud rather than silently degrading.
func engineFromContext(c fiber.Ctx) (permissionEngine, uuid.UUID, bool) {
	eng, _ := c.Locals("rbac_engine").(permissionEngine)
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if eng == nil || userID == uuid.Nil {
		return nil, uuid.Nil, false
	}
	return eng, userID, true
}

// requirePerm gates the handler on a global-scoped permission. Use it
// for system-wide resources (users, settings, RBAC, system reports).
// For per-cluster checks prefer requireClusterPerm so a cluster-scoped
// role grant is honoured.
func requirePerm(c fiber.Ctx, action, resource string) error {
	eng, userID, ok := engineFromContext(c)
	if !ok {
		return fiber.NewError(fiber.StatusInternalServerError, "RBAC engine not configured")
	}
	allowed, err := eng.HasGlobalPermission(c.Context(), userID, action, resource)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Permission check failed")
	}
	if !allowed {
		return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}
	return nil
}

// requireClusterPerm gates the handler on a permission scoped to a
// specific cluster. Global-scoped grants cover all clusters; a user
// with only a cluster-scoped grant on a different cluster is rejected
// with 403.
func requireClusterPerm(c fiber.Ctx, action, resource string, clusterID uuid.UUID) error {
	eng, userID, ok := engineFromContext(c)
	if !ok {
		return fiber.NewError(fiber.StatusInternalServerError, "RBAC engine not configured")
	}
	allowed, err := eng.HasPermission(c.Context(), userID, action, resource, "cluster", clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Permission check failed")
	}
	if !allowed {
		return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}
	return nil
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
// All current callers pass action="view"; the parameter is kept for symmetry
// with requireClusterPerm and to support future filters keyed on a different
// action verb (e.g. listing only clusters the user can manage).
//
// On failure the returned error is always a *fiber.Error so callers can
// `return err` directly and get the right HTTP status. Don't wrap with
// fmt.Errorf — that would hide the status code from Fiber's ErrorHandler.
func accessibleClusters(c fiber.Ctx, action, resource string) (clusterAccess, error) { //nolint:unparam // action always "view" today; preserved for future filters
	eng, userID, ok := engineFromContext(c)
	if !ok {
		return clusterAccess{}, fiber.NewError(fiber.StatusInternalServerError, "RBAC engine not configured")
	}

	perms, err := eng.LoadUserPermissions(c.Context(), userID)
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
func clusterIDFromParam(c fiber.Ctx) (uuid.UUID, error) {
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
