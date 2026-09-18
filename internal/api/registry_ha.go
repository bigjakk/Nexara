package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, withParams, optString and optCount — lives in
// registry_vms.go, where the first migrated domain defined it.

// haScope is the path prefix every HA route hangs off.
const haScope = clusterScope + "/ha"

// haSIDParam is a Proxmox HA resource id — "vm:109" — as a PATH parameter.
//
// The pattern accepts BOTH spellings of the separator, and that is
// load-bearing rather than defensive. Fiber does not decode a path
// parameter, and the frontend percent-encodes the SID, so what arrives
// here is "vm%3A109"; the handler runs url.PathUnescape on it before
// handing it to the client, which needs the literal colon because Proxmox
// rejects a percent-encoded one in an HA SID path. A pattern written
// against either spelling alone would reject every real request from one
// of the two directions.
//
// It is the same vocabulary proxmox.validateHAResourceID enforces
// (haResourceIDPattern in client_ha.go): an optional lowercase type
// prefix and a VMID. Stating it here is what keeps a traversal segment out
// of a path the client builds by concatenation — GetHAResource appends the
// SID raw, precisely because Proxmox will not take it escaped.
var haSIDParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     `^(?:[a-z]{1,16}(?::|%3[aA]))?\d{1,12}$`,
	MaxLength:   apischema.Ptr(32),
	Typetext:    "<type:vmid>",
	Description: `HA resource id, e.g. "vm:109". The colon may be percent-encoded.`,
}

// haConfigIDParam is an HA group or rule name as a PATH parameter.
//
// It is deliberately looser than the pve-configid format the CREATE
// bodies use, for the reason snapshotNameParam gives: Proxmox's own
// configid requires two characters and this does not, so a group or rule
// made outside Nexara cannot become unaddressable through our own stricter
// create rule. What it does enforce is the shape — a leading letter, then
// letters, digits, underscore and dash — which keeps "..", "%2e%2e" and a
// slash out of a path both proxmox client methods build by concatenation.
func haConfigIDParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Pattern:     `^[A-Za-z][A-Za-z0-9_-]*$`,
		MaxLength:   apischema.Ptr(128),
		Typetext:    "<name>",
		Description: description,
	}
}

// haFlag is an optional 0/1 integer, which is how Proxmox spells a boolean
// in the HA API and how every one of these endpoints has always taken one.
//
// It carries NO Default, and that matters for the six of them the handler
// forwards as a *int: omitting the key means "do not send this property",
// which is different from sending 0. A Default here would start writing
// nofailback=0 onto every group whose editor never mentioned failback.
func haFlag(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.Integer,
		Optional:    true,
		Minimum:     apischema.Ptr(0.0),
		Maximum:     apischema.Ptr(1.0),
		Typetext:    "<0|1>",
		Description: description,
	}
}

// haComment is the free-text note every HA object carries.
//
// It has NO format and NO pattern, and the empty string is the point: the
// resource, group and rule editors all send comment unconditionally, so a
// blank field arrives as "" and means "clear the note". apischema treats
// "" as a value the caller SUPPLIED (see present() in validate.go) and
// every registered format rejects it, so a format here would 400 every
// save from a form whose comment box is empty.
func haComment(what string) apischema.Property {
	return optString(4096, "<string>",
		"Free-text note stored on the "+what+". An empty value clears it.")
}

// haDigest is Proxmox's optimistic-concurrency token.
//
// Nexara's own UI does not send one, but the endpoint has always accepted
// and forwarded it — the bound parameter struct carries a Digest field and
// the client sets it when non-empty. Leaving it undeclared would turn a
// working external caller's compare-and-swap into "unknown parameter",
// which is the one way this migration could silently remove a safety
// feature rather than add one.
var haDigest = optString(128, "<sha1>",
	"Configuration digest from a previous read. Proxmox refuses the write if the object changed since.")

// registerHAEndpoints declares the 17 HA routes served by HAHandler.
//
// All 17 declare a plain Check: every handler resolved the cluster from
// the path and then made exactly one static requireClusterPerm call, six
// on view:ha and eleven on manage:ha. None is Deferred — nothing here
// picks its action or resource at request time — and none is Advisory:
// every listing gates on the cluster in its path rather than filtering
// through accessibleClusters.
//
// Arm and Disarm additionally call requireArmDisarmSupport, which refuses
// the pair on a cluster below PVE 9.2. That is a capability check, not an
// authorization one — it answers 400, not 403, and it is the same answer
// for every caller — so it stays in the handler and does not make the
// route Deferred.
func registerHAEndpoints(reg *Registry, h *handlers.HAHandler) {
	// ── Resources ─────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        haScope + "/resources",
		Description: "List the guests the cluster's HA manager is managing.",
		Group:       "High Availability",
		Permissions: clusterCheck("view", "ha"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListResources,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        haScope + "/resources",
		Description: "Put a guest under HA management.",
		Group:       "High Availability",
		Permissions: clusterCheck("manage", "ha"),
		Parameters: clusterParams(apischema.Properties{
			"sid": {
				Type: apischema.String,
				// The body spelling, so no percent-encoded separator: a
				// JSON string has no reason to escape a colon, and the
				// create dialog sends "vm:109" literally.
				Pattern:     `^(?:[a-z]{1,16}:)?\d{1,12}$`,
				MaxLength:   apischema.Ptr(32),
				Typetext:    "<type:vmid>",
				Description: `HA resource id for the guest, e.g. "vm:109".`,
			},
			// Optional, like the PUT's. The handler only ever required
			// `sid`, and CreateHAResource drops an empty state rather than
			// forwarding it (client_ha.go), so omitting it has always been
			// a supported shape that lets Proxmox apply its own default.
			// Declaring it required would 400 a caller that never sent it —
			// not Nexara's own dialogs, which always do, but any script.
			"state": haStateParam().AsOptional(),
			"group": optString(128, "<name>",
				"HA group to place the resource in. Omitted or empty leaves it ungrouped."),
			"max_restart":  optCount(64, "Times the HA manager may restart the guest on its own node before relocating it."),
			"max_relocate": optCount(64, "Times the HA manager may relocate the guest before giving up."),
			"comment":      haComment("resource"),
			"failback": haFlag("Move the guest back to its highest-priority node once that node returns. " +
				"Omitted leaves Proxmox's default rather than writing 0."),
		}),
		Handler: h.CreateResource,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        haScope + "/resources/:sid",
		Description: "Read one HA resource's configuration.",
		Group:       "High Availability",
		Permissions: clusterCheck("view", "ha"),
		Parameters:  clusterParams(apischema.Properties{"sid": haSIDParam}),
		Handler:     h.GetResource,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   haScope + "/resources/:sid",
		Description: "Change an HA resource's configuration. Every parameter is optional; " +
			"the ones omitted are left as they are.",
		Group:       "High Availability",
		Permissions: clusterCheck("manage", "ha"),
		Parameters: clusterParams(apischema.Properties{
			"sid":   haSIDParam,
			"state": haStateParam().AsOptional(),
			"group": optString(128, "<name>",
				"HA group to move the resource into. An EMPTY value removes it from its group, which is "+
					"what the editor sends when \"none\" is chosen."),
			"max_restart":  optCount(64, "Times the HA manager may restart the guest on its own node before relocating it."),
			"max_relocate": optCount(64, "Times the HA manager may relocate the guest before giving up."),
			"comment":      haComment("resource"),
			"failback":     haFlag("Move the guest back to its highest-priority node once that node returns."),
			"digest":       haDigest,
		}),
		Handler: h.UpdateResource,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        haScope + "/resources/:sid",
		Description: "Take a guest out of HA management. The guest itself is untouched.",
		Group:       "High Availability",
		Permissions: clusterCheck("manage", "ha"),
		Parameters:  clusterParams(apischema.Properties{"sid": haSIDParam}),
		Handler:     h.DeleteResource,
	})

	// ── Groups ────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   haScope + "/groups",
		Description: "List the cluster's HA groups. Answers with an empty list on Proxmox VE 9, " +
			"which migrated groups to HA rules and soft-disables the groups API.",
		Group:       "High Availability",
		Permissions: clusterCheck("view", "ha"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListGroups,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        haScope + "/groups",
		Description: "Create an HA group over a set of nodes. Refused on Proxmox VE 9, which uses HA rules instead.",
		Group:       "High Availability",
		Permissions: clusterCheck("manage", "ha"),
		Parameters: clusterParams(apischema.Properties{
			"group": {
				Type: apischema.String,
				// The CREATE rule is Proxmox's own configid, which is
				// stricter than haConfigIDParam on purpose — see that
				// property's comment for why the path parameter is not.
				Format:      "pve-configid",
				Typetext:    "<name>",
				Description: "Name for the new group: 2-40 characters, starting with a letter.",
			},
			"nodes":      haNodesParam(),
			"restricted": haFlag("Confine the group's guests to these nodes, rather than preferring them."),
			"nofailback": haFlag("Leave a guest where it is once a higher-priority node returns."),
			"comment":    haComment("group"),
		}),
		Handler: h.CreateGroup,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        haScope + "/groups/:group",
		Description: "Change an HA group. Every parameter is optional; the ones omitted are left as they are.",
		Group:       "High Availability",
		Permissions: clusterCheck("manage", "ha"),
		Parameters: clusterParams(apischema.Properties{
			"group":      haConfigIDParam("HA group name."),
			"nodes":      haNodesParam().AsOptional(),
			"restricted": haFlag("Confine the group's guests to these nodes, rather than preferring them."),
			"nofailback": haFlag("Leave a guest where it is once a higher-priority node returns."),
			"comment":    haComment("group"),
			"digest":     haDigest,
		}),
		Handler: h.UpdateGroup,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        haScope + "/groups/:group",
		Description: "Delete an HA group. Guests in it are left unmanaged by any group.",
		Group:       "High Availability",
		Permissions: clusterCheck("manage", "ha"),
		Parameters:  clusterParams(apischema.Properties{"group": haConfigIDParam("HA group name.")}),
		Handler:     h.DeleteGroup,
	})

	// ── Status ────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        haScope + "/status",
		Description: "Read the HA manager's current view of every managed resource and node.",
		Group:       "High Availability",
		Permissions: clusterCheck("view", "ha"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetStatus,
	})

	// ── Rules (PVE 8.3+) ──────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   haScope + "/rules",
		Description: "List the cluster's HA rules. Answers with an empty list on a Proxmox version " +
			"without the rules API.",
		Group:       "High Availability",
		Permissions: clusterCheck("view", "ha"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListRules,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        haScope + "/rules",
		Description: "Create an HA rule pinning guests to nodes, or keeping them together or apart.",
		Group:       "High Availability",
		Permissions: clusterCheck("manage", "ha"),
		Parameters: clusterParams(apischema.Properties{
			"rule": {
				Type:        apischema.String,
				Format:      "pve-configid",
				Typetext:    "<name>",
				Description: "Name for the new rule: 2-40 characters, starting with a letter.",
			},
			"type":      haRuleTypeParam(),
			"resources": haResourcesParam(),
			"nodes": optString(1024, "<node>[:<priority>][,<node>…]",
				"Nodes a node-affinity rule pins its resources to, each optionally with a priority."),
			"strict":   haFlag("Make a node-affinity rule a hard constraint rather than a preference."),
			"affinity": haAffinityParam(),
			"comment":  haComment("rule"),
		}),
		Handler: h.CreateRule,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   haScope + "/rules/:rule",
		Description: "Change an HA rule. Only type is required — Proxmox needs it on every write — " +
			"and the parameters omitted are left as they are.",
		Group:       "High Availability",
		Permissions: clusterCheck("manage", "ha"),
		Parameters: clusterParams(apischema.Properties{
			"rule":      haConfigIDParam("HA rule name."),
			"type":      haRuleTypeParam(),
			"resources": haResourcesParam().AsOptional(),
			"nodes": optString(1024, "<node>[:<priority>][,<node>…]",
				"Nodes a node-affinity rule pins its resources to, each optionally with a priority."),
			"strict":   haFlag("Make a node-affinity rule a hard constraint rather than a preference."),
			"affinity": haAffinityParam(),
			"comment":  haComment("rule"),
			"disable": haFlag("Suspend the rule without deleting it. Sending 0 re-enables it, which the client " +
				"turns into a property DELETE rather than disable=0 — see UpdateHARule in client_ha.go."),
			"digest": haDigest,
		}),
		Handler: h.UpdateRule,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   haScope + "/rules/:rule",
		Description: "Delete an HA rule. Deliberately idempotent, matching Proxmox: deleting a rule that " +
			"is already gone succeeds, and is audited as the no-op it was.",
		Group:       "High Availability",
		Permissions: clusterCheck("manage", "ha"),
		Parameters:  clusterParams(apischema.Properties{"rule": haConfigIDParam("HA rule name.")}),
		Handler:     h.DeleteRule,
	})

	// ── Manager ───────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        haScope + "/manager-status",
		Description: "Read the HA manager's own status, including which node holds the manager lock.",
		Group:       "High Availability",
		Permissions: clusterCheck("view", "ha"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetManagerStatus,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   haScope + "/arm",
		Description: "Re-arm the HA stack cluster-wide after a maintenance window. " +
			"Requires Proxmox VE 9.2 or newer.",
		Group:       "High Availability",
		Permissions: clusterCheck("manage", "ha"),
		Parameters:  clusterParams(nil),
		Handler:     h.ArmHA,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   haScope + "/disarm",
		Description: "Disarm the HA stack cluster-wide for planned maintenance, so fencing and recovery " +
			"stand down. Requires Proxmox VE 9.2 or newer.",
		Group:       "High Availability",
		Permissions: clusterCheck("manage", "ha"),
		Parameters: clusterParams(apischema.Properties{
			"resource_mode": {
				Type: apischema.String,
				// An enum rather than a plain string, because unlike the
				// Proxmox vocabularies elsewhere in this file the handler
				// ALREADY refused anything but these two by name. The
				// declaration states the same rule one layer earlier, and
				// the docs gain the two values.
				Enum:     []string{"freeze", "ignore"},
				Typetext: "<freeze|ignore>",
				Description: "What to do with managed resources while HA is disarmed: freeze their state, " +
					"or ignore them entirely.",
			},
		}),
		Handler: h.DisarmHA,
	})
}

// haStateParam is the requested state of an HA resource.
//
// No Enum, for the reason optString's doc comment gives at length: this is
// Proxmox's own vocabulary, which Proxmox owns and versions, and an Enum
// copied here would date on the next release and reject a value the
// cluster in front of the operator accepts. The Typetext carries the
// values a reader needs without the schema claiming to be their authority.
func haStateParam() apischema.Property {
	return apischema.Property{
		Type:      apischema.String,
		MinLength: apischema.Ptr(1),
		MaxLength: apischema.Ptr(32),
		Typetext:  "<started|stopped|disabled|ignored>",
		Description: "Requested state for the resource. started is what the create dialog sends, and is " +
			"what puts the guest under active HA management.",
	}
}

// haNodesParam is a comma-separated node list, optionally with per-node
// priorities ("pve-01:100,pve-02"). It carries no pattern: the priority
// suffix, the separator and the ordering are all Proxmox's, and a regex
// here would be a second, drifting copy of a rule PVE already enforces
// with a message of its own.
func haNodesParam() apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		MinLength:   apischema.Ptr(1),
		MaxLength:   apischema.Ptr(1024),
		Typetext:    "<node>[:<priority>][,<node>…]",
		Description: "Nodes in the group, each optionally with a priority — higher wins.",
	}
}

// haRuleTypeParam is the rule kind Proxmox's HA rules API takes.
//
// Deliberately NOT an enum. PVE 8.3 shipped node-affinity and
// resource-affinity and 9.x may add more, and haRuleToResponse in
// handlers/drs.go already has a default branch for a type it does not
// recognise — so the code is written to pass an unknown type through, and
// an enum here would be the one place that refused to.
func haRuleTypeParam() apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		MinLength:   apischema.Ptr(1),
		MaxLength:   apischema.Ptr(64),
		Typetext:    "<node-affinity|resource-affinity>",
		Description: "Rule kind. Proxmox requires it on every write, including an update that changes nothing else.",
	}
}

// haResourcesParam is the comma-separated SID list a rule applies to.
func haResourcesParam() apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		MinLength:   apischema.Ptr(1),
		MaxLength:   apischema.Ptr(4096),
		Typetext:    "<type:vmid>[,<type:vmid>…]",
		Description: `Guests the rule applies to, as HA resource ids: "vm:100,ct:101".`,
	}
}

// haAffinityParam is the direction of a resource-affinity rule.
//
// No enum, matching haRuleTypeParam: positive and negative are Proxmox's
// vocabulary, and handlers/drs.go reads the value back with an if rather
// than a closed switch.
func haAffinityParam() apischema.Property {
	return optString(32, "<positive|negative>",
		"For a resource-affinity rule: positive keeps the guests together, negative keeps them apart.")
}
