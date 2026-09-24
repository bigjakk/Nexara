package api

import (
	"slices"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, withParams, emptyOrNodeName, optFlag,
// optString, optCount and requiredNode — lives in registry_vms.go, where the
// first migrated domain defined it. pbsScope, and the Deferred reasoning
// this file inherits, live in registry_pbs.go; pathPrefix is the registry's
// own, in registry.go.

// pbsBackupScope is the per-server prefix the 19 PBS backup routes hang
// off. It is NOT the /pbs-servers/:id prefix registry_pbs.go declares: the
// PBSHandler routes spell the server's id :id, and these have always
// spelled it :pbs_id.
const pbsBackupScope = pbsScope + "/:pbs_id"

// pbsSafeIDPattern is the shape of every caller-supplied PBS object id that
// becomes a SEGMENT of a PBS request path — a datastore name, a sync job id,
// a verify job id.
//
// It exists for the reason the catalogue's pve-object-id does.
// internal/proxmox/pbs_client.go builds those paths by concatenation with
// url.PathEscape, and PathEscape escapes "/" but leaves "." and ".." alone,
// so an un-anchored value reaches PBS as a bare dot segment. PBS itself
// refuses one — its normalize_path rejects any component starting with "."
// (read from upstream source; pbs_client.go has the detail) — but a reverse
// proxy that normalises the path first resolves it upward instead: DELETE
// /admin/datastore/../snapshots onto /admin/snapshots, POST
// /admin/sync/../run onto /admin/run, neither what the caller named. The
// handlers only ever checked that the segment was non-empty, which ".."
// satisfies.
//
// The rule is the catalogue's pbs-safe-id, which is PBS's own
// PROXMOX_SAFE_ID character for character — so nothing PBS would accept is
// refused here and a datastore created outside Nexara cannot become
// unmanageable through it.
var pbsSafeIDPattern = apischema.Rule("pbs-safe-id")

// emptyOrPBSSafeID is pbsSafeIDPattern with the empty string allowed, for
// the two QUERY parameters where "" has always meant "do not filter" —
// ListSnapshots branches on `datastore != ""` and filterPruneJobsByStore on
// `store == ""`.
//
// The catalogue DERIVES it from pbs-safe-id rather than restating it, which
// is what stops the pair from parting company, and keeps the anchor on BOTH
// branches of the alternation: Go's regexp is a substring search, so
// `^$|[A-Za-z0-9_]...$` would match "..foo" from index 2 and let the
// traversal straight back in.
var emptyOrPBSSafeID = apischema.Rule("pbs-safe-id-or-empty")

// pbsDatastoreParam is a PBS datastore name as a PATH parameter.
//
// The 64-character cap is deliberately looser than PBS's own 32: it is here
// to keep a path segment finite, not to re-state a limit PBS enforces and
// may revise, and a name longer than PBS allows cannot exist to be named.
var pbsDatastoreParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     pbsSafeIDPattern,
	MaxLength:   apischema.Ptr(64),
	Typetext:    "<datastore>",
	Description: "PBS datastore name, as GET /pbs-servers/{pbs_id}/datastores reports it.",
}

// pbsJobIDParam is a PBS sync or verify job id as a PATH parameter. Same
// shape and same reason as pbsDatastoreParam; PBS assigns these ids from
// the same format.
var pbsJobIDParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     pbsSafeIDPattern,
	MaxLength:   apischema.Ptr(64),
	Typetext:    "<job id>",
	Description: "PBS job id, as the matching job listing reports it.",
}

// pbsTaskUPIDParam is a PBS task id as it arrives in a URL.
//
// It is NOT upidParam (registry_vms.go), and the difference is the anchor.
// A VM task route builds its path out of two caller-supplied halves and the
// client guards BOTH: the route unescapes the value, extractNodeFromUPID
// takes the node out of it, and that node goes through
// proxmox.validateNodeName while the UPID itself goes through
// proxmox.validateTaskUPID — each of which refuses "." and "..", so a
// traversal cannot ride in on either half. That is the whole reason
// upidParam can carry no anchor of its own. The PBS routes address the
// fixed node "localhost", so there is no node half here to carry a guard:
// what the route would build is /nodes/localhost/tasks/../status.
//
// The two claims above about the node half and the UPID guard are about the
// CLIENT rather than this file, and both moved after this declaration was
// written, which is why the anchor stays. validateNodeName
// (internal/proxmox/client.go) began refusing a bare "." only when it was
// changed to delegate to validatePathSegment; before that the node half of a
// VM task route accepted one. And the PBS UPID is now refused at the choke
// point too, by validatePBSTaskUPID (pbs_client.go). What the anchor adds is
// one cheap refusal a layer earlier, on the value as it ARRIVES — the only
// layer that does not depend on the client keeping its guard. (PBS would refuse the ".." path
// too: its REST server's normalize_path rejects any path component that
// starts with "." — validatePBSTaskUPID cites the upstream source — but
// Nexara should not be the layer that sends it.)
//
// The anchor is a leading alphanumeric and nothing more. A UPID starts with
// the literal "UPID" in both the raw form and the percent-encoded form the
// frontend sends (encodeURIComponent never encodes an alphanumeric), so it
// cannot refuse a real value — while a pattern over the REST of the string
// would have to guess which of the two forms arrived, which is exactly why
// upidParam carries none.
var pbsTaskUPIDParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     `^[A-Za-z0-9]`,
	MinLength:   apischema.Ptr(1),
	MaxLength:   apischema.Ptr(512),
	Typetext:    "<UPID>",
	Description: "PBS task id (UPID), percent-encoded.",
}

// pveBackupJobIDParam is a PVE vzdump job id as a PATH parameter.
//
// Same anchor, different server: internal/proxmox/client_backup.go builds
// /cluster/backup/<id> with url.PathEscape — to change or delete the job, and
// to read it back for a run — and the handlers only checked the segment was
// non-empty. PVE assigns these ids itself (a "backup-" prefix and a uuid), so
// the class is far wider than anything PVE produces — it is a traversal
// anchor, not a format. The rule is the catalogue's pve-object-id, shared with
// the firewall, SDN and metric-server ids that are anchored for the same
// reason.
var pveBackupJobIDParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     apischema.Rule("pve-object-id"),
	MaxLength:   apischema.Ptr(128),
	Typetext:    "<job id>",
	Description: "PVE backup job id, as GET /clusters/{cluster_id}/backup-jobs reports it.",
}

// pbsParams is the one path parameter every per-server backup route carries.
func pbsParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"pbs_id": apischema.StdOption("pbs-id"),
	}, extra)
}

// pbsStoreParams is pbsParams for the nine routes that also name a
// datastore.
func pbsStoreParams(extra apischema.Properties) apischema.Properties {
	return pbsParams(withParams(apischema.Properties{"store": pbsDatastoreParam}, extra))
}

// backupDeferredReason is the Deferred justification the 19 per-server
// routes share.
//
// It is requirePBSPerm (handlers/backup.go) rather than PBSHandler's inline
// version, but it makes the identical split, and pbsDeferredReason
// (registry_pbs.go) describes it for the sibling routes in the same words:
// load the server row, then gate CLUSTER-SCOPED on the cluster it is
// attached to when there is one, and on the instance-wide grant when the
// server is standalone. A permission decided by a DB lookup cannot be
// hoisted into middleware, which runs before any query.
const backupDeferredReason = "the scope depends on a DB lookup: requirePBSPerm loads the PBS server row " +
	"and gates on the cluster it is attached to when there is one, and on the instance-wide grant when " +
	"the server is standalone — the same split PBSHandler makes inline for the /pbs-servers routes"

// snapshotRefParams is the (type, id, time) triple that names one PBS
// snapshot, shared by the delete, protect and notes routes.
//
// All three are REQUIRED, which is exactly what each handler enforced:
// `req.BackupType == "" || req.BackupID == "" || req.BackupTime == 0` came
// back as one combined message. A Minimum of 1 on the timestamp is what
// keeps the zero refusal — apischema would otherwise accept an explicit 0 as
// a supplied value.
//
// The two strings carry no format. They reach PBS as QUERY parameters
// (url.Values), never as path segments, so what the schema owes them is a
// length bound rather than an anchor, and PBS owns the vocabulary of backup
// types it accepts.
//
// source is SourceBody on every one of them, and on the DELETE route that is
// load-bearing: ResolveSource infers QUERY for DELETE, while the handler has
// always read these from the body — which is what the delete dialog sends.
func snapshotRefParams(source apischema.Source) apischema.Properties {
	return apischema.Properties{
		"backup_type": {
			Type:        apischema.String,
			Source:      source,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(32),
			Typetext:    "<vm|ct|host>",
			Description: "Kind of guest the snapshot holds, as PBS records it.",
		},
		"backup_id": {
			Type:        apischema.String,
			Source:      source,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(255),
			Typetext:    "<id>",
			Description: "Backup group id, which for a Proxmox guest is its VMID.",
		},
		"backup_time": {
			Type:   apischema.Integer,
			Source: source,
			// 1 rather than 0: zero was refused outright before the migration,
			// and it is not a snapshot time any PBS ever recorded.
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(99999999999.0),
			Typetext:    "<unix timestamp>",
			Description: "Snapshot time as a Unix timestamp in seconds.",
		},
	}
}

// registerBackupEndpoints declares the 28 routes served by BackupHandler.
//
// The split is the widest of any domain so far, and it is the domain's own
// shape rather than an accident of migration:
//
//	19  Deferred  the /pbs-servers/:pbs_id routes, every one of which gates
//	              through requirePBSPerm — see backupDeferredReason
//	 7  Check     the cluster-scoped restore and backup-job routes, whose
//	              cluster is the first parameter of their own path
//	 2  Advisory  the two instance-wide listings, which filter through
//	              accessibleClusters instead of gating
//
// None of the 19 can hoist and none of the 2 can gate, so seven hand-placed
// calls move into middleware and the rest stay where they are. The tally in
// registry_backup_test.go states that out loud.
func registerBackupEndpoints(reg *Registry, h *handlers.BackupHandler) {
	// ── PBS datastores ────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/datastores",
		// Deferred renders as the bare word "deferred", so the permission an
		// operator building a role needs is spelled out here. Every route in
		// this group says it the same way.
		Description: "List the datastores a PBS server holds. Requires view:backup on the server's " +
			"cluster, or the instance-wide view:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters:  pbsParams(nil),
		Handler:     h.ListDatastores,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/datastores/status",
		Description: "Read every datastore's usage and deduplication figures in one call. Requires " +
			"view:backup on the server's cluster, or the instance-wide view:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters:  pbsParams(nil),
		Handler:     h.GetDatastoreStatus,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   pbsBackupScope + "/datastores/:store/gc",
		Description: "Start garbage collection on a datastore, which reclaims the chunks no snapshot " +
			"references any more. Runs as a background PBS task. Requires manage:backup on the server's " +
			"cluster, or the instance-wide manage:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters:  pbsStoreParams(nil),
		Handler:     h.TriggerGC,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   pbsBackupScope + "/datastores/:store/snapshots",
		Description: "Delete one snapshot from a datastore. The snapshot is named in the request BODY, " +
			"not the query string. Requires delete:backup on the server's cluster, or the instance-wide " +
			"delete:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		// SourceBody explicitly: this is a DELETE, and ResolveSource would
		// otherwise infer the query string while the handler reads the body.
		Parameters: pbsStoreParams(snapshotRefParams(apischema.SourceBody)),
		Handler:    h.DeleteSnapshot,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   pbsBackupScope + "/datastores/:store/snapshots/protect",
		Description: "Set or clear a snapshot's protected flag, which exempts it from pruning and from " +
			"deletion. Requires manage:backup on the server's cluster, or the instance-wide manage:backup " +
			"when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters: pbsStoreParams(withParams(snapshotRefParams(apischema.SourceAuto), apischema.Properties{
			// optFlag, i.e. Optional with Default false — NOT optTristateBool,
			// which has no Default: the handler sends protected=true/false
			// either way, so omitting it has always meant "unprotect", and the
			// Default states that rather than leaving a reader to infer it.
			"protected": optFlag("true protects the snapshot, false releases it. Omitted releases it."),
		})),
		Handler: h.ProtectSnapshot,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   pbsBackupScope + "/datastores/:store/snapshots/notes",
		Description: "Replace a snapshot's free-text note. Requires manage:backup on the server's " +
			"cluster, or the instance-wide manage:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters: pbsStoreParams(withParams(snapshotRefParams(apischema.SourceAuto), apischema.Properties{
			// Optional and unbounded-below: the handler never checked it, and
			// an empty comment is how the notes dialog clears one.
			//
			// The 4096-character ceiling is NEW — the handler had none — and it
			// is chosen against the wire rather than against PBS: the note
			// travels to PBS as a URL QUERY parameter (UpdateSnapshotNotes
			// builds ?notes=…), so a longer one runs into the server's
			// request-line limit and fails upstream with a message that names
			// nothing.
			"comment": optString(4096, "<string>", "Note to store on the snapshot. Empty clears it."),
		})),
		Handler: h.UpdateSnapshotNotes,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   pbsBackupScope + "/datastores/:store/prune",
		Description: "Apply a retention policy to a datastore, optionally as a dry run that only reports " +
			"what it would remove. Every keep-* count is optional and 0 means \"do not send this rule\", " +
			"which is how the prune dialog spells an unused one. Requires manage:backup on the server's " +
			"cluster, or the instance-wide manage:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters:  pbsStoreParams(pruneDatastoreParams()),
		Handler:     h.PruneDatastore,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/datastores/:store/rrd",
		Description: "Read a datastore's RRD performance series — transfer rate and IOPS. Requires " +
			"view:backup on the server's cluster, or the instance-wide view:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters: pbsStoreParams(apischema.Properties{
			// No Enum on either, deliberately, and the contrast with the
			// /metrics timeframe below is the point: THAT one indexes a table
			// Nexara owns, so its vocabulary is ours to close, while these two
			// are PBS's own. They reach it as query parameters rather than path
			// segments (url.QueryEscape), and PBS rejects a value it does not
			// know with a message of its own; a copy here would date on the
			// next PBS release and refuse a timeframe the server in front of
			// the operator accepts.
			//
			// The Defaults are the ones the handler substituted for a missing
			// parameter. An EXPLICIT empty value does not reach them —
			// apischema counts "" as supplied — but it is not a behaviour
			// change either: proxmox.GetDatastoreRRD substitutes the same two
			// for an empty string before building the request.
			"timeframe": {
				Type:        apischema.String,
				Optional:    true,
				Default:     "hour",
				MaxLength:   apischema.Ptr(32),
				Typetext:    "<hour|day|week|month|year>",
				Description: "RRD window to read.",
			},
			"cf": {
				Type:        apischema.String,
				Optional:    true,
				Default:     "AVERAGE",
				MaxLength:   apischema.Ptr(32),
				Typetext:    "<AVERAGE|MAX>",
				Description: "RRD consolidation function.",
			},
		}),
		Handler: h.GetDatastoreRRD,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/datastores/:store/config",
		Description: "Read a datastore's PBS configuration — its backing path, notification settings and " +
			"tuning. Requires view:backup on the server's cluster, or the instance-wide view:backup when " +
			"it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters:  pbsStoreParams(nil),
		Handler:     h.GetDatastoreConfig,
	})

	// ── Snapshots, jobs and tasks ─────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/snapshots",
		Description: "List the snapshots Nexara has collected for a PBS server, optionally narrowed to " +
			"one datastore. Requires view:backup on the server's cluster, or the instance-wide " +
			"view:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters: pbsParams(apischema.Properties{
			// The empty string has always meant "every datastore" — the handler
			// branches on `datastore != ""` — so this carries the anchor pattern
			// with an empty alternative rather than pbsDatastoreParam's, which
			// would 400 a caller who spells "no filter" that way.
			"datastore": {
				Type:        apischema.String,
				Optional:    true,
				Pattern:     emptyOrPBSSafeID,
				MaxLength:   apischema.Ptr(64),
				Typetext:    "<datastore>",
				Description: "Datastore to narrow to. Empty or omitted returns every datastore's snapshots.",
			},
		}),
		Handler: h.ListSnapshots,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/sync-jobs",
		Description: "List the sync jobs Nexara has collected for a PBS server. Requires view:backup on " +
			"the server's cluster, or the instance-wide view:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters:  pbsParams(nil),
		Handler:     h.ListSyncJobs,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   pbsBackupScope + "/sync-jobs/:job_id/run",
		Description: "Run a PBS sync job now. Runs as a background PBS task. Requires manage:backup on " +
			"the server's cluster, or the instance-wide manage:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters:  pbsParams(apischema.Properties{"job_id": pbsJobIDParam}),
		Handler:     h.RunSyncJob,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/prune-jobs",
		Description: "List the prune jobs configured on a PBS server, read live rather than from a " +
			"collected table, optionally narrowed to one datastore with ?store=. Requires view:backup on " +
			"the server's cluster, or the instance-wide view:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters: pbsParams(apischema.Properties{
			// Same empty sentinel as ?datastore= above: filterPruneJobsByStore
			// reads "" as "no filter".
			"store": {
				Type:        apischema.String,
				Optional:    true,
				Pattern:     emptyOrPBSSafeID,
				MaxLength:   apischema.Ptr(64),
				Typetext:    "<datastore>",
				Description: "Datastore to narrow to. Empty or omitted returns every job.",
			},
		}),
		Handler: h.ListPruneJobs,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/verify-jobs",
		Description: "List the verify jobs Nexara has collected for a PBS server. Requires view:backup " +
			"on the server's cluster, or the instance-wide view:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters:  pbsParams(nil),
		Handler:     h.ListVerifyJobs,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   pbsBackupScope + "/verify-jobs/:job_id/run",
		Description: "Run a PBS verify job now. Runs as a background PBS task. Requires manage:backup on " +
			"the server's cluster, or the instance-wide manage:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters:  pbsParams(apischema.Properties{"job_id": pbsJobIDParam}),
		Handler:     h.RunVerifyJob,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/tasks",
		Description: "List a PBS server's recent tasks. Requires view:backup on the server's cluster, or " +
			"the instance-wide view:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters: pbsParams(apischema.Properties{
			"limit": {
				Type:     apischema.Integer,
				Optional: true,
				// The value the handler substituted for a missing, EMPTY,
				// unparseable or out-of-range ?limit=. The bounds now REFUSE
				// the last three rather than silently substituting this — the
				// same trade the migration and CVE listings made, and for the
				// same reason: a page size the caller did not choose is
				// indistinguishable from one they did, so a paging bug reads as
				// missing data. (Empty is in that list because apischema counts
				// "" as a value the caller supplied, while the handler's
				// `if l := c.Query("limit"); l != ""` did not.)
				Default:     50,
				Minimum:     apischema.Ptr(1.0),
				Maximum:     apischema.Ptr(500.0),
				Typetext:    "<integer>",
				Description: "Maximum tasks to return.",
			},
		}),
		Handler: h.ListTasks,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/tasks/:upid",
		Description: "Get one PBS task's status. Requires view:backup on the server's cluster, or the " +
			"instance-wide view:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters:  pbsParams(apischema.Properties{"upid": pbsTaskUPIDParam}),
		Handler:     h.GetTaskStatus,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/tasks/:upid/log",
		Description: "Read one PBS task's log lines. Requires view:backup on the server's cluster, or " +
			"the instance-wide view:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters:  pbsParams(apischema.Properties{"upid": pbsTaskUPIDParam}),
		Handler:     h.GetTaskLog,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsBackupScope + "/metrics",
		Description: "Read the collected per-datastore metrics for a PBS server — the latest sample per " +
			"datastore, or a history window downsampled to a fixed bucket width. Requires view:backup on " +
			"the server's cluster, or the instance-wide view:backup when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: backupDeferredReason},
		Parameters: pbsParams(apischema.Properties{
			"timeframe": {
				Type: apischema.String,
				// Cloned rather than aliased: Property.Enum is only deep-copied
				// on the StdOption path, so sharing the package-level slice would
				// give every Server's schema the same backing array.
				Enum:     slices.Clone(handlers.PBSMetricsTimeframes),
				Optional: true,
				Default:  handlers.PBSMetricsLatestTimeframe,
				Typetext: "<latest|1h|6h|24h|7d>",
				Description: "latest returns the most recent sample per datastore; any other value is a " +
					"history window, each with its own bucket width chosen so the response stays a couple " +
					"of hundred points however long the window is. Unlike the RRD route's timeframe, " +
					"which is PBS's vocabulary, this one indexes a table Nexara owns — so an unrecognised " +
					"value is refused here rather than falling back to a window the caller did not ask for.",
			},
		}),
		Handler: h.GetDatastoreMetrics,
	})

	// ── Cluster-scoped: restore and vzdump jobs ───────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/restore",
		Description: "Restore a PBS snapshot onto this cluster as a guest. The archive is addressed " +
			"through the cluster's own PBS storage entry, so one must be configured in Proxmox first. " +
			"Runs as a background task.",
		Group:       "Backup",
		Permissions: clusterCheck("manage", "backup"),
		Parameters:  clusterParams(restoreBackupParams()),
		Handler:     h.RestoreBackup,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        clusterScope + "/backup",
		Description: "Run a one-off vzdump backup of a single guest on a node. Runs as a background task.",
		Group:       "Backup",
		Permissions: clusterCheck("manage", "backup"),
		Parameters: clusterParams(apischema.Properties{
			// A STRING rather than an integer, because that is the wire shape
			// this endpoint has always taken and vzdump's own --vmid accepts a
			// comma-separated list.
			"vmid": {
				Type:        apischema.String,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(512),
				Typetext:    "<vmid>[,<vmid>...]",
				Description: "Proxmox VMID to back up, or a comma-separated list of them.",
			},
			"node":     requiredNode("Node to run the backup on."),
			"storage":  optString(100, "<storage>", "Storage to write the archive to. Omitted lets Proxmox use the node's default."),
			"mode":     optString(32, "<snapshot|suspend|stop>", "vzdump mode. Omitted leaves Proxmox's default."),
			"compress": optString(32, "<0|1|gzip|lzo|zstd>", "Compression for the archive. Omitted leaves Proxmox's default."),
		}),
		Handler: h.TriggerBackup,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/backup-jobs",
		Description: "List the cluster's scheduled vzdump backup jobs, read live from Proxmox.",
		Group:       "Backup",
		Permissions: clusterCheck("view", "backup"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListBackupJobs,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/backup-jobs",
		Description: "Create a scheduled vzdump backup job. Nothing is required: the handler has always " +
			"relayed whatever the body named and let Proxmox refuse an unusable combination with a " +
			"message of its own.",
		Group:       "Backup",
		Permissions: clusterCheck("manage", "backup"),
		Parameters:  clusterParams(backupJobParams()),
		Handler:     h.CreateBackupJob,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   clusterScope + "/backup-jobs/:job_id",
		Description: "Change a scheduled vzdump backup job. Naming one guest selection clears the " +
			"others, and an explicitly empty node or comment clears that property — omitting either " +
			"leaves it as it is.",
		Group:       "Backup",
		Permissions: clusterCheck("manage", "backup"),
		Parameters:  clusterParams(withParams(backupJobParams(), apischema.Properties{"job_id": pveBackupJobIDParam})),
		Handler:     h.UpdateBackupJob,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        clusterScope + "/backup-jobs/:job_id",
		Description: "Delete a scheduled vzdump backup job. Archives it already produced are left alone.",
		Group:       "Backup",
		Permissions: clusterCheck("delete", "backup"),
		Parameters:  clusterParams(apischema.Properties{"job_id": pveBackupJobIDParam}),
		Handler:     h.DeleteBackupJob,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/backup-jobs/:job_id/run",
		Description: "Run a scheduled vzdump backup job now, outside its schedule, as the Proxmox GUI's " +
			"Run now does: the job's own settings are sent to vzdump on the node the job names, or, when " +
			"it names none, on every node Proxmox reports online. Whether a node starts a background task " +
			"is vzdump's decision: a node holding none of the job's guests usually answers that it has " +
			"nothing to back up, but an all-guests job starts a task there too unless the job has stop " +
			"set, and a VMID list naming a guest that no longer exists starts one on every node, which " +
			"then fails for that guest. Each task started is recorded like any other. Answers 200 with " +
			"tasks (a node and upid per task), skipped (nodes that found nothing to back up), errors (a " +
			"node and message per node where no backup started: not online, or refused), unconfirmed (a " +
			"node and message per node whose answer did not say whether a backup started — check the task " +
			"list before running the job again) and stops_running_backups (the job's stop flag, with which " +
			"vzdump stops any backup already running on every node that answered with a task or with " +
			"nothing to back up, and possibly on a node refused afterwards). Answers 502 " +
			"when no task was confirmed and a node failed or went unconfirmed, and 409 when the job's own " +
			"node is not online, in which case nothing is sent.",
		Group:       "Backup",
		Permissions: clusterCheck("manage", "backup"),
		Parameters:  clusterParams(apischema.Properties{"job_id": pveBackupJobIDParam}),
		Handler:     h.RunBackupJob,
	})

	// ── Instance-wide listings ────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pathPrefix + "pbs-snapshots",
		Description: "Find every PBS snapshot across every server for one backup id (a guest's VMID). " +
			"Filtered rather than gated: a cluster-bound server's snapshots need view:backup on its " +
			"cluster, and a standalone server's need it instance-wide.",
		Group: "Backup",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check: Check{Action: "view", Resource: "backup", Scope: ScopeCluster},
			Reason: "accessibleClusters(\"view\", \"backup\") builds the filter and the handler applies it " +
				"per PBS server — PermitsCluster for a cluster-bound one, HasGlobal for a standalone one; " +
				"the snapshots span every server, so there is no single cluster a gate could resolve",
		}},
		Parameters: apischema.Properties{
			// Required, matching the handler's own refusal of an empty one. The
			// value is a PBS backup group id, which for a Proxmox guest is its
			// VMID; it is used as a SQL parameter, never as a path segment.
			"backup_id": {
				Type:        apischema.String,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(255),
				Typetext:    "<vmid>",
				Description: "Backup group id to look up, which for a Proxmox guest is its VMID.",
			},
		},
		Handler: h.ListSnapshotsByBackupID,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pathPrefix + "backup-coverage",
		Description: "Report which guests are covered by a backup and which are not, across every " +
			"cluster the caller can see. Filtered rather than gated: view:backup decides which clusters' " +
			"guests appear, and view:veeam — resolved separately — decides where Veeam data may be " +
			"consulted, so a caller holding only the first gets the report as it was before Veeam existed.",
		Group: "Backup",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check: Check{Action: "view", Resource: "backup", Scope: ScopeCluster},
			Reason: "accessibleClusters(\"view\", \"backup\") builds the guest scope and a second " +
				"accessibleClusters(\"view\", \"veeam\") builds the Veeam scope; both are handed to " +
				"backupcoverage.Compute as per-cluster predicates, and the report spans every cluster " +
				"at once so there is none for a gate to resolve",
		}},
		Parameters: nil,
		Handler:    h.GetBackupCoverage,
	})
}

// pruneDatastoreParams is the body of POST .../datastores/:store/prune.
//
// NOTHING is required, which is exactly what the handler enforced: it bound
// the body and checked nothing, and proxmox.PruneDatastore sends only the
// keep-* rules that are greater than zero. That zero sentinel is why each
// count carries a Default of 0 rather than being left without one — omitting
// a rule and sending it as 0 have always meant the same thing here, unlike
// the tristate fields on the backup job below.
//
// The ceilings are sanity bounds rather than PBS limits: PBS itself takes
// any non-negative integer, and a keep count in the millions is a typo.
func pruneDatastoreParams() apischema.Properties {
	keep := func(description string) apischema.Property {
		p := optCount(100000, description)
		p.Default = 0
		return p
	}
	return apischema.Properties{
		"backup_type": optString(32, "<vm|ct|host>",
			"Narrow the prune to one kind of guest. Omitted or empty prunes every kind."),
		"backup_id": optString(255, "<id>",
			"Narrow the prune to one backup group. Omitted or empty prunes every group."),
		"dry_run":      optFlag("Report what would be removed without removing anything. A dry run writes no audit row."),
		"keep_last":    keep("Keep this many of the most recent snapshots per group. 0 does not send the rule."),
		"keep_daily":   keep("Keep one snapshot per day for this many days. 0 does not send the rule."),
		"keep_weekly":  keep("Keep one snapshot per week for this many weeks. 0 does not send the rule."),
		"keep_monthly": keep("Keep one snapshot per month for this many months. 0 does not send the rule."),
		"keep_yearly":  keep("Keep one snapshot per year for this many years. 0 does not send the rule."),
	}
}

// restoreBackupParams is the body of POST /clusters/:cluster_id/restore.
//
// Six parameters are required, matching exactly the one combined check the
// handler made: pbs_server_id, backup_type, backup_id, backup_time,
// target_node and vmid. The two numeric ones carry a Minimum of 1 because
// the handler refused `backup_time == 0` and `vmid <= 0`, which apischema
// would otherwise accept as supplied values.
//
// backup_type carries the Enum the handler enforced at the bottom of the
// function ("backup_type must be 'vm' or 'ct'") — a Nexara vocabulary rather
// than a Proxmox one, since it selects between RestoreVM and RestoreCT, so
// it is ours to close. What the Enum changes is WHEN the refusal happens: a
// value outside it used to be carried past the PBS-server lookup, the
// cluster lookup, the storage listing and the whole force branch before the
// switch at the bottom answered 400. Nothing destructive happened on that
// path — the force branch's own switch has no default, so an unknown type
// stopped no guest — but it is four reads and a Proxmox client for a request
// that was never going to work.
func restoreBackupParams() apischema.Properties {
	return apischema.Properties{
		"pbs_server_id": {
			Type:        apischema.String,
			Format:      "uuid",
			Typetext:    "<uuid>",
			Description: "Nexara PBS server the snapshot lives on.",
		},
		"backup_type": {
			Type:        apischema.String,
			Enum:        []string{"vm", "ct"},
			Typetext:    "<vm|ct>",
			Description: "Kind of guest to restore, which picks between qmrestore and pct restore.",
		},
		"backup_id": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(255),
			Typetext:    "<id>",
			Description: "Backup group id of the snapshot, which for a Proxmox guest is its VMID.",
		},
		"backup_time": {
			Type:        apischema.Integer,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(99999999999.0),
			Typetext:    "<unix timestamp>",
			Description: "Snapshot time as a Unix timestamp in seconds.",
		},
		"target_node": requiredNode(
			"Node to restore onto. A force restore over an existing guest is redirected to the node that " +
				"guest already lives on, because Proxmox requires it."),
		"vmid": {
			Type: apischema.Integer,
			// 1 rather than Proxmox's own 100: the handler refused `vmid <= 0`
			// and nothing narrower, and tightening it here would refuse a
			// restore onto a low VMID that Proxmox itself accepts.
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(999999999.0),
			Typetext:    "<integer>",
			Description: "VMID to restore the guest as.",
		},
		"datastore": optString(64, "<datastore>",
			"PBS datastore the snapshot lives in. Recorded on the task; the archive path is built from "+
				"the cluster's own PBS storage entry."),
		"storage": optString(100, "<storage>",
			"Storage to restore the disks onto. Omitted lets Proxmox use the archive's own."),
		"force":  optFlag("Overwrite an existing guest with this VMID. The guest is stopped first."),
		"unique": optFlag("Assign fresh MAC addresses to the restored guest's network devices."),
		"start_after_restore": optFlag(
			"Start the guest once the restore finishes. Watched in the background; the response returns " +
				"as soon as the restore task is dispatched."),
	}
}

// backupJobParams is the body shared by the vzdump job create and update
// routes.
//
// NOTHING is required on either, which is what both handlers enforced: each
// bound the body and checked nothing, so a PUT carrying one field is a
// partial update and a POST carrying none is a request Proxmox refuses with
// its own message. Making `schedule` or `storage` required here would look
// tidier and would reject a create that has always been Proxmox's to judge.
//
// FOUR parameters carry no Default, and that is the load-bearing part.
// `enabled`, `all`, `node` and `comment` are read back with OptInt/OptString
// so that "the caller never mentioned this" stays distinct from "the caller
// sent zero or empty": clearedProperties() unsets node and comment only on
// an explicit empty, and selectionKeys() reads all=0 as "not a selection".
// A Default would not collapse the two — OptInt and OptString report a
// default as not supplied (apischema.Property.Default) — but it would
// document a value an omitted field never gets. What WOULD collapse them,
// disabling jobs and clearing comments on every partial save, is reading any
// of the four with a plain accessor (see backupJobRequestFromParams).
func backupJobParams() apischema.Properties {
	return apischema.Properties{
		"enabled": {
			Type:     apischema.Integer,
			Optional: true,
			// NO Default — see the doc comment.
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(1.0),
			Typetext:    "<integer>",
			Description: "1 enables the job's schedule, 0 disables it. Omitted leaves it as it is.",
		},
		"type":     optString(32, "<vzdump>", "Job type. Proxmox has only ever had one, and omitting it is normal."),
		"schedule": optString(256, "<calendar event>", "When the job runs, in Proxmox's calendar-event syntax, e.g. \"mon..fri 02:00\"."),
		"storage":  optString(100, "<storage>", "Storage to write archives to."),
		"node": {
			Type:     apischema.String,
			Optional: true,
			// NO Default, and no node-name format: the EMPTY string means "run
			// on any node" and is what clears the property, which every
			// registered format rejects.
			Pattern:     emptyOrNodeName,
			MaxLength:   apischema.Ptr(63),
			Typetext:    "<name>",
			Description: "Restrict the job to one node. An explicit empty value clears the restriction; omitting it leaves it as it is.",
		},
		"vmid": optString(512, "<vmid>[,<vmid>...]", "Guests to back up, as a comma-separated VMID list. Selecting this clears the other selections."),
		"all": {
			Type:     apischema.Integer,
			Optional: true,
			// NO Default — see the doc comment.
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(1.0),
			Typetext:    "<integer>",
			Description: "1 backs up every guest. 0 is not a selection at all and leaves the job's current one alone.",
		},
		"exclude":          optString(512, "<vmid>[,<vmid>...]", "Guests to skip from an all-guests job. Sending it implies all=1, which is the one combination Proxmox allows."),
		"pool":             optPoolID("Back up every guest in this resource pool. Selecting this clears the other selections."),
		"mode":             optString(32, "<snapshot|suspend|stop>", "vzdump mode."),
		"compress":         optString(32, "<0|1|gzip|lzo|zstd>", "Compression for the archives."),
		"mailnotification": optString(32, "<always|failure>", "When Proxmox emails about this job."),
		"mailto":           optString(512, "<address>[,<address>...]", "Addresses Proxmox notifies. No email format: Proxmox owns the list syntax."),
		"comment": {
			Type:     apischema.String,
			Optional: true,
			// NO Default — see the doc comment.
			MaxLength:   apischema.Ptr(512),
			Typetext:    "<string>",
			Description: "Free-text note on the job. An explicit empty value clears it; omitting it leaves it as it is.",
		},
	}
}
