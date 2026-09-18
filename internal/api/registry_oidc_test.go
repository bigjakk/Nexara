package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// oidcRouteCount is how many endpoints registerOIDCEndpoints declares: the six
// admin CRUD routes plus /auth/oidc/authorize. The eighth OIDCHandler route,
// the provider CALLBACK, stays legacy — see TestOIDCCallbackIsStillLegacy. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum of
// per-domain constants.
const oidcRouteCount = 7

const testOIDCConfigID = "f6a7b8c9-0000-4000-8000-000000000077"

// oidcLegacyPermissions is what each handler checked with a hand-placed
// requirePerm call BEFORE this migration, transcribed from
// `git show HEAD:internal/api/handlers/oidc.go` at commit 357be6f.
//
// The six admin routes each made exactly one static, unconditional
// requirePerm(c, "manage", "user") call. Authorize made NONE and was mounted
// without authRequired, so it is listed with an empty permission: it is the
// Public route, and listing it is what makes this tally cover all 7.
var oidcLegacyPermissions = map[string]string{
	"GET /api/v1/oidc/configs":           "manage:user",
	"POST /api/v1/oidc/configs":          "manage:user",
	"GET /api/v1/oidc/configs/:id":       "manage:user",
	"PUT /api/v1/oidc/configs/:id":       "manage:user",
	"DELETE /api/v1/oidc/configs/:id":    "manage:user",
	"POST /api/v1/oidc/configs/:id/test": "manage:user",
	"GET /api/v1/auth/oidc/authorize":    "",
}

// oidcRoutesOutsideTheClusterCheckShape is every route in this domain: a
// provider configuration decides who may sign in to the WHOLE install, and the
// authorize route runs before any identity exists at all.
var oidcRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/oidc/configs":           "a provider config is instance-wide; the path names no cluster",
	"POST /api/v1/oidc/configs":          "a provider config is instance-wide; the path names no cluster",
	"GET /api/v1/oidc/configs/:id":       "a provider config is instance-wide; the path names no cluster",
	"PUT /api/v1/oidc/configs/:id":       "a provider config is instance-wide; the path names no cluster",
	"DELETE /api/v1/oidc/configs/:id":    "a provider config is instance-wide; the path names no cluster",
	"POST /api/v1/oidc/configs/:id/test": "a provider config is instance-wide; the path names no cluster",
	"GET /api/v1/auth/oidc/authorize":    "Public: it runs before any identity exists, so there is nothing to scope",
}

// declaredOIDCEndpoints returns every declaration in this domain, keyed
// "METHOD path".
func declaredOIDCEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, oidcConfigScope) || e.Path == oidcAuthorizePath {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestOIDCRoutesDeclareTheSamePermissionTheyEnforced is the tally: 6
// hand-placed calls in, 6 declared global Checks out, none kept — plus the one
// route that was already anonymous and is declared Public instead.
func TestOIDCRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredOIDCEndpoints(t)
	if len(declared) != oidcRouteCount {
		t.Fatalf("the registry declares %d OIDC routes, want %d", len(declared), oidcRouteCount)
	}
	if len(oidcLegacyPermissions) != oidcRouteCount {
		t.Fatalf("oidcLegacyPermissions has %d entries, want %d — the table must cover every migrated route",
			len(oidcLegacyPermissions), oidcRouteCount)
	}

	hoisted := 0
	for key, want := range oidcLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if want == "" {
			if e.Permissions.Public == "" {
				t.Errorf("%s declares %q; it was mounted without authRequired, so Public is the shape "+
					"that describes it — and the only one TestGuard_PublicRoutesAreExpected can review",
					key, e.Permissions.Describe())
			}
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check — it made one static, unconditional requirePerm call",
				key, e.Permissions.Describe())
			continue
		}
		hoisted++
		if e.Permissions.Describe() != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration",
				key, e.Permissions.Describe(), want)
		}
		if e.Permissions.Check.Scope != ScopeGlobal {
			t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, ScopeGlobal)
		}
	}
	if want := oidcRouteCount - 1; hoisted != want {
		t.Errorf("the tally moves %d permission call(s) into middleware, want %d", hoisted, want)
	}

	for key := range declared {
		if _, listed := oidcLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in oidcLegacyPermissions — a new OIDC route must be added "+
				"to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestOIDCAdminRoutesAreGatedByTheirDeclaration proves the 6 hoisted
// permissions are the permissions the routes enforce, end to end.
func TestOIDCAdminRoutesAreGatedByTheirDeclaration(t *testing.T) {
	for key, want := range oidcLegacyPermissions {
		if want == "" {
			continue // Public; covered by TestOIDCAuthorizeIsPublicAndReviewed
		}
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := strings.ReplaceAll(path, ":id", testOIDCConfigID)

			app := newRegistryApp(t, stubAuth(map[string]bool{"view:user": true}), gated)
			status, _ := send(t, app, authedRequest(method, target))
			if status != fiber.StatusForbidden {
				t.Fatalf("a caller holding only view:user got %d, want 403", status)
			}
			if cap.called {
				t.Error("the handler ran for a caller without manage:user")
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{"manage:user": true}), gated)
			status, _ = send(t, app, authedRequest(method, target))
			if status == fiber.StatusForbidden {
				t.Fatal("a caller holding manage:user got 403")
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{"manage:user": true}), gated)
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

// TestOIDCAuthorizeIsPublicAndReviewed pins the one route here that is served
// with no session at all.
//
// Public is a deliberate edit to a reviewed list, not an inline decision: the
// key has to be in publicRoutes (registryPublicRouteKeys folds the declaration
// into TestGuard_PublicRoutesAreExpected's comparison, in BOTH directions), and
// the reason has to be the one that list already carries. Restating it here
// keeps the two from drifting into two different justifications for the same
// exemption.
func TestOIDCAuthorizeIsPublicAndReviewed(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, oidcAuthorizePath)

	if e.Permissions.Public == "" {
		t.Fatalf("declares %q, want Public", e.Permissions.Describe())
	}
	key := fiber.MethodGet + " " + oidcAuthorizePath
	reviewed, listed := publicRoutes[key]
	if !listed {
		t.Fatalf("%s declares Public but is not in publicRoutes", key)
	}
	if e.Permissions.Public != reviewed {
		t.Errorf("the declared reason is %q but publicRoutes says %q — one exemption, one justification",
			e.Permissions.Public, reviewed)
	}
	if e.Permissions.authenticated() {
		t.Error("Public must install no authentication middleware")
	}

	// End to end: no session, no grant, and the handler still runs.
	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	app := newRegistryApp(t, stubAuth(map[string]bool{}), gated)
	status, _ := send(t, app, httptest.NewRequest(http.MethodGet, oidcAuthorizePath, nil))
	if status != fiber.StatusNoContent {
		t.Errorf("an anonymous caller got %d, want the handler to run — the login page calls this "+
			"before any identity exists", status)
	}
}

// TestOIDCCallbackIsStillLegacy records a decision, not a gap.
//
// The provider callback is the first route in this migration that the registry
// cannot express for a reason other than a parameter TYPE. Its query string is
// composed by the identity provider: RFC 9207 adds `iss`, OIDC session
// management adds `session_state`, an error response carries `error` and
// `error_description`, and nothing stops a provider adding its own. The
// registry answers an undeclared key with a 400, so any declaration — however
// generous — turns "this provider sends one extra parameter" into "SSO login
// returns 400", at the moment a user is trying to sign in and with nothing in
// the response to point at.
//
// It is pinned from both sides: the route must still be registered, and it must
// NOT be in the registry.
func TestOIDCCallbackIsStillLegacy(t *testing.T) {
	const key = "GET /api/v1/auth/oidc/callback"

	s := newRouteStubServer(t)
	if registryRouteKeySet(s.registry.Endpoints())[key] {
		t.Fatalf("%s is declared in the registry, but its query string is composed by the identity "+
			"provider and an undeclared key is a 400 — see registerOIDCEndpoints", key)
	}

	registered := false
	for _, r := range s.app.GetRoutes(true) {
		if r.Method+" "+normalizeRoutePath(r.Path) == key {
			registered = true
			break
		}
	}
	if !registered {
		t.Errorf("%s is not registered at all — it is meant to stay in router.go, not to disappear", key)
	}
	if _, public := publicRoutes[key]; !public {
		t.Errorf("%s is not in publicRoutes; the provider calls it with no session", key)
	}
}

// probeOIDCEndpoint is a declared OIDC endpoint with its handler swapped for a
// capture and its gate removed, so a parameter test needs neither a database
// nor a session.
func probeOIDCEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// oidcWriteRoutes are the two routes that share the config body.
var oidcWriteRoutes = []struct{ method, path string }{
	{fiber.MethodPost, oidcConfigScope},
	{fiber.MethodPut, oidcConfigScope + "/:id"},
}

// TestOIDCWriteBodyShape pins the parts of the shared body that a migration
// could change without anyone noticing.
//
//   - issuer_url and client_id are required and non-blank, which is the
//     handler's own `== ""` pair moved out.
//   - the booleans default to false, because they were non-pointer bools on a
//     full-body write and requireOIDCRedirectSchemeAck is written against that.
//   - redirect_uri stays OPTIONAL, deliberately: validateOIDCRedirectURI
//     refuses an empty one with a message naming the callback path this install
//     serves, which is more use to an operator than "redirect_uri is required".
func TestOIDCWriteBodyShape(t *testing.T) {
	for _, route := range oidcWriteRoutes {
		t.Run(route.method, func(t *testing.T) {
			e := declaredEndpoint(t, route.method, route.path)

			for _, name := range []string{"issuer_url", "client_id"} {
				prop := e.Parameters[name]
				if prop.Optional {
					t.Errorf("%q is optional, but both writes have always refused a request without it", name)
				}
				if prop.MinLength == nil || *prop.MinLength < 1 {
					t.Errorf("%q declares no MinLength; the handler's `== \"\"` check refused a blank value too", name)
				}
			}
			for _, name := range []string{"enabled", "auto_provision", "acknowledge_insecure_redirect"} {
				prop := e.Parameters[name]
				if !prop.Optional {
					t.Errorf("%q is required; the body has always accepted a request without it", name)
				}
				if prop.Default != false {
					t.Errorf("%q declares default %v, want false — an omitted boolean on this full-body "+
						"write has always meant false", name, prop.Default)
				}
			}
			if !e.Parameters["redirect_uri"].Optional {
				t.Error("redirect_uri is required; validateOIDCRedirectURI answers an empty one with a " +
					"message that names the callback path, which is the more useful refusal")
			}
		})
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeOIDCEndpoint(t, fiber.MethodPost, oidcConfigScope, cap))
	for _, body := range []string{
		`{}`,
		`{"issuer_url":"https://idp.example.com"}`,
		`{"client_id":"nexara"}`,
		`{"issuer_url":"","client_id":"nexara"}`,
		`{"issuer_url":"https://idp.example.com","client_id":""}`,
	} {
		cap.called = false
		status, env := send(t, app, jsonRequest(http.MethodPost, oidcConfigScope, body))
		if status != fiber.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", body, status, env.Message)
		}
		if cap.called {
			t.Errorf("%s: the handler ran for a body missing a required field", body)
		}
	}
}

// TestOIDCArrayParametersCarryScalarItems pins the two array parameters this
// domain carries.
//
// Both are arrays of SCALARS, which is what makes this domain declarable at
// all: apischema's Property.Items is restricted to scalar element types
// (compileItems), and the routes elsewhere that stayed legacy did so because
// their arrays hold objects. An Items entry that went missing would silently
// stop bounding the elements.
func TestOIDCArrayParametersCarryScalarItems(t *testing.T) {
	for _, route := range oidcWriteRoutes {
		e := declaredEndpoint(t, route.method, route.path)
		for _, name := range []string{"scopes", "allowed_domains"} {
			prop := e.Parameters[name]
			if prop.Type != apischema.Array {
				t.Errorf("%s: %q declares type %q, want %q", route.method, name, prop.Type, apischema.Array)
				continue
			}
			if prop.Items == nil {
				t.Errorf("%s: %q declares no Items, so nothing bounds its elements", route.method, name)
				continue
			}
			if prop.Items.Type != apischema.String {
				t.Errorf("%s: %q declares element type %q, want %q", route.method, name, prop.Items.Type, apischema.String)
			}
		}
	}

	// End to end on the shapes the admin page sends, including the one-element
	// spelling a query string would produce.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeOIDCEndpoint(t, fiber.MethodPost, oidcConfigScope, cap))
	const base = `"issuer_url":"https://idp.example.com","client_id":"nexara"`

	status, env := send(t, app, jsonRequest(http.MethodPost, oidcConfigScope,
		`{`+base+`,"scopes":["openid","email"],"allowed_domains":["example.com"]}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.Strings("scopes"); len(got) != 2 || got[0] != "openid" || got[1] != "email" {
		t.Errorf("scopes = %v, want [openid email]", got)
	}

	// An omitted array reads as absent, which is what lets the handler apply
	// its own openid/email/profile default rather than storing an empty set.
	cap.called = false
	status, env = send(t, app, jsonRequest(http.MethodPost, oidcConfigScope, `{`+base+`}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.Strings("scopes"); len(got) != 0 {
		t.Errorf("scopes = %v, want empty so the handler's own default applies", got)
	}
}

// TestOIDCClientSecretIsWriteOnly pins the one parameter in this domain that
// carries a credential.
func TestOIDCClientSecretIsWriteOnly(t *testing.T) {
	for _, route := range oidcWriteRoutes {
		e := declaredEndpoint(t, route.method, route.path)
		prop, ok := e.Parameters["client_secret"]
		if !ok {
			t.Errorf("%s declares no client_secret parameter", route.method)
			continue
		}
		if !prop.Optional {
			t.Errorf("%s: client_secret is required; a public client has none", route.method)
		}
		if src := apischema.ResolveSource("client_secret", prop, route.method, nil); src != apischema.SourceBody {
			t.Errorf("%s: client_secret resolves to %q, want %q — a credential must never travel in a URL",
				route.method, src, apischema.SourceBody)
		}
	}

	for _, route := range []struct{ method, path string }{
		{fiber.MethodGet, oidcConfigScope},
		{fiber.MethodGet, oidcConfigScope + "/:id"},
	} {
		if _, declared := declaredEndpoint(t, route.method, route.path).Parameters["client_secret"]; declared {
			t.Errorf("%s %s declares a client_secret parameter; a read takes none", route.method, route.path)
		}
	}
}

// TestOIDCDefaultRoleIDAcceptsEmptyOrUUID is the LDAP assertion repeated for
// the field the two domains share: the admin page sends null for "none", and an
// empty string has always meant the same thing, which every registered format
// would refuse.
func TestOIDCDefaultRoleIDAcceptsEmptyOrUUID(t *testing.T) {
	for _, route := range oidcWriteRoutes {
		prop := declaredEndpoint(t, route.method, route.path).Parameters["default_role_id"]
		if prop.Format != "" {
			t.Errorf("%s: default_role_id declares format %q; that refuses \"\"", route.method, prop.Format)
		}
		if prop.Pattern == "" {
			t.Errorf("%s: default_role_id declares no pattern, so any string reaches uuid.Parse", route.method)
		}
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeOIDCEndpoint(t, fiber.MethodPost, oidcConfigScope, cap))
	const base = `"issuer_url":"https://idp.example.com","client_id":"nexara"`
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"null", `{` + base + `,"default_role_id":null}`, fiber.StatusNoContent},
		{"empty", `{` + base + `,"default_role_id":""}`, fiber.StatusNoContent},
		{"a uuid", `{` + base + `,"default_role_id":"` + testRoleID + `"}`, fiber.StatusNoContent},
		{"not a uuid", `{` + base + `,"default_role_id":"none"}`, fiber.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cap.called = false
			status, env := send(t, app, jsonRequest(http.MethodPost, oidcConfigScope, tc.body))
			if status != tc.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tc.want)
			}
		})
	}
}

// TestOIDCGroupRoleMappingRejectsNonStringValues is the LDAP narrowing repeated
// for the field the two domains share. See its counterpart for why losing it
// would silently strip every user's group-derived roles at login.
func TestOIDCGroupRoleMappingRejectsNonStringValues(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, oidcConfigScope)
	if e.Parameters["group_role_mapping"].Type != apischema.Object {
		t.Fatalf("group_role_mapping declares type %q, want %q",
			e.Parameters["group_role_mapping"].Type, apischema.Object)
	}

	e.Permissions = Permissions{SelfService: "fixture; authorization is exercised separately"}
	app := newRegistryApp(t, noAuth(), e)
	status, env := send(t, app, jsonRequest(http.MethodPost, oidcConfigScope,
		`{"issuer_url":"https://idp.example.com","client_id":"nexara","group_role_mapping":{"ops":7}}`))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d (%q), want 400", status, env.Message)
	}
	if !strings.Contains(env.Message, "group_role_mapping") {
		t.Errorf("the rejection does not name the parameter: %q", env.Message)
	}
}

// TestEveryOIDCEndpointIsDocumented holds the declaration-is-the-documentation
// rule for this domain.
func TestEveryOIDCEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredOIDCEndpoints(t) {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no Description", key)
		}
		if e.Group != "Authentication" {
			t.Errorf("%s declares group %q, want %q", key, e.Group, "Authentication")
		}
	}
}
