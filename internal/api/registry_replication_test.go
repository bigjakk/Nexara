package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to
// registry_replication.go that quietly loosened a parameter would show up
// here.

// replicationRouteCount is how many endpoints
// registerReplicationEndpoints declares. See vmRouteCount in
// registry_vms_test.go for why the registry total is a sum of per-domain
// constants rather than one number.
const replicationRouteCount = 8

const testJobID = "100-0"

// replicationRoute renders one replication route's path with the test ids
// substituted.
func replicationRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":job_id", testJobID,
	).Replace(path)
}

// replicationLegacyPermissions is the permission each handler checked with
// a hand-placed requireClusterPerm call BEFORE Phase 6b, transcribed from
// internal/api/handlers/replication.go at commit 13ceac0 (8 handlers, 8
// calls, every one of them cluster-scoped on the "replication" resource).
var replicationLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/replication":                  "view:replication",
	"POST /api/v1/clusters/:cluster_id/replication":                 "manage:replication",
	"GET /api/v1/clusters/:cluster_id/replication/:job_id":          "view:replication",
	"PUT /api/v1/clusters/:cluster_id/replication/:job_id":          "manage:replication",
	"DELETE /api/v1/clusters/:cluster_id/replication/:job_id":       "manage:replication",
	"POST /api/v1/clusters/:cluster_id/replication/:job_id/trigger": "manage:replication",
	"GET /api/v1/clusters/:cluster_id/replication/:job_id/status":   "view:replication",
	"GET /api/v1/clusters/:cluster_id/replication/:job_id/log":      "view:replication",
}

// declaredReplicationEndpoints returns every declaration whose path is
// under the /replication prefix, keyed "METHOD path".
func declaredReplicationEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, replicationScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestReplicationRoutesDeclareTheSamePermissionTheyEnforced is the tally
// that makes deleting 8 requireClusterPerm calls a refactor rather than a
// change.
func TestReplicationRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredReplicationEndpoints(t)
	if len(declared) != replicationRouteCount {
		t.Fatalf("the registry declares %d replication routes, want %d", len(declared), replicationRouteCount)
	}
	if len(replicationLegacyPermissions) != replicationRouteCount {
		t.Fatalf("replicationLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(replicationLegacyPermissions), replicationRouteCount)
	}

	var view, manage int
	for key, want := range replicationLegacyPermissions {
		switch want {
		case "view:replication":
			view++
		case "manage:replication":
			manage++
		}
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; every replication route's permission is "+
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
	}
	if view != 4 || manage != 4 {
		t.Errorf("the tally splits %d view:replication / %d manage:replication, want 4 / 4", view, manage)
	}

	for key := range declared {
		if _, listed := replicationLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in replicationLegacyPermissions — a new replication "+
				"route must be added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestReplicationRoutesDeclareEveryPathParameter re-states
// checkPathParams' rule for this domain and adds the one it does NOT
// enforce: that :cluster_id is the FIRST placeholder.
func TestReplicationRoutesDeclareEveryPathParameter(t *testing.T) {
	for key, e := range declaredReplicationEndpoints(t) {
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
		if !strings.Contains(e.Path, "/:job_id") {
			continue
		}
		if e.Parameters["job_id"].Pattern != replicationJobIDPattern {
			t.Errorf("%s declares job_id with pattern %q, want %q — the client appends this value to a "+
				"Proxmox path, and url.PathEscape leaves \"..\" alone",
				key, e.Parameters["job_id"].Pattern, replicationJobIDPattern)
		}
	}
}

// probeReplicationEndpoint is a declared replication endpoint with its
// handler swapped for a capture.
func probeReplicationEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestReplicationCreateTakesIDUnderItsAlias is the assertion behind the
// one declaration in this phase that could not use the wire name it was
// given.
//
// checkPathParams refuses a body parameter named "id" on a cluster-scoped
// route, because clusterIDFromParam reads TWO names — cluster_id, falling
// back to id — so "id" is a name the permission gate also reads. On this
// path the fallback cannot fire, but the guard is about the name rather
// than about today's path, and routing around it would reopen the hole on
// the next path edit. The parameter is therefore declared as job_id with
// "id" as its alias, and BOTH spellings have to keep working: the create
// dialog sends "id", and the docs now name "job_id".
func TestReplicationCreateTakesIDUnderItsAlias(t *testing.T) {
	const path = replicationScope
	e := declaredEndpoint(t, fiber.MethodPost, path)
	if _, wrong := e.Parameters["id"]; wrong {
		t.Fatal("the create body declares a parameter literally named id; checkPathParams refuses that " +
			"on a cluster-scoped route, and Register would have panicked")
	}
	if got := e.Parameters["job_id"].Alias; got != "id" {
		t.Errorf("job_id declares alias %q, want id — the create dialog sends id and would otherwise "+
			"get \"unknown parameter\"", got)
	}

	target := replicationRoute(path)
	for _, spelling := range []string{"id", "job_id"} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeReplicationEndpoint(t, fiber.MethodPost, path, cap))
		body := `{"` + spelling + `":"100-0","type":"local","target":"pve-02","schedule":"*/15"}`
		status, env := send(t, app, jsonRequest(http.MethodPost, target, body))
		if status != fiber.StatusNoContent {
			t.Fatalf("spelling %q: status = %d (%q), want 204", spelling, status, env.Message)
		}
		if got := cap.params.String("job_id"); got != "100-0" {
			t.Errorf("spelling %q reached the handler as job_id=%q", spelling, got)
		}
	}

	// Sending both is a collision Validate reports rather than silently
	// picking a winner.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeReplicationEndpoint(t, fiber.MethodPost, path, cap))
	body := `{"id":"100-0","job_id":"200-0","type":"local","target":"pve-02"}`
	if status, env := send(t, app, jsonRequest(http.MethodPost, target, body)); status != fiber.StatusBadRequest {
		t.Errorf("both spellings at once: status = %d (%q), want 400", status, env.Message)
	}
}

// TestReplicationNodeIsARequiredQueryParameter covers the three routes
// that read ?node= and the refusal they used to write themselves.
//
// The trigger route is a POST, which is what makes the explicit
// Source: query load-bearing: ResolveSource reads an undeclared source
// from the BODY on a mutating verb, so an inferred source would look for
// node in a body the caller never sends and reject every request.
func TestReplicationNodeIsARequiredQueryParameter(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodPost, replicationScope + "/:job_id/trigger"},
		{fiber.MethodGet, replicationScope + "/:job_id/status"},
		{fiber.MethodGet, replicationScope + "/:job_id/log"},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			node := e.Parameters["node"]
			if node.Source != apischema.SourceQuery {
				t.Errorf("node declares source %q, want query", node.Source)
			}
			if node.Optional {
				t.Error("node is optional; the handler refused a missing one")
			}
			if node.Format != "node-name" {
				t.Errorf("node declares format %q, want node-name", node.Format)
			}

			target := replicationRoute(tt.path)
			for _, tc := range []struct {
				query string
				want  int
			}{
				{"?node=pve-01", fiber.StatusNoContent},
				{"", fiber.StatusBadRequest},
				{"?node=", fiber.StatusBadRequest},
				{"?node=a/b", fiber.StatusBadRequest},
				{"?nodes=pve-01", fiber.StatusBadRequest},
			} {
				cap := &capture{}
				app := newRegistryApp(t, noAuth(), probeReplicationEndpoint(t, tt.method, tt.path, cap))
				req := httptest.NewRequest(tt.method, target+tc.query, nil)
				if tt.method == fiber.MethodPost {
					// The trigger button sends no body at all, which is the
					// shape that would break if node were read from one.
					req = jsonRequest(tt.method, target+tc.query, "")
				}
				status, env := send(t, app, req)
				if status != tc.want {
					t.Errorf("%q: status = %d (%q), want %d", tc.query, status, env.Message, tc.want)
					continue
				}
				if tc.want == fiber.StatusNoContent && cap.params.String("node") != "pve-01" {
					t.Errorf("%q: node reached the handler as %q", tc.query, cap.params.String("node"))
				}
			}

			// A node sent in the BODY of the POST is told where it belongs
			// rather than silently ignored.
			if tt.method != fiber.MethodPost {
				return
			}
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeReplicationEndpoint(t, tt.method, tt.path, cap))
			status, env := send(t, app, jsonRequest(tt.method, target, `{"node":"pve-01"}`))
			if status != fiber.StatusBadRequest {
				t.Fatalf("node in the body: status = %d (%q), want 400", status, env.Message)
			}
			if !strings.Contains(env.Message, "query parameter") {
				t.Errorf("message = %q, want it to say the parameter belongs in the query string", env.Message)
			}
		})
	}
}

// TestReplicationLogLimitIsBounded pins the log endpoint's ?limit=,
// including the one spelling it deliberately drops: the handler forwarded
// the limit only when positive, so ?limit=0 used to mean "ignore the
// parameter I just sent". Omitting the key is how a caller asks for the
// default, and that still works.
func TestReplicationLogLimitIsBounded(t *testing.T) {
	const path = replicationScope + "/:job_id/log"
	if got := declaredEndpoint(t, fiber.MethodGet, path).Parameters["limit"].Default; got != 500 {
		t.Errorf("limit default = %#v, want 500 — the value the handler substituted", got)
	}

	target := replicationRoute(path) + "?node=pve-01"
	for _, tt := range []struct {
		query string
		want  int
		value int64
	}{
		{query: "", want: fiber.StatusNoContent, value: 500},
		{query: "&limit=100", want: fiber.StatusNoContent, value: 100},
		{query: "&limit=10000", want: fiber.StatusNoContent, value: 10000},
		{query: "&limit=0", want: fiber.StatusBadRequest},
		{query: "&limit=10001", want: fiber.StatusBadRequest},
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeReplicationEndpoint(t, fiber.MethodGet, path, cap))
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

// TestReplicationEditKeepsTheEmptyStringSentinel is this domain's
// compatibility assertion.
//
// The edit dialog sends schedule and comment UNCONDITIONALLY, so clearing
// either field arrives as "". The client drops an empty string rather than
// forwarding it (UpdateReplicationJob), so "" and "absent" have always
// meant the same thing to Proxmox — and any format on these two would 400
// a save that has always worked.
func TestReplicationEditKeepsTheEmptyStringSentinel(t *testing.T) {
	const path = replicationScope + "/:job_id"
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeReplicationEndpoint(t, fiber.MethodPut, path, cap))
	body := `{"schedule":"","comment":"","disable":0}`
	status, env := send(t, app, jsonRequest(http.MethodPut, replicationRoute(path), body))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — the edit dialog sends these keys empty", status, env.Message)
	}
	for _, key := range []string{"schedule", "comment"} {
		if got := cap.params.String(key); got != "" {
			t.Errorf("%s = %q, want the empty sentinel to survive", key, got)
		}
	}

	// disable stays tri-state: an explicit 0 puts a disabled job back on
	// its schedule, and an omitted one must not.
	if value, supplied := cap.params.OptInt("disable"); !supplied || value != 0 {
		t.Errorf("an explicit disable:0 read back as (%d, supplied=%v)", value, supplied)
	}
	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeReplicationEndpoint(t, fiber.MethodPut, path, cap))
	send(t, app, jsonRequest(http.MethodPut, replicationRoute(path), `{"schedule":"*/30"}`))
	if _, supplied := cap.params.OptInt("disable"); supplied {
		t.Error("disable reads as supplied on a body that omitted it; every edit would re-enable the job")
	}

	// And an empty body is a real request — "change nothing".
	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeReplicationEndpoint(t, fiber.MethodPut, path, cap))
	if status, env := send(t, app, jsonRequest(http.MethodPut, replicationRoute(path), `{}`)); status != fiber.StatusNoContent {
		t.Errorf("empty body: status = %d (%q), want 204", status, env.Message)
	}
}

// TestReplicationCreateRejectsWhatTheHandlerUsedTo pins the two "x is
// required" checks the handler no longer makes, and the job-id shape both
// the create body and the path now share.
func TestReplicationCreateRejectsWhatTheHandlerUsedTo(t *testing.T) {
	const path = replicationScope
	for _, tt := range []struct{ name, body, field string }{
		// Every message below names job_id even when the caller spelled it
		// "id", because Validate reports a parameter by its DECLARED name
		// rather than by the alias the request happened to use. Pinned
		// rather than glossed over: it is the one caller-visible cost of
		// the alias, and the docs name job_id with "id" listed alongside.
		{"no id", `{"type":"local","target":"pve-02"}`, "job_id:"},
		{"empty id", `{"id":"","type":"local","target":"pve-02"}`, "job_id:"},
		{"an id that is not a job id", `{"id":"vm-100","type":"local","target":"pve-02"}`, "job_id:"},
		{"a traversing id", `{"id":"../100-0","type":"local","target":"pve-02"}`, "job_id:"},
		{"no target", `{"id":"100-0","type":"local"}`, "target:"},
		{"empty target", `{"id":"100-0","type":"local","target":""}`, "target:"},
		{"a target that is not a node", `{"id":"100-0","target":"a/b"}`, "target:"},
		{"a misspelled key", `{"id":"100-0","target":"pve-02","sched":"*/15"}`, "sched:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeReplicationEndpoint(t, fiber.MethodPost, path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, replicationRoute(path), tt.body))
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

	// An explicit empty type is refused by name rather than forwarded. A
	// Default does NOT apply to "": apischema treats the empty string as a
	// value the caller supplied, so without a MinLength it would reach
	// Proxmox and come back as a 400 naming no field.
	cap0 := &capture{}
	app0 := newRegistryApp(t, noAuth(), probeReplicationEndpoint(t, fiber.MethodPost, path, cap0))
	status0, env0 := send(t, app0, jsonRequest(http.MethodPost, replicationRoute(path),
		`{"id":"100-0","type":"","target":"pve-02"}`))
	if status0 != fiber.StatusBadRequest {
		t.Errorf("empty type: status = %d (%q), want 400", status0, env0.Message)
	}
	if !strings.HasPrefix(env0.Message, "type:") {
		t.Errorf("empty type: message = %q, want it to name type", env0.Message)
	}

	// An omitted type still means local — the substitution the handler made
	// is now the schema's Default, and the docs say so.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeReplicationEndpoint(t, fiber.MethodPost, path, cap))
	body := `{"id":"100-0","target":"pve-02"}`
	if status, env := send(t, app, jsonRequest(http.MethodPost, replicationRoute(path), body)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.String("type"); got != "local" {
		t.Errorf("type = %q, want local", got)
	}
	if cap.params.Has("type") {
		t.Error("type reads as supplied; a default is not something the caller sent")
	}
}

// TestReplicationJobIDCreateAndPathAgree pins that a job this API can
// create is a job this API can address. Two independent patterns would
// drift, and the direction that bites is a create rule looser than the
// path rule: it produces a job the operator then cannot edit or delete.
func TestReplicationJobIDCreateAndPathAgree(t *testing.T) {
	create := declaredEndpoint(t, fiber.MethodPost, replicationScope).Parameters["job_id"]
	path := declaredEndpoint(t, fiber.MethodDelete, replicationScope+"/:job_id").Parameters["job_id"]
	if create.Pattern != path.Pattern {
		t.Errorf("create accepts %q but the path accepts %q", create.Pattern, path.Pattern)
	}
	if create.MaxLength == nil || path.MaxLength == nil || *create.MaxLength != *path.MaxLength {
		t.Errorf("create caps the id at %v and the path at %v", create.MaxLength, path.MaxLength)
	}
	// The path parameter must NOT carry the alias: an alias on a path
	// parameter is silently dead, which checkPathParams refuses outright.
	if path.Alias != "" {
		t.Errorf("the path parameter declares alias %q; a URL cannot carry a second spelling of a segment",
			path.Alias)
	}
}

// TestReplicationRouteIsGatedByItsDeclaration proves the permission the
// declaration states is the permission the route enforces, end to end.
//
// It is run against the job delete, which is the most consequential of the
// eight: a deleted job stops replicating silently, and the target keeps
// whatever stale copy it last received.
func TestReplicationRouteIsGatedByItsDeclaration(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, replicationScope+"/:job_id")
	if e.Permissions.Describe() != "manage:replication" {
		t.Fatalf("the job delete declares %q, want manage:replication", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := replicationRoute(replicationScope + "/:job_id")

	t.Run("a caller holding only view:replication is refused", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:replication": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodDelete, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding manage:replication gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:replication": true}), gated)
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
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:replication": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestEveryReplicationEndpointIsDocumented holds the declarations to the
// standard that makes this whole effort worth doing.
func TestEveryReplicationEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredReplicationEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Replication" {
			t.Errorf("%s is in group %q, want Replication", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
