package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// globalCheck, clusterParams, withParams, optFlag, optString and
// optTristateBool — lives in registry_vms.go, where the first migrated
// domain defined it.

// The two prefixes this domain hangs off. The catalog and the download
// source are INSTANCE-WIDE rather than per-cluster: every cluster pins
// against the same published set of versions, and being cut off from
// upstream is a property of the install, not of one cluster.
const (
	virtioWinGlobalScope  = pathPrefix + "virtio-win"
	virtioWinClusterScope = clusterScope + "/virtio-win"
)

// virtioWinVersionParam is an upstream virtio-win version, or the empty
// string.
//
// No format and no pattern: virtiowin.ValidVersion owns the vocabulary and
// the handlers still call it, so a second copy here would be one that
// drifts. The empty string is separately load-bearing — on the config it
// means "follow upstream stable", and on the download body it means
// "resolve the cluster's target" — and every registered format rejects "".
func virtioWinVersionParam(description string) apischema.Property {
	return optString(32, "<version>", description)
}

// registerVirtioWinEndpoints declares the 8 virtio-win routes served by
// VirtioWinHandler.
//
// All 8 declare a plain Check, and none is Deferred or Advisory. What is
// worth saying out loud is the SCOPE split, because it is the first one in
// the registry: the three routes under /api/v1/virtio-win are
// instance-wide, so they gate with requirePerm (ScopeGlobal) rather than
// requireClusterPerm, and there is no cluster in their paths for a
// cluster-scoped check to resolve.
//
// The resources are storage permissions rather than a resource of their
// own, inherited from the handlers: the effect of this feature is "a
// Proxmox node fetches a URL into a storage", which is what manage:storage
// already authorises on POST …/storage/:id/download-url. The source
// override is the exception, and the one route here gated on
// manage:settings: one write repoints every cluster's downloads at once,
// which is a broader blast radius than a storage operator has anywhere
// else.
func registerVirtioWinEndpoints(reg *Registry, h *handlers.VirtioWinHandler) {
	// ── Instance-wide catalog and download source ─────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   virtioWinGlobalScope + "/releases",
		Description: "List the known upstream virtio-win releases. Each one's iso_url is rebuilt against " +
			"the download source in force rather than served from the row, so a mirror configured after " +
			"discovery is reflected here.",
		Group:       "virtio-win",
		Permissions: globalCheck("view", "storage"),
		Parameters:  nil,
		Handler:     h.ListReleases,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   virtioWinGlobalScope + "/mirror",
		Description: "Read the instance-wide virtio-win download source, alongside what it resolves to " +
			"and what upstream would be.",
		Group: "virtio-win",
		// view:storage rather than manage:settings, matching the handler:
		// the operator reading a failed check on a cluster page has to be
		// able to see where it was pointed.
		Permissions: globalCheck("view", "storage"),
		Parameters:  nil,
		Handler:     h.GetMirror,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   virtioWinGlobalScope + "/mirror",
		Description: "Point virtio-win downloads at a mirror, for air-gapped installs. Answers 422 with a " +
			"confirmation code for a plain-http source or one resolving to a private address; re-submit " +
			"with the matching allow_ flag to proceed.",
		Group: "virtio-win",
		// manage:settings, not manage:storage: one write here redirects
		// every cluster's downloads at once.
		Permissions: globalCheck("manage", "settings"),
		Parameters: apischema.Properties{
			"base_url": {
				Type:     apischema.String,
				Optional: true,
				// The EMPTY string is the meaningful value that clears the
				// override and goes back to upstream, and it is what the card
				// sends for an empty field — so this cannot carry a format.
				// virtiowin.NormalizeBase is the choke point that owns the
				// rest of the rule (scheme, host, no credentials, no query),
				// and it rejects with a message of its own.
				Default:     "",
				MaxLength:   apischema.Ptr(2048),
				Typetext:    "<url>",
				Description: "Download root to use instead of upstream. An empty value clears the override.",
			},
			"allow_private_address": optFlag(
				"Confirm a source that resolves to a private or loopback address, which an internal " +
					"mirror always does."),
			"allow_insecure": optFlag(
				"Confirm a plain-http source. Separate from the address confirmation because it is a " +
					"different risk: the ISO is installed as kernel-mode drivers and upstream publishes " +
					"no checksum, so the transport is the only thing authenticating it."),
		},
		Handler: h.SetMirror,
	})

	// ── Per-cluster auto-download policy ──────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        virtioWinClusterScope + "/config",
		Description: "Read the cluster's virtio-win auto-download policy, or disabled defaults when none is stored.",
		Group:       "virtio-win",
		Permissions: clusterCheck("view", "storage"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetConfig,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   virtioWinClusterScope + "/config",
		Description: "Change the cluster's virtio-win auto-download policy. A storage is required once " +
			"auto-download is enabled, and an unparseable schedule is refused rather than silently " +
			"falling back to the six-hourly default.",
		Group:       "virtio-win",
		Permissions: clusterCheck("manage", "storage"),
		Parameters: clusterParams(apischema.Properties{
			"enabled": optFlag("Let the scheduler fetch and prune this cluster's virtio-win ISOs on its own."),
			"storage": {
				Type:     apischema.String,
				Optional: true,
				// Empty is what the card sends before a storage is picked, and
				// the handler refuses it only when enabled is true — a
				// cross-field rule no per-parameter format can express, and
				// one the storage-id format would pre-empt with the wrong
				// message.
				Default:   "",
				Pattern:   emptyOrStorageID,
				MaxLength: apischema.Ptr(100),
				Typetext:  "<storage>",
				Description: "ISO storage the downloads land on. Required — and refused empty — once " +
					"enabled is true.",
			},
			"node": {
				Type:      apischema.String,
				Optional:  true,
				Default:   "",
				Pattern:   emptyOrNodeName,
				MaxLength: apischema.Ptr(63),
				Typetext:  "<name>",
				Description: "Node to run the download on. Empty means any online node — the ISO lands " +
					"on the storage regardless.",
			},
			"target_version": virtioWinVersionParam(
				"Pin the cluster to this virtio-win version. Empty follows upstream stable."),
			"prune_enabled": optTristateBool(
				"Delete ISOs the cluster no longer targets. OMITTING it keeps the stored value: this " +
					"DELETES files, so a client that predates the field must not disarm an operator's " +
					"pruning, and coercing an absent key to true would arm destructive behaviour nobody " +
					"asked for."),
			"check_schedule": optString(256, "<cron>",
				"Five-field cron expression for the automatic check. Empty means every six hours."),
			"check_timezone": optString(64, "<IANA zone>",
				"Zone the schedule is read in. Empty means the server's own, which in a container is all "+
					"but always UTC."),
		}),
		Handler: h.UpdateConfig,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   virtioWinClusterScope + "/check",
		Description: "Run the cluster's scheduled virtio-win check now: refresh the catalog, then " +
			"reconcile the storage against the target. Refused when auto-download is off for the cluster — " +
			"use the download endpoint to fetch without opting in.",
		Group:       "virtio-win",
		Permissions: clusterCheck("manage", "storage"),
		Parameters:  clusterParams(nil),
		Handler:     h.CheckNow,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   virtioWinClusterScope + "/download",
		Description: "Download one virtio-win ISO to the cluster's configured storage now. Answers " +
			"already_present or already_running rather than an error when the intent is already satisfied.",
		Group:       "virtio-win",
		Permissions: clusterCheck("manage", "storage"),
		Parameters: clusterParams(apischema.Properties{
			"version": virtioWinVersionParam(
				"Version to fetch. Empty or omitted resolves the cluster's effective target instead."),
		}),
		Handler: h.Download,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        virtioWinClusterScope + "/downloads",
		Description: "List the cluster's virtio-win download history, newest first.",
		Group:       "virtio-win",
		Permissions: clusterCheck("view", "storage"),
		Parameters: clusterParams(apischema.Properties{
			"limit": {
				Type:     apischema.Integer,
				Optional: true,
				// The value the handler substituted for a missing ?limit=.
				// The bounds replace a CLAMP: ?limit=5000 used to answer with
				// 50 rows, which is indistinguishable from the caller's own
				// page size and makes a paging bug look like missing data.
				Default:     50,
				Minimum:     apischema.Ptr(1.0),
				Maximum:     apischema.Ptr(500.0),
				Typetext:    "<integer>",
				Description: "Maximum download records to return.",
			},
		}),
		Handler: h.ListDownloads,
	})
}
