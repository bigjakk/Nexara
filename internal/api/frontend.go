package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
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
// /api/ that matches no registered route is delegated to Fiber's native
// fallback via c.Next(); next() then returns ErrNotFound (or ErrMethodNotAllowed
// for a wrong method on a real route), which errorHandler renders as the
// standard JSON envelope {"error":"not_found", ...} with a 404 status. Without
// this guard the catch-all answered every unmatched /api/* path with
// 200 + text/html (the SPA shell), making typo'd/removed endpoints look like
// they succeeded and breaking API clients that call .json() on the response.
func (s *Server) RegisterFrontend(distFS fs.FS) {
	spa := filesystem.New(filesystem.Config{
		Root:         http.FS(distFS),
		Browse:       false,
		NotFoundFile: "index.html", // SPA fallback for client-side routing
	})

	s.app.Use("/", func(c *fiber.Ctx) error {
		// Never answer an API request with the SPA shell. Falling through to
		// Fiber's native 404/405 handling keeps the JSON error envelope
		// consistent with every other API error (400/401/405).
		if strings.HasPrefix(c.Path(), "/api/") {
			return c.Next()
		}
		if err := spa(c); err != nil {
			return err
		}
		// Cache policy. The embedded FS has zero modtimes, so responses carry
		// no validators (no Last-Modified/ETag) — without explicit headers,
		// browsers fall back to heuristics and can pin a stale shell that
		// references asset hashes deleted by the next upgrade (blank page on
		// mobile until the user clears site data).
		//   - /assets/* are content-hashed by Vite → cache forever. The
		//     NotFoundFile fallback answers a *stale* asset hash with the HTML
		//     shell, which must not be cached as the asset — hence the
		//     content-type guard.
		//   - Everything else (shell, theme-init.js, favicon) is re-fetched
		//     every load so a new release takes effect immediately.
		contentType := string(c.Response().Header.ContentType())
		if strings.HasPrefix(c.Path(), "/assets/") && !strings.HasPrefix(contentType, "text/html") {
			c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
		} else {
			c.Set(fiber.HeaderCacheControl, "no-cache")
		}
		// The Go mime table doesn't know .webmanifest, so the PWA manifest
		// falls back to application/octet-stream — which browsers refuse
		// under X-Content-Type-Options: nosniff.
		if strings.HasSuffix(c.Path(), ".webmanifest") {
			c.Set(fiber.HeaderContentType, "application/manifest+json")
		}
		return nil
	})
}
