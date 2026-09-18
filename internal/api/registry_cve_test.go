package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to registry_cve.go
// that quietly loosened a parameter would show up here.

// cveRouteCount is how many endpoints registerCVEEndpoints declares. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum
// of per-domain constants rather than one number.
const cveRouteCount = 10

const (
	testScanID = "8a1d2ab3-3f0e-4b4c-9f8a-000000000004"
	testNodeID = "8a1d2ab3-3f0e-4b4c-9f8a-000000000005"
)

// cveRoute renders one CVE route's path with the test ids substituted.
func cveRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":scan_id", testScanID,
	).Replace(path)
}

// cvePathPrefixes are the four places this domain's routes live. Unlike
// every other migrated domain it has no single scope — the groups hang off
// the cluster directly — so "is this a CVE route" has to be asked against
// a list rather than a prefix.
var cvePathPrefixes = []string{
	cveScanScope, cvePostureScope, cveScheduleScope, cveNotificationScope,
}

// cveLegacyPermissions is the permission each CVE handler checked with a
// hand-placed requireClusterPerm call BEFORE Phase 6b, transcribed from
// internal/api/handlers/cve.go at commit 13ceac0 (10 handlers, 10 calls,
// every one of them cluster-scoped on the "cve_scan" resource).
//
// cve_scan is the resource for all ten, the schedule and notification
// routes included: there is no separate cve_schedule or cve_notification
// permission, so an operator who can manage scans can also change when
// they run and who hears about them.
var cveLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/cve-scans":                          "view:cve_scan",
	"POST /api/v1/clusters/:cluster_id/cve-scans":                         "manage:cve_scan",
	"GET /api/v1/clusters/:cluster_id/cve-scans/:scan_id":                 "view:cve_scan",
	"GET /api/v1/clusters/:cluster_id/cve-scans/:scan_id/vulnerabilities": "view:cve_scan",
	"DELETE /api/v1/clusters/:cluster_id/cve-scans/:scan_id":              "manage:cve_scan",
	"GET /api/v1/clusters/:cluster_id/security-posture":                   "view:cve_scan",
	"GET /api/v1/clusters/:cluster_id/cve-scan-schedule":                  "view:cve_scan",
	"PUT /api/v1/clusters/:cluster_id/cve-scan-schedule":                  "manage:cve_scan",
	"GET /api/v1/clusters/:cluster_id/cve-notifications":                  "view:cve_scan",
	"PUT /api/v1/clusters/:cluster_id/cve-notifications":                  "manage:cve_scan",
}

// declaredCVEEndpoints returns every declaration under one of the four CVE
// path prefixes, keyed "METHOD path".
func declaredCVEEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		for _, prefix := range cvePathPrefixes {
			if strings.HasPrefix(e.Path, prefix) {
				out[e.Method+" "+e.Path] = e
				break
			}
		}
	}
	return out
}

// TestCVERoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes deleting 10 requireClusterPerm calls a refactor rather than a
// change.
func TestCVERoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredCVEEndpoints(t)
	if len(declared) != cveRouteCount {
		t.Fatalf("the registry declares %d CVE routes, want %d", len(declared), cveRouteCount)
	}
	if len(cveLegacyPermissions) != cveRouteCount {
		t.Fatalf("cveLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(cveLegacyPermissions), cveRouteCount)
	}

	var view, manage int
	for key, want := range cveLegacyPermissions {
		switch want {
		case "view:cve_scan":
			view++
		case "manage:cve_scan":
			manage++
		}
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; every CVE route's permission is statically known",
				key, e.Permissions.Describe())
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path, so the "+
				"declaration has to as well", key, e.Permissions.Check.Scope)
		}
	}
	if view != 6 || manage != 4 {
		t.Errorf("the tally splits %d view:cve_scan / %d manage:cve_scan, want 6 / 4", view, manage)
	}

	for key := range declared {
		if _, listed := cveLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in cveLegacyPermissions — a new CVE route must be added "+
				"to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestCVERoutesDeclareEveryPathParameter re-states checkPathParams' rule
// for this domain and adds the one it does NOT enforce: that :cluster_id
// is the FIRST placeholder.
func TestCVERoutesDeclareEveryPathParameter(t *testing.T) {
	for key, e := range declaredCVEEndpoints(t) {
		names := pathParamNames(e.Path)
		if len(names) == 0 || names[0] != "cluster_id" {
			t.Errorf("%s has path parameters %v; :cluster_id must be the first, or the permission "+
				"middleware cannot resolve the cluster the route acts on", key, names)
		}
		for _, name := range names {
			prop, ok := e.Parameters[name]
			if !ok {
				t.Errorf("%s has :%s with no entry in Parameters", key, name)
				continue
			}
			if prop.Optional {
				t.Errorf("%s declares the path parameter %q optional; a URL segment is always present",
					key, name)
			}
		}
		if !strings.Contains(e.Path, "/:scan_id") {
			continue
		}
		// It has to be the scan uuid, not a loosely-typed copy:
		// checkPathParams requires an entry for :scan_id, not that the entry
		// validates a uuid, and a bare string there would hand uuid.Parse
		// whatever the URL carried — which parseParamUUID reports as a 500,
		// not the 400 the caller deserves.
		if got := e.Parameters["scan_id"].Format; got != "uuid" {
			t.Errorf("%s declares scan_id with format %q, want uuid", key, got)
		}
	}
}

// probeCVEEndpoint is a declared CVE endpoint with its handler swapped for
// a capture.
func probeCVEEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestCVEScanListPagingIsBounded is a deliberate behaviour change, spelled
// out because it is one: the handler used to CLAMP an out-of-range limit
// or offset back to its default and answer 200, so ?limit=5000 quietly
// returned 20 rows and ?offset=-1 quietly returned the first page. The
// declaration refuses both instead.
func TestCVEScanListPagingIsBounded(t *testing.T) {
	const path = cveScanScope
	e := declaredEndpoint(t, fiber.MethodGet, path)
	if e.Parameters["limit"].Default != 20 {
		t.Errorf("limit default = %#v, want 20 — the value the handler substituted", e.Parameters["limit"].Default)
	}
	if e.Parameters["offset"].Default != 0 {
		t.Errorf("offset default = %#v, want 0", e.Parameters["offset"].Default)
	}

	target := cveRoute(path)
	for _, tt := range []struct {
		query  string
		want   int
		limit  int64
		offset int64
	}{
		{query: "", want: fiber.StatusNoContent, limit: 20, offset: 0},
		// What the security page actually sends.
		{query: "?limit=20", want: fiber.StatusNoContent, limit: 20, offset: 0},
		{query: "?limit=100&offset=100", want: fiber.StatusNoContent, limit: 100, offset: 100},
		{query: "?limit=101", want: fiber.StatusBadRequest},
		{query: "?limit=0", want: fiber.StatusBadRequest},
		{query: "?offset=-1", want: fiber.StatusBadRequest},
		{query: "?limit=lots", want: fiber.StatusBadRequest},
		{query: "?page=2", want: fiber.StatusBadRequest},
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCVEEndpoint(t, fiber.MethodGet, path, cap))
		status, env := send(t, app, httptest.NewRequest(http.MethodGet, target+tt.query, nil))
		if status != tt.want {
			t.Errorf("%q: status = %d (%q), want %d", tt.query, status, env.Message, tt.want)
			continue
		}
		if tt.want != fiber.StatusNoContent {
			continue
		}
		if got := cap.params.Int("limit"); got != tt.limit {
			t.Errorf("%q: limit = %d, want %d", tt.query, got, tt.limit)
		}
		if got := cap.params.Int("offset"); got != tt.offset {
			t.Errorf("%q: offset = %d, want %d", tt.query, got, tt.offset)
		}
	}
}

// TestVulnerabilityFiltersAreDeclared covers the three query parameters
// the vulnerability listing branches on, and the distinction the handler
// now depends on: it reads them through OptString, so "the caller chose a
// filter" is a supplied-ness question rather than a non-emptiness one.
func TestVulnerabilityFiltersAreDeclared(t *testing.T) {
	const path = cveScanScope + "/:scan_id/vulnerabilities"
	e := declaredEndpoint(t, fiber.MethodGet, path)

	severity := e.Parameters["severity"]
	if !slices.Equal(severity.Enum, handlers.CVESeverities) {
		t.Errorf("severity enum = %v, want handlers.CVESeverities %v", severity.Enum, handlers.CVESeverities)
	}
	if len(severity.Enum) > 0 && &severity.Enum[0] == &handlers.CVESeverities[0] {
		t.Error("the declared severity enum aliases handlers.CVESeverities; clone it")
	}
	if got := e.Parameters["node_id"].Format; got != "uuid" {
		t.Errorf("node_id declares format %q, want uuid — the handler's own uuid.Parse is gone", got)
	}
	if e.Parameters["kev"].Type != "boolean" {
		t.Errorf("kev is declared as %q, want boolean", e.Parameters["kev"].Type)
	}

	target := cveRoute(path)
	for _, tt := range []struct {
		query string
		want  int
		// filters the handler should see: which of the three were supplied.
		severity, node bool
		kev            bool
	}{
		{query: "", want: fiber.StatusNoContent},
		{query: "?severity=critical", want: fiber.StatusNoContent, severity: true},
		{query: "?node_id=" + testNodeID, want: fiber.StatusNoContent, node: true},
		{query: "?kev=true", want: fiber.StatusNoContent, kev: true},
		// The dashboard callout sends kev alone; the table sends kev
		// alongside a severity and the handler applies kev on its own.
		{query: "?severity=high&kev=true", want: fiber.StatusNoContent, severity: true, kev: true},
		{query: "?severity=catastrophic", want: fiber.StatusBadRequest},
		{query: "?severity=", want: fiber.StatusBadRequest},
		{query: "?node_id=not-a-uuid", want: fiber.StatusBadRequest},
		{query: "?kev=maybe", want: fiber.StatusBadRequest},
		{query: "?nodeid=" + testNodeID, want: fiber.StatusBadRequest},
	} {
		t.Run("q"+tt.query, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeCVEEndpoint(t, fiber.MethodGet, path, cap))
			status, env := send(t, app, httptest.NewRequest(http.MethodGet, target+tt.query, nil))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
			if tt.want != fiber.StatusNoContent {
				if cap.called {
					t.Error("the handler ran for a request the schema rejected")
				}
				return
			}
			if _, supplied := cap.params.OptString("severity"); supplied != tt.severity {
				t.Errorf("severity supplied = %v, want %v", supplied, tt.severity)
			}
			if _, supplied := cap.params.OptString("node_id"); supplied != tt.node {
				t.Errorf("node_id supplied = %v, want %v", supplied, tt.node)
			}
			if got := cap.params.Bool("kev"); got != tt.kev {
				t.Errorf("kev = %v, want %v", got, tt.kev)
			}
		})
	}
}

// TestCVEScheduleAndNotificationsStayPartial is the compatibility
// assertion this domain turns on. Both PUTs MERGE with what is stored, so
// an omitted parameter has to reach the handler as absent rather than as a
// default — a Default on any of them would rewrite the operator's stored
// value on every partial save.
//
// The one that would hurt is `enabled`: defaulting it to false would
// silently switch automatic scanning off for any client that sent only the
// interval.
func TestCVEScheduleAndNotificationsStayPartial(t *testing.T) {
	for _, tt := range []struct {
		name   string
		method string
		path   string
		params []string
	}{
		{"schedule", fiber.MethodPut, cveScheduleScope, []string{"enabled", "interval_hours"}},
		{
			"notifications", fiber.MethodPut, cveNotificationScope,
			[]string{"enabled", "notify_on_act", "notify_on_attend", "channel_ids", "cooldown_minutes"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			for _, name := range tt.params {
				prop, ok := e.Parameters[name]
				if !ok {
					t.Errorf("%s does not declare %q", tt.path, name)
					continue
				}
				if !prop.Optional {
					t.Errorf("%q is required; both of these endpoints merge with the stored row", name)
				}
				if prop.Default != nil {
					t.Errorf("%q declares default %#v; a default would overwrite the stored value on "+
						"every partial save", name, prop.Default)
				}
			}

			// An empty body is a real request here — it means "change
			// nothing" — and none of the parameters may read as supplied.
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeCVEEndpoint(t, tt.method, tt.path, cap))
			if status, env := send(t, app, jsonRequest(tt.method, cveRoute(tt.path), `{}`)); status != fiber.StatusNoContent {
				t.Fatalf("empty body: status = %d (%q), want 204", status, env.Message)
			}
			for _, name := range tt.params {
				if cap.params.Has(name) {
					t.Errorf("%q reads as supplied on an empty body", name)
				}
			}
		})
	}
}

// TestCVENotificationChannelsKeepTheEmptyList is this domain's sentinel.
//
// The card sends channel_ids:[] when notifications are switched off, and
// the handler distinguishes that from an absent key: [] CLEARS the stored
// channels, absent KEEPS them. Params.Strings alone cannot tell the two
// apart on every path, which is why the handler asks p.Has — and this is
// what pins that the schema really does keep them apart.
func TestCVENotificationChannelsKeepTheEmptyList(t *testing.T) {
	const path = cveNotificationScope
	target := cveRoute(path)

	t.Run("an explicit empty list reads as supplied", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCVEEndpoint(t, fiber.MethodPut, path, cap))
		body := `{"enabled":false,"notify_on_act":true,"notify_on_attend":false,` +
			`"channel_ids":[],"cooldown_minutes":60}`
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204 — the card sends this when notifications are off", status, env.Message)
		}
		if !cap.params.Has("channel_ids") {
			t.Error("channel_ids reads as absent; the handler would keep the stored channels instead of clearing them")
		}
		if got := cap.params.Strings("channel_ids"); len(got) != 0 {
			t.Errorf("channel_ids = %v, want an empty list", got)
		}
	})

	t.Run("a populated list survives", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCVEEndpoint(t, fiber.MethodPut, path, cap))
		body := `{"enabled":true,"notify_on_act":true,"channel_ids":["` + testNodeID + `"],"cooldown_minutes":0}`
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got := cap.params.Strings("channel_ids"); !slices.Equal(got, []string{testNodeID}) {
			t.Errorf("channel_ids = %v, want the one id", got)
		}
	})

	for _, tt := range []struct{ body, field string }{
		{`{"channel_ids":["not-a-uuid"]}`, "channel_ids[0]:"},
		{`{"channel_ids":"` + testNodeID + `"}`, "channel_ids[0]:"},
		{`{"cooldown_minutes":-1}`, "cooldown_minutes:"},
		{`{"cooldown_minutes":10081}`, "cooldown_minutes:"},
		{`{"notify_on_acts":true}`, "notify_on_acts:"},
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCVEEndpoint(t, fiber.MethodPut, path, cap))
		status, env := send(t, app, jsonRequest(http.MethodPut, target, tt.body))
		if tt.field == "channel_ids[0]:" && strings.HasPrefix(tt.body, `{"channel_ids":"`) {
			// A lone scalar stands for a one-element array (see toSlice in
			// apischema), so this one is ACCEPTED and the element is the
			// uuid — worth pinning rather than assuming it 400s.
			if status != fiber.StatusNoContent {
				t.Errorf("body %s: status = %d (%q), want 204 — a lone scalar is a one-element array",
					tt.body, status, env.Message)
			}
			continue
		}
		if status != fiber.StatusBadRequest {
			t.Errorf("body %s: status = %d (%q), want 400", tt.body, status, env.Message)
			continue
		}
		if !strings.HasPrefix(env.Message, tt.field) {
			t.Errorf("body %s: message = %q, want it to start with %q", tt.body, env.Message, tt.field)
		}
	}
}

// TestCVEScheduleIntervalIsBounded pins the bounds the handler used to
// check after merging, and that the merged check is still reachable — the
// schema bounds what the CALLER may send, and a row stored before those
// bounds existed can still be out of range.
func TestCVEScheduleIntervalIsBounded(t *testing.T) {
	const path = cveScheduleScope
	target := cveRoute(path)
	for _, tt := range []struct {
		body string
		want int
	}{
		{`{"enabled":true,"interval_hours":24}`, fiber.StatusNoContent},
		{`{"enabled":false,"interval_hours":1}`, fiber.StatusNoContent},
		{`{"interval_hours":168}`, fiber.StatusNoContent},
		{`{"interval_hours":0}`, fiber.StatusBadRequest},
		{`{"interval_hours":169}`, fiber.StatusBadRequest},
		{`{"interval_hours":"24"}`, fiber.StatusNoContent}, // coerced, per apischema's forgiving reads
		{`{"enabled":"yes"}`, fiber.StatusNoContent},       // likewise
		{`{"enabled":"perhaps"}`, fiber.StatusBadRequest},
		{`{"intervalHours":24}`, fiber.StatusBadRequest},
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCVEEndpoint(t, fiber.MethodPut, path, cap))
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, tt.body)); status != tt.want {
			t.Errorf("body %s: status = %d (%q), want %d", tt.body, status, env.Message, tt.want)
		}
	}
}

// TestCVETriggerScanTakesNoBody covers the trigger button, which calls
// apiClient.post(url) with one argument and so sends body:null while still
// setting Content-Type: application/json.
func TestCVETriggerScanTakesNoBody(t *testing.T) {
	if got := len(declaredEndpoint(t, fiber.MethodPost, cveScanScope).Parameters); got != 1 {
		t.Fatalf("the scan trigger declares %d parameters, want just :cluster_id", got)
	}
	for _, body := range []string{``, `{}`} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCVEEndpoint(t, fiber.MethodPost, cveScanScope, cap))
		if status, env := send(t, app, jsonRequest(http.MethodPost, cveRoute(cveScanScope), body)); status != fiber.StatusNoContent {
			t.Errorf("body %q: status = %d (%q), want 204", body, status, env.Message)
		}
	}
}

// TestCVERouteIsGatedByItsDeclaration proves the permission the
// declaration states is the permission the route enforces, end to end.
//
// It is run against the scan delete, which drops a scan and every
// vulnerability row it produced — the most consequential of the ten, and
// the one where a dropped gate would let any viewer erase the evidence the
// posture score is computed from.
func TestCVERouteIsGatedByItsDeclaration(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, cveScanScope+"/:scan_id")
	if e.Permissions.Describe() != "manage:cve_scan" {
		t.Fatalf("the scan delete declares %q, want manage:cve_scan", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := cveRoute(cveScanScope + "/:scan_id")

	t.Run("a caller holding only view:cve_scan is refused", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:cve_scan": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodDelete, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding manage:cve_scan gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:cve_scan": true}), gated)
		status, env := send(t, app, authedRequest(http.MethodDelete, target))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if !cap.called {
			t.Error("the handler did not run for a caller holding the declared permission")
		}
	})

	t.Run("an anonymous caller is refused before the gate", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:cve_scan": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestEveryCVEEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryCVEEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredCVEEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Security" {
			t.Errorf("%s is in group %q, want Security", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
