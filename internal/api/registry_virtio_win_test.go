package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to
// registry_virtio_win.go that quietly loosened a parameter would show up
// here.

// virtioWinRouteCount is how many endpoints registerVirtioWinEndpoints
// declares. See vmRouteCount in registry_vms_test.go for why the registry
// total is a sum of per-domain constants rather than one number.
const virtioWinRouteCount = 8

func virtioWinRoute(path string) string {
	return strings.Replace(path, ":cluster_id", testClusterID, 1)
}

// virtioWinRoutesOutsideTheClusterCheckShape is this domain's half of the
// registry-wide exception list in registry_vms_test.go.
//
// These three are the first GLOBAL-scoped Checks in the registry. They are
// not Deferred or Advisory — each installs a real gate — but the gate is
// requirePerm rather than requireClusterPerm, because their subject is the
// install: one catalog and one download source shared by every cluster.
var virtioWinRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/virtio-win/releases": "global Check: the release catalog is instance-wide, and the path " +
		"names no cluster for a cluster-scoped gate to resolve",
	"GET /api/v1/virtio-win/mirror": "global Check: the download source is instance-wide",
	"PUT /api/v1/virtio-win/mirror": "global Check on manage:settings: one write repoints every cluster's " +
		"downloads at once",
}

// virtioWinLegacyPermissions is the permission each handler checked with a
// hand-placed call BEFORE Phase 6c, transcribed from
// internal/api/handlers/virtio_win.go at commit 3099aa8 — 8 handlers, 8
// calls, five cluster-scoped (requireClusterPerm) and three instance-wide
// (requirePerm).
//
// scope is what makes this tally worth writing down rather than inferring:
// requirePerm and requireClusterPerm are different gates, and swapping one
// for the other silently either over- or under-authorizes.
var virtioWinLegacyPermissions = map[string]struct {
	permission string
	scope      ScopeKind
}{
	"GET /api/v1/virtio-win/releases":                       {"view:storage", ScopeGlobal},
	"GET /api/v1/virtio-win/mirror":                         {"view:storage", ScopeGlobal},
	"PUT /api/v1/virtio-win/mirror":                         {"manage:settings", ScopeGlobal},
	"GET /api/v1/clusters/:cluster_id/virtio-win/config":    {"view:storage", ScopeCluster},
	"PUT /api/v1/clusters/:cluster_id/virtio-win/config":    {"manage:storage", ScopeCluster},
	"POST /api/v1/clusters/:cluster_id/virtio-win/check":    {"manage:storage", ScopeCluster},
	"POST /api/v1/clusters/:cluster_id/virtio-win/download": {"manage:storage", ScopeCluster},
	"GET /api/v1/clusters/:cluster_id/virtio-win/downloads": {"view:storage", ScopeCluster},
}

// declaredVirtioWinEndpoints returns every declaration in this domain,
// keyed "METHOD path".
func declaredVirtioWinEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, virtioWinGlobalScope) || strings.HasPrefix(e.Path, virtioWinClusterScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestVirtioWinRoutesDeclareTheSamePermissionTheyEnforced is the tally
// that makes deleting 8 hand-placed permission calls a refactor rather
// than a change — including the scope half, which is what separates
// requirePerm from requireClusterPerm.
func TestVirtioWinRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredVirtioWinEndpoints(t)
	if len(declared) != virtioWinRouteCount {
		t.Fatalf("the registry declares %d virtio-win routes, want %d", len(declared), virtioWinRouteCount)
	}
	if len(virtioWinLegacyPermissions) != virtioWinRouteCount {
		t.Fatalf("virtioWinLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(virtioWinLegacyPermissions), virtioWinRouteCount)
	}

	var global, cluster int
	for key, want := range virtioWinLegacyPermissions {
		if want.scope == ScopeGlobal {
			global++
		} else {
			cluster++
		}
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want.permission)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; every virtio-win route's permission is "+
				"statically known", key, e.Permissions.Describe())
			continue
		}
		if got := e.Permissions.Describe(); got != want.permission {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want.permission)
		}
		if e.Permissions.Check.Scope != want.scope {
			t.Errorf("%s is %s-scoped but the handler used %s: requirePerm and requireClusterPerm are "+
				"different gates, and swapping one for the other either over- or under-authorizes",
				key, e.Permissions.Check.Scope, want.scope)
		}
	}
	if global != 3 || cluster != 5 {
		t.Errorf("the tally splits %d global / %d cluster-scoped, want 3 / 5", global, cluster)
	}

	for key := range declared {
		if _, listed := virtioWinLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in virtioWinLegacyPermissions — a new virtio-win route "+
				"must be added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestVirtioWinGlobalRoutesNameNoCluster is the structural half of the
// scope split: the three instance-wide routes have no :cluster_id, so a
// cluster-scoped Check on any of them would be refused at registration
// rather than silently resolving the wrong object.
func TestVirtioWinGlobalRoutesNameNoCluster(t *testing.T) {
	for key := range virtioWinRoutesOutsideTheClusterCheckShape {
		_, path, _ := strings.Cut(key, " ")
		if names := pathParamNames(path); len(names) != 0 {
			t.Errorf("%s has path parameters %v; these three are instance-wide", key, names)
		}
		if namesACluster(pathParamNames(path), path) {
			t.Errorf("%s would accept a cluster-scoped Check, but it acts on the whole install", key)
		}
	}
}

// probeVirtioWinEndpoint is a declared virtio-win endpoint with its
// handler swapped for a capture.
func probeVirtioWinEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestVirtioWinConfigRequiresNothingButItsPath pins the required SET of
// the config body against what the handler refused before the migration,
// derived from `git show HEAD:internal/api/handlers/virtio_win.go`.
//
// The handler refused an empty storage ONLY when enabled was true, which
// is a cross-field rule and stays in the handler. Declaring storage
// required would 400 the save that switches auto-download OFF.
func TestVirtioWinConfigRequiresNothingButItsPath(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, virtioWinClusterScope+"/config")
	var got []string
	for name, prop := range e.Parameters {
		if !prop.Optional {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	if want := []string{"cluster_id"}; !slices.Equal(got, want) {
		t.Errorf("required parameters = %v, want %v", got, want)
	}

	// And the shape that would have been the easy mistake: disabling the
	// feature with no storage set still works.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeVirtioWinEndpoint(t, fiber.MethodPut, virtioWinClusterScope+"/config", cap))
	body := `{"enabled":false,"storage":"","node":"","target_version":"","check_schedule":"","check_timezone":""}`
	if status, env := send(t, app, jsonRequest(http.MethodPut, virtioWinRoute(virtioWinClusterScope+"/config"), body)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is the payload that turns auto-download off", status, env.Message)
	}
	if got := cap.params.String("storage"); got != "" {
		t.Errorf("storage = %q, want the empty sentinel to survive", got)
	}
}

// TestVirtioWinPruneStaysTriState is the compatibility assertion that
// matters most here: prune DELETES ISOs, so an omitted key must keep the
// stored value rather than reading as false.
func TestVirtioWinPruneStaysTriState(t *testing.T) {
	const path = virtioWinClusterScope + "/config"
	prop := declaredEndpoint(t, fiber.MethodPut, path).Parameters["prune_enabled"]
	if !prop.Optional {
		t.Error("prune_enabled is required; an omitted key has to keep the stored value")
	}
	if prop.Default != nil {
		t.Errorf("prune_enabled declares default %#v; an omitted one keeps the stored value, and a "+
			"default would document it as set — on a field that deletes files", prop.Default)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeVirtioWinEndpoint(t, fiber.MethodPut, path, cap))
	if status, env := send(t, app, jsonRequest(http.MethodPut, virtioWinRoute(path), `{"enabled":false}`)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if _, supplied := cap.params.OptBool("prune_enabled"); supplied {
		t.Error("prune_enabled reads as supplied on a body that omitted it")
	}

	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeVirtioWinEndpoint(t, fiber.MethodPut, path, cap))
	if status, env := send(t, app, jsonRequest(http.MethodPut, virtioWinRoute(path), `{"enabled":false,"prune_enabled":false}`)); status != fiber.StatusNoContent {
		t.Fatalf("explicit false: status = %d (%q), want 204", status, env.Message)
	}
	if value, supplied := cap.params.OptBool("prune_enabled"); !supplied || value {
		t.Errorf("an explicit prune_enabled:false read back as (%v, supplied=%v)", value, supplied)
	}
}

// TestVirtioWinMirrorBodyKeepsItsEmptyClear pins the source override's own
// sentinel: an empty base_url clears the override and goes back to
// upstream, which is what the card sends for an empty field. Every
// registered format rejects "", so this parameter cannot carry one —
// virtiowin.NormalizeBase owns the rest of the rule.
func TestVirtioWinMirrorBodyKeepsItsEmptyClear(t *testing.T) {
	const path = virtioWinGlobalScope + "/mirror"
	prop := declaredEndpoint(t, fiber.MethodPut, path).Parameters["base_url"]
	if prop.Format != "" {
		t.Errorf("base_url declares format %q; an empty value is how the override is cleared", prop.Format)
	}
	if prop.Default != "" {
		t.Errorf("base_url default = %#v, want the empty string the handler already applied", prop.Default)
	}

	for _, tt := range []struct {
		name string
		body string
	}{
		{"clearing the override", `{"base_url":""}`},
		{"an empty body clears it too", `{}`},
		{"with both confirmations", `{"base_url":"http://192.0.2.10/virtio","allow_insecure":true,"allow_private_address":true}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeVirtioWinEndpoint(t, fiber.MethodPut, path, cap))
			if status, env := send(t, app, jsonRequest(http.MethodPut, path, tt.body)); status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
		})
	}

	// The two confirmation flags default to false: a caller that does not
	// send them has not confirmed anything.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeVirtioWinEndpoint(t, fiber.MethodPut, path, cap))
	if status, env := send(t, app, jsonRequest(http.MethodPut, path, `{"base_url":"https://mirror.example.com/virtio"}`)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if cap.params.Bool("allow_insecure") || cap.params.Bool("allow_private_address") {
		t.Error("a confirmation flag defaulted to true; omitting one means the caller confirmed nothing")
	}
}

// TestVirtioWinDownloadsLimitIsBounded pins the download history's
// ?limit=, including the spelling it deliberately drops: the handler
// CLAMPED anything outside 1..500 to 50, so ?limit=5000 answered with a
// page the caller never asked for.
func TestVirtioWinDownloadsLimitIsBounded(t *testing.T) {
	const path = virtioWinClusterScope + "/downloads"
	if got := declaredEndpoint(t, fiber.MethodGet, path).Parameters["limit"].Default; got != 50 {
		t.Errorf("limit default = %#v, want 50 — the value the handler substituted", got)
	}

	target := virtioWinRoute(path)
	for _, tt := range []struct {
		query string
		want  int
		value int64
	}{
		{query: "", want: fiber.StatusNoContent, value: 50},
		{query: "?limit=500", want: fiber.StatusNoContent, value: 500},
		{query: "?limit=0", want: fiber.StatusBadRequest},
		{query: "?limit=5000", want: fiber.StatusBadRequest},
		{query: "?offset=0", want: fiber.StatusBadRequest}, // this listing has never paged
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeVirtioWinEndpoint(t, fiber.MethodGet, path, cap))
		status, env := send(t, app, httptest.NewRequest(http.MethodGet, target+tt.query, nil))
		if status != tt.want {
			t.Errorf("%q: status = %d (%q), want %d", tt.query, status, env.Message, tt.want)
			continue
		}
		if tt.want == fiber.StatusNoContent && cap.params.Int("limit") != tt.value {
			t.Errorf("%q: limit = %d, want %d", tt.query, cap.params.Int("limit"), tt.value)
		}
	}
}

// TestVirtioWinPostRoutesTakeNoBody covers the check button, which calls
// apiClient.post(path) with no body at all.
func TestVirtioWinPostRoutesTakeNoBody(t *testing.T) {
	const path = virtioWinClusterScope + "/check"
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeVirtioWinEndpoint(t, fiber.MethodPost, path, cap))
	req := httptest.NewRequest(http.MethodPost, virtioWinRoute(path), nil)
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	if status, env := send(t, app, req); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — the check button sends no body", status, env.Message)
	}

	// And the download body's version is optional: omitting it resolves the
	// cluster's effective target, which is what the "download target" button
	// relies on.
	const dl = virtioWinClusterScope + "/download"
	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeVirtioWinEndpoint(t, fiber.MethodPost, dl, cap))
	if status, env := send(t, app, jsonRequest(http.MethodPost, virtioWinRoute(dl), `{}`)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if cap.params.Has("version") {
		t.Error("version reads as supplied on a body that omitted it")
	}
}

// TestVirtioWinMirrorRouteIsGatedByItsDeclaration proves the scope half of
// the declaration is the scope the route enforces, end to end.
//
// It is run against the source override, the one route here on
// manage:settings: one write repoints every cluster's driver-media
// downloads, which is why it does not take manage:storage.
func TestVirtioWinMirrorRouteIsGatedByItsDeclaration(t *testing.T) {
	const path = virtioWinGlobalScope + "/mirror"
	e := declaredEndpoint(t, fiber.MethodPut, path)
	if e.Permissions.Describe() != "manage:settings" {
		t.Fatalf("the source override declares %q, want manage:settings", e.Permissions.Describe())
	}
	if e.Permissions.Check.Scope != ScopeGlobal {
		t.Fatalf("the source override is %s-scoped, want global", e.Permissions.Check.Scope)
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()

	t.Run("a caller holding only manage:storage is refused", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:storage": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodPut, path))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding manage:settings gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:settings": true}), gated)
		status, env := send(t, app, authedRequest(http.MethodPut, path))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if !cap.called {
			t.Error("the handler did not run for a caller holding the declared permission")
		}
	})

	t.Run("an anonymous caller is refused before the gate", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:settings": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodPut, path, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestEveryVirtioWinEndpointIsDocumented holds the declarations to the
// standard that makes this whole effort worth doing.
func TestEveryVirtioWinEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredVirtioWinEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "virtio-win" {
			t.Errorf("%s is in group %q, want virtio-win", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
