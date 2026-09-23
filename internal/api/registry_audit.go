package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, globalCheck, clusterParams, withParams, optString and
// taskVmidsParam — lives in registry_vms.go and registry_tasks.go.

// auditScope is the instance-wide collection.
const auditScope = pathPrefix + "audit-log"

// auditFilterParams are the filters the three enumerating reads share: the
// global listing, the export, and the per-cluster listing.
//
// They are declared once because parseAuditFilters parses them once, for all
// three — and the ONE filter that is not here is the reason this is a function
// rather than a var. See auditFilterClusterParam.
func auditFilterParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"limit": {
			Type:     apischema.Integer,
			Optional: true,
			Default:  50,
			Minimum:  apischema.Ptr(1.0),
			Maximum:  apischema.Ptr(200.0),
			Typetext: "<integer>",
			Description: "Page size. On the export it is ignored — that endpoint reads up to its own " +
				"10000-row cap and reports the unclipped total.",
		},
		"offset": {
			Type:        apischema.Integer,
			Optional:    true,
			Default:     0,
			Minimum:     apischema.Ptr(0.0),
			Typetext:    "<integer>",
			Description: "Rows to skip. Ignored on the export, which always starts at the newest row.",
		},
		"resource_type": optString(64, "<string>", "Narrow to one audit_log.resource_type, e.g. vm or cluster."),
		"user_id": {
			Type:        apischema.String,
			Optional:    true,
			Format:      "uuid",
			Typetext:    "<uuid>",
			Description: "Narrow to the entries one user produced.",
		},
		"action": optString(128, "<string>", "Narrow to one action, e.g. vm_created. GET /api/v1/audit-log/actions lists them."),
		"source": optString(32, "<string>", "Narrow to one entry source, e.g. nexara or proxmox."),
		"start_time": {
			Type:        apischema.String,
			Optional:    true,
			MaxLength:   apischema.Ptr(64),
			Typetext:    "<RFC3339 timestamp>",
			Description: "Oldest entry to return, inclusive.",
		},
		"end_time": {
			Type:        apischema.String,
			Optional:    true,
			MaxLength:   apischema.Ptr(64),
			Typetext:    "<RFC3339 timestamp>",
			Description: "Newest entry to return, inclusive.",
		},
		"vmids": taskVmidsParam,
	}, extra)
}

// auditFilterClusterParam is the ?cluster_id= on the two INSTANCE-WIDE reads,
// and it is deliberately absent from the per-cluster one.
//
// Two rules meet here, and they point opposite ways. checkPathParams refuses a
// parameter NAMED cluster_id that resolves to anything but the path, so the
// instance-wide routes declare it under another name with "cluster_id" as an
// alias — the spelling every existing caller sends. But it ALSO refuses that
// alias on a route whose gate resolves the cluster from the path (commit
// facdf56), which is exactly GET /clusters/:cluster_id/audit-log: the handler
// there stamps the path's cluster over whatever the query carried, so accepting
// a second spelling would let a caller believe they had filtered to a cluster
// the gate never authorized.
//
// So the per-cluster listing simply does not declare it, and a caller who sends
// ?cluster_id= there gets a 400 rather than a value silently overwritten. The
// refusal is checkMisplaced's (registry_params.go), not "unknown parameter":
// cluster_id IS declared on that route, as the path parameter, so the query
// copy is reported as a path parameter sent in the wrong place.
var auditFilterClusterParam = apischema.Property{
	Type:     apischema.String,
	Alias:    "cluster_id",
	Optional: true,
	Format:   "uuid",
	Typetext: "<uuid>",
	Description: "Narrow the listing to one cluster. Also accepted as \"cluster_id\". The caller must hold " +
		"view:audit on it, or the request is refused rather than silently emptied.",
}

// auditScopeReason is the Advisory justification the three enumerating reads
// share.
//
// A gate cannot stand in for it, and the COUNT is why. The scope is stamped on
// the listing query AND on the count query, together: a Total computed under a
// wider scope than the Items is a leak no per-row guard can repair — it counted
// every cluster's entries and paginated over them — and it is the bug that
// shape exists to prevent. NULL-cluster rows (logins, settings changes) are
// excluded from a scoped caller by the same clause.
const auditScopeReason = "accessibleClusters(\"view\", \"audit\") builds the scope and applyAuditListScope " +
	"stamps it on BOTH the listing and the count query — a Total computed under a wider scope than the " +
	"Items is a leak no per-row guard can repair — with PermitsCluster re-checking each row and " +
	"NULL-cluster global entries excluded from a scoped caller; the read spans every cluster, so there " +
	"is none for a gate to resolve"

// syslogConfigParams is the body both syslog writes take.
//
// It is one shape for two endpoints because it is one config: the PUT stores it
// and the POST probes it without storing. Every field is optional with a
// default, because that is how the handler read a zero value out of a bound
// struct — and the cross-field rules (a host is required when enabled; the
// protocol is lower-cased and must be one of three) stay in the handler, where
// they can say which field made them fire.
//
// The values here reach an AUDIT ROW, via toSyslogAuditConfig — and that is a
// deliberate, bounded copy rather than Params.Raw(): view:audit is a default
// Viewer grant, so what a syslog change records has to be picked field by
// field. TestGuard_SyslogAuditFieldsClassified in the handlers package fails
// when proxsyslog.Config grows a field that is neither recorded nor listed as
// deliberately omitted.
func syslogConfigParams() apischema.Properties {
	return apischema.Properties{
		"enabled": optFlag("Whether audit entries are forwarded at all. Turning it off is itself audited."),
		"host": optString(256, "<host>",
			"Collector hostname or address. Required when enabled."),
		"port": {
			Type:        apischema.Integer,
			Optional:    true,
			Default:     0,
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(65535.0),
			Typetext:    "<integer>",
			Description: "Collector port. 0, or omitted, means 6514 for tls (RFC 5425) and 514 otherwise — UDP's assigned port (RFC 5426), which TCP collectors conventionally reuse; RFC 6587 assigns TCP none.",
		},
		"protocol": optString(8, "<udp|tcp|tls>",
			"Transport. Case-insensitive; empty or omitted means udp. Only checked when enabled."),
		"facility": {
			Type:        apischema.Integer,
			Optional:    true,
			Default:     0,
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(23.0),
			Typetext:    "<integer>",
			Description: "Syslog facility. 0, or omitted, means 16 (local0).",
		},
		"tls_skip_verify": optFlag(
			"Accept the collector's TLS certificate without verifying it. Recorded in the audit row, " +
				"because turning it on downgrades the transport carrying the audit stream."),
	}
}

// registerAuditEndpoints declares AuditHandler's nine routes.
//
// They split three ways, and the split is what this domain's migration had to
// get right:
//
//	3 Advisory  the two instance-wide listings and the export. None of them
//	            gates; all three FILTER, in SQL, and the filter is the
//	            authorization. See auditScopeReason.
//	5 Check     global view:audit for the two distinct-value lookups, global
//	            manage:audit for the three syslog routes.
//	1 Check     cluster-scoped view:audit for the per-cluster listing, which is
//	            the one read in this domain that names a cluster in its path.
//
// A second conditional check runs on every enumerating read and is NOT part of
// any of these shapes: reservedSettingVisibility resolves, per request, which
// reserved setting keys the caller may see the DETAILS of, and redacts the rest.
// It is not a gate — the row is still returned, and who changed which setting is
// still visible — so it is not what Advisory documents. It exists because
// view:audit is a default Viewer grant and a syslog_forwarding entry carries
// the collector the audit stream is sent to.
func registerAuditEndpoints(reg *Registry, h *handlers.AuditHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   auditScope + "/recent",
		Description: "Return the 50 most recent audit entries the caller may see, enriched with the " +
			"server-authoritative status of any task they dispatched. Scoped in SQL rather than trimmed " +
			"afterwards, so a cluster-scoped caller gets their newest 50 rather than whatever survives of " +
			"the instance's.",
		Group: "Audit Log",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "audit", Scope: ScopeCluster},
			Reason: auditScopeReason,
		}},
		Parameters: apischema.Properties{},
		Handler:    h.ListRecent,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        auditScope + "/actions",
		Description: "List the distinct action values present in the audit log, for the filter dropdown.",
		Group:       "Audit Log",
		Permissions: globalCheck("view", "audit"),
		Parameters:  apischema.Properties{},
		Handler:     h.ListActions,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        auditScope + "/users",
		Description: "List the distinct users present in the audit log, for the filter dropdown.",
		Group:       "Audit Log",
		Permissions: globalCheck("view", "audit"),
		Parameters:  apischema.Properties{},
		Handler:     h.ListUsers,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   auditScope + "/export",
		Description: "Export the matching audit entries as JSON, CSV or RFC 5424 syslog, under the same " +
			"filters and the same scope as the listing. Capped at 10000 rows; the JSON envelope carries " +
			"the unclipped total, so a truncated export is distinguishable from a complete one.",
		Group: "Audit Log",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "audit", Scope: ScopeCluster},
			Reason: auditScopeReason,
		}},
		Parameters: auditFilterParams(apischema.Properties{
			"filter_cluster_id": auditFilterClusterParam,
			"format": {
				Type:        apischema.String,
				Optional:    true,
				Default:     "json",
				Enum:        []string{"json", "csv", "syslog"},
				Typetext:    "<json|csv|syslog>",
				Description: "Output format. Only json carries the total, because the line formats have nowhere to put it.",
			},
		}),
		Handler: h.Export,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   auditScope + "/syslog-config",
		Description: "Read the stored audit-forwarding configuration, or the defaults — forwarding off, udp, " +
			"port 514, facility 16 (local0) — when none has been saved.",
		Group:       "Audit Log",
		Permissions: globalCheck("manage", "audit"),
		Parameters:  apischema.Properties{},
		Handler:     h.GetSyslogConfig,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   auditScope + "/syslog-config",
		Description: "Store the audit-forwarding configuration and reconfigure the live forwarder. The " +
			"change is audited BEFORE the forwarder is reconfigured, so the collector being redirected " +
			"away from — or switched off — is told so over the outgoing connection.",
		Group:       "Audit Log",
		Permissions: globalCheck("manage", "audit"),
		Parameters:  syslogConfigParams(),
		Handler:     h.UpdateSyslogConfig,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   auditScope + "/syslog-test",
		Description: "Probe a forwarding target without storing it. The attempt is audited whether or not " +
			"it connects: this endpoint opens an outbound connection to a host the caller names and " +
			"writes to it, so a run of refused ones is how a network would be swept.",
		Group:       "Audit Log",
		Permissions: globalCheck("manage", "audit"),
		Parameters:  syslogConfigParams(),
		Handler:     h.TestSyslog,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   auditScope,
		Description: "List audit entries under the given filters, with the total counted under the same " +
			"filters and the same scope — so a caller can tell a full page from a truncated one.",
		Group: "Audit Log",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "audit", Scope: ScopeCluster},
			Reason: auditScopeReason,
		}},
		Parameters: auditFilterParams(apischema.Properties{
			"filter_cluster_id": auditFilterClusterParam,
		}),
		Handler: h.List,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/audit-log",
		Description: "List one cluster's audit entries, under the same filters as the instance-wide " +
			"listing. A ?cluster_id= filter is NOT accepted here: the path already names the cluster the " +
			"gate authorized, and a second spelling could only disagree with it.",
		Group:       "Audit Log",
		Permissions: clusterCheck("view", "audit"),
		Parameters:  clusterParams(auditFilterParams(nil)),
		Handler:     h.ListByCluster,
	})
}
