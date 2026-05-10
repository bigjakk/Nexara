package ws

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
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
	// empty preserves the gofiber/contrib/websocket default of allowing
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

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})

	app.Get("/healthz", s.healthz)

	// Register console and VNC routes before generic /ws so they match first.
	if s.consoleHandler != nil {
		app.Use("/ws/console", s.authMiddleware)
		app.Get("/ws/console", websocket.New(s.consoleHandler.HandleConsole, wsConfigWithSubprotocol(s.allowedOrigins)))
	}

	if s.vncHandler != nil {
		app.Use("/ws/vnc", s.authMiddleware)
		app.Get("/ws/vnc", websocket.New(s.vncHandler.HandleVNC, wsConfigWithSubprotocol(s.allowedOrigins)))
	}

	app.Use("/ws", s.authMiddleware)
	app.Get("/ws", websocket.New(s.handleWS, wsConfigWithSubprotocol(s.allowedOrigins)))

	s.app = app
	return s
}

// Listen starts the HTTP server on the given port.
func (s *Server) Listen(port int) error {
	addr := fmt.Sprintf(":%d", port)
	s.logger.Info("WebSocket server listening", "addr", addr)
	return s.app.Listen(addr)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	return s.app.Shutdown()
}

// App returns the underlying Fiber app (for testing).
func (s *Server) App() *fiber.App {
	return s.app
}

// RegisterRoutes mounts WebSocket routes onto an external Fiber app.
// Used by the unified binary to serve WS on the same port as the API.
func (s *Server) RegisterRoutes(app *fiber.App) {
	if s.consoleHandler != nil {
		app.Use("/ws/console", s.authMiddleware)
		app.Get("/ws/console", websocket.New(s.consoleHandler.HandleConsole, wsConfigWithSubprotocol(s.allowedOrigins)))
	}

	if s.vncHandler != nil {
		app.Use("/ws/vnc", s.authMiddleware)
		app.Get("/ws/vnc", websocket.New(s.vncHandler.HandleVNC, wsConfigWithSubprotocol(s.allowedOrigins)))
	}

	app.Use("/ws", s.authMiddleware)
	app.Get("/ws", websocket.New(s.handleWS, wsConfigWithSubprotocol(s.allowedOrigins)))
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
// `*` short-circuits to nil — the gofiber/contrib/websocket convention for
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
func (s *Server) healthz(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// authMiddleware validates a short-lived scoped JWT before WebSocket upgrade.
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
// authenticated only by short-lived single-purpose tokens".
func (s *Server) authMiddleware(c *fiber.Ctx) error {
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

	// Compare case-insensitively because Fiber routes case-insensitively by
	// default (CaseSensitive=false). A request to /WS/Console still hits
	// the /ws/console handler; a strict equality on c.Path() would let it
	// fall into the "scope not required" branch and accept a regular
	// access token. Use EqualFold so the gate matches whatever Fiber's
	// router matched.
	path := c.Path()
	requiresConsoleScope := strings.EqualFold(path, "/ws/console") || strings.EqualFold(path, "/ws/vnc")

	switch {
	case requiresConsoleScope:
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
		if err := validateConsoleScope(c, claims.ConsoleScope); err != nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
		}
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
func tokenFromSubprotocolHeader(c *fiber.Ctx) string {
	values := c.Context().Request.Header.PeekAll("Sec-WebSocket-Protocol")
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
func validateConsoleScope(c *fiber.Ctx, scope *auth.ConsoleScope) error {
	return validateConsoleScopeFields(
		c.Path(),
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

	if clusterID != scope.ClusterID {
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
