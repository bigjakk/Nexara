package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// TestCompressionExclusion_TracksTheRefreshCookiePath pins that the compression
// and rate-limit exclusions and the refresh cookie's Path are ONE value.
//
// They used to be two literals kept in step by a comment in each file. That is
// the arrangement this repo keeps getting bitten by: nothing fails when one
// moves, and the failure is silent in the direction that matters — widening the
// cookie's Path without widening the exclusion leaves a credential-bearing
// subtree compressible, and widening it the other way hands an attacker a
// rate-limit-exempt path that is not auth at all.
//
// The probes below are DERIVED from handlers.RefreshCookiePath rather than
// written out, which is what keeps this from being a tautology dressed as a
// test: re-introducing a hand-copied literal in middleware.go makes the derived
// child stop being excluded, and the identity assertion names it outright.
// TestRefreshCookie_PathIsTheExportedScope holds the other end, proving that
// constant is the Path the cookie is actually issued with.
func TestCompressionExclusion_TracksTheRefreshCookiePath(t *testing.T) {
	if authCookieScopePrefix != handlers.RefreshCookiePath {
		t.Fatalf("authCookieScopePrefix = %q but the refresh cookie's Path is %q — these must be the "+
			"same value, not two that agree today; derive one from the other",
			authCookieScopePrefix, handlers.RefreshCookiePath)
	}
	if want := strings.TrimSuffix(handlers.RefreshCookiePath, "/"); authCookieScope != want {
		t.Fatalf("authCookieScope = %q, want %q (the cookie Path without its trailing slash)",
			authCookieScope, want)
	}
	// RFC 6265 §5.1.4. Without this the subtree root and the child prefix
	// collapse to the same string and the exclusion reaches a neighbour.
	// Errorf, not Fatalf: the table below is what shows the CONSEQUENCE of
	// losing the slash (the neighbour row starts matching), and stopping here
	// would hide it behind the cause.
	if !strings.HasSuffix(handlers.RefreshCookiePath, "/") {
		t.Errorf("refresh cookie Path %q has no trailing slash — the exclusion below would then match "+
			"%s-debug, which is not auth", handlers.RefreshCookiePath, authCookieScope)
	}

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"child of the cookie scope", handlers.RefreshCookiePath + "sessions", true},
		{"subtree root", strings.TrimSuffix(handlers.RefreshCookiePath, "/"), true},
		{"neighbour sharing the prefix", strings.TrimSuffix(handlers.RefreshCookiePath, "/") + "-debug/status", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			var got, ran bool
			app.Get("/*", func(c fiber.Ctx) error {
				got, ran = compressionSkipped(c), true
				return c.SendString("ok")
			})
			if _, err := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil)); err != nil {
				t.Fatalf("%s: %v", tc.path, err)
			}
			// Same reasoning as TestCompressionSkipped_Decisions: a route that
			// stopped matching would leave `got` false and the want:false row
			// would pass without the predicate ever running.
			if !ran {
				t.Fatalf("%s never reached the handler, so compressionSkipped was never called", tc.path)
			}
			if got != tc.want {
				t.Errorf("compressionSkipped(%s) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestRateLimitedAuthPaths_LieUnderTheCookieScope ties the other consumer of
// the prefix to the same constant.
//
// The general rate limiter skips everything under authCookieScopePrefix so a
// 429 on token refresh can never be mistaken for an auth failure and log the
// user out. These paths carry their own brute-force caps precisely because
// they are exempt from the general one, so a prefix that stopped covering them
// would silently move them onto the general limiter — and a prefix that grew
// to cover more would exempt endpoints that were never meant to be.
func TestRateLimitedAuthPaths_LieUnderTheCookieScope(t *testing.T) {
	paths := make([]string, 0, 2+len(authLimitedPaths))
	paths = append(paths, refreshLimitedPath, wsTokenLimitedPath)
	for p := range authLimitedPaths {
		paths = append(paths, p)
	}
	if len(paths) < 3 {
		t.Fatalf("only %d auth paths to check; this test would pass by iterating nothing", len(paths))
	}

	for _, p := range paths {
		if !strings.HasPrefix(p, authCookieScopePrefix) {
			t.Errorf("%s is rate-limited as an auth path but does not lie under %q (the refresh "+
				"cookie's Path) — the general limiter would no longer skip it", p, authCookieScopePrefix)
		}
	}
}
