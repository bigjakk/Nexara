package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to registry_alerts.go
// that quietly loosened a parameter would show up here.

// alertRouteCount is how many endpoints registerAlertEndpoints declares. It is
// 20 of AlertHandler's 22; the two writes that stay legacy are pinned by
// TestAlertRuleWritesAreStillLegacy rather than left to be noticed as a gap.
// See vmRouteCount in registry_vms_test.go for why the registry total is a sum
// of per-domain constants.
const alertRouteCount = 20

const (
	// One id for every :id in this domain: the routes that carry one take an
	// alert, a rule, a channel or a window, and no test here needs to tell them
	// apart — every handler is a capture.
	testAlertID          = "2c8b4d51-6a39-4f77-9e04-00000000000b"
	testChannelID        = "5b9d0c37-4e28-4a91-8f73-00000000000d"
	testAlertFilterClust = testClusterID
)

func alertRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":id", testAlertID,
	).Replace(path)
}

// alertRoutesOutsideTheClusterCheckShape is this domain's half of the
// registry-wide exception list in registry_vms_test.go.
//
// FOURTEEN of the 20 are here. An alert and an alert rule each carry the
// cluster they belong to as a NULLABLE column rather than as a path segment —
// and a NULL is meaningful, not missing data: it is a GLOBAL rule, one watching
// infrastructure no single cluster owns (a Veeam repository holds every
// cluster's backups). So the two listings filter and the five per-row routes
// branch, and neither shape can be a gate. The seven channel and summary routes
// are global outright.
//
// The six that are NOT here are the cluster-scoped alert and maintenance-window
// routes, whose cluster IS the first parameter of their own path.
var alertRoutesOutsideTheClusterCheckShape = func() map[string]string {
	const global = "global: a notification channel belongs to the install rather than to a cluster, and " +
		"the summary counts every cluster's alerts at once; neither path names one"
	const filtered = "Advisory: accessibleClusters builds the SQL scope the listing pages under and a " +
		"per-row check re-applies it; the listing spans every cluster, so there is none to gate on"
	const deferred = "Deferred: the cluster is a column on the row — the handler loads the alert or rule " +
		"by id and gates on its stored cluster_id, or instance-wide when that column is NULL"

	out := map[string]string{}
	for _, r := range []string{
		"GET /api/v1/alerts/summary",
		"GET /api/v1/notification-channels",
		"POST /api/v1/notification-channels",
		"GET /api/v1/notification-channels/:id",
		"PUT /api/v1/notification-channels/:id",
		"DELETE /api/v1/notification-channels/:id",
		"POST /api/v1/notification-channels/:id/test",
	} {
		out[r] = global
	}
	for _, r := range []string{
		"GET /api/v1/alerts",
		"GET /api/v1/alert-rules",
	} {
		out[r] = filtered
	}
	for _, r := range []string{
		"GET /api/v1/alerts/:id",
		"POST /api/v1/alerts/:id/acknowledge",
		"POST /api/v1/alerts/:id/resolve",
		"GET /api/v1/alert-rules/:id",
		"DELETE /api/v1/alert-rules/:id",
	} {
		out[r] = deferred
	}
	return out
}()

// alertLegacyPermissions is what each handler checked with hand-placed calls
// BEFORE Phase 6g, transcribed from `git show HEAD:internal/api/handlers/alerts.go`
// at commit facdf56.
//
// calls counts the permission calls each handler made, per ROUTE rather than
// per source line. The five Deferred routes are 2 apiece because each has a
// per-branch pair — requireClusterPerm on the row's stored cluster OR
// requirePerm instance-wide when that column is NULL. The two maintenance-window
// writes are also 2, for a different reason: the route's own
// requireClusterPerm hoists, and resolveNodeCluster's check against the cluster
// that OWNS a node named in the body cannot, because middleware resolves the
// path's cluster and nothing else.
//
// It covers only the TWENTY migrated routes. POST /api/v1/alert-rules and
// PUT /api/v1/alert-rules/:id stay legacy and keep their calls; a tally entry
// for a route that is still legacy would read as a migration that did not
// happen.
var alertLegacyPermissions = map[string]struct {
	permission string
	shape      string
	calls      int
	hoisted    int
}{
	"GET /api/v1/alerts":         {"view:alert", "Advisory", 1, 0},
	"GET /api/v1/alerts/summary": {"view:alert", "Check", 1, 1},
	"GET /api/v1/alerts/:id":     {"view:alert", "Deferred", 2, 0},
	// acknowledge:alert on BOTH, not manage: closing an alert is the same act
	// as acknowledging it, not a change to the rule that raised it.
	"POST /api/v1/alerts/:id/acknowledge": {"acknowledge:alert", "Deferred", 2, 0},
	"POST /api/v1/alerts/:id/resolve":     {"acknowledge:alert", "Deferred", 2, 0},

	"GET /api/v1/alert-rules":        {"view:alert", "Advisory", 1, 0},
	"GET /api/v1/alert-rules/:id":    {"view:alert", "Deferred", 2, 0},
	"DELETE /api/v1/alert-rules/:id": {"manage:alert", "Deferred", 2, 0},

	"GET /api/v1/notification-channels":           {"view:notification_channel", "Check", 1, 1},
	"POST /api/v1/notification-channels":          {"manage:notification_channel", "Check", 1, 1},
	"GET /api/v1/notification-channels/:id":       {"view:notification_channel", "Check", 1, 1},
	"PUT /api/v1/notification-channels/:id":       {"manage:notification_channel", "Check", 1, 1},
	"DELETE /api/v1/notification-channels/:id":    {"manage:notification_channel", "Check", 1, 1},
	"POST /api/v1/notification-channels/:id/test": {"manage:notification_channel", "Check", 1, 1},

	"GET /api/v1/clusters/:cluster_id/alerts":                     {"view:alert", "Check", 1, 1},
	"GET /api/v1/clusters/:cluster_id/alerts/count":               {"view:alert", "Check", 1, 1},
	"GET /api/v1/clusters/:cluster_id/maintenance-windows":        {"view:maintenance_window", "Check", 1, 1},
	"POST /api/v1/clusters/:cluster_id/maintenance-windows":       {"manage:maintenance_window", "Check", 2, 1},
	"PUT /api/v1/clusters/:cluster_id/maintenance-windows/:id":    {"manage:maintenance_window", "Check", 2, 1},
	"DELETE /api/v1/clusters/:cluster_id/maintenance-windows/:id": {"manage:maintenance_window", "Check", 1, 1},
}

// declaredAlertEndpoints returns every declaration in this domain, keyed
// "METHOD path".
//
// It filters on alertLegacyPermissions' own keys rather than on a path prefix,
// because the domain spans four unrelated prefixes and two of them —
// /clusters/:cluster_id/alerts and /alert-rules — would sweep in either every
// other cluster route or the two writes that are still legacy.
func declaredAlertEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		key := e.Method + " " + e.Path
		if _, listed := alertLegacyPermissions[key]; listed {
			out[key] = e
		}
	}
	return out
}

// TestAlertRoutesDeclareTheSamePermissionTheyEnforced is the tally that makes
// this migration a refactor rather than a change.
//
// Thirteen of the 27 hand-placed calls move into middleware and 14 stay. The
// tally says so out loud rather than leaving a reader to infer it from fourteen
// reason strings.
func TestAlertRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredAlertEndpoints(t)
	if len(declared) != alertRouteCount {
		t.Fatalf("the registry declares %d alert routes, want %d", len(declared), alertRouteCount)
	}
	if len(alertLegacyPermissions) != alertRouteCount {
		t.Fatalf("alertLegacyPermissions has %d entries, want %d — the table must cover every migrated route",
			len(alertLegacyPermissions), alertRouteCount)
	}

	var hoisted, kept int
	for key, want := range alertLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want.permission)
			continue
		}
		hoisted += want.hoisted
		kept += want.calls - want.hoisted

		switch want.shape {
		case "Check":
			if e.Permissions.Check == nil {
				t.Errorf("%s declares %q rather than a Check", key, e.Permissions.Describe())
				continue
			}
			if got := e.Permissions.Describe(); got != want.permission {
				t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want.permission)
			}
			// The scope differs WITHIN this shape, and getting it backwards is
			// the one mistake here that would change who can do what: a
			// cluster-scoped route declared global would let a caller holding
			// the grant instance-wide act on a cluster they hold nothing on,
			// and a global route declared cluster-scoped is not even
			// expressible — namesACluster refuses it.
			wantScope := ScopeGlobal
			if strings.HasPrefix(e.Path, clusterScope) {
				wantScope = ScopeCluster
			}
			if e.Permissions.Check.Scope != wantScope {
				t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, wantScope)
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
		case "Deferred":
			if e.Permissions.Deferred == "" {
				t.Errorf("%s declares %q, want Deferred: the cluster is a column on the row",
					key, e.Permissions.Describe())
				continue
			}
			// A Deferred route renders as the bare word "deferred", so the
			// permission an operator needs has to survive in the prose.
			if !strings.Contains(e.Description, want.permission) {
				t.Errorf("%s is Deferred but its Description never names %q, so the docs tell an operator "+
					"building a role nothing: %q", key, want.permission, e.Description)
			}
			// And the reason has to name both branches, not merely say that the
			// handler looks at something. The NULL branch is the one a reader
			// would otherwise miss, and it is what a global rule needs.
			for _, phrase := range []string{"stored cluster_id", "NULL"} {
				if !strings.Contains(e.Permissions.Deferred, phrase) {
					t.Errorf("%s: the Deferred reason does not name %q, which is half of what an operator "+
						"would otherwise have to guess: %q", key, phrase, e.Permissions.Deferred)
				}
			}
		default:
			t.Fatalf("%s: unknown shape %q in the tally", key, want.shape)
		}
	}
	if hoisted != 13 || kept != 14 {
		t.Errorf("the tally moves %d permission call(s) into middleware and keeps %d in handlers, want 13 / 14",
			hoisted, kept)
	}

	for key := range declared {
		if _, listed := alertLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in alertLegacyPermissions — a new alert route must be "+
				"added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestAlertRuleWritesAreStillLegacy pins the two routes this migration
// deliberately left behind, so that "20 of 22" is an assertion rather than a
// thing a reader has to notice.
//
// Their body carries `escalation_chain`, a JSON ARRAY OF OBJECTS, and
// apischema's Property.Items is restricted to scalar element types —
// compileItems refuses an Object element outright. Three halves are asserted:
// the routes are NOT in the registry, they ARE still mounted (a route that
// vanished would be an outage rather than a deferral), and the registry would
// still refuse the declaration they need. The third is what keeps this from
// rotting into a note nobody re-reads: the day apischema grows object items,
// this test fails and the carve-out gets revisited. It is the same shape
// TestFirewallTemplateWritesAreStillLegacy holds for the same reason.
func TestAlertRuleWritesAreStillLegacy(t *testing.T) {
	s := newRouteStubServer(t)

	legacy := []struct {
		method string
		path   string
	}{
		{fiber.MethodPost, alertRuleScope},
		{fiber.MethodPut, alertRuleScope + "/:id"},
	}

	for _, want := range legacy {
		for _, e := range s.registry.Endpoints() {
			if e.Method == want.method && e.Path == want.path {
				t.Errorf("%s %s is declared, but its body carries an array of objects that a "+
					"parameter schema cannot describe", want.method, want.path)
			}
		}
		var mounted bool
		for _, r := range s.app.GetRoutes(true) {
			if r.Method == want.method && normalizeRoutePath(r.Path) == want.path {
				mounted = true
			}
		}
		if !mounted {
			t.Errorf("%s %s is neither declared nor mounted; it has been lost, not deferred",
				want.method, want.path)
		}
	}

	// The reason, asserted rather than asserted-about: an escalation chain is
	// an array of objects, and that is refused at registration.
	err := NewRegistry().register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        alertRuleScope,
		Description: "synthetic probe for the object-items refusal",
		Group:       "Alerts",
		Permissions: globalCheck("manage", "alert"),
		Parameters: apischema.Properties{
			"escalation_chain": {
				Type:     apischema.Array,
				Optional: true,
				Items:    &apischema.Property{Type: apischema.Object},
			},
		},
		Handler: noopParamsHandler,
	})
	if err == nil {
		t.Fatal("Register accepted an array of objects; the reason these two routes stay legacy no longer holds")
	}
	if !strings.Contains(err.Error(), "scalar element types") {
		t.Errorf("Register refused the object-items schema for the wrong reason: %v", err)
	}
}

// TestAlertRoutesAreGatedByTheirDeclaration proves the thirteen hoisted
// permissions are the permissions the routes enforce, end to end — both
// scopes, since this domain has global and cluster-scoped Checks side by side.
func TestAlertRoutesAreGatedByTheirDeclaration(t *testing.T) {
	for key, want := range alertLegacyPermissions {
		if want.shape != "Check" {
			continue
		}
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			if got := e.Permissions.Describe(); got != want.permission {
				t.Fatalf("declares %q, want %q", got, want.permission)
			}
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := alertRoute(path)

			app := newRegistryApp(t, stubAuth(map[string]bool{"view:pbs": true}), gated)
			status, _ := send(t, app, authedRequest(method, target))
			if status != fiber.StatusForbidden {
				t.Fatalf("a caller holding an unrelated grant got %d, want 403", status)
			}
			if cap.called {
				t.Error("the handler ran for a caller without the declared permission")
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want.permission: true}), gated)
			status, _ = send(t, app, authedRequest(method, target))
			if status == fiber.StatusForbidden {
				t.Fatalf("a caller holding %q got 403", want.permission)
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want.permission: true}), gated)
			status, _ = send(t, app, httptest.NewRequest(method, target, nil))
			if status != fiber.StatusUnauthorized {
				t.Fatalf("an anonymous caller got %d, want 401", status)
			}
			if cap.called {
				t.Error("the handler ran for a request carrying no session")
			}
		})
	}
}

// TestAlertSummaryIsRegisteredBeforeTheAlertIDRoute is the ordering assertion
// the legacy block got right by accident of source order and the registry has
// to state.
//
// Fiber matches in registration order, so declaring GET /alerts/:id first would
// make GET /alerts/summary unreachable — it would resolve as an alert whose id
// is the literal "summary", which the uuid format then refuses with a 400. The
// summary card on the dashboard polls this route every 30 seconds.
func TestAlertSummaryIsRegisteredBeforeTheAlertIDRoute(t *testing.T) {
	s := newRouteStubServer(t)
	summary, byID := -1, -1
	for i, e := range s.registry.Endpoints() {
		if e.Method != fiber.MethodGet {
			continue
		}
		switch e.Path {
		case alertScope + "/summary":
			summary = i
		case alertScope + "/:id":
			byID = i
		}
	}
	if summary < 0 || byID < 0 {
		t.Fatalf("expected both routes to be declared; summary=%d byID=%d", summary, byID)
	}
	if summary > byID {
		t.Errorf("GET %s/summary is declared after GET %s/:id, so it is unreachable — Fiber matches in "+
			"registration order and the :id route would swallow it", alertScope, alertScope)
	}

	// And end to end: the literal path reaches its own handler rather than the
	// :id one.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeAlertEndpoint(t, fiber.MethodGet, alertScope+"/summary", cap),
		probeAlertEndpoint(t, fiber.MethodGet, alertScope+"/:id", &capture{}))
	status, env := send(t, app, httptest.NewRequest(http.MethodGet, pathPrefix+"alerts/summary", nil))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if !cap.called {
		t.Error("the summary request reached a different handler")
	}
}

// probeAlertEndpoint is a declared alert endpoint with its handler swapped for
// a capture and its gate removed, so a parameter test needs neither a database
// nor a session.
func probeAlertEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestAlertListingsKeepTheirEmptyFilterSentinels is the compatibility
// assertion for the three query filters whose EMPTY value means "do not
// filter".
//
// All three reached SQL as "" and were read there as "match everything": the
// handlers validated state and severity only when non-empty, and parsed
// cluster_id only when non-empty. An enum without the empty member, or the uuid
// format in place of the pattern, would 400 a request that has always worked.
func TestAlertListingsKeepTheirEmptyFilterSentinels(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, alertScope)
	for _, name := range []string{"state", "severity"} {
		if !slices.Contains(e.Parameters[name].Enum, "") {
			t.Errorf("%s declares enum %v with no empty member; \"\" has always meant \"do not filter\"",
				name, e.Parameters[name].Enum)
		}
	}
	for _, path := range []string{alertScope, alertRuleScope} {
		listing := declaredEndpoint(t, fiber.MethodGet, path)
		prop, ok := listing.Parameters["filter_cluster_id"]
		if !ok {
			t.Fatalf("GET %s declares no filter_cluster_id", path)
		}
		if prop.Format != "" {
			t.Errorf("GET %s: filter_cluster_id declares format %q; every registered format rejects the "+
				"empty string, which is the no-filter sentinel", path, prop.Format)
		}
		if prop.Pattern != emptyOrUUID {
			t.Errorf("GET %s: filter_cluster_id declares pattern %q, want the empty-or-uuid one", path, prop.Pattern)
		}
	}

	// End to end: the three empty spellings reach the handler as the sentinel
	// rather than as a 400.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeAlertEndpoint(t, fiber.MethodGet, alertScope, cap))
	target := pathPrefix + "alerts?state=&severity=&cluster_id="
	status, env := send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — an explicitly empty filter has always meant \"all\"", status, env.Message)
	}
	if got := cap.params.String("filter_cluster_id"); got != "" {
		t.Errorf("filter_cluster_id = %q, want the empty sentinel", got)
	}
}

// TestAlertClusterFilterAliasBindsTheQueryKeyCallersSend is the other half of
// the alias decision.
//
// checkPathParams refuses a parameter NAMED cluster_id that resolves to
// anything but the path, so both listings declare a name of their own. The
// alias is what keeps the existing callers working — the alert pages send
// ?cluster_id= — and without this test the rename would be a silent break of
// two working requests.
func TestAlertClusterFilterAliasBindsTheQueryKeyCallersSend(t *testing.T) {
	for _, path := range []string{alertScope, alertRuleScope} {
		t.Run(path, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodGet, path)
			if got := e.Parameters["filter_cluster_id"].Alias; got != "cluster_id" {
				t.Fatalf("filter_cluster_id declares alias %q, want cluster_id — the pages send that spelling", got)
			}
			if _, declared := e.Parameters["cluster_id"]; declared {
				t.Error("cluster_id is declared as a parameter NAME; checkPathParams refuses that because " +
					"the permission middleware reads the same name out of the path")
			}

			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeAlertEndpoint(t, fiber.MethodGet, path, cap))
			target := path + "?cluster_id=" + testAlertFilterClust
			status, env := send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204 — this is what the alert pages send", status, env.Message)
			}
			if got := cap.params.String("filter_cluster_id"); got != testAlertFilterClust {
				t.Errorf("filter_cluster_id = %q, want the aliased query value to bind", got)
			}
		})
	}
}

// TestAlertPagingRefusesOutOfRangeRatherThanSubstituting records the one
// deliberate behaviour change in this domain.
//
// `if limit <= 0 || limit > 100 { limit = 50 }` silently substituted 50 for
// every out-of-range page size, so a caller asking for 500 rows got 50 and no
// indication — which reads as missing data rather than as a rejected request.
// The bounds refuse it instead, the same trade the migration, CVE and PBS-task
// listings made.
func TestAlertPagingRefusesOutOfRangeRatherThanSubstituting(t *testing.T) {
	for _, path := range []string{
		alertScope,
		alertRuleScope,
		clusterScope + "/alerts",
		clusterScope + "/maintenance-windows",
	} {
		t.Run(path, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodGet, path)
			limit := e.Parameters["limit"]
			if limit.Default != 50 {
				t.Errorf("limit declares default %#v, want 50 — the value the handler substituted", limit.Default)
			}
			if limit.Minimum == nil || *limit.Minimum != 1 || limit.Maximum == nil || *limit.Maximum != 100 {
				t.Errorf("limit declares bounds [%v, %v], want [1, 100] — the range the handler accepted",
					limit.Minimum, limit.Maximum)
			}
			offset := e.Parameters["offset"]
			if offset.Default != 0 {
				t.Errorf("offset declares default %#v, want 0", offset.Default)
			}
			if offset.Minimum == nil || *offset.Minimum != 0 {
				t.Errorf("offset declares minimum %v, want 0 — a negative one was clamped", offset.Minimum)
			}

			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeAlertEndpoint(t, fiber.MethodGet, path, cap))
			status, _ := send(t, app, httptest.NewRequest(http.MethodGet, alertRoute(path)+"?limit=500", nil))
			if status != fiber.StatusBadRequest {
				t.Errorf("status = %d, want 400 for a page size outside the range the handler accepted", status)
			}
			if cap.called {
				t.Error("the handler ran for an out-of-range page size")
			}
		})
	}
}

// TestMaintenanceWindowBodiesMatchWhatEachHandlerEnforced pins the required
// SETs, which differ between the two routes and did so before the migration.
//
// CREATE parses both timestamps unconditionally, so omitting either has always
// been a 400. UPDATE reads every field as `if x != ""`, so an empty value is
// indistinguishable from an absent one and both leave the stored column alone.
func TestMaintenanceWindowBodiesMatchWhatEachHandlerEnforced(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		want   []string
	}{
		{fiber.MethodPost, clusterScope + "/maintenance-windows", []string{"cluster_id", "ends_at", "starts_at"}},
		{fiber.MethodPut, clusterScope + "/maintenance-windows/:id", []string{"cluster_id", "id"}},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			var got []string
			for name, prop := range e.Parameters {
				if !prop.Optional {
					got = append(got, name)
				}
			}
			sort.Strings(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("required parameters = %v, want %v", got, tt.want)
			}
			// node_id's EMPTY value means "the whole cluster", which every
			// registered format rejects.
			node := e.Parameters["node_id"]
			if node.Format != "" {
				t.Errorf("node_id declares format %q; the empty string is the whole-cluster sentinel", node.Format)
			}
			if node.Pattern != emptyOrUUID {
				t.Errorf("node_id declares pattern %q, want the empty-or-uuid one", node.Pattern)
			}
		})
	}
}

// TestMaintenanceWindowWritesKeepTheirNodeOwnerCheck is the assertion behind
// the two routes whose tally says 2 calls and 1 hoist.
//
// resolveNodeCluster authorizes against the cluster that OWNS the node named in
// the body — deliberately not the body's cluster_id and not the path's, which
// is the cross-cluster escape its own comment documents. Middleware resolves
// the path's cluster and nothing else, so hoisting the route's own gate must
// not have taken that call with it.
func TestMaintenanceWindowWritesKeepTheirNodeOwnerCheck(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodPost, clusterScope + "/maintenance-windows"},
		{fiber.MethodPut, clusterScope + "/maintenance-windows/:id"},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			// The declaration is a plain Check — the hoist happened — and the
			// prose has to carry what the handler still does, because Describe()
			// renders only the gate.
			if e.Permissions.Check == nil {
				t.Fatalf("declares %q, want a Check", e.Permissions.Describe())
			}
			for _, phrase := range []string{"manage:maintenance_window", "owns it"} {
				if !strings.Contains(e.Description, phrase) {
					t.Errorf("the Description never mentions %q, so the node-owner rule is undocumented: %q",
						phrase, e.Description)
				}
			}
		})
	}
}

// TestChannelTypeEnumCoversTheDispatcherVocabulary holds the declared enums and
// the exported vocabulary together.
//
// The handlers no longer look a channel type up at all — the declaration's Enum
// refuses an unknown one before either runs — so handlers.NotificationChannelTypes
// is the only list left, and these enums are the only things that publish it. A
// declaration built from anything else would either make a real dispatcher
// unreachable or offer one that does not exist.
func TestChannelTypeEnumCoversTheDispatcherVocabulary(t *testing.T) {
	create := declaredEndpoint(t, fiber.MethodPost, channelScope)
	if got := create.Parameters["channel_type"].Enum; !slices.Equal(got, handlers.NotificationChannelTypes) {
		t.Errorf("the create enum is %v, want %v", got, handlers.NotificationChannelTypes)
	}

	// The update enum is the same vocabulary PLUS the empty string, because the
	// handler read "" as "leave the type alone" (`if req.ChannelType != ""`).
	update := declaredEndpoint(t, fiber.MethodPut, channelScope+"/:id")
	want := append([]string{""}, handlers.NotificationChannelTypes...)
	if got := update.Parameters["channel_type"].Enum; !slices.Equal(got, want) {
		t.Errorf("the update enum is %v, want %v — \"\" has always meant \"leave it as it is\"", got, want)
	}
}

// TestChannelUpdateKeepsItsEnabledTristate is the compatibility assertion for
// the partial-update contract.
//
// `enabled` was bound as a POINTER so that omitting it left the stored flag
// alone, and the handler keeps that with a p.OptBool read, which a default
// cannot fool (apischema.Property.Default). A Default of true on the update
// would still be wrong: it would document every omitted `enabled` as a
// re-enable, while the create's default of true, below, is one the handler
// really applies.
func TestChannelUpdateKeepsItsEnabledTristate(t *testing.T) {
	const path = channelScope + "/:id"
	e := declaredEndpoint(t, fiber.MethodPut, path)
	prop := e.Parameters["enabled"]
	if !prop.Optional {
		t.Error("enabled is required; the handler left an absent field alone")
	}
	if prop.Default != nil {
		t.Errorf("enabled declares default %#v; it must have NONE — an omitted one leaves the stored "+
			"flag alone, and a default would document a re-enable", prop.Default)
	}
	// And the create's default IS true, which is the handler's own.
	if got := declaredEndpoint(t, fiber.MethodPost, channelScope).Parameters["enabled"].Default; got != true {
		t.Errorf("the create declares default %#v for enabled, want true", got)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeAlertEndpoint(t, fiber.MethodPut, path, cap))
	status, env := send(t, app, jsonRequest(http.MethodPut, pathPrefix+"notification-channels/"+testChannelID, `{"name":"renamed"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if _, supplied := cap.params.OptBool("enabled"); supplied {
		t.Error("enabled reads as supplied on a body that omitted it; the rename would rewrite it")
	}
	if cap.params.Has("config") {
		t.Error("config reads as supplied on a body that omitted it; the rename would re-encrypt it")
	}
}

// TestAlertIdentifiersAreUUIDs pins the identifier shape on every path
// parameter in this domain. The handlers parsed each one with uuid.Parse and
// answered 400 for anything else; the format states the same rule one layer
// earlier and names the field.
func TestAlertIdentifiersAreUUIDs(t *testing.T) {
	declared := declaredAlertEndpoints(t)
	if len(declared) == 0 {
		t.Fatal("no alert routes are declared; this guard would pass vacuously")
	}
	for key, e := range declared {
		for _, name := range pathParamNames(e.Path) {
			prop, ok := e.Parameters[name]
			if !ok {
				t.Fatalf("%s: path parameter %q is not declared", key, name)
			}
			if prop.Format != "uuid" {
				t.Errorf("%s: path parameter %q declares format %q, want uuid", key, name, prop.Format)
			}
		}
	}
}

// TestEveryAlertEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryAlertEndpointIsDocumented(t *testing.T) {
	groups := map[string]bool{"Alerts": true, "Notification Channels": true, "Maintenance Windows": true}
	for key, e := range declaredAlertEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if !groups[e.Group] {
			t.Errorf("%s is in group %q, want one of Alerts, Notification Channels or Maintenance Windows", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
