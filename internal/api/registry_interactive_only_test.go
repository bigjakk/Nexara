package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	fiberrecover "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Endpoint.InteractiveOnly: a route that changes the caller's own credentials or
// authentication factors, or ends their sessions, refuses a request authenticated with
// an API key. The rule has ONE owner, the registry's gate mounted right after
// authentication, instead of a check at the top of each handler. Pinned: the gate
// itself, with a stand-in for authentication (the first half of this file); the set of
// routes that carry the flag, by name (TestGuard_InteractiveOnly…); and the enforcement
// on every one of them through the REAL authRequired, the REAL declaration and a REAL
// nxra_ key (the second half).

// interactiveOnlyRoutes is every route that must be InteractiveOnly, with the request valid
// for it (a concrete target and a body that passes its schema, so that without the flag a
// request reaches the handler) and what a leaked key could do there. handler is the declared
// Handler as routeHandlerKey names it, so the set is held by WHAT SERVES a route as well as by
// method and path (TestGuard_NoRouteServesACredentialHandlerWithoutTheFlag).
//
// NOT in it, deliberately: routes that accept an API key today, whose joining is the
// operator's decision (GET /auth/sessions, PUT /auth/profile, POST /auth/ws-token, GET
// /auth/me, GET /auth/totp/status, POST /auth/console-token), and DELETE /api-keys[/:id],
// which REVOKE keys (what revoking is for) and change no credential or factor.
var interactiveOnlyRoutes = []struct {
	method, path string // as declared
	handler      string // routeHandlerKey of the declared Handler
	target       string // the request's URL: path with its parameters filled in
	body, why    string
}{
	{
		method: fiber.MethodPost, path: "/api/v1/auth/change-password", handler: "AuthHandler.ChangePassword", target: "/api/v1/auth/change-password",
		body: `{"old_password":"x","new_password":"y"}`,
		why:  "guess the current password, then set its own and end every session of the owner",
	},
	{
		method: fiber.MethodPost, path: "/api/v1/auth/totp/setup", handler: "TOTPHandler.BeginSetup", target: "/api/v1/auth/totp/setup",
		body: `{}`,
		why:  "begin enrolling its own authenticator on an account that has none",
	},
	{
		method: fiber.MethodPost, path: "/api/v1/auth/totp/setup/verify", handler: "TOTPHandler.ConfirmSetup", target: "/api/v1/auth/totp/setup/verify",
		body: `{"code":"123456"}`,
		why:  "turn 2FA on with its own authenticator and take the recovery codes, locking the owner out of login",
	},
	{
		method: fiber.MethodDelete, path: "/api/v1/auth/totp", handler: "TOTPHandler.Disable", target: "/api/v1/auth/totp",
		body: `{"code":"123456"}`,
		why:  "remove the owner's second factor (bounded by the code it would need to guess)",
	},
	{
		method: fiber.MethodPost, path: "/api/v1/auth/totp/recovery-codes/regenerate", handler: "TOTPHandler.RegenerateRecoveryCodes", target: "/api/v1/auth/totp/recovery-codes/regenerate",
		body: `{"code":"123456"}`,
		why:  "void the owner's recovery codes and read new ones (bounded by the code it would need to guess)",
	},
	{
		method: fiber.MethodPost, path: "/api/v1/auth/logout-all", handler: "AuthHandler.LogoutAll", target: "/api/v1/auth/logout-all",
		body: `{}`,
		why:  "loop it and keep the owner signed out: every session the owner opens is gone at the next call",
	},
	{
		method: fiber.MethodDelete, path: "/api/v1/auth/sessions/:id", handler: "AuthHandler.RevokeSessionByID", target: "/api/v1/auth/sessions/d4e5f6a7-0000-4000-8000-000000000066",
		body: ``,
		why:  "list the owner's sessions and end each of them one by one, which is logout-all by another route",
	},
	{
		method: fiber.MethodPost, path: "/api/v1/api-keys", handler: "APIKeyHandler.Create", target: "/api/v1/api-keys",
		body: `{"name":"ci"}`,
		why:  "mint more keys, so that a leaked key outlives its own revocation",
	},
}

// stubAuthKeyAware stands in for authRequired where a test is about what the registry
// does with the principal. It authenticates anything carrying X-Test-User, as a holder
// of the permission the endpoints here require unless X-Test-Grants says "none", and
// records HOW through the constants the real middleware uses, as X-Test-Auth-Method
// says: nothing is an interactive session, "key" an API key, "other" a kind of
// principal that does not exist yet, and "unmarked" one that authenticated and did not
// say how. A named function, so the chain can be read by name.
func stubAuthKeyAware(c fiber.Ctx) error {
	if c.Get("X-Test-User") == "" {
		return fiber.NewError(fiber.StatusUnauthorized, "Missing authorization token")
	}
	grants := map[string]bool{"manage:widget": true}
	if c.Get("X-Test-Grants") == "none" {
		grants = nil
	}
	c.Locals("user_id", uuid.MustParse(testUserID))
	c.Locals("rbac_engine", &stubRBACEngine{grants: grants})
	switch c.Get("X-Test-Auth-Method") {
	case "key":
		c.Locals(handlers.LocalsAuthMethod, handlers.AuthMethodAPIKey)
	case "other":
		c.Locals(handlers.LocalsAuthMethod, "service-token")
	case "unmarked":
	default:
		c.Locals(handlers.LocalsAuthMethod, handlers.AuthMethodSession)
	}
	return c.Next()
}

// interactiveOnlyEndpoint is a synthetic route that has everything the gate has to run
// ahead of: a rate limiter with a bucket of ONE request, a cluster-scoped permission,
// and a path parameter whose validation refuses a malformed value.
func interactiveOnlyEndpoint(cap *capture) Endpoint {
	e := gatedEndpoint(cap, Permissions{
		Check: &Check{Action: "manage", Resource: "widget", Scope: ScopeCluster},
	})
	e.InteractiveOnly = "synthetic: a leaked key must not be able to do this"
	e.RateLimiter = limiter.New(limiter.Config{
		Max:          1,
		Expiration:   time.Minute,
		KeyGenerator: func(fiber.Ctx) string { return "interactive-only" },
	})
	return e
}

// principalRequest is a request from a caller that authenticated as authMethod says
// (see stubAuthKeyAware; "" is an interactive session) and holds the permission unless
// grants is "none".
func principalRequest(method, target, authMethod, grants string) *http.Request {
	req := authedRequest(method, target)
	if authMethod != "" {
		req.Header.Set("X-Test-Auth-Method", authMethod)
	}
	if grants != "" {
		req.Header.Set("X-Test-Grants", grants)
	}
	return req
}

// TestInteractiveOnlyGate_SitsRightAfterAuthentication pins the order of the chain:
//
//	authRequired -> interactive-only gate -> rate limiter -> permission -> handler
//
// The gate comes straight after authentication, which is what says how the caller
// authenticated, and ahead of everything a refused key must not touch: a bucket it
// would spend, a permission answer it would learn its owner's grants from, the
// parameters' validation and the handler.
func TestInteractiveOnlyGate_SitsRightAfterAuthentication(t *testing.T) {
	app := newRegistryApp(t, stubAuthKeyAware, interactiveOnlyEndpoint(&capture{}))
	got := chainNames(t, app, fiber.MethodPost, "/api/v1/clusters/:cluster_id/widgets")

	want := []string{
		"internal/api.stubAuthKeyAware",
		"internal/api.interactiveOnlyGate",
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

// TestInteractiveOnlyGate_RefusesAnythingButASessionBeforeAnythingElseRuns is the
// behaviour the order is for, and the allowlist the gate is. An API key is refused 403
// with the gate's message whether or not its owner holds the permission (a permission
// answer would tell it something about the owner's grants), whether or not the request
// is well-formed (the validation that would answer 400 has not run), without spending
// the route's rate-limit bucket (a bucket of one, still whole afterwards) and without
// reaching the handler. So is every OTHER caller that is not a session — a method no
// gate has heard of, and one that authenticated without saying how: a gate that only
// recognised API keys would admit both. A session is not refused, which is the control.
func TestInteractiveOnlyGate_RefusesAnythingButASessionBeforeAnythingElseRuns(t *testing.T) {
	target := "/api/v1/clusters/" + testClusterID + "/widgets"
	malformed := "/api/v1/clusters/not-a-uuid/widgets"

	cap := &capture{}
	app := newRegistryApp(t, stubAuthKeyAware, interactiveOnlyEndpoint(cap))

	for _, tc := range []struct {
		name       string
		target     string
		authMethod string
		grants     string
	}{
		{"an API key whose owner holds the permission", target, "key", ""},
		{"an API key whose owner does not hold the permission", target, "key", "none"},
		{"an API key and a malformed request", malformed, "key", ""},
		{"a principal type that does not exist yet", target, "other", ""},
		{"a principal that did not say how it authenticated", target, "unmarked", ""},
	} {
		status, env := send(t, app, principalRequest(http.MethodPost, tc.target, tc.authMethod, tc.grants))
		if status != fiber.StatusForbidden || env.Error != "forbidden" || env.Message != interactiveOnlyMessage {
			t.Errorf("%s: status = %d, error = %q, message = %q; want 403 forbidden: %s",
				tc.name, status, env.Error, env.Message, interactiveOnlyMessage)
		}
	}
	if cap.called {
		t.Error("the handler ran for a request that was not authenticated by a session")
	}

	// The bucket is still whole: five refusals spent none of it.
	if status, env := send(t, app, principalRequest(http.MethodPost, target, "", "")); status != fiber.StatusNoContent {
		t.Fatalf("a session after five refusals: status = %d (%q), want 204 — the refusals spent the route's bucket", status, env.Message)
	}
	if !cap.called {
		t.Error("the handler did not run for a session: the gate refuses sessions")
	}
	// ... and what the session spent is the bucket of one: the control that the bucket
	// above was a real one.
	if status, _ := send(t, app, principalRequest(http.MethodPost, target, "", "")); status != fiber.StatusTooManyRequests {
		t.Errorf("a second session request = %d, want 429: the limiter in this fixture never limited, so the check above proved nothing", status)
	}

	t.Run("control: without the flag the same key reaches the handler", func(t *testing.T) {
		cap := &capture{}
		e := interactiveOnlyEndpoint(cap)
		e.InteractiveOnly = ""
		app := newRegistryApp(t, stubAuthKeyAware, e)
		if status, env := send(t, app, principalRequest(http.MethodPost, target, "key", "")); status != fiber.StatusNoContent || !cap.called {
			t.Errorf("status = %d (%q), handler reached = %t, want 204 and true: an unflagged route serves a key", status, env.Message, cap.called)
		}
	})
}

// TestRegisterRefusesAnInteractiveOnlyThatCannotWork: a blank reason says nothing to
// the reader the string is for, and a Public route has no session (no caller
// authenticated by an API key) for the gate to refuse. Both are mistakes in Nexara's
// own source, so a panic at boot like every other malformed declaration.
func TestRegisterRefusesAnInteractiveOnlyThatCannotWork(t *testing.T) {
	flagged := validEndpoint()
	flagged.InteractiveOnly = "a leaked key must not be able to do this"
	if msg := registerPanic(t, flagged); msg != "" {
		t.Fatalf("Register panicked on a valid InteractiveOnly endpoint: %s", msg)
	}

	blank := validEndpoint()
	blank.InteractiveOnly = "   "
	if msg := registerPanic(t, blank); !strings.Contains(msg, "blank InteractiveOnly") {
		t.Errorf("a blank reason: panic = %q, want it to say the InteractiveOnly is blank", msg)
	}

	public := validEndpoint()
	public.Permissions = Permissions{Public: "the login form needs it before a session exists"}
	public.InteractiveOnly = "a leaked key must not be able to do this"
	if msg := registerPanic(t, public); !strings.Contains(msg, "Public and InteractiveOnly") {
		t.Errorf("a Public route: panic = %q, want it to refuse the pairing", msg)
	}
}

// TestGuard_InteractiveOnlyRoutesAreExactlyTheCredentialRoutes pins the SET. Every route
// in interactiveOnlyRoutes must carry the flag — drop it from one and that route's name
// is the failure — and no other route may carry it without being added here with what
// a leaked key could do there, so the set stays a review surface rather than a list
// that drifts. It restates the list rather than deriving it: a guard that read the
// flags it is checking would pass whatever they said.
func TestGuard_InteractiveOnlyRoutesAreExactlyTheCredentialRoutes(t *testing.T) {
	declared := sharedEndpoints(t)

	want := map[string]string{}
	for _, r := range interactiveOnlyRoutes {
		want[r.method+" "+r.path] = r.why
	}
	if len(want) != 8 {
		t.Fatalf("interactiveOnlyRoutes lists %d routes, want the 8 this guard was written for: change-password, the four TOTP "+
			"credential routes, logout-all, the revoking of one session and the creation of an API key", len(want))
	}

	for key, why := range want {
		e, ok := declared[key]
		switch {
		case !ok:
			t.Errorf("%s is not declared at all, so nothing refuses an API key on it", key)
		case strings.TrimSpace(e.InteractiveOnly) == "":
			t.Errorf("%s no longer carries InteractiveOnly: an API key can reach it, and could %s", key, why)
		}
	}
	for key, e := range declared {
		if _, expected := want[key]; !expected && e.InteractiveOnly != "" {
			t.Errorf("%s is InteractiveOnly (%q), which this guard does not know about — add it to interactiveOnlyRoutes "+
				"with what a leaked key could do there, so the set stays a review surface", key, e.InteractiveOnly)
		}
	}
}

// handlersOutsideTheHandlersPackage is every declared endpoint whose Handler is a bound
// method of something other than a handler type in the handlers package, with the reason
// it is allowed to be: the guard below can follow a method of a handler type by name and
// nothing else, and what it cannot follow it does not let pass. Adding a route here is a
// decision to say, in review, that the function cannot reach one of the eight credential handlers.
var handlersOutsideTheHandlersPackage = map[string]string{
	"Server.handleVersion": "GET /api/v1/version is answered by a method of the Server itself and returns a constant: it holds no handler to reach",
}

// serverMethodValue matches the runtime name of a method value bound to the Server,
// "…/internal/api.(*Server).handleVersion-fm".
var serverMethodValue = regexp.MustCompile(`/internal/api\.\(\*Server\)\.(\w+)-fm$`)

// declaredHandlerKey names an endpoint's Handler for the guard below: "Type.Method" for a
// bound method of a handler type (routeHandlerKey), and ok=false, with the runtime name,
// for anything the guard cannot read the identity of — a closure, a plain function, a
// method of another kind of type that handlersOutsideTheHandlersPackage does not list.
func declaredHandlerKey(h Handler) (key string, ok bool) {
	if key := routeHandlerKey(h); key != "" {
		return key, true
	}
	full := runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
	if m := serverMethodValue.FindStringSubmatch(full); m != nil {
		if key := "Server." + m[1]; handlersOutsideTheHandlersPackage[key] != "" {
			return key, true
		}
	}
	return full, false
}

// TestGuard_NoRouteServesACredentialHandlerWithoutTheFlag holds the set by the other end: the
// eight routes are known by method and path, so a ninth that mounts one of the same handlers
// (h.ChangePassword on a versioned alias, say) passes every other guard while a leaked key
// walks through. The handler is what changes the credential, so every declared endpoint served
// by one of the eight must carry the flag, wherever it is mounted. The guard reads a handler's
// identity from the bound method, so a closure would walk past it: rather than follow calls it
// refuses what it cannot read, and every Handler must be a bound method of a handler type (or
// be listed, with its reason, in handlersOutsideTheHandlersPackage). Each row's route must be
// served by the handler the row names, and each handler must serve a route, so a rename fails
// here and does not leave this guard checking nothing.
func TestGuard_NoRouteServesACredentialHandlerWithoutTheFlag(t *testing.T) {
	declared := sharedEndpoints(t)

	byHandler := map[string][]Endpoint{}
	for route, e := range declared {
		key, ok := declaredHandlerKey(e.Handler)
		if !ok {
			t.Errorf("%s is served by %q, which is not a bound method of a handler type: this guard cannot tell which handler a closure or a "+
				"plain function reaches, so a route served that way could be one of the eight and carry no flag. Declare the method itself "+
				"(h.ChangePassword), or — for a function that is deliberately not a handler — list it in handlersOutsideTheHandlersPackage "+
				"with the reason it cannot reach a credential handler", route, key)
			continue
		}
		byHandler[key] = append(byHandler[key], e)
	}

	for _, r := range interactiveOnlyRoutes {
		if r.handler == "" {
			t.Errorf("%s %s names no handler in interactiveOnlyRoutes", r.method, r.path)
			continue
		}
		if e, ok := declared[r.method+" "+r.path]; ok {
			if got, _ := declaredHandlerKey(e.Handler); got != r.handler {
				t.Errorf("%s %s is served by %q, but interactiveOnlyRoutes says %q: the table and the declaration have drifted",
					r.method, r.path, got, r.handler)
			}
		}
		served := byHandler[r.handler]
		if len(served) == 0 {
			t.Errorf("no declared endpoint is served by %s (renamed? moved?): this guard would check nothing for it", r.handler)
			continue
		}
		for _, e := range served {
			if strings.TrimSpace(e.InteractiveOnly) == "" {
				t.Errorf("%s %s is served by %s, a handler that %s, and is not InteractiveOnly: an API key can reach it through this route",
					e.Method, e.Path, r.handler, r.why)
			}
		}
	}
}

// TestGuard_EveryInteractiveOnlyRouteMountsTheGateRightAfterAuthentication closes the
// gap between "declared" and "enforced", the way TestGuard_EveryDeclaredGateIsMountedAsMiddleware
// does for permissions: a declaration nothing mounts is a promise nothing keeps. It
// walks the whole mounted route table.
func TestGuard_EveryInteractiveOnlyRouteMountsTheGateRightAfterAuthentication(t *testing.T) {
	s := sharedRouteStub(t)
	chains := mountedChains(s)

	var flagged int
	for _, e := range s.registry.Endpoints() {
		key := e.Method + " " + normalizeRoutePath(e.Path)
		chain, mounted := chains[key]
		if !mounted {
			continue // reported by TestGuard_EveryDeclaredEndpointIsMountedExactlyOnce
		}
		hasGate := containsFragment(chain, "internal/api.interactiveOnlyGate")
		if e.InteractiveOnly == "" {
			if hasGate {
				t.Errorf("%s does not declare InteractiveOnly but its chain refuses API keys: %v", key, chain)
			}
			continue
		}
		flagged++
		if len(chain) < 2 || !strings.Contains(chain[0], "internal/api.(*Server).authRequired") ||
			!strings.Contains(chain[1], "internal/api.interactiveOnlyGate") {
			t.Errorf("%s declares InteractiveOnly but the gate is not the link right after authentication — the declaration says an "+
				"API key is refused and nothing (or something too late) enforces it: %v", key, chain)
		}
	}
	if flagged == 0 {
		t.Fatal("no endpoint declared InteractiveOnly, so this guard checked nothing")
	}
}

// interactiveHarness is the real authentication middleware, the real declarations of the
// eight routes and the real error handler, with a recorder where each handler was and a
// fake database behind the middleware.
type interactiveHarness struct {
	app     *fiber.App
	db      *chainDB
	key     string
	session string

	mu      sync.Mutex
	reached map[string]int
}

// newInteractiveHarness mounts the eight declared routes, each with its handler swapped
// for a recorder. edit, when given, changes each declaration first — the controls use
// it to take the flag or the permission away.
func newInteractiveHarness(t *testing.T, edit func(*Endpoint)) *interactiveHarness {
	t.Helper()
	declared := sharedEndpoints(t)
	h := &interactiveHarness{reached: map[string]int{}}

	reg := NewRegistry()
	for _, r := range interactiveOnlyRoutes {
		key := r.method + " " + r.path
		e, ok := declared[key]
		if !ok {
			t.Fatalf("%s is not declared in the registry", key)
		}
		e.Handler = func(c fiber.Ctx, _ *apischema.Params) error {
			h.mu.Lock()
			h.reached[key]++
			h.mu.Unlock()
			return c.SendStatus(fiber.StatusNoContent)
		}
		if edit != nil {
			edit(&e)
		}
		reg.Register(e)
	}

	fake, uid, apiKey := newChainDB(t)
	jwtSvc := auth.NewJWTService("interactive-only-test-secret", 15*time.Minute, 7*24*time.Hour)
	srv := &Server{queries: db.New(fake), jwtService: jwtSvc}
	token, _, err := jwtSvc.GenerateAccessToken(uid, chainEmail, "admin")
	if err != nil {
		t.Fatalf("access token: %v", err)
	}

	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	// A request that gets past the gate and finds no RBAC engine (this server has none)
	// fails inside the permission middleware; recovered, that is a response a test
	// asserts against by name, and not a crash of the package.
	app.Use(fiberrecover.New())
	mountRegistry(app, reg, srv.authRequired(), everyNodeIsAMember())

	h.app, h.db, h.key, h.session = app, fake, apiKey, token
	return h
}

// request sends the route's own request, valid for its schema, with the bearer.
func (h *interactiveHarness) request(t *testing.T, method, target, body, bearer string) (*http.Response, ErrorResponse) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+bearer)
	resp, err := h.app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var env ErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp, env
}

func (h *interactiveHarness) reachedCount(key string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reached[key]
}

// TestInteractiveOnly_AnAPIKeyIsRefusedOnEveryDeclaredRoute is the enforcement, route by
// route, through everything real but the handler: a real nxra_ key found by the real
// authRequired through GetAPIKeyByHash (so the marker the gate reads is the one
// authenticateAPIKey writes), the registry's own declarations with their real permissions,
// and a recorder for a handler, so "not reached" is something to count. The answer is the
// gate's 403, and the database saw only the key's lookup and its last-used stamp. The bodies
// are valid for each route's schema, so a missing gate would be answered by the handler, not
// by an accident of validation. Two controls say the 403 is the flag's doing: without the
// flag the same key reaches the handler, and with it a session does.
func TestInteractiveOnly_AnAPIKeyIsRefusedOnEveryDeclaredRoute(t *testing.T) {
	t.Run("the key is refused", func(t *testing.T) {
		for _, r := range interactiveOnlyRoutes {
			key := r.method + " " + r.path
			t.Run(key, func(t *testing.T) {
				h := newInteractiveHarness(t, nil)

				resp, env := h.request(t, r.method, r.target, r.body, h.key)

				if resp.StatusCode != http.StatusForbidden || env.Error != "forbidden" || env.Message != interactiveOnlyMessage {
					t.Errorf("status = %d, error = %q, message = %q; want 403 forbidden: %s",
						resp.StatusCode, env.Error, env.Message, interactiveOnlyMessage)
				}
				if n := h.reachedCount(key); n != 0 {
					t.Errorf("the handler was reached %d times by a request authenticated with an API key", n)
				}
				h.db.waitForKeyStamp(t)
				if got := h.db.statements(); !slices.Equal(got, []string{"GetAPIKeyByHash", "UpdateAPIKeyLastUsed"}) {
					t.Errorf("statements = %v, want only the key's lookup and stamp", got)
				}
			})
		}
	})

	for _, c := range []struct {
		name, bearerOf string // "key" or "session"
		edit           func(*Endpoint)
		why            string
	}{
		{"control: with the flag gone the same key reaches the handler", "key", func(e *Endpoint) {
			e.InteractiveOnly = ""
			e.Permissions = Permissions{SelfService: "test control: authorization is not what is under test"}
		}, "the request is not valid for the route, so the refusals above proved nothing"},
		{"control: with the flag on a session reaches the handler", "session", func(e *Endpoint) {
			e.Permissions = Permissions{SelfService: "test control: authorization is not what is under test"}
		}, "the gate refuses more than API keys"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newInteractiveHarness(t, c.edit)
			bearer := h.key
			if c.bearerOf == "session" {
				bearer = h.session
			}
			for _, r := range interactiveOnlyRoutes {
				key := r.method + " " + r.path
				resp, env := h.request(t, r.method, r.target, r.body, bearer)
				if resp.StatusCode != http.StatusNoContent || h.reachedCount(key) != 1 {
					t.Errorf("%s: status = %d (%q), handler reached %d times, want 204 and once: %s",
						key, resp.StatusCode, env.Message, h.reachedCount(key), c.why)
				}
			}
		})
	}
}

// TestDocEndpoints_PublishesTheInteractiveOnlyReason: the docs payload is rendered from
// the declarations, so the reason an API key is refused on a route is published with it,
// and only with it: the key is absent from the JSON of every other route, which a key
// may call as far as its owner's permissions go.
func TestDocEndpoints_PublishesTheInteractiveOnlyReason(t *testing.T) {
	const reason = "a leaked key must not be able to do this"

	reg := NewRegistry()
	flagged := validEndpoint()
	flagged.Path = "/api/v1/probe/flagged"
	flagged.Parameters = apischema.Properties{}
	flagged.Permissions = Permissions{SelfService: "acts on the caller's own account"}
	flagged.InteractiveOnly = reason
	plain := flagged
	plain.Path = "/api/v1/probe/plain"
	plain.InteractiveOnly = ""
	reg.Register(flagged)
	reg.Register(plain)

	byPath := map[string]handlers.APIEndpoint{}
	for _, ep := range docEndpoints(reg) {
		byPath[ep.Path] = ep
	}
	if got := byPath[flagged.Path].InteractiveOnly; got != reason {
		t.Errorf("the flagged route publishes interactive_only = %q, want its declaration's reason", got)
	}
	if got := byPath[plain.Path].InteractiveOnly; got != "" {
		t.Errorf("an unflagged route publishes interactive_only = %q, want none", got)
	}
	raw, err := json.Marshal(byPath[plain.Path])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "interactive_only") {
		t.Errorf("an unflagged route's JSON mentions interactive_only: %s", raw)
	}
	raw, _ = json.Marshal(byPath[flagged.Path])
	if !strings.Contains(string(raw), `"interactive_only":"`+reason+`"`) {
		t.Errorf("the flagged route's JSON does not carry the reason: %s", raw)
	}

	// And the real registry: each of the eight publishes its own.
	published := map[string]string{}
	for _, ep := range docEndpoints(sharedRouteStub(t).registry) {
		if ep.InteractiveOnly != "" {
			published[ep.Method+" "+ep.Path] = ep.InteractiveOnly
		}
	}
	for _, r := range interactiveOnlyRoutes {
		if published[r.method+" "+r.path] == "" {
			t.Errorf("%s %s publishes no interactive_only reason in the docs payload", r.method, r.path)
		}
	}
	if len(published) != len(interactiveOnlyRoutes) {
		t.Errorf("%d routes publish interactive_only, want %d", len(published), len(interactiveOnlyRoutes))
	}
}
