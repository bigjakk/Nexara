package api

import (
	"io/fs"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
)

// RegisterFrontend mounts the embedded SPA as a root catch-all so client-side
// deep links (e.g. /clusters/123/vms) resolve to index.html and let the React
// router take over.
//
// It MUST be called after setupRoutes (and the WS RegisterRoutes) so the real
// /api and /ws routes are matched first — this handler is the last entry in
// the stack.
//
// Crucially, the SPA shell is served ONLY for non-API paths. A request under
// /api/ that matches no registered route is skipped here (via the static
// middleware's Next predicate) and delegated to Fiber's native fallback, which
// returns ErrNotFound (or ErrMethodNotAllowed for a wrong method on a real
// route); errorHandler renders that as the standard JSON envelope
// {"error":"not_found", ...} with a 404 status. Without this guard the catch-all
// answered every unmatched /api/* path with 200 + text/html (the SPA shell),
// making typo'd/removed endpoints look like they succeeded and breaking API
// clients that call .json() on the response.
//
// Fiber v3 replaced the v2 filesystem middleware with middleware/static. The v2
// NotFoundFile (SPA fallback) is now expressed via Config.NotFoundHandler, the
// /api guard via Config.Next, and the post-serve cache-control/content-type
// fixups via Config.ModifyResponse.
func (s *Server) RegisterFrontend(distFS fs.FS) {
	// Read the SPA shell once for the client-side-routing fallback. If it's
	// missing (e.g. a build without an embedded frontend, or test scaffolding),
	// the fallback yields to Fiber's native 404 rather than serving an empty body.
	indexHTML, indexErr := fs.ReadFile(distFS, "index.html")

	s.app.Use("/", static.New("", static.Config{
		FS:     distFS,
		Browse: false,
		// Never serve the SPA shell for API paths — fall through to Fiber's
		// native 404/405 JSON envelope (see the doc comment above).
		Next: func(c fiber.Ctx) bool {
			return strings.HasPrefix(c.Path(), "/api/")
		},
		// ModifyResponse runs after a real file is found and served. The
		// embedded FS has zero modtimes, so responses carry no validators (no
		// Last-Modified/ETag) — without explicit headers, browsers fall back to
		// heuristics and can pin a stale shell that references asset hashes
		// deleted by the next upgrade (blank page on mobile until the user
		// clears site data).
		//   - /assets/* are content-hashed by Vite → cache forever.
		//   - Everything else (shell, theme-init.js, favicon) is re-fetched
		//     every load so a new release takes effect immediately.
		ModifyResponse: func(c fiber.Ctx) error {
			applyFrontendCacheHeaders(c)
			return nil
		},
		// SPA fallback: a non-API path that maps to no real file gets index.html
		// so the React router can resolve client-side routes. Served no-cache so
		// a new release takes effect immediately — and a stale /assets/<hash>
		// request that 404s is answered with the HTML shell, never re-cached as
		// the asset (the immutable rule in applyFrontendCacheHeaders only fires
		// for found, non-HTML /assets/* responses).
		NotFoundHandler: func(c fiber.Ctx) error {
			if indexErr != nil {
				return c.Next()
			}
			c.Set(fiber.HeaderCacheControl, "no-cache")
			c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
			return c.Status(fiber.StatusOK).Send(indexHTML)
		},
	}))
}

// applyFrontendCacheHeaders sets cache-control plus a couple of content-type
// fixups on successfully-served static frontend assets. Split out so the
// found-file path (static's ModifyResponse hook) stays readable.
func applyFrontendCacheHeaders(c fiber.Ctx) {
	contentType := string(c.Response().Header.ContentType())
	if strings.HasPrefix(c.Path(), "/assets/") && !strings.HasPrefix(contentType, "text/html") {
		c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
	} else {
		c.Set(fiber.HeaderCacheControl, "no-cache")
	}
	// The Go mime table doesn't know .webmanifest, so the PWA manifest falls
	// back to application/octet-stream — which browsers refuse under
	// X-Content-Type-Options: nosniff.
	if strings.HasSuffix(c.Path(), ".webmanifest") {
		c.Set(fiber.HeaderContentType, "application/manifest+json")
	}
}
