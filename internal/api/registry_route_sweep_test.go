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
	"github.com/bigjakk/nexara/internal/api/handlers"
	nexapp "github.com/bigjakk/nexara/internal/app"
	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/config"
)

// The dynamic counterpart to TestGuard_RegistryHandlersOnlyReadDeclaredParams, which proves
// STATICALLY that no handler reads an undeclared key but not that the request reaching it is
// well-formed. For every declared endpoint, synthesize a request satisfying its OWN schema,
// drive it through the mounted registry with the permission gate open, and require the
// handler is reached without a panic or a registry rejection. The Server is real but
// disconnected (pgx and Redis on an unbound port): a handler reaching for its database gets
// "connection refused", not a nil-pointer panic, so a PANIC (answered 599 by a recover handler
// no real endpoint produces) tells a registry-layer defect from a downstream failure.
// Findings are classified by classifySweepOutcome and sweepFindingClassification.

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

// newSweepServer builds a REAL Server through the production constructors, as cmd/nexara
// does, over a disconnected Postgres pool and Redis client (see the file comment). Both are
// supplied because AuthHandler needs a SessionManager, which needs Redis: without it every
// /auth/* route would be silently absent from s.registry.
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
	// One dial, no retries: go-redis's defaults spend ~1.7 s on every command a
	// handler sends the dead address (five dials, 100 ms apart, four times), which
	// was 20 of this sweep's 23 s. The handler gets the same refusal either way.
	rdb := redis.NewClient(&redis.Options{Addr: sweepUnreachableAddr, MaxRetries: -1, DialerRetries: 1})
	t.Cleanup(func() { _ = rdb.Close() })

	// Discarded: server construction logs a benign "proxmox cache:
	// subscriber failed to start" warning against sweepUnreachableAddr, and
	// this sweep's own findings are reported through t.Errorf/t.Logf, not
	// through the server's logger.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	a := nexapp.New(context.Background(), cfg, pool, rdb, logger)
	t.Cleanup(a.Close)

	// The sweep never touches s.app: through its limiters and the real RBAC engine, which
	// the same disconnected pool would fail closed, no handler would be reached. It mounts
	// s.registry itself with sweepAuth standing in for authentication.
	return New(a)
}

// ---------------------------------------------------------------------------
// Auth/permission stub: grant everything, since this sweep proves a route DISPATCHES and
// registry_chain_test.go already proves the gate denies and allows correctly.
// ---------------------------------------------------------------------------

// sweepAuthHeader gates sweepAuth so a request outside this file's dispatch helper fails as an unauthenticated one would.
const sweepAuthHeader = "X-Sweep-Auth"

// allowAllRBAC satisfies handlers' unexported permissionEngine (as stubRBACEngine in
// registry_chain_test.go does) and grants every action on every resource. HasPermission and
// HasGlobalPermission cover the routes gated by Permissions.Check/.Alternatives.
// LoadUserPermissions, the only method accessibleClusters calls, filters a listing inside
// Deferred/Advisory handlers by EXACT (action, resource) match: an empty list reads as "no
// grant at all" and those handlers fail closed to 403 before touching h.queries, so it
// carries one global entry per pair this codebase asks about.
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

// sweepDeferredPermissionPairs are the (action, resource) pairs consulted only from INSIDE
// a handler body via accessibleClusters, which sweepDeclaredPermissionPairs cannot see.
// Found with: grep -rhoP '(?:accessibleClusters|hasClusterPerm|hasGlobalPerm|requirePerm|requireClusterPerm)\(c(?:\.Context\(\))?, *"[a-z_]+", *"[a-z_]+"' internal/api/handlers/*.go
// plus veeamScopeForAction's "execute", whose action is a variable at one call site. A
// pair missing here fails its route closed to 403, visible at once as a finding.
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
		// An interactive session, as authRequired records one: an InteractiveOnly
		// route admits only that, and the sweep is about what handlers do with a
		// request that reached them.
		c.Locals(handlers.LocalsAuthMethod, handlers.AuthMethodSession)
		return c.Next()
	}
}

// ---------------------------------------------------------------------------
// Dispatch + classification.
// ---------------------------------------------------------------------------

// sweepPanicStatus is a status no real handler produces, the unambiguous "a panic happened
// here" signal, so classification never infers a panic from a message a handler might share.
const sweepPanicStatus = 599

// sweepPanicHandler is this sweep's recover.Config.PanicHandler: the default one reports a
// non-error panic as a bare 500 "Internal Server Error", indistinguishable from a dozen
// legitimate downstream failures. It must run in the goroutine the handler panics in
// (fiber.App.Test spawns its own), which is where recover.New's defer runs.
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
// than success: see classifySweepOutcome for what lands there and why.
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

// declarationBugMessage is validationError's exact string for a schema defect Compile()
// should have caught at Register; copied as a literal because it is unexported text.
const declarationBugMessage = "Request validation failed"

// rbacDeniedMessage is the literal denial text of requirePerm/requireClusterPerm
// (handlers/permission.go) and of the accessibleClusters-based refusals of Deferred
// handlers (veeamScopeForAction): the signal that a permission check said no.
const rbacDeniedMessage = "Insufficient permissions"

// notFoundFallbackMessage is fiber.ErrNotFound's message when the router matches no route
// ("Not Found", verified against this app.Test/errorHandler pairing; no handler calls
// fiber.NewError(404) with no message, so every real application 404 says more).
const notFoundFallbackMessage = "Not Found"

// classifySweepOutcome draws the line this file lives or dies on; TestSweepClassifiesDispatchOutcomes
// pins both sides. Pure: the gating a 401/403 needs lives in sweepFindingClassification. Findings, besides
// a transport error and a routing-miss 404: (1) a panic, answered 599 by the sweep's own recover handler,
// which no real endpoint produces; (2) a 400, since each request is built from the endpoint's own schema
// (the business-rule 400s one stateless request cannot satisfy are named in sweepExpectedFindings);
// (3) a 500 reading exactly declarationBugMessage, a schema defect Compile should have caught;
// (4) a 401/403 (on a Check/Alternatives route sweepAuth's grant never denies, so there it is always a
// finding). Everything else is sweepOK: the handler ran and met an unreachable database, Redis or
// Proxmox (2xx, 404, 409, 422, 502/503, a 500 naming what failed), which this sweep does not evaluate.
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

// sweepFindingClassification is classifySweepOutcome plus whether e is gated by
// Permissions.Check/.Alternatives. An auth-wiring verdict is excused only when the route is
// NOT gated AND the message is not rbacDeniedMessage. The second condition fixes a real
// bug: emptying allowAllRBAC.LoadUserPermissions turned every Deferred route's own
// refusal into a 403 that the old "!gated is excused" rule excused unconditionally,
// precisely on the routes sweepDeferredPermissionPairs exists to keep reachable. A 401, or
// a 403 carrying another handler's message (an invalid refresh token), is still excused.
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

// sweepValueOverrides supplies a value for a parameter NAME when the schema-only value
// satisfies apischema but not the handler's own business rule on the DECODED value. Each
// entry was found by running the sweep and reading the 400 and the handler code (cited on
// each line), picking a value that reaches further into the handler. Scoped by NAME because
// every name below has one apischema Type everywhere it appears ("vmid" is Integer, or a
// String selector list in PBS jobs, which coerce() accepts a number for); a name meaning two
// incompatible things is scoped by route in sweepRouteOverrides. Consulted only when the
// property declares no Enum, whose first entry Compile has proven valid.
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
	// "schedule" means two things and one value has to satisfy both: cron for
	// schedules and report schedules (cronspec.ValidateCron), and a Proxmox
	// CALENDAR EVENT for backup and replication jobs, whose handlers forward it
	// unchecked, so a plain 5-field cron passes only because nothing local asks.
	"schedule": "0 0 * * *",
	// totp.go's own Pattern-free declaration: 6 digits, checked by hand
	// after apischema's MaxLength(6) lets a non-digit 6-character string
	// through.
	"code": "123456",
	// SetNodeTimezone checks only `timezone == ""` and forwards the rest to Proxmox,
	// which owns the vocabulary, so "UTC" is not load-bearing: any non-empty string
	// does. It is the universal default, not a timezone that locates the operator.
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
	// "node_id" is NOT overridden globally: it is also a uuid PATH parameter on several
	// routes, where "" would leave the segment empty. The maintenance-window "node_id"
	// (emptyOrUUID) is overridden in sweepRouteOverrides. virtio_win.go's UpdateConfig
	// reads check_timezone/check_schedule and runs both through virtiowin.ValidateSchedule.
	"check_timezone": "UTC",
	"check_schedule": "0 0 * * *",
	// oidc.go's own comment on validateOIDCRedirectURI: it must end in
	// THIS install's own callback path (/api/v1/auth/oidc/callback), not any
	// caller-chosen one — a fixed suffix, not something a Format/Pattern can
	// state since the host in front of it varies per deployment.
	"redirect_uri": "https://example.com/api/v1/auth/oidc/callback",
	// Not a handler rule like the rest: registry_mappings.go's USB device id
	// pattern, ^[0-9A-Fa-f]{4}:[0-9A-Fa-f]{4}$, which nothing in
	// stringCandidatePool satisfies, so POST .../usb-mappings was skipped on
	// both variants. A made-up vendor:product id, like the route's own
	// examples.
	"device_id": "1234:5678",
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
	// send a sibling — that shape IS what Requires expresses, which the PCI
	// mapping update declares among add_node, add_path and replace).
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
	// A handler rule: the node must be the one the UPID names (the collector
	// polls the task there), and sweepValueOverrides' upid names pve-01.
	"POST " + taskHistoryScope: {values: map[string]any{"node": "pve-01"}},
	// Not a handler rule either, like "device_id" in sweepValueOverrides:
	// the PCI address pattern (registry_mappings.go), which nothing in
	// stringCandidatePool satisfies. Scoped to the route because "path" is
	// also the USB create's port, with a pattern of its own. A made-up
	// address in Proxmox's form.
	"POST " + pathPrefix + "clusters/:cluster_id/pci-mappings": {
		values: map[string]any{"path": "0000:01:00.0"},
	},
	// The same address pattern on the PCI update, as add_path: the device
	// to add, or the one replacing an entry.
	"PUT " + pathPrefix + "clusters/:cluster_id/pci-mappings/:mapping_id": {
		values: map[string]any{"add_path": "0000:01:00.0"},
	},
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
	// these two routes rather than sweepValueOverrides because "node_id" is
	// also an UNRELATED uuid-format path parameter elsewhere (see the note in
	// sweepValueOverrides), which "" would leave empty.
	"POST " + pathPrefix + "clusters/:cluster_id/maintenance-windows": {
		values: map[string]any{"node_id": ""},
	},
	"PUT " + pathPrefix + "clusters/:cluster_id/maintenance-windows/:id": {
		values: map[string]any{"node_id": ""},
	},
	// oidc/configs create: validateOIDCRedirectURI runs unconditionally, so redirect_uri is
	// optional in the schema but required by the handler. The update route needs no
	// override only because its first line, GetOIDCConfig, 500s against the disconnected
	// pool before the comparison that would fail the same way.
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
	// audit-log/syslog-test: TestSyslog checks `cfg.Host == ""` first, and the route opens
	// a REAL connection (proxsyslog.Forwarder.Test). With the synthesized "test01" the
	// outcome was decided by DNS, and a resolver that answered would have sent a datagram
	// to whatever it named. A loopback literal needs no resolver; a UDP dial and write
	// succeed with no listener; port 1 rather than the default 514 spares a syslog daemon on
	// the box; udp is pinned here although it is pinned globally, so a change to that cannot
	// make this a refused TCP connect. A failure reads "registry rejected the synthesized
	// request" with message="": TestSyslog answers a failed probe with {"success":false}.
	"POST " + pathPrefix + "audit-log/syslog-test": {
		values:        map[string]any{"host": "127.0.0.1", "port": int64(1), "protocol": "udp"},
		forceRequired: []string{"host", "port"},
	},
}

// sweepExpectedFindings names routes where even a well-chosen synthesized request cannot
// reach a downstream call, with the reason, checked every run: the sweep fails on an entry
// NEVER matched (the block it explained is gone; remove it) as on an unexpected finding (a
// new block; investigate it as each entry here was). Consulted only for a plain 400, so an
// entry never excuses a panic, declaration bug, auth-wiring gap or dispatch error.
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
	// Both need TOTP state a PRIOR request established, which one independent request cannot
	// fabricate (verify-login and DELETE auth/totp are past this: forcing "code" in
	// sweepRouteOverrides gets them to a genuine DB or Redis error). setup/verify maps ANY
	// Redis error, an outage as much as "no key", to this 400, and
	// recovery-codes/regenerate maps a DB error the same way: the disconnected stores reach
	// the real branch.
	"POST " + pathPrefix + "auth/totp/setup/verify":              "needs a real pending TOTP secret from a prior POST .../totp/setup in the same session",
	"POST " + pathPrefix + "auth/totp/recovery-codes/regenerate": "needs TOTP already enabled for the caller, which only a prior real enrollment establishes",
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

// closeRequiredParams returns the parameter names a request MUST carry: every non-Optional
// one plus, transitively, the Requires of an included one (Requires fires only on a parameter
// sent, and Compile allows it to target only an Optional one). Today only the PCI mapping
// update declares Requires, among optionals the with-optional variant sends together; this
// exists so a required parameter that drags a companion in is exercised from day one rather
// than reported as a false "registry rejected it".
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
	return synthesizeSweepRequestWith(e, includeOptional, sweepRouteOverrides[e.Method+" "+e.Path])
}

// synthesizeSweepRequestWith is synthesizeSweepRequest with the route's
// override handed in rather than read from sweepRouteOverrides, for a test
// that needs particular values in particular parameters —
// TestGuard_EveryRouteNamingANodeRefusesOneTheClusterDoesNotHold puts its own
// cluster and node into every route that names a node, merged over the
// route's own override.
func synthesizeSweepRequestWith(e Endpoint, includeOptional bool, override sweepEndpointOverride) sweepRequest {
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
	// (registry.go) refuses a path parameter with a non-path Source, and
	// refuses "?" in a path, so Fiber requires every segment; but nothing
	// forbids Optional:true on a path parameter's SCHEMA, which would tell
	// the synthesizer it may leave the value out. This is therefore a
	// genuine safety net, not dead code, though no route in today's
	// registry declares one that way.
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

// maxAllowedRouteSweepSkips bounds the (endpoint, variant, parameter) gaps the sweep may carry
// before it FAILS rather than quietly cover less. The skips list holds both a request
// synthesizeSweepRequest gave up on and a name excludeFromOptional dropped (never sent on
// either variant, so counting it keeps the sweep from claiming coverage it lacks). Today's
// registry has one permanent skip, "bootstrap" on POST /api/v1/clusters, which is exclusive
// with token_id/token_secret; the headroom is for one novel gap to be reported clearly.
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
// of. See case 4 of classifySweepOutcome.
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
	// Every node is a member here, and that is load-bearing rather than
	// convenient. The server's own lookup is its disconnected database, so
	// serve would answer every route that names a node with the lookup's 500
	// before its handler ran — an outcome classifySweepOutcome rightly counts
	// as fine, which would silently take those handlers out of this sweep.
	// Membership is TestGuard_EveryRouteNamingANodeRefusesOneTheClusterDoesNotHold's.
	mountRegistry(app, s.registry, sweepAuth(s.registry), everyNodeIsAMember())

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

// TestSweepClassifiesDispatchOutcomes pins the rulebook of sweepFindingClassification (which
// is classifySweepOutcome for a gated route) on both sides of the line. Every "downstream, not
// a finding" row is a shape a real handler produces. The gating rows are the fix for an
// earlier version that excused ANY 401/403 on a route not gated by Permissions.Check or
// .Alternatives, which excused a Deferred handler's own refusal (rbacDeniedMessage) on the
// very routes sweepDeferredPermissionPairs exists to keep reachable; emptying
// allowAllRBAC's grant list now fails the sweep instead of passing with 0 findings.
func TestSweepClassifiesDispatchOutcomes(t *testing.T) {
	cases := []struct {
		name  string
		o     sweepOutcome
		gated bool
		want  sweepClassification
	}{
		{"200 is a pass", sweepOutcome{status: fiber.StatusOK}, true, sweepOK},
		{"an application 404 is a pass",
			sweepOutcome{status: fiber.StatusNotFound, message: "cluster not found"}, true, sweepOK},
		{"a bare 'Not Found' means the router matched no route",
			sweepOutcome{status: fiber.StatusNotFound, message: notFoundFallbackMessage}, true, sweepFindingRoutingMiss},
		{"a 404 that merely CONTAINS the fallback text is an application 404",
			sweepOutcome{status: fiber.StatusNotFound, message: "resource Not Found in this cluster"}, true, sweepOK},
		{"a downstream 500, and one naming the dial failure, are passes",
			sweepOutcome{status: fiber.StatusInternalServerError, message: "dial tcp 127.0.0.1:1: connect: connection refused"}, true, sweepOK},
		{"502 from an unreachable Proxmox is a pass",
			sweepOutcome{status: fiber.StatusBadGateway, message: "failed to reach node"}, true, sweepOK},
		{"400 from validation is a registry-rejected finding",
			sweepOutcome{status: fiber.StatusBadRequest, message: "limit: expected an integer"}, true, sweepFindingRegistryRejected},
		{"the exact declaration-bug sentinel",
			sweepOutcome{status: fiber.StatusInternalServerError, message: declarationBugMessage}, true, sweepFindingDeclarationBug},
		{"a message that merely CONTAINS the sentinel is a pass",
			sweepOutcome{status: fiber.StatusInternalServerError, message: declarationBugMessage + " but recovered"}, true, sweepOK},
		{"the panic sentinel status, whatever the message and gating",
			sweepOutcome{status: sweepPanicStatus, message: `apischema: String("x"): parameter is not declared`}, false, sweepFindingPanic},
		{"a transport failure is its own finding, not a fatal abort",
			sweepOutcome{dispatchErr: errors.New("boom")}, true, sweepFindingDispatchError},
		{"gated route, a 401: a finding",
			sweepOutcome{status: fiber.StatusUnauthorized}, true, sweepFindingAuthWiring},
		{"gated route, any 403: a finding",
			sweepOutcome{status: fiber.StatusForbidden, message: "some other refusal"}, true, sweepFindingAuthWiring},
		{"gated route, the RBAC engine's own denial: a finding",
			sweepOutcome{status: fiber.StatusForbidden, message: rbacDeniedMessage}, true, sweepFindingAuthWiring},
		{"non-gated route, the RBAC engine's own denial: a finding (the fix)",
			sweepOutcome{status: fiber.StatusForbidden, message: rbacDeniedMessage}, false, sweepFindingAuthWiring},
		{"non-gated route, an unrelated 403: excused",
			sweepOutcome{status: fiber.StatusForbidden, message: "Account is disabled"}, false, sweepOK},
		{"non-gated route, a 401 (never the denial text): excused",
			sweepOutcome{status: fiber.StatusUnauthorized, message: "Invalid or expired refresh token"}, false, sweepOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sweepFindingClassification(tc.o, tc.gated); got != tc.want {
				t.Errorf("sweepFindingClassification(%+v, gated=%v) = %s, want %s", tc.o, tc.gated, got, tc.want)
			}
		})
	}
}

// TestGuard_RouteSweepCatchesAnUndeclaredParamRead is the anti-vacuity demonstration: a handler
// reading a Params key its declaration never listed (a bug only the HANDLER body has, so
// Register cannot refuse it) is reported by this file's own dispatch and classification as
// sweepFindingPanic, with apischema's message intact. It builds its own Registry rather than
// mutating the real one.
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
	mountRegistry(app, reg, sweepAuth(reg), nil)

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
