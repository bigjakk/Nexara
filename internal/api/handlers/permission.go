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

// hasClusterPerm reports whether the current user holds a specific cluster-scoped
// permission, without turning absence into an error. For handlers that must branch on a
// fine-grained permission after a coarse gate — e.g. the storage upload endpoint, whose
// content type is only known once the multipart stream is parsed, so it accepts iso/vztmpl
// for manage:storage but import for either manage:storage or manage:vm_import.
func hasClusterPerm(c fiber.Ctx, action, resource string, clusterID uuid.UUID) (bool, error) {
	eng, userID, ok := engineFromContext(c)
	if !ok {
		return false, fiber.NewError(fiber.StatusInternalServerError, "RBAC engine not configured")
	}
	allowed, err := eng.HasPermission(c.Context(), userID, action, resource, "cluster", clusterID)
	if err != nil {
		return false, fiber.NewError(fiber.StatusInternalServerError, "Permission check failed")
	}
	return allowed, nil
}

// hasGlobalPerm is requirePerm's non-erroring twin, the global-scope counterpart
// to hasClusterPerm. For handlers that must branch on a permission rather than
// gate on it — e.g. the audit list, which serves every view:audit holder but
// redacts the entries whose subject is owned by a narrower permission.
func hasGlobalPerm(c fiber.Ctx, action, resource string) (bool, error) {
	eng, userID, ok := engineFromContext(c)
	if !ok {
		return false, fiber.NewError(fiber.StatusInternalServerError, "RBAC engine not configured")
	}
	allowed, err := eng.HasGlobalPermission(c.Context(), userID, action, resource)
	if err != nil {
		return false, fiber.NewError(fiber.StatusInternalServerError, "Permission check failed")
	}
	return allowed, nil
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

// ScopedIDs returns the access set as a uuid[] SQL filter parameter: nil for
// global access, otherwise the granted cluster IDs. The nil/non-nil split is
// load-bearing — pgx sends a nil slice as SQL NULL, which the narg-style
// queries read as "no restriction", while a non-nil empty slice becomes '{}'
// and matches nothing. Skipping false map entries keeps this in exact
// agreement with PermitsCluster: SQL-filtered counts (which no per-row guard
// can re-check) and per-row checks must derive from the same set. Order is
// unspecified (map iteration).
func (a clusterAccess) ScopedIDs() []uuid.UUID {
	if a.HasGlobal {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(a.Allowed))
	for id, allowed := range a.Allowed {
		if allowed {
			ids = append(ids, id)
		}
	}
	return ids
}

// clusterScopeFilter pairs ScopedIDs with the question every list endpoint
// that uses it then asks: is any row reachable at all?
//
// ids is the uuid[] SQL filter — nil for global access (no restriction), the
// granted set for a scoped caller, '{}' for a caller with no grant anywhere.
// query is false only in that last case, and it is advice rather than a guard:
// '{}' already matches nothing, so a caller that ignores it still reads
// nothing. It exists to skip round-trips that could not return a row.
//
// One definition, because the rule is easy to restate slightly differently in
// each handler and the difference is a cross-cluster leak. Where a listing has
// a companion count, stamp the same ids on both — a Total computed under a
// wider scope than the rows is a leak no per-row guard can repair.
//
// On a NULLABLE cluster_id column, `cluster_id = ANY(ids)` evaluates to NULL
// for the null rows, so a scoped caller never matches one. That is deliberate
// wherever a NULL cluster means "global entry, global permission required"
// (audit_log, alert_rules, alert_history) and matches the per-row guards. Check
// it against the table before reusing this on a new one.
// query is derived from the ids themselves rather than from len(Allowed), so
// the two cannot disagree: ScopedIDs skips explicit-false map entries, and an
// access set holding only those would otherwise report "worth querying" while
// filtering to '{}'.
func clusterScopeFilter(access clusterAccess) (ids []uuid.UUID, query bool) {
	ids = access.ScopedIDs()
	return ids, access.HasGlobal || len(ids) > 0
}

// accessibleClusters returns the set of cluster IDs the user can perform
// (action, resource) against, used to filter top-level list endpoints
// (e.g. /clusters, /search, /migrations) to entries the user is allowed to see.
//
// Callers pass "view" for read filters and "execute" for Veeam job control,
// which resolves the same per-cluster grant set through a different verb.
//
// On failure the returned error is always a *fiber.Error so callers can
// `return err` directly and get the right HTTP status. Don't wrap with
// fmt.Errorf — that would hide the status code from Fiber's ErrorHandler.
func accessibleClusters(c fiber.Ctx, action, resource string) (clusterAccess, error) {
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
//
// requireAnyGlobalManage used to live above it, for the one endpoint shared by
// several add-flows — POST /api/v1/clusters/fetch-fingerprint. That is now the
// declared Permissions.Alternatives on the route (internal/api/registry_clusters.go),
// which RequireAnyPermission mounts as middleware with the same "any of these"
// semantics and a refusal message that names the alternatives; keeping a second
// implementation with no caller would be a second place for the rule to drift.
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
