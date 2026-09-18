package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/guesttools"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, withParams, optFlag, optString and optTristateBool —
// lives in registry_vms.go, where the first migrated domain defined it.

// guestToolsScope is the path prefix every guest tools route hangs off.
const guestToolsScope = clusterScope + "/guest-tools"

// guestVMIDParams is the pair every per-guest route carries.
//
// :cluster_id is FIRST, which namesACluster (permissions.go) requires of a
// cluster-scoped Check. That is worth stating rather than assuming for
// this domain specifically: the helper these handlers used before the
// migration was a THIRD cluster-extraction shape — it read the cluster the
// same way the other two did and then took the Proxmox VMID beside it — so
// the question was whether the routes it served still name their cluster
// first. They do. Its successor is guestToolsIDs in
// internal/api/handlers/guest_tools.go, which reads both out of the
// validated parameters instead.
//
// vmid is the PROXMOX VMID, deliberately not a vms.id UUID: the collector
// deletes and re-inserts guest rows, so per-guest state keyed on the UUID
// stops resolving at an arbitrary later time. The bounds are the ones that
// helper enforced by hand, and the upper one is load-bearing rather than
// cosmetic — SetPolicy writes to a table with no foreign key on vmid, so
// an out-of-range value used to be clamped by safeconv at the sqlc
// parameter while the audit row still recorded what the caller typed.
func guestVMIDParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"vmid": {
			Type:        apischema.Integer,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(999999999.0),
			Typetext:    "<integer>",
			Description: "Proxmox VMID of the guest.",
		},
	}, extra)
}

// guestToolsVersionParam is a pinned virtio-win version, or the empty
// string for "follow the level above".
//
// It carries no format and no pattern: virtiowin.ValidVersion is the choke
// point that owns the vocabulary, the handlers still call it, and a second
// copy here would be one that drifts. The empty string is separately
// load-bearing — it is how the policy card says "no pin" — and every
// registered format rejects "".
func guestToolsVersionParam(description string) apischema.Property {
	return optString(32, "<version>", description)
}

// registerGuestToolsEndpoints declares the 7 Windows guest tools routes
// served by GuestToolsHandler.
//
// All 7 declare a plain Check: every handler resolved the cluster from the
// path and then made exactly one static requireClusterPerm call — three on
// view:guest_tools, two on manage:guest_tools and two on
// execute:guest_tools. None is Deferred and none is Advisory.
//
// The resource is guest_tools rather than vm on purpose, and the routes
// inherit that from the handlers: running an installer inside a guest OS
// is worth granting, auditing and withholding on its own.
//
// Detect, StageUpdate and CancelUpdate additionally refuse the call when
// the guest tools engine is nil. That is an availability check, not an
// authorization one — it answers 503, and it is the same answer for every
// caller — so it stays in the handler and does not make the route
// Deferred, the same way HA's arm/disarm capability check does.
func registerGuestToolsEndpoints(reg *Registry, h *handlers.GuestToolsHandler) {
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        guestToolsScope + "/config",
		Description: "Read the cluster's Windows guest tools update policy, or disabled defaults when none is stored.",
		Group:       "Guest Tools",
		Permissions: clusterCheck("view", "guest_tools"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetConfig,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   guestToolsScope + "/config",
		Description: "Change the cluster's Windows guest tools update policy. mode is required; the " +
			"rest keep their stored values when omitted.",
		Group:       "Guest Tools",
		Permissions: clusterCheck("manage", "guest_tools"),
		Parameters: clusterParams(apischema.Properties{
			"mode": {
				Type: apischema.String,
				// An Enum rather than a plain string, because unlike the
				// Proxmox vocabularies elsewhere in the registry this one is
				// Nexara's own and the handler ALREADY refused anything but
				// these three by name. The declaration states the same rule
				// one layer earlier, and the docs gain the values.
				Enum:     []string{"disabled", "report", "staged"},
				Typetext: "<disabled|report|staged>",
				Description: "How far Nexara acts. disabled does nothing, report only detects installed " +
					"versions, staged also stages updates for guests that are behind.",
			},
			"target_version": guestToolsVersionParam(
				"Pin every guest in the cluster to this virtio-win version. Empty follows the cluster's " +
					"virtio-win target instead."),
			"max_concurrent": {
				Type:     apischema.Integer,
				Optional: true,
				// The value the handler substituted for a missing or
				// non-positive count. The minimum is 0 rather than 1 because
				// the policy card sends 0 for a cleared field — Number("") —
				// and the handler has always read that as "use the default".
				// The handler keeps that substitution; this states what
				// omitting the key does.
				Default:  guesttools.DefaultMaxConcurrent,
				Minimum:  apischema.Ptr(0.0),
				Maximum:  apischema.Ptr(100.0),
				Typetext: "<integer>",
				Description: "How many guests may update at once. 0 means the default of 5. Swapping " +
					"boot-disk drivers across a whole fleet at once turns one bad release into an outage.",
			},
			"snapshot_before": optTristateBool(
				"Snapshot a guest before updating it. OMITTING it keeps the stored value: this is the " +
					"rollback for a driver swap that can leave a guest unbootable, so a client that does " +
					"not know about the field must not clear it."),
		}),
		Handler: h.UpdateConfig,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   guestToolsScope + "/guests",
		Description: "List every Windows guest in the cluster with its guest tools state, including guests " +
			"never probed and guests excluded by policy.",
		Group:       "Guest Tools",
		Permissions: clusterCheck("view", "guest_tools"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListFleet,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   guestToolsScope + "/guests/:vmid/policy",
		Description: "Write a per-guest guest tools override: pin a version, leave a note, or exclude the " +
			"guest entirely.",
		Group:       "Guest Tools",
		Permissions: clusterCheck("manage", "guest_tools"),
		Parameters: guestVMIDParams(apischema.Properties{
			"target_version": guestToolsVersionParam(
				"Pin this guest to a virtio-win version. Empty removes the pin and follows the cluster."),
			"note": {
				Type:     apischema.String,
				Optional: true,
				// Stated rather than left implicit: the handler bound a plain
				// string, so an omitted note has always cleared the stored
				// one, and the editor sends it unconditionally.
				Default:     "",
				MaxLength:   apischema.Ptr(500),
				Typetext:    "<string>",
				Description: "Free-text note stored with the override. An empty value clears it.",
			},
			"excluded": optTristateBool(
				"Never touch this guest. OMITTING it keeps the stored exclusion, so a client that does " +
					"not know about the field cannot clear an operator's \"never touch this\"."),
		}),
		Handler: h.SetPolicy,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   guestToolsScope + "/guests/:vmid/detect",
		Description: "Probe one guest for its installed guest tools version now. Read-only inside the " +
			"guest, which is why it is view:guest_tools rather than execute.",
		Group:       "Guest Tools",
		Permissions: clusterCheck("view", "guest_tools"),
		Parameters:  guestVMIDParams(nil),
		Handler:     h.Detect,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   guestToolsScope + "/guests/:vmid/update",
		Description: "Stage a guest tools update for the guest's next boot, or run it immediately with " +
			"run_now.",
		Group:       "Guest Tools",
		Permissions: clusterCheck("execute", "guest_tools"),
		Parameters: guestVMIDParams(apischema.Properties{
			"run_now": optFlag("Start the installer immediately instead of waiting for the guest's next boot."),
		}),
		Handler: h.StageUpdate,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        guestToolsScope + "/guests/:vmid/update",
		Description: "Clear a staged guest tools update from a guest before it fires.",
		Group:       "Guest Tools",
		Permissions: clusterCheck("execute", "guest_tools"),
		Parameters:  guestVMIDParams(nil),
		Handler:     h.CancelUpdate,
	})
}
