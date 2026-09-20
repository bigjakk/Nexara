package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// TestRefreshCookie_PathIsTheExportedScope is the handlers-side half of the
// coupling between the refresh cookie's Path and the parts of internal/api
// that mirror it (response compression is skipped under that Path, and so is
// the general rate limiter).
//
// Exporting RefreshCookiePath only helps if it is the value that actually
// reaches the browser. A constant that middleware.go reads but setRefreshCookie
// no longer uses would be a decorative copy with the drift moved one step
// sideways, so this asserts against the Set-Cookie header rather than against
// the constant: whatever internal/api derives from RefreshCookiePath is
// therefore derived from the real Path attribute.
//
// clearRefreshCookie is checked alongside it because a browser only accepts a
// deletion whose attributes match the original Set-Cookie; if the two ever
// disagreed, logout would silently leave a live refresh cookie behind.
func TestRefreshCookie_PathIsTheExportedScope(t *testing.T) {
	app := fiber.New()
	app.Get("/set", func(c fiber.Ctx) error {
		setRefreshCookie(c, "refresh-token-placeholder", time.Hour)
		return c.SendString("ok")
	})
	app.Get("/clear", func(c fiber.Ctx) error {
		clearRefreshCookie(c)
		return c.SendString("ok")
	})

	for _, route := range []string{"/set", "/clear"} {
		t.Run(route, func(t *testing.T) {
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, route, nil))
			if err != nil {
				t.Fatalf("%s: %v", route, err)
			}
			defer resp.Body.Close()

			var got *http.Cookie
			for _, ck := range resp.Cookies() {
				if ck.Name == RefreshCookieName {
					got = ck
				}
			}
			// Non-vacuity: with no cookie in the response every assertion
			// below would be skipped and the test would pass having proved
			// nothing.
			if got == nil {
				t.Fatalf("%s set no %s cookie — nothing to check the Path of", route, RefreshCookieName)
			}

			if got.Path != RefreshCookiePath {
				t.Errorf("%s: cookie Path = %q, want RefreshCookiePath %q — internal/api derives its "+
					"compression and rate-limit exclusions from that constant, so it must be the Path "+
					"actually issued", route, got.Path, RefreshCookiePath)
			}
			// RFC 6265 §5.1.4: without the trailing slash the cookie (and the
			// exclusions mirroring it) would also match /api/v1/auth-debug.
			if !strings.HasSuffix(got.Path, "/") {
				t.Errorf("%s: cookie Path = %q has no trailing slash — per RFC 6265 §5.1.4 it would "+
					"then also match a neighbour like %s-debug", route, got.Path,
					strings.TrimSuffix(RefreshCookiePath, "/"))
			}
			if !got.HttpOnly {
				t.Errorf("%s: refresh cookie is not HttpOnly", route)
			}
		})
	}
}
