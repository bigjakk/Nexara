package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// TestRefreshCookie_PathIsTheExportedScope: internal/api skips compression and the general
// limiter under RefreshCookiePath, so the constant must be what reaches the browser; this
// asserts the Set-Cookie header, not the constant. clearRefreshCookie is checked too: a
// browser accepts a deletion only with matching attributes, or logout leaves a live cookie.
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
