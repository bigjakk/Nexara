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
// rather than a fixture shaped like them, so a change to registry_ha.go
// that quietly loosened a parameter would show up here.

// haRouteCount is how many endpoints registerHAEndpoints declares. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum
// of per-domain constants rather than one number.
const haRouteCount = 17

// haRoute renders one HA route's path with the test ids substituted, so a
// test can name the route the way the declaration does and still send a
// real request at it.
//
// :sid is rendered PERCENT-ENCODED, which is what the frontend sends and
// therefore what the pattern has to accept — see haSIDParam.
func haRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":sid", "vm%3A109",
		":group", "ha-group01",
		":rule", "ha-rule01",
	).Replace(path)
}

// haLegacyPermissions is the permission each HA handler checked with a
// hand-placed requireClusterPerm call BEFORE Phase 6b, transcribed from
// internal/api/handlers/ha.go at commit 13ceac0 (17 handlers, 17 calls,
// every one of them cluster-scoped on the "ha" resource).
//
// It exists so the migration is verifiable rather than asserted: the check
// moved from the handler body into route middleware, and the only thing
// that makes that safe is the two being the same check. Both directions
// are compared below.
var haLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/ha/resources":         "view:ha",
	"POST /api/v1/clusters/:cluster_id/ha/resources":        "manage:ha",
	"GET /api/v1/clusters/:cluster_id/ha/resources/:sid":    "view:ha",
	"PUT /api/v1/clusters/:cluster_id/ha/resources/:sid":    "manage:ha",
	"DELETE /api/v1/clusters/:cluster_id/ha/resources/:sid": "manage:ha",
	"GET /api/v1/clusters/:cluster_id/ha/groups":            "view:ha",
	"POST /api/v1/clusters/:cluster_id/ha/groups":           "manage:ha",
	"PUT /api/v1/clusters/:cluster_id/ha/groups/:group":     "manage:ha",
	"DELETE /api/v1/clusters/:cluster_id/ha/groups/:group":  "manage:ha",
	"GET /api/v1/clusters/:cluster_id/ha/status":            "view:ha",
	"GET /api/v1/clusters/:cluster_id/ha/rules":             "view:ha",
	"POST /api/v1/clusters/:cluster_id/ha/rules":            "manage:ha",
	"PUT /api/v1/clusters/:cluster_id/ha/rules/:rule":       "manage:ha",
	"DELETE /api/v1/clusters/:cluster_id/ha/rules/:rule":    "manage:ha",
	"GET /api/v1/clusters/:cluster_id/ha/manager-status":    "view:ha",
	"POST /api/v1/clusters/:cluster_id/ha/arm":              "manage:ha",
	"POST /api/v1/clusters/:cluster_id/ha/disarm":           "manage:ha",
}

// declaredHAEndpoints returns every declaration whose path is under the
// /ha prefix, keyed "METHOD path".
func declaredHAEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, haScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestHARoutesDeclareTheSamePermissionTheyEnforced is the tally that makes
// deleting 17 requireClusterPerm calls a refactor rather than a change.
func TestHARoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredHAEndpoints(t)
	if len(declared) != haRouteCount {
		t.Fatalf("the registry declares %d HA routes, want %d", len(declared), haRouteCount)
	}
	if len(haLegacyPermissions) != haRouteCount {
		t.Fatalf("haLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(haLegacyPermissions), haRouteCount)
	}

	var view, manage int
	for key, want := range haLegacyPermissions {
		switch want {
		case "view:ha":
			view++
		case "manage:ha":
			manage++
		}
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; every HA route's permission is statically known",
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
	// The split is stated rather than derived so a route silently changing
	// side — a read becoming a write, or worse the reverse — shows up as a
	// count, not only as one line of the loop above.
	if view != 6 || manage != 11 {
		t.Errorf("the tally splits %d view:ha / %d manage:ha, want 6 / 11", view, manage)
	}

	for key := range declared {
		if _, listed := haLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in haLegacyPermissions — a new HA route must be added "+
				"to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestHARoutesDeclareEveryPathParameter re-states checkPathParams' rule
// for this domain and adds the one it does NOT enforce: that :cluster_id
// is the FIRST placeholder, which namesACluster requires of a
// cluster-scoped Check.
func TestHARoutesDeclareEveryPathParameter(t *testing.T) {
	for key, e := range declaredHAEndpoints(t) {
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
			if prop.Pattern == "" && prop.Format == "" {
				t.Errorf("%s declares %q with neither a pattern nor a format; the client builds its "+
					"Proxmox path by concatenating this value", key, name)
			}
		}
	}
}

// probeHAEndpoint is a declared HA endpoint with its handler swapped for a
// capture, so a test can see exactly what the schema handed over without
// needing the handler's database and Proxmox client.
func probeHAEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	// The real declaration gates on an HA permission; these tests are about
	// the parameters, and the gate is exercised by
	// TestHARouteIsGatedByItsDeclaration below.
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestHASIDAcceptsBothSpellingsOfTheColon is the compatibility assertion
// this domain's path parameter turns on.
//
// Fiber does not decode a path parameter and the frontend percent-encodes
// the SID, so "vm%3A109" is what actually arrives — while the handler and
// every audit row want "vm:109", and Proxmox refuses the encoded form in
// an HA SID path. A pattern written against either spelling alone would
// reject every real request from one direction, silently, on all four
// per-resource routes.
func TestHASIDAcceptsBothSpellingsOfTheColon(t *testing.T) {
	const path = haScope + "/resources/:sid"
	for _, tt := range []struct {
		sid  string
		want int
	}{
		{sid: "vm%3A109", want: fiber.StatusNoContent},
		{sid: "vm:109", want: fiber.StatusNoContent},
		{sid: "ct%3A101", want: fiber.StatusNoContent},
		{sid: "109", want: fiber.StatusNoContent},
		// Everything the client's own haResourceIDPattern refuses. The
		// last two are why this parameter carries a pattern at all: the
		// client appends the SID to a Proxmox path RAW, because Proxmox
		// will not take it escaped.
		{sid: "vm%3Aabc", want: fiber.StatusBadRequest},
		{sid: "VM%3A109", want: fiber.StatusBadRequest},
		{sid: "%2e%2e", want: fiber.StatusBadRequest},
		{sid: "vm%3A109%2F..", want: fiber.StatusBadRequest},
	} {
		t.Run(tt.sid, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, fiber.MethodGet, path, cap))
			target := strings.NewReplacer(":cluster_id", testClusterID, ":sid", tt.sid).Replace(path)
			status, env := send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
			if tt.want != fiber.StatusNoContent {
				if cap.called {
					t.Error("the handler ran for a SID the schema rejected")
				}
				return
			}
			// The handler decodes it itself (decodeParamValue); what the
			// schema hands over is the raw segment, and that has to be
			// exactly what the URL carried.
			if got := cap.params.String("sid"); got != tt.sid {
				t.Errorf("sid reached the handler as %q, want the raw segment %q", got, tt.sid)
			}
		})
	}
}

// TestHAGroupAndRuleNamesRejectTraversal pins the other half of the path
// story. proxmox.GetHAGroup and DeleteHARule both build their Proxmox path
// by concatenation with url.PathEscape, which escapes "/" but leaves ".."
// alone — so a name that decodes to ".." would resolve upward wherever a
// proxy in front of pveproxy normalises the path (pveproxy itself takes it
// literally; see proxmox.validatePathSegment). The declared pattern is what
// stops that reaching the client at all.
func TestHAGroupAndRuleNamesRejectTraversal(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		param  string
	}{
		{fiber.MethodDelete, haScope + "/groups/:group", ":group"},
		{fiber.MethodDelete, haScope + "/rules/:rule", ":rule"},
	} {
		t.Run(tt.param, func(t *testing.T) {
			// Every one of these is written the way it would arrive in a
			// URL: a literal space or slash cannot travel in a path
			// segment, so the escaped forms are what a caller trying to
			// smuggle one actually sends.
			for _, name := range []string{"..", "%2e%2e", "a%2Fb", "1group", "-group", "a%20b"} {
				cap := &capture{}
				app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, tt.method, tt.path, cap))
				target := strings.NewReplacer(":cluster_id", testClusterID, tt.param, name).Replace(tt.path)
				status, _ := send(t, app, httptest.NewRequest(tt.method, target, nil))
				if status == fiber.StatusNoContent {
					t.Errorf("%q was accepted", name)
				}
				if cap.called {
					t.Errorf("%q reached the handler", name)
				}
			}
			// A real name still works, including a one-character one:
			// haConfigIDParam is deliberately looser than pve-configid so
			// an object made outside Nexara stays addressable.
			for _, name := range []string{"ha-group01", "a", "A_b-1"} {
				cap := &capture{}
				app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, tt.method, tt.path, cap))
				target := strings.NewReplacer(":cluster_id", testClusterID, tt.param, name).Replace(tt.path)
				if status, env := send(t, app, httptest.NewRequest(tt.method, target, nil)); status != fiber.StatusNoContent {
					t.Errorf("%q: status = %d (%q), want 204", name, status, env.Message)
				}
			}
		})
	}
}

// TestHAEditFormsKeepTheEmptyStringSentinel is the compatibility half of
// this domain's migration, and the one that would have been easiest to
// break.
//
// Three editors send a field empty so that clearing it CLEARS the stored
// value: the resource editor sends group:"" when "no group" is picked and
// comment:"" when the note is emptied (it sends only the fields the operator
// changed), and the group and rule editors send comment:"" whenever the box is
// blank. apischema treats "" as a value the caller SUPPLIED (not an absent
// one), and every registered format rejects it — so a format on any of these
// would 400 a save that has always worked, and folding "" into "absent" in the
// handler would turn the same save into a silent no-op.
//
// This checks both ends: the schema accepts the value, and it arrives
// marked as supplied, which is what optStringPtr turns into a non-nil
// pointer the client actually sends.
func TestHAEditFormsKeepTheEmptyStringSentinel(t *testing.T) {
	for _, tt := range []struct {
		name  string
		path  string
		body  string
		empty []string
	}{
		{
			name:  "resource editor clears the group and the comment",
			path:  haScope + "/resources/:sid",
			body:  `{"group":"","comment":""}`,
			empty: []string{"group", "comment"},
		},
		{
			name:  "group editor clears the comment",
			path:  haScope + "/groups/:group",
			body:  `{"nodes":"pve-01:100,pve-02","comment":""}`,
			empty: []string{"comment"},
		},
		{
			name:  "rule editor clears the comment",
			path:  haScope + "/rules/:rule",
			body:  `{"type":"node-affinity","resources":"vm:100","nodes":"pve-01","strict":1,"disable":0,"comment":""}`,
			empty: []string{"comment"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, fiber.MethodPut, tt.path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPut, haRoute(tt.path), tt.body))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204 — the editor sends these keys empty", status, env.Message)
			}
			for _, key := range tt.empty {
				value, supplied := cap.params.OptString(key)
				if value != "" {
					t.Errorf("%s = %q, want the empty sentinel to survive", key, value)
				}
				if !supplied {
					t.Errorf("%s reads as absent; optStringPtr would return nil and the clear would be "+
						"silently dropped", key)
				}
			}
		})
	}
}

// TestHAPartialUpdatesAreAccepted covers the two quick-action call sites
// that send a body with almost nothing in it: the resource state dropdown
// sends {state} alone, and the rule disable toggle sends {type, disable}
// alone. A schema that made resources or nodes required on PUT would break
// both, and neither goes through the full editor that would have caught it.
func TestHAPartialUpdatesAreAccepted(t *testing.T) {
	for _, tt := range []struct {
		name string
		path string
		body string
	}{
		{"resource state dropdown", haScope + "/resources/:sid", `{"state":"stopped"}`},
		{"rule disable toggle", haScope + "/rules/:rule", `{"type":"node-affinity","disable":1}`},
		{"rule enable toggle", haScope + "/rules/:rule", `{"type":"resource-affinity","disable":0}`},
		{"an empty resource update", haScope + "/resources/:sid", `{}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, fiber.MethodPut, tt.path, cap))
			if status, env := send(t, app, jsonRequest(http.MethodPut, haRoute(tt.path), tt.body)); status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
		})
	}

	// disable=0 has to stay distinguishable from an omitted disable: the
	// client turns an explicit 0 into a property DELETE that re-enables the
	// rule, and an omitted one into no key at all. This holds whatever the
	// declaration says about a default — apischema never reports a default as
	// supplied (see apischema.Property.Default) — so what it pins is that an
	// omitted key reaches the handler as omitted; TestHAFlagsCarryNoDefault is
	// what pins the declaration.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, fiber.MethodPut, haScope+"/rules/:rule", cap))
	send(t, app, jsonRequest(http.MethodPut, haRoute(haScope+"/rules/:rule"), `{"type":"node-affinity"}`))
	if _, supplied := cap.params.OptInt("disable"); supplied {
		t.Error("disable reads as supplied on a body that omitted it; the re-enable DELETE would go out on every edit")
	}
	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeHAEndpoint(t, fiber.MethodPut, haScope+"/rules/:rule", cap))
	send(t, app, jsonRequest(http.MethodPut, haRoute(haScope+"/rules/:rule"), `{"type":"node-affinity","disable":0}`))
	if value, supplied := cap.params.OptInt("disable"); !supplied || value != 0 {
		t.Errorf("disable = (%d, supplied=%v) for an explicit 0; the re-enable path depends on this", value, supplied)
	}
}

// TestHACreateBodiesRequireOnlyWhatTheHandlersDid pins the required SET of
// each create body against what the handler refused before the migration.
//
// It is stated as a set rather than left to the positive cases above,
// because a parameter that quietly became required is invisible to a test
// whose fixtures all happen to send it — which is exactly how `state` got
// through the first draft of this migration.
func TestHACreateBodiesRequireOnlyWhatTheHandlersDid(t *testing.T) {
	for _, tt := range []struct {
		path string
		want []string
	}{
		// CreateResource refused only an empty SID.
		{haScope + "/resources", []string{"cluster_id", "sid"}},
		// CreateGroup refused an empty group name and empty nodes.
		{haScope + "/groups", []string{"cluster_id", "group", "nodes"}},
		// CreateRule refused an empty rule name and an empty type.
		// `resources` is required here too, which the handler did NOT
		// check — but CreateHARule sets it unconditionally, so an omitted
		// one was already a Proxmox 400 with no field name. The
		// declaration turns that into a 400 that says which field.
		{haScope + "/rules", []string{"cluster_id", "resources", "rule", "type"}},
		// Disarm refused anything but freeze/ignore.
		{haScope + "/disarm", []string{"cluster_id", "resource_mode"}},
		// Arm took nothing at all.
		{haScope + "/arm", []string{"cluster_id"}},
	} {
		t.Run(tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodPost, tt.path)
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
		})
	}
}

// TestHARetryCountsCarryOnlyProxmoxsBound pins max_restart and max_relocate
// to pve-ha-manager's own rule: an integer, minimum 0, no maximum. They once
// carried a ceiling of 64 with no source, which answered 400 for a value
// Proxmox accepts; a count well above it must reach the handler, and only a
// negative one may be refused. The int32 ceiling registry_ha.go keeps is a
// platform bound, not a policy, so what fails here is any maximum BELOW it.
func TestHARetryCountsCarryOnlyProxmoxsBound(t *testing.T) {
	const int32Max = 2147483647
	for _, route := range []struct{ method, path string }{
		{fiber.MethodPost, haScope + "/resources"},
		{fiber.MethodPut, haScope + "/resources/:sid"},
	} {
		for _, key := range []string{"max_restart", "max_relocate"} {
			t.Run(route.method+" "+key, func(t *testing.T) {
				prop := declaredEndpoint(t, route.method, route.path).Parameters[key]
				if prop.Maximum != nil && *prop.Maximum < int32Max {
					t.Errorf("%s declares a maximum of %v; Proxmox has none, and only int32's "+
						"ceiling is a bound this API may add", key, *prop.Maximum)
				}

				body := func(n string) string {
					if route.method == fiber.MethodPost {
						return `{"sid":"vm:109","` + key + `":` + n + `}`
					}
					return `{"` + key + `":` + n + `}`
				}

				cap := &capture{}
				app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, route.method, route.path, cap))
				status, env := send(t, app, jsonRequest(route.method, haRoute(route.path), body("1000")))
				if status != fiber.StatusNoContent {
					t.Fatalf("%s=1000: status = %d (%q), want 204 — Proxmox accepts it", key, status, env.Message)
				}
				if got := cap.params.Int(key); got != 1000 {
					t.Errorf("%s reached the handler as %d, want 1000", key, got)
				}

				status, env = send(t, app, jsonRequest(route.method, haRoute(route.path), body("-1")))
				if status != fiber.StatusBadRequest || !strings.Contains(env.Message, key+": must be at least 0") {
					t.Errorf("%s=-1: status = %d (%q), want 400 naming the minimum — Proxmox's minimum is 0",
						key, status, env.Message)
				}
			})
		}
	}
}

// TestHAFlagsCarryNoDefault pins every HA 0/1 flag as optional, bounded to
// 0..1 and WITHOUT a Default, for the reasons on haFlag: a default never
// reaches Proxmox through the p.OptInt reads (see apischema.Property.Default),
// but CreateGroup and CreateRule read theirs with p.Int and send any non-zero
// value, and on every route it would document a value the endpoint does not
// apply.
func TestHAFlagsCarryNoDefault(t *testing.T) {
	flags := map[string][]string{
		fiber.MethodPost + " " + haScope + "/resources":     {"failback"},
		fiber.MethodPut + " " + haScope + "/resources/:sid": {"failback"},
		fiber.MethodPost + " " + haScope + "/groups":        {"restricted", "nofailback"},
		fiber.MethodPut + " " + haScope + "/groups/:group":  {"restricted", "nofailback"},
		fiber.MethodPost + " " + haScope + "/rules":         {"strict"},
		fiber.MethodPut + " " + haScope + "/rules/:rule":    {"strict", "disable"},
	}
	declared := declaredHAEndpoints(t)
	for key, names := range flags {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s is not declared", key)
			continue
		}
		for _, name := range names {
			prop, ok := e.Parameters[name]
			if !ok {
				t.Errorf("%s does not declare %q", key, name)
				continue
			}
			if !prop.Optional {
				t.Errorf("%s: %q is required; every one of these is optional", key, name)
			}
			if prop.Default != nil {
				t.Errorf("%s: %q declares default %#v; an omitted flag is left alone on a PUT and gets "+
					"Proxmox's default on a POST (see haFlag)", key, name, prop.Default)
			}
			if prop.Minimum == nil || *prop.Minimum != 0 || prop.Maximum == nil || *prop.Maximum != 1 {
				t.Errorf("%s: %q is not bounded to 0..1 (min %v, max %v)", key, name, prop.Minimum, prop.Maximum)
			}
		}
	}
}

// TestDisarmResourceModeIsAnEnum pins the one Proxmox vocabulary in this
// domain the schema DOES claim authority over — because the handler
// already did, refusing anything but these two by name. The rest
// (state, rule type, affinity) deliberately carry no enum; see the doc
// comments on haStateParam and haRuleTypeParam.
func TestDisarmResourceModeIsAnEnum(t *testing.T) {
	const path = haScope + "/disarm"
	e := declaredEndpoint(t, fiber.MethodPost, path)
	mode := e.Parameters["resource_mode"]
	if len(mode.Enum) != 2 || mode.Enum[0] != "freeze" || mode.Enum[1] != "ignore" {
		t.Errorf("resource_mode enum = %v, want [freeze ignore]", mode.Enum)
	}
	if mode.Optional {
		t.Error("resource_mode is optional; the handler refused an empty one")
	}

	for _, tt := range []struct {
		body string
		want int
	}{
		{`{"resource_mode":"freeze"}`, fiber.StatusNoContent},
		{`{"resource_mode":"ignore"}`, fiber.StatusNoContent},
		{`{"resource_mode":""}`, fiber.StatusBadRequest},
		{`{"resource_mode":"thaw"}`, fiber.StatusBadRequest},
		{`{}`, fiber.StatusBadRequest},
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, fiber.MethodPost, path, cap))
		if status, env := send(t, app, jsonRequest(http.MethodPost, haRoute(path), tt.body)); status != tt.want {
			t.Errorf("body %s: status = %d (%q), want %d", tt.body, status, env.Message, tt.want)
		}
	}

	// Arm takes nothing at all, and the dialog POSTs `{}` at it.
	if got := len(declaredEndpoint(t, fiber.MethodPost, haScope+"/arm").Parameters); got != 1 {
		t.Errorf("arm declares %d parameters, want just :cluster_id", got)
	}
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, fiber.MethodPost, haScope+"/arm", cap))
	if status, env := send(t, app, jsonRequest(http.MethodPost, haRoute(haScope+"/arm"), `{}`)); status != fiber.StatusNoContent {
		t.Errorf("arm with an empty body: status = %d (%q), want 204", status, env.Message)
	}
}

// TestHACreateBodiesRejectWhatTheHandlersUsedTo pins the five "x is
// required" checks the handlers no longer make, and the parameter set they
// never closed.
func TestHACreateBodiesRejectWhatTheHandlersUsedTo(t *testing.T) {
	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   string
		field  string
	}{
		{"resource with no sid", fiber.MethodPost, haScope + "/resources", `{"state":"started"}`, "sid:"},
		{"resource with an empty sid", fiber.MethodPost, haScope + "/resources", `{"sid":""}`, "sid:"},
		{"resource with a malformed sid", fiber.MethodPost, haScope + "/resources", `{"sid":"vm:abc"}`, "sid:"},
		{"group with no name", fiber.MethodPost, haScope + "/groups", `{"nodes":"pve-01"}`, "group:"},
		{"group with no nodes", fiber.MethodPost, haScope + "/groups", `{"group":"ha-group01"}`, "nodes:"},
		{"group with empty nodes", fiber.MethodPost, haScope + "/groups", `{"group":"ha-group01","nodes":""}`, "nodes:"},
		{"rule with no name", fiber.MethodPost, haScope + "/rules", `{"type":"node-affinity","resources":"vm:100"}`, "rule:"},
		{"rule with no type", fiber.MethodPost, haScope + "/rules", `{"rule":"ha-rule01","resources":"vm:100"}`, "type:"},
		{"rule with an empty type", fiber.MethodPost, haScope + "/rules", `{"rule":"ha-rule01","type":"","resources":"vm:100"}`, "type:"},
		{"rule update with no type", fiber.MethodPut, haScope + "/rules/:rule", `{"disable":1}`, "type:"},
		// PVE's additionalProperties => 0: a misspelled key used to be
		// bound to nothing and silently ignored.
		{"a misspelled resource key", fiber.MethodPost, haScope + "/resources", `{"sid":"vm:109","max_restarts":3}`, "max_restarts:"},
		// The path spells the SID; a body copy would be a second, editable
		// spelling of the object being written, and checkPathParams only
		// refuses that for the parameter names the permission gate reads.
		{"sid in the update body", fiber.MethodPut, haScope + "/resources/:sid", `{"sid":"vm:999","state":"stopped"}`, "sid:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, tt.method, tt.path, cap))
			status, env := send(t, app, jsonRequest(tt.method, haRoute(tt.path), tt.body))
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

// TestHACreateBodiesAcceptTheDialogs is the positive half: exactly what
// each create form sends today, accepted verbatim.
func TestHACreateBodiesAcceptTheDialogs(t *testing.T) {
	for _, tt := range []struct {
		name string
		path string
		body string
	}{
		{
			name: "resource form",
			path: haScope + "/resources",
			body: `{"sid":"vm:109","state":"started","group":"ha-group01","max_restart":1,` +
				`"max_relocate":1,"failback":1,"comment":"note"}`,
		},
		{
			// The form omits group, comment and the two counters rather
			// than sending them empty, so this is the shape that proves
			// they are genuinely optional and not merely tolerant of "".
			name: "resource form with everything optional left out",
			path: haScope + "/resources",
			body: `{"sid":"ct:101","state":"started","failback":0}`,
		},
		{
			// sid ALONE, which is all the handler ever required. A review
			// of this migration caught the create body declaring `state`
			// required when nothing had required it before: Nexara's own
			// two creators both send it, so no dialog would have noticed,
			// and only a script caller would have hit the 400.
			// CreateHAResource drops an empty state rather than forwarding
			// it, so omitting it lets Proxmox apply its own default.
			name: "just the sid, which is all the handler ever required",
			path: haScope + "/resources",
			body: `{"sid":"vm:110"}`,
		},
		{
			name: "group form",
			path: haScope + "/groups",
			body: `{"group":"ha-group01","nodes":"pve-01:100,pve-02","comment":"note"}`,
		},
		{
			name: "node-affinity rule form",
			path: haScope + "/rules",
			body: `{"rule":"ha-rule01","type":"node-affinity","resources":"vm:100,vm:101",` +
				`"nodes":"pve-01:100,pve-02","strict":1}`,
		},
		{
			name: "resource-affinity rule form",
			path: haScope + "/rules",
			body: `{"rule":"ha-rule02","type":"resource-affinity","resources":"vm:100,vm:101","affinity":"negative"}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, fiber.MethodPost, tt.path, cap))
			if status, env := send(t, app, jsonRequest(http.MethodPost, haRoute(tt.path), tt.body)); status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
		})
	}
}

// TestHADigestStaysDeclarable pins that Proxmox's compare-and-swap token
// still reaches the client.
//
// Nexara's own UI never sends one, so it is exactly the parameter a
// migration would drop without noticing — and dropping it turns an
// external caller's guarded write into "unknown parameter", which is the
// one way this change could remove a safety feature rather than add one.
func TestHADigestStaysDeclarable(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef01234567"
	// Each route carries the minimum body it requires alongside the
	// digest — the rules PUT needs a type, the other two need nothing.
	for _, tt := range []struct{ path, body string }{
		{haScope + "/resources/:sid", `{"digest":"` + digest + `"}`},
		{haScope + "/groups/:group", `{"digest":"` + digest + `"}`},
		{haScope + "/rules/:rule", `{"type":"node-affinity","digest":"` + digest + `"}`},
	} {
		t.Run(tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodPut, tt.path)
			if _, ok := e.Parameters["digest"]; !ok {
				t.Fatal("no digest parameter; a guarded write would come back as \"unknown parameter\"")
			}
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeHAEndpoint(t, fiber.MethodPut, tt.path, cap))
			if status, env := send(t, app, jsonRequest(http.MethodPut, haRoute(tt.path), tt.body)); status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			if got := cap.params.String("digest"); got != digest {
				t.Errorf("digest reached the handler as %q", got)
			}
		})
	}
}

// TestHARouteIsGatedByItsDeclaration proves the permission the declaration
// states is the permission the route enforces, end to end, on a REAL
// endpoint rather than a synthetic one.
//
// It is run against resource delete because that is the route whose
// deleted inline check would be least survivable: it takes a guest out of
// HA management, which is invisible until the node it was on fails.
func TestHARouteIsGatedByItsDeclaration(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, haScope+"/resources/:sid")
	if e.Permissions.Describe() != "manage:ha" {
		t.Fatalf("resource delete declares %q, want manage:ha", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := haRoute(haScope + "/resources/:sid")

	t.Run("a caller holding only view:ha is refused", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:ha": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodDelete, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding manage:ha gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:ha": true}), gated)
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
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:ha": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestEveryHAEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing: every route and every
// parameter says what it is for, because the declaration IS the
// documentation and 319 endpoints in this API still say nothing.
func TestEveryHAEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredHAEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "High Availability" {
			t.Errorf("%s is in group %q, want High Availability", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
