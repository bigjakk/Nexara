package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// favoritesScope is the collection all three routes hang off.
const favoritesScope = pathPrefix + "favorites"

// favoriteTargetParams are the three fields that name one starred resource.
// The add sends them in a body and the remove in a query string, but they are
// the same three values and are declared once so the two cannot drift on what a
// reference is.
//
// The cluster is named favorite_cluster_id with "cluster_id" as an ALIAS, for
// the reason consoleTokenParams (registry_auth.go) gives: checkPathParams
// refuses a parameter NAMED cluster_id that resolves to anything but the path,
// because clusterIDFromParam reads that name to decide which cluster a
// permission gate authorizes. Neither of these routes runs such a gate — the
// add is Deferred and the remove is SelfService — but the refusal is
// deliberately about the NAME rather than about whether today's shape happens
// to make it safe. The alias is what keeps the sidebar's star button working:
// it sends {"cluster_id": …}.
//
// `ref` is OPTIONAL here and validated in the handler, and that is not laziness:
// what a valid ref is depends on resource_type — empty for a cluster, a node
// name for a node, a decimal VMID for a guest — which is a cross-field rule no
// parameter schema can express. parseFavoriteTarget owns it, and owns it for
// both routes at once.
func favoriteTargetParams() apischema.Properties {
	return apischema.Properties{
		"resource_type": {
			Type:        apischema.String,
			Enum:        []string{"cluster", "node", "vm"},
			Typetext:    "<cluster|node|vm>",
			Description: "What kind of thing is starred. It also selects which permission the add is gated on.",
		},
		"favorite_cluster_id": {
			Type:     apischema.String,
			Alias:    "cluster_id",
			Format:   "uuid",
			Typetext: "<uuid>",
			Description: "Cluster the starred resource belongs to. Also accepted as \"cluster_id\", which " +
				"is what the sidebar sends.",
		},
		"ref": {
			Type:      apischema.String,
			Optional:  true,
			MaxLength: apischema.Ptr(255),
			Typetext:  "<node name|vmid>",
			Description: "Identifies the resource within its cluster: omitted or empty for a cluster " +
				"(cluster_id already names it), the node name for a node, the decimal VMID for a guest. " +
				"A guest VMID is normalised, so \"0101\" and \"101\" star the same guest once.",
		},
	}
}

// favoriteAddReason is the Deferred justification for POST /api/v1/favorites.
//
// Both halves of the permission come from the request body: the RESOURCE is
// whatever resource_type names, and the CLUSTER is the body's cluster_id,
// because the path names none. Middleware runs before the body is read, so
// neither is knowable to it.
//
// The gate is view on the target's OWN resource type, and that is what makes
// starring safe to offer: without it a caller who cannot see a cluster would
// get a foreign-key 500 for a real id and a quiet success for an invented one,
// which is an existence oracle over every cluster in the install.
const favoriteAddReason = "the resource is chosen by the request BODY — resource_type selects view:cluster, " +
	"view:node or view:vm — and the cluster comes from the body as well, since the path names none; " +
	"middleware runs before either is readable (requireClusterPerm in AddFavorite)"

// registerFavoritesEndpoints declares FavoritesHandler's three routes, and
// they take three different shapes for three different reasons.
//
// The LISTING is Advisory: it spans every cluster and filters per resource
// type, so there is no single cluster or single permission a gate could
// resolve. The ADD is Deferred: both halves of its permission are in the body.
// The REMOVE is SelfService, and deliberately ungated even though its sibling
// is not — the delete is keyed by the authenticated user id, so it can only
// ever reach the caller's own row, and requiring view access to UNSTAR would
// strand rows: someone who loses a grant would be left with a favorite they can
// neither see nor delete.
func registerFavoritesEndpoints(reg *Registry, h *handlers.FavoritesHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   favoritesScope,
		Description: "List the caller's own starred clusters, nodes and guests across every cluster, " +
			"each resolved to what the sidebar needs to draw and navigate to it. Each resource type is " +
			"filtered against its own grant — view:cluster for a cluster, view:node for a node, view:vm " +
			"for a guest — so a favorite the caller may no longer see is dropped from the listing but " +
			"kept in the table, and restoring the grant restores the shortcut.",
		Group: "Favorites",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check: Check{Action: "view", Resource: "cluster", Scope: ScopeCluster},
			Reason: "three accessibleClusters reads — (\"view\",\"cluster\"), (\"view\",\"node\") and " +
				"(\"view\",\"vm\") — build one scope per resource type, and clusterScopeFilter turns each " +
				"into the SQL scope its own query runs under; the listing spans every cluster and every " +
				"type, so there is neither a single cluster nor a single permission a gate could resolve",
		}},
		Parameters: apischema.Properties{},
		Handler:    h.ListFavorites,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   favoritesScope,
		Description: "Star a cluster, node or guest for the caller. Requires view on the target's own " +
			"resource type in its cluster — view:cluster, view:node or view:vm, chosen by resource_type — " +
			"and the target has to exist, which is also what bounds the table: every row then costs a " +
			"distinct real resource the caller can already see.",
		Group:       "Favorites",
		Permissions: Permissions{Deferred: favoriteAddReason},
		Parameters:  favoriteTargetParams(),
		Handler:     h.AddFavorite,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   favoritesScope,
		Description: "Unstar one of the caller's own favorites. Idempotent — removing one that is not " +
			"there succeeds, which is what a star toggle wants when two clicks race. The target is named " +
			"by query parameters rather than by a path, so a node name never has to survive URL path " +
			"segmentation.",
		Group:       "Favorites",
		Permissions: Permissions{SelfService: "removes one of the caller's own favorites; the DELETE is keyed by the authenticated user id, and gating it on view access would strand rows a user can no longer see"},
		Parameters:  favoriteTargetParams(),
		Handler:     h.RemoveFavorite,
	})
}
