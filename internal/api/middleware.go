package api

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

func (s *Server) setupMiddleware() {
	// Recover from panics.
	s.app.Use(recover.New())

	// Security headers (proxy-agnostic — always present regardless of reverse proxy choice).
	s.app.Use(func(c *fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		return c.Next()
	})

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

	// Body size limit for non-upload API traffic (10 MB).
	// Upload endpoints bypass this check — their bodies are streamed via
	// StreamRequestBody and parsed incrementally by the handler.
	const apiBodyLimit = 10 * 1024 * 1024
	s.app.Use(func(c *fiber.Ctx) error {
		if strings.Contains(c.Path(), "/storage/") && strings.HasSuffix(c.Path(), "/upload") {
			return c.Next()
		}
		cl := c.Get("Content-Length")
		if cl != "" {
			size, err := strconv.ParseInt(cl, 10, 64)
			if err == nil && size > apiBodyLimit {
				return fiber.ErrRequestEntityTooLarge
			}
		}
		return c.Next()
	})

	// Strict rate limiter for login/register — 15 attempts per minute per IP.
	// Applied before the general limiter so auth brute-force is caught early.
	s.app.Use(limiter.New(limiter.Config{
		Max:        15,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP() + ":auth"
		},
		Next: func(c *fiber.Ctx) bool {
			p := c.Path()
			return p != "/api/v1/auth/login" &&
				p != "/api/v1/auth/register" &&
				p != "/api/v1/auth/totp/verify-login"
		},
	}))

	// General rate limiting (in-memory storage).
	// Skip auth endpoints so token refresh is never blocked — a 429 on
	// /auth/refresh causes the frontend to interpret it as an auth failure
	// and log the user out.
	s.app.Use(limiter.New(limiter.Config{
		Max:        s.config.RateLimitMax,
		Expiration: s.config.RateLimitExpiration,
		Next: func(c *fiber.Ctx) bool {
			return strings.HasPrefix(c.Path(), "/api/v1/auth/") ||
				strings.HasPrefix(c.Path(), "/ws")
		},
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

		// API key tokens start with "nxra_".
		if strings.HasPrefix(token, "nxra_") {
			if err := s.authenticateAPIKey(c, token); err != nil {
				return err
			}
			return c.Next()
		}

		claims, err := s.jwtService.ValidateAccessToken(token)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired token")
		}

		c.Locals("user_id", claims.UserID)
		c.Locals("email", claims.Email)
		c.Locals("role", claims.Role)

		if s.rbacEngine != nil {
			c.Locals("rbac_engine", s.rbacEngine)
		}

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

		// API key tokens start with "nxra_".
		if strings.HasPrefix(token, "nxra_") {
			// Best-effort: if API key auth fails, continue unauthenticated.
			if err := s.authenticateAPIKey(c, token); err != nil {
				return c.Next()
			}
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

// authenticateAPIKey validates an nxra_ prefixed API key token and sets
// the user identity in Fiber locals. Returns an error on failure.
func (s *Server) authenticateAPIKey(c *fiber.Ctx, token string) error {
	if s.queries == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Database not configured")
	}

	keyHash := auth.HashToken(token)
	row, err := s.queries.GetAPIKeyByHash(c.Context(), keyHash)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired API key")
	}
	if !row.UserIsActive {
		return fiber.NewError(fiber.StatusUnauthorized, "User account is inactive")
	}

	c.Locals("user_id", row.UserID)
	c.Locals("email", row.UserEmail)
	c.Locals("role", row.UserRole)
	c.Locals("auth_method", "api_key")

	if s.rbacEngine != nil {
		c.Locals("rbac_engine", s.rbacEngine)
	}

	// Update last_used asynchronously to avoid adding latency.
	keyID := row.ID
	ip := c.IP()
	if len(ip) > 45 { // max IPv6 length
		ip = ip[:45]
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if updateErr := s.queries.UpdateAPIKeyLastUsed(ctx, db.UpdateAPIKeyLastUsedParams{
			ID:         keyID,
			LastUsedIp: pgtype.Text{String: ip, Valid: ip != ""},
		}); updateErr != nil {
			slog.Default().Warn("failed to update API key last_used", "error", updateErr)
		}
	}()

	return nil
}
