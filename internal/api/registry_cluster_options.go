package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, withParams, optString and optCount — lives in
// registry_vms.go, where the first migrated domain defined it.

// clusterOptionString is the shape almost every datacenter option has: an
// optional string the handler forwards as a *string, so that OMITTING the
// key means "do not send this property" and an EMPTY value means "send it
// empty".
//
// None of them carries a format, and that is a compatibility decision
// rather than an oversight. Proxmox owns these vocabularies — the console
// kind, the keyboard layout, the migration and HA and CRS property strings
// — versions them, and rejects a bad one with a message of its own; a
// second copy here would date on the next PVE release. The empty string is
// separately load-bearing: apischema treats "" as a value the caller
// supplied and every registered format rejects it, so a format would 400 a
// caller that clears a field by sending it empty. (Nexara's own options tab
// clears a field through PVE's `delete` parameter instead, which is what
// PVE requires — it 500s on an empty form value — but the endpoint has
// always forwarded an empty one.)
func clusterOptionString(maxLen int, typetext, description string) apischema.Property {
	return optString(maxLen, typetext, description)
}

// registerClusterOptionsEndpoints declares the 9 datacenter option, tag and
// corosync routes served by ClusterOptionsHandler.
//
// All 9 declare a plain Check: every handler resolved the cluster from the
// path and then made exactly one static requireClusterPerm call, six on
// view:cluster and three on manage:cluster. None is Deferred — nothing here
// picks its action or resource at request time — and none is Advisory:
// every read gates on the cluster in its path rather than filtering a
// listing through accessibleClusters.
//
// The resource is "cluster" rather than a dedicated one because that is
// what the handlers checked. These are datacenter-wide settings, so the
// permission that opens them is the permission that opens the cluster.
func registerClusterOptionsEndpoints(reg *Registry, h *handlers.ClusterOptionsHandler) {
	// ── Datacenter options ────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/options",
		Description: "Read the cluster's datacenter options as Proxmox stores them.",
		Group:       "Clusters",
		Permissions: clusterCheck("view", "cluster"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetOptions,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   clusterScope + "/options",
		Description: "Change the cluster's datacenter options. Every parameter is optional; the ones " +
			"omitted are left as they are, and a property is cleared by naming it in delete rather than " +
			"by sending it empty.",
		Group:       "Clusters",
		Permissions: clusterCheck("manage", "cluster"),
		Parameters:  clusterParams(updateClusterOptionsParams()),
		Handler:     h.UpdateOptions,
	})

	// ── Description and tags ──────────────────────────────────────────
	//
	// Both are narrow views onto /cluster/options — the same Proxmox call,
	// reading or writing one field group — kept as their own routes because
	// that is what the UI addresses.
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/description",
		Description: "Read the cluster's free-text description.",
		Group:       "Clusters",
		Permissions: clusterCheck("view", "cluster"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetDescription,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        clusterScope + "/description",
		Description: "Change the cluster's free-text description.",
		Group:       "Clusters",
		Permissions: clusterCheck("manage", "cluster"),
		Parameters: clusterParams(apischema.Properties{
			"description": {
				Type:     apischema.String,
				Optional: true,
				// The handler bound this into a plain string and forwarded
				// its ADDRESS unconditionally, so a body that omitted the key
				// cleared the description. Declaring it required would 400 a
				// request that used to work; the Default states what the zero
				// value already meant, so the docs answer "what happens if I
				// leave this out".
				Default:   "",
				MaxLength: apischema.Ptr(8192),
				Typetext:  "<string>",
				Description: "New description. An empty value — or omitting the key entirely, which is " +
					"the same thing here — clears it.",
			},
		}),
		Handler: h.UpdateDescription,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/tags",
		Description: "Read the cluster's registered tags, tag access policy and tag style.",
		Group:       "Clusters",
		Permissions: clusterCheck("view", "cluster"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetTags,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   clusterScope + "/tags",
		Description: "Change the cluster's tag settings. Every parameter is optional; the ones omitted " +
			"are left as they are.",
		Group:       "Clusters",
		Permissions: clusterCheck("manage", "cluster"),
		Parameters: clusterParams(apischema.Properties{
			// These three are the UNDERSCORED spellings this endpoint has
			// always taken, which are NOT the hyphenated ones PVE uses and
			// the options endpoint above forwards. The client translates;
			// renaming them here would break every existing caller for no
			// gain.
			"registered_tags": clusterOptionString(4096, "<tag>[;<tag>…]",
				"Semicolon-separated tags the cluster knows about."),
			"user_tag_access": clusterOptionString(256, "<none|list|existing|free>",
				"Which tags a non-privileged user may set on a guest."),
			"tag_style": clusterOptionString(1024, "<property string>",
				"Tag rendering options, as a Proxmox property string."),
		}),
		Handler: h.UpdateTags,
	})

	// ── Corosync configuration ────────────────────────────────────────
	for _, r := range []struct {
		suffix      string
		description string
		handler     Handler
	}{
		{"/config", "Read the cluster's corosync configuration.", h.GetClusterConfig},
		{"/config/join", "Read what a node needs to join this cluster, including its fingerprints.", h.GetJoinInfo},
		{"/config/nodes", "List the cluster's corosync nodes with their ring addresses and vote weights.", h.ListCorosyncNodes},
	} {
		reg.Register(Endpoint{
			Method:      fiber.MethodGet,
			Path:        clusterScope + r.suffix,
			Description: r.description,
			Group:       "Clusters",
			Permissions: clusterCheck("view", "cluster"),
			Parameters:  clusterParams(nil),
			Handler:     r.handler,
		})
	}
}

// updateClusterOptionsParams is the body of
// PUT /clusters/:cluster_id/options.
//
// It declares all nineteen fields of proxmox.UpdateClusterOptionsParams,
// including the four Proxmox spells with hyphens. That is the whole gain
// here: the handler bound an ad-hoc struct, so a misspelled key was
// silently dropped and the caller got a 200 for a save that changed
// nothing.
func updateClusterOptionsParams() apischema.Properties {
	return apischema.Properties{
		"console":    clusterOptionString(64, "<applet|vv|html5|xtermjs>", "Default console viewer for the whole datacenter."),
		"keyboard":   clusterOptionString(32, "<layout>", "Default keyboard layout for consoles, e.g. en-us."),
		"language":   clusterOptionString(32, "<language>", "Default web UI language."),
		"email_from": clusterOptionString(256, "<email>", "From address Proxmox sends cluster notifications with."),
		"http_proxy": clusterOptionString(512, "<url>", "HTTP proxy Proxmox fetches updates and appliances through."),
		"mac_prefix": clusterOptionString(32, "<prefix>", "MAC address prefix new guest NICs are generated under."),

		"migration":      clusterOptionString(512, "<property string>", "Migration settings, e.g. \"type=secure,network=192.0.2.0/24\"."),
		"migration_type": clusterOptionString(32, "<secure|insecure>", "Legacy standalone migration type. PVE 8+ carries it inside migration."),
		"bwlimit":        clusterOptionString(512, "<property string>", "Per-operation bandwidth caps in KiB/s, e.g. \"migration=100000,restore=50000\"."),
		"next-id":        clusterOptionString(128, "<property string>", "Bounds the VMID the \"next free id\" button suggests, e.g. \"lower=100,upper=1000\"."),
		"ha":             clusterOptionString(256, "<property string>", "Cluster-wide HA settings, e.g. \"shutdown_policy=migrate\"."),
		"fencing":        clusterOptionString(64, "<watchdog|hardware|both>", "How the HA stack fences a failed node."),
		"crs":            clusterOptionString(512, "<property string>", "Proxmox's own cluster resource scheduler settings, e.g. \"ha=basic\"."),

		// The ceiling is int32's, not a policy. Proxmox's own max_workers
		// schema has a minimum and NO maximum, and the handler forwarded
		// whatever it was given, so a smaller bound here — 1024, say — would
		// refuse a value that used to be stored, and refuse it for an
		// operator whose form field (ClusterOptionsTab) carries no max
		// either. What is worth bounding is only that the value survives the
		// *int the client narrows it to.
		"max_workers": optCount(2147483647,
			"Parallel workers a bulk action may run per node. 0 leaves Proxmox's own default, which is "+
				"what the options tab sends for an empty field."),

		"description": clusterOptionString(8192, "<string>", "Free-text description, the same value PUT /description writes."),

		// The hyphenated tag spellings PVE itself uses. The /tags route
		// above takes the underscored ones; both reach the same three
		// Proxmox properties.
		"registered-tags": clusterOptionString(4096, "<tag>[;<tag>…]", "Semicolon-separated tags the cluster knows about."),
		"user-tag-access": clusterOptionString(256, "<none|list|existing|free>", "Which tags a non-privileged user may set on a guest."),
		"tag-style":       clusterOptionString(1024, "<property string>", "Tag rendering options, as a Proxmox property string."),

		"delete": clusterOptionString(1024, "<key>[,<key>…]",
			"Comma-separated option names to clear. This is how a datacenter option is unset: Proxmox "+
				"rejects an empty form value for most of them, so the options tab sends the names here "+
				"instead."),
	}
}
