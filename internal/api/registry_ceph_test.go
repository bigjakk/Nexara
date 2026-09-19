package api

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to registry_ceph.go
// that quietly loosened a parameter would show up here.

// cephRouteCount is how many endpoints registerCephEndpoints declares.
// See vmRouteCount in registry_vms_test.go for why the registry total is a
// sum of per-domain constants rather than one number.
const cephRouteCount = 17

// cephRoute renders one Ceph route's path with the test ids substituted,
// so a test can name the route the way the declaration does and still send
// a real request at it.
func cephRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":osd_id", "3",
		":pool_name", "store01",
	).Replace(path)
}

// cephLegacyPermissions is the permission each Ceph route was gated on
// BEFORE Phase 6b, transcribed from internal/api/handlers/ceph.go and
// internal/api/handlers/ceph_osd.go at commit 13ceac0.
//
// The tally is 17 routes to FOURTEEN requireClusterPerm calls, and the
// gap is the thing worth checking rather than a discrepancy to wave away:
// eleven handlers carried their own call, and the five OSD lifecycle
// routes shared two — osdMembershipAction for in/out and osdDaemonAction
// for start/stop/restart. Both took the action as an argument, so a
// reader had to confirm the permission did NOT vary with it before the
// check could be hoisted into middleware. It did not: all five are
// manage:ceph.
//
// Both directions are compared below — a route in this table with no
// declaration, and a declared Ceph route missing from this table, are each
// a failure — so neither list can quietly drift away from the other.
var cephLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/ceph/status":                 "view:ceph",
	"GET /api/v1/clusters/:cluster_id/ceph/osds":                   "view:ceph",
	"GET /api/v1/clusters/:cluster_id/ceph/pools":                  "view:ceph",
	"GET /api/v1/clusters/:cluster_id/ceph/monitors":               "view:ceph",
	"GET /api/v1/clusters/:cluster_id/ceph/fs":                     "view:ceph",
	"GET /api/v1/clusters/:cluster_id/ceph/rules":                  "view:ceph",
	"POST /api/v1/clusters/:cluster_id/ceph/pools":                 "manage:ceph",
	"DELETE /api/v1/clusters/:cluster_id/ceph/pools/:pool_name":    "manage:ceph",
	"GET /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/preflight": "view:ceph",
	"POST /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/in":       "manage:ceph",
	"POST /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/out":      "manage:ceph",
	"POST /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/start":    "manage:ceph",
	"POST /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/stop":     "manage:ceph",
	"POST /api/v1/clusters/:cluster_id/ceph/osds/:osd_id/restart":  "manage:ceph",
	"GET /api/v1/clusters/:cluster_id/ceph/metrics":                "view:ceph",
	"GET /api/v1/clusters/:cluster_id/ceph/osds/metrics":           "view:ceph",
	"GET /api/v1/clusters/:cluster_id/ceph/pools/metrics":          "view:ceph",
}

// declaredCephEndpoints returns every declaration whose path is under the
// /ceph prefix, keyed "METHOD path".
func declaredCephEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, cephScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestCephRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes deleting 14 requireClusterPerm calls a refactor rather than a
// change.
func TestCephRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredCephEndpoints(t)
	if len(declared) != cephRouteCount {
		t.Fatalf("the registry declares %d Ceph routes, want %d", len(declared), cephRouteCount)
	}
	if len(cephLegacyPermissions) != cephRouteCount {
		t.Fatalf("cephLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(cephLegacyPermissions), cephRouteCount)
	}

	for key, want := range cephLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; every Ceph route's permission is statically "+
				"known, the five OSD lifecycle routes included — their shared helpers checked manage:ceph "+
				"whichever action they were handed", key, e.Permissions.Describe())
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

	for key := range declared {
		if _, listed := cephLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in cephLegacyPermissions — a new Ceph route must be "+
				"added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestCephOSDLifecycleRoutesAllGateOnManage is the specific claim that let
// the two shared helpers' checks be hoisted: the permission did not vary
// with the action they were handed.
//
// Asserted route by route rather than inferred from the table above,
// because this is the one place the migration had to reason about a check
// that lived somewhere other than the handler the route names.
func TestCephOSDLifecycleRoutesAllGateOnManage(t *testing.T) {
	for _, action := range handlers.CephOSDActions {
		key := fiber.MethodPost + " " + cephScope + "/osds/:osd_id/" + action
		e, ok := declaredCephEndpoints(t)[key]
		if !ok {
			t.Errorf("%s is not declared, but %q is in handlers.CephOSDActions — the enum and the "+
				"routes have to name the same five actions", key, action)
			continue
		}
		if got := e.Permissions.Describe(); got != "manage:ceph" {
			t.Errorf("%s declares %q, want manage:ceph", key, got)
		}
	}
}

// TestCephRoutesDeclareEveryPathParameter re-states checkPathParams' rule
// for this domain and adds the one it does NOT enforce: that :cluster_id
// is the FIRST placeholder, which namesACluster requires of a
// cluster-scoped Check.
func TestCephRoutesDeclareEveryPathParameter(t *testing.T) {
	for key, e := range declaredCephEndpoints(t) {
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
	}
}

// TestCephOSDIDIsANumber pins that :osd_id is declared as a bounded
// integer rather than a string.
//
// The handlers read it with p.Int and narrow the result to `int`, so a
// string declaration would hand strconv whatever the URL carried — which
// is what osdIDFromParam used to do, reporting its own 400. checkPathParams
// only requires that an entry EXIST for a :param; it says nothing about
// its type, so this is the assertion that keeps the schema doing the job
// the deleted helper did.
func TestCephOSDIDIsANumber(t *testing.T) {
	for key, e := range declaredCephEndpoints(t) {
		if !strings.Contains(e.Path, "/:osd_id") {
			continue
		}
		prop := e.Parameters["osd_id"]
		if prop.Type != "integer" {
			t.Errorf("%s declares osd_id as %q, want integer", key, prop.Type)
		}
		if prop.Minimum == nil || *prop.Minimum != 0 {
			t.Errorf("%s declares osd_id with minimum %v, want 0 — a negative OSD id named nothing, "+
				"and osdIDFromParam refused one", key, prop.Minimum)
		}
		if prop.Maximum == nil {
			t.Errorf("%s declares osd_id with no maximum; the handler narrows it to an int", key)
		}
	}

	// And the refusal is real, on a route that would otherwise reach a
	// Proxmox call.
	const path = cephScope + "/osds/:osd_id/preflight"
	for _, osd := range []string{"-1", "osd.3", "3.5", ""} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCephEndpoint(t, fiber.MethodGet, path, cap))
		target := strings.NewReplacer(":cluster_id", testClusterID, ":osd_id", osd).Replace(path)
		if status, env := send(t, app, httptest.NewRequest(http.MethodGet, target, nil)); status == fiber.StatusNoContent {
			t.Errorf("osd_id %q was accepted (%q); the schema has to refuse it", osd, env.Message)
		}
		if cap.called {
			t.Errorf("osd_id %q reached the handler", osd)
		}
	}
}

// probeCephEndpoint is a declared Ceph endpoint with its handler swapped
// for a capture, so a test can see exactly what the schema handed over
// without needing the handler's database and Proxmox client.
func probeCephEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	// The real declaration gates on a Ceph permission; these tests are
	// about the parameters, and the gate is exercised by
	// TestCephRouteIsGatedByItsDeclaration below.
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestCephPreflightActionEnumMatchesTheHandler pins that the declared
// action vocabulary IS handlers.CephOSDActions rather than a hand-written
// literal beside it, and that its default is the one the handler applied.
//
// ?action= is the first query parameter any migrated route declares, and
// it is the one the handler USED to read with c.Query("action", "out")
// followed by its own membership check. Both are now the schema's, so both
// are asserted here end to end rather than by reading the declaration.
func TestCephPreflightActionEnumMatchesTheHandler(t *testing.T) {
	const path = cephScope + "/osds/:osd_id/preflight"
	e := declaredEndpoint(t, fiber.MethodGet, path)
	action := e.Parameters["action"]
	if !slices.Equal(action.Enum, handlers.CephOSDActions) {
		t.Errorf("action enum = %v, want handlers.CephOSDActions %v", action.Enum, handlers.CephOSDActions)
	}
	if !action.Optional {
		t.Error("action is required; the handler defaulted a missing ?action= to out")
	}
	if action.Default != "out" {
		t.Errorf("action default = %#v, want \"out\" — the value c.Query(\"action\", \"out\") supplied", action.Default)
	}

	target := cephRoute(path)
	for _, tt := range []struct {
		query string
		want  int
		// action is what the handler must have been handed, checked only
		// on the requests that reach it.
		action string
	}{
		{query: "", want: fiber.StatusNoContent, action: "out"},
		{query: "?action=out", want: fiber.StatusNoContent, action: "out"},
		{query: "?action=stop", want: fiber.StatusNoContent, action: "stop"},
		{query: "?action=restart", want: fiber.StatusNoContent, action: "restart"},
		{query: "?action=destroy", want: fiber.StatusBadRequest},
		{query: "?action=OUT", want: fiber.StatusBadRequest},
		{query: "?action=", want: fiber.StatusBadRequest},
		// PVE's additionalProperties => 0: a misspelled query key used to
		// be silently ignored, leaving the caller with the default.
		{query: "?actoin=stop", want: fiber.StatusBadRequest},
	} {
		t.Run("q"+tt.query, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeCephEndpoint(t, fiber.MethodGet, path, cap))
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
			if got := cap.params.String("action"); got != tt.action {
				t.Errorf("action = %q, want %q", got, tt.action)
			}
			if got := cap.params.Int("osd_id"); got != 3 {
				t.Errorf("osd_id = %d, want 3", got)
			}
		})
	}
}

// TestCephMetricsTimeframeEnumMatchesTheTable pins the same three things
// for ?timeframe=: the vocabulary is handlers.CephMetricsTimeframes, the
// default is the constant cephMetricsWindow falls back to, and an
// unrecognised value is now REFUSED rather than silently served as 1h.
//
// That last one is a deliberate behaviour change and the reason this test
// spells it out: the handler used to hand anything at all to
// cephMetricsWindow, which returned the default window for a miss — so
// ?timeframe=7days quietly answered with an hour of data and said nothing.
func TestCephMetricsTimeframeEnumMatchesTheTable(t *testing.T) {
	const path = cephScope + "/metrics"
	e := declaredEndpoint(t, fiber.MethodGet, path)
	tf := e.Parameters["timeframe"]
	if !slices.Equal(tf.Enum, handlers.CephMetricsTimeframes) {
		t.Errorf("timeframe enum = %v, want handlers.CephMetricsTimeframes %v", tf.Enum, handlers.CephMetricsTimeframes)
	}
	if tf.Default != handlers.CephMetricsDefaultTimeframe {
		t.Errorf("timeframe default = %#v, want %q", tf.Default, handlers.CephMetricsDefaultTimeframe)
	}

	target := cephRoute(path)
	for _, tt := range []struct {
		query string
		want  int
		value string
	}{
		{query: "", want: fiber.StatusNoContent, value: handlers.CephMetricsDefaultTimeframe},
		{query: "?timeframe=7d", want: fiber.StatusNoContent, value: "7d"},
		{query: "?timeframe=7days", want: fiber.StatusBadRequest},
		{query: "?timeframe=", want: fiber.StatusBadRequest},
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCephEndpoint(t, fiber.MethodGet, path, cap))
		status, env := send(t, app, httptest.NewRequest(http.MethodGet, target+tt.query, nil))
		if status != tt.want {
			t.Errorf("%q: status = %d (%q), want %d", tt.query, status, env.Message, tt.want)
			continue
		}
		if tt.want == fiber.StatusNoContent && cap.params.String("timeframe") != tt.value {
			t.Errorf("%q: timeframe = %q, want %q", tt.query, cap.params.String("timeframe"), tt.value)
		}
	}
}

// TestCreateCephPoolBody covers what the deleted createPoolRequest struct
// and its three "x is required" checks used to do, plus the parameter set
// they never closed.
//
// The pool dialog sends exactly three keys (name, size, pg_num) and omits
// the other four entirely rather than sending them empty, so — unlike the
// container create body — there is no empty-string sentinel here to
// preserve. That is worth pinning: it is why `name` can carry a real
// pattern.
func TestCreateCephPoolBody(t *testing.T) {
	const path = cephScope + "/pools"
	target := cephRoute(path)

	t.Run("the dialog's body is accepted", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCephEndpoint(t, fiber.MethodPost, path, cap))
		body := `{"name":"store01","size":3,"pg_num":128}`
		if status, env := send(t, app, jsonRequest(http.MethodPost, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		// The four keys the dialog omits carry no default, so the handler
		// forwards their zero values and CreateCephPool drops them — the
		// behaviour "" and "absent" have always shared.
		for _, key := range []string{"application", "crush_rule_name", "pg_autoscale_mode"} {
			if cap.params.Has(key) {
				t.Errorf("%s reads as supplied, but the request did not send it", key)
			}
		}
		if cap.params.Has("min_size") {
			t.Error("min_size reads as supplied, but the request did not send it")
		}
	})

	t.Run("a full body is accepted", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCephEndpoint(t, fiber.MethodPost, path, cap))
		body := `{"name":".mgr","size":3,"min_size":2,"pg_num":32,` +
			`"application":"rbd","crush_rule_name":"replicated_rule","pg_autoscale_mode":"on"}`
		if status, env := send(t, app, jsonRequest(http.MethodPost, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got := cap.params.String("name"); got != ".mgr" {
			t.Errorf("name = %q — Ceph's own internal pools are dot-prefixed and have to survive", got)
		}
		if got := cap.params.String("crush_rule_name"); got != "replicated_rule" {
			t.Errorf("crush_rule_name = %q; the wire name is Nexara's, not PVE's crush_rule", got)
		}
	})

	for _, tt := range []struct{ name, body, field string }{
		{"missing name", `{"size":3,"pg_num":128}`, "name:"},
		{"empty name", `{"name":"","size":3,"pg_num":128}`, "name:"},
		{"traversing name", `{"name":"..","size":3,"pg_num":128}`, "name:"},
		{"missing size", `{"name":"store01","pg_num":128}`, "size:"},
		{"zero size", `{"name":"store01","size":0,"pg_num":128}`, "size:"},
		{"negative size", `{"name":"store01","size":-1,"pg_num":128}`, "size:"},
		{"missing pg_num", `{"name":"store01","size":3}`, "pg_num:"},
		{"zero pg_num", `{"name":"store01","size":3,"pg_num":0}`, "pg_num:"},
		// PVE's additionalProperties => 0. "crush_rule" is PVE's own
		// spelling, and sending it here used to be silently dropped — the
		// exact confusion the client's crush_rule_name comment describes.
		{"PVE's spelling of the rule", `{"name":"store01","size":3,"pg_num":128,"crush_rule":"r"}`, "crush_rule:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeCephEndpoint(t, fiber.MethodPost, path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, target, tt.body))
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

// TestCephPoolNameParamAndBodyAgree pins that a pool this API can create
// is a pool this API can delete. Two independent patterns would drift, and
// the direction that bites is a create rule looser than the delete rule:
// it produces a pool the operator then cannot remove.
func TestCephPoolNameParamAndBodyAgree(t *testing.T) {
	create := declaredEndpoint(t, fiber.MethodPost, cephScope+"/pools").Parameters["name"]
	del := declaredEndpoint(t, fiber.MethodDelete, cephScope+"/pools/:pool_name").Parameters["pool_name"]
	if create.Pattern != del.Pattern {
		t.Errorf("create accepts %q but delete accepts %q; a pool created here has to stay deletable",
			create.Pattern, del.Pattern)
	}
	if create.MaxLength == nil || del.MaxLength == nil || *create.MaxLength != *del.MaxLength {
		t.Errorf("create caps the name at %v and delete at %v", create.MaxLength, del.MaxLength)
	}
}

// TestCephPoolNameSurvivesThePathSegment drives the names PVE's own rule
// admits at the DELETE route as real requests, and checks the handler is
// handed the name the caller sent.
//
// The catalogue's witnesses already prove the regex takes these, but the
// regex is only the first thing standing between the caller and Proxmox:
// Fiber has to route a path segment containing the character, and it has to
// hand the handler the same bytes back. A rule that admits a name the
// router cannot carry would be a fix on paper only — the pool would still
// be undeletable, just with a different status code.
//
// Every name here is one PVE accepts and this API used to answer 400 to:
// the pattern was an invented ^\.?[A-Za-z0-9][A-Za-z0-9._-]*$, so a pool
// called "rbd+meta" could be neither read, edited nor destroyed through
// Nexara. ".mgr" is included as the regression the leading-dot allowance
// was added for.
//
// The names are sent RAW rather than through url.PathEscape, because that
// is what a client has to do for them to arrive intact: Fiber runs with
// UnescapePath at its default of false, so c.Params hands back the segment
// exactly as it came in (the same fact firewallIPSetEntryCIDRParam is built
// around). Every character used here is legal unencoded in a path segment
// — RFC 3986 sub-delims and unreserved.
//
// The characters that are NOT — "#", "%", "?" and a space, which a client
// must percent-encode — therefore arrive still encoded and are then escaped
// a second time by DeleteCephPool's url.PathEscape, so Proxmox is asked for
// a pool whose name literally contains "%23". That is the same pre-existing
// double-encoding defect firewallIPSetEntryCIDRParam records on the IP set
// route, and it lives in the client rather than in this rule.
//
// Widening did not CAUSE it, but it does widen its REACH, and that is worth
// stating plainly rather than filed under "pre-existing": before, a pool
// named with one of those characters could only exist if something outside
// Nexara had created it, so the defect needed a pre-existing pool to bite
// on. Now POST /ceph/pools will create one, and the DELETE that follows
// addresses the wrong name. Left for separate scoping; not asserted here,
// because asserting it would freeze the defect as the spec.
func TestCephPoolNameSurvivesThePathSegment(t *testing.T) {
	const path = cephScope + "/pools/:pool_name"
	prefix := strings.Replace(cephRoute(path), "store01", "", 1)

	for _, name := range []string{
		"rbd+meta", "pool!1", "pool'1", "a.b~c", "-pool", ".mgr",
		"a$b", "a,b", "a;b", "a=b", "a@b", "a(b)", "rbd",
	} {
		t.Run(name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeCephEndpoint(t, fiber.MethodDelete, path, cap))
			target := prefix + name

			status, env := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
			if status != fiber.StatusNoContent {
				t.Fatalf("DELETE %s: status = %d (%q), want 204 — PVE accepts this pool name, so "+
					"refusing it here makes the pool undeletable through this API", target, status, env.Message)
			}
			if got := cap.params.String("pool_name"); got != name {
				t.Errorf("handler was handed %q, want %q: the value did not survive the round trip "+
					"through the path segment, so the wrong pool would be destroyed", got, name)
			}
		})
	}
}

// TestCephPoolDeleteStillRefusesATraversingName is the other half: the one
// respect in which this rule is deliberately STRICTER than PVE's.
//
// PVE's own pattern (^[^:/\s]+$) admits "." and "..", and
// proxmox.DeleteCephPool concatenates the name into a Proxmox path — ".."
// pops the pool collection and lands DELETE on /nodes/{node}/ceph, "."
// stops a level short on /ceph/pool. RE2 has no negative lookahead, so the
// catalogue's rule excludes them positively, by requiring one character
// that is not a dot. That also excludes "...", which upstream would take
// and which names nothing.
//
// The segment is sent RAW, and that is the case that matters: nothing
// between the client and the router collapses a dot segment, so ".."
// really does arrive as ".." and the pattern is the thing that stops it.
// Sent percent-encoded it arrives as the literal text "%2E%2E" — a
// different string, which this rule accepts and which is NOT a traversal,
// because url.PathEscape re-escapes the percent on the way out and Proxmox
// is asked for a pool named "%2E%2E".
func TestCephPoolDeleteStillRefusesATraversingName(t *testing.T) {
	const path = cephScope + "/pools/:pool_name"
	prefix := strings.Replace(cephRoute(path), "store01", "", 1)

	for _, name := range []string{".", "..", "...", "...."} {
		t.Run(name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeCephEndpoint(t, fiber.MethodDelete, path, cap))
			target := prefix + name

			status, _ := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
			if status == fiber.StatusNoContent {
				t.Fatalf("DELETE %s was accepted; a name made only of dots is a traversal segment "+
					"once DeleteCephPool concatenates it into a Proxmox path", target)
			}
			if cap.called {
				t.Error("the handler ran for a name the schema must refuse")
			}
		})
	}
}

// TestCephPoolNameRefusesABackslashOnBothHalves pins the second deliberate
// divergence from PVE's pattern, and it guards a REGRESSION rather than a
// hypothetical.
//
// The two client methods do not check the name the same way.
// proxmox.DeleteCephPool runs validatePathSegment (internal/proxmox/client.go),
// which refuses "/" AND "\". proxmox.CreateCephPool runs no such check — it
// only refuses an empty name — because the name travels in the FORM BODY
// rather than in a path, so it needs no traversal guard.
//
// That asymmetry means a rule admitting a backslash is not merely lax, it is
// productive of unaddressable state: POST {"name":"a\\b"} answers 204 and the
// pool is created on the cluster, and every DELETE of it afterwards answers
// 400 `ceph pool name "a\b" must not contain a path separator`. Nexara mints a
// pool Nexara can never remove — the same failure the widening of this rule
// set out to close, arrived at from the create side instead of the delete
// side.
//
// So BOTH halves are asserted, and the create half is the one that matters:
// a rule that refused the backslash only on the delete path would leave
// exactly the bug above in place.
//
// The two halves are driven DIFFERENTLY, and the reason is worth recording
// because it looks like an inconsistency. The create half goes through the
// real router, because a JSON string body carries a literal backslash
// unchanged. The delete half validates the declaration directly, because
// app.Test serialises the request through net/url, which percent-encodes a
// raw backslash in a path — "a\b" arrives at the handler as "a%5Cb", a
// different string that this rule rightly accepts. That is a property of
// the TEST HARNESS, not of the system: nothing in HTTP stops a hand-built
// request putting a raw 0x5C byte in the request line, so the exclusion is
// still load-bearing on the path and is asserted where it can actually be
// observed.
func TestCephPoolNameRefusesABackslashOnBothHalves(t *testing.T) {
	names := []string{`a\b`, `\pool`, `pool\`, `..\..`}

	t.Run("create body", func(t *testing.T) {
		const path = cephScope + "/pools"
		for _, name := range names {
			t.Run(name, func(t *testing.T) {
				cap := &capture{}
				app := newRegistryApp(t, noAuth(), probeCephEndpoint(t, fiber.MethodPost, path, cap))
				// Go's %q escapes the backslash, which is also JSON's escape.
				body := fmt.Sprintf(`{"name":%q,"size":3,"pg_num":128}`, name)

				status, env := send(t, app, jsonRequest(http.MethodPost, cephRoute(path), body))
				if status != fiber.StatusBadRequest {
					t.Fatalf("POST name=%q: status = %d (%q), want 400 — CreateCephPool does not run "+
						"validatePathSegment, so a pool created under this name could never be deleted",
						name, status, env.Message)
				}
				if cap.called {
					t.Error("the handler ran for a name that would create an undeletable pool")
				}
			})
		}
	})

	t.Run("delete path", func(t *testing.T) {
		e := declaredEndpoint(t, fiber.MethodDelete, cephScope+"/pools/:pool_name")
		for _, name := range names {
			t.Run(name, func(t *testing.T) {
				_, err := e.Parameters.Validate(map[string]any{
					"cluster_id": testClusterID,
					"pool_name":  name,
				})
				if err == nil {
					t.Errorf("pool_name=%q was accepted; validatePathSegment refuses it one layer down, "+
						"so the schema should answer first and name the parameter", name)
				}
			})
		}
	})
}

// TestCephOSDActionsTakeNoBody covers the shape the OSD action dialog
// sends: an explicit `{}`. bodyValues decodes a mutating verb's body
// whether or not the schema declares one, precisely so an undeclared
// payload is refused — and "{}" has to stay on the right side of that
// line, because it carries nothing to refuse.
func TestCephOSDActionsTakeNoBody(t *testing.T) {
	for _, action := range handlers.CephOSDActions {
		path := cephScope + "/osds/:osd_id/" + action
		if got := len(declaredEndpoint(t, fiber.MethodPost, path).Parameters); got != 2 {
			t.Errorf("%s declares %d parameters, want just the two path ids", action, got)
		}
		for _, body := range []string{`{}`, ``} {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeCephEndpoint(t, fiber.MethodPost, path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, cephRoute(path), body))
			if status != fiber.StatusNoContent {
				t.Errorf("%s body %q: status = %d (%q), want 204", action, body, status, env.Message)
			}
		}
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeCephEndpoint(t, fiber.MethodPost, path, cap))
		if status, _ := send(t, app, jsonRequest(http.MethodPost, cephRoute(path), `{"force":true}`)); status != fiber.StatusBadRequest {
			t.Errorf("%s: status = %d for an undeclared parameter, want 400", action, status)
		}
	}
}

// TestCephRouteIsGatedByItsDeclaration proves the permission the
// declaration states is the permission the route enforces, end to end, on
// a REAL endpoint rather than a synthetic one.
//
// It is run against pool delete because that is the most consequential of
// the 17 — it destroys every object in the pool — and because it is one of
// the routes whose inline check was removed: if the declaration and the
// middleware ever disagreed, every guard in this package would still pass
// (they read the declaration) and the route would ship authenticated but
// ungated.
func TestCephRouteIsGatedByItsDeclaration(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, cephScope+"/pools/:pool_name")
	if e.Permissions.Describe() != "manage:ceph" {
		t.Fatalf("pool delete declares %q, want manage:ceph", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := cephRoute(cephScope + "/pools/:pool_name")

	t.Run("a caller holding only view:ceph is refused", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:ceph": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodDelete, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding manage:ceph gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:ceph": true}), gated)
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
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:ceph": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestOSDLifecycleRoutesAreGatedByTheirDeclaration is the same end-to-end
// proof for the five routes whose check did NOT live in the handler the
// route names. Hoisting a delegated check is the one move in this domain
// that could have dropped a gate without any declaration looking wrong.
func TestOSDLifecycleRoutesAreGatedByTheirDeclaration(t *testing.T) {
	for _, action := range handlers.CephOSDActions {
		t.Run(action, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodPost, cephScope+"/osds/:osd_id/"+action)
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := cephRoute(e.Path)

			app := newRegistryApp(t, stubAuth(map[string]bool{"view:ceph": true}), gated)
			if status, _ := send(t, app, authedJSON(http.MethodPost, target, `{}`)); status != fiber.StatusForbidden {
				t.Errorf("view:ceph got status %d, want 403", status)
			}
			if cap.called {
				t.Fatal("the handler ran for a caller holding only view:ceph")
			}

			app = newRegistryApp(t, stubAuth(map[string]bool{"manage:ceph": true}), gated)
			if status, env := send(t, app, authedJSON(http.MethodPost, target, `{}`)); status != fiber.StatusNoContent {
				t.Errorf("manage:ceph got status %d (%q), want 204", status, env.Message)
			}
			if !cap.called {
				t.Error("the handler did not run for a caller holding manage:ceph")
			}
		})
	}
}

// authedJSON is authedRequest with a JSON body, for the POST routes whose
// gate has to be exercised with the `{}` their dialog sends. The header is
// the one stubAuth reads, not a real bearer token.
func authedJSON(method, target, body string) *http.Request {
	req := jsonRequest(method, target, body)
	req.Header.Set("X-Test-User", "yes")
	return req
}

// TestEveryCephEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing: every route and every
// parameter says what it is for, because the declaration IS the
// documentation and 319 endpoints in this API still say nothing.
//
// Only two of the four checks below can actually fire — Register panics on
// a blank Description, a blank Group or a schema that fails Compile, so
// those three have necessarily passed by the time declaredCephEndpoints
// returns. The two that bite are the Group value (Register requires A
// group, not THIS one) and the per-parameter Description, which Compile
// does not look at at all.
func TestEveryCephEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredCephEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Ceph" {
			t.Errorf("%s is in group %q, want Ceph", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}

// TestCephDeclarationsShareNoEnumBackingArray is the Ceph half of the
// assertion the VM status route's comment makes: Property.Enum is only
// deep-copied on the StdOption path, so a declaration that aliased the
// package-level slice would give every Server's schema the same backing
// array — and a handler that sorted or appended to one would edit them
// all.
func TestCephDeclarationsShareNoEnumBackingArray(t *testing.T) {
	first := declaredEndpoint(t, fiber.MethodGet, cephScope+"/osds/:osd_id/preflight").Parameters["action"].Enum
	if len(first) == 0 {
		t.Fatal("the pre-flight route declares no action enum")
	}
	if &first[0] == &handlers.CephOSDActions[0] {
		t.Error("the declared enum aliases handlers.CephOSDActions; clone it")
	}

	tf := declaredEndpoint(t, fiber.MethodGet, cephScope+"/metrics").Parameters["timeframe"].Enum
	if len(tf) == 0 {
		t.Fatal("the metrics route declares no timeframe enum")
	}
	if &tf[0] == &handlers.CephMetricsTimeframes[0] {
		t.Error("the declared enum aliases handlers.CephMetricsTimeframes; clone it")
	}
}

// TestCephRouteKeysAreUnique is cheap insurance against the copy-paste
// this file's two `for` loops invite: five OSD routes and six read routes
// are each declared from a table, and a duplicated suffix would register
// the same path twice. Register panics on that, but the panic names the
// path rather than the table, so this says which list to look in.
func TestCephRouteKeysAreUnique(t *testing.T) {
	if got := len(declaredCephEndpoints(t)); got != len(slices.Sorted(maps.Keys(cephLegacyPermissions))) {
		t.Errorf("the registry declares %d distinct Ceph route keys, the tally names %d", got, len(cephLegacyPermissions))
	}
}
