package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, withParams and optTristateBool — lives in
// registry_vms.go, where the first migrated domain defined it. emptyOrUUID
// lives in registry_pbs.go.

// The CVE domain has no single path prefix: the four groups below hang off
// the cluster directly rather than off a "/cve" scope, which is how they
// were registered and is not worth changing here — a path rename is a
// break for every caller, and this migration is meant to be one.
const (
	cveScanScope         = clusterScope + "/cve-scans"
	cvePostureScope      = clusterScope + "/security-posture"
	cveScheduleScope     = clusterScope + "/cve-scan-schedule"
	cveNotificationScope = clusterScope + "/cve-notifications"
)

// cveScanIDParam is a scan's Nexara row id as a PATH parameter.
var cveScanIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Typetext:    "<uuid>",
	Description: "Nexara CVE scan identifier.",
}

// registerCVEEndpoints declares the 10 CVE and security-posture routes
// served by CVEHandler.
//
// All 10 declare a plain Check: six on view:cve_scan, four on
// manage:cve_scan, each the single static requireClusterPerm call its
// handler carried. None is Deferred and none is Advisory — every listing
// here resolves one cluster from its own path and gates on it.
//
// Two of the reads additionally re-check that the scan in the path belongs
// to the cluster in the path, and keep doing so. That is not an
// authorization decision the gate could make: the gate authorizes the
// CLUSTER, and a scan id from another cluster paired with a cluster the
// caller can see is a cross-object read no permission check would catch.
// It answers 404, not 403, for the same reason the container routes do —
// whether a scan exists elsewhere is not the caller's to learn.
func registerCVEEndpoints(reg *Registry, h *handlers.CVEHandler) {
	// ── Scans ─────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        cveScanScope,
		Description: "List the cluster's CVE scans, newest first.",
		Group:       "Security",
		Permissions: clusterCheck("view", "cve_scan"),
		Parameters: clusterParams(apischema.Properties{
			"limit": {
				Type:        apischema.Integer,
				Optional:    true,
				Default:     20,
				Minimum:     apischema.Ptr(1.0),
				Maximum:     apischema.Ptr(100.0),
				Typetext:    "<integer>",
				Description: "Maximum scans to return.",
			},
			"offset": {
				Type:        apischema.Integer,
				Optional:    true,
				Default:     0,
				Minimum:     apischema.Ptr(0.0),
				Maximum:     apischema.Ptr(1000000.0),
				Typetext:    "<integer>",
				Description: "Scans to skip before the first one returned.",
			},
		}),
		Handler: h.ListScans,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   cveScanScope,
		Description: "Start a CVE scan of every node in the cluster. The scan runs in the background; " +
			"the response carries the pending scan row to poll.",
		Group:       "Security",
		Permissions: clusterCheck("manage", "cve_scan"),
		Parameters:  clusterParams(nil),
		Handler:     h.TriggerScan,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        cveScanScope + "/:scan_id",
		Description: "Get one CVE scan with its per-node results.",
		Group:       "Security",
		Permissions: clusterCheck("view", "cve_scan"),
		Parameters:  clusterParams(apischema.Properties{"scan_id": cveScanIDParam}),
		Handler:     h.GetScan,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   cveScanScope + "/:scan_id/vulnerabilities",
		Description: "List a scan's vulnerabilities. The kev filter is applied on its own, ahead of " +
			"severity and node_id, because the dashboard's actively-exploited callout links straight here.",
		Group:       "Security",
		Permissions: clusterCheck("view", "cve_scan"),
		Parameters: clusterParams(apischema.Properties{
			"scan_id": cveScanIDParam,
			"severity": {
				Type: apischema.String,
				// CVESeverities plus the EMPTY string, which is not a
				// severity: the handler checked only a non-empty value and
				// read ?severity= as every severity (`if severity != ""`),
				// so leaving it out would 400 a request that always worked.
				//
				// Built fresh rather than aliased, for the reason the VM
				// status route's enum gives: Property.Enum is only
				// deep-copied on the StdOption path, so sharing the
				// package-level slice would give every Server's schema the
				// same backing array. Appending to a one-element literal
				// always allocates, so no slices.Clone is needed.
				Enum:     append([]string{""}, handlers.CVESeverities...),
				Optional: true,
				Typetext: "<critical|high|medium|low|unknown>",
				// The handler applies ONE filter, not a combination: kev when it
				// is true, else node_id when it is non-empty, else this. So the
				// Description has to say when a severity is ignored — and say
				// it by value, not by presence: kev=false, or an empty kev or
				// node_id, does not displace it.
				Description: "Return only vulnerabilities at this severity. Ignored when kev is true or node_id " +
					"is non-empty: the filters are not combined, and kev, then node_id, takes precedence. " +
					"Empty or omitted returns every severity.",
			},
			"node_id": {
				Type:     apischema.String,
				Optional: true,
				// Empty-or-uuid rather than the uuid format, which rejects "":
				// the handler parsed only a non-empty value, so ?node_id=
				// meant every node's.
				Pattern:   emptyOrUUID,
				MaxLength: apischema.Ptr(36),
				Typetext:  "<uuid>",
				Description: "Return only vulnerabilities found on this node. Ignored when kev is true; takes " +
					"precedence over severity. Empty or omitted returns every node's.",
			},
			"kev": {
				Type:     apischema.Boolean,
				Optional: true,
				Default:  false,
				// The handler compared the raw value with "true", so ?kev= was
				// false — no KEV filter, and the node and severity filters
				// still applied. An empty value is therefore ABSENT here, which
				// puts it on the Default like an omitted one; no boolean
				// spelling is empty, so without this ?kev= would be a 400.
				EmptyIsAbsent: true,
				Typetext:      "<boolean>",
				Description: "Return only vulnerabilities on CISA's Known Exploited Vulnerabilities " +
					"catalogue. Applied INSTEAD of severity and node_id, not alongside them. Empty or " +
					"omitted is false.",
			},
		}),
		Handler: h.ListVulnerabilities,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        cveScanScope + "/:scan_id",
		Description: "Delete a CVE scan and every vulnerability row it produced.",
		Group:       "Security",
		Permissions: clusterCheck("manage", "cve_scan"),
		Parameters:  clusterParams(apischema.Properties{"scan_id": cveScanIDParam}),
		Handler:     h.DeleteScan,
	})

	// ── Posture ───────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   cvePostureScope,
		Description: "Summarize the cluster's security posture from its most recent scan. " +
			"Answers status=no_scans with a perfect score when the cluster has never been scanned.",
		Group:       "Security",
		Permissions: clusterCheck("view", "cve_scan"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetSecurityPosture,
	})

	// ── Schedule ──────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        cveScheduleScope,
		Description: "Read the cluster's automatic CVE scan schedule, or the defaults when none is stored.",
		Group:       "Security",
		Permissions: clusterCheck("view", "cve_scan"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetSchedule,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   cveScheduleScope,
		Description: "Change the cluster's automatic CVE scan schedule. Both parameters are optional; " +
			"the one omitted keeps its stored value.",
		Group:       "Security",
		Permissions: clusterCheck("manage", "cve_scan"),
		Parameters: clusterParams(apischema.Properties{
			"enabled": optTristateBool(
				"Whether the scheduler starts scans on its own. OMITTING it keeps the stored value; " +
					"a default of false here would silently switch scanning off for any client that " +
					"sent only the interval."),
			"interval_hours": {
				Type:     apischema.Integer,
				Optional: true,
				// No Default, for the same reason as `enabled`: the handler
				// merges an omitted value with what is stored, and a
				// default would overwrite it with 24 on every partial save.
				Minimum:     apischema.Ptr(1.0),
				Maximum:     apischema.Ptr(168.0),
				Typetext:    "<integer>",
				Description: "Hours between automatic scans, from one hour to one week.",
			},
		}),
		Handler: h.UpdateSchedule,
	})

	// ── Notifications ─────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        cveNotificationScope,
		Description: "Read the cluster's CVE notification settings, or disabled defaults when none are stored.",
		Group:       "Security",
		Permissions: clusterCheck("view", "cve_scan"),
		Parameters:  clusterParams(nil),
		Handler:     h.GetCVENotificationConfig,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   cveNotificationScope,
		Description: "Change the cluster's CVE notification settings. Every parameter is optional; " +
			"the ones omitted keep their stored values.",
		Group:       "Security",
		Permissions: clusterCheck("manage", "cve_scan"),
		Parameters: clusterParams(apischema.Properties{
			"enabled":          optTristateBool("Whether a completed scan sends a notification at all."),
			"notify_on_act":    optTristateBool("Notify for vulnerabilities in CISA SSVC's Act band — exploited, and high impact."),
			"notify_on_attend": optTristateBool("Notify for vulnerabilities in CISA SSVC's Attend band."),
			"channel_ids": {
				Type:     apischema.Array,
				Optional: true,
				Items: &apischema.Property{
					Type:     apischema.String,
					Format:   "uuid",
					Typetext: "<uuid>",
				},
				MaxLength: apischema.Ptr(64),
				Typetext:  "<[uuid,…]>",
				Description: "Notification channels to send to. An EMPTY LIST is a real value that clears " +
					"them, and is what the card sends when notifications are switched off; OMITTING the " +
					"key keeps the stored channels instead.",
			},
			"cooldown_minutes": {
				Type:     apischema.Integer,
				Optional: true,
				// No Default: the handler merges an omitted value with what
				// is stored, so a default would rewrite the operator's
				// cooldown to 60 on every partial save.
				Minimum:     apischema.Ptr(0.0),
				Maximum:     apischema.Ptr(10080.0),
				Typetext:    "<integer>",
				Description: "Minimum minutes between notifications, up to one week. 0 sends every time.",
			},
		}),
		Handler: h.UpdateCVENotificationConfig,
	})
}
