package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// proxmoxJSONTags returns the wire names a Proxmox parameter struct
// forwards, read off its json tags.
//
// Reading the struct rather than repeating its field list is what makes
// TestClusterOptionsBodyCoversEveryProxmoxProperty a ratchet: a field
// added to the struct and not declared in the schema would otherwise be a
// parameter the endpoint silently rejects, which is the exact failure
// closing the parameter set was meant to remove.
func proxmoxJSONTags(t *testing.T, v any) []string {
	t.Helper()
	typ := reflect.TypeOf(v)
	out := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			t.Fatalf("%s.%s has no json tag; this comparison reads wire names off them",
				typ.Name(), typ.Field(i).Name)
		}
		out = append(out, tag)
	}
	return out
}

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to
// registry_cluster_options.go that quietly loosened a parameter would show
// up here.

// clusterOptionsRouteCount is how many endpoints
// registerClusterOptionsEndpoints declares. See vmRouteCount in
// registry_vms_test.go for why the registry total is a sum of per-domain
// constants rather than one number.
const clusterOptionsRouteCount = 9

// clusterOptionsPaths are this domain's routes. They hang off the cluster
// directly rather than off a prefix of their own, so they are listed
// rather than matched — a prefix filter would sweep in every other
// cluster-scoped domain.
var clusterOptionsPaths = map[string]bool{
	clusterScope + "/options":      true,
	clusterScope + "/description":  true,
	clusterScope + "/tags":         true,
	clusterScope + "/config":       true,
	clusterScope + "/config/join":  true,
	clusterScope + "/config/nodes": true,
}

// clusterOptionsLegacyPermissions is the permission each handler checked
// with a hand-placed requireClusterPerm call BEFORE Phase 6c, transcribed
// from internal/api/handlers/cluster_options.go at commit 3099aa8 — 9
// handlers, 9 calls, every one of them cluster-scoped on the "cluster"
// resource.
var clusterOptionsLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/options":      "view:cluster",
	"PUT /api/v1/clusters/:cluster_id/options":      "manage:cluster",
	"GET /api/v1/clusters/:cluster_id/description":  "view:cluster",
	"PUT /api/v1/clusters/:cluster_id/description":  "manage:cluster",
	"GET /api/v1/clusters/:cluster_id/tags":         "view:cluster",
	"PUT /api/v1/clusters/:cluster_id/tags":         "manage:cluster",
	"GET /api/v1/clusters/:cluster_id/config":       "view:cluster",
	"GET /api/v1/clusters/:cluster_id/config/join":  "view:cluster",
	"GET /api/v1/clusters/:cluster_id/config/nodes": "view:cluster",
}

// declaredClusterOptionsEndpoints returns every declaration in this
// domain, keyed "METHOD path".
func declaredClusterOptionsEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if clusterOptionsPaths[e.Path] {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestClusterOptionsRoutesDeclareTheSamePermissionTheyEnforced is the
// tally that makes deleting 9 requireClusterPerm calls a refactor rather
// than a change.
func TestClusterOptionsRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredClusterOptionsEndpoints(t)
	if len(declared) != clusterOptionsRouteCount {
		t.Fatalf("the registry declares %d cluster option routes, want %d", len(declared), clusterOptionsRouteCount)
	}
	if len(clusterOptionsLegacyPermissions) != clusterOptionsRouteCount {
		t.Fatalf("clusterOptionsLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(clusterOptionsLegacyPermissions), clusterOptionsRouteCount)
	}

	var view, manage int
	for key, want := range clusterOptionsLegacyPermissions {
		switch want {
		case "view:cluster":
			view++
		case "manage:cluster":
			manage++
		}
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; every route in this domain resolved its cluster "+
				"from the path and then made one static call", key, e.Permissions.Describe())
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
	if view != 6 || manage != 3 {
		t.Errorf("the tally splits %d view:cluster / %d manage:cluster, want 6 / 3", view, manage)
	}

	for key := range declared {
		if _, listed := clusterOptionsLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in clusterOptionsLegacyPermissions — a new cluster option "+
				"route must be added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// probeClusterOptionsEndpoint is a declared endpoint with its handler
// swapped for a capture.
func probeClusterOptionsEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

func clusterOptionsRoute(path string) string {
	return strings.Replace(path, ":cluster_id", testClusterID, 1)
}

// TestClusterOptionsBodyCoversEveryProxmoxProperty is the assertion that
// makes this endpoint's declaration worth having.
//
// The handler bound proxmox.UpdateClusterOptionsParams directly, so a key
// the struct did not name was silently dropped and the caller got a 200
// for a save that changed nothing. The schema closes the set, which only
// helps if it declares EVERY field the struct forwards — a missing one
// turns a working save into "unknown parameter".
//
// The want list is derived from the struct's own json tags, so adding a
// field to proxmox.UpdateClusterOptionsParams without declaring it here
// fails the build rather than shipping a silently-dropped parameter.
func TestClusterOptionsBodyCoversEveryProxmoxProperty(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, clusterScope+"/options")

	want := proxmoxJSONTags(t, proxmox.UpdateClusterOptionsParams{})
	got := make([]string, 0, len(e.Parameters))
	for name := range e.Parameters {
		if name == "cluster_id" {
			continue // the path parameter, not a Proxmox property
		}
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("the options body declares %v\nbut proxmox.UpdateClusterOptionsParams forwards %v", got, want)
	}

	// Every one of them is optional: this is a partial update, and the
	// handler never required any of them.
	for name, prop := range e.Parameters {
		if name == "cluster_id" {
			continue
		}
		if !prop.Optional {
			t.Errorf("%q is required; PUT /options has always been a partial update", name)
		}
	}
}

// TestClusterOptionsAcceptsTheOptionsTabPayload is the compatibility
// assertion for this domain: the exact shape ClusterOptionsTab sends,
// including the hyphenated keys and the `delete` list it uses to clear a
// field (Proxmox 500s on an empty form value for most of these, so an
// empty string is NOT how the UI clears one).
func TestClusterOptionsAcceptsTheOptionsTabPayload(t *testing.T) {
	const path = clusterScope + "/options"
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeClusterOptionsEndpoint(t, fiber.MethodPut, path, cap))

	body := `{"console":"xtermjs","keyboard":"en-us","language":"en","email_from":"noreply@example.com",` +
		`"http_proxy":"","mac_prefix":"","fencing":"watchdog","max_workers":0,` +
		`"migration":"type=secure","ha":"shutdown_policy=migrate","crs":"ha=basic",` +
		`"bwlimit":"migration=100000","next-id":"lower=100,upper=1000",` +
		`"delete":"http_proxy,mac_prefix"}`
	status, env := send(t, app, jsonRequest(http.MethodPut, clusterOptionsRoute(path), body))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is the shape the options tab sends", status, env.Message)
	}
	if got := cap.params.String("next-id"); got != "lower=100,upper=1000" {
		t.Errorf("next-id = %q; the hyphenated Proxmox spelling has to survive", got)
	}
	if got := cap.params.String("delete"); got != "http_proxy,mac_prefix" {
		t.Errorf("delete = %q, want the clear list to reach the handler", got)
	}
	// max_workers=0 is what the tab sends for an empty field, and it must
	// stay distinguishable from an omitted one: the handler forwards it as
	// a *int.
	if value, supplied := cap.params.OptInt("max_workers"); !supplied || value != 0 {
		t.Errorf("an explicit max_workers:0 read back as (%d, supplied=%v)", value, supplied)
	}
}

// TestClusterOptionsKeepTheirTriState pins the *string semantics the bound
// struct had: an omitted key must NOT reach Proxmox, and an empty one must.
// Folding the two together would either send every property on every save
// or make clearing one impossible.
func TestClusterOptionsKeepTheirTriState(t *testing.T) {
	const path = clusterScope + "/options"

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeClusterOptionsEndpoint(t, fiber.MethodPut, path, cap))
	if status, env := send(t, app, jsonRequest(http.MethodPut, clusterOptionsRoute(path), `{"console":""}`)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if value, supplied := cap.params.OptString("console"); !supplied || value != "" {
		t.Errorf("an explicit console:\"\" read back as (%q, supplied=%v)", value, supplied)
	}
	if _, supplied := cap.params.OptString("keyboard"); supplied {
		t.Error("keyboard reads as supplied on a body that omitted it; UpdateOptions would send it to Proxmox")
	}

	// And an empty body is a real request — "change nothing".
	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeClusterOptionsEndpoint(t, fiber.MethodPut, path, cap))
	if status, env := send(t, app, jsonRequest(http.MethodPut, clusterOptionsRoute(path), `{}`)); status != fiber.StatusNoContent {
		t.Errorf("empty body: status = %d (%q), want 204", status, env.Message)
	}
}

// TestClusterOptionsRejectsAMisspelledKey is the other half of closing the
// parameter set: the failure mode the bound struct had was silence.
func TestClusterOptionsRejectsAMisspelledKey(t *testing.T) {
	for _, tt := range []struct{ path, body, field string }{
		// "registered_tags" is the /tags spelling; on /options the property
		// is hyphenated, and sending the wrong one used to do nothing.
		{clusterScope + "/options", `{"registered_tags":"a;b"}`, "registered_tags:"},
		{clusterScope + "/options", `{"max_wrokers":4}`, "max_wrokers:"},
		// And the mirror image on /tags, where the hyphenated spelling is
		// the wrong one.
		{clusterScope + "/tags", `{"registered-tags":"a;b"}`, "registered-tags:"},
	} {
		t.Run(tt.path+" "+tt.field, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeClusterOptionsEndpoint(t, fiber.MethodPut, tt.path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPut, clusterOptionsRoute(tt.path), tt.body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, tt.field) {
				t.Errorf("message = %q, want it to start with %q", env.Message, tt.field)
			}
			if cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}
}

// TestClusterDescriptionClearsOnAnOmittedKey pins the one place in this
// domain where an omitted parameter is NOT "leave it alone".
//
// UpdateDescription bound a plain string and took its address
// unconditionally, so a body with no description cleared it. The schema's
// Default of "" states that rather than changing it — declaring the
// parameter required would 400 a request that used to work.
func TestClusterDescriptionClearsOnAnOmittedKey(t *testing.T) {
	const path = clusterScope + "/description"
	if got := declaredEndpoint(t, fiber.MethodPut, path).Parameters["description"].Default; got != "" {
		t.Errorf("description default = %#v, want the empty string the handler already applied", got)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeClusterOptionsEndpoint(t, fiber.MethodPut, path, cap))
	if status, env := send(t, app, jsonRequest(http.MethodPut, clusterOptionsRoute(path), `{}`)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.String("description"); got != "" {
		t.Errorf("description = %q, want \"\"", got)
	}

	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeClusterOptionsEndpoint(t, fiber.MethodPut, path, cap))
	body := `{"description":"Primary datacenter"}`
	if status, env := send(t, app, jsonRequest(http.MethodPut, clusterOptionsRoute(path), body)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.String("description"); got != "Primary datacenter" {
		t.Errorf("description = %q, want it carried through", got)
	}
}

// TestClusterOptionsRouteIsGatedByItsDeclaration proves the permission the
// declaration states is the permission the route enforces, end to end.
//
// It is run against PUT /options, the most consequential of the nine: one
// write here changes migration, HA and fencing policy for the whole
// datacenter.
func TestClusterOptionsRouteIsGatedByItsDeclaration(t *testing.T) {
	const path = clusterScope + "/options"
	e := declaredEndpoint(t, fiber.MethodPut, path)
	if e.Permissions.Describe() != "manage:cluster" {
		t.Fatalf("PUT /options declares %q, want manage:cluster", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := clusterOptionsRoute(path)

	t.Run("a caller holding only view:cluster is refused", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:cluster": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodPut, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding manage:cluster gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:cluster": true}), gated)
		status, env := send(t, app, authedRequest(http.MethodPut, target))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if !cap.called {
			t.Error("the handler did not run for a caller holding the declared permission")
		}
	})

	t.Run("an anonymous caller is refused before the gate", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:cluster": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodPut, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestEveryClusterOptionsEndpointIsDocumented holds the declarations to
// the standard that makes this whole effort worth doing.
func TestEveryClusterOptionsEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredClusterOptionsEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Clusters" {
			t.Errorf("%s is in group %q, want Clusters", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
