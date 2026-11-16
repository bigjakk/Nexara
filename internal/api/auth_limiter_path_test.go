package api

import (
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

// TestLogout_HasABudgetOfItsOwn: sign-out had no cap at all (the general limiter
// skips the auth subtree). It gets one per address, and it is NOT the Auth bucket:
// an address that has spent that on logins must still be able to sign out.
func TestLogout_HasABudgetOfItsOwn(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	post := func(path string) int {
		t.Helper()
		resp, err := s.app.Test(httptest.NewRequest(http.MethodPost, path, nil))
		if err != nil {
			t.Fatalf("Test: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	for range 20 {
		post("/api/v1/auth/login")
	}
	for i := 1; i <= logoutRateLimit; i++ {
		if code := post("/api/v1/auth/logout"); code == fiber.StatusTooManyRequests {
			t.Fatalf("sign-out %d of %d answered 429 after the Auth bucket was spent", i, logoutRateLimit)
		}
	}
	if code := post("/api/v1/auth/logout"); code != fiber.StatusTooManyRequests {
		t.Errorf("sign-out %d = %d, want 429: the cap does not hold", logoutRateLimit+1, code)
	}
}
