package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, withParams, optString and requiredNode — lives in
// registry_vms.go, where the first migrated domain defined it.

// replicationScope is the path prefix every replication route hangs off.
const replicationScope = clusterScope + "/replication"

// replicationJobIDPattern is Proxmox's replication job id: the guest's
// VMID, a dash, and the job number — "100-0".
//
// The CREATE body and the PATH parameter share it deliberately, the same
// way the Ceph pool name does: a job this API can create has to be a job
// this API can address, and the direction that bites is a create rule
// looser than the path rule, which produces a job the operator then cannot
// edit or delete.
//
// It also keeps a traversal segment out of the Proxmox path, which
// GetReplicationJob and its siblings build by concatenation with
// url.PathEscape — and PathEscape leaves ".." alone.
const replicationJobIDPattern = `^\d{1,12}-\d{1,9}$`

// replicationJobIDParam is the job id as a PATH parameter.
var replicationJobIDParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     replicationJobIDPattern,
	MaxLength:   apischema.Ptr(24),
	Typetext:    "<vmid>-<n>",
	Description: `Proxmox replication job id, e.g. "100-0".`,
}

// replicationNodeParam is the ?node= every per-node route requires.
//
// Source is stated EXPLICITLY rather than inferred, and that is
// load-bearing on the trigger route: ResolveSource reads an undeclared
// source from the body on a mutating verb, so on POST …/trigger the
// parameter would be looked for in a body the caller never sends. The two
// GETs would infer query correctly; declaring it on all three keeps the
// three routes saying the same thing and survives a verb change.
var replicationNodeParam = func() apischema.Property {
	p := requiredNode("Node to address. Replication is a per-node service, so this is the node the job " +
		"runs on — normally the job's source node.")
	p.Source = apischema.SourceQuery
	return p
}()

// registerReplicationEndpoints declares the 8 replication routes served by
// ReplicationHandler.
//
// All 8 declare a plain Check: four on view:replication, four on
// manage:replication, each the single static requireClusterPerm call its
// handler carried. None is Deferred and none is Advisory.
//
// Three of them read ?node= and refused a missing one with a hand-written
// "Node query parameter is required". Those are the first REQUIRED query
// parameters in the registry, and the refusal is now the schema's — which
// also means the node name is format-checked before it reaches
// proxmox.validateNodeName rather than after.
func registerReplicationEndpoints(reg *Registry, h *handlers.ReplicationHandler) {
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        replicationScope,
		Description: "List the cluster's storage replication jobs.",
		Group:       "Replication",
		Permissions: clusterCheck("view", "replication"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListJobs,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        replicationScope,
		Description: "Create a storage replication job for one guest.",
		Group:       "Replication",
		Permissions: clusterCheck("manage", "replication"),
		Parameters: clusterParams(apischema.Properties{
			// Declared as job_id with "id" as an ALIAS, and that is not
			// cosmetic. checkPathParams refuses a parameter named "id" that
			// resolves to anything but the path, because clusterIDFromParam
			// reads TWO names — cluster_id, falling back to id — so a body
			// "id" is a name the permission gate also reads. On THIS path
			// the fallback cannot fire (:cluster_id is present, so the gate
			// never reaches the fallback), but the refusal is deliberately
			// about the NAME rather than about whether today's path happens
			// to make it safe, and routing around a fail-closed guard is
			// how the hole it protects gets reopened by the next path edit.
			//
			// The alias is what keeps every existing caller working: the
			// create dialog sends {"id": "100-0"}, and Alias exists for
			// exactly this — a second spelling of one parameter, read from
			// the same source. job_id is the canonical name because it is
			// what the other seven routes spell in their paths.
			"job_id": {
				Type:      apischema.String,
				Alias:     "id",
				Pattern:   replicationJobIDPattern,
				MaxLength: apischema.Ptr(24),
				Typetext:  "<vmid>-<n>",
				Description: `Id for the new job: the guest's VMID, a dash, and a job number — "100-0". ` +
					`Proxmox derives which guest the job replicates from it. Also accepted as "id", ` +
					"which is what Nexara's own create dialog sends.",
			},
			"type": {
				Type:     apischema.String,
				Optional: true,
				// The value the handler substituted for a missing type, and
				// the only one Proxmox defines today. Stated as the Default
				// so the docs answer "what happens if I leave this out".
				Default: "local",
				// MinLength, because a Default does NOT apply to an explicit
				// "": apischema treats the empty string as a value the caller
				// supplied. The handler's `if req.Type == "" { "local" }`
				// used to absorb that, and without this the empty string
				// would reach Proxmox and come back as a 400 naming no field.
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(32),
				Typetext:    "<local>",
				Description: "Replication kind. Proxmox has only local replication today.",
			},
			"target":   requiredNode("Node to replicate onto."),
			"schedule": replicationScheduleParam(),
			"rate":     replicationRateParam(),
			"comment":  replicationCommentParam(),
			"disable":  replicationDisableParam(),
		}),
		Handler: h.CreateJob,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        replicationScope + "/:job_id",
		Description: "Read one replication job's configuration.",
		Group:       "Replication",
		Permissions: clusterCheck("view", "replication"),
		Parameters:  clusterParams(apischema.Properties{"job_id": replicationJobIDParam}),
		Handler:     h.GetJob,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   replicationScope + "/:job_id",
		Description: "Change a replication job. Every parameter is optional; the ones omitted are left " +
			"as they are.",
		Group:       "Replication",
		Permissions: clusterCheck("manage", "replication"),
		Parameters: clusterParams(apischema.Properties{
			"job_id":   replicationJobIDParam,
			"schedule": replicationScheduleParam(),
			"rate":     replicationRateParam(),
			"comment":  replicationCommentParam(),
			"disable":  replicationDisableParam(),
			"remove_job": optString(32, "<local|full>",
				"Mark the job for removal (pve-guest-common ReplicationConfig remove_job): the job "+
					"removes its local replication snapshots, with full also the replicated volumes on "+
					"the target, and then removes itself. Nexara's own UI does not send this, but the "+
					"endpoint has always forwarded it."),
		}),
		Handler: h.UpdateJob,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   replicationScope + "/:job_id",
		// Nexara sends neither keep nor force (proxmox.DeleteReplicationJob), so pve-manager
		// PVE/API2/ReplicationConfig.pm delete marks a local job remove_job=full, and
		// pve-guest-common PVE/Replication.pm removes the target volumes and the source
		// replication snapshots on the runner's next pass, even for a disabled job.
		Description: "Delete a replication job. Proxmox also deletes the data already replicated onto " +
			"the target node, and the job's replication snapshots on the source, on the replication " +
			"runner's next pass, even if the job is disabled. The guest and its own disks are untouched.",
		Group:       "Replication",
		Permissions: clusterCheck("manage", "replication"),
		Parameters:  clusterParams(apischema.Properties{"job_id": replicationJobIDParam}),
		Handler:     h.DeleteJob,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        replicationScope + "/:job_id/trigger",
		Description: "Run a replication job now, ahead of its schedule.",
		Group:       "Replication",
		Permissions: clusterCheck("manage", "replication"),
		Parameters: clusterParams(apischema.Properties{
			"job_id": replicationJobIDParam,
			"node":   replicationNodeParam,
		}),
		Handler: h.TriggerSync,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        replicationScope + "/:job_id/status",
		Description: "Read a replication job's live status from the node that runs it.",
		Group:       "Replication",
		Permissions: clusterCheck("view", "replication"),
		Parameters: clusterParams(apischema.Properties{
			"job_id": replicationJobIDParam,
			"node":   replicationNodeParam,
		}),
		Handler: h.GetStatus,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        replicationScope + "/:job_id/log",
		Description: "Read a replication job's log from the node that runs it.",
		Group:       "Replication",
		Permissions: clusterCheck("view", "replication"),
		Parameters: clusterParams(apischema.Properties{
			"job_id": replicationJobIDParam,
			"node":   replicationNodeParam,
			"limit": {
				Type:     apischema.Integer,
				Optional: true,
				// The value the handler substituted for a missing ?limit=.
				Default: 500,
				Minimum: apischema.Ptr(1.0),
				Maximum: apischema.Ptr(10000.0),
				// The handler forwards the limit only when it is positive,
				// so 0 used to mean "let Proxmox decide". A minimum of 1
				// removes that spelling rather than keeping a value whose
				// meaning is "ignore the parameter I just sent"; omitting
				// the key is how a caller asks for the default, and that
				// still works.
				Typetext:    "<integer>",
				Description: "Maximum log lines to return.",
			},
		}),
		Handler: h.GetLog,
	})
}

// replicationScheduleParam is the job's calendar event.
//
// No pattern: Proxmox's calendar-event syntax is its own — "*/15",
// "mon..fri 22:00", "*-*-* 02:30:00" — and a regex here would be a
// drifting second copy of a grammar PVE parses itself and rejects with a
// message of its own.
//
// It carries no format either, and that is the empty-string sentinel this
// domain has: the edit dialog sends schedule unconditionally, so clearing
// the field arrives as "". The client drops an empty schedule rather than
// forwarding it (see UpdateReplicationJob), so "" and "absent" have always
// meant the same thing to Proxmox — and any format would 400 the save.
func replicationScheduleParam() apischema.Property {
	return optString(256, "<calendar event>",
		"When the job runs, as a Proxmox calendar event such as \"*/15\". "+
			"Empty or omitted leaves the stored schedule alone.")
}

// replicationRateParam is the bandwidth cap, in MB/s, as Proxmox spells it.
func replicationRateParam() apischema.Property {
	return optString(32, "<number>",
		"Bandwidth cap in MB/s. Omitted means no limit.")
}

// replicationCommentParam is the job's free-text note. Like schedule, the
// edit dialog sends it unconditionally, so an empty value is a real one.
func replicationCommentParam() apischema.Property {
	return optString(4096, "<string>",
		"Free-text note stored on the job. An empty value clears it.")
}

// replicationDisableParam is the 0/1 flag Proxmox uses for "do not run
// this job".
//
// It carries NO Default: the handler forwards it as a *int, so omitting
// the key means "do not send this property" — different from sending 0,
// which re-enables a disabled job. The *int comes from p.OptInt, which a
// default cannot fool (apischema.Property.Default), so a Default of 0 would
// not re-enable anything; it would document every edit as a re-enable.
func replicationDisableParam() apischema.Property {
	return apischema.Property{
		Type:        apischema.Integer,
		Optional:    true,
		Minimum:     apischema.Ptr(0.0),
		Maximum:     apischema.Ptr(1.0),
		Typetext:    "<0|1>",
		Description: "Suspend the job without deleting it. Sending 0 puts it back on its schedule.",
	}
}
