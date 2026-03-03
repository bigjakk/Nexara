package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
)

func (s *Server) setupMiddleware() {
	// Recover from panics.
	s.app.Use(recover.New())

	// Add unique request ID.
	s.app.Use(requestid.New())

	// Structured request logging with request ID.
	s.app.Use(logger.New(logger.Config{
		Format: "${time} | ${status} | ${latency} | ${ip} | ${locals:requestid} | ${method} ${path}\n",
	}))

	// CORS.
	s.app.Use(cors.New(cors.Config{
		AllowOrigins: s.config.CORSAllowOrigins,
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET, POST, PUT, PATCH, DELETE, OPTIONS",
	}))

	// Rate limiting (in-memory storage).
	s.app.Use(limiter.New(limiter.Config{
		Max:        s.config.RateLimitMax,
		Expiration: s.config.RateLimitExpiration,
	}))
}

// authRequired returns middleware that rejects unauthenticated requests.
func (s *Server) authRequired() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if s.jwtService == nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Auth not configured")
		}

		token := extractBearerToken(c)
		if token == "" {
			return fiber.NewError(fiber.StatusUnauthorized, "Missing authorization token")
		}

		claims, err := s.jwtService.ValidateAccessToken(token)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired token")
		}

		c.Locals("user_id", claims.UserID)
		c.Locals("email", claims.Email)
		c.Locals("role", claims.Role)

		return c.Next()
	}
}

// authOptional returns middleware that extracts auth info if present, but doesn't require it.
func (s *Server) authOptional() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if s.jwtService == nil {
			return c.Next()
		}

		token := extractBearerToken(c)
		if token == "" {
			return c.Next()
		}

		claims, err := s.jwtService.ValidateAccessToken(token)
		if err != nil {
			return c.Next()
		}

		c.Locals("user_id", claims.UserID)
		c.Locals("email", claims.Email)
		c.Locals("role", claims.Role)

		return c.Next()
	}
}

// extractBearerToken extracts the JWT from the Authorization header.
func extractBearerToken(c *fiber.Ctx) string {
	header := c.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}
