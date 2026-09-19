package api

import (
	"slices"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, withParams, optString and optCount — lives in
// registry_vms.go, where the first migrated domain defined it. It is
// reused here rather than copied.

// cephScope is the path prefix every Ceph route hangs off.
const cephScope = clusterScope + "/ceph"

// osdParams is the two path parameters every per-OSD route carries.
//
// :cluster_id is FIRST, which namesACluster (permissions.go) requires of
// a cluster-scoped Check: clusterIDFromParam resolves the gate's cluster
// out of the first placeholder, so a path that led with :osd_id would
// authorize the wrong object.
//
// osd_id is Ceph's own numeric OSD id, declared as an integer rather than
// a string so the schema rejects "osd.3" and "-1" before the handler
// reaches strconv — which is exactly what the hand-rolled osdIDFromParam
// used to do, minus the 400 it had to write itself.
func osdParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"osd_id": {
			Type:    apischema.Integer,
			Minimum: apischema.Ptr(0.0),
			// Ceph stores an OSD id as a 32-bit signed int, so nothing
			// above this can name a real OSD. The bound is here so that a
			// value the handler would narrow to `int` cannot arrive
			// arbitrarily large.
			Maximum:     apischema.Ptr(2147483647.0),
			Typetext:    "<integer>",
			Description: "Ceph OSD id, the number in \"osd.3\".",
		},
	}, extra)
}

// cephPoolNameParam is a Ceph pool name as a PATH parameter.
//
// The rule is the catalogue's ceph-pool-name, shared with the create body
// below: PVE's own `^[^:/\s]+$`, minus a name made only of dots and minus
// the backslash. It is deliberately wide — "rbd+meta", "pool!1", ".mgr"
// and "-pool" are all pool names Ceph and PVE accept, and a pool this API
// cannot name is a pool this API cannot delete.
//
// The two exclusions are both about keeping create and delete in
// agreement, and the catalogue entry gives the full reasoning. In short:
// ".." pops the pool collection and lands DELETE on /nodes/{node}/ceph,
// and a backslash passes CreateCephPool (the name travels in the form
// body, unchecked) but is refused by DeleteCephPool's validatePathSegment,
// so admitting it would let this API mint a pool it could never remove.
//
// proxmox.DeleteCephPool also runs validatePathSegment on the value and
// then url.PathEscape. Note that PathEscape does NOT escape everything
// this rule admits — "$", "&", "+", "=", "@" and "~" pass through
// unchanged. They are harmless because they are legal in a path segment,
// not because they are encoded; the characters that would matter, "/" and
// "\", are excluded by the rule itself. The rule here is the same refusal
// validatePathSegment makes, but earlier, and with a message that names
// the parameter.
var cephPoolNameParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     apischema.Rule("ceph-pool-name"),
	MaxLength:   apischema.Ptr(128),
	Typetext:    "<pool>",
	Description: "Ceph pool name.",
}

// registerCephEndpoints declares the 17 Ceph routes served by CephHandler.
//
// All 17 declare a plain Check, and none is Deferred or Advisory. Two of
// them are worth saying out loud, because the hand-placed check they
// replace did not live in the handler the route names: the five OSD
// lifecycle routes funnel through osdMembershipAction and osdDaemonAction
// (handlers/ceph_osd.go), which each performed ONE requireClusterPerm on
// the handler's behalf. Both took the action as an argument, so the
// question was whether the permission they checked varied with it — it
// did not: every one of the five is manage:ceph regardless, so the check
// is statically known and hoists into middleware like the rest. A helper
// that picked its action or resource off its argument would have had to
// stay Deferred.
//
// No route here filters a listing through accessibleClusters, so none is
// Advisory: every one gates on the cluster in its path.
func registerCephEndpoints(reg *Registry, h *handlers.CephHandler) {
	// ── Live Proxmox reads ────────────────────────────────────────────
	for _, r := range []struct {
		suffix      string
		description string
		handler     Handler
	}{
		{"/status", "Read the cluster's live Ceph health, PG, OSD and monitor counters.", h.GetStatus},
		{"/osds", "List the cluster's OSDs, flattened out of the CRUSH tree with each one's host.", h.ListOSDs},
		{"/pools", "List the cluster's Ceph pools with their size, PG count and throughput.", h.ListPools},
		{"/monitors", "List the cluster's Ceph monitors.", h.ListMonitors},
		{"/fs", "List the cluster's CephFS filesystems and the pools backing them.", h.ListFS},
		{"/rules", "List the cluster's CRUSH rules.", h.ListCrushRules},
	} {
		reg.Register(Endpoint{
			Method:      fiber.MethodGet,
			Path:        cephScope + r.suffix,
			Description: r.description,
			Group:       "Ceph",
			Permissions: clusterCheck("view", "ceph"),
			Parameters:  clusterParams(nil),
			Handler:     r.handler,
		})
	}

	// ── Pools ─────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   cephScope + "/pools",
		Description: "Create a Ceph pool. Proxmox does the work in a background task, so the response " +
			"carries a UPID rather than the finished pool.",
		Group:       "Ceph",
		Permissions: clusterCheck("manage", "ceph"),
		Parameters:  clusterParams(createCephPoolParams()),
		Handler:     h.CreatePool,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        cephScope + "/pools/:pool_name",
		Description: "Destroy a Ceph pool and everything stored in it. Irreversible.",
		Group:       "Ceph",
		Permissions: clusterCheck("manage", "ceph"),
		Parameters:  clusterParams(apischema.Properties{"pool_name": cephPoolNameParam}),
		Handler:     h.DeletePool,
	})

	// ── OSDs ──────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   cephScope + "/osds/:osd_id/preflight",
		Description: "Assess what a lifecycle action would cost this OSD's cluster in redundancy. " +
			"Advisory only — the action endpoints never consult it.",
		Group:       "Ceph",
		Permissions: clusterCheck("view", "ceph"),
		Parameters: osdParams(apischema.Properties{
			"action": {
				Type: apischema.String,
				// Cloned rather than aliased, for the reason the VM status
				// route's enum gives: Property.Enum is only deep-copied on
				// the StdOption path, so sharing the package-level slice
				// would give every Server's schema the same backing array.
				Enum:     slices.Clone(handlers.CephOSDActions),
				Optional: true,
				// The default the handler has always applied to a missing
				// ?action=, stated here so the docs answer "what happens
				// if I leave this out".
				Default:     "out",
				Typetext:    "<in|out|start|stop|restart>",
				Description: "Action to assess. Defaults to out, the one operators reach for first.",
			},
		}),
		Handler: h.GetOSDPreflight,
	})
	for _, r := range []struct {
		suffix      string
		description string
		handler     Handler
	}{
		{"/in", "Mark an OSD in, returning it to the CRUSH map. Applied immediately; no task is spawned.", h.SetOSDIn},
		{"/out", "Mark an OSD out, draining its data onto the rest of the cluster. Applied immediately; no task is spawned.", h.SetOSDOut},
		{"/start", "Start an OSD's daemon on its own host.", h.StartOSD},
		{"/stop", "Stop an OSD's daemon on its own host.", h.StopOSD},
		{"/restart", "Restart an OSD's daemon on its own host.", h.RestartOSD},
	} {
		reg.Register(Endpoint{
			Method:      fiber.MethodPost,
			Path:        cephScope + "/osds/:osd_id" + r.suffix,
			Description: r.description,
			Group:       "Ceph",
			Permissions: clusterCheck("manage", "ceph"),
			Parameters:  osdParams(nil),
			Handler:     r.handler,
		})
	}

	// ── Collected metrics ─────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        cephScope + "/metrics",
		Description: "Read the collected Ceph cluster metrics over a window, downsampled to a fixed bucket width.",
		Group:       "Ceph",
		Permissions: clusterCheck("view", "ceph"),
		Parameters: clusterParams(apischema.Properties{
			"timeframe": {
				Type:     apischema.String,
				Enum:     slices.Clone(handlers.CephMetricsTimeframes),
				Optional: true,
				Default:  handlers.CephMetricsDefaultTimeframe,
				Typetext: "<1h|6h|24h|7d>",
				Description: "History window. Each one carries its own bucket width, chosen so the response " +
					"stays a couple of hundred points however long the window is.",
			},
		}),
		Handler: h.GetHistorical,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        cephScope + "/osds/metrics",
		Description: "Read the latest collected per-OSD metrics for this cluster.",
		Group:       "Ceph",
		Permissions: clusterCheck("view", "ceph"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetOSDMetrics,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        cephScope + "/pools/metrics",
		Description: "Read the latest collected per-pool metrics for this cluster.",
		Group:       "Ceph",
		Permissions: clusterCheck("view", "ceph"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetPoolMetrics,
	})
}

// createCephPoolParams is the body of POST /clusters/:cluster_id/ceph/pools.
//
// Three parameters are required, matching what the handler and the client
// have always enforced: name, size and pg_num. The other four are dropped
// by CreateCephPool when they are empty or non-positive (see
// client_storage.go), so "" and "absent" mean the same thing to Proxmox —
// which is why none of them carries a format that would reject "".
//
// crush_rule_name is Nexara's own wire name and is NOT PVE's, which is
// crush_rule; the client translates, and the comment on that translation
// explains why sending PVE's own spelling was the bug. Renaming it here
// to "align" would break every existing caller for no gain.
func createCephPoolParams() apischema.Properties {
	return apischema.Properties{
		"name": {
			Type: apischema.String,
			// Same rule as the path parameter, and from the same catalogue
			// entry so that it stays the same one: a pool created here has
			// to stay deletable through cephPoolNameParam, and a create
			// rule looser than the delete rule would produce a pool this
			// API could not remove.
			Pattern:     apischema.Rule("ceph-pool-name"),
			MaxLength:   apischema.Ptr(128),
			Typetext:    "<pool>",
			Description: "Name for the new pool.",
		},
		"size": {
			Type:    apischema.Integer,
			Minimum: apischema.Ptr(1.0),
			// Ceph's own ceiling on replicas per pool.
			Maximum:     apischema.Ptr(255.0),
			Typetext:    "<integer>",
			Description: "Replica count. Ceph keeps this many copies of every object.",
		},
		"pg_num": {
			Type:        apischema.Integer,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(1048576.0),
			Typetext:    "<integer>",
			Description: "Placement group count for the pool.",
		},

		"min_size": optCount(255,
			"Minimum replicas that must be available for I/O to continue. Omitted or 0 leaves Ceph's default."),
		"application": optString(64, "<rbd|cephfs|rgw>",
			"Application tag for the pool. Omitted lets Proxmox choose, which is rbd for a Proxmox storage pool."),
		"crush_rule_name": optString(128, "<rule>",
			"CRUSH rule to place the pool's data with. Omitted uses the cluster's default rule."),
		"pg_autoscale_mode": optString(16, "<on|off|warn>",
			"PG autoscaler mode for the pool. Omitted leaves Ceph's default."),
	}
}
