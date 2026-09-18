package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// registerSearchEndpoints declares SearchHandler's single route.
//
// It is Advisory rather than a Check, and the distinction is the endpoint's
// whole shape: the search spans every cluster, so there is no single cluster a
// gate could resolve, and a global view:cluster Check would refuse exactly the
// cluster-scoped operators the filtering exists to serve. What keeps it safe is
// the per-row PermitsCluster call on every VM, node, storage pool and cluster
// the handler considers.
func registerSearchEndpoints(reg *Registry, h *handlers.SearchHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pathPrefix + "search",
		Description: "Search guests, nodes, storage pools and clusters by name, and guests by VMID as " +
			"well. A term shorter than two characters returns an empty list rather than everything, and " +
			"the result set stops at roughly 100 entries.",
		Group: "Settings",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check: Check{Action: "view", Resource: "cluster", Scope: ScopeCluster},
			Reason: "accessibleClusters(\"view\", \"cluster\") builds the scope and access.PermitsCluster " +
				"is applied to every candidate row — guests, nodes, storage pools and the clusters " +
				"themselves; the search spans every cluster, so there is none for a gate to resolve",
		}},
		Parameters: apischema.Properties{
			"q": {
				Type:     apischema.String,
				Optional: true,
				// Bounded rather than free: the term is compared against every
				// row in the inventory, and nothing else caps what a caller can
				// send. 256 is far above any name in Proxmox's own namespaces
				// (a node name is 63, a storage id 100).
				MaxLength: apischema.Ptr(256),
				Typetext:  "<string>",
				Description: "Search term, matched case-insensitively as a substring. Fewer than two " +
					"characters after trimming returns an empty list.",
			},
		},
		Handler: h.GlobalSearch,
	})
}
