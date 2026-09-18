package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts — rather
// than a fixture shaped like them, so a change to registry_access.go that
// quietly loosened a parameter would show up here.

// accessRouteCount is how many endpoints registerAccessEndpoints declares. It is
// ALL 25 of AccessHandler's routes; nothing in this domain was left legacy. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum of
// per-domain constants.
const accessRouteCount = 25

const (
	testAccessUserID  = "nexara%40pve"
	testAccessTokenID = "api"
	testAccessGroupID = "operators"
	testAccessRoleID  = "NexaraOperator"
	testAccessRealm   = "pve"
)

func accessRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":userid", testAccessUserID,
		":tokenid", testAccessTokenID,
		":groupid", testAccessGroupID,
		":roleid", testAccessRoleID,
		":realm", testAccessRealm,
	).Replace(path)
}

// accessLegacyPermissions is what each handler checked with a hand-placed
// requireClusterPerm call BEFORE Phase 6h, transcribed from
// `git show HEAD:internal/api/handlers/access.go` at commit 3ca85e9.
//
// Every entry is one call, and every call hoists: each handler resolved the
// cluster from its own path with clusterIDFromParam and then made exactly one
// static requireClusterPerm(c, action, accessResource, clusterID). There is no
// Deferred, Advisory or global route in this domain and no route made two
// calls, which is why this table carries no `calls`/`hoisted` columns the way
// alertLegacyPermissions does — 25 calls in, 25 gates out, nothing kept.
//
// The RESOURCE was the package const accessResource rather than a literal, so
// it is resolved here through the same symbol the declarations name (now
// exported as handlers.AccessResource) instead of being re-typed as "access" —
// a second spelling is what the export exists to prevent.
var accessLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/access/users":                            "view",
	"POST /api/v1/clusters/:cluster_id/access/users":                           "manage",
	"GET /api/v1/clusters/:cluster_id/access/users/:userid":                    "view",
	"PUT /api/v1/clusters/:cluster_id/access/users/:userid":                    "manage",
	"DELETE /api/v1/clusters/:cluster_id/access/users/:userid":                 "manage",
	"GET /api/v1/clusters/:cluster_id/access/users/:userid/tokens":             "view",
	"GET /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid":    "view",
	"POST /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid":   "manage",
	"PUT /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid":    "manage",
	"DELETE /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid": "manage",
	"GET /api/v1/clusters/:cluster_id/access/groups":                           "view",
	"POST /api/v1/clusters/:cluster_id/access/groups":                          "manage",
	"GET /api/v1/clusters/:cluster_id/access/groups/:groupid":                  "view",
	"PUT /api/v1/clusters/:cluster_id/access/groups/:groupid":                  "manage",
	"DELETE /api/v1/clusters/:cluster_id/access/groups/:groupid":               "manage",
	"GET /api/v1/clusters/:cluster_id/access/roles":                            "view",
	"POST /api/v1/clusters/:cluster_id/access/roles":                           "manage",
	"GET /api/v1/clusters/:cluster_id/access/roles/:roleid":                    "view",
	"PUT /api/v1/clusters/:cluster_id/access/roles/:roleid":                    "manage",
	"DELETE /api/v1/clusters/:cluster_id/access/roles/:roleid":                 "manage",
	"GET /api/v1/clusters/:cluster_id/access/acl":                              "view",
	"PUT /api/v1/clusters/:cluster_id/access/acl":                              "manage",
	"GET /api/v1/clusters/:cluster_id/access/domains":                          "view",
	"GET /api/v1/clusters/:cluster_id/access/domains/:realm":                   "view",
	"GET /api/v1/clusters/:cluster_id/access/permissions":                      "view",
}

// declaredAccessEndpoints returns every declaration in this domain, keyed
// "METHOD path". The prefix is enough here, unlike in the alert domain: all 25
// routes hang off one path and nothing else is mounted under it.
func declaredAccessEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, accessScope+"/") {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestAccessRoutesDeclareTheSamePermissionTheyEnforced is the tally that makes
// this migration a refactor rather than a change: 25 hand-placed calls in, 25
// declared cluster-scoped Checks out, none kept in a handler.
//
// It runs in both directions — a declared route missing from the table is
// reported too — so a new access route cannot appear without the tally being
// re-made, which is the only thing that keeps it a review surface.
func TestAccessRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredAccessEndpoints(t)
	if len(declared) != accessRouteCount {
		t.Fatalf("the registry declares %d access routes, want %d", len(declared), accessRouteCount)
	}
	if len(accessLegacyPermissions) != accessRouteCount {
		t.Fatalf("accessLegacyPermissions has %d entries, want %d — the table must cover every migrated route",
			len(accessLegacyPermissions), accessRouteCount)
	}

	hoisted := 0
	for key, action := range accessLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s:%s before the migration but is not declared in the registry",
				key, action, handlers.AccessResource)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check — every route in this domain resolves its "+
				"cluster from its own path and checks one static permission", key, e.Permissions.Describe())
			continue
		}
		hoisted++
		if want := action + ":" + handlers.AccessResource; e.Permissions.Describe() != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration",
				key, e.Permissions.Describe(), want)
		}
		// Getting the scope backwards is the one mistake here that would change
		// who can do what: a global declaration would let a caller holding
		// manage:access instance-wide mint an Administrator token on a cluster
		// they hold nothing on.
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, ScopeCluster)
		}
	}
	if hoisted != accessRouteCount {
		t.Errorf("the tally moves %d permission call(s) into middleware, want all %d", hoisted, accessRouteCount)
	}

	for key := range declared {
		if _, listed := accessLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in accessLegacyPermissions — a new access route must be "+
				"added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestAccessRoutesAreGatedByTheirDeclaration proves the 25 hoisted permissions
// are the permissions the routes enforce, end to end.
//
// The "unrelated grant" case is deliberately view:access on a MANAGE route
// rather than some foreign permission: manage:access can mint an Administrator
// token, so the view/manage split inside this domain is the one that matters,
// and a route that accidentally declared view would pass a check against a
// wholly unrelated grant.
func TestAccessRoutesAreGatedByTheirDeclaration(t *testing.T) {
	for key, action := range accessLegacyPermissions {
		method, path, _ := strings.Cut(key, " ")
		want := action + ":" + handlers.AccessResource
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := accessRoute(path)

			unrelated := "view:" + handlers.AccessResource
			if action == "view" {
				unrelated = "manage:" + handlers.AccessResource
			}
			app := newRegistryApp(t, stubAuth(map[string]bool{unrelated: true}), gated)
			status, _ := send(t, app, authedRequest(method, target))
			if status != fiber.StatusForbidden {
				t.Fatalf("a caller holding only %q got %d, want 403", unrelated, status)
			}
			if cap.called {
				t.Error("the handler ran for a caller without the declared permission")
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want: true}), gated)
			status, _ = send(t, app, authedRequest(method, target))
			if status == fiber.StatusForbidden {
				t.Fatalf("a caller holding %q got 403", want)
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want: true}), gated)
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

// probeAccessEndpoint is a declared access endpoint with its handler swapped
// for a capture and its gate removed, so a parameter test needs neither a
// database nor a session.
func probeAccessEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestAccessPathSegmentsAreAnchored is the traversal guard for this domain.
//
// Every one of these identifiers becomes a segment of a Proxmox request path,
// built with url.PathEscape — which escapes "/" but leaves "." and ".." alone —
// so an un-anchored segment resolves onto the PARENT collection once pveproxy
// normalises the path. The handlers only ever checked the segment was non-empty,
// which ".." satisfies.
//
// The second half is what keeps the anchor honest: a real value, including the
// percent-encoded user id every correct client sends, must still pass. An anchor
// that refused "nexara%40pve" would break the whole tab.
func TestAccessPathSegmentsAreAnchored(t *testing.T) {
	// The two segments a path normaliser resolves onto the parent. The anchor
	// is "an optional leading dot, then a NON-dot" — the shape registry_ceph.go
	// uses — so it keeps both of these out while still admitting a dotted name
	// such as ".hidden" that a group or role created outside Nexara may carry.
	traversals := []string{".", ".."}

	segments := []struct {
		name  string
		param string
		route string
		good  []string
	}{
		{
			name:  "userid",
			param: "userid",
			route: accessScope + "/users/:userid",
			// The last three are the regression M1 named: encodeURIComponent
			// leaves `- _ . ! ~ * ' ( )` unescaped, and proxmox.validateUserID
			// accepts every one of them in the name half, so a pattern that
			// admitted only [A-Za-z0-9._@-] would 400 an ordinary AD username.
			good: []string{
				"nexara%40pve", "nexara@pve", "root%40pam", "first.last-1%40pve",
				"o'brien%40ad", "o'brien@ad", "a!b~c*d(e)f%40pve",
			},
		},
		{
			name:  "tokenid",
			param: "tokenid",
			route: accessScope + "/users/:userid/tokens/:tokenid",
			good:  []string{"api", "nexara-collector", "a.b_c-1"},
		},
		{
			name:  "groupid",
			param: "groupid",
			route: accessScope + "/groups/:groupid",
			// "..archive" is the M3 regression: proxmox.validateAccessName
			// refuses EXACTLY "." and "..", so a group whose name merely starts
			// with two dots is legal and must stay reachable.
			good: []string{"operators", "ops-team", "a.b", ".hidden", "..archive", "...", "a", ".a"},
		},
		{
			name:  "roleid",
			param: "roleid",
			route: accessScope + "/roles/:roleid",
			good:  []string{"NexaraOperator", "PVEAdmin", "role-1", ".internal", "..legacy", "r"},
		},
		{
			name:  "realm",
			param: "realm",
			route: accessScope + "/domains/:realm",
			good:  []string{"pve", "pam", "ldap-1", "ad.example"},
		},
	}

	for _, seg := range segments {
		t.Run(seg.name, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodGet, seg.route)
			prop, ok := e.Parameters[seg.param]
			if !ok {
				t.Fatalf("%s declares no %q parameter", seg.route, seg.param)
			}
			if prop.Pattern == "" {
				t.Fatalf("%s: %q declares no pattern, so \"..\" reaches url.PathEscape and resolves "+
					"onto the parent collection", seg.route, seg.param)
			}

			base := map[string]any{
				"cluster_id": testClusterID,
				"userid":     "nexara%40pve",
				"tokenid":    "api",
				"groupid":    "operators",
				"roleid":     "PVEAdmin",
				"realm":      "pve",
			}
			validate := func(value string) error {
				in := map[string]any{}
				for name := range e.Parameters {
					if v, known := base[name]; known {
						in[name] = v
					}
				}
				in[seg.param] = value
				_, err := e.Parameters.Validate(in)
				return err
			}

			for _, bad := range traversals {
				if err := validate(bad); err == nil {
					t.Errorf("%q was accepted for %s — it is the traversal segment the anchor exists to refuse",
						bad, seg.param)
				}
			}
			for _, good := range seg.good {
				if err := validate(good); err != nil {
					t.Errorf("%q was refused for %s: %v — the anchor must not cost a value Proxmox itself accepts",
						good, seg.param, err)
				}
			}
		})
	}
}

// TestAccessPatternsAreNoNarrowerThanWhatACallerCanSend is the assertion that
// would have caught the two narrowings a review found in the first draft of
// these patterns, and it is a PROPERTY rather than a list of examples.
//
// A too-tight pattern does not fail loudly. It 400s one operator holding one
// unusual identifier, months later, with a message about a regex — which is why
// picking the class by eye is not good enough and why the two rules below are
// derived from their own sources of truth instead.
func TestAccessPatternsAreNoNarrowerThanWhatACallerCanSend(t *testing.T) {
	userid := declaredEndpoint(t, fiber.MethodGet, accessScope+"/users/:userid").Parameters["userid"]
	uidRe := regexp.MustCompile(userid.Pattern)

	// encodeURIComponent's unreserved set, verbatim from the ECMAScript spec:
	// letters, digits and these nine. A correct client sends each of them RAW,
	// so any of them missing from the class is a 400 on a value nothing escapes
	// — and proxmox.validateUserID accepts all nine in the name half, refusing
	// only "/", "\", "%", control characters and a name of exactly "." or "..".
	for _, ch := range []string{"-", "_", ".", "!", "~", "*", "'", "(", ")"} {
		// Placed after a leading letter, because the FIRST character is where
		// the traversal anchor deliberately excludes ".".
		if candidate := "a" + ch + "b@pve"; !uidRe.MatchString(candidate) {
			t.Errorf("the userid pattern refuses %q, but encodeURIComponent leaves %q unescaped and "+
				"proxmox.validateUserID accepts it — a real PVE account would 400", candidate, ch)
		}
	}
	// And the three shapes that must stay refused, whatever the class admits.
	for _, bad := range []string{"a/b@pve", `a\b@pve`, "a%b@pve", ".", ".."} {
		if uidRe.MatchString(bad) {
			t.Errorf("the userid pattern accepts %q", bad)
		}
	}

	// The group/role class is small enough to enumerate, so the subset property
	// is checked EXHAUSTIVELY rather than sampled: over every string of length
	// 1..3 drawn from proxmox.accessNamePattern's own class, the declaration
	// must accept exactly what that validator accepts — which is everything
	// except the two literal traversal segments.
	groupid := declaredEndpoint(t, fiber.MethodGet, accessScope+"/groups/:groupid").Parameters["groupid"]
	nameRe := regexp.MustCompile(groupid.Pattern)
	const class = "aZ0._-" // one representative of each character kind in [A-Za-z0-9._-]
	var walk func(prefix string, depth int)
	walk = func(prefix string, depth int) {
		if prefix != "" {
			want := prefix != "." && prefix != ".."
			if got := nameRe.MatchString(prefix); got != want {
				t.Errorf("the group/role pattern %s %q; proxmox.validateAccessName refuses exactly "+
					"\".\" and \"..\" and accepts everything else in its class",
					map[bool]string{true: "accepts", false: "refuses"}[got], prefix)
			}
		}
		if depth == 0 {
			return
		}
		for _, ch := range class {
			walk(prefix+string(ch), depth-1)
		}
	}
	walk("", 3)
}

// TestAccessTraversalIsRefusedAtTheRoute proves the anchor bites on a real
// request rather than only in a schema unit test, and that it does so BEFORE
// the handler runs.
func TestAccessTraversalIsRefusedAtTheRoute(t *testing.T) {
	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeAccessEndpoint(t, fiber.MethodGet, accessScope+"/groups/:groupid", cap))

	target := pathPrefix + "clusters/" + testClusterID + "/access/groups/.."
	status, _ := send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
	// Fiber normalises "/groups/.." out of the URL before routing, so the
	// request lands on the collection path instead — which this app does not
	// mount. Either answer is a refusal; what must NOT happen is the handler
	// running with ".." in hand.
	if status != fiber.StatusBadRequest && status != fiber.StatusNotFound {
		t.Errorf("status = %d, want 400 or 404 for a traversal segment", status)
	}
	if cap.called {
		t.Error("the handler ran for a traversal segment")
	}

	// The escaped spelling reaches routing intact and must be refused by the
	// pattern rather than by Fiber.
	cap.called = false
	escaped := pathPrefix + "clusters/" + testClusterID + "/access/groups/%2e%2e"
	status, env := send(t, app, httptest.NewRequest(http.MethodGet, escaped, nil))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d (%q), want 400 for an escaped traversal segment", status, env.Message)
	}
	if cap.called {
		t.Error("the handler ran for an escaped traversal segment")
	}
}

// accessForceRoutes are the four routes that can sever Nexara's own credential
// and therefore take ?force=.
var accessForceRoutes = []struct{ method, path string }{
	{fiber.MethodPut, accessScope + "/users/:userid"},
	{fiber.MethodDelete, accessScope + "/users/:userid"},
	{fiber.MethodPut, accessScope + "/users/:userid/tokens/:tokenid"},
	{fiber.MethodDelete, accessScope + "/users/:userid/tokens/:tokenid"},
}

// TestAccessForceIsReadFromTheQueryString pins the source of the one parameter
// this migration had to state explicitly.
//
// forceRequested read c.Query("force") on all four routes. Two of them are PUTs,
// and ResolveSource reads an unsourced parameter on a mutating verb out of the
// BODY — so a declaration that left Source unset would silently stop honouring
// the "?force=true" every caller sends, turning the confirmation into one that
// can never be given. It fails CLOSED (the guard refuses), which is why nothing
// would have crashed and why this needs a test rather than a code comment.
func TestAccessForceIsReadFromTheQueryString(t *testing.T) {
	for _, r := range accessForceRoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			e := declaredEndpoint(t, r.method, r.path)
			prop, ok := e.Parameters["force"]
			if !ok {
				t.Fatalf("declares no force parameter, but the handler reads one")
			}
			if prop.Source != apischema.SourceQuery {
				t.Errorf("force declares source %q, want %q — the UI appends ?force=true to the URL",
					prop.Source, apischema.SourceQuery)
			}
			if prop.Type != apischema.Boolean {
				t.Errorf("force declares type %q, want %q", prop.Type, apischema.Boolean)
			}
			if prop.Default != false {
				t.Errorf("force declares default %v, want false — omitting it must not force anything", prop.Default)
			}
		})
	}

	// End to end on the shape the UI actually sends, and on the absence of it.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeAccessEndpoint(t, fiber.MethodDelete, accessScope+"/users/:userid", cap))
	base := pathPrefix + "clusters/" + testClusterID + "/access/users/" + testAccessUserID

	for _, tc := range []struct {
		query string
		want  bool
	}{
		{"", false},
		{"?force=true", true},
		{"?force=1", true},
		{"?force=false", false},
		{"?force=0", false},
	} {
		cap.called = false
		status, env := send(t, app, httptest.NewRequest(http.MethodDelete, base+tc.query, nil))
		if status != fiber.StatusNoContent {
			t.Fatalf("%q: status = %d (%q), want 204", tc.query, status, env.Message)
		}
		if got := cap.params.Bool("force"); got != tc.want {
			t.Errorf("%q: force = %v, want %v", tc.query, got, tc.want)
		}
	}

	// A value that is neither now 400s rather than silently reading as "no".
	// forceRequested returned false for it, which on a destructive route is the
	// safe answer but an unreported one: the operator believed they had
	// confirmed and the request came back 409 with no explanation of why their
	// flag was ignored.
	cap.called = false
	status, _ := send(t, app, httptest.NewRequest(http.MethodDelete, base+"?force=banana", nil))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a force value that is neither true nor false", status)
	}
	if cap.called {
		t.Error("the handler ran for an unparseable force value")
	}
}

// TestAccessUserFieldsStayTristate is the compatibility assertion for the eight
// account attributes whose Proxmox struct field is a POINTER.
//
// nil omits the key from the outbound form (leave the stored value alone) and a
// pointer to the zero value clears it. A Default on any of them would make
// p.OptString report every request as having supplied it, so a rename would
// start clearing the e-mail address — and a Default on `enable` would re-enable
// a deliberately disabled account on every comment edit.
func TestAccessUserFieldsStayTristate(t *testing.T) {
	tristate := []string{"comment", "email", "firstname", "lastname", "groups", "keys", "enable", "expire"}

	for _, route := range []struct{ method, path string }{
		{fiber.MethodPost, accessScope + "/users"},
		{fiber.MethodPut, accessScope + "/users/:userid"},
	} {
		t.Run(route.method, func(t *testing.T) {
			e := declaredEndpoint(t, route.method, route.path)
			for _, name := range tristate {
				prop, ok := e.Parameters[name]
				if !ok {
					t.Errorf("%s declares no %q parameter", route.path, name)
					continue
				}
				if !prop.Optional {
					t.Errorf("%s: %q is required; every account attribute has always been optional", route.path, name)
				}
				if prop.Default != nil {
					t.Errorf("%s: %q declares default %v — it is a tristate, and a default collapses "+
						"\"left alone\" into \"set to this\"", route.path, name, prop.Default)
				}
			}
		})
	}

	// `email` deliberately carries no format: every registered one refuses the
	// empty string, which is the only way to clear a stored address.
	for _, route := range []struct{ method, path string }{
		{fiber.MethodPost, accessScope + "/users"},
		{fiber.MethodPut, accessScope + "/users/:userid"},
	} {
		if f := declaredEndpoint(t, route.method, route.path).Parameters["email"].Format; f != "" {
			t.Errorf("%s %s: email declares format %q; that refuses \"\", which is how a caller clears it",
				route.method, route.path, f)
		}
	}

	// And end to end: an empty string survives as a SUPPLIED value rather than
	// falling back to a default, which is what makes "clear it" expressible.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeAccessEndpoint(t, fiber.MethodPut, accessScope+"/users/:userid", cap))
	target := pathPrefix + "clusters/" + testClusterID + "/access/users/" + testAccessUserID
	status, env := send(t, app, jsonRequest(http.MethodPut, target, `{"comment":""}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if v, supplied := cap.params.OptString("comment"); !supplied || v != "" {
		t.Errorf(`comment = (%q,%v), want ("",true) — an empty comment is a clear, not an omission`, v, supplied)
	}
	if _, supplied := cap.params.OptString("email"); supplied {
		t.Error("email reads as supplied although the request never sent it")
	}
}

// TestAccessRequiredSetsMatchWhatEachHandlerEnforced pins the required
// parameters against what the code refused BEFORE the migration, since a
// required set is the easiest thing to widen or narrow by accident.
//
// The three create routes each had one explicit `if req.X == ""` check. The role
// UPDATE is the interesting one: proxmox.UpdateAccessRole refuses a NIL privs
// outright, because an empty value strips every privilege and a client that
// forgot the field must not disarm a role by accident — so `privs` is required
// here, while on CREATE the handler dereferenced a nil into "" and it is not.
func TestAccessRequiredSetsMatchWhatEachHandlerEnforced(t *testing.T) {
	cases := []struct {
		method, path string
		required     []string
		optional     []string
	}{
		{fiber.MethodPost, accessScope + "/users", []string{"userid"}, []string{"password", "comment", "groups"}},
		{fiber.MethodPost, accessScope + "/groups", []string{"groupid"}, []string{"comment"}},
		{fiber.MethodPost, accessScope + "/roles", []string{"roleid"}, []string{"privs"}},
		{fiber.MethodPut, accessScope + "/roles/:roleid", []string{"privs"}, []string{"append"}},
		{fiber.MethodPut, accessScope + "/acl", []string{"path", "roles"}, []string{"users", "groups", "tokens", "propagate", "delete"}},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			e := declaredEndpoint(t, tc.method, tc.path)
			for _, name := range tc.required {
				prop, ok := e.Parameters[name]
				if !ok {
					t.Errorf("declares no %q parameter", name)
					continue
				}
				if prop.Optional {
					t.Errorf("%q is optional, but the endpoint has always refused a request without it", name)
				}
			}
			for _, name := range tc.optional {
				prop, ok := e.Parameters[name]
				if !ok {
					t.Errorf("declares no %q parameter", name)
					continue
				}
				if !prop.Optional {
					t.Errorf("%q is required, but the endpoint has always accepted a request without it", name)
				}
			}
		})
	}

	// End to end on the two that changed hands: the role update refuses an
	// omitted privs at the schema now (it used to reach the client's own
	// refusal), and still accepts an EMPTY one, which is the deliberate spelling
	// of "remove every privilege".
	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeAccessEndpoint(t, fiber.MethodPut, accessScope+"/roles/:roleid", cap))
	target := pathPrefix + "clusters/" + testClusterID + "/access/roles/" + testAccessRoleID

	status, env := send(t, app, jsonRequest(http.MethodPut, target, `{}`))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d (%q), want 400 for a role update with no privs", status, env.Message)
	}
	if cap.called {
		t.Error("the handler ran for a role update that named no privileges")
	}

	cap.called = false
	status, env = send(t, app, jsonRequest(http.MethodPut, target, `{"privs":""}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 for an explicit clear", status, env.Message)
	}
	if v, supplied := cap.params.OptString("privs"); !supplied || v != "" {
		t.Errorf(`privs = (%q,%v), want ("",true) — an empty list is a deliberate clear`, v, supplied)
	}
}

// TestAccessRoleUpdateDoesNotDeclareARoleIDBody records a deliberate narrowing.
//
// UpdateRole and UpdateGroup both bound a struct carrying the id as well as the
// mutable fields, and both IGNORED it — the id comes from the path. An
// undeclared key is now a 400, so a caller that sent {"roleid":…,"privs":…}
// gets told rather than silently having half its body dropped. That is the
// behaviour the registry exists to produce; this test says so out loud so the
// narrowing is a decision rather than something nobody noticed.
func TestAccessRoleUpdateDoesNotDeclareARoleIDBody(t *testing.T) {
	for _, tc := range []struct {
		method, path, ignored, body string
	}{
		{fiber.MethodPut, accessScope + "/roles/:roleid", "roleid", `{"privs":"VM.Audit","roleid":"Other"}`},
		{fiber.MethodPut, accessScope + "/groups/:groupid", "groupid", `{"comment":"x","groupid":"other"}`},
	} {
		t.Run(tc.ignored, func(t *testing.T) {
			e := declaredEndpoint(t, tc.method, tc.path)
			prop := e.Parameters[tc.ignored]
			if prop.Source == apischema.SourceBody {
				t.Fatalf("%q is declared as a body parameter; it is the path segment", tc.ignored)
			}

			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeAccessEndpoint(t, tc.method, tc.path, cap))
			target := accessRoute(tc.path)
			status, env := send(t, app, jsonRequest(tc.method, target, tc.body))
			if status != fiber.StatusBadRequest {
				t.Errorf("status = %d (%q), want 400 — the id in the body is not a parameter this route takes",
					status, env.Message)
			}
			if !strings.Contains(env.Message, tc.ignored) {
				t.Errorf("the rejection does not name %q: %q", tc.ignored, env.Message)
			}
		})
	}
}

// TestAccessPasswordIsWriteOnly pins the one parameter in this domain that
// carries a credential.
//
// It must be declared — a caller has to be able to set an initial password —
// and it must exist on the CREATE route alone. The update route never accepted
// one (PVE changes a password through /access/password, which Nexara does not
// proxy), and declaring it there would create a write path whose audit row this
// file's own guard would then have to police.
func TestAccessPasswordIsWriteOnly(t *testing.T) {
	create := declaredEndpoint(t, fiber.MethodPost, accessScope+"/users")
	prop, ok := create.Parameters["password"]
	if !ok {
		t.Fatal("POST .../access/users declares no password parameter")
	}
	if !prop.Optional {
		t.Error("password is required; an account with no password is the normal shape for a token owner")
	}

	for _, route := range []struct{ method, path string }{
		{fiber.MethodPut, accessScope + "/users/:userid"},
		{fiber.MethodPost, accessScope + "/users/:userid/tokens/:tokenid"},
		{fiber.MethodPut, accessScope + "/users/:userid/tokens/:tokenid"},
	} {
		if _, declared := declaredEndpoint(t, route.method, route.path).Parameters["password"]; declared {
			t.Errorf("%s %s declares a password parameter; this route has never accepted one",
				route.method, route.path)
		}
	}
}

// TestAccessIdentifiersAreClusterUUIDs keeps the one gate parameter honest: the
// cluster id every route in this domain resolves its permission from must be the
// path's own, and must be a UUID.
func TestAccessIdentifiersAreClusterUUIDs(t *testing.T) {
	for key := range accessLegacyPermissions {
		method, path, _ := strings.Cut(key, " ")
		e := declaredEndpoint(t, method, path)
		prop, ok := e.Parameters["cluster_id"]
		if !ok {
			t.Errorf("%s declares no cluster_id", key)
			continue
		}
		if prop.Format != "uuid" {
			t.Errorf("%s: cluster_id declares format %q, want uuid", key, prop.Format)
		}
		if prop.Source != apischema.SourcePath {
			t.Errorf("%s: cluster_id declares source %q, want %q — the gate reads it from the path",
				key, prop.Source, apischema.SourcePath)
		}
	}
}

// TestEveryAccessEndpointIsDocumented holds the declaration-is-the-documentation
// rule for this domain: a route that cannot say what it does in one line is not
// ready to ship, and a Group is what puts it in the right section of the docs.
func TestEveryAccessEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredAccessEndpoints(t) {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no Description", key)
		}
		if e.Group != "Proxmox Access Control" {
			t.Errorf("%s declares group %q, want %q", key, e.Group, "Proxmox Access Control")
		}
	}
}
