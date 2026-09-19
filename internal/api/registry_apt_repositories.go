package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, withParams and optString — lives in
// registry_vms.go, where the first migrated domain defined it.

// aptScope is the path all three routes hang off.
//
// The node segment is spelled :node rather than :node_name, which is why this
// file cannot use nodeParams: these three routes have always registered it that
// way, and renaming a path parameter renames nothing the caller sees but does
// rename what c.Params and the schema have to agree on.
const aptScope = clusterScope + "/nodes/:node/apt/repositories"

// aptParams is the pair every route here carries.
func aptParams(extra apischema.Properties) apischema.Properties {
	return clusterParams(withParams(apischema.Properties{
		"node": apischema.StdOption("node-name"),
	}, extra))
}

// aptDigestParam is Proxmox's optimistic-concurrency token for the repository
// file.
//
// Optional because both write endpoints have always accepted the request
// without one, and Proxmox then applies the change unconditionally. It is
// carried through verbatim rather than parsed: the value is Proxmox's own
// checksum of /etc/apt/sources.list*, and its shape is theirs to change.
var aptDigestParam = apischema.Property{
	Type:      apischema.String,
	Optional:  true,
	MaxLength: apischema.Ptr(128),
	Typetext:  "<digest>",
	Description: "Digest returned by the listing, so a concurrent edit is refused rather than overwritten. " +
		"Omitted, Proxmox applies the change unconditionally.",
}

// registerAptRepositoryEndpoints declares AptRepositoryHandler's three routes.
//
// All three are a plain cluster-scoped Check on the apt_repository resource —
// view for the listing, manage for the two writes — and nothing about them is
// conditional. The three share one path, which is why they read as one resource
// with three verbs rather than as three endpoints.
func registerAptRepositoryEndpoints(reg *Registry, h *handlers.AptRepositoryHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   aptScope,
		Description: "List a node's APT repositories, the standard repositories Proxmox offers it, and " +
			"any warnings about the current configuration.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "apt_repository"),
		Parameters:  aptParams(nil),
		Handler:     h.ListRepositories,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        aptScope,
		Description: "Enable or disable one repository entry, identified by the file it lives in and its index within that file.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "apt_repository"),
		Parameters: aptParams(apischema.Properties{
			"path": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(256),
				Typetext:  "<path>",
				Description: "Repository file the entry lives in, as the listing reports it — e.g. " +
					"/etc/apt/sources.list.",
			},
			"index": {
				Type:     apischema.Integer,
				Optional: true,
				Default:  0,
				Minimum:  apischema.Ptr(0.0),
				// The real ceiling is how many entries the file has, which only
				// Proxmox knows and which it answers for itself. This bound is
				// here so the value narrows to an int exactly on every build.
				Maximum:     apischema.Ptr(2147483647.0),
				Typetext:    "<integer>",
				Description: "Zero-based index of the entry within that file, as the listing reports it.",
			},
			"enabled": {
				Type:     apischema.Boolean,
				Optional: true,
				Default:  false,
				Typetext: "<boolean>",
				Description: "New state for the entry. Omitted means false, which is what the handler " +
					"has always read out of a body that left it out.",
			},
			"digest": aptDigestParam,
		}),
		Handler: h.ToggleRepository,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        aptScope,
		Description: "Add one of the standard repositories Proxmox offers for this node, named by its handle (e.g. no-subscription).",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "apt_repository"),
		Parameters: aptParams(apischema.Properties{
			"handle": {
				Type: apischema.String,
				// The handler's own handlePattern, restated one layer earlier:
				// the value becomes a segment of the request Proxmox parses, and
				// the standard handles are lowercase identifiers.
				Pattern:     `^[a-z][a-z0-9-]{0,63}$`,
				MaxLength:   apischema.Ptr(64),
				Typetext:    "<handle>",
				Description: "Standard repository handle, as the listing's standard-repositories section reports it.",
			},
			"digest": aptDigestParam,
		}),
		Handler: h.AddStandardRepository,
	})
}
