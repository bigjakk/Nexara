package api

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
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

// corsAllowOrigins converts the comma-separated CORS_ALLOW_ORIGINS config value
// into the []string that Fiber v3's cors middleware expects (v2 accepted a raw
// comma-string). An empty or literal "*" value maps to ["*"], preserving the v2
// behavior where an unset AllowOrigins defaulted to "*" (allow all). The cors
// middleware sets no credentials here, so a wildcard origin is valid and does not
// trip v3's AllowCredentials+"*" guard.
func corsAllowOrigins(raw string) []string {
	if raw == "" || raw == "*" {
		return []string{"*"}
	}
	var out []string
	for _, o := range strings.Split(raw, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		return []string{"*"}
	}
	return out
}

func (s *Server) setupMiddleware() {
	// Recover from panics.
	s.app.Use(recover.New())

	// Security headers (proxy-agnostic — always present regardless of reverse proxy choice).
	s.app.Use(func(c fiber.Ctx) error {
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
		s.app.Use(func(c fiber.Ctx) error {
			handlers.SetProxmoxCacheLocal(c, s.proxmoxCache)
			return c.Next()
		})
	}

	// Add unique request ID.
	s.app.Use(requestid.New())

	// Structured request logging with request ID.
	// Fiber v3's requestid middleware registers a logger context tag
	// (logger.RegisterContextTag("requestid", …)), so the ID is referenced as
	// ${requestid} — the v2 ${locals:requestid} no longer resolves because
	// requestid stores the value in the request context, not c.Locals.
	s.app.Use(logger.New(logger.Config{
		Format: "${time} | ${status} | ${latency} | ${ip} | ${requestid} | ${method} ${path}\n",
	}))

	// CORS. Fiber v3 takes []string for the allow-lists (v2 took comma-strings).
	s.app.Use(cors.New(cors.Config{
		AllowOrigins: corsAllowOrigins(s.config.CORSAllowOrigins),
		AllowHeaders: []string{"Origin", "Content-Type", "Accept", "Authorization"},
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
	}))

	// Body size limit for non-upload API traffic (10 MB).
	// Upload endpoints bypass this check — their bodies are streamed via
	// StreamRequestBody and parsed incrementally by the handler.
	const apiBodyLimit = 10 * 1024 * 1024
	s.app.Use(func(c fiber.Ctx) error {
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
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":auth"
		},
		Next: func(c fiber.Ctx) bool {
			switch limiterPath(c) {
			case "/api/v1/auth/login",
				"/api/v1/auth/register",
				"/api/v1/auth/totp/verify-login",
				"/api/v1/auth/totp",
				"/api/v1/auth/totp/recovery-codes/regenerate",
				// The OIDC flow is unauthenticated and every call does real
				// work: /authorize performs an outbound discovery fetch to the
				// IdP and writes a 10-minute Redis state key. Everything under
				// /api/v1/auth/ is exempt from the general limiter, so without
				// these two entries an anonymous loop can pin the server on
				// outbound HTTP, hammer the operator's IdP, and grow the Redis
				// instance that also holds sessions.
				"/api/v1/auth/oidc/authorize",
				"/api/v1/auth/oidc/callback":
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
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":refresh"
		},
		Next: func(c fiber.Ctx) bool {
			return limiterPath(c) != "/api/v1/auth/refresh"
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
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":ws-token"
		},
		Next: func(c fiber.Ctx) bool {
			return limiterPath(c) != "/api/v1/auth/ws-token"
		},
	}))

	// Snapshot-resync limiter — each call makes one Proxmox listing plus one
	// DB write per snapshot, and the endpoint is view-gated, so without a
	// dedicated cap a read-only account could pin both the DB and PVE well
	// within the general limiter's budget.
	s.app.Use(limiter.New(limiter.Config{
		Max:        30,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":snapshot-resync"
		},
		Next: func(c fiber.Ctx) bool {
			return !strings.HasSuffix(limiterPath(c), "/guest-snapshots/resync")
		},
	}))

	// General rate limiting (in-memory storage).
	// Skip auth endpoints so token refresh is never blocked — a 429 on
	// /auth/refresh causes the frontend to interpret it as an auth failure
	// and log the user out.
	s.app.Use(limiter.New(limiter.Config{
		Max:        s.config.RateLimitMax,
		Expiration: s.config.RateLimitExpiration,
		Next: func(c fiber.Ctx) bool {
			return strings.HasPrefix(limiterPath(c), "/api/v1/auth/") ||
				strings.HasPrefix(limiterPath(c), "/ws")
		},
	}))
}

// clusterCreateLimiter caps POST /api/v1/clusters at 10/min/IP.
//
// That endpoint is the one place Nexara can be handed a Proxmox password, and
// in bootstrap mode every call spends a real /access/ticket attempt against the
// operator's hypervisor. Without a dedicated cap the general limiter's much
// larger budget would make Nexara a convenient password-spraying proxy onto a
// cluster it is trusted to reach.
//
// Attached to the ROUTE rather than via app.Use, for two reasons:
//
//   - A path-matching Next() cannot be written safely at app level. Fiber routes
//     on a lowercased, slash-trimmed path (CaseSensitive and StrictRouting are
//     both false), so "POST /API/v1/clusters//" reaches this handler while a
//     comparison against "/api/v1/clusters" does not match it — the limiter
//     would be skipped by a request that still spends a login attempt.
//   - App-level middleware runs before the group's authRequired, so anonymous
//     traffic could drain the bucket and lock legitimate onboarding out.
//     Behind a proxy with TRUSTED_PROXIES unset every client shares one bucket,
//     which makes that a one-line denial of service.
//
// Per-IP rather than per-user because the limiter still runs before the handler
// resolves the actor; manage:cluster is enforced inside Create.
func (s *Server) clusterCreateLimiter() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        10,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":cluster-create"
		},
	})
}

// veeamConnectLimiter caps the Veeam endpoints that spend a real logon at
// 10/min/IP — create, update and test.
//
// Same reasoning as clusterCreateLimiter, and a worse target: every one of
// these calls performs an OAuth2 password grant against a VBR server that is
// usually Active-Directory-backed, with a `DOMAIN\user` credential. Under the
// general limiter's budget an authenticated manage:veeam holder could spray
// hundreds of domain logons a minute through Nexara's IP, and repeated /test
// calls against a stored account are a one-liner lockout DoS.
//
// Attached to the route rather than app-level, for the reasons spelled out on
// clusterCreateLimiter: a path-matching Next() cannot be written safely, and
// app-level middleware runs before authRequired.
func (s *Server) veeamConnectLimiter() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        10,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":veeam-connect"
		},
	})
}

// fingerprintFetchLimiter caps POST /api/v1/clusters/fetch-fingerprint at
// 30/min/IP.
//
// The endpoint dials an arbitrary caller-supplied host to read its
// certificate. It spends no credential, so it is not the spraying risk
// clusterCreateLimiter guards — but under the general limiter it was 600
// outbound TLS probes a minute, and the 200-vs-502 split is a clean
// "is something listening here" oracle. It is shared by the cluster, PBS and
// Veeam add-flows, so the cap is looser than the 10/min on the creates it
// precedes: a human fumbling the certificate step must not lock themselves
// out of the create that follows it.
func (s *Server) fingerprintFetchLimiter() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        30,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":fetch-fingerprint"
		},
	})
}

// limiterPath returns the request path the way Fiber ROUTED it, which is the
// only spelling a path-matching limiter can safely compare against.
//
// Fiber builds a lowercased, slash-trimmed detectionPath and matches routes on
// that, while c.Path() returns the raw path — CaseSensitive and StrictRouting
// are both false here. A limiter gating on the raw path therefore steps aside
// for spellings that still reach the handler:
//
//	POST /api/v1/auth/login/   the auth limiter's exact match misses it, and the
//	                           general limiter's "/api/v1/auth/" prefix still
//	                           matches, so it is skipped too — login ends up with
//	                           NO rate limit at all
//	POST /API/v1/auth/login    auth limiter skipped; the general limiter applies
//	                           its far larger budget instead
//
// Route-attached limiters (see clusterCreateLimiter) do not need this, because
// matching is Fiber's job by then.
func limiterPath(c fiber.Ctx) string {
	p := strings.ToLower(c.Path())
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// authRequired returns middleware that rejects unauthenticated requests.
func (s *Server) authRequired() fiber.Handler {
	return func(c fiber.Ctx) error {
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
	return func(c fiber.Ctx) error {
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
func extractBearerToken(c fiber.Ctx) string {
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
func (s *Server) authenticateAPIKey(c fiber.Ctx, token string) error {
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
	go func() { //nolint:gosec // G118: intentionally detached — async last_used update must outlive the request (Fiber recycles the request context)
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
