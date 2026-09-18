package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/apischema"
	nexapp "github.com/bigjakk/nexara/internal/app"
	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/config"
)

// This file is the dynamic counterpart to
// TestGuard_RegistryHandlersOnlyReadDeclaredParams (registry_paramkey_guard_test.go).
// That guard proves STATICALLY, by reading handler source with go/ast, that no
// handler calls a Params accessor for a key its own declaration lacks. It says
// so in its own limitation note: it proves the call is unreachable-if-wrong, not
// that the request which reaches it is ever actually well-formed.
//
// This proves the same property DYNAMICALLY: for every endpoint the real
// registry declares (s.buildRegistry(), the ~535-route table setupRoutes
// mounts), synthesize a request that satisfies the endpoint's OWN schema —
// respecting Enum, Format, Pattern, numeric/length bounds and Source — drive
// it through the mounted registry with the permission gate stubbed open, and
// require that extraction runs, validation runs, and the handler is reached
// WITHOUT PANICKING. A handler that reads an undeclared key panics inside
// apischema.Params by design (see that package's doc comment); the request
// this test builds is what makes that panic actually fire instead of waiting
// for the first real caller to send exactly the right shape to trigger it.
//
// # Why a real, disconnected Server rather than the zero-value stub
//
// api_docs_drift_test.go's newRouteStubServer builds a Server whose handler
// fields are &handlers.ClusterHandler{} and friends — zero-value structs
// whose unexported queries/proxmoxCache/eventPub fields are nil. Its own doc
// comment says why that is safe THERE and would not be safe here: "No
// handler is ever invoked — route registration only stores bound method
// values." A handler this test actually calls would nil-pointer-panic on its
// first `h.queries.Something(ctx, ...)`, the instant it tried to reach its
// dependencies — which would make EVERY substantive route panic, for a
// reason that has nothing to do with the registry layer this test exists to
// check.
//
// newSweepServer below instead builds a Server through the SAME production
// constructors cmd/nexara uses — nexapp.New then api.New — with a REAL
// *pgxpool.Pool and *redis.Client pointed at 127.0.0.1 with nothing
// listening (the exact pattern internal/app/app_test.go's testPool uses to
// exercise app.New with no database at all). Real-but-disconnected clients
// do not nil-panic: pgxpool and go-redis both connect lazily, so the first
// query gets a plain "connection refused" error and the handler returns it
// normally, the same way it would return a real Postgres or Proxmox error.
// That is what makes "the handler ran and could not reach its dependencies"
// (expected, not a finding) distinguishable from "something in the registry
// or permission layer panicked" (a finding) by whether a PANIC happened at
// all — see classifySweepOutcome — rather than by pattern-matching a
// message a real handler could also legitimately produce.
//
// # How a registry rejection is told apart from a downstream failure
//
// The crux of whether this test means anything. Four shapes are
// unambiguous, in decreasing order of how load-bearing they are, and
// everything else is a pass:
//
//  1. A panic. Nothing in Endpoint.serve's normal path panics — extract()
//     and Properties.Validate() both return plain errors on a caller
//     mistake (registry_params.go, apischema/validate.go) — so a panic can
//     only come from inside the handler body or from apischema.Params
//     itself refusing an undeclared key or a wrong-type accessor. This
//     test mounts its own recover.New with a PanicHandler that answers a
//     status (599) no real endpoint ever produces, so classification never
//     has to guess a panic happened from a message; TestGuard_
//     RouteSweepCatchesAnUndeclaredParamRead proves the mechanism against a
//     handler built to trigger exactly this.
//  2. HTTP 400. Endpoint.serve's only source of one is extract() rejecting
//     a misplaced parameter or Properties.Validate() rejecting the request
//     against its OWN schema (registry.go's validationError maps a
//     *apischema.ValidationError to 400). Since every synthesized request
//     is built FROM that same schema, a 400 means either the synthesizer
//     misread a facet or the declaration is internally inconsistent in a
//     way Compile() does not catch — a finding either way.
//
//     A handler CAN also return its own 400 for a business rule apischema
//     cannot express — a cross-field exclusivity, a value that needs a
//     live database row to judge, a probe against a caller-supplied host —
//     and running the first version of this sweep against the real
//     registry found roughly forty of them. Each was read individually
//     (see synthesizeSweepRequest's overrides and sweepExpectedFindings
//     below): most turned out to be the synthesizer being too generic
//     (an unrecognised "format" field wanting "raw", a cron-shaped
//     "schedule" string, an RFC3339 timestamp) and got fixed by teaching it
//     a better value, which is a legitimate synthesizer improvement, not a
//     workaround — it makes the sweep reach FURTHER into the handler, not
//     less. A small remainder generically cannot be satisfied by one
//     independent, stateless request (a multipart file upload with no
//     apischema body parameter at all; TOTP verification, which needs a
//     secret a prior request enrolled) and those are named explicitly in
//     sweepExpectedFindings with the reason, checked every run, and
//     reported separately from an actual finding rather than silently
//     dropped. See the task report for the full list and the reasoning
//     behind each entry.
//  3. HTTP 500 whose message is EXACTLY "Request validation failed" —
//     validationError's other branch, for a schema defect Compile() itself
//     should have caught at Register time. Unreachable through the real
//     registry (Register panics first), which is why the second half of
//     this file's demo constructs a registry that bypasses Register to
//     prove the classification still fires if it ever became reachable.
//  4. HTTP 401/403. On a route mounted behind Permissions.Check or
//     .Alternatives, sweepAuth's grant (allowAllRBAC.HasPermission/
//     HasGlobalPermission, unconditional) is unreachable, so either status
//     there is always a finding — registry_chain_test.go already proves
//     the permission gate itself denies and allows correctly, so nothing
//     here should ever see it deny.
//
//     On a route NOT gated that way (Public, SelfService, Deferred,
//     Advisory), a 401/403 is a finding ONLY when its message is EXACTLY
//     rbacDeniedMessage ("Insufficient permissions") — the literal text
//     BOTH requirePerm/requireClusterPerm (permission.go) and several
//     Deferred handlers' OWN internal accessibleClusters-based checks
//     answer with (veeamScopeForAction in veeam.go, for one). That message
//     is exactly what sweepAuth's sweepDeferredPermissionPairs grant
//     exists to prevent, so seeing it anyway means the grant list is
//     missing an (action, resource) pair a handler actually asks for —
//     the gap sweepDeferredPermissionPairs' own comment describes, and the
//     one an earlier version of this file got backwards: it treated "not
//     Check/Alternatives-gated" as license to excuse ANY 401/403, which
//     excused precisely the routes sweepDeferredPermissionPairs exists to
//     cover and left the rule protecting only routes where
//     allowAllRBAC.HasPermission's unconditional true makes it
//     unreachable. sweepFindingClassification is the fixed version; see
//     its own doc comment for how the bug was found.
//
//     Any OTHER 401/403 on a non-gated route is still excused: it is far
//     more likely to be the handler's own UNRELATED check — an invalid
//     refresh token, an unenrolled TOTP session — which is exactly the
//     downstream category this sweep does not evaluate.
//
// Everything else — 2xx, 404, 409, 422, a 500 whose message names whatever
// the handler actually failed at ("Failed to list clusters", a bare
// "dial tcp 127.0.0.1:1: connect: connection refused", …), 502, 503 — means
// the handler ran and did what a handler does against an unreachable
// database, Redis or Proxmox: return a plain error. That is what this test
// is NOT evaluating, on purpose; TestSweepClassifiesDispatchOutcomes pins
// concrete examples of both sides of the line.

// ---------------------------------------------------------------------------
// Server construction: a real Server, real (but unreachable) dependencies.
// ---------------------------------------------------------------------------

// sweepUnreachableAddr is loopback with nothing bound to it: connecting
// fails immediately with ECONNREFUSED rather than timing out, which is what
// lets this whole sweep run in-process with no Docker and no real Postgres
// or Redis. Mirrors testPool in internal/app/app_test.go.
const sweepUnreachableAddr = "127.0.0.1:1"

// sweepJWTSecret and sweepEncryptionKey are never used to sign or decrypt
// anything real — every DB read this sweep triggers fails at the connection
// (sweepUnreachableAddr) before any handler reaches a query result to
// decrypt — they exist only so serverDeps.hasCrypto()/hasFullSecure() are
// true and every handler factory in server.go actually constructs its
// handler instead of leaving the field nil (which would silently drop that
// domain's routes out of s.buildRegistry() — see newSweepServer).
const sweepJWTSecret = "test-secret-at-least-16-chars"

// sweepEncryptionKey is 32 bytes hex-encoded, the shape
// config.generateEncryptionKey produces (internal/config/secrets.go).
var sweepEncryptionKey = strings.Repeat("00", 32)

func newSweepPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(),
		"postgres://nobody:nobody@"+sweepUnreachableAddr+"/nexara_unreachable_test?sslmode=disable")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newSweepServer builds a REAL Server through the production constructors —
// nexapp.New then api.New (this package's New) — exactly as cmd/nexara does,
// over a real-but-disconnected Postgres pool and Redis client. See the file
// comment for why a real, if unreachable, dependency is the load-bearing
// choice: it is what makes a handler return a plain error instead of
// nil-pointer-panicking the moment it reaches for something this test never
// wired.
//
// Both the pool and Redis are supplied (unlike internal/app/app_test.go's
// TestNew_FullyWiredBuildsEverything, which passes nil Redis) because
// AuthHandler needs a SessionManager, and SessionManager needs Redis
// (registerAuth's hasDB()&&jwt&&sessionMgr gate in server.go) — without it
// every /auth/* route the registry declares would be silently absent from
// s.registry, and this sweep would never see it.
func newSweepServer(t *testing.T) *Server {
	t.Helper()

	cfg := &config.Config{
		JWTSecret:            sweepJWTSecret,
		EncryptionKey:        sweepEncryptionKey,
		AccessTokenTTL:       15 * time.Minute,
		RefreshTokenTTL:      7 * 24 * time.Hour,
		TaskHistoryRetention: 168 * time.Hour,
		DataDir:              t.TempDir(),
		ChangelogRepo:        "bigjakk/Nexara",
		// Generous on purpose: this sweep can send up to two requests to
		// the SAME per-route limiter (required-only, then with-optional),
		// and a couple of routes share ONE limiter instance across several
		// endpoints (registerVeeamEndpoints' connect/control — see
		// veeamConnectLimiter's own comment in middleware.go). The general
		// app-level limiter this config value would otherwise drive is
		// never mounted here at all — see below.
		RateLimitMax:        1_000_000,
		RateLimitExpiration: time.Minute,
		MaxUploadSize:       16106127360,
		ProxyHeader:         "X-Forwarded-For",
	}

	pool := newSweepPool(t)
	rdb := redis.NewClient(&redis.Options{Addr: sweepUnreachableAddr})
	t.Cleanup(func() { _ = rdb.Close() })

	// Discarded: server construction logs a benign "proxmox cache:
	// subscriber failed to start" warning against sweepUnreachableAddr, and
	// this sweep's own findings are reported through t.Errorf/t.Logf, not
	// through the server's logger.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	a := nexapp.New(context.Background(), cfg, pool, rdb, logger)
	t.Cleanup(a.Close)

	// New (server.go) also builds s.app via buildFiberConfig + setupMiddleware
	// and mounts the registry onto IT. This sweep deliberately never touches
	// s.app: setupMiddleware installs the app-level auth/refresh/ws-token/
	// general rate limiters (middleware.go), and reaching a route through them
	// would mean minting a real JWT and running requests through the REAL
	// RBAC engine — which, backed by the same disconnected pool, would fail
	// closed on every permission check before any handler was reached. This
	// sweep takes only s.registry and mounts it itself, the same way
	// newRegistryApp does in registry_request_test.go, with sweepAuth standing
	// in for authentication — "stub the RBAC engine to grant everything" per
	// the task brief, the same shape registry_chain_test.go's stubAuth uses.
	return New(a)
}

// ---------------------------------------------------------------------------
// Auth/permission stub: reach every handler, the way registry_chain_test.go's
// stubAuth does, but grant everything rather than keying off a fixed map —
// this sweep's job is to prove a route DISPATCHES, not to re-prove the
// permission gate denies and allows correctly (registry_chain_test.go
// already does that end to end).
// ---------------------------------------------------------------------------

// sweepAuthHeader gates sweepAuth so a request that reaches it by accident
// (outside this file's own dispatch helper) fails the same way a real
// unauthenticated request would, rather than silently succeeding.
const sweepAuthHeader = "X-Sweep-Auth"

// allowAllRBAC structurally satisfies handlers' unexported permissionEngine
// interface (see stubRBACEngine in registry_chain_test.go, which this
// mirrors) and grants every action on every resource, cluster-scoped or
// global.
//
// HasPermission/HasGlobalPermission grant unconditionally, which covers
// every route mounted through Permissions.Check/.Alternatives — the
// registry's OWN declared gate, read by handlers.requirePerm/
// requireClusterPerm. LoadUserPermissions is a second, narrower door: it is
// the only method permission.go's accessibleClusters calls, used inside
// several Deferred/Advisory handlers to FILTER a listing or to resolve which
// cluster a sub-resource (a Veeam server, in particular) belongs to before
// authorizing an action on it. accessibleClusters filters its returned
// UserPermissions.Permissions by EXACT (action, resource) match with no
// wildcard, so an empty list here — the obvious "grant nothing to filter"
// zero value — reads as "the caller holds no grant at all" and the SAME
// handlers that would otherwise reach a plain downstream DB error instead
// fail closed to 403 "Insufficient permissions" (veeamScopeForAction,
// veeam.go) before ever touching h.queries. permissions carries one
// global-scoped entry per (action, resource) pair this codebase actually
// asks accessibleClusters about, so LoadUserPermissions grants just as
// unconditionally as the other two methods do, and every Deferred handler
// that filters through it is reached as deep as a Check-gated one.
type allowAllRBAC struct {
	permissions []auth.ScopedPermission
}

func (allowAllRBAC) HasPermission(context.Context, uuid.UUID, string, string, string, uuid.UUID) (bool, error) {
	return true, nil
}

func (allowAllRBAC) HasGlobalPermission(context.Context, uuid.UUID, string, string) (bool, error) {
	return true, nil
}

func (a allowAllRBAC) LoadUserPermissions(context.Context, uuid.UUID) (*auth.UserPermissions, error) {
	return &auth.UserPermissions{Permissions: a.permissions}, nil
}

// sweepDeferredPermissionPairs are (action, resource) pairs consulted only
// from INSIDE a handler body via accessibleClusters — never through
// Permissions.Check or .Alternatives, so sweepDeclaredPermissionPairs (which
// walks the registry's own declarations) cannot see them. Found by:
//
//	grep -rhoP '(?:accessibleClusters|hasClusterPerm|hasGlobalPerm|requirePerm|requireClusterPerm)\(c(?:\.Context\(\))?, *"[a-z_]+", *"[a-z_]+"' internal/api/handlers/*.go
//
// plus veeamScopeForAction's "execute" call (veeam_control.go:550), which
// that grep's literal-argument pattern misses because the action there is a
// local variable at one of its two call sites, not a string literal.
//
// If a future Deferred handler asks accessibleClusters about a pair not
// listed here, the route it belongs to fails closed to 403 exactly the way
// veeam.go's routes did before this list existed — visible immediately as a
// new finding, not a silent gap, which is why this is a plain list to
// extend rather than something this file tries to compute by other means.
var sweepDeferredPermissionPairs = []auth.ScopedPermission{
	{Action: "acknowledge", Resource: "alert", ScopeType: "global"},
	{Action: "delete", Resource: "pbs", ScopeType: "global"},
	{Action: "delete", Resource: "storage", ScopeType: "global"},
	{Action: "execute", Resource: "veeam", ScopeType: "global"},
	{Action: "generate", Resource: "report", ScopeType: "global"},
	{Action: "manage", Resource: "alert", ScopeType: "global"},
	{Action: "manage", Resource: "cluster", ScopeType: "global"},
	{Action: "manage", Resource: "migration", ScopeType: "global"},
	{Action: "manage", Resource: "network", ScopeType: "global"},
	{Action: "manage", Resource: "notification_channel", ScopeType: "global"},
	{Action: "manage", Resource: "notification_dlq", ScopeType: "global"},
	{Action: "manage", Resource: "pbs", ScopeType: "global"},
	{Action: "manage", Resource: "report", ScopeType: "global"},
	{Action: "manage", Resource: "role", ScopeType: "global"},
	{Action: "manage", Resource: "settings", ScopeType: "global"},
	{Action: "manage", Resource: "storage", ScopeType: "global"},
	{Action: "manage", Resource: "task", ScopeType: "global"},
	{Action: "manage", Resource: "user", ScopeType: "global"},
	{Action: "manage", Resource: "veeam", ScopeType: "global"},
	{Action: "manage", Resource: "vm", ScopeType: "global"},
	{Action: "manage", Resource: "vm_folder", ScopeType: "global"},
	{Action: "manage", Resource: "vm_import", ScopeType: "global"},
	{Action: "view", Resource: "alert", ScopeType: "global"},
	{Action: "view", Resource: "audit", ScopeType: "global"},
	{Action: "view", Resource: "backup", ScopeType: "global"},
	{Action: "view", Resource: "cluster", ScopeType: "global"},
	{Action: "view", Resource: "container", ScopeType: "global"},
	{Action: "view", Resource: "migration", ScopeType: "global"},
	{Action: "view", Resource: "notification_dlq", ScopeType: "global"},
	{Action: "view", Resource: "pbs", ScopeType: "global"},
	{Action: "view", Resource: "report", ScopeType: "global"},
	{Action: "view", Resource: "task", ScopeType: "global"},
	{Action: "view", Resource: "veeam", ScopeType: "global"},
	{Action: "view", Resource: "vm", ScopeType: "global"},
}

// sweepDeclaredPermissionPairs walks reg's OWN declarations — Check,
// Alternatives and Advisory alike — so the grant list stays complete as the
// registry grows, with no manual bookkeeping for the common (gate-shaped)
// case. Combined with sweepDeferredPermissionPairs in sweepAuth.
func sweepDeclaredPermissionPairs(reg *Registry) []auth.ScopedPermission {
	seen := map[[2]string]bool{}
	var out []auth.ScopedPermission
	add := func(action, resource string) {
		if action == "" || resource == "" {
			return
		}
		key := [2]string{action, resource}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, auth.ScopedPermission{Action: action, Resource: resource, ScopeType: "global"})
	}
	for _, e := range reg.Endpoints() {
		if e.Permissions.Check != nil {
			add(e.Permissions.Check.Action, e.Permissions.Check.Resource)
		}
		for _, alt := range e.Permissions.Alternatives {
			add(alt.Action, alt.Resource)
		}
		if e.Permissions.Advisory != nil {
			add(e.Permissions.Advisory.Action, e.Permissions.Advisory.Resource)
		}
	}
	return out
}

// sweepAuth stands in for Server.authRequired, exactly where mountRegistry
// wants an authentication middleware (registry.go refuses a nil one). reg is
// the registry being swept, so the grant list always covers whatever it
// currently declares — see sweepDeclaredPermissionPairs.
func sweepAuth(reg *Registry) fiber.Handler {
	permissions := append(sweepDeclaredPermissionPairs(reg), sweepDeferredPermissionPairs...)
	return func(c fiber.Ctx) error {
		if c.Get(sweepAuthHeader) == "" {
			return fiber.NewError(fiber.StatusUnauthorized, "sweep: missing "+sweepAuthHeader)
		}
		c.Locals("user_id", uuid.MustParse(testUserID))
		c.Locals("rbac_engine", allowAllRBAC{permissions: permissions})
		return c.Next()
	}
}

// ---------------------------------------------------------------------------
// Dispatch + classification.
// ---------------------------------------------------------------------------

// sweepPanicStatus is a status no real handler in this codebase ever
// produces (statusText in errors.go names nothing past 429, and nothing in
// the handler tree returns a literal 599). sweepPanicHandler uses it as an
// unambiguous "a panic happened here" signal, so classifySweepOutcome never
// has to infer a panic from a message a real handler's own error text could
// coincidentally also contain.
const sweepPanicStatus = 599

// sweepPanicHandler is this sweep's recover.Config.PanicHandler. Without one,
// recover.New's DefaultPanicHandler wraps a non-error panic value with
// fmt.Errorf("%v", r), which errorHandler then reports as a bare
// "Internal Server Error" 500 — indistinguishable from a dozen legitimate
// downstream failures. Installing a distinct sentinel status is the whole
// difference between this test being able to tell a panic apart from a
// downstream failure and not.
//
// This has to run inside the SAME goroutine the handler panics in:
// fiber.App.Test spawns request handling on its own goroutine
// (app.go's Test method), and Go cannot recover a panic from any goroutine
// but the one it occurred in — a bare `defer recover()` around app.Test
// itself does nothing but let the panic crash the whole `go test` binary a
// moment later. recover.New's own defer runs where the panic actually
// happens, which is the only place a recover can work.
func sweepPanicHandler(_ fiber.Ctx, r any) error {
	return fiber.NewError(sweepPanicStatus, fmt.Sprintf("%v", r))
}

// sweepOutcome is what one synthesized request produced.
type sweepOutcome struct {
	status      int
	message     string
	dispatchErr error // non-nil means app.Test itself failed (e.g. a timeout)
}

// sweepDispatch sends one request through app and reports what came back.
// It never calls t.Fatal: a transport-level failure (dispatchErr) is
// reported as its own finding so one hung or broken route does not cut the
// sweep short before every other endpoint has had its turn.
func sweepDispatch(app *fiber.App, method, target string, body []byte) sweepOutcome {
	var req *http.Request
	if len(body) > 0 {
		req = httptest.NewRequest(method, target, bytes.NewReader(body))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Header.Set(sweepAuthHeader, "yes")

	// fiber.App.Test defaults to a 1s timeout with FailOnTimeout true
	// (app.go). A couple of real routes bound their own outbound call at
	// 10s (cluster fetch-fingerprint/verify-certificate,
	// internal/changelog's fetchTimeout) — comfortably inside 15s even if
	// this sandbox has no route to the outside world and every such call
	// fails slow instead of fast.
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second, FailOnTimeout: true})
	if err != nil {
		return sweepOutcome{dispatchErr: fmt.Errorf("app.Test(%s %s): %w", method, target, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return sweepOutcome{dispatchErr: fmt.Errorf("read response body for %s %s: %w", method, target, err)}
	}
	var env ErrorResponse
	_ = json.Unmarshal(raw, &env) // a 2xx (or a non-standard error) body is not this envelope; best-effort only
	return sweepOutcome{status: resp.StatusCode, message: env.Message}
}

// sweepClassification is what classifySweepOutcome decided about one
// sweepOutcome. sweepOK ("everything else") deliberately covers far more
// than success: see the file comment for the full list of what lands there
// and why.
type sweepClassification int

const (
	sweepOK sweepClassification = iota
	sweepFindingPanic
	sweepFindingRegistryRejected
	sweepFindingDeclarationBug
	sweepFindingAuthWiring
	sweepFindingDispatchError
	sweepFindingRoutingMiss
)

func (k sweepClassification) String() string {
	switch k {
	case sweepFindingPanic:
		return "panic"
	case sweepFindingRegistryRejected:
		return "registry rejected the synthesized request (400)"
	case sweepFindingDeclarationBug:
		return `schema declaration bug (500 "Request validation failed")`
	case sweepFindingAuthWiring:
		return "sweep auth/permission stub was not honoured"
	case sweepFindingDispatchError:
		return "app.Test itself failed (transport/timeout)"
	case sweepFindingRoutingMiss:
		return "synthesized request did not match any route (404 \"Not Found\")"
	default:
		return "ok"
	}
}

// declarationBugMessage is validationError's (registry.go) exact string for
// a schema defect Compile() should already have caught at Register time —
// copied here as a literal, not imported, because it names an unexported
// function's hard-coded text rather than a constant either file exports.
// TestGuard_RouteSweepCatchesAnUndeclaredParamRead and
// TestSweepClassifiesDispatchOutcomes both pin that this stays meaningful.
const declarationBugMessage = "Request validation failed"

// rbacDeniedMessage is requirePerm/requireClusterPerm's (handlers/permission.go)
// literal denial text — the SAME string a Deferred/Advisory handler's own
// internal accessibleClusters-based refusal reaches for too (veeamScopeForAction
// in veeam.go, for one), since both paths ultimately answer through
// fiber.NewError(fiber.StatusForbidden, "Insufficient permissions"). That
// reuse is exactly what makes it the right signal for "a permission check
// said no" regardless of WHICH permission check — see sweepFindingClassification.
const rbacDeniedMessage = "Insufficient permissions"

// notFoundFallbackMessage is fiber.ErrNotFound's message when the router
// matches no route at all: NewError(404) with no message argument defaults
// to utils.StatusMessage(404) ("Not Found", verified empirically against
// this exact app.Test/errorHandler pairing — fiber v3 does NOT spell it
// "Cannot GET /..." the way some older frameworks do). No handler in this
// codebase calls fiber.NewError(fiber.StatusNotFound) with zero arguments
// (grep confirms it), so every REAL application 404 carries its own specific
// message ("cluster not found", "OIDC config not found", …) and this exact
// literal is safe to treat as "the request never reached a route at all."
const notFoundFallbackMessage = "Not Found"

// classifySweepOutcome draws the line this whole file lives or dies on. See
// the file comment for the reasoning behind each case; TestSweepClassifies
// DispatchOutcomes pins concrete examples of both sides.
//
// This function alone cannot apply case 4's "only on a Check/Alternatives-
// gated route, UNLESS the message is the RBAC engine's own denial text"
// qualifier — it has no Endpoint to consult — so
// TestGuard_EveryRegistryRouteDispatchesWithoutPanicking never calls this
// directly for that case; sweepFindingClassification wraps it with the
// missing route context instead. classifySweepOutcome stays a pure function
// of the outcome alone so TestSweepClassifiesDispatchOutcomes can pin it
// with plain table cases.
func classifySweepOutcome(o sweepOutcome) sweepClassification {
	switch {
	case o.dispatchErr != nil:
		return sweepFindingDispatchError
	case o.status == sweepPanicStatus:
		return sweepFindingPanic
	case o.status == fiber.StatusNotFound && o.message == notFoundFallbackMessage:
		return sweepFindingRoutingMiss
	case o.status == fiber.StatusBadRequest:
		return sweepFindingRegistryRejected
	case o.status == fiber.StatusInternalServerError && o.message == declarationBugMessage:
		return sweepFindingDeclarationBug
	case o.status == fiber.StatusUnauthorized || o.status == fiber.StatusForbidden:
		return sweepFindingAuthWiring
	default:
		return sweepOK
	}
}

// sweepFindingClassification is classifySweepOutcome plus the one piece of
// route context it cannot see on its own: whether e is gated by
// Permissions.Check/.Alternatives (sweepGated).
//
// A sweepFindingAuthWiring verdict is downgraded to sweepOK only when the
// route is NOT gated AND the message is not rbacDeniedMessage. That second
// condition is the fix for a real bug this file shipped with: emptying
// allowAllRBAC.LoadUserPermissions turned every Deferred-permission route's
// OWN internal refusal (veeamScopeForAction and the rest of
// sweepDeferredPermissionPairs' callers) into a 403 that the old
// "!gated → sweepOK" rule excused unconditionally, because none of those
// routes are Check/Alternatives-gated by definition — precisely the routes
// the rule existed to protect, and the routes on which the grant is
// UNREACHABLE for a Check-gated route, since allowAllRBAC.HasPermission
// always returns true there. A 401, or a 403 carrying some OTHER handler's
// own message (an invalid refresh token, an unenrolled TOTP session), is
// still legitimately excused on a non-gated route — that half of the
// original rule was correct and is unchanged.
func sweepFindingClassification(o sweepOutcome, gated bool) sweepClassification {
	kind := classifySweepOutcome(o)
	if kind == sweepFindingAuthWiring && !gated && o.message != rbacDeniedMessage {
		return sweepOK
	}
	return kind
}

// ---------------------------------------------------------------------------
// Synthesizer: build a value that satisfies one declared Property, and a
// full request that satisfies one Endpoint's Parameters.
// ---------------------------------------------------------------------------

// formatSamples gives a known-good literal per registered apischema format
// (apischema/format.go's init()). Every one of these is the FORMATTED
// (post-normalization) value, not raw input, because it is compared against
// this property's own MinLength/MaxLength/Pattern as-is — see
// satisfiesStringConstraints.
var formatSamples = map[string]string{
	"uuid":         "12345678-1234-1234-1234-123456789abc",
	"node-name":    "pve-01",  // CLAUDE.md placeholder scheme
	"storage-id":   "store01", // CLAUDE.md placeholder scheme
	"pve-configid": "cfg-test01",
	"disk-size":    "10",
	"cidr":         "192.0.2.0/24", // CLAUDE.md placeholder scheme (TEST-NET-1)
	"email":        "user@example.com",
	"ip":           "192.0.2.10",        // CLAUDE.md placeholder scheme
	"mac-addr":     "02:00:00:00:00:01", // CLAUDE.md placeholder scheme
	// A 64-hex-char value shaped like a SHA-256 fingerprint, built below
	// from a counting sequence rather than any real certificate — CLAUDE.md's
	// "no lab identifiers" rule calls out TLS/SSH fingerprints by name.
	"fingerprint-sha256": syntheticFingerprint(),
	"bwlimit":            "1024",
}

// syntheticFingerprint builds a 32-pair (64 hex char) value satisfying
// apischema's fpColonRe, from a counting sequence so nothing resembling a
// real fingerprint is ever typed into the source.
func syntheticFingerprint() string {
	pairs := make([]string, 32)
	for i := range pairs {
		pairs[i] = fmt.Sprintf("%02X", i)
	}
	return strings.Join(pairs, ":")
}

// stringCandidatePool is tried, in order, against any String property that
// declares a Pattern but no Format: the first entry that satisfies BOTH the
// pattern and this property's own length bounds wins. Surveyed against
// every distinct Pattern the real registry declares as of this writing (29
// of them, from plain PVE-style identifiers to a device path, an email, a
// bare 6-digit TOTP code and a "digits-dash-digits" range) — every one is
// satisfied by an entry here. A pattern this pool cannot satisfy is reported
// as a skip rather than guessed at; see maxAllowedRouteSweepSkips.
var stringCandidatePool = []string{
	"test01",
	"test-01",
	"123456",
	"8006",
	"10",
	"10G",
	"/dev/sda",
	"user@example.com",
	"100-0",
	"12345678-1234-1234-1234-123456789abc",
	"https://example.com",
	"TestValue0123456789",
	"a",
	"ab",
}

// sweepValueOverrides supplies a specific value for a parameter NAME when
// the generic synthesizer's schema-only value satisfies apischema but not a
// handler's own business rule beneath it — a distinct check the handler
// makes on the DECODED value, beyond what the property's Type/Enum/Format/
// Pattern/bounds already constrain. Every entry was found by running this
// sweep, reading the resulting 400's message and the handler code that
// produced it (cited on each line), and picking a value that reaches
// further into the handler rather than merely a different failure — which
// is the same reasoning that makes stringCandidatePool worth having at all,
// just narrower than a Pattern can express.
//
// Scoped by NAME because the survey behind this file (registry_dump's
// resolved-source listing, one line per declared parameter across all 535
// endpoints) confirmed every name below has exactly ONE apischema Type
// everywhere it appears in this registry — including "vmid", which is
// Integer in most places and a String selector list in PBS backup-job
// bodies; apischema's own coerce() accepts a JSON number for a String field
// (validate.go's toString), so one override value is genuinely safe in
// both. A name that turned out to mean two INCOMPATIBLE things would need
// scoping by route instead, the way sweepRouteOverrides is.
//
// Consulted only when the property declares no Enum (synthesizeValueFor):
// an Enum's first entry is already proven valid by Compile's
// compileEnumEntry, and "format" is both an override key here (disk
// move / migration, checked by hand against raw/qcow2/vmdk) and a real Enum
// on the reports-schedule endpoint (html/csv) — the Enum path never
// consults this map, so the two do not collide.
var sweepValueOverrides = map[string]any{
	// vms.go SetVMConfig / containers.go's twin: `if len(fields) == 0`
	// after decoding — the schema can require the key, not that the object
	// hold anything (registry_vms.go's own comment on "fields").
	"fields": map[string]any{"description": "test01"},
	// vms.go MoveDisk / migrations: no Enum declared (format is a free
	// string the schema does not close), but the handler does
	// `switch format { case "raw","qcow2","vmdk": ... }` by hand.
	"format":      "raw",
	"disk_format": "raw",
	// vms.go taskUPID: extractNodeFromUPID splits on ':' and reads field 1
	// as the node name; this is what a real Proxmox UPID looks like.
	"upid": "UPID:pve-01:00001A2B:00003344:5F2E1A00:qmstart:100:test-user@pve:",
	// registry_nodes.go's syslogTimeParam Typetext spells the accepted
	// forms; "-1h" is one of them and needs no clock arithmetic to stay
	// valid. "until" is the journal endpoint's companion end-of-window
	// parameter, parsed the same way.
	"since": "-1h",
	"until": "-1h",
	// registry_alerts.go's local `timestamp` closure and audit-log's own
	// start/end params carry no Format ("apischema registers none for...",
	// per that closure's own comment) — the handler parses RFC3339 itself.
	// ends_at/end_time are deliberately a day AFTER starts_at/start_time:
	// maintenance-windows enforces ends_at > starts_at.
	"start_time": "2026-01-01T00:00:00Z",
	"end_time":   "2026-01-02T00:00:00Z",
	"starts_at":  "2026-01-01T00:00:00Z",
	"ends_at":    "2026-01-02T00:00:00Z",
	// registry_cve.go's own Description: "An EMPTY LIST is a real value
	// that clears them" — cve.go's channel-existence check has nothing to
	// look up against an empty list, and a synthesized UUID cannot name a
	// channel row a disconnected DB was never asked to create.
	"channel_ids": []any{},
	// virtiowin/version.go's versionPattern: `^\d+(?:\.\d+)*(?:-\d+)?$`.
	"target_version": "0.1.262-1",
	// vm_import_helpers.go enforces Proxmox's real floor (100), tighter
	// than the schema's declared Minimum(1) — see registry_vm_import.go's
	// bounds comment for why the schema itself stays at the wider PVE
	// limit.
	"vmid": int64(100),
	// registry_audit.go's optString(8, "<udp|tcp|tls>", ...): no Enum: (see
	// the file's own note that the three protocols are a closed set the
	// schema does not close).
	"protocol": "udp",
	// clusterAPIURLParam / pbs.go's inline "api_url" / registry_vm_import.go's
	// "url" all carry no Format — isHTTPURL/NormalizeBase-shaped checks stay
	// in the handler on purpose (registry_vm_import.go's own comment: "no
	// parameter schema can make the address decision this endpoint's whole
	// permission choice is about"). https://example.com is public and
	// resolves to a real, non-private address, so it also clears the
	// private-address confirmation these endpoints gate separately.
	"api_url": "https://example.com",
	"url":     "https://example.com",
	// ldap.go requires the ldap/ldaps scheme specifically — a plain
	// https:// value would fail this ONE check the way the generic pool
	// candidate did.
	"server_url": "ldap://ldap.example.com",
	// oidc.go's own issuer check (validateIssuerURL) parses the host as a
	// literal IP first and only falls through to a real net.LookupIP for a
	// hostname — so a TEST-NET-1 literal clears the same not-private check
	// a real issuer would need to pass, without this sweep's result
	// depending on whether the sandbox running it has DNS or network
	// access at all. net.IP.IsPrivate() is RFC 1918 only, not RFC 5737, so
	// 192.0.2.0/24 reads as a normal public address to it — the same
	// distinction url_policy_test.go pins for the same stdlib method.
	"issuer_url": "https://192.0.2.10",
	// virtiowin/mirror.go's NormalizeBase: same http(s)-only rule as
	// api_url/url, spelled with its own message.
	"base_url": "https://example.com/virtio-win/",
	// "schedule" means two DIFFERENT things depending on which endpoint
	// declares it, and this one value has to satisfy both because apischema
	// carries no Format/Pattern for either: registry_schedules.go's cluster
	// schedules and registry_reports.go's report schedules parse it through
	// robfig/cron server-side (cronspec.ValidateCron), where a plain 5-field
	// expression is valid regardless of which of the two cron dialects the
	// parser actually is. registry_backup.go's backup-job schedule and
	// registry_replication.go's replication schedule instead declare it as
	// a Proxmox CALENDAR EVENT ("<calendar event>", e.g. "mon..fri 02:00" or
	// "*/15") — a different syntax "0 0 * * *" is not valid in — but
	// neither handler checks the string locally at all (grep confirms:
	// backup.go and replication.go both just forward p.String("schedule")
	// straight into the Proxmox API call, the same as "timezone" below), so
	// this value passes them only because nothing local is asking.
	"schedule": "0 0 * * *",
	// totp.go's own Pattern-free declaration: 6 digits, checked by hand
	// after apischema's MaxLength(6) lets a non-digit 6-character string
	// through.
	"code": "123456",
	// registry_nodes.go's bare "timezone" (SetNodeTimezone, nodes.go) checks
	// only `timezone == ""` locally and forwards anything else straight to
	// Proxmox, which "owns the vocabulary and rejects a name its tzdata
	// does not carry" per the property's own Description — time.LoadLocation
	// is called only inside internal/virtiowin/schedule.go, which is what
	// check_timezone/check_schedule below actually exercise, NOT this key.
	// "UTC" is therefore not load-bearing here — any non-empty string up to
	// 64 characters passes SetNodeTimezone identically — it is used only
	// because it is a realistic example matching the Description's own
	// ("e.g. Etc/UTC"), and (per CLAUDE.md) a timezone name is only a
	// disclosure when it locates the OPERATOR, not when it is the universal
	// default every installation accepts.
	"timezone": "UTC",
	// registry_tasks.go's taskVmidsParam: one comma-separated STRING of
	// Proxmox VMIDs, declared as a bounded string rather than an array
	// specifically so apischema does not try to coerce it (see that var's
	// own comment) — parseVmidsParam (tasks.go) owns the shape instead.
	"vmids": "100,101",
	// validateLDAPFilters (ldap.go) is one rule per field, not several: user_filter
	// must contain "{{username}}" and group_filter must contain "{{userDN}}",
	// each checked in isolation. No Format or Pattern states it because
	// apischema has no facet for "the string must contain this substring".
	"user_filter":  "(uid={{username}})",
	"group_filter": "(member={{userDN}})",
	// "node_id" is NOT overridden globally: cve-scans/:scan_id/vulnerabilities
	// declares a DIFFERENT "node_id" with a strict Format:"uuid" (no empty
	// alternative), and a blanket "" would break that one instead of fixing
	// it. The maintenance-window "node_id" (Pattern emptyOrUUID, which DOES
	// accept "") gets its own value in sweepRouteOverrides below, scoped to
	// the two routes that actually declare it.
	//
	// virtio_win.go's UpdateConfig reads these two under the names
	// check_timezone/check_schedule (distinct from the plain "timezone"/
	// "schedule" registry_nodes.go and the cron-schedule endpoints use) and
	// runs them both through virtiowin.ValidateSchedule.
	"check_timezone": "UTC",
	"check_schedule": "0 0 * * *",
	// oidc.go's own comment on validateOIDCRedirectURI: it must end in
	// THIS install's own callback path (/api/v1/auth/oidc/callback), not any
	// caller-chosen one — a fixed suffix, not something a Format/Pattern can
	// state since the host in front of it varies per deployment.
	"redirect_uri": "https://example.com/api/v1/auth/oidc/callback",
}

// sweepEndpointOverride is the route-scoped counterpart to
// sweepValueOverrides, for the handful of cross-field rules apischema's flat
// per-parameter vocabulary genuinely cannot express — see registry_clusters.go's
// own comment on token_id/token_secret: "a cross-field rule the handler
// owns". Keyed by "METHOD path" in sweepRouteOverrides, exactly as
// Endpoint.Method+" "+Endpoint.Path spells it.
type sweepEndpointOverride struct {
	// values replaces the synthesized value for these names, on whichever
	// variant includes the name — checked before sweepValueOverrides, so a
	// route-specific need can override the global one.
	values map[string]any
	// forceRequired additionally includes these names on the required-only
	// variant, for a parameter the schema marks Optional but the handler's
	// cross-field business logic makes conditionally required whenever the
	// endpoint is called at all (not merely whenever the caller happens to
	// send a sibling — that shape IS what Requires expresses, and the real
	// registry declares none of it as of this writing).
	forceRequired []string
	// excludeFromOptional removes these names from the with-optional
	// variant: for a parameter that is mutually exclusive with another one
	// this route also declares, sending every optional parameter at once —
	// the with-optional variant's whole premise — cannot be satisfied by
	// ANY combination of values, so one side of the exclusive pair has to
	// give way. The required-only variant is untouched.
	excludeFromOptional []string
}

var sweepRouteOverrides = map[string]sweepEndpointOverride{
	// ssh-credentials: auth_type's Enum[0] is "password" (checked against
	// registry_rolling_update.go), and the handler requires a password
	// whenever auth_type resolves to it — registry_rolling_update.go's own
	// comment says this is deliberately left to the handler rather than
	// half-stated via Requires.
	"PUT " + pathPrefix + "clusters/:cluster_id/ssh-credentials": {
		forceRequired: []string{"password"},
	},
	// migrations.go requires target_node whenever migration_type resolves
	// to its Enum[0] ("intra-cluster"); cross-cluster migrations don't need
	// it, which is exactly the kind of value-conditional rule Requires
	// cannot state.
	"POST " + pathPrefix + "migrations": {
		forceRequired: []string{"target_node"},
		// migrations.go: "disk_format only applies to storage and both
		// migration modes" — forceRequired above pins migration_type at its
		// Enum[0] ("intra-cluster"), which disk_format is not valid
		// alongside. "" is a real, documented value here rather than a
		// dodge: imageFormatParam's own description says "Empty lets the
		// storage decide", ValidImageFormat("") returns true
		// (proxmox/diskmove.go), and migrations.go's own gate is
		// `diskFormat != "" && migrationMode != ...` — so "" clears BOTH
		// checks and the field stays under test on both variants, unlike an
		// exclusion, which would never send it at all.
		values: map[string]any{"disk_format": ""},
	},
	// clusters.go: "Supply either token_id and token_secret, or a bootstrap
	// block — not both" (registry_clusters.go's own comment on why this
	// stays in the handler). The with-optional variant's premise — every
	// optional parameter at once — cannot satisfy an exclusive-or no matter
	// what values are chosen, so bootstrap gives way; token_id/token_secret
	// stay under test on both variants.
	"POST " + pathPrefix + "clusters": {
		forceRequired:       []string{"token_id", "token_secret"},
		excludeFromOptional: []string{"bootstrap"},
	},
	// console-token: console_type's Enum[0] is "node_shell", and the handler
	// checks the VALUE, not presence — `if vmid != 0` (auth.go) — so 0 is a
	// real, documented value rather than a dodge: the property's own
	// Minimum(0) and its Description both say "0 is how a node_shell
	// request spells 'no guest'" (registry_auth.go). That keeps vmid's own
	// bounds under test on the with-optional variant, unlike an exclusion,
	// which would never send it at all.
	"POST " + pathPrefix + "auth/console-token": {
		values: map[string]any{"vmid": int64(0)},
	},
	// cve-notifications: "At least one channel is required when enabled"
	// (cve.go) — sweepValueOverrides sends an empty channel_ids (its own
	// comment explains why), which conflicts with enabled defaulting to
	// true on the with-optional variant; false keeps the field itself
	// under test without tripping the cross-field rule.
	"PUT " + pathPrefix + "clusters/:cluster_id/cve-notifications": {
		values: map[string]any{"enabled": false},
	},
	// reports/schedules: EmailChannelID is only even looked up when
	// EmailEnabled is true (reports.go) — false keeps email_channel_id's
	// own Format/length facets under test on the with-optional variant
	// without needing a channel row the disconnected DB was never asked to
	// create.
	"POST " + pathPrefix + "reports/schedules": {
		values: map[string]any{"email_enabled": false},
	},
	// registry_alerts.go's maintenance-window node_id: Pattern emptyOrUUID
	// accepts "" deliberately (its own comment: "the EMPTY string is the
	// sentinel both handlers read"), and "" means "suppress the whole
	// cluster" — a real node UUID would need a row this sweep's
	// disconnected DB was never asked to create, so the field's own
	// documented empty case is the value that reaches furthest. Scoped to
	// these two routes rather than sweepValueOverrides because
	// cve-scans/:scan_id/vulnerabilities declares an UNRELATED "node_id"
	// with a strict Format:"uuid" that does not accept "".
	"POST " + pathPrefix + "clusters/:cluster_id/maintenance-windows": {
		values: map[string]any{"node_id": ""},
	},
	"PUT " + pathPrefix + "clusters/:cluster_id/maintenance-windows/:id": {
		values: map[string]any{"node_id": ""},
	},
	// oidc/configs create: validateOIDCRedirectURI (oidc.go) runs
	// unconditionally, with no `if raw != ""` guard the way
	// virtiowin.ValidateSchedule has for its own optional fields — so
	// redirect_uri is optional in the schema but effectively required by
	// the handler the moment this route is called at all.
	//
	// The update route (PUT .../oidc/configs/:id) needs no override, but
	// NOT because omitting redirect_uri there is safe — Update's own
	// `req.RedirectURI != existing.RedirectUri` compares against whatever
	// oidcConfigFromParams read (p.String, with no awareness of `existing`
	// at all), so an omitted field reads back as "" and — on a real config
	// with a real stored redirect_uri — differs from it and fails the same
	// unconditional check Create hits. This route needs no override purely
	// because Update's FIRST line, h.queries.GetOIDCConfig, 500s against
	// the disconnected pool before ever reaching that comparison.
	"POST " + pathPrefix + "oidc/configs": {
		forceRequired: []string{"redirect_uri"},
	},
	// totp.go's Disable (DELETE) and VerifyLogin (POST verify-login) both
	// gate on `code == "" && recovery_code == ""` before anything else —
	// Disable at totp.go:206, VerifyLogin at totp.go:338 — which is what the
	// required-only variant hits, NOT a pending-secret dependency. Forcing
	// "code" reuses sweepValueOverrides["code"] (already valid: 6 digits)
	// and both routes then reach a REAL downstream call — Disable's
	// h.queries.GetUserTOTPSecret (a plain DB error against the
	// disconnected pool) and VerifyLogin's h.rdb.Incr (a plain Redis
	// error) — so neither needs a sweepExpectedFindings entry any more.
	"POST " + pathPrefix + "auth/totp/verify-login": {
		forceRequired: []string{"code"},
	},
	"DELETE " + pathPrefix + "auth/totp": {
		forceRequired: []string{"code"},
	},
	// audit-log/syslog-test: TestSyslog (audit.go) checks `cfg.Host == ""`
	// first, so required-only needs "host" forced the same way every other
	// "optional in the schema, unconditionally required by the handler"
	// case here does. "port" is ALSO forced, and that one is not
	// stylistic: the schema declares Default:0 and documents it as
	// "0, or omitted, means the RFC 5424 default of 514" (registry_audit.go),
	// but TestSyslog checks `cfg.Port < 1 || cfg.Port > 65535` BEFORE its
	// OWN `cfg.Port == 0 → 514` fallback a few lines later (audit.go) — so
	// the documented "0 means default" behaviour is dead code, unreachable
	// through this handler, and omitting/defaulting port 400s with "Port
	// must be between 1 and 65535" instead of ever reaching the network
	// probe. That is a real handler defect this sweep found; it is reported
	// (see the task reply), not fixed here — forcing "port" past it with
	// the generic synthesizer's value (1, comfortably non-zero) is what
	// lets BOTH variants reach TestSyslog's actual job, the network probe,
	// which is what sweepExpectedFindings' syslog-test entry below is for.
	"POST " + pathPrefix + "audit-log/syslog-test": {
		forceRequired: []string{"host", "port"},
	},
}

// sweepExpectedFindings names routes where even a maximally well-chosen
// synthesized request cannot reach a downstream call, and states why —
// checked every run so the list cannot silently rot the way an unstated
// exception would (CLAUDE.md's rule on exemption lists applies here too).
// TestGuard_EveryRegistryRouteDispatchesWithoutPanicking fails if an entry
// here is NEVER matched (the block it used to justify no longer applies —
// remove the entry) exactly as it fails on an unexpected finding (a NEW
// block appeared — investigate it the way every entry here was investigated
// before being added). Both are "the two-tier heuristic in the file comment
// is a start, not the whole story" made concrete: read the failure, decide
// which side of the line it is actually on, and say why in code a future
// reader can check against the same handler source these comments cite.
//
// Consulted ONLY when the outcome classified as sweepFindingRegistryRejected
// (a plain 400) — the main loop checks that before it ever looks a route up
// here. An entry does not, and must not, excuse a panic, a declaration bug,
// an auth-wiring gap or a dispatch error on the same route: those are never
// "the handler ran its own business rule", and an allowlist that could not
// tell them apart from one would quietly stop meaning anything the moment a
// route on it started panicking instead of 400ing.
var sweepExpectedFindings = map[string]string{
	// Both declare Parameters: apischema.Properties{} on purpose — the
	// payload is multipart, and declaring nothing is what keeps extraction
	// from touching the body at all (registry_settings.go's own comment).
	// An empty request is therefore fully schema-valid; the 400 is
	// UploadLogo/UploadFavicon's own c.FormFile("logo") finding nothing,
	// which no synthesizer speaking only apischema's declared parameters
	// can supply.
	"POST " + pathPrefix + "settings/branding/logo":    "multipart-only upload; no apischema body parameter exists to carry a file",
	"POST " + pathPrefix + "settings/branding/favicon": "multipart-only upload; no apischema body parameter exists to carry a file",
	// Both need TOTP state a PRIOR real request established in the same
	// session, which one independent synthesized request cannot fabricate.
	// (verify-login and DELETE .../auth/totp do NOT belong here — forcing
	// "code" in sweepRouteOverrides gets both of them past their own
	// cross-field presence check and on to a genuine downstream DB/Redis
	// error instead.)
	//
	// setup/verify reads the pending secret with `h.rdb.Get(...).Result()`
	// and maps ANY error — a genuine Redis outage exactly as much as "no
	// key" — to this same 400, so a live server with Redis down would
	// answer identically; this sweep's disconnected Redis is reaching the
	// real branch, not dodging it. recovery-codes/regenerate's
	// h.queries.GetUserTOTPSecret does the equivalent thing one layer down,
	// mapping a DB error the same way it maps "genuinely never enrolled".
	"POST " + pathPrefix + "auth/totp/setup/verify":              "needs a real pending TOTP secret from a prior POST .../totp/setup in the same session",
	"POST " + pathPrefix + "auth/totp/recovery-codes/regenerate": "needs TOTP already enabled for the caller, which only a prior real enrollment establishes",
	// audit.go's TestSyslog opens a REAL network probe to the caller-named
	// host/port (proxsyslog.Forwarder.Test) and, on failure, answers 400
	// with a hand-built {"success":false,"error":...} body — NOT the
	// standard ErrorResponse envelope this sweep's dispatch reads Message
	// from, which is why the with-optional line above showed an empty
	// message. No synthesized host reaches an actual listening syslog
	// collector, on any host reachable from a sandboxed test run or not.
	"POST " + pathPrefix + "audit-log/syslog-test": "opens a real network probe to a caller-supplied host; no synthesized target is a listening syslog collector",
}

// satisfiesStringConstraints checks s against every facet String.Validate
// would: length bounds always, Pattern when declared. Enum and Format are
// handled by their callers, before this is ever consulted — see
// synthesizeString.
func satisfiesStringConstraints(prop apischema.Property, s string) bool {
	n := utf8.RuneCountInString(s)
	if prop.MinLength != nil && n < *prop.MinLength {
		return false
	}
	if prop.MaxLength != nil && n > *prop.MaxLength {
		return false
	}
	if prop.Pattern == "" {
		return true
	}
	re, err := regexp.Compile(prop.Pattern)
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// fitLength builds a value of the right size out of base when no fixed
// candidate satisfies a length-bounded (but pattern-free) property — a
// generic description field with a tight MaxLength, say. Only ever consulted
// when Pattern is empty: with a pattern, a value shaped to length alone has
// no better chance of matching than the pool already tried.
func fitLength(base string, minLen, maxLen *int) string {
	if maxLen != nil && *maxLen <= 0 {
		return ""
	}
	s := base
	for minLen != nil && utf8.RuneCountInString(s) < *minLen {
		s += base
	}
	if maxLen != nil {
		for utf8.RuneCountInString(s) > *maxLen {
			r := []rune(s)
			s = string(r[:len(r)-1])
		}
	}
	return s
}

func intBoundText(p *int) any {
	if p == nil {
		return "none"
	}
	return *p
}

// synthesizeString picks a value for a String property in the same priority
// Validate checks them: Enum first (an entry is always legal — Compile's
// compileEnumEntry already proved every one survives this property's own
// pipeline unchanged), then a known Format sample, then the candidate pool
// against Pattern/length, then a length-only fallback for a pattern-free
// property the pool's fixed sizes didn't happen to fit.
func synthesizeString(prop apischema.Property) (any, bool, string) {
	if len(prop.Enum) > 0 {
		return prop.Enum[0], true, ""
	}
	if prop.Format != "" {
		sample, known := formatSamples[prop.Format]
		if !known {
			return nil, false, fmt.Sprintf("no sample value registered for format %q", prop.Format)
		}
		if !satisfiesStringConstraints(prop, sample) {
			return nil, false, fmt.Sprintf(
				"the sample for format %q does not fit this property's own length bounds [%v,%v]",
				prop.Format, intBoundText(prop.MinLength), intBoundText(prop.MaxLength))
		}
		return sample, true, ""
	}
	for _, c := range stringCandidatePool {
		if satisfiesStringConstraints(prop, c) {
			return c, true, ""
		}
	}
	if prop.Pattern == "" {
		if s := fitLength("test", prop.MinLength, prop.MaxLength); s != "" {
			return s, true, ""
		}
	}
	return nil, false, fmt.Sprintf(
		"no candidate string satisfies pattern %q within length [%v,%v]",
		prop.Pattern, intBoundText(prop.MinLength), intBoundText(prop.MaxLength))
}

// synthesizeInt picks the smallest value that satisfies Minimum (default 1,
// PVE identifiers and counts are rarely legitimately <=0), then clamps down
// to Maximum if that is stricter. checkIntBounds' comparisons are inclusive
// at both ends (validate.go), so touching a bound is always legal.
func synthesizeInt(prop apischema.Property) int64 {
	v := int64(1)
	if prop.Minimum != nil {
		if m := int64(math.Ceil(*prop.Minimum)); m > v {
			v = m
		}
	}
	if prop.Maximum != nil {
		if m := int64(math.Floor(*prop.Maximum)); m < v {
			v = m
		}
	}
	return v
}

// synthesizeNumber is synthesizeInt's float twin.
func synthesizeNumber(prop apischema.Property) float64 {
	v := 1.0
	if prop.Minimum != nil && *prop.Minimum > v {
		v = *prop.Minimum
	}
	if prop.Maximum != nil && *prop.Maximum < v {
		v = *prop.Maximum
	}
	return v
}

// synthesizeArray builds a slice of synthesizeParam(*prop.Items) repeated
// enough times to satisfy MinLength (default one element). compileItems
// (apischema/validate.go) only ever allows a scalar Items type, so no
// recursion beyond one level is possible.
func synthesizeArray(prop apischema.Property) (any, bool, string) {
	if prop.Items == nil {
		return nil, false, "array parameter declares no Items schema"
	}
	v, ok, reason := synthesizeParam(*prop.Items)
	if !ok {
		return nil, false, "array items: " + reason
	}
	n := 1
	if prop.MinLength != nil && *prop.MinLength > n {
		n = *prop.MinLength
	}
	out := make([]any, 0, n)
	for range n {
		out = append(out, v)
	}
	return out, true, ""
}

// synthesizeParam returns SOME value prop's own facets accept. ok is false
// when nothing in this file's pool of known values does — see
// maxAllowedRouteSweepSkips for what happens then. It never consults
// sweepValueOverrides/sweepRouteOverrides; synthesizeValueFor is the
// name-aware wrapper that does, and is what synthesizeSweepRequest actually
// calls per parameter.
func synthesizeParam(prop apischema.Property) (value any, ok bool, reason string) {
	switch prop.Type {
	case apischema.String:
		return synthesizeString(prop)
	case apischema.Integer:
		return synthesizeInt(prop), true, ""
	case apischema.Number:
		return synthesizeNumber(prop), true, ""
	case apischema.Boolean:
		return true, true, ""
	case apischema.Object:
		// Object is carried through unvalidated (no nested-properties field
		// — apischema/params.go's Object doc comment), so {} always
		// satisfies the SCHEMA. A handler that assumes a specific key
		// exists inside it is exactly the kind of thing this sweep is
		// built to surface as a finding, not to paper over — see
		// sweepValueOverrides["fields"] for the one route where that
		// actually happened.
		return map[string]any{}, true, ""
	case apischema.Array:
		return synthesizeArray(prop)
	default:
		return nil, false, fmt.Sprintf("unrecognised property type %q", prop.Type)
	}
}

// synthesizeValueFor is synthesizeParam plus the two override tables: a
// route-scoped value (override.values) wins first, then a name-scoped one
// (sweepValueOverrides) when the property declares no Enum, then the
// generic synthesizer. See sweepValueOverrides' own doc comment for why
// Enum always outranks both.
func synthesizeValueFor(name string, prop apischema.Property, override sweepEndpointOverride) (any, bool, string) {
	if override.values != nil {
		if v, has := override.values[name]; has {
			return v, true, ""
		}
	}
	if len(prop.Enum) == 0 {
		if v, has := sweepValueOverrides[name]; has {
			return v, true, ""
		}
	}
	return synthesizeParam(prop)
}

// closeRequiredParams returns the set of parameter names a request MUST
// carry: every non-Optional parameter, plus (transitively) every parameter
// named in an included parameter's Requires. Requires only ever fires on a
// parameter the caller actually sent (validate.go), and a non-Optional
// parameter is always sent — so a required parameter's Requires companions
// are, in practice, required too, even though they are declared Optional.
// Compile() only allows Requires to target an Optional parameter in the
// first place (compileProperty), which is what makes this closure necessary
// rather than redundant. The real registry declares no Requires at all as of
// this writing; this exists so a future one is exercised correctly on day one
// rather than silently producing a false "registry rejected it" finding.
func closeRequiredParams(props apischema.Properties) map[string]bool {
	included := make(map[string]bool, len(props))
	for name, prop := range props {
		if !prop.Optional {
			included[name] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for name := range included {
			for _, req := range props[name].Requires {
				if !included[req] {
					included[req] = true
					changed = true
				}
			}
		}
	}
	return included
}

// stringForWire renders a synthesized value the way it has to appear in a
// URL: path and query parameters are always text on the wire, decoded back
// through apischema's own coerce() (validate.go), which accepts a numeric or
// boolean string same as a JSON number or bool.
func stringForWire(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(t)
	}
}

// substitutePath rebuilds path with each :name replaced by values[name],
// using the identical scan registry.go's pathParamNames uses (via
// isParamNameByte) so a name that is a prefix of another (":id" inside a
// hypothetical ":identifier") can never be replaced wrong the way a plain
// strings.ReplaceAll(":id", …) could.
func substitutePath(path string, values map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] != ':' {
			b.WriteByte(path[i])
			continue
		}
		j := i + 1
		for j < len(path) && isParamNameByte(path[j]) {
			j++
		}
		if j == i+1 {
			b.WriteByte(path[i])
			continue
		}
		name := path[i+1 : j]
		b.WriteString(url.PathEscape(values[name]))
		i = j - 1
	}
	return b.String()
}

// sweepRequest is what synthesizeSweepRequest built, or why it could not.
type sweepRequest struct {
	ok     bool
	reason string
	target string
	body   []byte
	// excluded names the parameters sweepRouteOverrides.excludeFromOptional
	// dropped from THIS request — populated only on the with-optional
	// variant, since that is the only one the exclusion touches. The
	// caller accounts for each one as its own skip: an excluded parameter
	// is never sent on EITHER variant (required-only never included it to
	// begin with, being Optional), so "0 skipped" would otherwise overstate
	// what this sweep actually covered.
	excluded []string
}

// synthesizeSweepRequest builds one request for e. When includeOptional is
// false, only the required-parameter closure (closeRequiredParams, plus any
// sweepRouteOverrides.forceRequired) is sent — proving the endpoint's OWN
// declared defaults survive Validate(). When true, every declared parameter
// is sent except any sweepRouteOverrides.excludeFromOptional — proving every
// optional facet the endpoint declares (Enum, Pattern, Format, bounds,
// arrays, objects) is independently satisfiable. Both matter: an optional
// parameter with a self-contradictory Enum/Pattern combination would pass
// the required-only pass and only ever fail the second.
func synthesizeSweepRequest(e Endpoint, includeOptional bool) sweepRequest {
	override := sweepRouteOverrides[e.Method+" "+e.Path]

	included := closeRequiredParams(e.Parameters)
	var excluded []string
	if includeOptional {
		for name := range e.Parameters {
			included[name] = true
		}
		for _, name := range override.excludeFromOptional {
			delete(included, name)
			excluded = append(excluded, name)
		}
	} else {
		for _, name := range override.forceRequired {
			included[name] = true
		}
	}

	pathValues := make(map[string]string, len(e.pathParams))
	query := url.Values{}
	body := make(map[string]any)

	for _, name := range slices.Sorted(maps.Keys(e.Parameters)) {
		if !included[name] {
			continue
		}
		prop := e.Parameters[name]
		value, ok, reason := synthesizeValueFor(name, prop, override)
		if !ok {
			return sweepRequest{reason: fmt.Sprintf("%s: %s", name, reason)}
		}
		switch apischema.ResolveSource(name, prop, e.Method, e.pathParams) {
		case apischema.SourcePath:
			pathValues[name] = stringForWire(value)
		case apischema.SourceQuery:
			query.Set(name, stringForWire(value))
		default: // SourceBody, and SourceAuto resolved to body by ResolveSource
			body[name] = value
		}
	}

	// Every :name the ROUTE itself declares must have a value regardless of
	// pass, or Fiber never matches the route at all — a routing failure this
	// test cannot tell apart from a synthesizer bug. checkPathParams
	// (registry.go) refuses a path parameter with a non-path Source, but
	// nothing forbids Optional:true on one; this is therefore a genuine
	// safety net, not dead code, even though grep confirms no route in
	// today's registry spells a path segment ":name?".
	for _, name := range e.pathParams {
		if _, done := pathValues[name]; done {
			continue
		}
		prop, declared := e.Parameters[name]
		if !declared {
			// Register() would already have refused this at startup
			// (checkPathParams); unreachable through the real registry.
			return sweepRequest{reason: fmt.Sprintf("%s: path parameter has no schema entry", name)}
		}
		value, ok, reason := synthesizeValueFor(name, prop, override)
		if !ok {
			return sweepRequest{reason: fmt.Sprintf("%s (required by the route path): %s", name, reason)}
		}
		pathValues[name] = stringForWire(value)
	}

	target := substitutePath(e.Path, pathValues)
	if q := query.Encode(); q != "" {
		target += "?" + q
	}

	var bodyBytes []byte
	if len(body) > 0 {
		b, err := json.Marshal(body)
		if err != nil {
			return sweepRequest{reason: "marshal synthesized body: " + err.Error()}
		}
		bodyBytes = b
	}
	return sweepRequest{ok: true, target: target, body: bodyBytes, excluded: excluded}
}

// ---------------------------------------------------------------------------
// The sweep itself.
// ---------------------------------------------------------------------------

// maxAllowedRouteSweepSkips bounds how many (endpoint, variant, parameter)
// gaps this sweep may carry before it FAILS rather than quietly covering
// less of the registry. Two different things land in the same skips list
// (see the main loop), deliberately: synthesizeSweepRequest giving up on a
// request outright, and a name sweepRouteOverrides.excludeFromOptional
// dropped from the with-optional variant — an excluded parameter is never
// sent on EITHER variant, so counting it here is what keeps this sweep from
// claiming coverage it does not have.
//
// Pinned at a small non-zero number, not zero: today's registry (535
// endpoints, two variants each) carries exactly ONE permanent, named skip —
// "bootstrap" on POST /api/v1/clusters' with-optional variant, excluded
// because it is mutually exclusive with token_id/token_secret, which stay
// under test instead (see sweepRouteOverrides) — so the number to beat is
// 1, and the remaining headroom is for one genuinely novel synthesizer gap
// to be added and reported clearly rather than turning "extend the
// synthesizer" into an emergency. See the task report for today's actual
// count.
const maxAllowedRouteSweepSkips = 5

// sweepVariant is one way of filling out an endpoint's declared parameters.
type sweepVariant struct {
	name            string
	includeOptional bool
}

var sweepVariants = []sweepVariant{
	{"required-only", false},
	{"with-optional", true},
}

// sweepGated reports whether e is mounted behind Permissions.Check or
// .Alternatives — the only shapes sweepAuth's grant actually sits in front
// of. See classifySweepOutcome's case 4 in the file comment.
func sweepGated(e Endpoint) bool {
	return e.Permissions.Check != nil || len(e.Permissions.Alternatives) > 0
}

// TestGuard_EveryRegistryRouteDispatchesWithoutPanicking is the dynamic
// counterpart described in this file's top comment. It exercises every
// endpoint s.buildRegistry() declares, in both sweepVariants, and requires
// zero panics and zero registry-layer rejections of a request this test
// built specifically to satisfy the endpoint's own declared schema — with
// sweepExpectedFindings' short, cited allowlist as the one deliberate
// exception, checked both ways every run.
func TestGuard_EveryRegistryRouteDispatchesWithoutPanicking(t *testing.T) {
	s := newSweepServer(t)
	endpoints := s.registry.Endpoints()
	if len(endpoints) == 0 {
		t.Fatal("the server declared no registry endpoints, so this test would exercise nothing")
	}

	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Use(recover.New(recover.Config{PanicHandler: sweepPanicHandler}))
	mountRegistry(app, s.registry, sweepAuth(s.registry))

	type sweepSkip struct {
		route  string
		pass   string
		reason string
	}
	type sweepFinding struct {
		route   string
		pass    string
		kind    sweepClassification
		status  int
		message string
	}

	var (
		exercised     int
		skips         []sweepSkip
		findings      []sweepFinding
		expectedHits  int
		matchedExpect = make(map[string]bool, len(sweepExpectedFindings))
	)

	for _, e := range endpoints {
		route := e.Method + " " + e.Path
		gated := sweepGated(e)
		for _, v := range sweepVariants {
			req := synthesizeSweepRequest(e, v.includeOptional)
			if !req.ok {
				skips = append(skips, sweepSkip{route: route, pass: v.name, reason: req.reason})
				continue
			}
			for _, name := range req.excluded {
				skips = append(skips, sweepSkip{route: route, pass: v.name,
					reason: name + ": excluded from with-optional by sweepRouteOverrides — mutually " +
						"exclusive with a sibling parameter this route also declares"})
			}
			exercised++
			outcome := sweepDispatch(app, e.Method, req.target, req.body)
			kind := sweepFindingClassification(outcome, gated)
			if kind == sweepOK {
				continue
			}
			// The allowlist speaks for 400s only (see sweepExpectedFindings'
			// own doc comment and each entry's citation) — every other kind
			// is either unreachable through the real registry
			// (sweepFindingDeclarationBug) or something this sweep must
			// never excuse regardless of which route it lands on
			// (sweepFindingPanic, sweepFindingAuthWiring on a route that
			// reaches here at all, sweepFindingDispatchError, sweepFindingRoutingMiss).
			if kind == sweepFindingRegistryRejected {
				if reason, expected := sweepExpectedFindings[route]; expected {
					expectedHits++
					matchedExpect[route] = true
					t.Logf("EXPECTED %-14s %-55s status=%d message=%q — %s",
						v.name, route, outcome.status, outcome.message, reason)
					continue
				}
			}
			findings = append(findings, sweepFinding{
				route: route, pass: v.name, kind: kind,
				status: outcome.status, message: outcome.message,
			})
		}
	}

	t.Logf("route sweep: %d declared endpoints, %d requests exercised, %d skipped, %d findings, %d expected",
		len(endpoints), exercised, len(skips), len(findings), expectedHits)
	for _, sk := range skips {
		t.Logf("SKIP    %-14s %-55s reason=%s", sk.pass, sk.route, sk.reason)
	}
	for _, f := range findings {
		t.Errorf("FINDING %-14s %-55s kind=%-55s status=%d message=%q",
			f.pass, f.route, f.kind, f.status, f.message)
	}

	// An entry nothing ever matched means the block it was written to
	// explain no longer applies — the synthesizer got smarter, the handler
	// changed — and the entry is now silently hiding whatever the route
	// does today. Same "an un-pasted exception gets re-raised" principle
	// CLAUDE.md states for the PII allowlist, applied to this one.
	for route, reason := range sweepExpectedFindings {
		if !matchedExpect[route] {
			t.Errorf("sweepExpectedFindings[%q] was never matched this run (reason: %s) — "+
				"remove the entry, or find out why the block it documented stopped happening", route, reason)
		}
	}

	if len(skips) > maxAllowedRouteSweepSkips {
		t.Fatalf("%d (endpoint, variant) combinations could not be synthesised, over the pinned threshold of %d — "+
			"see the SKIP lines above. A growing skip list is this test silently checking less of the registry "+
			"every release, which is exactly the failure mode it exists to avoid; extend synthesizeParam's pools "+
			"instead of raising this constant.", len(skips), maxAllowedRouteSweepSkips)
	}
}

// TestSweepClassifiesDispatchOutcomes pins classifySweepOutcome's rulebook
// against concrete examples of both sides of the line it draws — the exact
// thing the task asks this whole file to justify. Every "downstream, not a
// finding" case here is a SHAPE actually seen from a real handler in this
// codebase (clusters.go's ClusterHandler.List, and the generic mapping a
// dial failure gets), not a hypothetical.
func TestSweepClassifiesDispatchOutcomes(t *testing.T) {
	cases := []struct {
		name string
		o    sweepOutcome
		want sweepClassification
	}{
		{"200 is a pass", sweepOutcome{status: fiber.StatusOK}, sweepOK},
		{"204 is a pass", sweepOutcome{status: fiber.StatusNoContent}, sweepOK},
		{"404 is a normal application response, not a registry finding",
			sweepOutcome{status: fiber.StatusNotFound, message: "cluster not found"}, sweepOK},
		{"a bare 'Not Found' means the router never matched any route at all",
			sweepOutcome{status: fiber.StatusNotFound, message: notFoundFallbackMessage}, sweepFindingRoutingMiss},
		{"a 404 that merely CONTAINS the fallback text is a real app-level 404, not a routing miss",
			sweepOutcome{status: fiber.StatusNotFound, message: "resource Not Found in this cluster"}, sweepOK},
		{"409 is a normal application response",
			sweepOutcome{status: fiber.StatusConflict, message: "already exists"}, sweepOK},
		{"a generic downstream 500 is not the declaration-bug sentinel",
			sweepOutcome{status: fiber.StatusInternalServerError, message: "Failed to list clusters"}, sweepOK},
		{"a 500 naming the actual dial failure is a pass",
			sweepOutcome{status: fiber.StatusInternalServerError, message: "dial tcp 127.0.0.1:1: connect: connection refused"}, sweepOK},
		{"502 from an unreachable Proxmox is a pass",
			sweepOutcome{status: fiber.StatusBadGateway, message: "failed to reach node"}, sweepOK},
		{"400 from validation is a registry-rejected finding",
			sweepOutcome{status: fiber.StatusBadRequest, message: "limit: expected an integer"}, sweepFindingRegistryRejected},
		{"the exact declaration-bug sentinel message",
			sweepOutcome{status: fiber.StatusInternalServerError, message: declarationBugMessage}, sweepFindingDeclarationBug},
		{"a message that merely CONTAINS the sentinel text is not an exact match, and is a pass",
			sweepOutcome{status: fiber.StatusInternalServerError, message: declarationBugMessage + " but recovered"}, sweepOK},
		{"the panic sentinel status, regardless of message",
			sweepOutcome{status: sweepPanicStatus, message: `apischema: String("x"): parameter is not declared`}, sweepFindingPanic},
		{"401 means the sweep's own auth stub was not honoured",
			sweepOutcome{status: fiber.StatusUnauthorized}, sweepFindingAuthWiring},
		{"403 means the sweep's own RBAC stub was not honoured",
			sweepOutcome{status: fiber.StatusForbidden}, sweepFindingAuthWiring},
		{"a transport-level failure is its own finding, not a fatal abort",
			sweepOutcome{dispatchErr: errors.New("boom")}, sweepFindingDispatchError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifySweepOutcome(tc.o); got != tc.want {
				t.Errorf("classifySweepOutcome(%+v) = %s, want %s", tc.o, got, tc.want)
			}
		})
	}
}

// TestSweepFindingClassificationAppliesGatingAndRBACDenial pins
// sweepFindingClassification's rulebook — the fix for the bug the task
// reply calls H1: an earlier version excused ANY 401/403 on a route not
// gated by Permissions.Check/.Alternatives, which excused precisely a
// Deferred handler's OWN internal permission refusal (rbacDeniedMessage)
// on the very routes sweepDeferredPermissionPairs exists to keep reachable,
// while the rule was never able to fire on a gated route at all, since
// allowAllRBAC.HasPermission there always returns true. Proven empirically
// too: emptying allowAllRBAC's grant list and re-running the full sweep now
// fails instead of silently passing with 0 findings — see the task reply.
func TestSweepFindingClassificationAppliesGatingAndRBACDenial(t *testing.T) {
	cases := []struct {
		name  string
		o     sweepOutcome
		gated bool
		want  sweepClassification
	}{
		{"gated route, RBAC engine's own denial: a finding (unaffected by this fix)",
			sweepOutcome{status: fiber.StatusForbidden, message: rbacDeniedMessage}, true, sweepFindingAuthWiring},
		{"gated route, some other 403 message: still a finding — gated never downgrades",
			sweepOutcome{status: fiber.StatusForbidden, message: "some other refusal"}, true, sweepFindingAuthWiring},
		{"non-gated route, RBAC engine's own denial: THE FIX — must stay a finding",
			sweepOutcome{status: fiber.StatusForbidden, message: rbacDeniedMessage}, false, sweepFindingAuthWiring},
		{"non-gated route, an unrelated 403 message: still legitimately excused",
			sweepOutcome{status: fiber.StatusForbidden, message: "Account is disabled"}, false, sweepOK},
		{"non-gated route, a 401 (never rbacDeniedMessage — that string is 403-only): still excused",
			sweepOutcome{status: fiber.StatusUnauthorized, message: "Invalid or expired refresh token"}, false, sweepOK},
		{"a panic is unaffected by gating either way",
			sweepOutcome{status: sweepPanicStatus, message: "apischema: ..."}, false, sweepFindingPanic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sweepFindingClassification(tc.o, tc.gated); got != tc.want {
				t.Errorf("sweepFindingClassification(%+v, gated=%v) = %s, want %s", tc.o, tc.gated, got, tc.want)
			}
		})
	}
}

// TestGuard_RouteSweepCatchesAnUndeclaredParamRead is the demonstration the
// task asks for: if this test suite found nothing wrong with the real
// registry, this is what it would have caught, proven by actually breaking
// a declaration the same way registry_request_test.go's
// TestRegistryLogsAHandlerPanic does — a handler reading a Params key its
// own declaration never listed — and confirming this file's OWN dispatch +
// classification plumbing reports it as sweepFindingPanic, with apischema's
// own message intact.
//
// It builds its own tiny Registry rather than mutating the real one, because
// Register() panics on a route declaring itself but reading undeclared
// params has no way to fail Register — the bug only exists in the HANDLER
// body, which Register cannot see. That is precisely the gap
// TestGuard_RegistryHandlersOnlyReadDeclaredParams closes statically and
// this whole file closes dynamically.
func TestGuard_RouteSweepCatchesAnUndeclaredParamRead(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        "/api/v1/sweep-harness-demo/:id",
		Description: "Not a real endpoint: exists only to prove the sweep catches a Params bug.",
		Group:       "Test",
		Permissions: Permissions{SelfService: "synthetic route for this file's own self-test"},
		Parameters: apischema.Properties{
			"id": apischema.StdOption("cluster-id"),
		},
		Handler: func(_ fiber.Ctx, p *apischema.Params) error {
			// The exact bug class this file exists to catch dynamically —
			// apischema.Params panics by design on a key its schema never
			// declared. Mirrors TestRegistryLogsAHandlerPanic in
			// registry_request_test.go.
			return errors.New(p.String("undeclared_key"))
		},
	})

	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Use(recover.New(recover.Config{PanicHandler: sweepPanicHandler}))
	mountRegistry(app, reg, sweepAuth(reg))

	outcome := sweepDispatch(app, fiber.MethodGet, "/api/v1/sweep-harness-demo/"+testClusterID, nil)
	if outcome.dispatchErr != nil {
		t.Fatalf("app.Test failed outright: %v", outcome.dispatchErr)
	}
	kind := classifySweepOutcome(outcome)
	if kind != sweepFindingPanic {
		t.Fatalf("an undeclared Params read must classify as a panic finding; got status=%d message=%q kind=%s",
			outcome.status, outcome.message, kind)
	}
	if !strings.Contains(outcome.message, "apischema:") {
		t.Errorf("panic message = %q, want it to carry apischema's own prefix identifying this bug class", outcome.message)
	}
	if !strings.Contains(outcome.message, "undeclared_key") {
		t.Errorf("panic message = %q, want it to name the offending key", outcome.message)
	}
	t.Logf("the sweep correctly caught the deliberately broken declaration: status=%d message=%q",
		outcome.status, outcome.message)
}
