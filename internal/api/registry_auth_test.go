package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// authRouteCount is how many endpoints registerAuthEndpoints declares: 13 of
// AuthHandler's 15. Register and Logout stay legacy — see
// TestAuthOptionalRoutesAreStillLegacy. See vmRouteCount in
// registry_vms_test.go for why the registry total is a sum of per-domain
// constants.
const authRouteCount = 13

const testSessionID = "a7b8c9d0-0000-4000-8000-000000000088"

// authPublicRoutes are the five that run before a session exists.
var authPublicRoutes = []string{
	"POST /api/v1/auth/login",
	"POST /api/v1/auth/refresh",
	"GET /api/v1/auth/setup-status",
	"GET /api/v1/auth/sso-status",
	"POST /api/v1/auth/oidc/token-exchange",
}

// authSelfServiceRoutes are the seven that act on the CALLER's own account.
var authSelfServiceRoutes = []string{
	"POST /api/v1/auth/logout-all",
	"GET /api/v1/auth/sessions",
	"DELETE /api/v1/auth/sessions/:id",
	"POST /api/v1/auth/ws-token",
	"GET /api/v1/auth/me",
	"PUT /api/v1/auth/profile",
	"POST /api/v1/auth/change-password",
}

// authConsoleTokenKey is the one Deferred route in this domain.
const authConsoleTokenKey = "POST /api/v1/auth/console-token"

// authLegacyPermissions is what each handler checked BEFORE this migration,
// transcribed from `git show HEAD:internal/api/handlers/auth.go` and
// `…/auth_sessions.go` at commit 357be6f.
//
// Exactly ONE of the thirteen made a permission call — ConsoleToken, with a
// requireClusterPerm whose ACTION is fixed ("console") and whose RESOURCE is
// chosen by the body — and it does not hoist, so the tally is 1 call in, 0
// hoisted, 1 kept. The other twelve checked nothing: five ran with no session
// at all and seven took their subject from c.Locals("user_id").
var authLegacyPermissions = map[string]string{
	"POST /api/v1/auth/login":               "",
	"POST /api/v1/auth/refresh":             "",
	"GET /api/v1/auth/setup-status":         "",
	"GET /api/v1/auth/sso-status":           "",
	"POST /api/v1/auth/oidc/token-exchange": "",
	"POST /api/v1/auth/logout-all":          "",
	"GET /api/v1/auth/sessions":             "",
	"DELETE /api/v1/auth/sessions/:id":      "",
	"POST /api/v1/auth/ws-token":            "",
	"GET /api/v1/auth/me":                   "",
	"PUT /api/v1/auth/profile":              "",
	"POST /api/v1/auth/change-password":     "",
	authConsoleTokenKey:                     "console",
}

// authRoutesOutsideTheClusterCheckShape is every route in this domain. None is
// a plain cluster-scoped Check, and the console mint — which IS about one
// cluster — cannot be one either, because the cluster arrives in the body.
var authRoutesOutsideTheClusterCheckShape = map[string]string{
	"POST /api/v1/auth/login":               "Public: it issues the session, so there is none to scope",
	"POST /api/v1/auth/refresh":             "Public: the refresh cookie is the credential, not a session",
	"GET /api/v1/auth/setup-status":         "Public: the login page asks before any identity exists",
	"GET /api/v1/auth/sso-status":           "Public: the login page asks before any identity exists",
	"POST /api/v1/auth/oidc/token-exchange": "Public: it trades the one-time code for the session",
	"POST /api/v1/auth/logout-all":          "SelfService: the subject is the caller's own session",
	"GET /api/v1/auth/sessions":             "SelfService: the subject is the caller's own session",
	"DELETE /api/v1/auth/sessions/:id":      "SelfService: the subject is the caller's own session; ownership is checked in the handler",
	"POST /api/v1/auth/ws-token":            "SelfService: the subject is the caller's own session",
	"GET /api/v1/auth/me":                   "SelfService: the subject is the caller's own session",
	"PUT /api/v1/auth/profile":              "SelfService: the subject is the caller's own session",
	"POST /api/v1/auth/change-password":     "SelfService: the subject is the caller's own session",
	authConsoleTokenKey:                     "Deferred: the resource comes from the body's `type` and the cluster from the body too, since the path names none",
}

// declaredAuthEndpoints returns every declaration in this domain, keyed
// "METHOD path".
//
// It excludes the TOTP routes, which share the /auth prefix but belong to
// registry_totp.go's tally, and /auth/oidc/authorize, which belongs to
// registry_oidc.go's.
func declaredAuthEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if !strings.HasPrefix(e.Path, authScope+"/") && e.Path != authScope {
			continue
		}
		if strings.HasPrefix(e.Path, totpScope) || e.Path == oidcAuthorizePath {
			continue
		}
		out[e.Method+" "+e.Path] = e
	}
	return out
}

// TestAuthRoutesDeclareTheSamePermissionTheyEnforced is the tally: 1 permission
// call in, 0 hoisted, 1 kept — plus twelve routes that checked nothing and now
// declare which of the two reasons applies.
func TestAuthRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredAuthEndpoints(t)
	if len(declared) != authRouteCount {
		t.Fatalf("the registry declares %d auth routes, want %d", len(declared), authRouteCount)
	}
	if len(authLegacyPermissions) != authRouteCount {
		t.Fatalf("authLegacyPermissions has %d entries, want %d — the table must cover every migrated route",
			len(authLegacyPermissions), authRouteCount)
	}

	public, selfService, deferred := 0, 0, 0
	for key, action := range authLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s is not declared in the registry", key)
			continue
		}
		if action != "" {
			// The one route that DID check. It must stay Deferred: the action
			// is static but the resource is not, so a Check could not state it.
			if e.Permissions.Deferred == "" {
				t.Errorf("%s declares %q, but its resource is chosen by the request body — a Check "+
					"cannot state it", key, e.Permissions.Describe())
				continue
			}
			deferred++
			continue
		}
		switch {
		case e.Permissions.Public != "":
			public++
		case e.Permissions.SelfService != "":
			selfService++
		default:
			t.Errorf("%s declares %q; it checked no permission before the migration, so it is either "+
				"Public (no session) or SelfService (the caller's own identity) — and which one it is "+
				"decides whether a session is required at all", key, e.Permissions.Describe())
		}
	}
	if public != len(authPublicRoutes) || selfService != len(authSelfServiceRoutes) || deferred != 1 {
		t.Errorf("the tally is %d Public, %d SelfService and %d Deferred; want %d, %d and 1",
			public, selfService, deferred, len(authPublicRoutes), len(authSelfServiceRoutes))
	}

	for key := range declared {
		if _, listed := authLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in authLegacyPermissions — a new auth route must be added "+
				"to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestAuthPublicRoutesCarryNoSessionCheck pins the five routes served with no
// authentication at all, in both directions.
//
// Public is the one shape where getting it wrong in EITHER direction is
// visible: declaring it on a route that needs a session opens it to the
// internet, and failing to declare it on one that does not breaks login. The
// key must be in publicRoutes — registryPublicRouteKeys folds the declaration
// into TestGuard_PublicRoutesAreExpected's comparison — and the route must
// actually run for an anonymous caller.
func TestAuthPublicRoutesCarryNoSessionCheck(t *testing.T) {
	for _, key := range authPublicRoutes {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			if e.Permissions.Public == "" {
				t.Fatalf("declares %q, want Public", e.Permissions.Describe())
			}
			if e.Permissions.authenticated() {
				t.Error("Public must install no authentication middleware")
			}

			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			app := newRegistryApp(t, stubAuth(map[string]bool{}), gated)

			// A body that satisfies whatever the route requires, so the only
			// thing that can refuse the request is the session check.
			req := httptest.NewRequest(method, path, nil)
			switch key {
			case "POST /api/v1/auth/login":
				req = jsonRequest(method, path, `{"email":"someone@example.com","password":"x"}`)
			case "POST /api/v1/auth/oidc/token-exchange":
				req = jsonRequest(method, path, `{"code":"abc"}`)
			}
			status, env := send(t, app, req)
			if status != fiber.StatusNoContent {
				t.Fatalf("an anonymous caller got %d (%q), want the handler to run", status, env.Message)
			}
		})
	}
}

// TestAuthSelfServiceRoutesTakeNoSubject is the assertion SelfService actually
// needs.
//
// SelfService is the riskiest label in the vocabulary because the route IS
// authenticated: an IDOR — a subject read from the path, the query or the body
// rather than from the session — hides exactly there. Six of the seven declare
// no parameter at all; the seventh, the per-session revoke, names a SESSION id
// and not a user id, and its handler answers 404 for a session belonging to
// somebody else rather than acting on it. That 404 is the ownership check, and
// it is why the route can be self-service while naming something in its path.
func TestAuthSelfServiceRoutesTakeNoSubject(t *testing.T) {
	subjectish := []string{"user_id", "userid", "user", "email", "subject", "account_id"}

	for _, key := range authSelfServiceRoutes {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			if e.Permissions.SelfService == "" {
				t.Fatalf("declares %q, want SelfService", e.Permissions.Describe())
			}
			if !e.Permissions.authenticated() {
				t.Error("is not authenticated; SelfService means the session identifies the subject, so there must be one")
			}
			for _, name := range subjectish {
				if _, declared := e.Parameters[name]; declared {
					t.Errorf("declares a %q parameter — the subject must come from the session, not from the caller", name)
				}
			}

			// Only the per-session revoke may name anything in its path, and
			// only a session id.
			params := pathParamNames(e.Path)
			if key == "DELETE /api/v1/auth/sessions/:id" {
				if len(params) != 1 || params[0] != "id" {
					t.Errorf("the path carries %v, want just the session id", params)
				}
			} else if len(params) != 0 {
				t.Errorf("the path carries %v; a self-service route must not name a subject", params)
			}

			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := strings.ReplaceAll(path, ":id", testSessionID)

			// No grant is needed …
			app := newRegistryApp(t, stubAuth(map[string]bool{}), gated)
			req := authedRequest(method, target)
			switch key {
			case "PUT /api/v1/auth/profile":
				req = jsonRequest(method, target, `{"display_name":"Operator"}`)
				req.Header.Set("X-Test-User", "yes")
			case "POST /api/v1/auth/change-password":
				req = jsonRequest(method, target, `{"old_password":"a","new_password":"b"}`)
				req.Header.Set("X-Test-User", "yes")
			}
			status, env := send(t, app, req)
			if status != fiber.StatusNoContent {
				t.Fatalf("a caller holding no grant got %d (%q), want the handler to run", status, env.Message)
			}

			// … but a session is.
			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{}), gated)
			status, _ = send(t, app, httptest.NewRequest(method, target, nil))
			if status != fiber.StatusUnauthorized {
				t.Errorf("an anonymous caller got %d, want 401", status)
			}
			if cap.called {
				t.Error("the handler ran for a request carrying no session")
			}
		})
	}
}

// TestAuthExemptionReasonsMatchTheReviewedLists keeps one exemption to one
// justification: the inline reason and the reviewed list's reason must be the
// same sentence, for both shapes. Neither existing guard compares them.
func TestAuthExemptionReasonsMatchTheReviewedLists(t *testing.T) {
	for _, key := range authPublicRoutes {
		method, path, _ := strings.Cut(key, " ")
		e := declaredEndpoint(t, method, path)
		reviewed, listed := publicRoutes[key]
		if !listed {
			t.Errorf("%s declares Public but is not in publicRoutes", key)
			continue
		}
		if e.Permissions.Public != reviewed {
			t.Errorf("%s: the declared reason is %q but publicRoutes says %q — one exemption, one justification",
				key, e.Permissions.Public, reviewed)
		}
	}

	for _, key := range authSelfServiceRoutes {
		method, path, _ := strings.Cut(key, " ")
		e := declaredEndpoint(t, method, path)
		reviewed, listed := selfServiceRoutes[key]
		if !listed {
			t.Errorf("%s declares SelfService but is not in selfServiceRoutes", key)
			continue
		}
		if e.Permissions.SelfService != reviewed {
			t.Errorf("%s: the declared reason is %q but selfServiceRoutes says %q — one exemption, one justification",
				key, e.Permissions.SelfService, reviewed)
		}
	}
}

// TestAuthOptionalRoutesAreStillLegacy records the one vocabulary gap this
// tranche hit, so it stays a decision rather than becoming a gap.
//
// Register and Logout are mounted with authOptional: the session is parsed IF
// one is presented, and the request proceeds either way. Permissions has no
// shape for that. Public installs NO authentication middleware, so
// c.Locals("role") would never be set and Register would refuse every
// admin-created account after the first; every other shape requires a session,
// which would 401 the logout a valid refresh cookie must still be able to
// perform after the access token has expired.
//
// Pinned from both sides: the routes must still be registered and public, and
// they must NOT be in the registry — a well-meaning later declaration of either
// is a silent behaviour change, not a compile error.
func TestAuthOptionalRoutesAreStillLegacy(t *testing.T) {
	keys := []string{"POST /api/v1/auth/register", "POST /api/v1/auth/logout"}

	s := newRouteStubServer(t)
	inRegistry := registryRouteKeySet(s.registry.Endpoints())
	registered := map[string]bool{}
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		registered[r.Method+" "+normalizeRoutePath(r.Path)] = true
	}

	for _, key := range keys {
		if inRegistry[key] {
			t.Errorf("%s is declared in the registry, but it is mounted with authOptional and the "+
				"Permissions vocabulary cannot express that — see registerAuthEndpoints", key)
		}
		if !registered[key] {
			t.Errorf("%s is not registered at all — it is meant to stay in router.go, not to disappear", key)
		}
		if _, public := publicRoutes[key]; !public {
			t.Errorf("%s is not in publicRoutes; authOptional does not require a session", key)
		}
	}
}

// probeAuthEndpoint is a declared auth endpoint with its handler swapped for a
// capture and its gate removed, so a parameter test needs neither a database
// nor a session.
func probeAuthEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestConsoleTokenClusterIsNotNamedClusterID is the escalation guard this
// route's declaration had to route around, recorded so the workaround is not
// mistaken for an arbitrary name.
//
// checkPathParams refuses a parameter NAMED cluster_id that resolves to
// anything but the path, because clusterIDFromParam reads that name to decide
// which cluster a permission gate authorizes — so a body parameter of that name
// is a name the gate also reads. The cluster here genuinely arrives in the body
// (the path names none), so it is declared under a different name with
// "cluster_id" as an ALIAS, which every existing caller keeps sending.
func TestConsoleTokenClusterIsNotNamedClusterID(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, authScope+"/console-token")

	if _, declared := e.Parameters["cluster_id"]; declared {
		t.Fatal("declares a parameter NAMED cluster_id; the permission middleware reads that name out " +
			"of the path, so a body parameter of it is a name the gate also reads")
	}
	prop, ok := e.Parameters["console_cluster_id"]
	if !ok {
		t.Fatal("declares no console_cluster_id")
	}
	if prop.Alias != "cluster_id" {
		t.Errorf("console_cluster_id declares alias %q, want \"cluster_id\" — that is the spelling every "+
			"caller sends", prop.Alias)
	}
	if prop.Format != "uuid" {
		t.Errorf("console_cluster_id declares format %q, want uuid", prop.Format)
	}

	// End to end on the spelling the console code sends.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeAuthEndpoint(t, fiber.MethodPost, authScope+"/console-token", cap))
	target := authScope + "/console-token"

	status, env := send(t, app, jsonRequest(http.MethodPost, target,
		`{"cluster_id":"`+testClusterID+`","node":"pve-01","type":"node_shell"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — the alias is what keeps the console code working", status, env.Message)
	}
	if got := cap.params.String("console_cluster_id"); got != testClusterID {
		t.Errorf("console_cluster_id = %q, want %q — the alias did not bind", got, testClusterID)
	}

	// Both spellings at once is a collision Validate reports rather than one
	// of them silently winning.
	cap.called = false
	status, _ = send(t, app, jsonRequest(http.MethodPost, target,
		`{"cluster_id":"`+testClusterID+`","console_cluster_id":"`+testClusterID+`","node":"pve-01","type":"node_shell"}`))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 when both spellings are sent", status)
	}
}

// TestConsoleTokenTypeVocabularyIsClosed pins the enum that decides which
// permission the handler asks for.
//
// The type selects the RBAC resource: node_shell -> node, vm_serial/vm_vnc ->
// vm, ct_attach/ct_vnc -> container. A type outside the set fell through to the
// handler's default branch and answered 400; it now does so at the schema, and
// the set is closed either way. Losing an entry would make a console
// unreachable; gaining one would reach the default branch with no resource
// chosen.
func TestConsoleTokenTypeVocabularyIsClosed(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, authScope+"/console-token")
	want := []string{"node_shell", "vm_serial", "vm_vnc", "ct_attach", "ct_vnc"}
	got := e.Parameters["type"].Enum
	if len(got) != len(want) {
		t.Fatalf("type declares enum %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("type declares enum %v, want %v", got, want)
		}
	}
	if e.Parameters["type"].Optional {
		t.Error("type is optional; every console mint has always had to say which console")
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeAuthEndpoint(t, fiber.MethodPost, authScope+"/console-token", cap))
	target := authScope + "/console-token"

	for _, body := range []string{
		`{"cluster_id":"` + testClusterID + `","node":"pve-01"}`,
		`{"cluster_id":"` + testClusterID + `","node":"pve-01","type":"spice"}`,
		`{"cluster_id":"` + testClusterID + `","node":"pve-01","type":""}`,
	} {
		cap.called = false
		status, env := send(t, app, jsonRequest(http.MethodPost, target, body))
		if status != fiber.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", body, status, env.Message)
		}
		if cap.called {
			t.Errorf("%s: the handler ran for a console type outside the vocabulary", body)
		}
	}
}

// TestConsoleTokenVMIDAcceptsZero records why vmid's minimum is 0 rather than
// 1, which every other VMID parameter in the registry uses.
//
// 0 is how a node_shell request spells "no guest" — the handler REFUSES a
// non-zero vmid for node_shell and a non-positive one for a guest console, and
// both rules are cross-field with `type`. A minimum of 1 would make the schema
// refuse the node-shell shape the console page sends.
func TestConsoleTokenVMIDAcceptsZero(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, authScope+"/console-token")
	prop := e.Parameters["vmid"]
	if !prop.Optional {
		t.Error("vmid is required, but a node_shell mint sends none")
	}
	if prop.Minimum == nil || *prop.Minimum != 0 {
		t.Errorf("vmid declares minimum %v, want 0 — a node_shell request omits it and the handler reads 0",
			prop.Minimum)
	}
	if prop.Maximum == nil {
		t.Error("vmid declares no maximum")
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeAuthEndpoint(t, fiber.MethodPost, authScope+"/console-token", cap))
	target := authScope + "/console-token"

	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"node shell omits vmid", `{"cluster_id":"` + testClusterID + `","node":"pve-01","type":"node_shell"}`, fiber.StatusNoContent},
		{"guest console sends one", `{"cluster_id":"` + testClusterID + `","node":"pve-01","type":"vm_vnc","vmid":101}`, fiber.StatusNoContent},
		{"negative is refused", `{"cluster_id":"` + testClusterID + `","node":"pve-01","type":"vm_vnc","vmid":-1}`, fiber.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cap.called = false
			status, env := send(t, app, jsonRequest(http.MethodPost, target, tc.body))
			if status != tc.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tc.want)
			}
		})
	}
}

// TestAuthCredentialsAreBodyOnly pins the four parameters in this domain that
// carry a secret.
//
// A credential in a query string is recorded by every proxy access log and
// leaks through Referer. ResolveSource reads an unsourced parameter on a
// mutating verb out of the body, which is what each of these relies on — so
// what this asserts is that none of them has acquired an explicit query source,
// and that the request still refuses one sent that way.
func TestAuthCredentialsAreBodyOnly(t *testing.T) {
	cases := []struct {
		method, path string
		names        []string
	}{
		{fiber.MethodPost, authScope + "/login", []string{"password"}},
		{fiber.MethodPost, authScope + "/refresh", []string{"refresh_token"}},
		{fiber.MethodPost, authScope + "/change-password", []string{"old_password", "new_password"}},
	}

	for _, tc := range cases {
		e := declaredEndpoint(t, tc.method, tc.path)
		for _, name := range tc.names {
			prop, ok := e.Parameters[name]
			if !ok {
				t.Errorf("%s declares no %q parameter", tc.path, name)
				continue
			}
			if src := apischema.ResolveSource(name, prop, tc.method, e.pathParams); src != apischema.SourceBody {
				t.Errorf("%s: %q resolves to %q, want %q — a credential must never travel in a URL",
					tc.path, name, src, apischema.SourceBody)
			}
		}
	}

	// And a password sent as a query parameter is refused rather than read.
	cap := &capture{}
	path := authScope + "/login"
	app := newRegistryApp(t, noAuth(), probeAuthEndpoint(t, fiber.MethodPost, path, cap))
	status, _ := send(t, app, jsonRequest(http.MethodPost, path+"?password=hunter2",
		`{"email":"someone@example.com","password":"hunter2"}`))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a password sent as a query parameter", status)
	}
	if cap.called {
		t.Error("the handler ran for a password sent in the URL")
	}
}

// TestAuthLoginEmailIsNotFormatValidated records a narrowing that was
// deliberately NOT made.
//
// `email` here is a LOOKUP key matched against whatever is stored, including
// accounts provisioned from a directory. Every registered apischema format
// refuses more than it accepts; an "email" format on this parameter would lock
// out an account whose address Nexara itself accepted at creation, and it would
// do so at login, with a message about a format.
func TestAuthLoginEmailIsNotFormatValidated(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, authScope+"/login")
	prop := e.Parameters["email"]
	if prop.Format != "" {
		t.Errorf("email declares format %q; it is a lookup key matched against what is stored, not a "+
			"new address being accepted", prop.Format)
	}
	if prop.Optional {
		t.Error("email is optional, but login has always refused a request without it")
	}
	if prop.MinLength == nil || *prop.MinLength < 1 {
		t.Error("email declares no MinLength; the handler's `== \"\"` check refused a blank one too")
	}

	cap := &capture{}
	path := authScope + "/login"
	app := newRegistryApp(t, noAuth(), probeAuthEndpoint(t, fiber.MethodPost, path, cap))
	for _, body := range []string{`{}`, `{"email":"a@example.com"}`, `{"password":"x"}`, `{"email":"","password":"x"}`} {
		cap.called = false
		status, env := send(t, app, jsonRequest(http.MethodPost, path, body))
		if status != fiber.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", body, status, env.Message)
		}
		if cap.called {
			t.Errorf("%s: the handler ran for incomplete credentials", body)
		}
	}
}

// TestAuthRefreshAcceptsAnEmptyBody is the compatibility assertion for the one
// route whose body has always been optional: browsers post "{}" and rely on the
// HttpOnly cookie.
func TestAuthRefreshAcceptsAnEmptyBody(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, authScope+"/refresh")
	if !e.Parameters["refresh_token"].Optional {
		t.Fatal("refresh_token is required; the browser path carries the token in a cookie, not the body")
	}

	cap := &capture{}
	path := authScope + "/refresh"
	app := newRegistryApp(t, noAuth(), probeAuthEndpoint(t, fiber.MethodPost, path, cap))

	status, env := send(t, app, jsonRequest(http.MethodPost, path, `{}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 for the empty body the SPA sends", status, env.Message)
	}
	if _, supplied := cap.params.OptString("refresh_token"); supplied {
		t.Error("refresh_token reads as supplied although the request never sent it")
	}
}

// TestEveryAuthEndpointIsDocumented holds the declaration-is-the-documentation
// rule for this domain.
func TestEveryAuthEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredAuthEndpoints(t) {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no Description", key)
		}
		if e.Group != "Authentication" {
			t.Errorf("%s declares group %q, want %q", key, e.Group, "Authentication")
		}
	}
	// The one Deferred route renders as the bare word "deferred" in the docs,
	// so the permissions an operator needs have to be in its Description.
	console := declaredEndpoint(t, fiber.MethodPost, authScope+"/console-token")
	for _, want := range []string{"console:node", "console:vm", "console:container"} {
		if !strings.Contains(console.Description, want) {
			t.Errorf("the console-token Description does not name %q: %q", want, console.Description)
		}
	}
}
