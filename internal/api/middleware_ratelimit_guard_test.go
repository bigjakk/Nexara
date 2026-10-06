package api

import (
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// The three auth-facing rate limiters (15/min login-and-TOTP, 30/min refresh, 60/min ws-token) are
// mounted app-wide with Use and select their traffic by PATH (authLimitedPaths in middleware.go), so a
// route's declaration and the cap that protects it agree only by spelling. Declaring a route in the
// registry re-spells its path, and getting it wrong (/auth/totp/ for /auth/totp, a rename) takes the
// brute-force cap off a credential endpoint while the route keeps working; no other guard notices, the
// limiter simply stops matching. This makes the agreement a build failure both ways: every limited path
// must name a mounted route, and the paths a limiter is written for must not lose their cap.

// rateLimitedPaths is every path the three limiters select on, flattened.
func rateLimitedPaths() []string {
	out := make([]string, 0, len(authLimitedPaths)+2)
	for path := range authLimitedPaths {
		out = append(out, path)
	}
	out = append(out, refreshLimitedPath, wsTokenLimitedPath)
	sort.Strings(out)
	return out
}

// limiterPathOf applies the same normalisation limiterPath does at request
// time — lower case, trailing slash trimmed — so a route registered as
// "/api/v1/auth/totp/" and a limiter written for "/api/v1/auth/totp" are
// compared the way the running server compares them.
func limiterPathOf(routePath string) string {
	p := strings.ToLower(routePath)
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// TestGuard_RateLimitedPathsAreRegisteredRoutes is the production guard: every
// path one of the auth limiters caps must be a path the server serves.
//
// A limited path that matches no route is a cap protecting nothing — which is
// exactly what a rename or a reshaped path during a registry migration
// produces, silently and with the route still working.
func TestGuard_RateLimitedPathsAreRegisteredRoutes(t *testing.T) {
	s := sharedRouteStub(t)

	served := map[string]bool{}
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		served[limiterPathOf(r.Path)] = true
	}

	for _, path := range rateLimitedPaths() {
		if !served[path] {
			t.Errorf("rate limiter caps %q but no route is registered at that path — the cap protects "+
				"nothing. Fix the path in middleware.go, or the route's declaration, so they agree.", path)
		}
	}
}

// TestGuard_CredentialRoutesKeepTheirRateLimit is the other direction, the one that matters more: the
// routes below accept a credential or mint a token and each has a per-IP cap for a stated reason
// (password brute force, a TOTP code, refresh replay, an OIDC flow pinning the server on outbound HTTP);
// dropping one from the path set is a one-line edit with no other symptom. The list is restated, not
// derived: a guard reading the map it checks passes whatever it says. NOT in the list (the general
// limiter skips /api/v1/auth/, so absence means NO per-IP cap): logout (dead cookie after one call);
// logout-all and DELETE sessions/:id (capped per USER on their own routes, held OUT of this set on
// purpose, see below); setup-status and sso-status (one boolean from an indexed read); oidc/token-exchange
// (a 5-second single-use GETDEL key); and the two worth revisiting, totp/setup/verify (its own 5-attempt
// Redis budget) and console-token (Deferred, so its body decodes before the permission check).
func TestGuard_CredentialRoutesKeepTheirRateLimit(t *testing.T) {
	mustBeLimited := map[string]string{
		"/api/v1/auth/login":                          "password brute force",
		"/api/v1/auth/register":                       "unauthenticated account creation on a fresh install",
		"/api/v1/auth/change-password":                "checks the CURRENT password, so it is a second place to guess it, reachable with nothing but a bearer token",
		"/api/v1/auth/totp/verify-login":              "six-digit second factor",
		"/api/v1/auth/totp":                           "the disable route validates a TOTP or recovery code",
		"/api/v1/auth/totp/recovery-codes/regenerate": "validates a TOTP code",
		"/api/v1/auth/oidc/authorize":                 "anonymous, and every call fetches the IdP's discovery document",
		"/api/v1/auth/oidc/callback":                  "anonymous, and every call writes a Redis state key",
	}

	for path, why := range mustBeLimited {
		if !authLimitedPaths[path] {
			t.Errorf("%s is not in authLimitedPaths, so it has no per-IP cap — %s", path, why)
		}
	}
	// And nothing may quietly join the set without being justified here.
	for path := range authLimitedPaths {
		if _, expected := mustBeLimited[path]; !expected {
			t.Errorf("authLimitedPaths caps %q, which this guard does not know about — add it with the "+
				"reason, so the set stays a review surface", path)
		}
	}

	// The two routes that end sessions must stay OUT of the per-address set and
	// each carry a limiter of its own. That set is one bucket per address, spent
	// before authentication and shared with login, so anyone with no credential at
	// all could drain it and refuse the owner the remedy for a stolen token. Their
	// caps are per user, on the route (TestLogoutAll_* and TestSessionRevoke_* drive
	// them, and TestSessionEndingRoutesHaveBudgetsOfTheirOwn that they are two);
	// what is pinned here is the declaration of each, and that the shared set holds
	// neither path. The second is not a detail: ending one session is signing out
	// everywhere one device at a time, so a route without a cap of its own is the
	// way round the first.
	for _, route := range []struct{ method, path, what string }{
		{fiber.MethodPost, "/api/v1/auth/logout-all", "ends every session of the account"},
		{fiber.MethodDelete, "/api/v1/auth/sessions/:id", "ends one session of the account, so a loop of it is logout-all one device at a time"},
	} {
		if authLimitedPaths[limiterPathOf(route.path)] {
			t.Errorf("%s is in authLimitedPaths: anyone could drain the bucket it would share with login, and with it the owner's way "+
				"to end a stolen session — it is capped per user on its route instead", route.path)
		}
		if e := declaredEndpoint(t, route.method, route.path); e.RateLimiter == nil {
			t.Errorf("%s %s declares no RateLimiter: it %s, so a loop of it keeps the owner signed out, and nothing caps it per user",
				route.method, route.path, route.what)
		}
	}

	if refreshLimitedPath != "/api/v1/auth/refresh" {
		t.Errorf("refreshLimitedPath = %q; the refresh cap exists to bound cookie replay", refreshLimitedPath)
	}
	if wsTokenLimitedPath != "/api/v1/auth/ws-token" {
		t.Errorf("wsTokenLimitedPath = %q; the ws-token cap exists to bound mint loops", wsTokenLimitedPath)
	}
}
