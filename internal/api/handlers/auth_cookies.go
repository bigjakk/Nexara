package handlers

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// RefreshCookieName is the cookie name carrying the long-lived refresh token
// for browser clients. Web clients receive only this cookie (HttpOnly + Secure
// + SameSite=Strict); the refresh token never reaches JavaScript / localStorage.
const RefreshCookieName = "nexara_refresh_token"

// refreshCookiePath scopes the cookie to children of the auth subtree so it
// is only sent on /api/v1/auth/* requests, minimising exposure. The trailing
// slash matters: per RFC 6265 §5.1.4, "/api/v1/auth" without a trailing
// slash would also match a hypothetical neighbour like "/api/v1/auth-debug",
// so we keep the trailing slash to constrain the cookie to legitimate
// children only.
const refreshCookiePath = "/api/v1/auth/"

// setRefreshCookie installs the refresh-token cookie with HttpOnly, Secure
// (when the request is HTTPS), SameSite=Strict, and a path scoped to the auth
// endpoints. The Secure flag is downgraded on plain-HTTP requests so homelab
// IP-only deploys don't have the browser silently drop the cookie — those
// users have already accepted the risk by not fronting Nexara with TLS, and
// "Set-Cookie disappears" is a worse failure mode than "cookie not Secure".
// `c.Protocol()` honors X-Forwarded-Proto only when the upstream is in
// TRUSTED_PROXIES, so a TLS-terminating proxy with that config set still
// gets a Secure cookie.
func setRefreshCookie(c *fiber.Ctx, token string, ttl time.Duration) {
	c.Cookie(&fiber.Cookie{
		Name:     RefreshCookieName,
		Value:    token,
		Path:     refreshCookiePath,
		HTTPOnly: true,
		Secure:   c.Protocol() == "https",
		SameSite: "Strict",
		Expires:  time.Now().Add(ttl),
	})
}

// clearRefreshCookie expires the refresh-token cookie immediately. The
// attributes (Path/Domain/Secure/SameSite) must match the original Set-Cookie
// for the browser to accept the deletion, so Secure tracks the request scheme
// the same way as setRefreshCookie.
func clearRefreshCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		HTTPOnly: true,
		Secure:   c.Protocol() == "https",
		SameSite: "Strict",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// readRefreshTokenFromCookie returns the refresh token stored in the request
// cookie, or "" if the cookie is absent.
func readRefreshTokenFromCookie(c *fiber.Ctx) string {
	return c.Cookies(RefreshCookieName)
}

// isMobileClient reports whether the request comes from the native mobile
// shell. Mobile clients have no DOM cookie jar and instead store the refresh
// token in SecureStore, so the response keeps the refresh token in the body
// for them.
func isMobileClient(c *fiber.Ctx) bool {
	return strings.EqualFold(strings.TrimSpace(c.Get("X-Nexara-Device-Type")), "mobile")
}
