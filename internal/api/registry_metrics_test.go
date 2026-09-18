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
// rather than a fixture shaped like them.

// metricsRouteCount is how many endpoints registerMetricsEndpoints declares.
// See vmRouteCount in registry_vms_test.go for why the registry total is a sum
// of per-domain constants rather than one number.
const metricsRouteCount = 3

// metricsLegacyPermissions is the permission each handler checked with a
// hand-placed requireClusterPerm call BEFORE Phase 6j, transcribed from
// `git show HEAD:internal/api/handlers/metrics.go` at commit eaeafa7 — three
// handlers, three calls, every one cluster-scoped and every one on a DIFFERENT
// resource.
//
// The three resources are the point of the tally: gating all three on
// view:cluster would have been the obvious simplification and would have handed
// a cluster viewer a guest's and a node's series without the grants that own
// them.
var metricsLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/metrics":                "view:cluster",
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/metrics":     "view:vm",
	"GET /api/v1/clusters/:cluster_id/nodes/:node_id/metrics": "view:node",
}

// declaredMetricsEndpoints returns the three declarations by "METHOD path".
func declaredMetricsEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if _, want := metricsLegacyPermissions[e.Method+" "+e.Path]; want {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestMetricsRoutesDeclareTheSamePermissionTheyEnforced is the tally that makes
// deleting three requireClusterPerm calls a refactor rather than a change.
//
// It also replaces the FIRST half of the guard that used to live in
// internal/api/handlers/metrics_authz_guard_test.go, and replaces it with
// something stronger: that guard searched the handler body for a
// requireClusterPerm call and could not see WHICH permission it named, so all
// three could have collapsed onto view:cluster and still passed. This compares
// the action and the resource of each one exactly.
func TestMetricsRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredMetricsEndpoints(t)
	if len(declared) != metricsRouteCount {
		t.Fatalf("the registry declares %d metrics routes, want %d", len(declared), metricsRouteCount)
	}
	if len(metricsLegacyPermissions) != metricsRouteCount {
		t.Fatalf("metricsLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(metricsLegacyPermissions), metricsRouteCount)
	}

	for key, want := range metricsLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; every metrics route's permission is statically known",
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
}

// TestMetricRangeVocabulary pins the declared Enum against the handler's own
// rangeDurations map.
//
// They are two copies of one list, and the direction that bites is a schema
// that accepts a value the map does not carry: the handler answers 500 for that
// rather than serving an empty series (metricWindow), so the drift surfaces as
// an outage on one window rather than as silence — but only after a request
// exercises it. This makes it a build failure instead.
func TestMetricRangeVocabulary(t *testing.T) {
	want := handlers.MetricRangeKeys()
	got := slices.Clone(metricRangeParam.Enum)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("the declared ?range= enum is %v but handlers.rangeDurations carries %v — "+
			"a value in one and not the other is either a 500 or a window nobody can ask for", got, want)
	}
	if metricRangeParam.Default != "1h" {
		t.Errorf("?range= default = %#v, want \"1h\" — the value the handler substituted", metricRangeParam.Default)
	}
}

// TestMetricsRangeIsValidatedBeforeTheHandler drives the declaration end to
// end: the accepted windows reach the handler, an unlisted one is a 400 naming
// the field, and omitting it carries the default.
func TestMetricsRangeIsValidatedBeforeTheHandler(t *testing.T) {
	const path = clusterScope + "/metrics"
	target := strings.ReplaceAll(path, ":cluster_id", testClusterID)

	for _, tt := range []struct {
		query string
		want  int
		value string
	}{
		{query: "", want: fiber.StatusNoContent, value: "1h"},
		{query: "?range=6h", want: fiber.StatusNoContent, value: "6h"},
		{query: "?range=7d", want: fiber.StatusNoContent, value: "7d"},
		{query: "?range=30d", want: fiber.StatusBadRequest},
		{query: "?range=", want: fiber.StatusBadRequest},
		{query: "?window=1h", want: fiber.StatusBadRequest},
	} {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodGet, path)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		status, env := send(t, app, httptest.NewRequest(http.MethodGet, target+tt.query, nil))
		if status != tt.want {
			t.Errorf("%q: status = %d (%q), want %d", tt.query, status, env.Message, tt.want)
			continue
		}
		if tt.want != fiber.StatusNoContent {
			if cap.called {
				t.Errorf("%q: the handler ran for a request the schema rejected", tt.query)
			}
			continue
		}
		if got := cap.params.String("range"); got != tt.value {
			t.Errorf("%q: range reached the handler as %q, want %q", tt.query, got, tt.value)
		}
	}
}

// TestEveryMetricsEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryMetricsEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredMetricsEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Metrics" {
			t.Errorf("%s is in group %q, want Metrics", key, e.Group)
		}
		names := pathParamNames(e.Path)
		if len(names) == 0 || names[0] != "cluster_id" {
			t.Errorf("%s has path parameters %v; :cluster_id must be the first, or the permission "+
				"middleware cannot resolve the cluster the route acts on", key, names)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
