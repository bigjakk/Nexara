package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts — rather
// than a fixture shaped like them, so a change to registry_rbac.go that quietly
// loosened a parameter would show up here.

// rbacRouteCount is how many endpoints registerRBACEndpoints declares. It is ALL
// 10 of RBACHandler's routes; nothing in this domain was left legacy. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum of
// per-domain constants.
const rbacRouteCount = 10

const (
	testRoleID       = "b2c3d4e5-0000-4000-8000-000000000011"
	testAssignmentID = "b2c3d4e5-0000-4000-8000-000000000022"
	testRBACUserID   = "b2c3d4e5-0000-4000-8000-000000000033"
)

func rbacRoute(path string) string {
	return strings.NewReplacer(
		":user_id", testRBACUserID,
		":id", testRoleID,
	).Replace(path)
}

// rbacLegacyPermissions is what each handler checked with a hand-placed
// requirePerm call BEFORE this migration, transcribed from
// `git show HEAD:internal/api/handlers/rbac.go` at commit 357be6f.
//
// Nine handlers made exactly one static, unconditional requirePerm(c, action,
// "role") call as their first statement, so all nine hoist into middleware. The
// tenth — MyPermissions — made NO call at all and is listed with an empty
// action: it is the SelfService route, and its presence in this table is what
// makes the tally cover all 10 rather than quietly omitting the one route that
// had nothing to move.
var rbacLegacyPermissions = map[string]string{
	"GET /api/v1/rbac/roles":                       "view",
	"POST /api/v1/rbac/roles":                      "manage",
	"GET /api/v1/rbac/roles/:id":                   "view",
	"PUT /api/v1/rbac/roles/:id":                   "manage",
	"DELETE /api/v1/rbac/roles/:id":                "manage",
	"GET /api/v1/rbac/permissions":                 "view",
	"GET /api/v1/rbac/users/:user_id/roles":        "view",
	"POST /api/v1/rbac/users/:user_id/roles":       "manage",
	"DELETE /api/v1/rbac/users/:user_id/roles/:id": "manage",
	"GET /api/v1/rbac/me/permissions":              "",
}

// rbacRoutesOutsideTheClusterCheckShape is every route in this domain, and that
// is the domain's own shape rather than a widened exception surface.
//
// A Nexara ROLE is an instance-wide object: it is defined once, it names
// permissions from one instance-wide catalogue, and a path here never carries a
// cluster for a cluster-scoped gate to resolve. (A role ASSIGNMENT can be
// scoped to a cluster, but the cluster is a value in the body, not the subject
// of the route — the thing being written is the assignment row.) So all nine
// gated routes are global Checks, and the tenth is SelfService.
//
// registry_access.go is the deliberate contrast, and the reason this comment
// spells the difference out: those 25 routes are the PROXMOX access model, one
// cluster's own users and tokens, reached through that cluster's credential,
// and every one of them is a cluster-scoped Check.
var rbacRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/rbac/roles":                       "Nexara's own roles are instance-wide; the path names no cluster",
	"POST /api/v1/rbac/roles":                      "Nexara's own roles are instance-wide; the path names no cluster",
	"GET /api/v1/rbac/roles/:id":                   "Nexara's own roles are instance-wide; the path names no cluster",
	"PUT /api/v1/rbac/roles/:id":                   "Nexara's own roles are instance-wide; the path names no cluster",
	"DELETE /api/v1/rbac/roles/:id":                "Nexara's own roles are instance-wide; the path names no cluster",
	"GET /api/v1/rbac/permissions":                 "the permission catalogue is one instance-wide list",
	"GET /api/v1/rbac/users/:user_id/roles":        "the subject is a Nexara account, not a cluster",
	"POST /api/v1/rbac/users/:user_id/roles":       "the subject is a Nexara account; the cluster a grant is scoped to is a body value, not the route's subject",
	"DELETE /api/v1/rbac/users/:user_id/roles/:id": "the subject is a Nexara account, not a cluster",
	"GET /api/v1/rbac/me/permissions":              "SelfService: the subject is the caller's own session, so there is no object to scope",
}

// declaredRBACEndpoints returns every declaration in this domain, keyed
// "METHOD path". The prefix is enough: all 10 routes hang off /api/v1/rbac and
// nothing else is mounted under it.
func declaredRBACEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, rbacScope+"/") {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestRBACRoutesDeclareTheSamePermissionTheyEnforced is the tally that makes
// this migration a refactor rather than a change: 9 hand-placed calls in, 9
// declared global Checks out, none kept in a handler — plus the one route that
// never had a call and is declared SelfService instead.
//
// It runs in both directions, so a new RBAC route cannot appear without the
// tally being re-made.
func TestRBACRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredRBACEndpoints(t)
	if len(declared) != rbacRouteCount {
		t.Fatalf("the registry declares %d RBAC routes, want %d", len(declared), rbacRouteCount)
	}
	if len(rbacLegacyPermissions) != rbacRouteCount {
		t.Fatalf("rbacLegacyPermissions has %d entries, want %d — the table must cover every migrated route",
			len(rbacLegacyPermissions), rbacRouteCount)
	}

	hoisted := 0
	for key, action := range rbacLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s:role before the migration but is not declared in the registry", key, action)
			continue
		}
		if action == "" {
			// The one route that checked nothing. It must not have gained a
			// gate, and it must not have been left with no declaration at all.
			if e.Permissions.SelfService == "" {
				t.Errorf("%s declares %q; it checked no permission before the migration and answers with the "+
					"caller's OWN grants, so SelfService is the only shape that describes it", key, e.Permissions.Describe())
			}
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check — it made one static, unconditional requirePerm call",
				key, e.Permissions.Describe())
			continue
		}
		hoisted++
		if want := action + ":role"; e.Permissions.Describe() != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration",
				key, e.Permissions.Describe(), want)
		}
		// Getting the scope wrong here would change who can do what in the
		// direction that matters: a cluster-scoped declaration on a path that
		// names no cluster is refused at registration, but the converse — a
		// global grant standing in for a per-cluster one — is the mistake this
		// pins. A role is an instance-wide object; there is no cluster to
		// resolve.
		if e.Permissions.Check.Scope != ScopeGlobal {
			t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, ScopeGlobal)
		}
	}
	if want := rbacRouteCount - 1; hoisted != want {
		t.Errorf("the tally moves %d permission call(s) into middleware, want %d", hoisted, want)
	}

	for key := range declared {
		if _, listed := rbacLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in rbacLegacyPermissions — a new RBAC route must be added "+
				"to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestRBACRoutesAreGatedByTheirDeclaration proves the 9 hoisted permissions are
// the permissions the routes enforce, end to end.
//
// The "unrelated grant" case is the OTHER action on the same resource rather
// than a foreign permission, because view/manage is the split that matters
// here: manage:role can grant any permission the instance knows to any account,
// so a manage route that accidentally declared view would be openable by every
// built-in Viewer.
func TestRBACRoutesAreGatedByTheirDeclaration(t *testing.T) {
	for key, action := range rbacLegacyPermissions {
		if action == "" {
			continue // SelfService; covered by TestRBACMyPermissionsIsSelfService
		}
		method, path, _ := strings.Cut(key, " ")
		want := action + ":role"
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := rbacRoute(path)

			unrelated := "view:role"
			if action == "view" {
				unrelated = "manage:role"
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

// TestRBACMyPermissionsIsSelfService pins the one route in this domain that
// installs no permission middleware.
//
// SelfService is the riskiest label in the vocabulary: the route IS
// authenticated, so an IDOR — a subject taken from the path or the body rather
// than from the session — is exactly what would hide behind it. The assertions
// are therefore about the SUBJECT, not only about the shape: the declaration
// must name no parameter at all, so there is nothing a caller could substitute,
// and the route must still refuse an anonymous request.
func TestRBACMyPermissionsIsSelfService(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, rbacScope+"/me/permissions")

	if e.Permissions.SelfService == "" {
		t.Fatalf("declares %q, want SelfService", e.Permissions.Describe())
	}
	if len(e.Parameters) != 0 {
		t.Errorf("declares parameters %v; a self-service route must take no subject from the caller",
			e.Parameters)
	}
	if !e.Permissions.authenticated() {
		t.Error("is not authenticated; SelfService means the session identifies the subject, so there must be one")
	}

	// End to end: no grant at all is enough, but no SESSION is not.
	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := rbacScope + "/me/permissions"

	app := newRegistryApp(t, stubAuth(map[string]bool{}), gated)
	status, _ := send(t, app, authedRequest(http.MethodGet, target))
	if status != fiber.StatusNoContent {
		t.Errorf("a caller holding no grant got %d, want the handler to run", status)
	}

	cap.called = false
	app = newRegistryApp(t, stubAuth(map[string]bool{}), gated)
	status, _ = send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
	if status != fiber.StatusUnauthorized {
		t.Errorf("an anonymous caller got %d, want 401", status)
	}
	if cap.called {
		t.Error("the handler ran for a request carrying no session")
	}
}

// probeRBACEndpoint is a declared RBAC endpoint with its handler swapped for a
// capture and its gate removed, so a parameter test needs neither a database
// nor a session.
func probeRBACEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestRBACIdentifiersAreUUIDs keeps every identifier in this domain a UUID read
// from the PATH.
//
// The :id on /rbac/roles/:id is one of the two names clusterIDFromParam falls
// back to (gateParamNames in registry.go), so a body or query parameter of that
// name would be a name the permission middleware also reads. Nothing in this
// domain is cluster-scoped today, which is why the source assertion — rather
// than the absence of the name — is what keeps it safe.
func TestRBACIdentifiersAreUUIDs(t *testing.T) {
	cases := []struct {
		method, path string
		params       []string
	}{
		{fiber.MethodGet, rbacScope + "/roles/:id", []string{"id"}},
		{fiber.MethodPut, rbacScope + "/roles/:id", []string{"id"}},
		{fiber.MethodDelete, rbacScope + "/roles/:id", []string{"id"}},
		{fiber.MethodGet, rbacScope + "/users/:user_id/roles", []string{"user_id"}},
		{fiber.MethodPost, rbacScope + "/users/:user_id/roles", []string{"user_id"}},
		{fiber.MethodDelete, rbacScope + "/users/:user_id/roles/:id", []string{"user_id", "id"}},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			e := declaredEndpoint(t, tc.method, tc.path)
			for _, name := range tc.params {
				prop, ok := e.Parameters[name]
				if !ok {
					t.Errorf("declares no %q parameter", name)
					continue
				}
				if prop.Format != "uuid" {
					t.Errorf("%q declares format %q, want uuid", name, prop.Format)
				}
				if prop.Source != apischema.SourcePath {
					t.Errorf("%q declares source %q, want %q", name, prop.Source, apischema.SourcePath)
				}
			}
		})
	}
}

// TestRBACRoleUpdateFieldsStayTristate is the compatibility assertion for the
// three fields whose request-struct field was a POINTER.
//
// nil meant "leave the stored value alone" and a pointer to the zero value
// meant "set it to this". The update reads them with p.OptString and p.Has,
// which a default cannot fool (apischema.Property.Default), so a Default would
// not blank a renamed role's description or strip its permissions; it would
// document both — and permission_ids is shared with the create, whose
// p.Strings read would grant a default to every role made without a list.
func TestRBACRoleUpdateFieldsStayTristate(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, rbacScope+"/roles/:id")
	for _, name := range []string{"name", "description", "permission_ids"} {
		prop, ok := e.Parameters[name]
		if !ok {
			t.Errorf("declares no %q parameter", name)
			continue
		}
		if !prop.Optional {
			t.Errorf("%q is required; every field on the role update has always been optional", name)
		}
		if prop.Default != nil {
			t.Errorf("%q declares default %v — it is a tristate, an omitted one is left alone, and a "+
				"default would document otherwise", name, prop.Default)
		}
	}

	// End to end: an EMPTY permission_ids survives as a SUPPLIED value, which
	// is what makes "strip every permission" expressible and distinct from an
	// omission that must leave the set alone.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeRBACEndpoint(t, fiber.MethodPut, rbacScope+"/roles/:id", cap))
	target := rbacScope + "/roles/" + testRoleID

	status, env := send(t, app, jsonRequest(http.MethodPut, target, `{"permission_ids":[]}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if !cap.params.Has("permission_ids") {
		t.Error("an empty permission_ids array reads as absent; the deliberate clear would be dropped")
	}

	cap.called = false
	status, env = send(t, app, jsonRequest(http.MethodPut, target, `{"name":"Operators"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if cap.params.Has("permission_ids") {
		t.Error("permission_ids reads as supplied although the request never sent it — editing a name would strip the role")
	}
	if v, supplied := cap.params.OptString("name"); !supplied || v != "Operators" {
		t.Errorf(`name = (%q,%v), want ("Operators",true)`, v, supplied)
	}
}

// TestRBACRoleCreateRequiresAName pins the one explicit check CreateRole made,
// and that the name cannot be satisfied by an empty string — which the old
// `if req.Name == ""` also refused.
func TestRBACRoleCreateRequiresAName(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, rbacScope+"/roles")
	if e.Parameters["name"].Optional {
		t.Error("name is optional, but CreateRole has always refused a request without it")
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeRBACEndpoint(t, fiber.MethodPost, rbacScope+"/roles", cap))

	for _, body := range []string{`{}`, `{"name":""}`} {
		cap.called = false
		status, env := send(t, app, jsonRequest(http.MethodPost, rbacScope+"/roles", body))
		if status != fiber.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", body, status, env.Message)
		}
		if cap.called {
			t.Errorf("%s: the handler ran for a role with no name", body)
		}
	}
}

// TestRBACAssignRoleScopeVocabulary pins the scope_type enum and its default.
//
// The handler used to normalise an empty scope_type to "global" and then refuse
// anything that was neither value. Both rules move into the declaration, and
// the default is what keeps the SPA — which sends scope_type explicitly — and
// any older client that omits it behaving identically.
func TestRBACAssignRoleScopeVocabulary(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, rbacScope+"/users/:user_id/roles")

	scopeType := e.Parameters["scope_type"]
	if scopeType.Default != "global" {
		t.Errorf("scope_type declares default %v, want \"global\" — an omitted scope has always meant instance-wide", scopeType.Default)
	}
	if len(scopeType.Enum) != 2 || scopeType.Enum[0] != "global" || scopeType.Enum[1] != "cluster" {
		t.Errorf("scope_type declares enum %v, want [global cluster]", scopeType.Enum)
	}
	if e.Parameters["role_id"].Optional {
		t.Error("role_id is optional, but AssignUserRole has always refused a request without it")
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeRBACEndpoint(t, fiber.MethodPost, rbacScope+"/users/:user_id/roles", cap))
	target := rbacScope + "/users/" + testRBACUserID + "/roles"

	// The shape the SPA sends.
	status, env := send(t, app, jsonRequest(http.MethodPost, target,
		`{"role_id":"`+testRoleID+`","scope_type":"global"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}

	// Omitted scope_type still reaches the handler as "global".
	cap.called = false
	status, env = send(t, app, jsonRequest(http.MethodPost, target, `{"role_id":"`+testRoleID+`"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.String("scope_type"); got != "global" {
		t.Errorf("scope_type = %q, want %q", got, "global")
	}

	// And a third value is refused before the handler runs.
	cap.called = false
	status, _ = send(t, app, jsonRequest(http.MethodPost, target,
		`{"role_id":"`+testRoleID+`","scope_type":"datacenter"}`))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a scope_type outside the vocabulary", status)
	}
	if cap.called {
		t.Error("the handler ran for an unknown scope_type")
	}
}

// TestRBACRevokeAssignmentTakesTheAssignmentID records that the :id on the
// revoke path is the ASSIGNMENT's id, not the role's.
//
// Both are uuids, so nothing about the shape distinguishes them, and revoking
// by role id would silently remove the wrong grant — or none. The declaration's
// description is the only place that says which; this pins that the route
// declares both identifiers and nothing else.
func TestRBACRevokeAssignmentTakesTheAssignmentID(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, rbacScope+"/users/:user_id/roles/:id")
	if len(e.Parameters) != 2 {
		t.Errorf("declares %d parameters, want exactly user_id and id", len(e.Parameters))
	}
	if !strings.Contains(e.Parameters["id"].Description, "assignment") {
		t.Errorf("the id description (%q) does not say it is the assignment's id; both ids are uuids, "+
			"so the description is the only thing that distinguishes them", e.Parameters["id"].Description)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeRBACEndpoint(t, fiber.MethodDelete, rbacScope+"/users/:user_id/roles/:id", cap))
	target := rbacScope + "/users/" + testRBACUserID + "/roles/" + testAssignmentID
	status, env := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.String("id"); got != testAssignmentID {
		t.Errorf("id = %q, want %q", got, testAssignmentID)
	}
}

// TestRBACMyPermissionsReasonMatchesTheReviewedList keeps one exemption to one
// justification, the way the auth and TOTP domains already do.
//
// TestGuard_RegistrySelfServiceRoutesAreReviewed only checks that the KEY is in
// selfServiceRoutes; it never compares the reasons. Without this, the inline
// string and the reviewed one could say different things, and the next reader
// would have two accounts of why a route needs no gate with nothing saying
// which is current.
func TestRBACMyPermissionsReasonMatchesTheReviewedList(t *testing.T) {
	const key = "GET /api/v1/rbac/me/permissions"
	e := declaredEndpoint(t, fiber.MethodGet, rbacScope+"/me/permissions")

	reviewed, listed := selfServiceRoutes[key]
	if !listed {
		t.Fatalf("%s declares SelfService but is not in selfServiceRoutes", key)
	}
	if e.Permissions.SelfService != reviewed {
		t.Errorf("the declared reason is %q but selfServiceRoutes says %q — one exemption, one justification",
			e.Permissions.SelfService, reviewed)
	}
}

// TestEveryRBACEndpointIsDocumented holds the
// declaration-is-the-documentation rule for this domain.
func TestEveryRBACEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredRBACEndpoints(t) {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no Description", key)
		}
		if e.Group != "Roles & Permissions" {
			t.Errorf("%s declares group %q, want %q", key, e.Group, "Roles & Permissions")
		}
	}
}
