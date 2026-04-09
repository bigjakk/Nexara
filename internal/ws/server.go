package ws

import (
	"fmt"
	"log/slog"
	"strconv"
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
	// when nil, cluster channels fall open with a server-side warning
	// log to support test fixtures.
	rbacEngine *auth.RBACEngine

	pingInterval time.Duration
	pongTimeout  time.Duration
}

// ServerConfig holds optional dependencies for the WebSocket server.
type ServerConfig struct {
	ConsoleHandler *ConsoleHandler
	VNCHandler     *VNCHandler
	// RBACEngine is required in production for the subscribe-time
	// permission check on cluster channels. If nil, the WS server logs
	// a warning at startup and falls open on cluster subscriptions.
	RBACEngine *auth.RBACEngine
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
	}

	if s.rbacEngine == nil {
		// Production deploys MUST configure this — without it any
		// authenticated user can subscribe to any cluster channel
		// (security review H1 fix). Log loudly so misconfiguration
		// is caught at startup rather than discovered post-deploy.
		s.logger.Warn("ws server: no RBAC engine configured — cluster channel subscriptions will not be authorization-checked")
	}

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})

	app.Get("/healthz", s.healthz)

	// Register console and VNC routes before generic /ws so they match first.
	if s.consoleHandler != nil {
		app.Use("/ws/console", s.authMiddleware)
		app.Get("/ws/console", websocket.New(s.consoleHandler.HandleConsole))
	}

	if s.vncHandler != nil {
		app.Use("/ws/vnc", s.authMiddleware)
		app.Get("/ws/vnc", websocket.New(s.vncHandler.HandleVNC))
	}

	app.Use("/ws", s.authMiddleware)
	app.Get("/ws", websocket.New(s.handleWS))

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
		app.Get("/ws/console", websocket.New(s.consoleHandler.HandleConsole))
	}

	if s.vncHandler != nil {
		app.Use("/ws/vnc", s.authMiddleware)
		app.Get("/ws/vnc", websocket.New(s.vncHandler.HandleVNC))
	}

	app.Use("/ws", s.authMiddleware)
	app.Get("/ws", websocket.New(s.handleWS))
}

// healthz returns a 200 OK for health checks.
func (s *Server) healthz(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// authMiddleware validates the JWT token from the query parameter before WebSocket upgrade.
// It supports two kinds of JWTs:
//   - Regular access tokens — accepted on any WS path.
//   - Scoped console tokens (ConsoleScope != nil) — accepted ONLY on /ws/console
//     or /ws/vnc, and only if the upgrade query params exactly match the scope.
func (s *Server) authMiddleware(c *fiber.Ctx) error {
	if !websocket.IsWebSocketUpgrade(c) {
		return fiber.ErrUpgradeRequired
	}

	token := c.Query("token")
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

	// If this is a scoped console token, enforce the scope exactly.
	if claims.ConsoleScope != nil {
		if err := validateConsoleScope(c, claims.ConsoleScope); err != nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
		}
	}

	// Store claims in locals for the WebSocket handler.
	c.Locals("userID", claims.UserID)
	c.Locals("email", claims.Email)
	c.Locals("role", claims.Role)

	return c.Next()
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
	// subscribe message (security review H1). The closure is nil when
	// no RBAC engine was wired in (test fixtures); the client falls
	// open in that case with a server-side warning.
	var checker PermissionChecker
	if s.rbacEngine != nil {
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
