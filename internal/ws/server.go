package ws

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
)

// Server is the WebSocket server backed by Fiber.
type Server struct {
	app            *fiber.App
	hub            *Hub
	jwt            *auth.JWTService
	logger         *slog.Logger
	consoleHandler *ConsoleHandler
	vncHandler     *VNCHandler
	// rbacEngine resolves view:cluster permissions for the subscribe-time
	// gate in client.go::canSubscribe (security review H1). Plumbed
	// through ServerConfig from main.go. Production runs MUST set this;
	// when nil (and no testPermissionChecker is set), cluster channel
	// subscribes fail closed.
	rbacEngine *auth.RBACEngine
	// testPermissionChecker is an optional override used by integration
	// tests so they can run the subscribe gate end-to-end without
	// spinning up a real RBAC engine. When set, it takes precedence
	// over rbacEngine.HasPermission. Never set in production builds.
	testPermissionChecker PermissionChecker
	// allowedOrigins is the WebSocket upgrade Origin allow-list. nil or
	// empty preserves the gofiber/contrib/v3/websocket default of allowing
	// all origins (the historical behaviour, suitable for dev/lab
	// homelabs); a non-empty list enforces an exact-match check at
	// upgrade time, rejecting cross-origin upgrades with HTTP 403.
	allowedOrigins []string

	pingInterval time.Duration
	pongTimeout  time.Duration
}

// ServerConfig holds optional dependencies for the WebSocket server.
type ServerConfig struct {
	ConsoleHandler *ConsoleHandler
	VNCHandler     *VNCHandler
	// RBACEngine is required in production for the subscribe-time
	// permission check on cluster channels. If nil (and no
	// TestPermissionChecker is set), the WS server logs a warning at
	// startup and cluster subscribes fail closed.
	RBACEngine *auth.RBACEngine
	// TestPermissionChecker, when non-nil, overrides the RBAC engine for
	// the subscribe gate. Reserved for integration tests that exercise
	// the gate without a full Postgres+Redis-backed engine.
	TestPermissionChecker PermissionChecker
	// AllowedOrigins, when non-empty, enables strict Origin checking on
	// the /ws, /ws/console, and /ws/vnc upgrade endpoints. Each entry is
	// matched against the request's Origin header byte-for-byte (so the
	// scheme + host + port must match exactly, e.g.
	// `https://nexara.example.com`). A nil/empty value preserves the
	// permissive default — appropriate for dev/lab installs but logged
	// as a warning at startup so production deploys catch the gap.
	AllowedOrigins []string
}

// NewServer creates a new WebSocket server.
func NewServer(hub *Hub, jwtSvc *auth.JWTService, logger *slog.Logger, pingInterval, pongTimeout time.Duration, opts ...ServerConfig) *Server {
	s := &Server{
		hub:          hub,
		jwt:          jwtSvc,
		logger:       logger,
		pingInterval: pingInterval,
		pongTimeout:  pongTimeout,
	}

	if len(opts) > 0 {
		s.consoleHandler = opts[0].ConsoleHandler
		s.vncHandler = opts[0].VNCHandler
		s.rbacEngine = opts[0].RBACEngine
		s.testPermissionChecker = opts[0].TestPermissionChecker
		s.allowedOrigins = opts[0].AllowedOrigins
	}

	if s.rbacEngine == nil && s.testPermissionChecker == nil {
		// Production deploys MUST configure RBACEngine — without it
		// cluster channel subscribes fail closed (post-5.1) and every
		// subscribe attempt logs a warning. Log loudly at startup so
		// misconfiguration is caught early.
		s.logger.Warn("ws server: no RBAC engine configured — cluster channel subscriptions will be denied")
	}

	if len(s.allowedOrigins) == 0 {
		// Permissive default — fine for self-hosted dev / lab installs,
		// but log loudly so production operators see the gap and set
		// WS_ALLOWED_ORIGINS to the SPA's public origin.
		s.logger.Warn("ws server: WS_ALLOWED_ORIGINS not set — accepting WebSocket upgrades from any origin (set this in production for CSRF defence-in-depth)")
	}

	// Fiber v3 moved DisableStartupMessage from fiber.Config to ListenConfig
	// (set in Listen / RegisterRoutes' host app), so New takes no config here.
	app := fiber.New()

	app.Get("/healthz", s.healthz)

	s.mountRoutes(app)

	s.app = app
	return s
}

// Listen starts the HTTP server on the given port.
func (s *Server) Listen(port int) error {
	addr := fmt.Sprintf(":%d", port)
	s.logger.Info("WebSocket server listening", "addr", addr)
	return s.app.Listen(addr, fiber.ListenConfig{DisableStartupMessage: true})
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	return s.app.Shutdown()
}

// RegisterRoutes mounts WebSocket routes onto an external Fiber app.
// Used by the unified binary to serve WS on the same port as the API.
func (s *Server) RegisterRoutes(app *fiber.App) {
	s.mountRoutes(app)
}

// mountRoutes builds the three WebSocket handlers and mounts them behind their
// gates. NewServer and RegisterRoutes both call it, so the standalone server
// and the unified binary cannot mount them differently.
func (s *Server) mountRoutes(app *fiber.App) {
	cfg := wsConfigWithSubprotocol(s.allowedOrigins)
	var console, vnc fiber.Handler
	if s.consoleHandler != nil {
		console = websocket.New(s.consoleHandler.HandleConsole, cfg)
	}
	if s.vncHandler != nil {
		vnc = websocket.New(s.vncHandler.HandleVNC, cfg)
	}
	s.mountGated(app, console, vnc, websocket.New(s.handleWS, cfg))
}

// mountGated attaches each route with its gate IN the route's own handler
// chain, never with Use on a path prefix — and that is a security property,
// not a style.
//
// Under Use("/ws", …) the gate ran for every path beneath /ws and chose which
// token to demand by re-reading c.Path(), while Fiber's router, with
// StrictRouting off, sends "/ws/console/" to the /ws/console route. The
// trailing slash took the hub branch — which accepts the hub token any
// signed-in user can mint from /api/v1/auth/ws-token — and the request then
// reached HandleConsole: a node shell on any node of any cluster, for any
// account, Viewers included. Attached to the route, the gate that runs is the
// one the same match chose, so no spelling of the path can put the gate and
// the handler out of step.
//
// It takes the terminal handlers as arguments so a test can mount sentinels
// through this exact function rather than through a copy of its route table —
// a copy is what hid the bypass. A nil console or VNC handler leaves that
// route unmounted.
func (s *Server) mountGated(app *fiber.App, console, vnc, hub fiber.Handler) {
	if console != nil {
		app.Get("/ws/console", s.consoleAuthMiddleware, console)
	}
	if vnc != nil {
		app.Get("/ws/vnc", s.consoleAuthMiddleware, vnc)
	}
	app.Get("/ws", s.hubAuthMiddleware, hub)
}

// subprotocolNegotiationName is the static `Sec-WebSocket-Protocol` value the
// server echoes back to acknowledge protocol negotiation. Clients send it
// alongside their token-bearing protocol entry: `Sec-WebSocket-Protocol:
// nexara.token, nexara.token.<jwt>`. The fasthttp websocket upgrader matches
// only against this static string (the per-connection token entry is parsed
// out of the request header in authMiddleware), so the server's response
// header never leaks the JWT.
const subprotocolNegotiationName = "nexara.token"

// subprotocolTokenPrefix is the prefix on the token-bearing protocol entry.
// Format: `nexara.token.<jwt>` — the JWT is base64url-encoded with `.`
// separators between header/payload/signature, all valid HTTP token chars.
const subprotocolTokenPrefix = "nexara.token."

// wsConfigWithSubprotocol returns a websocket.Config that lists the static
// `nexara.token` subprotocol so the upgrader echoes it back when the client
// requests it. Without this, browsers would close the connection with code
// 1006 because the server didn't acknowledge the requested subprotocol.
//
// allowedOrigins, when non-empty, populates Config.Origins so the gofiber
// CheckOrigin runs an exact-match check against the request's `Origin`
// header (CSRF defence-in-depth on the upgrade path). nil/empty preserves
// the package's permissive default of allowing all origins — see the
// startup warning emitted from NewServer when this is the case.
func wsConfigWithSubprotocol(allowedOrigins []string) websocket.Config {
	cfg := websocket.Config{
		Subprotocols: []string{subprotocolNegotiationName},
	}
	if len(allowedOrigins) > 0 {
		// Copy so callers can't mutate the slice we hand to the upgrader.
		cfg.Origins = append([]string(nil), allowedOrigins...)
	}
	return cfg
}

// ParseAllowedOrigins splits a comma-separated origin string (typically
// the WS_ALLOWED_ORIGINS env var) into a list of origin entries. Whitespace
// is trimmed around each entry, empty entries are dropped, and a literal
// `*` short-circuits to nil — the gofiber/contrib/v3/websocket convention for
// "allow all origins" — so operators can keep their dev configs explicit
// without needing a separate "no allow-list" toggle.
//
// Returns nil for an empty input or any input containing a `*` entry.
func ParseAllowedOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	var origins []string
	for _, e := range strings.Split(raw, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if e == "*" {
			return nil
		}
		origins = append(origins, e)
	}
	return origins
}

// healthz returns a 200 OK for health checks.
func (s *Server) healthz(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// consoleScopeLocal is the Locals key under which the console gate hands the
// scope it VALIDATED to HandleConsole and HandleVNC. They read the cluster,
// node, guest and console type from it rather than from the query string.
const consoleScopeLocal = "consoleScope"

// consoleScopeQueryKeys are the query parameters a console scope is checked
// against. Each may appear once; see authenticate.
var consoleScopeQueryKeys = []string{"cluster_id", "node", "vmid", "type"}

// consoleAuthMiddleware gates /ws/console and /ws/vnc: a console-scoped token
// whose scope matches the upgrade.
func (s *Server) consoleAuthMiddleware(c fiber.Ctx) error { return s.authenticate(c, true) }

// hubAuthMiddleware gates the generic /ws hub: a hub-scoped token.
func (s *Server) hubAuthMiddleware(c fiber.Ctx) error { return s.authenticate(c, false) }

// authenticate validates a short-lived scoped JWT before WebSocket upgrade.
//
// Two locations are accepted (in order of preference):
//
//  1. `Sec-WebSocket-Protocol: nexara.token, nexara.token.<jwt>` — the JWT
//     rides in the second protocol entry. The first (static) entry is what
//     the upgrader echoes back. Keeps the token out of the URL — and
//     therefore out of access logs, browser history, and Referer headers.
//  2. `?token=<jwt>` — legacy fallback for clients that can't set
//     subprotocols at upgrade time.
//
// Three token kinds are recognised by their scope claims:
//
//   - Console-scoped (ConsoleScope != nil) — required on /ws/console and
//     /ws/vnc. Scope must match the upgrade's query parameters exactly.
//   - WS-hub-scoped (WSScope == "hub") — required on the generic /ws hub.
//   - Regular access token — REJECTED everywhere. The point is to keep
//     long-lived bearer tokens out of WS upgrades entirely, so a leaked
//     URL or proxy access log entry can't be replayed against the API.
//
// The mint endpoints (/api/v1/auth/console-token + /api/v1/auth/ws-token)
// run the underlying RBAC check before issuing the scoped JWT, so this
// middleware is the single chokepoint that enforces "WS upgrades are
// authenticated only by short-lived single-purpose tokens". HandleConsole and
// HandleVNC make no permission check of their own; everything rests on this.
//
// consoleRoute says which of the two shapes the ROUTE demands. It is decided
// by where the gate is mounted (mountGated), never by re-reading the path.
func (s *Server) authenticate(c fiber.Ctx, consoleRoute bool) error {
	if !websocket.IsWebSocketUpgrade(c) {
		return fiber.ErrUpgradeRequired
	}

	token := tokenFromSubprotocolHeader(c)
	if token == "" {
		// Legacy URL-token fallback. The frontend always sends via
		// subprotocol after remediation 2.7, so any hit here is from a
		// stale browser, mobile, or third-party integration. Log loudly
		// so we can spot it in ops and decommission the fallback.
		token = c.Query("token")
		if token != "" {
			s.logger.Warn("ws auth: token via URL fallback (decommission target)",
				"path", c.Path(),
				"ip", c.IP(),
			)
		}
	}
	if token == "" {
		s.logger.Warn("ws auth: missing token", "path", c.Path())
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing token"})
	}

	claims, err := s.jwt.ValidateAccessToken(token)
	if err != nil {
		// Log only the validation reason and the request path. The
		// previous M4 debug block also logged token_head/token_tail
		// (first/last 8 bytes of the JWT) — removed per security
		// review Z1 because the signature tail leaks bits of the
		// HMAC and is being written to stdout unconditionally.
		s.logger.Warn("ws auth: token validation failed",
			"path", c.Path(),
			"error", err.Error(),
		)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid token"})
	}

	// Which token shape to demand comes from the route this gate is mounted
	// on, not from c.Path(). Deriving it from the path is what let
	// "/ws/console/" — routed to the console handler, but not EqualFold to
	// "/ws/console" — fall into the hub branch; an EqualFold had already been
	// added for the case variants and could not see the slash. See mountGated.
	path := c.Path()

	switch {
	case consoleRoute:
		if claims.ConsoleScope == nil {
			s.logger.Warn("ws auth: scoped token required on console path",
				"path", path,
				"user_id", claims.UserID,
			)
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "scoped console token required",
			})
		}
		if claims.WSScope != "" {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "ws-scoped token cannot be used on this path",
			})
		}
		// One value per key. c.Query below returns the FIRST copy of a
		// repeated key, while the websocket package's own capture of the
		// query keeps the LAST — so when HandleConsole read its parameters
		// from the query, "?cluster_id=A&…&cluster_id=B" passed a check for
		// A and opened a console on B. The handlers now take every value
		// from the scope stored below, so a duplicate can no longer steer
		// them; it is still refused, as a request shape no client of this
		// server has a reason to send.
		for _, key := range consoleScopeQueryKeys {
			if len(c.RequestCtx().QueryArgs().PeekMulti(key)) > 1 {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
					"error": "duplicate query parameter " + key,
				})
			}
		}
		if err := validateConsoleScope(c, claims.ConsoleScope); err != nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
		}
		c.Locals(consoleScopeLocal, claims.ConsoleScope)
	default:
		// Generic /ws hub.
		if claims.ConsoleScope != nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "console-scoped token cannot be used on this path",
			})
		}
		if claims.WSScope != auth.WSScopeHub {
			s.logger.Warn("ws auth: hub-scoped token required on /ws",
				"path", path,
				"user_id", claims.UserID,
			)
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "ws-scoped token required",
			})
		}
	}

	// Store claims in locals for the WebSocket handler.
	c.Locals("userID", claims.UserID)
	c.Locals("email", claims.Email)
	c.Locals("role", claims.Role)

	return c.Next()
}

// tokenFromSubprotocolHeader extracts the JWT from the request's
// `Sec-WebSocket-Protocol` header — handling the multi-line case where a
// client splits the comma-separated list across multiple `Sec-WebSocket-
// Protocol:` lines (RFC 7230 §3.2.2 permits this, fasthttp stores them as
// distinct kv entries, and `c.Get` would only see the first).
//
// Joins all values with `,` and delegates to tokenFromSubprotocol.
// Defence-in-depth — well-behaved browsers send a single line, but
// custom clients can split.
func tokenFromSubprotocolHeader(c fiber.Ctx) string {
	// Fiber v3: c.Context() now returns the Go context.Context; the underlying
	// fasthttp.RequestCtx (for raw header access) is c.RequestCtx().
	values := c.RequestCtx().Request.Header.PeekAll("Sec-WebSocket-Protocol")
	if len(values) == 0 {
		return ""
	}
	if len(values) == 1 {
		return tokenFromSubprotocol(string(values[0]))
	}
	var combined []byte
	for i, v := range values {
		if i > 0 {
			combined = append(combined, ',')
		}
		combined = append(combined, v...)
	}
	return tokenFromSubprotocol(string(combined))
}

// tokenFromSubprotocol parses a comma-separated `Sec-WebSocket-Protocol`
// value and returns the JWT in the first entry that exact-prefix-matches
// `nexara.token.`. Returns "" if no such entry exists OR if the entry's
// JWT segment contains any non-token character (whitespace, control chars,
// or comma).
//
// JWT chars per RFC 7519 §2 are unpadded base64url (`A-Za-z0-9-_`) plus `.`
// separators — all valid HTTP `tchar` values per RFC 7230 §3.2.6. So a
// well-formed JWT entry has zero whitespace; reject anything else as a
// hardening measure (M2 in the 2.7 security review).
func tokenFromSubprotocol(header string) string {
	if header == "" {
		return ""
	}
	for _, raw := range strings.Split(header, ",") {
		entry := strings.TrimSpace(raw)
		token, ok := strings.CutPrefix(entry, subprotocolTokenPrefix)
		if !ok {
			continue
		}
		if token == "" || !isValidJWTSegment(token) {
			continue
		}
		return token
	}
	return ""
}

// isValidJWTSegment returns true iff every byte in s is a valid HTTP
// `tchar` AND a valid JWT character (unpadded base64url + `.` separator).
// The actual signature/structure check happens in JWT parsing — this is
// purely a "did the header survive transport intact" gate.
func isValidJWTSegment(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch {
		case b >= 'A' && b <= 'Z':
		case b >= 'a' && b <= 'z':
		case b >= '0' && b <= '9':
		case b == '-' || b == '_' || b == '.':
		default:
			return false
		}
	}
	return true
}

// validateConsoleScope verifies that a scoped console token is being used on
// the correct path (/ws/console or /ws/vnc) and that all query parameters
// match the scope embedded in the token. Any mismatch is a hard reject.
//
// The path it checks the scope TYPE against is the route's registered one,
// not c.Path(): a vm_vnc token belongs on /ws/vnc however the client spelled
// the path Fiber matched to it.
func validateConsoleScope(c fiber.Ctx, scope *auth.ConsoleScope) error {
	return validateConsoleScopeFields(
		c.Route().Path,
		c.Query("cluster_id"),
		c.Query("node"),
		c.Query("vmid"),
		c.Query("type"),
		scope,
	)
}

// expectedQueryTypeForScope translates a console scope's `Type` field into
// the value the matching WebSocket endpoint expects in its `?type=` query
// parameter. The two protocols differ:
//
//   - /ws/console (terminals): the query type matches the scope type
//     directly — `node_shell`, `vm_serial`, or `ct_attach`.
//   - /ws/vnc (graphical):     the query type is EMPTY for QEMU VMs and
//     the literal string `lxc` for containers — see VNCViewer.tsx where
//     `tab.type === "ct_vnc" ? "lxc" : undefined` builds the URL.
//
// Returning an empty string means "expect the query param to be absent".
func expectedQueryTypeForScope(scopeType string) string {
	switch scopeType {
	case "vm_vnc":
		return ""
	case "ct_vnc":
		return "lxc"
	default:
		return scopeType
	}
}

// sameUUID reports whether a and b are the same UUID. Both must be in the
// canonical 36-character form, in either case — a UUID's hex digits are
// case-insensitive, and that form is all the console-token route's uuid format
// accepts. The other spellings uuid.Parse tolerates ("{…}", "urn:uuid:…", bare
// hex, and a 38-character form whose braces it never checks) match nothing,
// and neither does anything that does not parse, including another value that
// does not parse.
func sameUUID(a, b string) bool {
	if len(a) != 36 || len(b) != 36 {
		return false
	}
	ua, errA := uuid.Parse(a)
	ub, errB := uuid.Parse(b)
	return errA == nil && errB == nil && ua == ub
}

// validateConsoleScopeFields is the pure validation core used by
// validateConsoleScope. Exposed for testing.
func validateConsoleScopeFields(path, clusterID, node, vmidStr, typeStr string, scope *auth.ConsoleScope) error {
	// VNC types must upgrade via /ws/vnc; terminal types via /ws/console.
	switch scope.Type {
	case "vm_vnc", "ct_vnc":
		if path != "/ws/vnc" {
			return fmt.Errorf("console token scope type %q requires /ws/vnc", scope.Type)
		}
	case "node_shell", "vm_serial", "ct_attach":
		if path != "/ws/console" {
			return fmt.Errorf("console token scope type %q requires /ws/console", scope.Type)
		}
	default:
		return fmt.Errorf("invalid console scope type %q", scope.Type)
	}

	// Compared as UUIDs, not as text. The token carries the id the
	// console-token route's uuid format normalised to lowercase, while
	// ?cluster_id is whatever the client typed — so a client that sent the
	// same uppercase id to both was refused here as a mismatch. The handlers
	// act on the scope's own copy (consoleScopeLocal), never on this one, so
	// the query only has to name the same cluster.
	if !sameUUID(clusterID, scope.ClusterID) {
		return fmt.Errorf("cluster_id mismatch")
	}
	if node != scope.Node {
		return fmt.Errorf("node mismatch")
	}
	if typeStr != expectedQueryTypeForScope(scope.Type) {
		return fmt.Errorf("type mismatch")
	}

	// vmid is 0 for node_shell; otherwise it must match.
	if scope.VMID != 0 {
		reqVMID, err := strconv.Atoi(vmidStr)
		if err != nil || reqVMID != scope.VMID {
			return fmt.Errorf("vmid mismatch")
		}
	} else if vmidStr != "" {
		return fmt.Errorf("vmid not allowed for this scope")
	}

	return nil
}

// handleWS handles a WebSocket connection after upgrade.
func (s *Server) handleWS(conn *websocket.Conn) {
	userID, _ := conn.Locals("userID").(uuid.UUID)
	clientID := fmt.Sprintf("%s-%s", userID, uuid.New().String()[:8])

	// Pass userID + a permission-check closure into the client so
	// canSubscribe() can enforce per-cluster view permissions on each
	// subscribe message (security review H1). Test override beats the
	// real engine when present; nil leaves canSubscribe to deny cluster
	// channels (post-5.1, no synthetic-admin fall-open).
	var checker PermissionChecker
	switch {
	case s.testPermissionChecker != nil:
		checker = s.testPermissionChecker
	case s.rbacEngine != nil:
		checker = s.rbacEngine.HasPermission
	}
	client := NewClient(
		clientID, conn, s.hub, s.logger, s.pingInterval, s.pongTimeout,
		userID, checker,
	)
	s.hub.Register(client)

	// Send welcome message.
	client.trySend(newWelcomeMsg())

	// Start write pump in a separate goroutine.
	go client.writePump()

	// readPump blocks until the connection is closed.
	client.readPump()

	// Wait for writePump to finish before returning, so Fiber's
	// releaseConn doesn't reset the conn while writePump still uses it.
	<-client.done
}
