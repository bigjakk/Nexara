package handlers

import (
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// This file exposes the RBAC gates as Fiber middleware, so that a route's
// permission can be DECLARED where the route is registered instead of
// hand-placed inside the handler body.
//
// The gates themselves are unexported (requirePerm, requireClusterPerm and
// their non-erroring twins in permission.go) and stay that way: these
// constructors are thin wrappers, not a second implementation. There is
// exactly one place that consults the RBAC engine, and a route mounted
// through the registry reaches it by the same call graph the 544
// hand-placed checks do — which is what keeps rbac_route_guard_test.go's
// static walk meaningful for both styles at once.
//
// Attach the result to the ROUTE, never to a Group. Fiber v3 applies
// group middleware at match time, so it never appears in a route's
// Handlers slice and route-table introspection cannot see it — the same
// reasoning spelled out at length on clusterCreateLimiter in
// internal/api/middleware.go.

// PermissionRef names one permission requirement: an (action, resource)
// pair and the scope it is resolved in. It exists for RequireAnyPermission,
// where several requirements are alternatives to each other.
//
// ClusterScoped resolves the permission against the cluster named in the
// request path (cluster_id, or id — see clusterIDFromParam); otherwise the
// permission must be held globally.
type PermissionRef struct {
	Action        string
	Resource      string
	ClusterScoped bool
}

func (r PermissionRef) String() string {
	return r.Action + ":" + r.Resource
}

// RequirePermission gates a route on a global-scoped permission.
//
// Use it for system-wide resources (users, settings, RBAC, system
// reports). For anything that belongs to one cluster use
// RequireClusterPermission, so that a cluster-scoped role grant is
// honoured rather than silently insufficient.
func RequirePermission(action, resource string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if err := requirePerm(c, action, resource); err != nil {
			return err
		}
		return c.Next()
	}
}

// RequireClusterPermission gates a route on a permission scoped to the
// cluster named in the request path. A global grant covers every cluster;
// a grant on a different cluster is a 403.
//
// The route must carry a :cluster_id (or :id) path parameter — without one
// clusterIDFromParam answers 400 on every request, which is a gate that
// rejects everybody rather than a gate that checks anything. Register
// refuses such a declaration at startup rather than letting it ship.
func RequireClusterPermission(action, resource string) fiber.Handler {
	return func(c fiber.Ctx) error {
		clusterID, err := clusterIDFromParam(c)
		if err != nil {
			return err
		}
		if err := requireClusterPerm(c, action, resource, clusterID); err != nil {
			return err
		}
		return c.Next()
	}
}

// RequireAnyPermission passes when the caller holds ANY of refs, and is
// how "view:vm or view:container" is declared for the handful of endpoints
// that serve both guest kinds through one path.
//
// It gates on the union deliberately: the alternatives are different names
// for the same underlying object, so a caller holding either one is
// entitled to the route, and the handler narrows to the specific object
// afterwards. Do not reach for it to paper over a route that should have
// been two routes.
//
// A failure to REACH the engine (a missing engine, a Redis outage) is
// returned immediately rather than treated as "this alternative said no":
// falling through to the next ref would turn an outage into a 403 and
// hide it, and the last ref's 403 would be the only thing anyone saw.
func RequireAnyPermission(refs ...PermissionRef) fiber.Handler {
	wantsCluster := slices.ContainsFunc(refs, func(r PermissionRef) bool { return r.ClusterScoped })
	return func(c fiber.Ctx) error {
		// Resolved once, up front, and only when some ref asks for it: a
		// malformed cluster id is the caller's mistake either way, and
		// re-parsing it per ref would report it as whichever alternative
		// happened to be tried first.
		var clusterID uuid.UUID
		if wantsCluster {
			id, err := clusterIDFromParam(c)
			if err != nil {
				return err
			}
			clusterID = id
		}
		for _, ref := range refs {
			var (
				allowed bool
				err     error
			)
			if ref.ClusterScoped {
				allowed, err = hasClusterPerm(c, ref.Action, ref.Resource, clusterID)
			} else {
				allowed, err = hasGlobalPerm(c, ref.Action, ref.Resource)
			}
			if err != nil {
				return err
			}
			if allowed {
				return c.Next()
			}
		}
		return fiber.NewError(fiber.StatusForbidden,
			"Requires one of: "+strings.Join(refNames(refs), ", "))
	}
}

func refNames(refs []PermissionRef) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.String())
	}
	return names
}
