package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — globalCheck, withParams
// and optString — lives in registry_vms.go, where the first migrated domain
// defined it.

// dlqScope is the collection all five routes hang off.
const dlqScope = pathPrefix + "notification-dlq"

// dlqEntryParams is the one path parameter the three per-entry routes carry.
//
// It is spelled :id because that is what all three already register. The name
// is one of the two clusterIDFromParam reads (see gateParamNames in
// registry.go), which is safe on two counts: it resolves to the PATH, so
// checkPathParams' refusal of the same name declared as a body or query
// parameter does not bite, and every route here declares a GLOBAL Check — so no
// cluster is ever resolved out of the path at all.
func dlqEntryParams() apischema.Properties {
	return apischema.Properties{
		"id": {
			Type:        apischema.String,
			Format:      "uuid",
			Source:      apischema.SourcePath,
			Typetext:    "<uuid>",
			Description: "Dead-letter entry identifier.",
		},
	}
}

// registerNotificationDLQEndpoints declares NotificationDLQHandler's five
// routes.
//
// Every one is a GLOBAL Check, and that is the domain's defining fact rather
// than an oversight: a cluster-scoped grant satisfies no global check
// (internal/auth/rbac.go), so a viewer of one cluster is refused outright here
// rather than served a filtered listing. The listing's own doc comment spells
// out why widening it is not a one-line change — LIMIT/OFFSET are applied
// across every cluster's rows and the per-row guard trims afterwards, so a
// scoped caller would page through the global rowset and get short pages with
// holes.
//
// Two of the three writes check MORE than they declare, and that asymmetry is
// deliberate rather than a gap in the declaration:
//
//   - Retry additionally requires manage:notification_channel, because a replay
//     sends a real notification on behalf of a channel the caller may not
//     otherwise be allowed to test or modify. Permissions has no "all of"
//     shape, so the declaration states the gate that always runs and the
//     Description names the second one — which is what an operator building a
//     role reads.
//   - All three per-entry writes additionally require manage:notification_dlq
//     on the ROW's cluster when the row carries one, which is only knowable
//     after the row is read.
//
// Declaring either of those as the whole story would claim less than the code
// enforces, which is the safe direction; claiming more is what this registry
// exists to prevent.
func registerNotificationDLQEndpoints(reg *Registry, h *handlers.NotificationDLQHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   dlqScope,
		Description: "List dead-lettered notifications — dispatches that exhausted their retries or were " +
			"rate-limited — newest first, optionally narrowed to one state or one channel.",
		Group:       "Notification Channels",
		Permissions: globalCheck("view", "notification_dlq"),
		Parameters: apischema.Properties{
			"limit": {
				Type:        apischema.Integer,
				Optional:    true,
				Default:     50,
				Minimum:     apischema.Ptr(1.0),
				Maximum:     apischema.Ptr(100.0),
				Typetext:    "<integer>",
				Description: "Page size.",
			},
			"offset": {
				Type:        apischema.Integer,
				Optional:    true,
				Default:     0,
				Minimum:     apischema.Ptr(0.0),
				Typetext:    "<integer>",
				Description: "Rows to skip.",
			},
			"state": {
				Type:     apischema.String,
				Optional: true,
				// The handler's own validDLQStates, restated one layer earlier,
				// plus the EMPTY string. It is safe to restate — unlike a
				// Proxmox vocabulary, these are Nexara's own column values —
				// and TestNotificationDLQStateVocabulary pins the two lists
				// against each other. "" is not a state: it is "do not
				// filter", which the handler has always read it as
				// (`if state != ""`) and ListNotificationDLQ still does, so
				// dropping it would 400 a request that has always worked —
				// the call the alert listings' enums made too.
				Enum:        []string{"", "pending", "rate_limited", "retrying", "resolved", "dismissed"},
				Typetext:    "<pending|rate_limited|retrying|resolved|dismissed>",
				Description: "Narrow the listing to one state. Empty or omitted returns every state.",
			},
			"channel_id": {
				Type:     apischema.String,
				Optional: true,
				// Empty-or-uuid rather than the uuid format, which rejects "":
				// the handler only parsed a non-empty value, so ?channel_id=
				// meant "every channel".
				Pattern:     emptyOrUUID,
				MaxLength:   apischema.Ptr(36),
				Typetext:    "<uuid>",
				Description: "Narrow the listing to the entries queued for one notification channel. Empty or omitted returns every channel.",
			},
		},
		Handler: h.List,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        dlqScope + "/summary",
		Description: "Return the dead-letter counts per state, for the alerts page's queue widget.",
		Group:       "Notification Channels",
		Permissions: globalCheck("view", "notification_dlq"),
		Parameters:  apischema.Properties{},
		Handler:     h.Summary,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   dlqScope + "/:id/retry",
		Description: "Re-attempt a failed dispatch through the alert engine, so the same retry schedule is " +
			"honoured. Requires manage:notification_channel as well as manage:notification_dlq — a replay " +
			"sends a real notification on behalf of a channel the caller may not otherwise be allowed to " +
			"test or modify — and, for an entry that names a cluster, manage:notification_dlq on that cluster too.",
		Group:       "Notification Channels",
		Permissions: globalCheck("manage", "notification_dlq"),
		Parameters:  dlqEntryParams(),
		Handler:     h.Retry,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   dlqScope + "/:id/dismiss",
		Description: "Mark a dead-letter entry as dismissed without retrying it, for a failure that is no " +
			"longer actionable. For an entry that names a cluster, manage:notification_dlq on that cluster " +
			"is required as well.",
		Group:       "Notification Channels",
		Permissions: globalCheck("manage", "notification_dlq"),
		Parameters:  dlqEntryParams(),
		Handler:     h.Dismiss,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   dlqScope + "/:id",
		Description: "Permanently remove a dead-letter entry. For an entry that names a cluster, " +
			"manage:notification_dlq on that cluster is required as well.",
		Group:       "Notification Channels",
		Permissions: globalCheck("manage", "notification_dlq"),
		Parameters:  dlqEntryParams(),
		Handler:     h.Delete,
	})
}
