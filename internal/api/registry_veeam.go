package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, globalCheck, vmParams, withParams, optFlag and optString —
// lives in registry_vms.go, where the first migrated domain defined it.
// pathPrefix is the registry's own, in registry.go.

// veeamScope is the instance-wide collection every Veeam server hangs off.
//
// It is GLOBAL rather than cluster-scoped, and that is the domain's defining
// fact rather than an accident of the path: one VBR server can protect
// several Proxmox clusters, so knowing a server exists reveals infrastructure
// spanning clusters the caller may hold nothing on. The per-cluster scoping
// applies to the DATA those servers produce — jobs, sessions, backup objects —
// which is resolved through the veeam_platforms mapping at request time and is
// why a third of this domain is Advisory or Deferred.
const veeamScope = pathPrefix + "veeam-servers"

// veeamServerIDParam is a Veeam server's Nexara row id as a PATH parameter.
//
// It is spelled :id because that is what all 24 routes under this prefix
// already register. The name is one of the two clusterIDFromParam reads (see
// gateParamNames in registry.go), which is safe on two counts: it resolves to
// the PATH, so checkPathParams' refusal of the same name declared as a body or
// query parameter does not bite, and every route that declares it is a GLOBAL
// Check, Advisory or Deferred — none of which resolves a cluster out of the
// path at all.
//
// The one cluster-scoped Check in this file is the VM detail page's Veeam card,
// and it declares no :id: its path is /clusters/:cluster_id/vms/:vm_id/veeam.
// That is not a coincidence to rely on, though — namesACluster (permissions.go)
// refuses a cluster-scoped Check whose first path parameter is not :cluster_id,
// so a Veeam server id could not become the cluster a gate authorizes even if
// someone tried.
var veeamServerIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Source:      apischema.SourcePath,
	Typetext:    "<uuid>",
	Description: "Nexara Veeam server identifier.",
}

// veeamUpstreamIDParam builds a PATH parameter for one of the ids VEEAM
// assigns — a repository, a platform, a backup object, a job or a session.
//
// Every one of them is a uuid that the handlers parsed with uuid.Parse and
// answered 400 for; the format states the same rule one layer earlier and
// names the field. None of them ever becomes a segment of an outbound request
// path by concatenation the way a PBS datastore does — veeam.Client builds its
// URLs from the uuid's canonical string form — so the format IS the anchor and
// no extra pattern is needed.
func veeamUpstreamIDParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Format:      "uuid",
		Source:      apischema.SourcePath,
		Typetext:    "<uuid>",
		Description: description,
	}
}

// veeamServerParams is the one path parameter every per-server route carries.
func veeamServerParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{"id": veeamServerIDParam}, extra)
}

// veeamPlatformClusterParam is the Nexara cluster a Veeam platform or backup
// object is attached to, in the two BODIES that carry one.
//
// It is declared under a name of its own with "cluster_id" as an ALIAS, for
// exactly the reason pbsAttachedClusterParam (registry_pbs.go) and
// reportClusterParam (registry_reports.go) are: checkPathParams refuses a
// parameter NAMED cluster_id that resolves to anything but the path, because
// clusterIDFromParam reads that name to decide which cluster a gate
// authorizes. Neither of these two routes runs a cluster-scoped gate —
// MapPlatform is a global Check and MapBackupObjectGuest is Deferred — but the
// refusal is deliberately about the NAME rather than about whether today's
// path happens to make it safe. The alias is what keeps every existing caller
// working: both dialogs send {"cluster_id": …}.
//
// NULL is the meaningful value here, not the empty string: apischema's
// present() reads an explicit JSON null as absent (see validate.go), which is
// exactly how both handlers already read `*uuid.UUID == nil` — "detach", or
// "hand this object back to automatic resolution". So the uuid FORMAT is
// right, unlike the PBS create body's empty-string sentinel.
func veeamPlatformClusterParam(description string) apischema.Property {
	return apischema.Property{
		Type:     apischema.String,
		Alias:    "cluster_id",
		Optional: true,
		Format:   "uuid",
		Typetext: "<uuid>",
		Description: description +
			" Omitted, or sent as null, detaches it. Also accepted as \"cluster_id\", which is what " +
			"Nexara's own dialogs send.",
	}
}

// veeamScopeReason is the Advisory justification the four collected-inventory
// listings share.
//
// Every one of them serves a caller holding view:veeam ANYWHERE and then drops
// the rows they may not see, resolving each row's cluster through the server's
// veeam_platforms mapping. A gate cannot stand in for that: a listing spans
// every cluster one Veeam server protects, so there is no single cluster for
// middleware to resolve, and requirePerm would 403 every cluster-scoped Veeam
// viewer before the filtering ever ran — which is what veeamScopeFor's own doc
// comment says out loud.
const veeamScopeReason = "accessibleClusters(\"view\", \"veeam\") builds the scope and " +
	"veeamScope.permitsPlatform applies it per row, resolving each row's cluster through the server's " +
	"veeam_platforms mapping and answering an UNMAPPED platform with the instance-wide grant alone; " +
	"the listing spans every cluster the server protects, so there is none for a gate to resolve"

// veeamRowScopeReason is the Deferred justification for a route that reaches
// the decision about ONE row rather than about a listing.
//
// It loads the object, job or session by id — always narrowed to the server in
// the path, so a caller can never pair an id from one server with another — and
// then gates on the cluster that row's platform maps to. Middleware runs before
// any query, so it cannot make that decision.
//
// It is the BASE rule, stated once: the restore-points read uses it verbatim,
// and the three reasons below specialise it for the routes whose verb or whose
// second read differs. Keeping it named rather than inlining it is what lets
// those three say what they add instead of restating the whole thing.
const veeamRowScopeReason = "the cluster to authorize is resolved from the row: the handler loads the " +
	"object, job or session (always scoped to the server in the path) and gates on the cluster its " +
	"veeam_platforms mapping names, falling back to the instance-wide grant when the platform is " +
	"unmapped — which middleware cannot do because it runs before any query"

// veeamSessionScopeReason is veeamRowScopeReason for the three per-session
// routes, and it exists because they are the one place in this domain where the
// ACTION itself is computed rather than fixed.
//
// resolveSessionForControl takes the verb as an argument: stopping a run passes
// "execute", reading its log or its per-guest breakdown passes "view". One
// implementation, two grants — so a reason that named either verb would be
// wrong on the other two routes, and a reader checking which grant a route
// needs has to read the Description for that.
const veeamSessionScopeReason = "the cluster to authorize is resolved from the row, under a verb the ROUTE " +
	"chooses: resolveSessionForControl runs accessibleClusters with \"execute\" for the stop and \"view\" " +
	"for the log and task reads, BEFORE loading the session — so a caller holding no grant at all cannot " +
	"tell a session that exists from one that does not — and then gates on the cluster the session's " +
	"veeam_platforms mapping names, falling back to the instance-wide grant when the platform is unmapped"

// veeamMapObjectReason is MapBackupObjectGuest's own Deferred justification,
// which is the row-scoped rule PLUS a second step worth naming.
const veeamMapObjectReason = "the cluster to authorize is resolved through TWO reads: the handler loads " +
	"the backup object (scoped to the server in the path), resolves its platform through veeam_platforms " +
	"to a cluster, and then requires manage:veeam on THAT cluster — never on the cluster_id the body " +
	"names, which must equal it. An unmapped platform is a 422 rather than a permission decision, " +
	"because there is no cluster to check against yet"

// registerVeeamEndpoints declares the 25 routes served by VeeamHandler.
//
// The split is the domain's own shape:
//
//	12  Check     the server registry, the repository/platform/infrastructure
//	              listings and the platform mapping — all GLOBAL, because a
//	              Veeam server is not a per-cluster resource — plus the one
//	              cluster-scoped route, the VM detail page's Veeam card
//	 4  Advisory  the collected-inventory listings, which filter per row
//	              through the platform mapping (veeamScopeReason)
//	 9  Deferred  the per-row reads and the job/session control calls, whose
//	              cluster comes out of a DB lookup — veeamRowScopeReason and the
//	              three reasons that specialise it
//
// connect and control are the two shared rate limiters the legacy block built,
// passed in as ONE instance each rather than constructed per route: separate
// limiter instances hold separate stores, which would silently multiply the
// budget they exist to cap. That is the same reasoning router.go spelled out
// when these were mounted by hand, and the registry's RateLimiter field
// attaches them in the same position — after authentication, ahead of the
// permission check, so an unauthorized flood spends the caller's own bucket.
func registerVeeamEndpoints(reg *Registry, h *handlers.VeeamHandler, connect, control fiber.Handler) {
	// ── Server registry ───────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   veeamScope,
		Description: "Register a Veeam Backup & Replication server (VBR 13.1+). The server is probed " +
			"before it is stored, so a failed connection is a failed create: the probe is what produces " +
			"the pinned API revision, and a row without one would fall back to whatever schema the " +
			"server chooses to default to.",
		Group:       "Backup",
		Permissions: globalCheck("manage", "veeam"),
		RateLimiter: connect,
		Parameters:  createVeeamParams(),
		Handler:     h.Create,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope,
		Description: "List the registered Veeam Backup & Replication servers. The configured account " +
			"name is returned only to a manage:veeam holder — it is half of a domain administrator " +
			"credential, and view:veeam is seeded to the built-in Viewer.",
		Group:       "Backup",
		Permissions: globalCheck("view", "veeam"),
		Parameters:  nil,
		Handler:     h.List,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope + "/:id",
		Description: "Get one Veeam server's connection details. The password is never returned, and " +
			"the account name only to a manage:veeam holder.",
		Group:       "Backup",
		Permissions: globalCheck("view", "veeam"),
		Parameters:  veeamServerParams(nil),
		Handler:     h.Get,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   veeamScope + "/:id",
		Description: "Change a Veeam server. Every parameter is optional and the ones omitted are left " +
			"as they are. Changing the address, the credentials or the TLS handling re-probes the " +
			"server; renaming it or toggling enabled does not, so an unreachable server can still be " +
			"disabled. Re-pointing the address without re-entering the password is refused outright.",
		Group:       "Backup",
		Permissions: globalCheck("manage", "veeam"),
		RateLimiter: connect,
		Parameters:  updateVeeamParams(),
		Handler:     h.Update,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        veeamScope + "/:id",
		Description: "Remove a Veeam server and the credential stored with it. Collected inventory goes with it.",
		Group:       "Backup",
		Permissions: globalCheck("delete", "veeam"),
		Parameters:  veeamServerParams(nil),
		Handler:     h.Delete,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   veeamScope + "/:id/test",
		Description: "Test the stored connection and report the server's version, licence edition and " +
			"covered Proxmox clusters. Persists nothing. Gated on manage rather than view because it " +
			"spends a real logon against the stored administrator account rather than reading Nexara's " +
			"own state.",
		Group:       "Backup",
		Permissions: globalCheck("manage", "veeam"),
		RateLimiter: connect,
		Parameters:  veeamServerParams(nil),
		Handler:     h.Test,
	})

	// ── Global-scope inventory ────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope + "/:id/repositories",
		Description: "List the server's backup repositories with their capacity. Global scope on " +
			"purpose: one repository holds every cluster's backups, so there is no cluster to attribute " +
			"it to.",
		Group:       "Backup",
		Permissions: globalCheck("view", "veeam"),
		Parameters:  veeamServerParams(nil),
		Handler:     h.ListRepositories,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        veeamScope + "/:id/repositories/:repository_id/metrics",
		Description: "Read one repository's capacity over time, from the samples the collector stores.",
		Group:       "Backup",
		Permissions: globalCheck("view", "veeam"),
		Parameters: veeamServerParams(apischema.Properties{
			"repository_id": veeamUpstreamIDParam(
				"Repository id as VEEAM assigns it (veeam_id in the repository listing), not Nexara's row id."),
			// NO Enum, deliberately. parseVeeamRange answers an unrecognised
			// value with the 7-day window rather than an error — "a chart with a
			// sensible window beats a 400", as its own doc comment puts it — so
			// closing the vocabulary here would turn a request that has always
			// worked into a 400. The Default is the value that fallback produces:
			// "7d" is itself not a switch case, so it reaches the same branch an
			// omitted parameter did.
			//
			// The MaxLength is the one thing that IS refused, and it is a bound on
			// a query value rather than a vocabulary: a range longer than any of
			// the four spellings could only ever have fallen back to 7d anyway.
			"range": {
				Type:      apischema.String,
				Optional:  true,
				Default:   "7d",
				MaxLength: apischema.Ptr(16),
				Typetext:  "<24h|7d|30d|90d>",
				Description: "How far back to read. Anything other than 24h, 30d or 90d — including an " +
					"unrecognised value — is the 7-day window.",
			},
		}),
		Handler: h.GetRepositoryMetrics,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope + "/:id/platforms",
		Description: "List the Veeam platforms (Proxmox connections) this server protects and the " +
			"Nexara cluster each is mapped to. Global view:veeam: the answer spans clusters by " +
			"construction, and a partial one would make the mapping UI silently incomplete.",
		Group:       "Backup",
		Permissions: globalCheck("view", "veeam"),
		Parameters:  veeamServerParams(nil),
		Handler:     h.ListPlatforms,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   veeamScope + "/:id/platforms/:platform_id",
		Description: "Map a Veeam platform to a Nexara cluster, or detach it. Every cluster-scoped " +
			"Veeam permission resolves through this mapping, so the write needs GLOBAL manage:veeam — " +
			"not a grant on the cluster being attached, which would let a holder scoped to cluster A " +
			"point a platform holding cluster B's guests at A and read it.",
		Group:       "Backup",
		Permissions: globalCheck("manage", "veeam"),
		Parameters: veeamServerParams(apischema.Properties{
			"platform_id": veeamUpstreamIDParam(
				"Veeam platform id, as GET /veeam-servers/{id}/platforms reports it."),
			"platform_cluster_id": veeamPlatformClusterParam(
				"Nexara cluster this platform's guests belong to."),
		}),
		Handler: h.MapPlatform,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope + "/:id/infrastructure",
		Description: "List Veeam's own guests on the protected clusters — worker appliances and the VBR " +
			"server itself. Backup coverage excludes these, and an exclusion nobody can inspect is " +
			"indistinguishable from a coverage bug. Global view:veeam, like the repository and platform " +
			"listings: the answer spans every cluster the server protects.",
		Group:       "Backup",
		Permissions: globalCheck("view", "veeam"),
		Parameters:  veeamServerParams(nil),
		Handler:     h.ListInfrastructure,
	})

	// ── Cluster-filtered inventory ────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope + "/:id/jobs",
		Description: "List the server's Proxmox backup jobs with their last result and progress. " +
			"Filtered rather than gated: a job appears only when the caller holds view:veeam on the " +
			"cluster its Veeam platform maps to, and a job that has never run has no platform yet — so " +
			"it needs the instance-wide grant.",
		Group: "Backup",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "veeam", Scope: ScopeCluster},
			Reason: veeamScopeReason,
		}},
		Parameters: veeamServerParams(nil),
		Handler:    h.ListJobs,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope + "/:id/sessions",
		Description: "List the server's recent Proxmox backup runs. Filtered the same way the job " +
			"listing is: view:veeam on the cluster the run's platform maps to, or instance-wide for an " +
			"unattributable run.",
		Group: "Backup",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "veeam", Scope: ScopeCluster},
			Reason: veeamScopeReason,
		}},
		Parameters: veeamServerParams(nil),
		Handler:    h.ListSessions,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope + "/:id/backup-objects",
		Description: "List the guests this server backs up, one row per guest. Two rows can share a " +
			"NAME without being the same guest — that is a rebuilt guest whose replacement reused its " +
			"name, which is what the SMBIOS uuid tells apart. Filtered on view:veeam per cluster.",
		Group: "Backup",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "veeam", Scope: ScopeCluster},
			Reason: veeamScopeReason,
		}},
		Parameters: veeamServerParams(nil),
		Handler:    h.ListBackupObjects,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope + "/:id/orphaned-objects",
		Description: "List backup objects whose Veeam platform IS mapped but which match no guest on " +
			"it — restore points held for machines that no longer exist in the form that was backed up. " +
			"Filtered on view:veeam per cluster, and every row here has a mapped platform by " +
			"construction, so a caller scoped elsewhere sees none of it.",
		Group: "Backup",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "veeam", Scope: ScopeCluster},
			Reason: veeamScopeReason,
		}},
		Parameters: veeamServerParams(nil),
		Handler:    h.ListOrphanedObjects,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope + "/:id/backup-objects/:object_id/restore-points",
		// Deferred renders as the bare word "deferred", so the permission an
		// operator building a role needs is spelled out here. Every Deferred
		// route in this file says it the same way.
		Description: "List one backup object's restore points, with malware status and file-level " +
			"restore availability. Requires view:veeam on the cluster the object's Veeam platform maps " +
			"to, or the instance-wide view:veeam when it is unmapped. A caller who may not see the " +
			"object gets 404 rather than 403, so the answer cannot confirm that a guest exists on a " +
			"cluster they have no access to.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: veeamRowScopeReason},
		Parameters: veeamServerParams(apischema.Properties{
			"object_id": veeamUpstreamIDParam(
				"Nexara's row id for the backup object, as GET /veeam-servers/{id}/backup-objects returns it."),
		}),
		Handler: h.ListRestorePoints,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   veeamScope + "/:id/backup-objects/:object_id/guest",
		Description: "Pin a backup object to a guest by VMID, overriding automatic correlation, or " +
			"clear the pin and hand the object back to it. Requires manage:veeam on the cluster the " +
			"object's Veeam platform is mapped to; the guest must be on that same cluster, because a " +
			"platform IS one Proxmox connection and an object from it cannot belong to another.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: veeamMapObjectReason},
		Parameters: veeamServerParams(apischema.Properties{
			"object_id": veeamUpstreamIDParam(
				"Nexara's row id for the backup object."),
			"guest_cluster_id": veeamPlatformClusterParam(
				"Cluster the guest lives on, which must be the one this object's platform is mapped to."),
			// NO Default: the handler requires cluster and vmid to be set
			// together or both absent, reading both with Opt accessors, which
			// a default cannot fool (apischema.Property.Default); a declared
			// default would document every clear as a pin.
			"vmid": {
				Type:     apischema.Integer,
				Optional: true,
				// 1 rather than 0: a VMID is a positive Proxmox identifier, and
				// apischema would otherwise accept an explicit 0 as supplied.
				Minimum:  apischema.Ptr(1.0),
				Maximum:  apischema.Ptr(999999999.0),
				Typetext: "<integer>",
				Description: "Proxmox VMID of the guest to pin this object to. Must be sent together " +
					"with the cluster; omitting both clears the pin.",
			},
		}),
		Handler: h.MapBackupObjectGuest,
	})

	// ── Job and session control ───────────────────────────────────────
	//
	// Every one of these mints a fresh OAuth2 password grant against a
	// domain-backed VBR server, so all seven share the control limiter rather
	// than the general one.
	for _, job := range []struct {
		action      string
		description string
		handler     Handler
	}{
		{"start", "Start a Veeam backup job now. Asynchronous: the session that comes back is a " +
			"starting state for the poll loop to settle, never a result. A job with no objects to " +
			"process answers started=false and creates no run, which is reported as its own outcome " +
			"rather than as a success nobody can find afterwards.", h.StartJob},
		{"stop", "Stop a Veeam job's running session. Disruptive: the run is abandoned and the guests " +
			"it had not reached keep whatever recovery point they already had. Recorded as " +
			"Nexara-initiated so the veeam_job_failed alert does not fire for it — Veeam itself " +
			"records a cancelled run as \"Failed\".", h.StopJob},
		{"enable", "Put a Veeam job back on its schedule.", h.EnableJob},
		{"disable", "Take a Veeam job off its schedule. The quietest destructive action in the " +
			"integration: nothing is deleted and no alarm is raised anywhere, so protection stops " +
			"accruing while every existing restore point sits there looking healthy.", h.DisableJob},
	} {
		reg.Register(Endpoint{
			Method: fiber.MethodPost,
			Path:   veeamScope + "/:id/jobs/:job_id/" + job.action,
			Description: job.description +
				" Requires execute:veeam on the cluster the job's Veeam platform maps to, or the " +
				"instance-wide execute:veeam when the job has never run and so has no platform yet.",
			Group:       "Backup",
			Permissions: Permissions{Deferred: veeamJobControlReason},
			RateLimiter: control,
			Parameters: veeamServerParams(apischema.Properties{
				"job_id": veeamUpstreamIDParam(
					"Job id as VEEAM assigns it (veeam_id in the job listing), not Nexara's row id."),
			}),
			Handler: job.handler,
		})
	}
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   veeamScope + "/:id/sessions/:session_id/stop",
		Description: "Stop one running Veeam session. Recorded as Nexara-initiated, as the job stop " +
			"is. Requires execute:veeam on the cluster the session's Veeam platform maps to, or the " +
			"instance-wide execute:veeam when it is unattributable.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: veeamSessionScopeReason},
		RateLimiter: control,
		Parameters:  veeamSessionParams(),
		Handler:     h.StopSession,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope + "/:id/sessions/:session_id/logs",
		Description: "Read one run's log, live from Veeam rather than from a stored copy. AN EMPTY LOG " +
			"IS NORMAL for a stopped run — Veeam keeps no records for a killed session. Requires " +
			"view:veeam on the cluster the session's Veeam platform maps to, or the instance-wide " +
			"view:veeam when it is unattributable. It costs a logon, so it shares the control limiter.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: veeamSessionScopeReason},
		RateLimiter: control,
		Parameters:  veeamSessionParams(),
		Handler:     h.GetSessionLogs,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   veeamScope + "/:id/sessions/:session_id/tasks",
		Description: "Per-guest breakdown of one run: which guests it processed and which failed, " +
			"linked to their Nexara guest where the name resolves unambiguously. An empty list is TWO " +
			"different facts — a run still in flight has no task rows yet, and a finished run may have " +
			"kept none — so read it against the run's own state. Requires view:veeam on the cluster " +
			"the session's Veeam platform maps to, or the instance-wide view:veeam when it is " +
			"unattributable.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: veeamSessionScopeReason},
		RateLimiter: control,
		Parameters:  veeamSessionParams(),
		Handler:     h.GetSessionTasks,
	})

	// ── The VM detail page's Veeam card ───────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/vms/:vm_id/veeam",
		Description: "Read one guest's Veeam protection: whether it has a restore point, how recent, " +
			"how it was correlated, and the recent points themselves. Gated on view:veeam for the " +
			"cluster rather than view:vm — a caller who may see the guest is not thereby entitled to " +
			"its backup posture. The only cluster-scoped route in this domain, because it is the only " +
			"one whose subject is a guest rather than a Veeam server.",
		Group:       "Backup",
		Permissions: clusterCheck("view", "veeam"),
		Parameters:  vmParams(nil),
		Handler:     h.GetGuestVeeamProtection,
	})
}

// veeamJobControlReason is the Deferred justification the four job-control
// routes share. It is veeamRowScopeReason's decision reached through
// "execute" rather than "view", and it says so rather than reusing a string
// whose verb would then be wrong.
const veeamJobControlReason = "the cluster to authorize is resolved from the row, under a different " +
	"verb: resolveJobForControl runs accessibleClusters(\"execute\", \"veeam\") BEFORE loading the job " +
	"— so a caller holding no execute grant anywhere cannot tell a job that exists from one that does " +
	"not — and then gates on the cluster the job's veeam_platforms mapping names, falling back to the " +
	"instance-wide grant for a job that has never run and so has no platform"

// veeamSessionParams is the pair the three per-session routes carry.
func veeamSessionParams() apischema.Properties {
	return veeamServerParams(apischema.Properties{
		"session_id": veeamUpstreamIDParam(
			"Session id as VEEAM assigns it (veeam_id in the session listing), not Nexara's row id."),
	})
}

// createVeeamParams is the body of POST /api/v1/veeam-servers.
//
// FOUR parameters are required, matching exactly the one combined check the
// handler made: name, base_url, username and password. The URL rules (https, a
// host, no credentials) stay in validateURLFormat and enforceURLAddressPolicy,
// which own them and answer with messages of their own — and the address policy
// is a DNS-resolving SSRF check that no parameter schema could make.
//
// The length caps on base_url, username and password are NEW — the handler
// capped only `name` — and they are chosen against the wire rather than against
// Veeam: every one of these values is stored and then sent on every outbound
// request, so what the schema owes them is a bound, not a format. They are far
// above anything a real VBR deployment uses.
func createVeeamParams() apischema.Properties {
	return apischema.Properties{
		"name": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(255),
			Typetext:    "<string>",
			Description: "Display name for the server.",
		},
		"base_url": {
			Type:      apischema.String,
			MinLength: apischema.Ptr(1),
			MaxLength: apischema.Ptr(2048),
			Typetext:  "<url>",
			Description: "Base URL of the VBR REST API, e.g. https://vbr.example.com:9419. Must be " +
				"https and must not carry credentials.",
		},
		"username": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(255),
			Typetext:    "<user|DOMAIN\\user>",
			Description: "Veeam account to connect as. Returned only to manage:veeam holders.",
		},
		// Declaring the password at all is what makes checkMisplaced refuse it
		// as a QUERY parameter, so a caller cannot move a domain credential into
		// a URL that proxies and access logs record. It is never read back — the
		// response type carries no password field — and never reaches an audit
		// row, which this project makes readable by every Viewer.
		"password": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(1024),
			Typetext:    "<secret>",
			Description: "Password for that account. Stored encrypted and never returned.",
		},
		"tls_fingerprint": optString(128, "<SHA256 fingerprint>",
			"Expected SHA-256 leaf-certificate fingerprint. Empty or omitted verifies against the "+
				"system roots instead. No format: the add dialog sends it empty when the fetch step "+
				"was skipped."),
		"verify_tls": {
			Type:     apischema.Boolean,
			Optional: true,
			// The handler's own default, stated rather than left implicit:
			// `verifyTLS := true; if req.VerifyTLS != nil { … }`.
			Default:  true,
			Typetext: "<boolean>",
			Description: "Verify the server's certificate chain. Omitted verifies. Turning it off with " +
				"no fingerprint pinned needs acknowledge_insecure_tls.",
		},
		"acknowledge_insecure_tls": optFlag(
			"Confirm storing the password against a connection whose certificate is never verified — " +
				"neither pinned nor chain-verified."),
		"allow_private_address": optFlag(
			"Confirm a base_url that resolves to a private or loopback address, which a VBR on the " +
				"same network always does."),
	}
}

// updateVeeamParams is the body of PUT /api/v1/veeam-servers/:id.
//
// EVERY connection field is optional and read through an Opt accessor, so
// omitting one leaves the stored column alone. That is the whole contract of
// this route: the edit dialog sends only what it has, and the enable/disable
// toggle would otherwise rewrite the row it touched.
//
// SEVEN carry no Default: name, base_url, username, password,
// tls_fingerprint, verify_tls and enabled were all bound as POINTERS precisely
// so that "the caller never mentioned this" stays distinct from "the caller
// sent empty or false" — tls_fingerprint's empty string CLEARS the pin. Six of
// them the handler reads through Opt accessors, which a default cannot fool
// (apischema.Property.Default), so there a Default would only document a
// partial save as resetting them. password it reads with p.String and treats
// any non-empty value as a new credential, so a Default there WOULD be sent.
// The two confirmation flags are the only ones that carry a default; they
// were not pointers, and optFlag's explicit false is what they read as.
func updateVeeamParams() apischema.Properties {
	return apischema.Properties{
		"id": veeamServerIDParam,
		// NO MinLength: the handler answers an explicit "" with "name must not
		// be empty", and a bare "must be at least 1 character" would lose that.
		"name": optString(255, "<string>", "New display name. An empty value is refused."),
		"base_url": optString(2048, "<url>",
			"New base URL. Changing it re-probes the server, and doing so without also sending the "+
				"password is refused: the stored credential was entrusted to one address."),
		"username": optString(255, "<user|DOMAIN\\user>",
			"New account to connect as. An empty value is refused."),
		"password": optString(1024, "<secret>",
			"New password. Omit the field — or send it empty — to keep the stored one."),
		"tls_fingerprint": optString(128, "<SHA256 fingerprint>",
			"New expected SHA-256 fingerprint. An explicit EMPTY value clears the pin, which needs "+
				"acknowledge_insecure_tls unless verification stays on; omitting it leaves the pin alone."),
		"verify_tls": {
			Type:     apischema.Boolean,
			Optional: true,
			// NO Default — see the doc comment.
			Typetext:    "<boolean>",
			Description: "Verify the server's certificate chain. Omitted leaves it as it is.",
		},
		"enabled": {
			Type:     apischema.Boolean,
			Optional: true,
			// NO Default — see the doc comment.
			Typetext: "<boolean>",
			Description: "Whether the collector syncs this server. Omitted leaves it as it is. " +
				"Toggling it does not re-probe, so an unreachable server can still be disabled.",
		},
		"acknowledge_insecure_tls": optFlag(
			"Confirm a change that would leave the stored password riding on a connection whose " +
				"certificate is never verified."),
		"allow_private_address": optFlag(
			"Confirm a base_url that resolves to a private or loopback address."),
	}
}
