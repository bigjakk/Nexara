package api

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/proxdash/proxdash/internal/api/handlers"
	"github.com/proxdash/proxdash/internal/auth"
	"github.com/proxdash/proxdash/internal/config"
	db "github.com/proxdash/proxdash/internal/db/generated"
)

// Server is the API server that holds all dependencies.
type Server struct {
	app            *fiber.App
	config         *config.Config
	db             *pgxpool.Pool
	queries        *db.Queries
	redis          *redis.Client
	jwtService     *auth.JWTService
	sessionManager *auth.SessionManager
	authHandler    *handlers.AuthHandler
}

// New creates a new API server with the given dependencies.
func New(cfg *config.Config, pool *pgxpool.Pool, rdb *redis.Client) *Server {
	s := &Server{
		config: cfg,
		db:     pool,
		redis:  rdb,
	}

	if pool != nil {
		s.queries = db.New(pool)
	}

	// Initialize auth services when dependencies are available.
	if cfg.JWTSecret != "" {
		s.jwtService = auth.NewJWTService(cfg.JWTSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	}

	if s.queries != nil && rdb != nil {
		s.sessionManager = auth.NewSessionManager(s.queries, rdb)
	}

	if s.queries != nil && s.jwtService != nil && s.sessionManager != nil {
		s.authHandler = handlers.NewAuthHandler(s.queries, s.jwtService, s.sessionManager)
	}

	s.app = fiber.New(fiber.Config{
		ErrorHandler:          errorHandler,
		DisableStartupMessage: true,
	})

	s.setupMiddleware()
	s.setupRoutes()

	return s
}

// Listen starts the HTTP server on the given address.
func (s *Server) Listen(addr string) error {
	return s.app.Listen(addr)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	return s.app.Shutdown()
}

// App returns the underlying Fiber app for testing.
func (s *Server) App() *fiber.App {
	return s.app
}
