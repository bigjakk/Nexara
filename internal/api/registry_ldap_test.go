package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// ldapRouteCount is how many endpoints registerLDAPEndpoints declares. It is
// ALL 7 of LDAPHandler's routes; nothing in this domain was left legacy. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum of
// per-domain constants.
const ldapRouteCount = 7

const testLDAPConfigID = "e5f6a7b8-0000-4000-8000-000000000066"

// ldapLegacyPermissions is what each handler checked with a hand-placed
// requirePerm call BEFORE this migration, transcribed from
// `git show HEAD:internal/api/handlers/ldap.go` at commit 357be6f.
//
// All seven made exactly one static, unconditional requirePerm(c, "manage",
// "user") call as their first statement: 7 in, 7 out, none kept.
var ldapLegacyPermissions = map[string]string{
	"GET /api/v1/ldap/configs":           "manage:user",
	"POST /api/v1/ldap/configs":          "manage:user",
	"GET /api/v1/ldap/configs/:id":       "manage:user",
	"PUT /api/v1/ldap/configs/:id":       "manage:user",
	"DELETE /api/v1/ldap/configs/:id":    "manage:user",
	"POST /api/v1/ldap/configs/:id/test": "manage:user",
	"POST /api/v1/ldap/configs/:id/sync": "manage:user",
}

// ldapRoutesOutsideTheClusterCheckShape is every route in this domain: a
// directory configuration decides who may sign in to the WHOLE install, so no
// path here names a cluster.
var ldapRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/ldap/configs":           "a directory config is instance-wide; the path names no cluster",
	"POST /api/v1/ldap/configs":          "a directory config is instance-wide; the path names no cluster",
	"GET /api/v1/ldap/configs/:id":       "a directory config is instance-wide; the path names no cluster",
	"PUT /api/v1/ldap/configs/:id":       "a directory config is instance-wide; the path names no cluster",
	"DELETE /api/v1/ldap/configs/:id":    "a directory config is instance-wide; the path names no cluster",
	"POST /api/v1/ldap/configs/:id/test": "a directory config is instance-wide; the path names no cluster",
	"POST /api/v1/ldap/configs/:id/sync": "a directory config is instance-wide; the path names no cluster",
}

// declaredLDAPEndpoints returns every declaration in this domain, keyed
// "METHOD path".
func declaredLDAPEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, ldapConfigScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestLDAPRoutesDeclareTheSamePermissionTheyEnforced is the tally: 7
// hand-placed calls in, 7 declared global Checks out, none kept.
func TestLDAPRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredLDAPEndpoints(t)
	if len(declared) != ldapRouteCount {
		t.Fatalf("the registry declares %d LDAP routes, want %d", len(declared), ldapRouteCount)
	}
	if len(ldapLegacyPermissions) != ldapRouteCount {
		t.Fatalf("ldapLegacyPermissions has %d entries, want %d — the table must cover every migrated route",
			len(ldapLegacyPermissions), ldapRouteCount)
	}

	hoisted := 0
	for key, want := range ldapLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
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
	if hoisted != ldapRouteCount {
		t.Errorf("the tally moves %d permission call(s) into middleware, want all %d", hoisted, ldapRouteCount)
	}

	for key := range declared {
		if _, listed := ldapLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in ldapLegacyPermissions — a new LDAP route must be added "+
				"to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestLDAPRoutesAreGatedByTheirDeclaration proves the 7 hoisted permissions are
// the permissions the routes enforce, end to end.
//
// The unrelated grant is view:user, which every built-in Viewer holds — and
// which must NOT open a route that returns a directory's bind DN or re-points
// where every login's password is sent.
func TestLDAPRoutesAreGatedByTheirDeclaration(t *testing.T) {
	for key := range ldapLegacyPermissions {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := strings.ReplaceAll(path, ":id", testLDAPConfigID)

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

// probeLDAPEndpoint is a declared LDAP endpoint with its handler swapped for a
// capture and its gate removed, so a parameter test needs neither a database
// nor a session.
func probeLDAPEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// ldapWriteRoutes are the two routes that share the config body.
var ldapWriteRoutes = []struct{ method, path string }{
	{fiber.MethodPost, ldapConfigScope},
	{fiber.MethodPut, ldapConfigScope + "/:id"},
}

// TestLDAPTransportBooleansDefaultFalse is the assertion that this migration
// did NOT quietly improve something.
//
// start_tls and skip_tls_verify were non-pointer bools on a full-body PUT, so
// omitting start_tls has always turned StartTLS OFF — and
// requireLDAPTransportAck exists precisely to catch that accident and make the
// operator confirm it. Declaring them as tristates would read as a
// strictly-better schema while REMOVING the request the confirmation was
// written for: an omitted start_tls would stop meaning "off", so the downgrade
// would stop being detected and the prompt would stop firing.
func TestLDAPTransportBooleansDefaultFalse(t *testing.T) {
	for _, route := range ldapWriteRoutes {
		t.Run(route.method, func(t *testing.T) {
			e := declaredEndpoint(t, route.method, route.path)
			for _, name := range []string{"enabled", "start_tls", "skip_tls_verify", "acknowledge_insecure_tls"} {
				prop, ok := e.Parameters[name]
				if !ok {
					t.Errorf("declares no %q parameter", name)
					continue
				}
				if !prop.Optional {
					t.Errorf("%q is required; the body has always accepted a request without it", name)
				}
				if prop.Default != false {
					t.Errorf("%q declares default %v, want false — an omitted boolean on this full-body "+
						"write has always meant false, and the insecure-transport confirmation depends on it",
						name, prop.Default)
				}
			}
		})
	}

	// End to end: a body that never mentions start_tls hands the handler false,
	// which is the value the downgrade gate compares against the stored config.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeLDAPEndpoint(t, fiber.MethodPost, ldapConfigScope, cap))
	status, env := send(t, app, jsonRequest(http.MethodPost, ldapConfigScope,
		`{"server_url":"ldaps://ldap.example.com","search_base_dn":"dc=example,dc=com"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if cap.params.Bool("start_tls") {
		t.Error("start_tls reads true although the request never sent it")
	}
	if cap.params.Bool("skip_tls_verify") {
		t.Error("skip_tls_verify reads true although the request never sent it")
	}
}

// TestLDAPRequiredFieldsMatchWhatTheHandlerEnforced pins the one explicit check
// both writes made: server_url and search_base_dn, refused when empty.
//
// MinLength rather than mere presence, because `if req.X == ""` refused an
// EMPTY string too and a required-but-blank value would otherwise sail through.
func TestLDAPRequiredFieldsMatchWhatTheHandlerEnforced(t *testing.T) {
	for _, route := range ldapWriteRoutes {
		t.Run(route.method, func(t *testing.T) {
			e := declaredEndpoint(t, route.method, route.path)
			for _, name := range []string{"server_url", "search_base_dn"} {
				prop := e.Parameters[name]
				if prop.Optional {
					t.Errorf("%q is optional, but both writes have always refused a request without it", name)
				}
				if prop.MinLength == nil || *prop.MinLength < 1 {
					t.Errorf("%q declares no MinLength; the handler's `== \"\"` check refused a blank value too", name)
				}
			}
		})
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeLDAPEndpoint(t, fiber.MethodPost, ldapConfigScope, cap))
	for _, body := range []string{
		`{}`,
		`{"server_url":"ldaps://ldap.example.com"}`,
		`{"search_base_dn":"dc=example,dc=com"}`,
		`{"server_url":"","search_base_dn":"dc=example,dc=com"}`,
		`{"server_url":"ldaps://ldap.example.com","search_base_dn":""}`,
	} {
		cap.called = false
		status, env := send(t, app, jsonRequest(http.MethodPost, ldapConfigScope, body))
		if status != fiber.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", body, status, env.Message)
		}
		if cap.called {
			t.Errorf("%s: the handler ran for a body missing a required field", body)
		}
	}
}

// TestLDAPDefaultRoleIDAcceptsEmptyOrUUID records why default_role_id carries a
// PATTERN rather than the uuid format.
//
// The admin page sends null for "no default role" and the field used to be a
// *string whose "" was treated the same way. Every registered apischema format
// refuses the empty string, so declaring Format "uuid" would 400 a body that
// spells "none" as "" — a shape an API client can reasonably send, and one the
// handler has always accepted.
func TestLDAPDefaultRoleIDAcceptsEmptyOrUUID(t *testing.T) {
	for _, route := range ldapWriteRoutes {
		e := declaredEndpoint(t, route.method, route.path)
		prop := e.Parameters["default_role_id"]
		if prop.Format != "" {
			t.Errorf("%s: default_role_id declares format %q; that refuses \"\", which is how a caller "+
				"spells \"no default role\"", route.method, prop.Format)
		}
		if prop.Pattern == "" {
			t.Errorf("%s: default_role_id declares no pattern, so any string reaches uuid.Parse", route.method)
		}
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeLDAPEndpoint(t, fiber.MethodPost, ldapConfigScope, cap))
	const base = `"server_url":"ldaps://ldap.example.com","search_base_dn":"dc=example,dc=com"`

	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"absent", `{` + base + `}`, fiber.StatusNoContent},
		{"null", `{` + base + `,"default_role_id":null}`, fiber.StatusNoContent},
		{"empty", `{` + base + `,"default_role_id":""}`, fiber.StatusNoContent},
		{"a uuid", `{` + base + `,"default_role_id":"` + testRoleID + `"}`, fiber.StatusNoContent},
		{"not a uuid", `{` + base + `,"default_role_id":"none"}`, fiber.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cap.called = false
			status, env := send(t, app, jsonRequest(http.MethodPost, ldapConfigScope, tc.body))
			if status != tc.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tc.want)
			}
		})
	}
}

// TestLDAPGroupRoleMappingRejectsNonStringValues is the narrowing that had to be
// KEPT rather than lost.
//
// group_role_mapping was a map[string]string, so the JSON decoder refused a
// numeric value with a 400. apischema's Object type carries a nested value
// through unvalidated, so without handlers.StringMapFromObject such a body
// would be stored — and then silently decode to an EMPTY mapping at login time,
// because the read is json.Unmarshal into a map[string]string with its error
// discarded. Every user would quietly lose their group-derived roles.
func TestLDAPGroupRoleMappingRejectsNonStringValues(t *testing.T) {
	for _, route := range ldapWriteRoutes {
		e := declaredEndpoint(t, route.method, route.path)
		prop := e.Parameters["group_role_mapping"]
		if prop.Type != apischema.Object {
			t.Errorf("%s: group_role_mapping declares type %q, want %q", route.method, prop.Type, apischema.Object)
		}
		if !prop.Optional {
			t.Errorf("%s: group_role_mapping is required; the body has always accepted a request without it", route.method)
		}
	}

	// The declaration cannot express the element type, so the refusal is the
	// handler's — driven here through the REAL handler with nil queries, which
	// is enough because the refusal happens before the first DB call.
	e := declaredEndpoint(t, fiber.MethodPost, ldapConfigScope)
	e.Permissions = Permissions{SelfService: "fixture; authorization is exercised separately"}
	app := newRegistryApp(t, noAuth(), e)
	const base = `"server_url":"ldaps://ldap.example.com","search_base_dn":"dc=example,dc=com"`

	status, env := send(t, app, jsonRequest(http.MethodPost, ldapConfigScope,
		`{`+base+`,"group_role_mapping":{"cn=ops,dc=example,dc=com":7}}`))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d (%q), want 400 — a non-string value would be stored and then silently "+
			"decode to an empty mapping at login", status, env.Message)
	}
	if !strings.Contains(env.Message, "group_role_mapping") {
		t.Errorf("the rejection does not name the parameter: %q", env.Message)
	}
}

// TestLDAPSyncIntervalIsBoundedToInt32 pins the bound that replaced the JSON
// decoder's own overflow refusal.
//
// The field was an int32, so encoding/json answered 400 for a number that did
// not fit. apischema hands back an int64, and narrowing that to the int32
// column would WRAP instead — a caller asking for 4294967356 minutes would get
// 60, silently. The maximum is what keeps the old refusal.
func TestLDAPSyncIntervalIsBoundedToInt32(t *testing.T) {
	for _, route := range ldapWriteRoutes {
		e := declaredEndpoint(t, route.method, route.path)
		prop := e.Parameters["sync_interval_minutes"]
		if prop.Maximum == nil {
			t.Fatalf("%s: sync_interval_minutes declares no maximum; the column is int32 and the "+
				"narrowing would wrap", route.method)
		}
		if *prop.Maximum > 2147483647 {
			t.Errorf("%s: sync_interval_minutes declares maximum %v, which does not fit int32",
				route.method, *prop.Maximum)
		}
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeLDAPEndpoint(t, fiber.MethodPost, ldapConfigScope, cap))
	const base = `"server_url":"ldaps://ldap.example.com","search_base_dn":"dc=example,dc=com"`

	status, _ := send(t, app, jsonRequest(http.MethodPost, ldapConfigScope,
		`{`+base+`,"sync_interval_minutes":4294967356}`))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a value that does not fit int32", status)
	}
	if cap.called {
		t.Error("the handler ran for a value that would have wrapped")
	}

	// And the value the admin page sends still works.
	cap.called = false
	status, env := send(t, app, jsonRequest(http.MethodPost, ldapConfigScope,
		`{`+base+`,"sync_interval_minutes":60}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.Int("sync_interval_minutes"); got != 60 {
		t.Errorf("sync_interval_minutes = %d, want 60", got)
	}
}

// TestLDAPBindPasswordIsWriteOnly pins the one parameter in this domain that
// carries a credential: it must be declarable (an operator has to be able to
// set one) and it must not be a path or query value, which proxies and access
// logs record.
func TestLDAPBindPasswordIsWriteOnly(t *testing.T) {
	for _, route := range ldapWriteRoutes {
		e := declaredEndpoint(t, route.method, route.path)
		prop, ok := e.Parameters["bind_password"]
		if !ok {
			t.Errorf("%s declares no bind_password parameter", route.method)
			continue
		}
		if !prop.Optional {
			t.Errorf("%s: bind_password is required; an anonymous bind is legal and stores none", route.method)
		}
		if src := apischema.ResolveSource("bind_password", prop, route.method, nil); src != apischema.SourceBody {
			t.Errorf("%s: bind_password resolves to %q, want %q — a credential must never travel in a URL",
				route.method, src, apischema.SourceBody)
		}
	}

	// The read routes must not echo it back as a parameter either.
	for _, route := range []struct{ method, path string }{
		{fiber.MethodGet, ldapConfigScope},
		{fiber.MethodGet, ldapConfigScope + "/:id"},
	} {
		if _, declared := declaredEndpoint(t, route.method, route.path).Parameters["bind_password"]; declared {
			t.Errorf("%s %s declares a bind_password parameter; a read takes none", route.method, route.path)
		}
	}
}

// TestLDAPConfigIDIsAUUIDFromThePath keeps the one identifier in this domain
// honest.
func TestLDAPConfigIDIsAUUIDFromThePath(t *testing.T) {
	for key := range ldapLegacyPermissions {
		method, path, _ := strings.Cut(key, " ")
		if !strings.Contains(path, ":id") {
			continue
		}
		prop, ok := declaredEndpoint(t, method, path).Parameters["id"]
		if !ok {
			t.Errorf("%s declares no id parameter", key)
			continue
		}
		if prop.Format != "uuid" {
			t.Errorf("%s: id declares format %q, want uuid", key, prop.Format)
		}
		if prop.Source != apischema.SourcePath {
			t.Errorf("%s: id declares source %q, want %q", key, prop.Source, apischema.SourcePath)
		}
	}
}

// TestEveryLDAPEndpointIsDocumented holds the declaration-is-the-documentation
// rule for this domain.
func TestEveryLDAPEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredLDAPEndpoints(t) {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no Description", key)
		}
		if e.Group != "Authentication" {
			t.Errorf("%s declares group %q, want %q", key, e.Group, "Authentication")
		}
	}
}
