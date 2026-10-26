package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/auth"
)

// RefreshCookieName is the cookie name carrying the long-lived refresh token.
// This is now the ONLY delivery path: the cookie is HttpOnly + Secure +
// SameSite=Strict, so the refresh token never reaches JavaScript /
// localStorage. The native app that used to receive it in the response body
// (it had no DOM cookie jar) was removed in v1.9.x.
const RefreshCookieName = "nexara_refresh_token"

// RefreshCookiePath scopes the cookie to children of the auth subtree so it
// is only sent on /api/v1/auth/* requests, minimising exposure. The trailing
// slash matters: per RFC 6265 §5.1.4, "/api/v1/auth" without a trailing
// slash would also match a hypothetical neighbour like "/api/v1/auth-debug",
// so we keep the trailing slash to constrain the cookie to legitimate
// children only.
//
// Exported because it is a security boundary two packages have to agree on:
// internal/api excludes exactly this subtree from response compression (the
// BREACH precondition is "a credential the browser attaches by itself", which
// is what this Path defines) and from the general rate limiter. Those used to
// be a hand-copied literal kept in step by a comment; they now derive from
// this constant, so the agreement is mechanical rather than remembered.
//
// The dependency runs handlers -> internal/api and not the other way because
// internal/api already imports this package; the reverse would be an import
// cycle. See authCookieScopePrefix in internal/api/middleware.go.
const RefreshCookiePath = "/api/v1/auth/"

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
		Path:     RefreshCookiePath,
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
		Path:     RefreshCookiePath,
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

// browserBinding is a cookie that ties a step of the SSO sign-in to the browser that
// took the step before it: the login-CSRF defence of the OIDC flow. Its value is what
// the next step is handed in its URL or body — the state, the exchange code — and that
// step refuses a value this browser was not given. Without it, anyone could start a
// sign-in of their own, sign in at the identity provider as themselves, and send the
// callback link it ends in, or the /oidc-callback link with its exchange code, to
// somebody else: their browser finished the sign-in and was signed in as the attacker,
// and out of their own session, which a sign-in ends when it replaces its cookie.
//
// HttpOnly, Secure as the refresh cookie is (refreshCookieSecure), and SameSite=Lax,
// not Strict: the callback is a top-level navigation from the identity provider's site,
// a cross-site request a Strict cookie is not sent on. Its path is the one route that
// reads it, under RefreshCookiePath's subtree like every auth cookie (the callback's
// follows the redirect URI, see oidcCallbackCookiePath). Its lifetime is a Max-Age,
// which the browser counts from when it got the cookie: an Expires is judged by the
// browser's own clock, and a lab laptop a minute ahead of the server would drop the
// exchange's binding on arrival. A response that sets or spends one is marked
// no-store, so that no shared cache hands one browser's binding to another. And the
// cookie's name follows the value (cookieName), so that two sign-ins in one browser —
// two tabs, a second install on another port of the host — seldom overwrite each other's.
type browserBinding struct {
	prefix, path string
	ttl          time.Duration
}

var (
	// oidcStateBinding is set by Authorize and read by Callback, and lives as long as
	// the state it binds does in Redis.
	oidcStateBinding = browserBinding{prefix: "nexara_oidc_state", path: oidcCallbackPath, ttl: auth.OIDCStateTTL}
	// oidcExchangeBinding is set with the exchange code (provisionAndStoreExchange)
	// and read by the exchange (AuthHandler.OIDCTokenExchange).
	oidcExchangeBinding = browserBinding{prefix: "nexara_oidc_exchange", path: "/api/v1/auth/oidc/token-exchange", ttl: time.Minute}
)

// bindingSlots is how many cookie names a binding spreads its values over: enough that
// two sign-ins at once in one browser seldom share one (1 in 16, and then the newer
// replaces the older, whose sign-in is refused and started again), and few enough that
// a page which keeps sending the browser to Authorize — a popup it navigates, within
// the auth rate limit — leaves at most 16 cookies, about 1 KB. A name per value let it
// pile up about 150 in the state's 10 minutes: a Cookie header on the callback past
// nginx's default 8 KB header line, and near the 180 cookies a browser keeps for a site.
const bindingSlots = 16

// cookieName is the binding's cookie for value: one of bindingSlots names, picked by a
// digest of the value, never the value itself. The value is still compared (matches):
// the name only picks the cookie out.
func (b browserBinding) cookieName(value string) string {
	sum := sha256.Sum256([]byte(value))
	return b.prefix + "_" + strconv.FormatUint(uint64(sum[0]%bindingSlots), 16)
}

// at is the binding with its cookie at path instead of its own.
func (b browserBinding) at(path string) browserBinding {
	b.path = path
	return b
}

// set gives the browser the binding for value.
func (b browserBinding) set(c fiber.Ctx, value string) {
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Cookie(&fiber.Cookie{
		Name:     b.cookieName(value),
		Value:    value,
		Path:     b.path,
		HTTPOnly: true,
		Secure:   refreshCookieSecure(c),
		SameSite: "Lax",
		MaxAge:   int(b.ttl / time.Second),
	})
}

// spend expires the binding for value — a binding is good for one step — with the
// attributes it was set with, which the browser needs to accept the deletion.
func (b browserBinding) spend(c fiber.Ctx, value string) {
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Cookie(&fiber.Cookie{
		Name:     b.cookieName(value),
		Value:    "",
		Path:     b.path,
		HTTPOnly: true,
		Secure:   refreshCookieSecure(c),
		SameSite: "Lax",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// matches reports whether the request carries a binding that holds value: a cookie
// that is missing, or holds another value, is no match, and nor is an empty value.
// Every cookie of the name is looked at, not only the first: a stray one planted
// under another path or domain, which the browser would send first, must not lock
// the real one out.
func (b browserBinding) matches(c fiber.Ctx, value string) bool {
	if value == "" {
		return false
	}
	name := b.cookieName(value)
	for held, got := range c.Request().Header.Cookies() {
		if string(held) == name && len(got) > 0 && subtle.ConstantTimeCompare(got, []byte(value)) == 1 {
			return true
		}
	}
	return false
}

// oidcCallbackCookiePath is the path the browser comes back to from the identity
// provider: the redirect URI's, which a reverse proxy may have put under a prefix
// (validateOIDCRedirectURI allows one), or the route's own when it cannot be read.
func oidcCallbackCookiePath(redirectURI string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return oidcCallbackPath
	}
	if path := strings.TrimSuffix(u.EscapedPath(), "/"); path != "" {
		return path
	}
	return oidcCallbackPath
}
