package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/reports"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to
// registry_reports.go that quietly loosened a parameter would show up here.

// reportRouteCount is how many endpoints registerReportEndpoints declares.
// See vmRouteCount in registry_vms_test.go for why the registry total is a
// sum of per-domain constants rather than one number.
const reportRouteCount = 12

const testReportID = "2c1b0a99-4d3e-4f5a-8b7c-000000000012"

func reportRoute(path string) string {
	return strings.NewReplacer(":id", testReportID).Replace(path)
}

// reportRoutesOutsideTheClusterCheckShape is this domain's half of the
// registry-wide exception list in registry_vms_test.go.
//
// ALL TWELVE are here, which no other domain has needed, and the reason is
// structural rather than an artefact of the migration: a report belongs to a
// cluster and the path never names one, so there is nothing for a
// cluster-scoped gate to resolve. Listing every one of them individually is
// the point — "the whole domain is exempt" is exactly the shape that rots
// into a rubber stamp, so each entry says which of the three reasons applies.
var reportRoutesOutsideTheClusterCheckShape = func() map[string]string {
	const stored = "Deferred: the handler loads the row by id and gates on the cluster_id it stores; the path names no cluster"
	const body = "Deferred: the handler gates on the cluster_id the body carries; the path names no cluster"
	const advisory = "Advisory: accessibleClusters builds the SQL scope and PermitsCluster re-checks each row; the listing spans every cluster"
	return map[string]string{
		"GET /api/v1/reports/schedules":        advisory,
		"POST /api/v1/reports/schedules":       body,
		"GET /api/v1/reports/schedules/:id":    stored,
		"PUT /api/v1/reports/schedules/:id":    stored,
		"DELETE /api/v1/reports/schedules/:id": stored,
		"POST /api/v1/reports/generate":        body,
		"GET /api/v1/reports/runs":             advisory,
		"GET /api/v1/reports/runs/:id":         stored,
		"GET /api/v1/reports/runs/:id/html":    stored,
		"GET /api/v1/reports/runs/:id/csv":     stored,
		"DELETE /api/v1/reports/runs/:id":      stored,
		"POST /api/v1/reports/runs/:id/email":  stored,
	}
}()

// reportLegacyPermissions is what each handler checked with hand-placed
// calls BEFORE Phase 6f, transcribed from
// `git show HEAD:internal/api/handlers/reports.go` at commit 1d2b59f.
//
// calls is 2 for UpdateSchedule because it authorizes the schedule's CURRENT
// cluster and then, when the body moves it, the TARGET cluster as well —
// which is the check that stops a manage:report holder on cluster A pushing
// a schedule onto cluster B.
var reportLegacyPermissions = map[string]struct {
	permission string
	shape      string
	calls      int
}{
	"GET /api/v1/reports/schedules":        {"view:report", "Advisory", 1},
	"POST /api/v1/reports/schedules":       {"manage:report", "Deferred", 1},
	"GET /api/v1/reports/schedules/:id":    {"view:report", "Deferred", 1},
	"PUT /api/v1/reports/schedules/:id":    {"manage:report", "Deferred", 2},
	"DELETE /api/v1/reports/schedules/:id": {"manage:report", "Deferred", 1},
	"POST /api/v1/reports/generate":        {"generate:report", "Deferred", 1},
	"GET /api/v1/reports/runs":             {"view:report", "Advisory", 1},
	"GET /api/v1/reports/runs/:id":         {"view:report", "Deferred", 1},
	"GET /api/v1/reports/runs/:id/html":    {"view:report", "Deferred", 1},
	"GET /api/v1/reports/runs/:id/csv":     {"view:report", "Deferred", 1},
	"DELETE /api/v1/reports/runs/:id":      {"manage:report", "Deferred", 1},
	"POST /api/v1/reports/runs/:id/email":  {"generate:report", "Deferred", 1},
}

// declaredReportEndpoints returns every declaration in this domain, keyed
// "METHOD path".
func declaredReportEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, reportScope+"/") {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestReportRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes this migration a refactor rather than a change.
//
// NOTHING hoists: all 13 hand-placed calls stay in their handlers, because
// no report route names its cluster in its path. The tally says so out loud
// rather than leaving a reader to infer it from twelve reason strings.
func TestReportRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredReportEndpoints(t)
	if len(declared) != reportRouteCount {
		t.Fatalf("the registry declares %d report routes, want %d", len(declared), reportRouteCount)
	}
	if len(reportLegacyPermissions) != reportRouteCount {
		t.Fatalf("reportLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(reportLegacyPermissions), reportRouteCount)
	}

	var hoisted, kept int
	for key, want := range reportLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want.permission)
			continue
		}
		if e.Permissions.Check != nil {
			hoisted += want.calls
			t.Errorf("%s declares a plain Check; its path names no cluster, so the gate would resolve "+
				"the report's own id as if it were one", key)
			continue
		}
		kept += want.calls
		switch want.shape {
		case "Deferred":
			if e.Permissions.Deferred == "" {
				t.Errorf("%s declares %q, want Deferred", key, e.Permissions.Describe())
				continue
			}
			// A Deferred route renders as the bare word "deferred", so the
			// permission an operator needs has to survive in the prose.
			if !strings.Contains(e.Description, want.permission) {
				t.Errorf("%s is Deferred but its Description never names %q, so the docs tell an operator "+
					"building a role nothing: %q", key, want.permission, e.Description)
			}
			// And the reason has to say WHERE the cluster comes from, not
			// merely that the handler looks at something.
			if !strings.Contains(e.Permissions.Deferred, "cluster") {
				t.Errorf("%s: the Deferred reason does not name the cluster it resolves: %q",
					key, e.Permissions.Deferred)
			}
		case "Advisory":
			if e.Permissions.Advisory == nil {
				t.Errorf("%s declares %q, want Advisory: the listing filters rather than gates",
					key, e.Permissions.Describe())
				continue
			}
			if got := e.Permissions.Advisory.String(); got != want.permission {
				t.Errorf("%s filters on %q but the handler used %q", key, got, want.permission)
			}
			if !strings.Contains(e.Permissions.Advisory.Reason, "accessibleClusters") {
				t.Errorf("%s: the Advisory reason does not name accessibleClusters, which is what does "+
					"the filtering: %q", key, e.Permissions.Advisory.Reason)
			}
		default:
			t.Fatalf("%s: unknown shape %q in the tally", key, want.shape)
		}
	}
	if hoisted != 0 || kept != 13 {
		t.Errorf("the tally moves %d permission call(s) into middleware and keeps %d in handlers, want 0 / 13",
			hoisted, kept)
	}

	for key := range declared {
		if _, listed := reportLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in reportLegacyPermissions — a new report route must be "+
				"added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestReportIDIsAPathParameterOnly is the fail-closed half of spelling this
// domain's identifier :id.
//
// clusterIDFromParam reads TWO names — cluster_id, falling back to id — so
// "id" is a name the permission gate also reads. It resolves to the PATH
// here, which checkPathParams requires, and namesACluster refuses a
// cluster-scoped Check on these paths, so nobody can "simplify" a Deferred
// away into a gate that would authorize a report id as if it were a cluster.
func TestReportIDIsAPathParameterOnly(t *testing.T) {
	for key, e := range declaredReportEndpoints(t) {
		if !slices.Contains(pathParamNames(e.Path), "id") {
			continue
		}
		prop, ok := e.Parameters["id"]
		if !ok {
			t.Errorf("%s has :id with no entry in Parameters", key)
			continue
		}
		if prop.Source != "" && prop.Source != "path" {
			t.Errorf("%s declares id with source %q; the gate reads it from the path", key, prop.Source)
		}
		if prop.Format != "uuid" {
			t.Errorf("%s declares id with format %q, want uuid", key, prop.Format)
		}
		if namesACluster(pathParamNames(e.Path), e.Path) {
			t.Errorf("%s would accept a cluster-scoped Check, but :id is a report id", key)
		}
	}
}

// TestReportClusterIDTakesItsWireNameUnderAnAlias is the assertion behind
// the three declarations in this domain that could not use the name their
// callers send.
//
// checkPathParams refuses a body parameter named "cluster_id" because
// clusterIDFromParam reads that name to decide which cluster the gate
// authorizes. No gate runs on these paths, but the guard is about the name
// rather than about today's path. The parameter is therefore declared as
// report_cluster_id with "cluster_id" as its alias, and BOTH spellings have
// to keep working: the schedule form and the generate dialog both send
// "cluster_id".
func TestReportClusterIDTakesItsWireNameUnderAnAlias(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodPost, reportScope + "/schedules"},
		{fiber.MethodPut, reportScope + "/schedules/:id"},
		{fiber.MethodPost, reportScope + "/generate"},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			if _, wrong := e.Parameters["cluster_id"]; wrong {
				t.Fatal("the body declares a parameter literally named cluster_id; checkPathParams " +
					"refuses that because the permission gate reads the same name, and Register would " +
					"have panicked")
			}
			if got := e.Parameters["report_cluster_id"].Alias; got != "cluster_id" {
				t.Errorf("report_cluster_id declares alias %q, want cluster_id — both dialogs send it "+
					"and would otherwise get \"unknown parameter\"", got)
			}
		})
	}

	// Both spellings reach the handler as report_cluster_id, and sending both
	// at once is a collision Validate reports rather than silently picking a
	// winner.
	const path = reportScope + "/generate"
	for _, spelling := range []string{"cluster_id", "report_cluster_id"} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeReportEndpoint(t, fiber.MethodPost, path, cap))
		body := `{"report_type":"backup_compliance","` + spelling + `":"` + testClusterID + `"}`
		status, env := send(t, app, jsonRequest(http.MethodPost, path, body))
		if status != fiber.StatusNoContent {
			t.Fatalf("spelling %q: status = %d (%q), want 204", spelling, status, env.Message)
		}
		if got := cap.params.String("report_cluster_id"); got != testClusterID {
			t.Errorf("spelling %q reached the handler as %q", spelling, got)
		}
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeReportEndpoint(t, fiber.MethodPost, path, cap))
	body := `{"report_type":"backup_compliance","cluster_id":"` + testClusterID +
		`","report_cluster_id":"` + testClusterID + `"}`
	if status, env := send(t, app, jsonRequest(http.MethodPost, path, body)); status != fiber.StatusBadRequest {
		t.Errorf("both spellings at once: status = %d (%q), want 400", status, env.Message)
	}
}

// probeReportEndpoint is a declared report endpoint with its handler swapped
// for a capture.
func probeReportEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestReportTypeEnumTracksTheCatalogue holds the declared vocabulary and
// reports.AllTypes together. A type added to the catalogue but not offered
// here is unreachable through the API, and one offered here but absent from
// the catalogue passes validation and then fails in the generator.
func TestReportTypeEnumTracksTheCatalogue(t *testing.T) {
	want := make([]string, 0, len(reports.AllTypes))
	for _, rt := range reports.AllTypes {
		want = append(want, string(rt))
	}
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodPost, reportScope + "/schedules"},
		{fiber.MethodPut, reportScope + "/schedules/:id"},
		{fiber.MethodPost, reportScope + "/generate"},
	} {
		got := declaredEndpoint(t, tt.method, tt.path).Parameters["report_type"].Enum
		if !slices.Equal(got, want) {
			t.Errorf("%s %s declares report_type enum %v, want %v", tt.method, tt.path, got, want)
		}
	}

	// And every member has to survive reports.ValidReportType, which is the
	// check the handler still makes on the effective value.
	for _, rt := range want {
		if !reports.ValidReportType(rt) {
			t.Errorf("the enum offers %q but reports.ValidReportType refuses it", rt)
		}
	}
}

// TestScheduleCreateRequiresOnlyWhatTheHandlerDid pins the required SET of
// the create body against what the handler refused before the migration,
// derived from `git show HEAD:internal/api/handlers/reports.go`.
//
// time_range_hours is the one that reads like a tightening and is not:
// validateScheduleRequest ran BEFORE the `== 0 → 168` line and refused
// anything below 1, so omitting it has always been a 400 and that line was
// unreachable on this route.
func TestScheduleCreateRequiresOnlyWhatTheHandlerDid(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, reportScope+"/schedules")
	var got []string
	for name, prop := range e.Parameters {
		if !prop.Optional {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	want := []string{"name", "report_cluster_id", "report_type", "time_range_hours"}
	if !slices.Equal(got, want) {
		t.Errorf("required parameters = %v, want %v", got, want)
	}

	// schedule is OPTIONAL and empty-able, and those are two separate
	// concessions to the same old behaviour.
	//
	// This assertion is here because its earlier version got the answer
	// wrong in a way that is worth not repeating. It listed "schedule" in
	// the required set, which pinned a REGRESSION rather than catching one:
	// the old handler ran `if schedule != ""` before validating, so a body
	// that OMITTED the key inserted a manual-only row and answered 201.
	// Declaring it required turned that into a 400 naming a parameter the
	// caller had never needed to send, and the test then froze it.
	//
	// A required-set test only protects the callers it remembers. The old
	// one remembered the empty-string caller and forgot the absent-key one.
	if prop := e.Parameters["schedule"]; !prop.Optional {
		t.Error("schedule is required; omitting it used to insert a manual-only schedule and answer 201")
	}
	if min := e.Parameters["schedule"].MinLength; min != nil {
		t.Errorf("schedule declares minimum length %d; the empty string is a schedule that never fires", *min)
	}
}

// TestScheduleUpdateKeepsEveryFieldOptional pins the partial-update
// contract: the handler seeds every field from the stored row and overwrites
// only what the caller sent, so a required field would 400 the
// enable/disable toggle, which PUTs `{"enabled": …}` and nothing else. A
// DEFAULT on one would not rewrite anything — every read is an Opt accessor
// or behind Has, which a default cannot fool (apischema.Property.Default) —
// but it would document the toggle as rewriting that column.
func TestScheduleUpdateKeepsEveryFieldOptional(t *testing.T) {
	const path = reportScope + "/schedules/:id"
	e := declaredEndpoint(t, fiber.MethodPut, path)
	var required []string
	for name, prop := range e.Parameters {
		if !prop.Optional {
			required = append(required, name)
		}
		if prop.Default != nil {
			t.Errorf("%s declares default %#v; a partial save leaves an omitted column alone, and a "+
				"default would document it as rewritten", name, prop.Default)
		}
	}
	sort.Strings(required)
	if want := []string{"id"}; !slices.Equal(required, want) {
		t.Errorf("required parameters = %v, want %v — everything but the path id is a partial update",
			required, want)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeReportEndpoint(t, fiber.MethodPut, path, cap))
	status, env := send(t, app, jsonRequest(http.MethodPut, reportRoute(path), `{"enabled":false}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is what the enable/disable toggle sends", status, env.Message)
	}
	if value, supplied := cap.params.OptBool("enabled"); !supplied || value {
		t.Errorf("enabled read back as (%v, supplied=%v), want (false, true)", value, supplied)
	}
	for _, absent := range []string{"name", "schedule", "format"} {
		if _, supplied := cap.params.OptString(absent); supplied {
			t.Errorf("%s reads as supplied on a body that omitted it; the update would overwrite the "+
				"stored column with an empty string", absent)
		}
	}
	if cap.params.Has("parameters") {
		t.Error("parameters reads as supplied on a body that omitted it; the update would store {}")
	}
	if cap.params.Has("email_recipients") {
		t.Error("email_recipients reads as supplied on a body that omitted it; the update would clear the list")
	}
}

// TestScheduleFormatKeepsTheEmptySentinel is the compatibility assertion for
// the one enum in this domain that carries the empty string.
//
// The handler read "" as "html" — it refused a format only when it was
// non-empty and not one of the two — so a caller spelling the default that
// way has always worked, and an enum of exactly {html, csv} would 400 them.
func TestScheduleFormatKeepsTheEmptySentinel(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodPost, reportScope + "/schedules"},
		{fiber.MethodPost, reportScope + "/generate"},
	} {
		if got := declaredEndpoint(t, tt.method, tt.path).Parameters["format"].Enum; !slices.Contains(got, "") {
			t.Errorf("%s %s declares format enum %v, which drops the empty spelling the handler read as html",
				tt.method, tt.path, got)
		}
	}

	// The UPDATE is the exception, and it is the other half of the same
	// fidelity rule: UpdateSchedule never normalised "" to "html" and
	// report_schedules.format carries CHECK (format IN ('html','csv')), so
	// PUT {"format":""} has always written a value the column refuses and
	// come back as a 500. Offering it would publish an accepted value that
	// has never produced a working update.
	if got := declaredEndpoint(t, fiber.MethodPut, reportScope+"/schedules/:id").Parameters["format"].Enum; slices.Contains(got, "") {
		t.Errorf("the update declares format enum %v; the empty spelling reaches the column's CHECK "+
			"constraint and 500s, so it is not an accepted value on this route", got)
	}

	const path = reportScope + "/schedules"
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeReportEndpoint(t, fiber.MethodPost, path, cap))
	body := `{"name":"Weekly backup compliance","report_type":"backup_compliance","cluster_id":"` +
		testClusterID + `","time_range_hours":168,"schedule":"","format":"","email_enabled":false,` +
		`"email_recipients":[],"parameters":{},"enabled":true}`
	status, env := send(t, app, jsonRequest(http.MethodPost, path, body))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is what the schedule form sends", status, env.Message)
	}
	if got := cap.params.String("format"); got != "" {
		t.Errorf("format = %q, want the empty sentinel to survive", got)
	}
}

// TestRecipientsUseTheHandlersOwnEmailRule pins the decision behind the one
// parameter where apischema had a registered format and it was NOT used.
//
// handlers.EmailAddressPattern and the "email" format disagree in both
// directions — the format refuses a dot-irregular local part this accepts,
// accepts an angle-bracketed address and a one-character TLD this refuses,
// and NORMALISES what it validates. The migration keeps the rule that was
// there; this is where a later swap has to be a deliberate edit.
func TestRecipientsUseTheHandlersOwnEmailRule(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		name   string
	}{
		{fiber.MethodPost, reportScope + "/schedules", "email_recipients"},
		{fiber.MethodPut, reportScope + "/schedules/:id", "email_recipients"},
		{fiber.MethodPost, reportScope + "/runs/:id/email", "recipients"},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			prop := declaredEndpoint(t, tt.method, tt.path).Parameters[tt.name]
			if prop.Items == nil {
				t.Fatalf("%s declares no items schema", tt.name)
			}
			if prop.Items.Format != "" {
				t.Errorf("%s's element declares format %q; the email format both accepts and refuses "+
					"addresses this endpoint's own rule does not, and it normalises what it validates",
					tt.name, prop.Items.Format)
			}
			if prop.Items.Pattern != handlers.EmailAddressPattern {
				t.Errorf("%s's element declares pattern %q, want handlers.EmailAddressPattern",
					tt.name, prop.Items.Pattern)
			}
			// MaxLength on an ARRAY counts elements: this is the 50-address cap.
			if prop.MaxLength == nil || *prop.MaxLength != handlers.MaxEmailRecipients {
				t.Errorf("%s declares max length %v, want %d elements", tt.name, prop.MaxLength,
					handlers.MaxEmailRecipients)
			}
		})
	}

	// End to end on the one body where the schema is the ONLY check: the
	// email route's recipient loop moved into the declaration, because that
	// body is the sole source of the list.
	const path = reportScope + "/runs/:id/email"
	for _, tt := range []struct{ name, body, want string }{
		{"a malformed address", `{"channel_id":"` + testClusterID + `","recipients":["not-an-address"]}`, "recipients[0]:"},
		{"too many addresses", `{"channel_id":"` + testClusterID + `","recipients":[` +
			strings.TrimSuffix(strings.Repeat(`"a@example.com",`, handlers.MaxEmailRecipients+1), ",") + `]}`, "recipients:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeReportEndpoint(t, fiber.MethodPost, path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, reportRoute(path), tt.body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, tt.want) {
				t.Errorf("message = %q, want it to start with %q", env.Message, tt.want)
			}
			if cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}
}

// TestGenerateAcceptsWhatTheDialogSends is the compatibility assertion for
// the generate body, including the `format` key that is validated and
// discarded — an undeclared key is a 400 now, so dropping it would have
// broken a caller that has been sending it.
func TestGenerateAcceptsWhatTheDialogSends(t *testing.T) {
	const path = reportScope + "/generate"
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeReportEndpoint(t, fiber.MethodPost, path, cap))
	body := `{"report_type":"cluster_digest","cluster_id":"` + testClusterID +
		`","time_range_hours":168,"parameters":{"top_n":10}}`
	status, env := send(t, app, jsonRequest(http.MethodPost, path, body))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is what the generate dialog sends", status, env.Message)
	}
	if got := cap.params.Object("parameters")["top_n"]; got == nil {
		t.Error("parameters reached the handler without top_n")
	}

	// time_range_hours is optional here, unlike on the schedule create: this
	// handler applied its 168 default BEFORE the range check.
	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeReportEndpoint(t, fiber.MethodPost, path, cap))
	short := `{"report_type":"cluster_digest","cluster_id":"` + testClusterID + `"}`
	status, env = send(t, app, jsonRequest(http.MethodPost, path, short))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — omitting time_range_hours has always worked here", status, env.Message)
	}
	if got := cap.params.Int("time_range_hours"); got != 168 {
		t.Errorf("time_range_hours = %d, want the handler's own 168 default", got)
	}
	if cap.params.Has("time_range_hours") {
		t.Error("time_range_hours reads as supplied when the caller omitted it")
	}
}

// TestReportRunDocumentRoutesAreStillDeclared guards the two routes that do
// NOT answer through the JSON envelope. They serve a document, which is easy
// to overlook when a domain is migrated wholesale — and a route left in
// router.go would fail the legacy ratchet rather than silently work.
func TestReportRunDocumentRoutesAreStillDeclared(t *testing.T) {
	for _, suffix := range []string{"/html", "/csv"} {
		e := declaredEndpoint(t, fiber.MethodGet, reportScope+"/runs/:id"+suffix)
		if e.Permissions.Deferred == "" {
			t.Errorf("%s declares %q, want Deferred", e.Path, e.Permissions.Describe())
		}
		if len(e.Parameters) != 1 {
			t.Errorf("%s declares %d parameters, want exactly the path id — these routes take no "+
				"query parameters and an undeclared one is now a 400", e.Path, len(e.Parameters))
		}
	}
}

// TestEveryReportEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryReportEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredReportEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Reports" {
			t.Errorf("%s is in group %q, want Reports", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
			if prop.Items != nil && strings.TrimSpace(prop.Items.Description) == "" {
				t.Errorf("%s: parameter %q has an element schema with no description", key, name)
			}
		}
	}
}

// TestReportRoutesRefuseAnUndeclaredQueryParameter is the other half of
// closing the parameter set: neither listing takes one, and a caller who
// thinks they do — ?limit=, ?cluster_id= — now finds out rather than being
// silently served the first 100 rows of everything.
func TestReportRoutesRefuseAnUndeclaredQueryParameter(t *testing.T) {
	for _, path := range []string{reportScope + "/schedules", reportScope + "/runs"} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeReportEndpoint(t, fiber.MethodGet, path, cap))
		status, env := send(t, app, httptest.NewRequest(http.MethodGet, path+"?limit=5", nil))
		if status != fiber.StatusBadRequest {
			t.Errorf("%s?limit=5: status = %d (%q), want 400 — the listing is capped at 100 rows and "+
				"has never taken a limit", path, status, env.Message)
		}
		if cap.called {
			t.Errorf("%s: the handler ran for a request carrying a parameter it does not declare", path)
		}
	}
}

// The regression this pins was a 400 on an OMITTED key, and the declaration
// test that was supposed to catch it asserted on the declaration alone. So
// this one goes through the validator instead: a body with no "schedule" at
// all must reach the handler, and must reach it indistinguishable from one
// that sent "".
//
// Both halves matter. Absent had to work because the old value-typed struct
// field made an omitted key a "" the handler then skipped validating; empty
// had to keep working because the schedule form sends it on every save of a
// manual-only schedule.
func TestScheduleCreateAcceptsAnOmittedSchedule(t *testing.T) {
	path := reportScope + "/schedules"
	base := `"name":"nightly","report_type":"cluster_digest","cluster_id":"` + testClusterID +
		`","time_range_hours":168`

	for _, tt := range []struct {
		name string
		body string
	}{
		{"omitted entirely", "{" + base + "}"},
		{"sent as the empty string", "{" + base + `,"schedule":""}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeReportEndpoint(t, fiber.MethodPost, path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, path, tt.body))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204 — this inserted a manual-only schedule and answered 201 "+
					"before the migration", status, env.Message)
			}
			if got := cap.params.String("schedule"); got != "" {
				t.Errorf("schedule = %q, want \"\" — the handler skips cron validation and next_run_at on it", got)
			}
		})
	}
}
