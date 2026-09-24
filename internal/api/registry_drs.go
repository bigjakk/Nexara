package api

import (
	"slices"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, withParams, optString and optTristateBool — lives in
// registry_vms.go, where the first migrated domain defined it.

// drsScope is the path prefix every DRS route hangs off.
const drsScope = clusterScope + "/drs"

// drsVMIDsParam is the guest list a DRS rule applies to.
//
// An array of VMIDs rather than of Nexara guest ids, and that is not a
// choice this migration made: the column is JSONB, the frontend types it
// `number[]`, and the engine matches rules against the Proxmox VMID
// because a guest's Nexara row id churns — the collector deletes and
// re-inserts guest rows, so anything persisted against vms.id goes stale.
func drsVMIDsParam(optional bool, description string) apischema.Property {
	p := apischema.Property{
		Type:     apischema.Array,
		Optional: optional,
		Items: &apischema.Property{
			Type:     apischema.Integer,
			Minimum:  apischema.Ptr(1.0),
			Maximum:  apischema.Ptr(999999999.0),
			Typetext: "<integer>",
		},
		MaxLength:   apischema.Ptr(4096),
		Typetext:    "<[vmid,…]>",
		Description: description,
	}
	if !optional {
		p.MinLength = apischema.Ptr(1)
	}
	return p
}

// drsNodeNamesParam is the node list a pin rule confines its guests to.
//
// It is OPTIONAL and, more to the point, an EMPTY ARRAY is a value the
// create dialog really sends: the node picker only appears for a pin rule,
// so an affinity or anti-affinity rule posts node_names:[] rather than
// omitting the key. An array analogue of the empty-string sentinel, and a
// MinLength here would 400 two of the three rule types.
var drsNodeNamesParam = apischema.Property{
	Type:     apischema.Array,
	Optional: true,
	Items: &apischema.Property{
		Type:     apischema.String,
		Format:   "node-name",
		Typetext: "<name>",
	},
	MaxLength: apischema.Ptr(256),
	Typetext:  "<[node,…]>",
	Description: "Nodes a pin rule confines its guests to. Empty for an affinity or anti-affinity rule, " +
		"which the create dialog sends as [].",
}

// registerDRSEndpoints declares the 10 DRS routes served by DRSHandler.
//
// All 10 declare a plain Check: four on view:drs, six on manage:drs, each
// the single static requireClusterPerm call its handler carried. None is
// Deferred — nothing here picks its action or resource at request time.
//
// None is Advisory either, and that one was worth checking rather than
// assuming: an Advisory declaration is for a listing that FILTERS on a
// permission instead of gating on it (accessibleClusters), and every
// listing here — rules, history, ha-rules — resolves one cluster from its
// own path and gates on that cluster. accessibleClusters appears nowhere
// in handlers/drs.go.
//
// DeleteHARule stays a DRS route gated on manage:drs rather than
// delegating to HAHandler.DeleteRule, which is gated on manage:ha. The
// declaration is where that distinction is now visible: two routes onto
// one PVE endpoint, deliberately requiring different permissions.
func registerDRSEndpoints(reg *Registry, h *handlers.DRSHandler) {
	// ── Configuration ─────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   drsScope + "/config",
		Description: "Read the cluster's DRS configuration, with Proxmox's own native CRS state alongside it " +
			"when the cluster has one.",
		Group:       "DRS",
		Permissions: clusterCheck("view", "drs"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetConfig,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        drsScope + "/config",
		Description: "Write the cluster's DRS configuration.",
		Group:       "DRS",
		Permissions: clusterCheck("manage", "drs"),
		Parameters: clusterParams(apischema.Properties{
			"mode": {
				Type: apischema.String,
				// Cloned rather than aliased, for the reason the VM status
				// route's enum gives: Property.Enum is only deep-copied on
				// the StdOption path, so sharing the package-level slice
				// would give every Server's schema the same backing array.
				Enum:     slices.Clone(handlers.DRSModes),
				Typetext: "<disabled|advisory|automatic>",
				Description: "What DRS does with its recommendations: nothing, record them, or queue them " +
					"for the scheduler leader to execute.",
			},
			"weights": {
				Type: apischema.Object,
				// The same object the handler substituted for a missing
				// weights key. Stating it as the schema's Default is what
				// puts it in the published docs; deepCopyValue hands each
				// request its own copy, so a handler cannot edit it.
				Default:  map[string]any{"cpu": 0.3, "memory": 0.7},
				Optional: true,
				Typetext: "<object>",
				Description: "Relative weight of each resource when scoring a node, as an object of " +
					"factor to weight. Omitted uses cpu 0.3 / memory 0.7.",
			},
			"imbalance_threshold": {
				Type: apischema.Number,
				// THIS BOUND IS INCLUSIVE AND THE REAL RULE IS NOT.
				//
				// UpdateConfig's own `if threshold <= 0` check is NOT
				// redundant with this Minimum, and must not be tidied away
				// on the assumption that the schema covers it.
				// apischema.Property has Minimum and Maximum and no
				// EXCLUSIVE form of either, so "greater than 0" is a rule
				// this declaration cannot express: 0 passes validation here
				// and is refused one layer down. A threshold of 0 would make
				// every imbalance actionable, which is a migration loop.
				//
				// TestDRSConfigBody's "a zero threshold is still refused"
				// case asserts BOTH halves — that the schema lets 0 through,
				// and that the handler then refuses it — so deleting the
				// handler check fails a test rather than quietly widening
				// the endpoint, and so that check cannot rot into dead code
				// if apischema ever grows an exclusive minimum without this
				// declaration being updated to use it.
				Minimum:  apischema.Ptr(0.0),
				Maximum:  apischema.Ptr(1.0),
				Typetext: "<number>",
				Description: "How far apart node scores must be before DRS proposes a move, as a fraction. " +
					"Must be greater than 0 — a threshold of 0 would make every imbalance actionable.",
			},
			"eval_interval_seconds": {
				Type:    apischema.Integer,
				Minimum: apischema.Ptr(60.0),
				// The column is an int32, and a JSON number above that used
				// to fail the struct bind as "Invalid request body" —
				// a 400 that named nothing. Bounding it here names the field.
				Maximum:     apischema.Ptr(2147483647.0),
				Typetext:    "<integer>",
				Description: "Seconds between scheduled evaluations. The scheduler refuses anything under 60.",
			},
			"include_containers": optFlag("Consider LXC containers as well as VMs when planning moves."),
			"exclude_veeam_workers": optTristateBool(
				"Pin the guests Veeam owns — worker appliances, and the VBR server where it runs on the " +
					"cluster it protects — so DRS never migrates them mid-backup. OMITTING this leaves the " +
					"stored value alone; it defaults to on, and a client that sent false by omission would " +
					"silently disarm it."),
		}),
		Handler: h.UpdateConfig,
	})

	// ── Nexara's own rules ────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        drsScope + "/rules",
		Description: "List the cluster's Nexara-managed DRS rules.",
		Group:       "DRS",
		Permissions: clusterCheck("view", "drs"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListRules,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        drsScope + "/rules",
		Description: "Create a Nexara-managed DRS rule keeping guests together, apart, or pinned to nodes.",
		Group:       "DRS",
		Permissions: clusterCheck("manage", "drs"),
		Parameters: clusterParams(apischema.Properties{
			"rule_type":  drsRuleTypeParam(),
			"vm_ids":     drsVMIDsParam(true, "Proxmox VMIDs the rule applies to."),
			"node_names": drsNodeNamesParam,
			"enabled":    optFlag("Whether the engine should honour the rule."),
		}),
		Handler: h.CreateRule,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        drsScope + "/rules/:rule_id",
		Description: "Delete a Nexara-managed DRS rule.",
		Group:       "DRS",
		Permissions: clusterCheck("manage", "drs"),
		Parameters: clusterParams(apischema.Properties{
			"rule_id": {
				Type:        apischema.String,
				Format:      "uuid",
				Typetext:    "<uuid>",
				Description: "Nexara DRS rule identifier.",
			},
		}),
		Handler: h.DeleteRule,
	})

	// ── Evaluation ────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   drsScope + "/evaluate",
		Description: "Evaluate the cluster now and return the moves DRS would make. In automatic mode it " +
			"also queues an evaluation for the scheduler leader, which owns the only executor.",
		Group:       "DRS",
		Permissions: clusterCheck("manage", "drs"),
		Parameters:  clusterParams(nil),
		Handler:     h.TriggerEvaluate,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        drsScope + "/history",
		Description: "List the moves DRS has proposed or made on this cluster, newest first.",
		Group:       "DRS",
		Permissions: clusterCheck("view", "drs"),
		Parameters: clusterParams(apischema.Properties{
			"limit": {
				Type:        apischema.Integer,
				Optional:    true,
				Default:     50,
				Minimum:     apischema.Ptr(1.0),
				Maximum:     apischema.Ptr(500.0),
				Typetext:    "<integer>",
				Description: "Maximum rows to return.",
			},
		}),
		Handler: h.ListHistory,
	})

	// ── Proxmox's own HA rules, through the DRS page ──────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   drsScope + "/ha-rules",
		Description: "List Proxmox's own HA rules, rendered in DRS's vocabulary so the two rule sets read " +
			"side by side.",
		Group:       "DRS",
		Permissions: clusterCheck("view", "drs"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListHARules,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        drsScope + "/ha-rules",
		Description: "Create a Proxmox HA rule from DRS's vocabulary. Requires manage:drs, not manage:ha.",
		Group:       "DRS",
		Permissions: clusterCheck("manage", "drs"),
		Parameters: clusterParams(apischema.Properties{
			"rule_name": {
				Type:        apischema.String,
				Format:      "pve-configid",
				Typetext:    "<name>",
				Description: "Name for the new Proxmox HA rule: 2-128 characters, starting with a letter.",
			},
			"rule_type": drsRuleTypeParam(),
			"vm_ids": drsVMIDsParam(false,
				"Proxmox VMIDs the rule applies to. At least one is required — Proxmox's rule has no "+
					"meaning without a resource."),
			"node_names": drsNodeNamesParam,
			"enabled": optFlag("Accepted for symmetry with the Nexara rule body, but NOT yet forwarded: " +
				"Proxmox creates the rule active, and disabling one is a separate write through the HA tab."),
		}),
		Handler: h.CreateHARule,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   drsScope + "/ha-rules/:rule_name",
		Description: "Delete a Proxmox HA rule from the DRS page. Requires manage:drs, not manage:ha, and " +
			"audits whether the rule was really there to remove.",
		Group:       "DRS",
		Permissions: clusterCheck("manage", "drs"),
		Parameters: clusterParams(apischema.Properties{
			// Not the percent-tolerant shape haSIDParam takes: an HA rule
			// name has no character the SPA's apiPath would encode — the
			// rule is a leading letter, then letters, digits, underscore and
			// dash — so it arrives exactly as spelled, and the handler's
			// decodeParamValue (DRSHandler.DeleteHARule) changes nothing
			// today. The pattern sees only the RAW segment. What keeps a
			// traversing DECODED name out of the Proxmox path is the client:
			// DeleteHARule runs validateHAConfigID
			// (internal/proxmox/client_ha.go) before it escapes the id into
			// the path.
			"rule_name": haConfigIDParam("Proxmox HA rule name."),
		}),
		Handler: h.DeleteHARule,
	})
}

// drsRuleTypeParam is DRS's own rule vocabulary.
//
// Unlike the Proxmox vocabularies elsewhere in this registry, this one IS
// an enum: the three values are Nexara's, not Proxmox's — the handler maps
// them onto PVE's node-affinity and resource-affinity — so nothing
// upstream can add a fourth without this repo changing too. The list comes
// from handlers.DRSRuleTypes, which is held against the map the handlers
// validate with.
func drsRuleTypeParam() apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Enum:        slices.Clone(handlers.DRSRuleTypes),
		Typetext:    "<affinity|anti-affinity|pin>",
		Description: "Keep the guests together, keep them apart, or pin them to named nodes.",
	}
}
