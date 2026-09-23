package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary lives in registry_vms.go; the four-file
// split of NetworkHandler's 66 routes is explained in registry_networks.go.

// firewallTemplateScope is the instance-wide template collection. Unlike
// every other route in this handler it hangs off no cluster: a template is a
// saved set of rules Nexara stores in its OWN database, and applying one to
// a cluster is a separate, cluster-scoped route.
const firewallTemplateScope = pathPrefix + "firewall-templates"

// firewallTemplateIDParam is Nexara's own row id for a template.
var firewallTemplateIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Typetext:    "<uuid>",
	Description: "Nexara firewall template identifier, as GET /firewall-templates returns it.",
}

// registerFirewallTemplateEndpoints declares FOUR of the six firewall
// template routes.
//
// The two writes — POST and PUT /api/v1/firewall-templates — are
// deliberately left in router.go, and the reason is a gap in the parameter
// vocabulary rather than anything about the routes themselves:
//
//	their body carries `rules`, a JSON ARRAY OF OBJECTS (the rule set the
//	template stores), and apischema cannot describe one. Property.Items is
//	restricted to scalar element types by compileItems in
//	apischema/validate.go — "only scalar element types are supported" — so
//	an Array of Object is refused at registration, and an Object parameter
//	cannot stand in because the value is an array rather than a map.
//
// Declaring the routes without `rules` is not an option either: an
// undeclared key is now a 400, so it would break exactly the request these
// endpoints exist for. TestFirewallTemplateWritesAreStillLegacy pins both
// halves of that, so "4 of 6" is an assertion rather than something a reader
// has to notice, and it fails the moment apischema grows object items.
//
// The permissions are the other thing worth naming. The five instance-wide
// routes are GLOBAL — requirePerm, i.e. HasGlobalPermission, not
// requireClusterPerm — because a template belongs to the install rather than
// to a cluster. The sixth, apply, is cluster-scoped: it writes rules into
// ONE cluster's firewall, and the cluster is the first parameter in its
// path. Declaring apply as global would REFUSE the operator whose
// manage:network is scoped to that cluster — a global check is satisfied only
// by a global grant (internal/auth/rbac.go), and a global grant already
// covers every cluster, so it would add no one and shut out every
// cluster-scoped holder. Declaring the other five as cluster-scoped is not
// even expressible, since their paths name no cluster.
func registerFirewallTemplateEndpoints(reg *Registry, h *handlers.NetworkHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   firewallTemplateScope,
		Description: "List the saved firewall rule templates. They live in Nexara's own database and " +
			"belong to the install rather than to a cluster, so this needs an instance-wide view:network.",
		Group:       "Firewall",
		Permissions: globalCheck("view", "network"),
		Parameters:  nil,
		Handler:     h.ListTemplates,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        firewallTemplateScope + "/:id",
		Description: "Read one firewall rule template, including the rules it stores.",
		Group:       "Firewall",
		Permissions: globalCheck("view", "network"),
		Parameters:  apischema.Properties{"id": firewallTemplateIDParam},
		Handler:     h.GetTemplate,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   firewallTemplateScope + "/:id",
		Description: "Delete a firewall rule template. Rules already applied to a cluster are not " +
			"touched — applying a template copies its rules rather than linking them.",
		Group:       "Firewall",
		Permissions: globalCheck("delete", "network"),
		Parameters:  apischema.Properties{"id": firewallTemplateIDParam},
		Handler:     h.DeleteTemplate,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/firewall-templates/:id/apply",
		Description: "Copy a template's rules into one cluster's firewall. Each rule is added " +
			"independently and a rule Proxmox rejects is SKIPPED, so the response reports how many of " +
			"the total landed. Cluster-scoped manage:network, unlike the template routes themselves.",
		Group:       "Firewall",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(apischema.Properties{
			"id": firewallTemplateIDParam,
		}),
		Handler: h.ApplyTemplate,
	})
}
