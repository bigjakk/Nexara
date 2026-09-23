package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterParams and pathPrefix — lives in registry_vms.go and registry.go.

// guestSnapshotScope is the instance-wide inventory listing.
const guestSnapshotScope = pathPrefix + "guest-snapshots"

// guestSnapshotFilterClusterParam is the optional ?cluster_id= on the central
// listing.
//
// It is declared under a name of its own with "cluster_id" as an ALIAS, for the
// reason consoleTokenParams (registry_auth.go) and veeamPlatformClusterParam
// (registry_veeam.go) are: checkPathParams refuses a parameter NAMED cluster_id
// that resolves to anything but the path, because clusterIDFromParam reads that
// name to decide which cluster a permission gate authorizes. This route runs no
// gate at all — it is Advisory, and the handler filters per row — but the
// refusal is deliberately about the NAME rather than about whether today's
// shape happens to make it safe. The alias keeps the spelling this listing has
// always read: the handler took c.Query("cluster_id") before it was declared.
// Nexara's own snapshots page sends no filter at all.
//
// The EMPTY string is accepted and means "do not filter", which is what that
// handler did (`if cid != ""`). That is why it carries the empty-or-uuid
// pattern rather than the uuid format, which rejects "" — the same call
// alertFilterClusterParam makes for the same query parameter.
var guestSnapshotFilterClusterParam = apischema.Property{
	Type:      apischema.String,
	Alias:     "cluster_id",
	Optional:  true,
	Pattern:   emptyOrUUID,
	MaxLength: apischema.Ptr(36),
	Typetext:  "<uuid>",
	Description: "Narrow the listing to one cluster; the caller must hold view:vm or view:container on it. " +
		"Empty or omitted lists every cluster the caller can see. Also accepted as \"cluster_id\".",
}

// guestSnapshotScopeReason is the Advisory justification for the listing.
//
// A gate cannot stand in for it on two counts. The listing spans every cluster,
// so there is no single cluster middleware could resolve; and the two guest
// kinds are separate grants, resolved PER ROW from the snapshot row's own
// guest_type — which is persisted on the snapshot precisely so the split still
// works for a row whose guest has since vanished.
const guestSnapshotScopeReason = "accessibleClusters(\"view\", \"vm\") and accessibleClusters(\"view\", " +
	"\"container\") build two scopes and filterGuestSnapshotRows applies the matching one per row, " +
	"chosen by the row's persisted guest_type; the listing spans every cluster, so there is none for a " +
	"gate to resolve, and the two guest kinds are separate grants"

// registerGuestSnapshotEndpoints declares GuestSnapshotHandler's two routes.
//
// They take the two different shapes this domain's split produces. The central
// LISTING is Advisory — nothing gates it, the handler filters. The per-guest
// RESYNC has a cluster in its path and a coarse gate that passes on EITHER
// guest permission, which is exactly Alternatives; the finer per-class check
// (a QEMU guest needs view:vm, an LXC one view:container) stays in the handler,
// because which one applies is not known until the guest row is read, and it
// answers 404 rather than 403 so a caller holding only the other class cannot
// learn that a guest with this vmid exists.
func registerGuestSnapshotEndpoints(reg *Registry, h *handlers.GuestSnapshotHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   guestSnapshotScope,
		Description: "List collected guest snapshots across every cluster, oldest first. QEMU rows require " +
			"view:vm on the row's cluster and LXC rows view:container — the two are separate grants, so a " +
			"caller holding one sees only that kind.",
		Group: "Guest Snapshots",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "vm", Scope: ScopeCluster},
			Reason: guestSnapshotScopeReason,
		}},
		Parameters: apischema.Properties{
			"filter_cluster_id": guestSnapshotFilterClusterParam,
		},
		Handler: h.List,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/guest-snapshots/resync",
		Description: "Refresh one guest's snapshot inventory straight from Proxmox and converge the stored " +
			"rows, so the central page reflects a just-finished snapshot without waiting for the collector. " +
			"An empty or implausible listing from Proxmox is refused rather than allowed to prune.",
		Group: "Guest Snapshots",
		// The coarse gate: EITHER guest permission on the cluster in the path
		// opens the route, which is what the handler's two hasClusterPerm calls
		// amounted to. The precise per-class check stays there — see the
		// function comment above.
		Permissions: Permissions{Alternatives: []Check{
			{Action: "view", Resource: "vm", Scope: ScopeCluster},
			{Action: "view", Resource: "container", Scope: ScopeCluster},
		}},
		Parameters: clusterParams(apischema.Properties{
			"vmid": {
				Type:     apischema.Integer,
				Minimum:  apischema.Ptr(1.0),
				Maximum:  apischema.Ptr(999999999.0),
				Typetext: "<integer>",
				Description: "Proxmox VMID of the guest. The guest is resolved on (cluster, vmid) rather " +
					"than on its Nexara row id, because the collector re-issues those.",
			},
		}),
		Handler: h.Resync,
	})
}
