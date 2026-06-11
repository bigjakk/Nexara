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

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// contentSecurityPolicy is the default CSP for the SPA shell + API responses.
// JSX auto-escaping is otherwise the only XSS control, so this is the second
// line of defense. Notes on the directives:
//   - script-src 'self': the Vite production build emits only external module
//     scripts (no inline, no eval). index.html must NOT carry inline scripts —
//     they get silently blocked (console-only error). The pre-paint theme
//     bootstrap lives in frontend/public/theme-init.js for exactly this reason.
//   - style-src 'unsafe-inline': Tailwind/shadcn (Radix), Recharts, and React
//     Flow inject inline styles — required, and low-risk for styles.
//   - connect-src ws: wss': the floating console (xterm) and noVNC open
//     same-origin WebSockets; ws: covers the dev (http) origin.
//   - worker-src/img-src blob': noVNC/xterm renderers and canvas-to-blob.
// Handlers that serve downloadable HTML (reports, settings export) set their
// own stricter CSP via c.Set after this middleware, which overrides it.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self'; " +
	"connect-src 'self' ws: wss:; " +
	"worker-src 'self' blob:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"object-src 'none'"

func (s *Server) setupMiddleware() {
	// Recover from panics.
	s.app.Use(recover.New())

	// Security headers (proxy-agnostic — always present regardless of reverse proxy choice).
	s.app.Use(func(c *fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Set("Content-Security-Policy", contentSecurityPolicy)
		// HSTS is opt-in (HSTS_MAX_AGE > 0) — pinning HTTPS over a self-signed or
		// plain-HTTP origin would make cert errors unbypassable.
		if s.config.HSTSMaxAge > 0 {
			c.Set("Strict-Transport-Security", "max-age="+strconv.Itoa(s.config.HSTSMaxAge)+"; includeSubDomains")
		}
		return c.Next()
	})

	// Expose the shared Proxmox client cache to every request so
	// handlers.CreateProxmoxClient routes through it. Nil-safe: when
	// proxmoxCache is unset (no encryption key, test scaffolding) the
	// helper falls through to per-call construction.
	if s.proxmoxCache != nil {
		s.app.Use(func(c *fiber.Ctx) error {
			handlers.SetProxmoxCacheLocal(c, s.proxmoxCache)
			return c.Next()
		})
	}

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

	// Strict rate limiter for login/register and TOTP code-validating paths
	// — 15 attempts per minute per IP. Applied before the general limiter so
	// auth brute-force is caught early. Includes Disable and
	// RegenerateRecoveryCodes because both validate a TOTP code; without this,
	// an attacker holding a stolen access token would have no per-IP cap and
	// only the per-user lockout (5 fails / 5 min cooldown) — see Phase 4.4.
	s.app.Use(limiter.New(limiter.Config{
		Max:        15,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP() + ":auth"
		},
		Next: func(c *fiber.Ctx) bool {
			switch c.Path() {
			case "/api/v1/auth/login",
				"/api/v1/auth/register",
				"/api/v1/auth/totp/verify-login",
				"/api/v1/auth/totp",
				"/api/v1/auth/totp/",
				"/api/v1/auth/totp/recovery-codes/regenerate":
				return false
			}
			return true
		},
	}))

	// Refresh-specific rate limiter — 30/min/IP. Tighter than the general
	// limiter because /auth/refresh is below the general bypass; comfortably
	// above any legitimate pattern (proactive refresh fires once every ~14
	// minutes per session). Caps cookie-replay grinding without breaking
	// normal browser sessions.
	s.app.Use(limiter.New(limiter.Config{
		Max:        30,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP() + ":refresh"
		},
		Next: func(c *fiber.Ctx) bool {
			return c.Path() != "/api/v1/auth/refresh"
		},
	}))

	// WS-token mint limiter — 60/min/IP. Each /ws connection mints one
	// token; legitimate reconnect/backoff is well under 1/sec. The
	// limiter sits inside the auth-bypassed group, so without this an
	// authenticated user could fire mints in a tight loop. Per-IP
	// (vs per-user) because limiter middleware runs before authRequired.
	s.app.Use(limiter.New(limiter.Config{
		Max:        60,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP() + ":ws-token"
		},
		Next: func(c *fiber.Ctx) bool {
			return c.Path() != "/api/v1/auth/ws-token"
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

		// Scoped console tokens are single-purpose: they ONLY authorize a
		// specific WebSocket upgrade. Reject them at the regular API boundary
		// so a leaked console token cannot be used to call other endpoints.
		if claims.ConsoleScope != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Console-scoped token cannot be used for API requests")
		}
		// Same logic for WS-hub-scoped tokens — they only authorize the
		// /ws upgrade, never an API request.
		if claims.WSScope != "" {
			return fiber.NewError(fiber.StatusUnauthorized, "WS-scoped token cannot be used for API requests")
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

		// Scoped console / WS-hub tokens must not be treated as general-purpose auth.
		if claims.ConsoleScope != nil || claims.WSScope != "" {
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
	c.Locals("api_key_id", row.ID)

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
