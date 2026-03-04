package ws

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/proxdash/proxdash/internal/auth"
)

// Server is the WebSocket server backed by Fiber.
type Server struct {
	app    *fiber.App
	hub    *Hub
	jwt    *auth.JWTService
	logger *slog.Logger

	pingInterval time.Duration
	pongTimeout  time.Duration
}

// NewServer creates a new WebSocket server.
func NewServer(hub *Hub, jwtSvc *auth.JWTService, logger *slog.Logger, pingInterval, pongTimeout time.Duration) *Server {
	s := &Server{
		hub:          hub,
		jwt:          jwtSvc,
		logger:       logger,
		pingInterval: pingInterval,
		pongTimeout:  pongTimeout,
	}

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})

	app.Get("/healthz", s.healthz)
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

// healthz returns a 200 OK for health checks.
func (s *Server) healthz(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

// authMiddleware validates the JWT token from the query parameter before WebSocket upgrade.
func (s *Server) authMiddleware(c *fiber.Ctx) error {
	if !websocket.IsWebSocketUpgrade(c) {
		return fiber.ErrUpgradeRequired
	}

	token := c.Query("token")
	if token == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing token"})
	}

	claims, err := s.jwt.ValidateAccessToken(token)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid token"})
	}

	// Store claims in locals for the WebSocket handler.
	c.Locals("userID", claims.UserID)
	c.Locals("email", claims.Email)
	c.Locals("role", claims.Role)

	return c.Next()
}

// handleWS handles a WebSocket connection after upgrade.
func (s *Server) handleWS(conn *websocket.Conn) {
	userID, _ := conn.Locals("userID").(uuid.UUID)
	clientID := fmt.Sprintf("%s-%s", userID, uuid.New().String()[:8])

	client := NewClient(clientID, conn, s.hub, s.logger, s.pingInterval, s.pongTimeout)
	s.hub.Register(client)

	// Send welcome message.
	client.trySend(newWelcomeMsg())

	// Start write pump in a separate goroutine.
	go client.writePump()

	// readPump blocks until the connection is closed.
	client.readPump()
}
