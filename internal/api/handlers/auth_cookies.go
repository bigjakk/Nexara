package handlers

import (
	"time"

	"github.com/gofiber/fiber/v3"
)

// RefreshCookieName is the cookie name carrying the long-lived refresh token.
// This is now the ONLY delivery path: the cookie is HttpOnly + Secure +
// SameSite=Strict, so the refresh token never reaches JavaScript /
// localStorage. The native app that used to receive it in the response body
// (it had no DOM cookie jar) was removed in v1.9.x.
const RefreshCookieName = "nexara_refresh_token"

// refreshCookiePath scopes the cookie to children of the auth subtree so it
// is only sent on /api/v1/auth/* requests, minimising exposure. The trailing
// slash matters: per RFC 6265 §5.1.4, "/api/v1/auth" without a trailing
// slash would also match a hypothetical neighbour like "/api/v1/auth-debug",
// so we keep the trailing slash to constrain the cookie to legitimate
// children only.
const refreshCookiePath = "/api/v1/auth/"

// cookieSecureMode is the SECURE_COOKIES policy, set once at startup via
// SetCookieSecureMode. See refreshCookieSecure.
var cookieSecureMode = "auto"

// SetCookieSecureMode configures the refresh-cookie Secure policy from the
// SECURE_COOKIES setting. Called once during server setup, before serving.
func SetCookieSecureMode(mode string) {
	switch mode {
	case "always", "never", "auto":
		cookieSecureMode = mode
	default:
		cookieSecureMode = "auto"
	}
}

// refreshCookieSecure decides the Secure flag for the refresh cookie:
//   - "always": always Secure — for TLS deployments behind a reverse proxy
//     where scheme detection (X-Forwarded-Proto) is unreliable.
//   - "never": never Secure — explicit opt-in for plain-HTTP lab use.
//   - "auto" (default): Secure when the request is HTTPS. c.Protocol() honors
//     X-Forwarded-Proto only from a TRUSTED_PROXIES upstream, and the flag is
//     downgraded on plain-HTTP so homelab IP-only deploys don't have the browser
//     silently drop the cookie ("Set-Cookie disappears" is a worse failure mode).
func refreshCookieSecure(c fiber.Ctx) bool {
	switch cookieSecureMode {
	case "always":
		return true
	case "never":
		return false
	default:
		return c.Protocol() == "https"
	}
}

// setRefreshCookie installs the refresh-token cookie with HttpOnly, Secure (per
// SECURE_COOKIES, see refreshCookieSecure), SameSite=Strict, and a path scoped
// to the auth endpoints.
func setRefreshCookie(c fiber.Ctx, token string, ttl time.Duration) {
	c.Cookie(&fiber.Cookie{
		Name:     RefreshCookieName,
		Value:    token,
		Path:     refreshCookiePath,
		HTTPOnly: true,
		Secure:   refreshCookieSecure(c),
		SameSite: "Strict",
		Expires:  time.Now().Add(ttl),
	})
}

// clearRefreshCookie expires the refresh-token cookie immediately. The
// attributes (Path/Domain/Secure/SameSite) must match the original Set-Cookie
// for the browser to accept the deletion, so Secure tracks setRefreshCookie.
func clearRefreshCookie(c fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		HTTPOnly: true,
		Secure:   refreshCookieSecure(c),
		SameSite: "Strict",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// readRefreshTokenFromCookie returns the refresh token stored in the request
// cookie, or "" if the cookie is absent.
func readRefreshTokenFromCookie(c fiber.Ctx) string {
	return c.Cookies(RefreshCookieName)
}
