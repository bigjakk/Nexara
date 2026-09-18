package api

import (
	"slices"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, globalCheck, clusterParams, withParams and optString — lives in
// registry_vms.go, where the first migrated domain defined it.
// emptyOrUUID lives in registry_pbs.go; pathPrefix is the registry's own, in
// registry.go.

// The three instance-wide collections this domain hangs off. An alert and an
// alert rule each carry the cluster they belong to as a NULLABLE column rather
// than as a path segment — a NULL means a GLOBAL rule, one watching
// infrastructure no cluster owns — which is what makes five of these routes
// Deferred and two of them Advisory. A notification channel belongs to the
// install outright and has no cluster at all.
const (
	alertScope     = pathPrefix + "alerts"
	alertRuleScope = pathPrefix + "alert-rules"
	channelScope   = pathPrefix + "notification-channels"
)

// alertIDParam is the :id every route in this domain that names one row takes —
// an alert-history row, an alert rule, a notification channel or a maintenance
// window.
//
// It is spelled :id because that is what every route carrying one already
// registers. The name is one of the two clusterIDFromParam reads (see
// gateParamNames in registry.go), which is safe only because it resolves to the
// PATH — checkPathParams refuses the same name declared as a body or query
// parameter — and because no route in this file declares a cluster-scoped Check
// on a path whose first placeholder is :id: namesACluster would refuse one, so
// an alert id can never become the cluster a gate authorizes.
func alertIDParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Format:      "uuid",
		Typetext:    "<uuid>",
		Description: description,
	}
}

// alertFilterClusterParam is the ?cluster_id= filter the two instance-wide
// listings accept.
//
// It is declared as filter_cluster_id with "cluster_id" as an ALIAS, for the
// reason pbsAttachedClusterParam (registry_pbs.go) and reportClusterParam
// (registry_reports.go) give: checkPathParams refuses a parameter NAMED
// cluster_id that resolves to anything but the path, because clusterIDFromParam
// reads that name to decide which cluster a gate authorizes. Both listings are
// Advisory and run no gate at all, but the refusal is about the NAME rather
// than about whether today's path happens to make it safe. The alias keeps the
// existing callers working: the alert pages send ?cluster_id=.
//
// The EMPTY string is accepted and means "do not filter", which is what both
// handlers already read (`if clusterIDQ != ""`). That is why it carries the
// empty-or-uuid pattern rather than the uuid format, which rejects "" — the
// same sentinel decision registry_vms.go made the standing rule.
func alertFilterClusterParam() apischema.Property {
	return apischema.Property{
		Type:      apischema.String,
		Alias:     "cluster_id",
		Optional:  true,
		Pattern:   emptyOrUUID,
		MaxLength: apischema.Ptr(36),
		Typetext:  "<uuid>",
		Description: "Narrow the listing to one cluster, which additionally requires view:alert on it. " +
			"Empty or omitted returns every cluster the caller can see. Also accepted as \"cluster_id\", " +
			"which is what Nexara's own pages send.",
	}
}

// alertPageParams is the limit/offset pair the four paged listings share.
//
// The bounds now REFUSE a value outside them rather than silently substituting
// the default, which is the trade the migration, CVE and PBS-task listings
// already made and for the same reason: a page size the caller did not choose
// is indistinguishable from one they did, so a paging bug reads as missing
// data. `limit=0` and `limit=500` used to come back as 50 with no indication.
func alertPageParams(noun string) apischema.Properties {
	return apischema.Properties{
		"limit": {
			Type:     apischema.Integer,
			Optional: true,
			// The value the handler substituted for a missing, empty,
			// unparseable or out-of-range ?limit=.
			Default:     50,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(100.0),
			Typetext:    "<integer>",
			Description: "Maximum " + noun + " to return.",
		},
		"offset": {
			Type:     apischema.Integer,
			Optional: true,
			Default:  0,
			Minimum:  apischema.Ptr(0.0),
			// A sanity ceiling rather than a limit the handler had: it bounded
			// nothing above, and an offset in the tens of millions is a typo.
			Maximum:     apischema.Ptr(1000000.0),
			Typetext:    "<integer>",
			Description: "How many " + noun + " to skip before the first one returned.",
		},
	}
}

// alertRowClusterReason is the Deferred justification the five routes that load
// a row share.
//
// Each reads the alert or the rule by its id and then authorizes against the
// cluster that row STORES — and against the instance-wide grant when the column
// is NULL, which is what a GLOBAL rule is: one watching infrastructure no
// cluster owns. There is no cluster in the path for middleware to resolve, and
// middleware runs before any query, so neither branch can be hoisted.
//
// It is also what makes the authorization correct rather than a cross-object
// read: the row names its own cluster, so a caller can never pair an id from
// one cluster with a cluster they can see.
const alertRowClusterReason = "the cluster to authorize is a column on the row, not a segment of the " +
	"path: the handler loads the alert or the rule by id and gates cluster-scoped on its stored " +
	"cluster_id, falling back to the instance-wide grant when that column is NULL — which is what a " +
	"global-scope rule is. Middleware cannot make either decision, because it runs before any query"

// alertScopeReason is the Advisory justification the two instance-wide listings
// share.
const alertScopeReason = "accessibleClusters(\"view\", \"alert\") builds the cluster set, " +
	"clusterScopeFilter turns it into the SQL scope the listing pages under — before LIMIT/OFFSET, so a " +
	"scoped caller never gets short pages with holes in them — and a per-row check re-applies it, " +
	"answering a NULL cluster_id with the instance-wide grant alone; the listing spans every cluster, " +
	"so there is none for a gate to resolve"

// registerAlertEndpoints declares 20 of AlertHandler's 22 routes.
//
// The split is:
//
//	13  Check     the six cluster-scoped routes, whose cluster is the first
//	              parameter of their own path, and the seven instance-wide
//	              ones whose subject is the install — the alert summary and
//	              the six notification-channel routes
//	 2  Advisory  the alert-history and alert-rule listings, which filter
//	              through accessibleClusters instead of gating
//	 5  Deferred  the routes that load a row by id and branch on whether it
//	              carries a cluster (alertRowClusterReason)
//
// TWO routes are deliberately left in router.go — POST /api/v1/alert-rules and
// PUT /api/v1/alert-rules/:id — and the reason is a gap in the parameter
// vocabulary rather than anything about the routes themselves:
//
//	their body carries `escalation_chain`, a JSON ARRAY OF OBJECTS (the
//	channel/delay steps a firing rule escalates through), and apischema
//	cannot describe one. Property.Items is restricted to scalar element types
//	by compileItems in apischema/validate.go — "only scalar element types are
//	supported" — and an Object parameter cannot stand in because the value is
//	an array rather than a map.
//
// Declaring them without `escalation_chain` is not an option either: an
// undeclared key is now a 400, so it would break the escalation editor on every
// save. TestAlertRuleWritesAreStillLegacy pins both halves of that, so "20 of
// 22" is an assertion rather than something a reader has to notice, and it
// fails the moment apischema grows object items. It is the same carve-out
// registerFirewallTemplateEndpoints records for the same reason.
func registerAlertEndpoints(reg *Registry, h *handlers.AlertHandler) {
	// ── Alert history ─────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   alertScope,
		Description: "List alert history, newest first, optionally narrowed by state, severity or " +
			"cluster. Filtered rather than gated: an alert appears only when the caller holds view:alert " +
			"on the cluster it belongs to, and a GLOBAL alert — one raised by a rule watching " +
			"infrastructure no cluster owns — needs the instance-wide grant.",
		Group: "Alerts",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "alert", Scope: ScopeCluster},
			Reason: alertScopeReason,
		}},
		Parameters: withParams(alertPageParams("alerts"), apischema.Properties{
			// The EMPTY string is a member of both enums because the handlers
			// validated only a non-empty value and passed "" to SQL as "do not
			// filter". Dropping it would 400 a request that has always worked.
			"state": {
				Type:        apischema.String,
				Optional:    true,
				Enum:        []string{"", "pending", "firing", "acknowledged", "resolved"},
				Typetext:    "<pending|firing|acknowledged|resolved>",
				Description: "Narrow to one alert state. Empty or omitted returns every state.",
			},
			"severity": {
				Type:        apischema.String,
				Optional:    true,
				Enum:        []string{"", "critical", "warning", "info"},
				Typetext:    "<critical|warning|info>",
				Description: "Narrow to one severity. Empty or omitted returns every severity.",
			},
			"filter_cluster_id": alertFilterClusterParam(),
		}),
		Handler: h.ListAlerts,
	})
	// BEFORE /alerts/:id: Fiber matches in registration order, so a literal
	// path declared after a :param path that also matches it is unreachable.
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   alertScope + "/summary",
		Description: "Count the alerts that are firing, pending and acknowledged right now, and the " +
			"firing ones by severity. Instance-wide and NOT filtered per cluster, which is why it needs " +
			"the instance-wide view:alert rather than a grant on one cluster.",
		Group:       "Alerts",
		Permissions: globalCheck("view", "alert"),
		Parameters:  nil,
		Handler:     h.GetAlertSummary,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   alertScope + "/:id",
		// Deferred renders as the bare word "deferred", so the permission an
		// operator building a role needs is spelled out here. Every Deferred
		// route in this file says it the same way.
		Description: "Get one alert. Requires view:alert on the cluster the alert belongs to, or the " +
			"instance-wide view:alert when it carries no cluster.",
		Group:       "Alerts",
		Permissions: Permissions{Deferred: alertRowClusterReason},
		Parameters:  apischema.Properties{"id": alertIDParam("Alert history identifier.")},
		Handler:     h.GetAlert,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   alertScope + "/:id/acknowledge",
		Description: "Acknowledge a firing alert, which silences its escalation without claiming the " +
			"condition is over. Only an alert in the firing state can be acknowledged. Requires " +
			"acknowledge:alert on the cluster the alert belongs to, or the instance-wide " +
			"acknowledge:alert when it carries no cluster.",
		Group:       "Alerts",
		Permissions: Permissions{Deferred: alertRowClusterReason},
		Parameters:  apischema.Properties{"id": alertIDParam("Alert history identifier.")},
		Handler:     h.AcknowledgeAlert,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   alertScope + "/:id/resolve",
		Description: "Resolve a firing or acknowledged alert by hand. Requires acknowledge:alert — not " +
			"manage:alert — on the cluster the alert belongs to, or the instance-wide acknowledge:alert " +
			"when it carries no cluster: closing an alert is the same act as acknowledging it, not a " +
			"change to the rule that raised it.",
		Group:       "Alerts",
		Permissions: Permissions{Deferred: alertRowClusterReason},
		Parameters:  apischema.Properties{"id": alertIDParam("Alert history identifier.")},
		Handler:     h.ResolveAlert,
	})

	// ── Alert rules ───────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   alertRuleScope,
		Description: "List alert rules. Filtered rather than gated: a rule appears only when the caller " +
			"holds view:alert on the cluster it is scoped to, and a GLOBAL rule — one with no cluster at " +
			"all — needs the instance-wide grant.",
		Group: "Alerts",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "alert", Scope: ScopeCluster},
			Reason: alertScopeReason,
		}},
		Parameters: withParams(alertPageParams("rules"), apischema.Properties{
			"filter_cluster_id": alertFilterClusterParam(),
		}),
		Handler: h.ListRules,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   alertRuleScope + "/:id",
		Description: "Get one alert rule. Requires view:alert on the cluster the rule is scoped to, or " +
			"the instance-wide view:alert for a global rule.",
		Group:       "Alerts",
		Permissions: Permissions{Deferred: alertRowClusterReason},
		Parameters:  apischema.Properties{"id": alertIDParam("Alert rule identifier.")},
		Handler:     h.GetRule,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   alertRuleScope + "/:id",
		Description: "Delete an alert rule. The alerts it already raised are kept — they are history, " +
			"not state the rule owns. Requires manage:alert on the cluster the rule is scoped to, or the " +
			"instance-wide manage:alert for a global rule.",
		Group:       "Alerts",
		Permissions: Permissions{Deferred: alertRowClusterReason},
		Parameters:  apischema.Properties{"id": alertIDParam("Alert rule identifier.")},
		Handler:     h.DeleteRule,
	})

	// ── Notification channels ─────────────────────────────────────────
	//
	// All six are GLOBAL. A channel is an instance-wide delivery target that
	// any cluster's rule may escalate through, its path names no cluster, and
	// its stored configuration holds a webhook URL or an API token — so
	// reading one is not something a grant on one cluster should buy.
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   channelScope,
		Description: "List the notification channels alert rules can deliver through. The stored " +
			"configuration — webhook URLs, tokens, addresses — is never returned.",
		Group:       "Notification Channels",
		Permissions: globalCheck("view", "notification_channel"),
		Parameters:  nil,
		Handler:     h.ListChannels,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   channelScope,
		Description: "Create a notification channel. The config object is encrypted at rest and never " +
			"read back; its accepted keys are the dispatcher's own and are validated when the channel is " +
			"tested or used.",
		Group:       "Notification Channels",
		Permissions: globalCheck("manage", "notification_channel"),
		Parameters:  createChannelParams(),
		Handler:     h.CreateChannel,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        channelScope + "/:id",
		Description: "Get one notification channel's name, type and enabled flag. The config is never returned.",
		Group:       "Notification Channels",
		Permissions: globalCheck("view", "notification_channel"),
		Parameters:  apischema.Properties{"id": alertIDParam("Notification channel identifier.")},
		Handler:     h.GetChannel,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   channelScope + "/:id",
		Description: "Change a notification channel. Every parameter is optional and the ones omitted " +
			"keep their stored value; sending config replaces it whole, because it is stored encrypted " +
			"and cannot be merged.",
		Group:       "Notification Channels",
		Permissions: globalCheck("manage", "notification_channel"),
		Parameters:  updateChannelParams(),
		Handler:     h.UpdateChannel,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   channelScope + "/:id",
		Description: "Delete a notification channel. Rules that escalate through it keep the reference " +
			"and stop delivering; nothing rewrites them.",
		Group:       "Notification Channels",
		Permissions: globalCheck("manage", "notification_channel"),
		Parameters:  apischema.Properties{"id": alertIDParam("Notification channel identifier.")},
		Handler:     h.DeleteChannel,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   channelScope + "/:id/test",
		Description: "Send a test notification through a channel with a synthetic alert payload. Gated " +
			"on manage rather than view because it makes an outbound call using the stored credential " +
			"rather than reading Nexara's own state.",
		Group:       "Notification Channels",
		Permissions: globalCheck("manage", "notification_channel"),
		Parameters:  apischema.Properties{"id": alertIDParam("Notification channel identifier.")},
		Handler:     h.TestChannel,
	})

	// ── Cluster-scoped ────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/alerts",
		Description: "List one cluster's alert history, newest first. Unlike the instance-wide listing " +
			"this one is gated rather than filtered: the cluster is in its own path, so there is exactly " +
			"one grant to check.",
		Group:       "Alerts",
		Permissions: clusterCheck("view", "alert"),
		Parameters:  clusterParams(alertPageParams("alerts")),
		Handler:     h.ListAlertsByCluster,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/alerts/count",
		Description: "Count one cluster's firing, pending and acknowledged alerts. What the sidebar badge reads.",
		Group:       "Alerts",
		Permissions: clusterCheck("view", "alert"),
		Parameters:  clusterParams(nil),
		Handler:     h.CountActiveAlertsByCluster,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/maintenance-windows",
		Description: "List a cluster's maintenance windows — the periods during which its alerts are " +
			"suppressed.",
		Group:       "Maintenance Windows",
		Permissions: clusterCheck("view", "maintenance_window"),
		Parameters:  clusterParams(alertPageParams("windows")),
		Handler:     h.ListMaintenanceWindows,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/maintenance-windows",
		Description: "Create a maintenance window, optionally pinned to one node. Naming a node ALSO " +
			"requires manage:maintenance_window on the cluster that owns it — deliberately the node's " +
			"own cluster rather than the one in the path, which is what stops a caller who manages " +
			"cluster A pinning a window to a node in cluster B; the node must then also belong to the " +
			"cluster in the path.",
		Group:       "Maintenance Windows",
		Permissions: clusterCheck("manage", "maintenance_window"),
		Parameters:  clusterParams(maintenanceWindowParams(true)),
		Handler:     h.CreateMaintenanceWindow,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   clusterScope + "/maintenance-windows/:id",
		Description: "Change a maintenance window. Every parameter is optional and an omitted or EMPTY " +
			"one leaves the stored value alone — there is no way to clear a description through this " +
			"route, which is how it has always behaved. A window belonging to another cluster answers " +
			"404 rather than being edited through this one. Naming a node ALSO requires " +
			"manage:maintenance_window on the cluster that owns it, exactly as the create does — the " +
			"node's own cluster, not the one in the path — and the node must then also belong to the " +
			"cluster in the path.",
		Group:       "Maintenance Windows",
		Permissions: clusterCheck("manage", "maintenance_window"),
		Parameters: clusterParams(withParams(maintenanceWindowParams(false), apischema.Properties{
			"id": alertIDParam("Maintenance window identifier."),
		})),
		Handler: h.UpdateMaintenanceWindow,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   clusterScope + "/maintenance-windows/:id",
		Description: "Delete a maintenance window. Alerts suppressed while it was open are not " +
			"re-raised. A window belonging to another cluster answers 404.",
		Group:       "Maintenance Windows",
		Permissions: clusterCheck("manage", "maintenance_window"),
		Parameters: clusterParams(apischema.Properties{
			"id": alertIDParam("Maintenance window identifier."),
		}),
		Handler: h.DeleteMaintenanceWindow,
	})
}

// maintenanceWindowParams is the body shared by the create and update routes.
//
// required is what differs, and it is pinned to what each handler enforced
// rather than to what reads as a sensible minimum. On CREATE both timestamps
// are parsed unconditionally, so omitting either has always been a 400. On
// UPDATE every field is read as `if x != "" { … }`, so an empty value is
// indistinguishable from an absent one and both leave the stored column alone —
// which is why nothing here is a tristate and why an empty description cannot
// clear one.
//
// Neither timestamp carries a format, because apischema registers none for
// RFC3339 and the handlers parse them with time.Parse — which owns the rule and
// answers with a message naming the field. The length cap is a bound on a
// string that reaches a timestamp parser, not a restatement of RFC3339.
func maintenanceWindowParams(required bool) apischema.Properties {
	timestamp := func(description string) apischema.Property {
		p := apischema.Property{
			Type:        apischema.String,
			Optional:    !required,
			MaxLength:   apischema.Ptr(64),
			Typetext:    "<RFC3339 timestamp>",
			Description: description,
		}
		if required {
			p.MinLength = apischema.Ptr(1)
		}
		return p
	}
	// What an ABSENT node_id means differs between the two routes, and the
	// description has to say which — both render into the API docs under their
	// own endpoint. On create the column starts NULL, so omitting it is "the
	// whole cluster"; on update the handler keeps the stored pin, so omitting it
	// changes nothing and there is no way to unpin a window through this route.
	nodeAbsent := "Empty or omitted suppresses alerts for the whole cluster."
	if !required {
		nodeAbsent = "Empty or omitted leaves the existing pin alone; this route cannot unpin a window."
	}
	return apischema.Properties{
		"node_id": {
			Type:     apischema.String,
			Optional: true,
			// The EMPTY string is the sentinel both handlers read
			// (`if req.NodeID != ""`), and every registered format rejects it —
			// so the rule is a pattern rather than the uuid format. Same
			// reasoning as emptyOrNodeName in registry_vms.go.
			Pattern:   emptyOrUUID,
			MaxLength: apischema.Ptr(36),
			Typetext:  "<uuid>",
			Description: "Pin the window to one node. " + nodeAbsent + " The node must belong to the " +
				"cluster in the path, and naming it requires manage:maintenance_window on the cluster " +
				"that owns it.",
		},
		"description": optString(1024, "<string>",
			"Free-text note on why the window exists. An empty value is treated as absent, not as a clear."),
		"starts_at": timestamp("When the suppression starts, as an RFC3339 timestamp."),
		"ends_at":   timestamp("When it ends, as an RFC3339 timestamp. Must be after starts_at."),
	}
}

// createChannelParams is the body of POST /api/v1/notification-channels.
//
// THREE parameters are required, matching exactly what the handler refused an
// empty value for: name, channel_type and config.
//
// `config` is an Object with no nested schema, for the reason
// reportParametersParam gives: its accepted keys belong to the dispatcher the
// channel_type selects — a Slack webhook URL, a PagerDuty routing key, an SMTP
// host — and they grow with the dispatcher registry, so a copy here would date.
// What the declaration buys is that they can only arrive HERE: a caller cannot
// spread them across the top level of the body, because an undeclared
// top-level key is now rejected by name. The value is encrypted whole and
// never read back.
func createChannelParams() apischema.Properties {
	return apischema.Properties{
		"name": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(255),
			Typetext:    "<string>",
			Description: "Display name for the channel.",
		},
		"channel_type": {
			Type: apischema.String,
			// Cloned rather than aliased: Property.Enum is only deep-copied on
			// the StdOption path, so sharing the package-level slice would give
			// every Server's schema the same backing array.
			Enum:     slices.Clone(handlers.NotificationChannelTypes),
			Typetext: "<email|webhook|slack|discord|pagerduty|teams|telegram>",
			Description: "Which dispatcher delivers this channel. The vocabulary is Nexara's own — it " +
				"selects among the registered dispatchers — so an unrecognised value is refused here " +
				"rather than at delivery time.",
		},
		"config": {
			Type:     apischema.Object,
			Typetext: "<object>",
			Description: "Dispatcher-specific settings — the webhook URL, the API token, the recipient " +
				"list. Stored encrypted and never returned.",
		},
		"enabled": {
			Type:     apischema.Boolean,
			Optional: true,
			// The handler's own default, stated rather than left implicit: a
			// channel created without saying otherwise delivers.
			Default:     true,
			Typetext:    "<boolean>",
			Description: "Whether the channel delivers. Omitted creates it enabled.",
		},
	}
}

// updateChannelParams is the body of PUT /api/v1/notification-channels/:id.
//
// Every parameter but the path `id` is optional, and none of them carries a
// default. `enabled` is the one that matters: the handler reads it through a
// pointer so that omitting it leaves the stored flag alone, and a Default of
// true here would re-enable a disabled channel on every rename.
//
// name and channel_type are read as `if x != ""`, so an empty value is absent
// rather than a clear — which is why neither carries a MinLength that would
// turn an empty string into a 400 the route has never answered.
func updateChannelParams() apischema.Properties {
	return apischema.Properties{
		"id":   alertIDParam("Notification channel identifier."),
		"name": optString(255, "<string>", "New display name. An empty value leaves it as it is."),
		"channel_type": {
			Type:     apischema.String,
			Optional: true,
			// The EMPTY string is a member because the handler validated the
			// value only when non-empty and read "" as "leave the type alone".
			//
			// No slices.Clone here, unlike the create above: appending to a fresh
			// one-element literal always allocates, so this never shares a
			// backing array with the package-level slice.
			Enum:     append([]string{""}, handlers.NotificationChannelTypes...),
			Typetext: "<email|webhook|slack|discord|pagerduty|teams|telegram>",
			Description: "New dispatcher for this channel. Empty or omitted leaves it as it is. " +
				"Changing it does NOT rewrite config, so send both together.",
		},
		"config": {
			Type:     apischema.Object,
			Optional: true,
			Typetext: "<object>",
			Description: "Replacement dispatcher settings, stored encrypted. Omitted keeps the stored " +
				"ones; the object replaces them whole rather than merging, because the stored value is " +
				"ciphertext.",
		},
		"enabled": {
			Type:     apischema.Boolean,
			Optional: true,
			// NO Default — see the doc comment.
			Typetext:    "<boolean>",
			Description: "Whether the channel delivers. Omitted leaves it as it is.",
		},
	}
}
