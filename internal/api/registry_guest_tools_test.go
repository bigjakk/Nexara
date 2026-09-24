package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/guesttools"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to
// registry_guest_tools.go that quietly loosened a parameter would show up
// here.

// guestToolsRouteCount is how many endpoints
// registerGuestToolsEndpoints declares. See vmRouteCount in
// registry_vms_test.go for why the registry total is a sum of per-domain
// constants rather than one number.
const guestToolsRouteCount = 7

const testGuestVMID = "109"

// guestToolsRoute renders one guest tools route's path with the test ids
// substituted.
func guestToolsRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":vmid", testGuestVMID,
	).Replace(path)
}

// guestToolsLegacyPermissions is the permission each handler checked with
// a hand-placed requireClusterPerm call BEFORE Phase 6c, transcribed from
// internal/api/handlers/guest_tools.go at commit 3099aa8 — 7 handlers, 7
// calls, every one of them cluster-scoped on the "guest_tools" resource.
//
// The resource is the point of the tally here: these could plausibly have
// been execute:vm, and the three-way action split (view / manage /
// execute) is what an operator building a role reads.
var guestToolsLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/guest-tools/config":                 "view:guest_tools",
	"PUT /api/v1/clusters/:cluster_id/guest-tools/config":                 "manage:guest_tools",
	"GET /api/v1/clusters/:cluster_id/guest-tools/guests":                 "view:guest_tools",
	"PUT /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/policy":    "manage:guest_tools",
	"POST /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/detect":   "view:guest_tools",
	"POST /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/update":   "execute:guest_tools",
	"DELETE /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/update": "execute:guest_tools",
}

// declaredGuestToolsEndpoints returns every declaration whose path is
// under the /guest-tools prefix, keyed "METHOD path".
func declaredGuestToolsEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, guestToolsScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestGuestToolsRoutesDeclareTheSamePermissionTheyEnforced is the tally
// that makes deleting 7 requireClusterPerm calls a refactor rather than a
// change.
func TestGuestToolsRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredGuestToolsEndpoints(t)
	if len(declared) != guestToolsRouteCount {
		t.Fatalf("the registry declares %d guest tools routes, want %d", len(declared), guestToolsRouteCount)
	}
	if len(guestToolsLegacyPermissions) != guestToolsRouteCount {
		t.Fatalf("guestToolsLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(guestToolsLegacyPermissions), guestToolsRouteCount)
	}

	var view, manage, execute int
	for key, want := range guestToolsLegacyPermissions {
		switch want {
		case "view:guest_tools":
			view++
		case "manage:guest_tools":
			manage++
		case "execute:guest_tools":
			execute++
		}
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; every guest tools route's permission is "+
				"statically known", key, e.Permissions.Describe())
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path, so the "+
				"declaration has to as well", key, e.Permissions.Check.Scope)
		}
		if e.Permissions.Check.Resource != "guest_tools" {
			t.Errorf("%s gates on resource %q; guest tools has a resource of its own precisely so that "+
				"\"run this installer inside the OS\" can be withheld separately from execute:vm",
				key, e.Permissions.Check.Resource)
		}
	}
	if view != 3 || manage != 2 || execute != 2 {
		t.Errorf("the tally splits %d view / %d manage / %d execute, want 3 / 2 / 2", view, manage, execute)
	}

	for key := range declared {
		if _, listed := guestToolsLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in guestToolsLegacyPermissions — a new guest tools route "+
				"must be added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestGuestToolsRoutesNameTheirClusterFirst is the assertion behind this
// domain's third cluster-extraction shape.
//
// The helper these handlers used before the migration read the cluster and
// the Proxmox VMID together, which is neither of the two shapes the earlier
// batches migrated. A cluster-scoped Check resolves its cluster from the
// FIRST path placeholder (namesACluster, permissions.go), so the question
// was whether these paths still lead with :cluster_id. They do — and this
// asserts it rather than leaving it to Register's panic, which would only
// fire if someone reordered the path.
func TestGuestToolsRoutesNameTheirClusterFirst(t *testing.T) {
	for key, e := range declaredGuestToolsEndpoints(t) {
		names := pathParamNames(e.Path)
		if len(names) == 0 || names[0] != "cluster_id" {
			t.Errorf("%s has path parameters %v; :cluster_id must be the first, or the permission "+
				"middleware cannot resolve the cluster the route acts on", key, names)
		}
		if !namesACluster(names, e.Path) {
			t.Errorf("%s would be refused a cluster-scoped Check", key)
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
	}
}

// probeGuestToolsEndpoint is a declared guest tools endpoint with its
// handler swapped for a capture.
func probeGuestToolsEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestGuestToolsVMIDIsBoundedBySchema moves the hand-written vmid bounds
// the old helper carried into the declaration, and keeps them biting.
//
// The upper bound is load-bearing: SetPolicy writes to a table with no
// foreign key on vmid, so an out-of-range value used to be clamped by
// safeconv at the sqlc parameter while the audit row still recorded what
// the caller typed — an audit entry naming a guest that was never written.
func TestGuestToolsVMIDIsBoundedBySchema(t *testing.T) {
	const path = guestToolsScope + "/guests/:vmid/policy"
	for _, tt := range []struct {
		vmid string
		want int
	}{
		{"109", fiber.StatusNoContent},
		{"999999999", fiber.StatusNoContent},
		{"0", fiber.StatusBadRequest},
		{"-1", fiber.StatusBadRequest},
		{"1000000000", fiber.StatusBadRequest},
		{"abc", fiber.StatusBadRequest},
	} {
		t.Run(tt.vmid, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeGuestToolsEndpoint(t, fiber.MethodPut, path, cap))
			target := strings.NewReplacer(":cluster_id", testClusterID, ":vmid", tt.vmid).Replace(path)
			status, env := send(t, app, jsonRequest(http.MethodPut, target, `{}`))
			if status != tt.want {
				t.Fatalf("vmid %q: status = %d (%q), want %d", tt.vmid, status, env.Message, tt.want)
			}
			if tt.want == fiber.StatusBadRequest && cap.called {
				t.Error("the handler ran for a vmid the schema rejected")
			}
		})
	}
}

// TestGuestToolsConfigRequiresOnlyMode pins the required SET of the config
// body against what the handler refused before the migration, derived from
// `git show HEAD:internal/api/handlers/guest_tools.go`.
//
// Only mode was refused: target_version was checked only when non-empty,
// max_concurrent substituted a default for anything non-positive, and
// snapshot_before was a pointer precisely so it could be absent.
func TestGuestToolsConfigRequiresOnlyMode(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, guestToolsScope+"/config")
	var got []string
	for name, prop := range e.Parameters {
		if !prop.Optional {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	want := []string{"cluster_id", "mode"}
	if !slices.Equal(got, want) {
		t.Errorf("required parameters = %v, want %v", got, want)
	}

	mode := e.Parameters["mode"]
	if len(mode.Enum) != 3 || mode.Enum[0] != "disabled" || mode.Enum[1] != "report" || mode.Enum[2] != "staged" {
		t.Errorf("mode enum = %v, want [disabled report staged]", mode.Enum)
	}
}

// TestGuestToolsPolicyRequiresNothingButItsPath is the same assertion for
// the per-guest override: the handler bound every field and refused none
// of them outright.
func TestGuestToolsPolicyRequiresNothingButItsPath(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, guestToolsScope+"/guests/:vmid/policy")
	var got []string
	for name, prop := range e.Parameters {
		if !prop.Optional {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	want := []string{"cluster_id", "vmid"}
	if !slices.Equal(got, want) {
		t.Errorf("required parameters = %v, want %v", got, want)
	}
}

// TestGuestToolsPointerFieldsStayTriState is the compatibility assertion
// that matters most in this domain.
//
// snapshot_before is the rollback for a driver swap that can leave a guest
// unbootable, and excluded is the operator saying "never touch this
// guest". Both were *bool precisely so that a client which does not know
// about the field cannot clear them, and the handler keeps that with
// p.OptBool, which a declared default cannot fool (apischema.Property.Default);
// the reads below pin that an omitted key reaches it as not supplied. A
// Default would still be wrong: it would document every such save as setting
// the field.
func TestGuestToolsPointerFieldsStayTriState(t *testing.T) {
	for _, tt := range []struct {
		path string
		name string
	}{
		{guestToolsScope + "/config", "snapshot_before"},
		{guestToolsScope + "/guests/:vmid/policy", "excluded"},
	} {
		t.Run(tt.path+" "+tt.name, func(t *testing.T) {
			prop := declaredEndpoint(t, fiber.MethodPut, tt.path).Parameters[tt.name]
			if !prop.Optional {
				t.Errorf("%s is required; an omitted key has to keep the stored value", tt.name)
			}
			if prop.Default != nil {
				t.Errorf("%s declares default %#v; an omitted one keeps the stored value, and a "+
					"default would document it as set", tt.name, prop.Default)
			}

			// Omitted reads as not supplied…
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeGuestToolsEndpoint(t, fiber.MethodPut, tt.path, cap))
			body := `{"mode":"report"}`
			if strings.Contains(tt.path, "policy") {
				body = `{}`
			}
			if status, env := send(t, app, jsonRequest(http.MethodPut, guestToolsRoute(tt.path), body)); status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			if _, supplied := cap.params.OptBool(tt.name); supplied {
				t.Errorf("%s reads as supplied on a body that omitted it", tt.name)
			}

			// …and an explicit false reads as supplied.
			cap = &capture{}
			app = newRegistryApp(t, noAuth(), probeGuestToolsEndpoint(t, fiber.MethodPut, tt.path, cap))
			explicit := `{"mode":"report","` + tt.name + `":false}`
			if strings.Contains(tt.path, "policy") {
				explicit = `{"` + tt.name + `":false}`
			}
			if status, env := send(t, app, jsonRequest(http.MethodPut, guestToolsRoute(tt.path), explicit)); status != fiber.StatusNoContent {
				t.Fatalf("explicit false: status = %d (%q), want 204", status, env.Message)
			}
			value, supplied := cap.params.OptBool(tt.name)
			if !supplied || value {
				t.Errorf("an explicit %s:false read back as (%v, supplied=%v)", tt.name, value, supplied)
			}
		})
	}
}

// TestGuestToolsMaxConcurrentKeepsTheZeroSpelling is this domain's
// empty-sentinel case.
//
// GuestToolsPolicyCard's number input reads Number(e.target.value), which
// is 0 for a CLEARED field, and the handler has always read a non-positive
// count as "use the default". A minimum of 1 would 400 a save the operator
// can reach by selecting the field's contents and deleting them.
func TestGuestToolsMaxConcurrentKeepsTheZeroSpelling(t *testing.T) {
	const path = guestToolsScope + "/config"
	prop := declaredEndpoint(t, fiber.MethodPut, path).Parameters["max_concurrent"]
	if prop.Minimum == nil || *prop.Minimum != 0 {
		t.Errorf("max_concurrent minimum = %v, want 0 — the policy card sends 0 for a cleared field", prop.Minimum)
	}
	if prop.Maximum == nil || *prop.Maximum != 100 {
		t.Errorf("max_concurrent maximum = %v, want 100 — the bound the handler wrote by hand", prop.Maximum)
	}
	if prop.Default != guesttools.DefaultMaxConcurrent {
		t.Errorf("max_concurrent default = %#v, want %d — the value the handler substituted",
			prop.Default, guesttools.DefaultMaxConcurrent)
	}

	for _, tt := range []struct {
		value string
		want  int
	}{
		{"0", fiber.StatusNoContent},
		{"1", fiber.StatusNoContent},
		{"100", fiber.StatusNoContent},
		{"101", fiber.StatusBadRequest},
		{"-1", fiber.StatusBadRequest},
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeGuestToolsEndpoint(t, fiber.MethodPut, path, cap))
		body := `{"mode":"staged","max_concurrent":` + tt.value + `}`
		if status, env := send(t, app, jsonRequest(http.MethodPut, guestToolsRoute(path), body)); status != tt.want {
			t.Errorf("max_concurrent=%s: status = %d (%q), want %d", tt.value, status, env.Message, tt.want)
		}
	}

	// An omitted count carries the default rather than 0, so the handler's
	// own substitution never fires for a client that simply did not say.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeGuestToolsEndpoint(t, fiber.MethodPut, path, cap))
	if status, env := send(t, app, jsonRequest(http.MethodPut, guestToolsRoute(path), `{"mode":"staged"}`)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.Int("max_concurrent"); got != int64(guesttools.DefaultMaxConcurrent) {
		t.Errorf("max_concurrent = %d, want %d", got, guesttools.DefaultMaxConcurrent)
	}
}

// TestGuestToolsVersionKeepsTheEmptySentinel pins the other compatibility
// decision: "" means "follow the level above", and every registered format
// rejects "". The vocabulary check stays in the handler, where
// virtiowin.ValidVersion owns it.
func TestGuestToolsVersionKeepsTheEmptySentinel(t *testing.T) {
	for _, tt := range []struct{ path, body string }{
		{guestToolsScope + "/config", `{"mode":"report","target_version":""}`},
		{guestToolsScope + "/guests/:vmid/policy", `{"target_version":"","note":""}`},
	} {
		t.Run(tt.path, func(t *testing.T) {
			if got := declaredEndpoint(t, fiber.MethodPut, tt.path).Parameters["target_version"].Format; got != "" {
				t.Errorf("target_version declares format %q; every registered format rejects the empty "+
					"string this field uses as its sentinel", got)
			}
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeGuestToolsEndpoint(t, fiber.MethodPut, tt.path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPut, guestToolsRoute(tt.path), tt.body))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			if got := cap.params.String("target_version"); got != "" {
				t.Errorf("target_version = %q, want the empty sentinel to survive", got)
			}
		})
	}
}

// TestGuestToolsPostRoutesTakeNoBody covers the two POSTs the UI calls
// with no body at all: apiClient.post(path) sends Content-Length 0 with a
// JSON content type, which must not read as a malformed body.
func TestGuestToolsPostRoutesTakeNoBody(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodPost, guestToolsScope + "/guests/:vmid/detect"},
		{fiber.MethodPost, guestToolsScope + "/guests/:vmid/update"},
		{fiber.MethodDelete, guestToolsScope + "/guests/:vmid/update"},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeGuestToolsEndpoint(t, tt.method, tt.path, cap))
			req := httptest.NewRequest(tt.method, guestToolsRoute(tt.path), nil)
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			if status, env := send(t, app, req); status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204 — the UI sends no body", status, env.Message)
			}
			if !cap.called {
				t.Error("the handler did not run")
			}
		})
	}

	// run_now still defaults to false when the stage request carries a body
	// that omits it, which is what "stage for next boot" means.
	cap := &capture{}
	path := guestToolsScope + "/guests/:vmid/update"
	app := newRegistryApp(t, noAuth(), probeGuestToolsEndpoint(t, fiber.MethodPost, path, cap))
	if status, env := send(t, app, jsonRequest(http.MethodPost, guestToolsRoute(path), `{}`)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if cap.params.Bool("run_now") {
		t.Error("run_now defaulted to true; omitting it means \"stage for next boot\"")
	}
}

// TestGuestToolsRouteIsGatedByItsDeclaration proves the permission the
// declaration states is the permission the route enforces, end to end.
//
// It is run against the staged update, the most consequential of the
// seven: it mounts an ISO and schedules an installer inside a Windows
// guest.
func TestGuestToolsRouteIsGatedByItsDeclaration(t *testing.T) {
	const path = guestToolsScope + "/guests/:vmid/update"
	e := declaredEndpoint(t, fiber.MethodPost, path)
	if e.Permissions.Describe() != "execute:guest_tools" {
		t.Fatalf("the staged update declares %q, want execute:guest_tools", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := guestToolsRoute(path)

	t.Run("a caller holding manage:vm is refused", func(t *testing.T) {
		// The point of a dedicated resource: guest control does not imply
		// permission to run an installer inside the operating system.
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:vm": true, "execute:vm": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodPost, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding execute:guest_tools gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"execute:guest_tools": true}), gated)
		status, env := send(t, app, authedRequest(http.MethodPost, target))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if !cap.called {
			t.Error("the handler did not run for a caller holding the declared permission")
		}
	})

	t.Run("an anonymous caller is refused before the gate", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"execute:guest_tools": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodPost, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestEveryGuestToolsEndpointIsDocumented holds the declarations to the
// standard that makes this whole effort worth doing.
func TestEveryGuestToolsEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredGuestToolsEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Guest Tools" {
			t.Errorf("%s is in group %q, want Guest Tools", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
