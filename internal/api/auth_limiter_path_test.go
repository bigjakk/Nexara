package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestAuthLimiterCannotBeSpelledAround is a regression test for a live authentication brute-force
// bypass. Fiber routes on a lowercased, slash-trimmed path (CaseSensitive and StrictRouting are both false
// in buildFiberConfig) while c.Path() returns the path before that fold, and the auth limiter matched it
// exactly. "POST /api/v1/auth/login/" reached the login handler with NO rate limit (the auth limiter's
// switch missed it and the general limiter's "/api/v1/auth/" prefix check still matched, so that skipped it
// too: 15/min became unlimited); "POST /API/v1/auth/login" fell through to the general limiter's budget,
// 600/min instead of 15/min. Verified against fiber v3 before and after the fix; limiterPath() normalizes
// to the spelling Fiber actually routed on.
func TestAuthLimiterCannotBeSpelledAround(t *testing.T) {
	// newTestServer sets RateLimitMax=100, and the auth cap is 15 — so a 429
	// inside this many requests can only have come from the auth limiter.
	const attempts = 60

	for _, path := range []string{
		"/api/v1/auth/login",
		// U+0130 sent raw: the router folds ASCII only and routes it nowhere, so
		// the auth bucket must not count it (strings.ToLower made it "i").
		"!/apİ/v1/auth/login",
		"/api/v1/auth/login/",
		"/API/v1/auth/login",
		"/api/V1/Auth/Login",
		// Fiber v3.6 routes these to login too, so the limiters must read the
		// normalized path the router matches (c.Path()), never the raw one.
		"/api/v1/x/../auth/login",
		"/api/v1/auth/%6cogin",
	} {
		t.Run(path, func(t *testing.T) {
			s := newTestServer(t)

			target, wantLimited := strings.CutPrefix(path, "!")
			wantLimited = !wantLimited
			var got []int
			for i := 0; i < attempts; i++ {
				req := httptest.NewRequest(http.MethodPost, "/", nil)
				req.URL = &url.URL{Opaque: target} // on the wire byte for byte
				resp, err := s.app.Test(req)
				if err != nil {
					t.Fatalf("Test: %v", err)
				}
				got = append(got, resp.StatusCode)
				_ = resp.Body.Close()
			}

			// No early return on 404. The limiter is app-level middleware and
			// runs BEFORE routing, so it counts a request whether or not a
			// route matches — and bailing on 404 is precisely how this test
			// would pass while the bypass it exists for was wide open.
			var limited bool
			for _, code := range got {
				if code == fiber.StatusTooManyRequests {
					limited = true
					break
				}
			}
			if limited != wantLimited {
				t.Errorf("%s: %d requests, auth cap hit %t, want %t — the limiter and the router "+
					"read this spelling differently (statuses: %v)", target, attempts, limited, wantLimited, got[:5])
			}
		})
	}
}

// TestLogout_HasNoPerAddressLimit: sign-out is unauthenticated, so a per-address cap
// on it lets anyone behind the same NAT or proxy loop it and turn every honest
// sign-out from there into a 429 that leaves the session alive (see the Auth limiter
// in setupMiddleware). Far more sign-outs than any cap here allows, from one address,
// and the owner's still reaches the handler.
func TestLogout_HasNoPerAddressLimit(t *testing.T) {
	s := newTestServer(t)
	t.Parallel()
	s.app.Post("/api/v1/auth/logout", func(c fiber.Ctx) error {
		if c.Cookies("nexara_refresh") != "the-owners-cookie" {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		return c.SendStatus(fiber.StatusOK)
	})
	post := func(cookie string) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		req.Header.Set("Cookie", "nexara_refresh="+cookie)
		resp, err := s.app.Test(req)
		if err != nil {
			t.Fatalf("Test: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	for i := range 300 {
		post(fmt.Sprintf("random-%d", i))
	}
	if code := post("the-owners-cookie"); code != fiber.StatusOK {
		t.Errorf("the owner's sign-out after 300 others from the same address = %d, want 200", code)
	}
}

// The console-token mint is capped at 60 a minute per address, and the router's
// other spellings of its path spend the same budget: 61 mints, the last two in
// upper case and escaped, and only the last is refused.
func TestConsoleTokenMintsAreCapped(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	var got []int
	for i := 0; i < 61; i++ {
		target := "/api/v1/auth/console-token"
		switch i {
		case 59:
			target = "/API/v1/auth/console-token"
		case 60:
			target = "/api/v1/auth/%63onsole-token"
		}
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.URL = &url.URL{Opaque: target}
		resp, err := s.app.Test(req)
		if err != nil {
			t.Fatalf("Test: %v", err)
		}
		got = append(got, resp.StatusCode)
		_ = resp.Body.Close()
	}
	if got[59] == fiber.StatusTooManyRequests || got[60] != fiber.StatusTooManyRequests {
		t.Errorf("the 60th and 61st mints answered %d and %d; want the 61st alone refused 429", got[59], got[60])
	}
}
