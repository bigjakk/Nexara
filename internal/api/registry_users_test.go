package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// userRouteCount is how many endpoints registerUserEndpoints declares. It is
// ALL 4 of UserHandler's routes; nothing in this domain was left legacy. The
// admin TOTP reset that shares their path prefix is TOTPHandler's route and is
// counted with the TOTP domain. See vmRouteCount in registry_vms_test.go for
// why the registry total is a sum of per-domain constants.
const userRouteCount = 4

const testUserRowID = "c3d4e5f6-0000-4000-8000-000000000044"

// userLegacyPermissions is what each handler checked with a hand-placed
// requirePerm call BEFORE this migration, transcribed from
// `git show HEAD:internal/api/handlers/users.go` at commit 357be6f.
//
// `calls` is how many requirePerm calls the handler made and `hoisted` is how
// many moved into middleware, because this domain is the first in the tranche
// where the two differ. Update made TWO calls — manage:user unconditionally and
// manage:role only when the body carried `role` — so neither hoists: a Check
// can state one static permission, and stating only the first would document
// half the gate. See userUpdateReason.
var userLegacyPermissions = map[string]struct {
	permission string
	calls      int
	hoisted    int
}{
	"GET /api/v1/users":        {"view:user", 1, 1},
	"GET /api/v1/users/:id":    {"view:user", 1, 1},
	"PUT /api/v1/users/:id":    {"manage:user", 2, 0},
	"DELETE /api/v1/users/:id": {"manage:user", 1, 1},
}

// userRoutesOutsideTheClusterCheckShape is every route in this domain.
//
// A NEXARA account is an instance-wide object — who may sign in to this install
// — so no path here names a cluster and no check can be cluster-scoped. The
// contrast is registry_access.go, whose 25 routes are one cluster's PROXMOX
// users and are cluster-scoped for exactly that reason.
var userRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/users":        "a Nexara account is instance-wide; the path names no cluster",
	"GET /api/v1/users/:id":    "a Nexara account is instance-wide; the path names no cluster",
	"PUT /api/v1/users/:id":    "Deferred: manage:user always, plus manage:role only when the body carries `role`",
	"DELETE /api/v1/users/:id": "a Nexara account is instance-wide; the path names no cluster",
}

// declaredUserEndpoints returns every declaration in this domain, keyed
// "METHOD path".
//
// It matches on the exact paths rather than on the /users prefix, because the
// admin TOTP reset is mounted under the same prefix by a different handler and
// belongs to a different tally.
func declaredUserEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if e.Path == userScope || e.Path == userScope+"/:id" {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestUserRoutesDeclareTheSamePermissionTheyEnforced is the tally: 5
// hand-placed calls in, 3 declared global Checks out, and 2 calls deliberately
// kept in UserHandler.Update because its second one is conditional.
func TestUserRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredUserEndpoints(t)
	if len(declared) != userRouteCount {
		t.Fatalf("the registry declares %d user routes, want %d", len(declared), userRouteCount)
	}
	if len(userLegacyPermissions) != userRouteCount {
		t.Fatalf("userLegacyPermissions has %d entries, want %d — the table must cover every migrated route",
			len(userLegacyPermissions), userRouteCount)
	}

	calls, hoisted := 0, 0
	for key, want := range userLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want.permission)
			continue
		}
		calls += want.calls
		hoisted += want.hoisted

		if want.hoisted == 0 {
			if e.Permissions.Deferred == "" {
				t.Errorf("%s declares %q, but its gate is conditional on the body — a Check would state "+
					"only the unconditional half", key, e.Permissions.Describe())
			}
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check — it made one static, unconditional requirePerm call",
				key, e.Permissions.Describe())
			continue
		}
		if e.Permissions.Describe() != want.permission {
			t.Errorf("%s declares %q but the handler enforced %q before the migration",
				key, e.Permissions.Describe(), want.permission)
		}
		if e.Permissions.Check.Scope != ScopeGlobal {
			t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, ScopeGlobal)
		}
	}
	if calls != 5 || hoisted != 3 {
		t.Errorf("the tally is %d call(s) in and %d hoisted, want 5 and 3", calls, hoisted)
	}

	for key := range declared {
		if _, listed := userLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in userLegacyPermissions — a new user route must be added "+
				"to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestUserRoutesAreGatedByTheirDeclaration proves the three hoisted permissions
// are the permissions the routes enforce, end to end.
//
// The unrelated grant is the OTHER action on the same resource, which is the
// split that matters: manage:user can disable or delete any account on the
// install, and a manage route that accidentally declared view would be openable
// by every built-in Viewer.
func TestUserRoutesAreGatedByTheirDeclaration(t *testing.T) {
	for key, want := range userLegacyPermissions {
		if want.hoisted == 0 {
			continue // Deferred; covered by TestUserUpdateIsDeferredAndChecksBeforeTheDatabase
		}
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := strings.ReplaceAll(path, ":id", testUserRowID)

			unrelated := "view:user"
			if strings.HasPrefix(want.permission, "view:") {
				unrelated = "manage:user"
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
			app = newRegistryApp(t, stubAuth(map[string]bool{want.permission: true}), gated)
			status, _ = send(t, app, authedRequest(method, target))
			if status == fiber.StatusForbidden {
				t.Fatalf("a caller holding %q got 403", want.permission)
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want.permission: true}), gated)
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

// TestUserUpdateIsDeferredAndChecksBeforeTheDatabase is the one route in this
// tranche whose shape had to be DECIDED rather than ported, so it is pinned
// from three sides.
//
// A Deferred declaration installs no middleware, which means the handler is the
// gate — and the whole hazard of the shape is a handler that then checks
// nothing. registryEnforcementGaps already proves a permission leaf is
// REACHABLE from UserHandler.Update; what it cannot prove is that the check
// runs before anything happens. This drives a real request with the REAL
// handler, whose queries are nil: a caller holding no grant must come back 403,
// which is only possible if the refusal happened before the first DB call.
//
// The reason string is asserted too, because for a Deferred route it is the
// only record of what the handler checks — Describe() renders the bare word
// "deferred" — and the second, conditional grant exists nowhere else.
func TestUserUpdateIsDeferredAndChecksBeforeTheDatabase(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, userScope+"/:id")

	if e.Permissions.Deferred == "" {
		t.Fatalf("declares %q, want Deferred", e.Permissions.Describe())
	}
	for _, want := range []string{"manage:user", "manage:role"} {
		if !strings.Contains(e.Permissions.Deferred, want) {
			t.Errorf("the Deferred reason does not name %q, which is half of what the handler checks: %q",
				want, e.Permissions.Deferred)
		}
	}
	// The Description is what the API docs render for a Deferred route, so an
	// operator building a role has to be able to read both grants there too.
	for _, want := range []string{"manage:user", "manage:role"} {
		if !strings.Contains(e.Description, want) {
			t.Errorf("the Description does not name %q; Describe() renders only \"deferred\", so this is "+
				"what an operator building a role reads: %q", want, e.Description)
		}
	}

	// End to end against the REAL handler (nil queries, nil rbac): a caller
	// with no grant must be refused, and the refusal must happen before the
	// nil database is touched.
	target := userScope + "/" + testUserRowID
	app := newRegistryApp(t, stubAuth(map[string]bool{"view:user": true}), e)
	req := jsonRequest(http.MethodPut, target, `{"display_name":"x"}`)
	req.Header.Set("X-Test-User", "yes")
	status, _ := send(t, app, req)
	if status != fiber.StatusForbidden {
		t.Errorf("a caller holding only view:user got %d, want 403 — Update's own manage:user check "+
			"must run before anything else", status)
	}

	status, _ = send(t, app, jsonRequest(http.MethodPut, target, `{"display_name":"x"}`))
	if status != fiber.StatusUnauthorized {
		t.Errorf("an anonymous caller got %d, want 401", status)
	}
}

// probeUserEndpoint is a declared user endpoint with its handler swapped for a
// capture and its gate removed, so a parameter test needs neither a database
// nor a session.
func probeUserEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestUserUpdateFieldsStayTristate is the compatibility assertion for the three
// fields whose request-struct field was a POINTER.
//
// `is_active` is the one that matters most: every refusal in the handler keys
// on the field having been SUPPLIED rather than on its value, and the
// deactivation path revokes every session the account holds. A Default could
// not make an unrelated edit read as supplied (apischema.Property.Default) —
// the reads below pin that an omitted key arrives as omitted — but it would
// document every such edit as setting the account's active state.
func TestUserUpdateFieldsStayTristate(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, userScope+"/:id")
	for _, name := range []string{"display_name", "is_active", "role"} {
		prop, ok := e.Parameters[name]
		if !ok {
			t.Errorf("declares no %q parameter", name)
			continue
		}
		if !prop.Optional {
			t.Errorf("%q is required; every field on the user update has always been optional", name)
		}
		if prop.Default != nil {
			t.Errorf("%q declares default %v — it is a tristate, an omitted one is left alone, and a "+
				"default would document otherwise", name, prop.Default)
		}
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeUserEndpoint(t, fiber.MethodPut, userScope+"/:id", cap))
	target := userScope + "/" + testUserRowID

	status, env := send(t, app, jsonRequest(http.MethodPut, target, `{"display_name":"Operator"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if _, supplied := cap.params.OptBool("is_active"); supplied {
		t.Error("is_active reads as supplied although the request never sent it — a name edit would " +
			"rewrite the account's active state and revoke its sessions")
	}
	if _, supplied := cap.params.OptString("role"); supplied {
		t.Error("role reads as supplied although the request never sent it — a name edit would demand manage:role")
	}

	// And the value a caller CAN send is carried through as supplied, so that
	// "disable this account" stays expressible.
	cap.called = false
	status, env = send(t, app, jsonRequest(http.MethodPut, target, `{"is_active":false}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if v, supplied := cap.params.OptBool("is_active"); !supplied || v {
		t.Errorf("is_active = (%v,%v), want (false,true)", v, supplied)
	}
}

// TestUserUpdateRoleVocabulary pins the enum that replaced the handler's own
// `!= "admin" && != "user"` check, and records the ordering change that comes
// with moving it.
//
// The refusal used to land AFTER the manage:role check, so a caller holding
// neither grant saw 403; it now lands before, so they see 400. Nothing about
// the vocabulary is secret — it is in the API docs — but the change is a
// decision, so it is written down here rather than discovered later.
func TestUserUpdateRoleVocabulary(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, userScope+"/:id")
	role := e.Parameters["role"]
	if len(role.Enum) != 2 || role.Enum[0] != "admin" || role.Enum[1] != "user" {
		t.Fatalf("role declares enum %v, want [admin user]", role.Enum)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeUserEndpoint(t, fiber.MethodPut, userScope+"/:id", cap))
	target := userScope + "/" + testUserRowID

	status, _ := send(t, app, jsonRequest(http.MethodPut, target, `{"role":"superuser"}`))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a role outside the vocabulary", status)
	}
	if cap.called {
		t.Error("the handler ran for a role outside the vocabulary")
	}

	cap.called = false
	status, env := send(t, app, jsonRequest(http.MethodPut, target, `{"role":"admin"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
}

// TestUserIdentifiersAreUUIDsFromThePath keeps the one gate-visible parameter
// honest.
//
// :id is one of the two names clusterIDFromParam falls back to (gateParamNames
// in registry.go). Nothing in this domain is cluster-scoped, so the fallback is
// never reached today — what makes that durable is that the parameter resolves
// to the PATH, which is where the gate would read it from.
func TestUserIdentifiersAreUUIDsFromThePath(t *testing.T) {
	for _, method := range []string{fiber.MethodGet, fiber.MethodPut, fiber.MethodDelete} {
		e := declaredEndpoint(t, method, userScope+"/:id")
		prop, ok := e.Parameters["id"]
		if !ok {
			t.Errorf("%s %s declares no id parameter", method, userScope+"/:id")
			continue
		}
		if prop.Format != "uuid" {
			t.Errorf("%s: id declares format %q, want uuid", method, prop.Format)
		}
		if prop.Source != apischema.SourcePath {
			t.Errorf("%s: id declares source %q, want %q", method, prop.Source, apischema.SourcePath)
		}
	}
}

// TestEveryUserEndpointIsDocumented holds the declaration-is-the-documentation
// rule for this domain.
func TestEveryUserEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredUserEndpoints(t) {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no Description", key)
		}
		if e.Group != "User Management" {
			t.Errorf("%s declares group %q, want %q", key, e.Group, "User Management")
		}
	}
}
