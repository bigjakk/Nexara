package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/reports"
)

// The shared declaration vocabulary this file uses — optFlag from
// registry_vms.go, where the first migrated domain defined it, and
// emptyOrUUID from registry_pbs.go — lives with the domain that introduced
// it. pathPrefix is the registry's own, in registry.go.

// reportScope is the instance-wide collection every report route hangs off.
// NOTHING in this domain is cluster-scoped in its PATH: a schedule and a run
// each carry the cluster they belong to as a stored column, which is what
// makes almost every route here Deferred.
const reportScope = pathPrefix + "reports"

// reportIDParam is a schedule id or a run id as a PATH parameter.
//
// It is spelled :id because that is what the eight routes carrying one
// already register.
// The name is one of the two clusterIDFromParam reads (see gateParamNames in
// registry.go), which is safe only because it resolves to the PATH —
// checkPathParams refuses the same name declared as a body or query
// parameter — and because namesACluster refuses a cluster-scoped Check on a
// path that does not start with /clusters/:id, so nobody can turn a report
// id into a cluster the gate would authorize.
func reportIDParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Format:      "uuid",
		Typetext:    "<uuid>",
		Description: description,
	}
}

// reportClusterParam is the cluster a schedule or a run belongs to, in the
// three BODIES that carry one.
//
// It is declared as report_cluster_id with "cluster_id" as an ALIAS, for
// exactly the reason pbsAttachedClusterParam (registry_pbs.go) is:
// checkPathParams refuses a parameter NAMED cluster_id that resolves to
// anything but the path, because clusterIDFromParam reads that name to
// decide which cluster the permission gate authorizes. No gate runs on these
// paths at all — all three are Deferred — but the refusal is deliberately
// about the NAME rather than about whether today's path happens to make it
// safe, and routing around a fail-closed guard is how the hole it protects
// gets reopened by the next path edit. The alias is what keeps every
// existing caller working: the schedule form and the generate dialog both
// send {"cluster_id": …}.
//
// Worth knowing before a FOURTH parameter takes this shape: checkPathParams
// compares gateParamNames against the declared NAMES only, never against an
// Alias, so the alias is not itself guarded. That is harmless on every route
// that uses it today — all of them are Deferred, and namesACluster refuses a
// cluster-scoped Check on a path with no /clusters/:id prefix — but an alias
// of "cluster_id" on a route that DID carry such a prefix would put the gate's
// own name in a body with nothing firing.
func reportClusterParam(optional bool, description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Alias:       "cluster_id",
		Optional:    optional,
		Format:      "uuid",
		Typetext:    "<uuid>",
		Description: description + " Also accepted as \"cluster_id\", which is what Nexara's own dialogs send.",
	}
}

// reportTypeEnum is the catalogue the schedule and generate bodies accept,
// derived from reports.AllTypes so the enum, the generator's switch and the
// report_schedules CHECK constraint cannot drift apart.
//
// Built fresh on every call rather than shared, for the reason the storage
// type enum gives: Property.Enum is only deep-copied on the StdOption path,
// so a shared package-level slice would give every Server's schema the same
// backing array.
func reportTypeEnum() []string {
	out := make([]string, 0, len(reports.AllTypes))
	for _, t := range reports.AllTypes {
		out = append(out, string(t))
	}
	return out
}

// reportFormatEnum is the rendering the schedule stores.
//
// The EMPTY string is a member, and that is not an oversight: the handler
// read "" as "html" (`if format != "" && format != "html" && format != "csv"`
// followed by `if req.Format == "" { req.Format = "html" }`), so an external
// caller spelling the default that way has always worked. Dropping it would
// 400 a request that has always succeeded, which the sentinel decisions in
// registry_vms.go make the standing rule here.
func reportFormatEnum() []string { return []string{"", "html", "csv"} }

// reportRecipientsParam is the recipient list both email-carrying bodies
// take.
//
// The element rule is handlers.EmailAddressPattern, the exact regex the
// handlers enforced, rather than apischema's registered "email" format —
// the two disagree in both directions and the format normalises what it
// validates, so swapping would have been a behaviour change rather than a
// migration. handlers.EmailAddressPattern's own doc comment states the
// differences; picking one of the two for the whole API is a separate
// decision.
//
// MaxLength on an ARRAY counts ELEMENTS (checkLength in apischema), so this
// is the `len(recipients) > 50` cap stated declaratively.
func reportRecipientsParam(description string) apischema.Property {
	return apischema.Property{
		Type:      apischema.Array,
		Optional:  true,
		MaxLength: apischema.Ptr(handlers.MaxEmailRecipients),
		Items: &apischema.Property{
			Type:        apischema.String,
			Pattern:     handlers.EmailAddressPattern,
			MaxLength:   apischema.Ptr(320),
			Typetext:    "<address>",
			Description: "Email address.",
		},
		Typetext:    "<address>[,<address>...]",
		Description: description,
	}
}

// reportParametersParam is the per-type options object both schedule bodies
// and the generate body carry.
//
// An Object with no nested schema, for the reason storagePluginParams gives:
// the accepted keys are reports.Params' own and they grow with the
// catalogue, so a copy here would date. reports.NormalizeParams still owns
// the per-key bounds and the canonicalisation, and validateScheduleFields
// still owns the 64 KB cap — apischema has no serialized-size facet for an
// Object. What the declaration buys is that the options can only arrive
// HERE: a caller cannot spread them across the top level of the body,
// because an undeclared top-level key is now rejected by name.
func reportParametersParam() apischema.Property {
	return apischema.Property{
		Type:     apischema.Object,
		Optional: true,
		Typetext: "<object>",
		Description: "Per-type report options — stale_after_hours, top_n, snapshot_warn_days and the " +
			"sections map. Unknown keys are dropped rather than stored.",
	}
}

// reportStoredClusterReason is the Deferred justification the eight routes
// that load a row share.
//
// Each one reads the schedule or the run by its id and then authorizes
// against the cluster that row STORES. There is no cluster in the path for
// middleware to resolve — and that is also what makes the authorization
// correct rather than a cross-object read: the object names its own cluster,
// so a caller can never pair an id from one cluster with a cluster they can
// see.
const reportStoredClusterReason = "the cluster to authorize is a column on the row, not a segment of the " +
	"path: the handler loads the schedule or the run by id and then gates on its stored cluster_id, " +
	"which middleware cannot do because it runs before any query"

// reportBodyClusterReason is the Deferred justification for the two routes
// that reach the same decision from the BODY rather than from a row.
const reportBodyClusterReason = "the cluster to authorize is named in the body, which no middleware can " +
	"read: the handler gates on the cluster_id the request carries"

// registerReportEndpoints declares the 12 routes served by ReportHandler.
//
// NOT ONE of them is a plain Check, and that is the domain's shape rather
// than an artefact of the migration: a report belongs to a cluster, but the
// path never names one. Two listings filter through accessibleClusters
// (Advisory), two take the cluster from the body (Deferred), and the other
// eight load a row and authorize against the cluster it stores (Deferred).
// Every hand-placed permission call therefore stays in its handler; the
// declarations state what each one checks so an operator building a role can
// still read it.
func registerReportEndpoints(reg *Registry, h *handlers.ReportHandler) {
	// ── Schedules ─────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   reportScope + "/schedules",
		Description: "List report schedules. Filtered rather than gated: a schedule appears only when " +
			"the caller holds view:report on the cluster it belongs to.",
		Group: "Reports",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check: Check{Action: "view", Resource: "report", Scope: ScopeCluster},
			Reason: "accessibleClusters(\"view\", \"report\") builds the cluster set, clusterScopeFilter " +
				"turns it into the SQL scope the listing queries with, and PermitsCluster re-checks each " +
				"row — the listing spans every cluster, so there is none for a gate to resolve",
		}},
		Parameters: nil,
		Handler:    h.ListSchedules,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   reportScope + "/schedules",
		// Deferred renders as the bare word "deferred", so the permission an
		// operator building a role needs is spelled out here.
		Description: "Create a report schedule. Requires manage:report on the cluster named in the body.",
		Group:       "Reports",
		Permissions: Permissions{Deferred: reportBodyClusterReason},
		Parameters:  createScheduleParams(),
		Handler:     h.CreateSchedule,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        reportScope + "/schedules/:id",
		Description: "Get one report schedule. Requires view:report on the cluster the schedule belongs to.",
		Group:       "Reports",
		Permissions: Permissions{Deferred: reportStoredClusterReason},
		Parameters:  apischema.Properties{"id": reportIDParam("Report schedule identifier.")},
		Handler:     h.GetSchedule,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   reportScope + "/schedules/:id",
		Description: "Change a report schedule. Every parameter is optional; the ones omitted keep their " +
			"stored value, and saving the schedule makes the caller the user its runs read under. " +
			"Requires manage:report on the cluster the schedule belongs to, and additionally on the " +
			"target cluster when the schedule is being moved.",
		Group:       "Reports",
		Permissions: Permissions{Deferred: reportStoredClusterReason},
		Parameters:  updateScheduleParams(),
		Handler:     h.UpdateSchedule,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        reportScope + "/schedules/:id",
		Description: "Delete a report schedule. Runs it already produced are kept. Requires manage:report on the cluster the schedule belongs to.",
		Group:       "Reports",
		Permissions: Permissions{Deferred: reportStoredClusterReason},
		Parameters:  apischema.Properties{"id": reportIDParam("Report schedule identifier.")},
		Handler:     h.DeleteSchedule,
	})

	// ── Generation ────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   reportScope + "/generate",
		Description: "Generate a report now and store the run. Renders synchronously and is capped at 3 " +
			"concurrent generations instance-wide, so a fourth caller gets 429. Requires generate:report " +
			"on the cluster named in the body.",
		Group:       "Reports",
		Permissions: Permissions{Deferred: reportBodyClusterReason},
		Parameters:  generateReportParams(),
		Handler:     h.GenerateReport,
	})

	// ── Runs ──────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   reportScope + "/runs",
		Description: "List report runs, newest first. Filtered rather than gated: a run appears only " +
			"when the caller holds view:report on the cluster it belongs to.",
		Group: "Reports",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check: Check{Action: "view", Resource: "report", Scope: ScopeCluster},
			Reason: "accessibleClusters(\"view\", \"report\") builds the cluster set, clusterScopeFilter " +
				"turns it into the SQL scope the listing queries with, and PermitsCluster re-checks each " +
				"row — the listing spans every cluster, so there is none for a gate to resolve",
		}},
		Parameters: nil,
		Handler:    h.ListRuns,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        reportScope + "/runs/:id",
		Description: "Get one report run's metadata and status. Requires view:report on the cluster the run belongs to.",
		Group:       "Reports",
		Permissions: Permissions{Deferred: reportStoredClusterReason},
		Parameters:  apischema.Properties{"id": reportIDParam("Report run identifier.")},
		Handler:     h.GetRun,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   reportScope + "/runs/:id/html",
		Description: "Download a finished run's rendered HTML. Served as a document rather than through " +
			"the JSON envelope, under a locked-down Content-Security-Policy. Requires view:report on the " +
			"cluster the run belongs to.",
		Group:       "Reports",
		Permissions: Permissions{Deferred: reportStoredClusterReason},
		Parameters:  apischema.Properties{"id": reportIDParam("Report run identifier.")},
		Handler:     h.GetRunHTML,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        reportScope + "/runs/:id/csv",
		Description: "Download a finished run's CSV as an attachment. Requires view:report on the cluster the run belongs to.",
		Group:       "Reports",
		Permissions: Permissions{Deferred: reportStoredClusterReason},
		Parameters:  apischema.Properties{"id": reportIDParam("Report run identifier.")},
		Handler:     h.GetRunCSV,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   reportScope + "/runs/:id",
		Description: "Delete a report run. Requires manage:report — not view:report — on the cluster the " +
			"run belongs to: a run is the durable record of a report, and removing one is a management " +
			"act rather than a viewing one.",
		Group:       "Reports",
		Permissions: Permissions{Deferred: reportStoredClusterReason},
		Parameters:  apischema.Properties{"id": reportIDParam("Report run identifier.")},
		Handler:     h.DeleteRun,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   reportScope + "/runs/:id/email",
		Description: "Send a finished run through an email channel — the digest as the body, the stored " +
			"HTML attached, and the CSV when asked for. Requires generate:report on the cluster the run " +
			"belongs to: delivering a report is the same act as producing one, and nothing here reads " +
			"anything the caller could not already download.",
		Group:       "Reports",
		Permissions: Permissions{Deferred: reportStoredClusterReason},
		Parameters: apischema.Properties{
			"id": reportIDParam("Report run identifier."),
			"channel_id": {
				Type:        apischema.String,
				Format:      "uuid",
				Typetext:    "<uuid>",
				Description: "Notification channel to send through. Must be of type email.",
			},
			"recipients": reportRecipientsParam(
				"Addresses to send to. Omitted or empty uses the channel's own recipient list."),
			"with_csv": optFlag("Attach the run's CSV as well as its HTML."),
		},
		Handler: h.EmailRun,
	})
}

// createScheduleParams is the body of POST /api/v1/reports/schedules.
//
// FOUR parameters are required, and the set is pinned to what the handler
// refused rather than to what reads as a sensible minimum:
//
//	name             1–200 characters
//	report_type      a member of the catalogue
//	cluster_id       a uuid, and the cluster the gate authorizes
//	time_range_hours 1–8760
//
// time_range_hours is the one worth spelling out. The handler carries
// `if req.TimeRangeHours == 0 { req.TimeRangeHours = 168 }`, which looks like
// a default and is not: validateScheduleRequest ran FIRST and refused
// anything below 1, so a request omitting it has always been a 400 and that
// line has never been reachable on this route. Declaring it optional with a
// default of 168 would therefore be a behaviour change dressed as tidying.
// The schedule form sends it on every save.
//
// `schedule` is OPTIONAL and empty-able, and it was briefly listed above as
// the fifth required parameter — which was wrong, and is worth recording
// because the mistake is an easy one to make twice.
//
// The handler validated the cron only when non-empty AND computed
// next_run_at only when non-empty, so BOTH an omitted key and an empty
// value produced a manual-only row and a 201. Reading "may be EMPTY" as
// "required, but the empty string is allowed" preserves half of that and
// turns the other half into a 400 naming a parameter the caller never had
// to send. Optional with no Default is what reproduces it: an absent key
// and "" are then indistinguishable to the handler, which is precisely
// what the old value-typed struct field gave it.
func createScheduleParams() apischema.Properties {
	return apischema.Properties{
		"name": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(200),
			Typetext:    "<string>",
			Description: "Display name for the schedule.",
		},
		"report_type": {
			Type:        apischema.String,
			Enum:        reportTypeEnum(),
			Typetext:    "<report type>",
			Description: "Which report to produce. The catalogue is reports.AllTypes, which the database's own CHECK constraint mirrors.",
		},
		"report_cluster_id": reportClusterParam(false,
			"Cluster the report covers, and the cluster manage:report is checked against."),
		"time_range_hours": {
			Type:        apischema.Integer,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(8760.0),
			Typetext:    "<integer>",
			Description: "How far back each run looks, in hours. Up to 8760, which is a year.",
		},
		"schedule": {
			Type: apischema.String,
			// Optional AND no MinLength, which are two different concessions to
			// the same old behaviour and both are load-bearing.
			//
			// The handler ran `if schedule != ""` before validating, so BOTH an
			// empty value and an ABSENT key produced a schedule that never fires
			// on its own — inserted with a NULL next_run_at and answered 201.
			// Declaring the key required preserved the empty case and broke the
			// absent one: a caller that had always omitted it got a 400 naming a
			// parameter it had never needed to send.
			Optional:  true,
			MaxLength: apischema.Ptr(256),
			Typetext:  "<cron>",
			Description: "When the schedule runs, as a cron expression. Empty means it never fires on " +
				"its own. The expression itself is checked by cronspec.ValidateCron, which owns the " +
				"syntax and answers with a message of its own.",
		},
		"format": {
			Type:     apischema.String,
			Optional: true,
			Enum:     reportFormatEnum(),
			Typetext: "<html|csv>",
			Description: "Rendering the schedule's email attaches. Empty or omitted means html; both " +
				"renderings are stored on every run either way.",
		},

		"email_enabled": optFlag("Email each run through the channel below."),
		"email_channel_id": {
			Type:     apischema.String,
			Optional: true,
			// No uuid FORMAT: the empty string means "no channel", which every
			// registered format rejects, and the handler branched on
			// `*req.EmailChannelID != ""`.
			Pattern:   emptyOrUUID,
			MaxLength: apischema.Ptr(36),
			Typetext:  "<uuid>",
			Description: "Notification channel to email each run through. Looked up and required to be " +
				"of type email, but only when email_enabled is set. Empty or omitted attaches none.",
		},
		"email_recipients": reportRecipientsParam(
			"Addresses each run is emailed to. Omitted stores an empty list, which uses the channel's own recipients."),
		"parameters": reportParametersParam(),
		"enabled": {
			Type:     apischema.Boolean,
			Optional: true,
			// The handler's own default, stated rather than left implicit: a
			// schedule created without saying otherwise is active.
			Default:     true,
			Typetext:    "<boolean>",
			Description: "Whether the schedule fires. Omitted creates it enabled.",
		},
	}
}

// updateScheduleParams is the body of PUT /api/v1/reports/schedules/:id.
//
// EVERY parameter is optional and read through an Opt accessor, so omitting
// one leaves the stored column alone. That is the whole contract of this
// route: the enable/disable toggle PUTs `{"enabled": …}` and nothing else,
// and a Default on any field here would rewrite the rest of the row with it.
//
// email_channel_id is the one field whose EMPTY value is meaningful rather
// than absent — it detaches the channel — which is why it carries the
// empty-or-uuid pattern rather than the uuid format.
func updateScheduleParams() apischema.Properties {
	create := createScheduleParams()
	out := apischema.Properties{
		"id": reportIDParam("Report schedule identifier."),
	}
	for name, prop := range create {
		p := prop.AsOptional()
		// AsOptional keeps the Default, and on this route a Default is exactly
		// what must not survive: `enabled` would then be rewritten to true on
		// every partial save.
		p.Default = nil
		out[name] = p
	}
	// format is the one field whose VOCABULARY differs between the two
	// routes, and it is a pre-existing asymmetry rather than one this
	// declaration introduces. CreateSchedule normalises "" to "html" before
	// the insert; UpdateSchedule never has, and report_schedules.format
	// carries CHECK (format IN ('html','csv')) — so PUT {"format":""} has
	// always written a value the column refuses and come back as
	// "Failed to update schedule", a 500. Offering the empty spelling here
	// would publish, as an accepted value, one that has never produced a
	// working update. The same shape is recorded on
	// pbsAttachedClusterUpdateParam: state each route's actual rule rather
	// than paper over the difference.
	format := out["format"]
	format.Enum = []string{"html", "csv"}
	format.Description = "New rendering for the schedule's email. Omitted leaves it as it is. Unlike " +
		"the create body this does NOT accept an empty value: the column refuses it and the update " +
		"has never normalised it."
	out["format"] = format

	// The rest need a different description on this route, because what
	// "omitted" means changes.
	out["report_cluster_id"] = reportClusterParam(true,
		"Move the schedule to another cluster, which additionally requires manage:report on the target. "+
			"Omitted leaves it where it is.")
	enabled := out["enabled"]
	enabled.Description = "Whether the schedule fires. Omitted leaves it as it is."
	out["enabled"] = enabled
	schedule := out["schedule"]
	schedule.Description = "New cron expression. Omitted leaves the stored one alone and does NOT " +
		"re-validate it, so a schedule whose expression can never fire is still disable-able. An " +
		"explicit empty value stops it firing on its own."
	out["schedule"] = schedule
	return out
}

// generateReportParams is the body of POST /api/v1/reports/generate.
//
// TWO parameters are required — report_type and cluster_id — which is
// exactly what the handler refused. time_range_hours is optional here,
// unlike on the schedule create: this handler applied its 168 default BEFORE
// the range check (`if req.TimeRangeHours <= 0 { … = 168 }`), so omitting it
// has always worked.
//
// `format` is declared although the handler does nothing with it. It was
// validated and then discarded — a run always stores both renderings and the
// caller picks one with the /html or /csv route — and an undeclared key is
// now a 400, so dropping it would break a caller that has been sending it.
// The Description says so rather than leaving the next reader to discover it.
func generateReportParams() apischema.Properties {
	return apischema.Properties{
		"report_type": {
			Type:        apischema.String,
			Enum:        reportTypeEnum(),
			Typetext:    "<report type>",
			Description: "Which report to produce.",
		},
		"report_cluster_id": reportClusterParam(false,
			"Cluster the report covers, and the cluster generate:report is checked against."),
		"time_range_hours": {
			Type:     apischema.Integer,
			Optional: true,
			// The value the handler substituted for a missing or non-positive
			// one. The Minimum refuses a negative rather than silently
			// substituting this, the trade the migration and CVE listings made.
			Default:     168,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(8760.0),
			Typetext:    "<integer>",
			Description: "How far back the report looks, in hours. Omitted is 168, a week.",
		},
		"format": {
			Type:     apischema.String,
			Optional: true,
			Enum:     reportFormatEnum(),
			Typetext: "<html|csv>",
			Description: "Accepted and ignored: a run always stores both renderings, and the caller " +
				"picks one by fetching /runs/{id}/html or /runs/{id}/csv.",
		},
		"parameters": reportParametersParam(),
	}
}
