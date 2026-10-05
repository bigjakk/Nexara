package api

import (
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// The three auth-facing rate limiters — the 15/min login-and-TOTP cap, the
// 30/min refresh cap and the 60/min ws-token cap — are mounted app-wide with
// Use and select their traffic by PATH (see authLimitedPaths in middleware.go).
// Nothing about a route's declaration mentions the cap that protects it, and
// nothing about the cap mentions the route: they agree only by spelling.
//
// That is a quiet failure mode, and one this migration ran directly at.
// Declaring a route in the registry re-spells its path in a second place, and
// getting it wrong — /auth/totp/ instead of /auth/totp, or a rename — takes the
// brute-force cap off a credential endpoint while the route keeps working
// perfectly. No existing guard would notice: the limiter simply stops matching.
//
// This turns the agreement into a build failure in both directions: every
// limited path must name a route the server actually mounts, and the paths a
// limiter is written for must not quietly lose their cap.

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
	s := newRouteStubServer(t)

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

// TestGuard_CredentialRoutesKeepTheirRateLimit is the other direction, and the
// one that matters more.
//
// The routes below all accept a credential or mint a token, and each was given a
// per-IP cap for a stated reason: brute-forcing a password, grinding a six-digit
// TOTP code, replaying a refresh cookie, or looping the unauthenticated OIDC flow
// to pin the server on outbound HTTP. Dropping one from the limiter's path set is
// a one-line edit with no other symptom, so the set is pinned here rather than
// left to review.
//
// This deliberately restates the list rather than deriving it: a guard that
// reads the same map it is checking would pass whatever that map said.
//
// NOT in the list, and deliberately so — recorded here because the general
// limiter skips everything under /api/v1/auth/, so a path's absence from
// authLimitedPaths means it has NO per-IP cap at all, and an omission nobody
// wrote down is an omission nobody re-reads:
//
//   - /api/v1/auth/logout revokes the one session whose cookie the caller
//     already holds; there is nothing to guess, and nothing to loop on — the
//     cookie is dead after the first call. (/api/v1/auth/logout-all and
//     DELETE /api/v1/auth/sessions/:id are the other case: they end the
//     sessions of the account, so a loop of either keeps the owner signed out.
//     Each is capped per USER on its own route, and both are held OUT of this
//     set on purpose — see below.)
//   - /api/v1/auth/setup-status and /api/v1/auth/sso-status are anonymous but
//     answer with one boolean (plus, for SSO, a display name), from a single
//     indexed read.
//   - /api/v1/auth/oidc/token-exchange consumes a 5-second single-use Redis
//     key with GETDEL, so a replay finds nothing and there is no budget to
//     grind.
//   - /api/v1/auth/totp/setup/verify and /api/v1/auth/console-token are the
//     two worth revisiting: the first validates an enrolment code, bounded
//     only by its own 5-attempt Redis budget, and the second is Deferred, so
//     its body is decoded before the permission check runs. Both are
//     unchanged from before the registry migration.
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
