package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
)

// stubRBACEngine structurally satisfies the unexported permissionEngine
// interface in internal/api/handlers, so the permission middleware runs
// its real code path with no Postgres and no Redis behind it. grants is
// keyed "action:resource".
type stubRBACEngine struct {
	grants map[string]bool
}

func (s *stubRBACEngine) HasPermission(_ context.Context, _ uuid.UUID, action, resource, _ string, _ uuid.UUID) (bool, error) {
	return s.grants[action+":"+resource], nil
}

func (s *stubRBACEngine) HasGlobalPermission(_ context.Context, _ uuid.UUID, action, resource string) (bool, error) {
	return s.grants[action+":"+resource], nil
}

func (s *stubRBACEngine) LoadUserPermissions(_ context.Context, _ uuid.UUID) (*auth.UserPermissions, error) {
	return &auth.UserPermissions{}, nil
}

// stubAuth stands in for Server.authRequired. It is a named function so
// that its position in a route's handler chain is identifiable by name.
// A request carrying no X-Test-User is rejected exactly as authRequired
// rejects a request with no bearer token.
func stubAuth(grants map[string]bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Get("X-Test-User") == "" {
			return fiber.NewError(fiber.StatusUnauthorized, "Missing authorization token")
		}
		c.Locals("user_id", uuid.MustParse(testUserID))
		c.Locals("rbac_engine", &stubRBACEngine{grants: grants})
		return c.Next()
	}
}

const testUserID = "11111111-2222-3333-4444-555555555555"

func authedRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("X-Test-User", "yes")
	return req
}

func handlerName(h fiber.Handler) string {
	return runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
}

// chainNames returns the handler chain Fiber recorded for a route, which
// is the only thing a guard test can inspect — and the reason every link
// is attached per route rather than through a Group. Fiber v3 applies
// group middleware at match time, so a permission attached to a Group
// never appears here at all.
func chainNames(t *testing.T, app *fiber.App, method, path string) []string {
	t.Helper()
	for _, r := range app.GetRoutes(true) {
		if r.Method != method || r.Path != path {
			continue
		}
		names := make([]string, 0, len(r.Handlers))
		for _, h := range r.Handlers {
			names = append(names, handlerName(h))
		}
		return names
	}
	t.Fatalf("route %s %s is not registered", method, path)
	return nil
}

func gatedEndpoint(cap *capture, perms Permissions) Endpoint {
	return Endpoint{
		Method:      fiber.MethodPost,
		Path:        "/api/v1/clusters/:cluster_id/widgets",
		Description: "Create a widget.",
		Group:       "Widgets",
		Permissions: perms,
		Parameters: apischema.Properties{
			"cluster_id": apischema.StdOption("cluster-id"),
		},
		Handler: cap.handler(),
	}
}

// TestRegistryChainOrder pins the middleware order:
//
//	authRequired -> rate limiter -> permission -> handler
//
// The limiter sits ahead of the permission check so that a caller
// hammering an endpoint they are not entitled to spends their own bucket
// instead of collecting free 403s at full rate.
func TestRegistryChainOrder(t *testing.T) {
	cap := &capture{}
	e := gatedEndpoint(cap, Permissions{
		Check: &Check{Action: "manage", Resource: "widget", Scope: ScopeCluster},
	})
	e.RateLimiter = limiter.New(limiter.Config{
		Max:          10,
		Expiration:   time.Minute,
		KeyGenerator: func(fiber.Ctx) string { return "chain-order" },
	})

	app := newRegistryApp(t, stubAuth(nil), e)
	got := chainNames(t, app, fiber.MethodPost, "/api/v1/clusters/:cluster_id/widgets")

	want := []string{
		"internal/api.stubAuth",
		"middleware/limiter.",
		"internal/api/handlers.RequireClusterPermission",
		"internal/api.Endpoint.serve",
	}
	if len(got) != len(want) {
		t.Fatalf("chain = %v, want %d links in the order %v", got, len(want), want)
	}
	for i, fragment := range want {
		if !strings.Contains(got[i], fragment) {
			t.Errorf("chain[%d] = %q, want it to be %s", i, got[i], fragment)
		}
	}
}

func TestRegistryChainForEachPermissionShape(t *testing.T) {
	cases := []struct {
		name  string
		perms Permissions
		want  []string
	}{
		{
			name:  "global check",
			perms: Permissions{Check: &Check{Action: "manage", Resource: "widget", Scope: ScopeGlobal}},
			want:  []string{"internal/api.stubAuth", "internal/api/handlers.RequirePermission", "internal/api.Endpoint.serve"},
		},
		{
			name: "alternatives",
			perms: Permissions{Alternatives: []Check{
				{Action: "view", Resource: "vm", Scope: ScopeCluster},
				{Action: "view", Resource: "container", Scope: ScopeCluster},
			}},
			want: []string{"internal/api.stubAuth", "internal/api/handlers.RequireAnyPermission", "internal/api.Endpoint.serve"},
		},
		{
			// The four shapes that install no gate still install auth —
			// and the absence of a gate is exactly what their stated
			// reason has to justify.
			name:  "deferred",
			perms: Permissions{Deferred: "the resource depends on which object the body names"},
			want:  []string{"internal/api.stubAuth", "internal/api.Endpoint.serve"},
		},
		{
			name: "advisory",
			perms: Permissions{Advisory: &AdvisoryCheck{
				Check:  Check{Action: "view", Resource: "cluster", Scope: ScopeCluster},
				Reason: "accessibleClusters filters the listing to the caller's clusters",
			}},
			want: []string{"internal/api.stubAuth", "internal/api.Endpoint.serve"},
		},
		{
			name:  "self-service",
			perms: Permissions{SelfService: "acts on the caller's own identity, taken from the session"},
			want:  []string{"internal/api.stubAuth", "internal/api.Endpoint.serve"},
		},
		{
			// Public is the one shape that drops authentication.
			name:  "public",
			perms: Permissions{Public: "the login page asks whether an admin exists yet"},
			want:  []string{"internal/api.Endpoint.serve"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, stubAuth(nil), gatedEndpoint(cap, tc.perms))
			got := chainNames(t, app, fiber.MethodPost, "/api/v1/clusters/:cluster_id/widgets")
			if len(got) != len(tc.want) {
				t.Fatalf("chain = %v, want %v", got, tc.want)
			}
			for i, fragment := range tc.want {
				if !strings.Contains(got[i], fragment) {
					t.Errorf("chain[%d] = %q, want %s", i, got[i], fragment)
				}
			}
		})
	}
}

// TestRegistryLimiterOnAPublicRoute covers the combination the login and
// setup-status endpoints will need in Phase 4: no session to authenticate
// against, and therefore all the more reason to cap the route.
func TestRegistryLimiterOnAPublicRoute(t *testing.T) {
	cap := &capture{}
	e := gatedEndpoint(cap, Permissions{Public: "the login page asks this before any identity exists"})
	e.RateLimiter = limiter.New(limiter.Config{
		Max:          1,
		Expiration:   time.Minute,
		KeyGenerator: func(fiber.Ctx) string { return "public-flood" },
	})
	app := newRegistryApp(t, stubAuth(nil), e)

	got := chainNames(t, app, fiber.MethodPost, "/api/v1/clusters/:cluster_id/widgets")
	want := []string{"middleware/limiter.", "internal/api.Endpoint.serve"}
	if len(got) != len(want) {
		t.Fatalf("chain = %v, want %v — a public route takes the limiter but no authentication", got, want)
	}
	for i, fragment := range want {
		if !strings.Contains(got[i], fragment) {
			t.Errorf("chain[%d] = %q, want %s", i, got[i], fragment)
		}
	}

	// And the cap still applies to anonymous callers, which is the whole
	// point of allowing a limiter here.
	target := "/api/v1/clusters/" + testClusterID + "/widgets"
	if status, env := send(t, app, httptest.NewRequest(http.MethodPost, target, nil)); status != fiber.StatusNoContent {
		t.Fatalf("first anonymous request: status = %d (%q), want 204", status, env.Message)
	}
	if status, _ := send(t, app, httptest.NewRequest(http.MethodPost, target, nil)); status != fiber.StatusTooManyRequests {
		t.Errorf("second anonymous request: status = %d, want 429", status)
	}
}

func TestRegistryPermissionGateDeniesAndAllows(t *testing.T) {
	target := "/api/v1/clusters/" + testClusterID + "/widgets"

	t.Run("denied", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:widget": true}),
			gatedEndpoint(cap, Permissions{Check: &Check{Action: "manage", Resource: "widget", Scope: ScopeCluster}}))

		status, env := send(t, app, authedRequest(http.MethodPost, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d (%q), want 403", status, env.Message)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("allowed", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:widget": true}),
			gatedEndpoint(cap, Permissions{Check: &Check{Action: "manage", Resource: "widget", Scope: ScopeCluster}}))

		status, env := send(t, app, authedRequest(http.MethodPost, target))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if !cap.called {
			t.Error("the handler did not run for a caller holding the declared permission")
		}
	})

	t.Run("alternatives pass on either", func(t *testing.T) {
		alts := Permissions{Alternatives: []Check{
			{Action: "view", Resource: "vm", Scope: ScopeCluster},
			{Action: "view", Resource: "container", Scope: ScopeCluster},
		}}
		for _, granted := range []string{"view:vm", "view:container"} {
			cap := &capture{}
			app := newRegistryApp(t, stubAuth(map[string]bool{granted: true}), gatedEndpoint(cap, alts))
			status, env := send(t, app, authedRequest(http.MethodPost, target))
			if status != fiber.StatusNoContent {
				t.Fatalf("%s: status = %d (%q), want 204", granted, status, env.Message)
			}
		}

		cap := &capture{}
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:node": true}), gatedEndpoint(cap, alts))
		status, env := send(t, app, authedRequest(http.MethodPost, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403 for a caller holding neither alternative", status)
		}
		if !strings.Contains(env.Message, "view:vm") || !strings.Contains(env.Message, "view:container") {
			t.Errorf("message = %q, want it to name both alternatives", env.Message)
		}
	})
}

// TestRegistryLimiterRunsAheadOfThePermissionCheck is the behavioural
// half of the chain order. A caller with no permission draws on their own
// bucket: the first attempt is a 403, and once the bucket is empty the
// answer becomes 429. If the permission gate ran first, an unauthorized
// caller would collect 403s at full rate forever and never be capped.
func TestRegistryLimiterRunsAheadOfThePermissionCheck(t *testing.T) {
	cap := &capture{}
	e := gatedEndpoint(cap, Permissions{
		Check: &Check{Action: "manage", Resource: "widget", Scope: ScopeCluster},
	})
	e.RateLimiter = limiter.New(limiter.Config{
		Max:          1,
		Expiration:   time.Minute,
		KeyGenerator: func(fiber.Ctx) string { return "denied-flood" },
	})
	app := newRegistryApp(t, stubAuth(nil), e)

	target := "/api/v1/clusters/" + testClusterID + "/widgets"
	if status, env := send(t, app, authedRequest(http.MethodPost, target)); status != fiber.StatusForbidden {
		t.Fatalf("first request: status = %d (%q), want 403", status, env.Message)
	}
	if status, _ := send(t, app, authedRequest(http.MethodPost, target)); status != fiber.StatusTooManyRequests {
		t.Errorf("second request: status = %d, want 429 — the limiter must run ahead of the permission check, "+
			"so an unauthorized flood spends the caller's own bucket", status)
	}
}

// TestRegistryAuthRunsAheadOfTheLimiter is the other ordering constraint,
// and the one clusterCreateLimiter's comment is about: app-level or
// pre-auth middleware lets anonymous traffic drain a bucket that
// legitimate callers need, which behind a proxy with TRUSTED_PROXIES
// unset is a one-line denial of service.
func TestRegistryAuthRunsAheadOfTheLimiter(t *testing.T) {
	cap := &capture{}
	e := gatedEndpoint(cap, Permissions{SelfService: "synthetic route"})
	e.RateLimiter = limiter.New(limiter.Config{
		Max:          2,
		Expiration:   time.Minute,
		KeyGenerator: func(fiber.Ctx) string { return "anonymous-flood" },
	})
	app := newRegistryApp(t, stubAuth(nil), e)

	target := "/api/v1/clusters/" + testClusterID + "/widgets"
	for i := range 5 {
		if status, env := send(t, app, httptest.NewRequest(http.MethodPost, target, nil)); status != fiber.StatusUnauthorized {
			t.Fatalf("anonymous request %d: status = %d (%q), want 401", i+1, status, env.Message)
		}
	}

	// The bucket must still be intact for the caller who actually has a
	// session.
	if status, env := send(t, app, authedRequest(http.MethodPost, target)); status != fiber.StatusNoContent {
		t.Errorf("authenticated request after an anonymous flood: status = %d (%q), want 204 — "+
			"anonymous traffic drained the bucket", status, env.Message)
	}
}

// TestRegistryPublicRouteNeedsNoSession covers the one shape that drops
// authentication, end to end rather than by chain inspection.
func TestRegistryPublicRouteNeedsNoSession(t *testing.T) {
	cap := &capture{}
	app := newRegistryApp(t, stubAuth(nil), Endpoint{
		Method:      fiber.MethodGet,
		Path:        "/api/v1/setup-status",
		Description: "Report whether an admin exists yet.",
		Group:       "Auth",
		Permissions: Permissions{Public: "the login page asks this before any identity exists"},
		Parameters:  apischema.Properties{},
		Handler:     cap.handler(),
	})

	status, env := send(t, app, httptest.NewRequest(http.MethodGet, "/api/v1/setup-status", nil))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if !cap.called {
		t.Error("the handler did not run on a public route")
	}
}

// TestMountRegistryRefusesToMountWithoutAuthentication closes the one
// fail-open this file could otherwise have. A nil auth middleware would
// mount every endpoint that declared itself authenticated with no session
// check at all — silently, and with the declaration still reading as if
// there were one.
func TestMountRegistryRefusesToMountWithoutAuthentication(t *testing.T) {
	reg := NewRegistry()
	reg.Register(gatedEndpoint(&capture{}, Permissions{
		Check: &Check{Action: "manage", Resource: "widget", Scope: ScopeCluster},
	}))

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("mountRegistry accepted a nil authentication middleware")
		}
		if msg := strings.ToLower(fmt.Sprint(r)); !strings.Contains(msg, "authentication") {
			t.Errorf("panic = %q, want it to name the missing authentication middleware", msg)
		}
	}()
	mountRegistry(fiber.New(), reg, nil)
}

// TestSetupRoutesMountsTheRegistry pins the production wiring: the
// buildRegistry + mountRegistry pair at the top of setupRoutes.
//
// It asserts against a REAL migrated endpoint rather than a probe swapped
// into a global, because there is no global left to swap — the registry is
// built per Server from that Server's own handlers (see buildRegistry).
// The route it picks is the disk attach, which is the one this phase
// exists for.
func TestSetupRoutesMountsTheRegistry(t *testing.T) {
	const attachPath = "/api/v1/clusters/:cluster_id/vms/:vm_id/disks/attach"

	s := newRouteStubServer(t)

	got := chainNames(t, s.app, fiber.MethodPost, attachPath)
	want := []string{
		// setupRoutes passes s.authRequired(), so the real production
		// authentication middleware is what lands here.
		"internal/api.(*Server).authRequired",
		// From Permissions{Check: manage:vm, ScopeCluster} — nothing in
		// the handler body places this any more.
		"internal/api/handlers.RequireClusterPermission",
		"internal/api.Endpoint.serve",
	}
	if len(got) != len(want) {
		t.Fatalf("chain = %v, want %v — setupRoutes did not mount the registry as declared", got, want)
	}
	for i, fragment := range want {
		if !strings.Contains(got[i], fragment) {
			t.Errorf("chain[%d] = %q, want %s", i, got[i], fragment)
		}
	}
}

// TestGuard_EveryDeclaredGateIsMountedAsMiddleware closes the gap
// between "declared" and "actually gated".
//
// registryEnforcementGaps treats Check and Alternatives as structurally
// satisfied — Permissions.middleware is a pure function of the
// declaration, so there is nothing to statically infer — and every other
// guard in this package reads the declaration too. That leaves ONE thing
// nobody checks: whether mountRegistry put the gate on the route. A
// method-conditional, an early `continue`, a reordering that dropped the
// permission link, and all 33 routes would still declare manage:vm while
// serving every authenticated caller, with every guard green.
//
// TestRegistryChainOrder and TestSetupRoutesMountsTheRegistry each pin
// ONE chain end to end. This walks all of them.
func TestGuard_EveryDeclaredGateIsMountedAsMiddleware(t *testing.T) {
	s := newRouteStubServer(t)

	declared := s.registry.Endpoints()
	if len(declared) == 0 {
		t.Fatal("the server declared no registry endpoints, so this guard would check nothing")
	}

	chains := map[string][]string{}
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		names := make([]string, 0, len(r.Handlers))
		for _, h := range r.Handlers {
			names = append(names, handlerName(h))
		}
		chains[r.Method+" "+normalizeRoutePath(r.Path)] = names
	}

	var gated int
	for _, e := range declared {
		key := e.Method + " " + normalizeRoutePath(e.Path)
		chain, mounted := chains[key]
		if !mounted {
			// Reported by TestGuard_EveryDeclaredEndpointIsMountedExactlyOnce;
			// skipped here so one fault does not produce two failures.
			continue
		}

		if e.Permissions.authenticated() && !containsFragment(chain, "internal/api.(*Server).authRequired") {
			t.Errorf("%s declares %q, which requires a session, but its mounted chain has no authentication "+
				"middleware: %v", key, e.Permissions.Describe(), chain)
		}

		if e.Permissions.Check == nil && len(e.Permissions.Alternatives) == 0 {
			// Deferred, Advisory, Public and SelfService install no gate by
			// design; registryEnforcementGaps is what holds the first two
			// to reaching a permission leaf inside the handler.
			continue
		}
		gated++
		if !containsFragment(chain, "/handlers.Require") {
			t.Errorf("%s declares the gate %q but no handlers.Require* middleware is mounted on it — "+
				"the declaration says it is authorized and nothing enforces that: %v",
				key, e.Permissions.Describe(), chain)
		}
	}

	if gated == 0 {
		t.Fatal("no endpoint declared a Check or Alternatives, so the gate half of this guard checked nothing")
	}
}

// containsFragment reports whether any entry in names contains fragment.
// Runtime handler names carry the full import path plus a .funcN suffix,
// so a substring match is what identifies a middleware.
func containsFragment(names []string, fragment string) bool {
	for _, n := range names {
		if strings.Contains(n, fragment) {
			return true
		}
	}
	return false
}

// TestGuard_EveryDeclaredEndpointIsMountedExactlyOnce replaces
// TestPackageRegistryIsStillEmpty, the Phase 4 tripwire that asserted the
// registry held nothing and said in its own failure message that it "must
// be taught to check them once Phase 4 starts migrating routes". This is
// that check.
//
// It keeps the half of the old test that never depended on emptiness — no
// "METHOD path" may appear twice in the mounted route table — and that
// half is NOT subsumed by registryLegacyRouteConflicts, which only ever
// compares registry declarations against legacy routes: two LEGACY blocks
// claiming one path, or one endpoint mounted twice, are invisible to it
// and visible here.
//
// What it adds is the coverage the old test could not have: every
// endpoint the registry declares must actually appear in the route table.
// A declaration that never mounts is a route nobody serves and every
// declaration-reading guard in this package still passes on — the exact
// shape of failure a registry makes possible and an imperative
// registration cannot.
func TestGuard_EveryDeclaredEndpointIsMountedExactlyOnce(t *testing.T) {
	s := newRouteStubServer(t)

	declared := s.registry.Endpoints()
	if len(declared) == 0 {
		t.Fatal("the server declared no registry endpoints, so neither half of this guard would check anything")
	}

	mounted := map[string]int{}
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		mounted[r.Method+" "+normalizeRoutePath(r.Path)]++
	}

	for key, n := range mounted {
		if n > 1 {
			t.Errorf("route %s is registered %d times — two blocks claim it, and Fiber serves whichever "+
				"was registered first while the other is dead code", key, n)
		}
	}

	for _, e := range declared {
		key := e.Method + " " + normalizeRoutePath(e.Path)
		if mounted[key] == 0 {
			t.Errorf("endpoint %s is declared in the registry but is not in the mounted route table — "+
				"nothing serves it, and every guard that reads the declaration passes anyway", key)
		}
	}
}
