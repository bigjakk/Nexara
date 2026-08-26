package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestAuthLimiterCannotBeSpelledAround is a regression test for a live
// authentication brute-force bypass.
//
// Fiber routes on a lowercased, slash-trimmed path (CaseSensitive and
// StrictRouting are both false in buildFiberConfig) while c.Path() returns the
// raw path. The auth limiter matched the raw path exactly, so:
//
//   - "POST /api/v1/auth/login/" reached the login handler with NO rate limit
//     whatsoever — the auth limiter's switch missed it, and the general
//     limiter's "/api/v1/auth/" prefix check still matched, so that skipped it
//     too. 15/min became unlimited.
//   - "POST /API/v1/auth/login" fell through to the general limiter's budget:
//     600/min instead of 15/min.
//
// Verified against fiber v3 before and after the fix. limiterPath() normalizes
// to the spelling Fiber actually routed on.
func TestAuthLimiterCannotBeSpelledAround(t *testing.T) {
	// newTestServer sets RateLimitMax=100, and the auth cap is 15 — so a 429
	// inside this many requests can only have come from the auth limiter.
	const attempts = 60

	for _, path := range []string{
		"/api/v1/auth/login",
		"/api/v1/auth/login/",
		"/API/v1/auth/login",
		"/api/V1/Auth/Login",
	} {
		t.Run(path, func(t *testing.T) {
			s := newTestServer(t)

			var got []int
			for i := 0; i < attempts; i++ {
				resp, err := s.app.Test(httptest.NewRequest(http.MethodPost, path, nil))
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
			if !limited {
				t.Errorf("%s passed through the middleware stack %d times without hitting the "+
					"auth cap — this spelling escapes brute-force protection (statuses: %v)",
					path, attempts, got[:5])
			}
		})
	}
}
