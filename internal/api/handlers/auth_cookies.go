package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
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
//   - "auto" (default): Secure when the request is HTTPS: c.Scheme(), which is
//     https for TLS Nexara ends itself, and honors X-Forwarded-Proto only from a
//     TRUSTED_PROXIES upstream. The flag is downgraded on plain-HTTP so homelab
//     IP-only deploys don't have the browser silently drop the cookie
//     ("Set-Cookie disappears" is a worse failure mode). Not c.Protocol(): in
//     Fiber v3 that is the HTTP version ("HTTP/1.1"), which left auto never Secure.
func refreshCookieSecure(c fiber.Ctx) bool {
	switch cookieSecureMode {
	case "always":
		return true
	case "never":
		return false
	default:
		return c.Scheme() == "https"
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
// took the step before it: the login-CSRF defence of the OIDC flow. Without it, anyone
// could start a sign-in of their own, sign in at the identity provider as themselves,
// and send the callback link it ends in, or the /oidc-callback link with its exchange
// code, to somebody else: their browser finished the sign-in and was signed in as the
// attacker, and out of their own session, which a sign-in ends when it replaces its
// cookie.
//
// The cookie holds a random secret (newBinding), and the server keeps only its
// SHA-256, in a binding record, beside the value the next step is handed in its URL or
// body — the state, the exchange code. That step refuses a value whose stored digest the browser's cookie
// does not hash to. The value alone is not enough: a callback or /oidc-callback URL
// that leaks to a log cannot be redeemed by setting the cookie by hand.
//
// HttpOnly, Secure as the configuration decides (oidcBindingSecure) — never the
// request, so that an install whose redirect URI is https never reads an unprefixed
// cookie a plain-http request carries — and SameSite=Lax,
// not Strict: the callback is a top-level navigation from the identity provider's site,
// a cross-site request a Strict cookie is not sent on. When Secure, its name carries the
// __Host- prefix, which the browser accepts only from a secure origin, with Path=/ and
// no Domain: a sibling subdomain or a plain-http page cannot plant one. Without Secure
// (a plain-http install) it keeps an unprefixed name and the path of the one route that
// reads it (the callback's follows the redirect URI, see oidcCallbackCookiePath). Its
// lifetime is a Max-Age, which the browser counts from when it got the cookie: an
// Expires is judged by the browser's own clock, and a lab laptop a minute ahead of the
// server would drop the exchange's binding on arrival. A response that sets or spends
// one is marked no-store, so that no shared cache hands one browser's binding to
// another. And the cookie's name follows the value (cookieName), so that two sign-ins
// in one browser — two tabs, a second install on another port of the host — seldom
// overwrite each other's.
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

// hostCookiePrefix is the prefix a browser accepts only on a Secure cookie with Path=/
// and no Domain, set from a secure origin.
const hostCookiePrefix = "__Host-"

// oidcBindingSecure is whether the SSO bindings are Secure, __Host- cookies, decided
// from configuration alone: SECURE_COOKIES=always, or auto with an https redirect URI
// (the address the browser comes back to). Not from the request: whoever can set a
// cookie for the host — a sibling subdomain, a man in the middle of a plain-http
// request — could otherwise plant an unprefixed binding of their own and have it read.
func oidcBindingSecure(redirectURI string) bool {
	switch cookieSecureMode {
	case "always":
		return true
	case "never":
		return false
	default:
		u, err := url.Parse(redirectURI)
		return err == nil && strings.EqualFold(u.Scheme, "https")
	}
}

// newBinding is a binding's secret, for the browser's cookie, and its record, for the
// server to keep beside the value it binds (bindingRecord).
func newBinding(secure bool) (secret, record string, err error) {
	secret, err = auth.GenerateRandomString(32)
	if err != nil {
		return "", "", fmt.Errorf("generating a browser binding: %w", err)
	}
	return secret, bindingRecord(secure, secret), nil
}

// bindingRecord is what the server keeps of a binding: whether its cookie is Secure,
// as the configuration decided when it was set, and the digest of its secret.
func bindingRecord(secure bool, secret string) string {
	if secure {
		return "1:" + bindingDigest(secret)
	}
	return "0:" + bindingDigest(secret)
}

// parseBindingRecord reads a bindingRecord; ok is false for anything else.
func parseBindingRecord(record string) (secure bool, digest string, ok bool) {
	flag, digest, found := strings.Cut(record, ":")
	if !found || digest == "" || flag != "0" && flag != "1" {
		return false, "", false
	}
	return flag == "1", digest, true
}

// bindingDigest is the SHA-256 of a binding's secret, hex: what the server keeps.
func bindingDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// cookieName is the binding's cookie for value: one of bindingSlots names, picked by a
// digest of the value, never the value itself, and __Host- prefixed when the cookie is
// Secure. The secret is still compared (matches): the name only picks the cookie out.
func (b browserBinding) cookieName(secure bool, value string) string {
	sum := sha256.Sum256([]byte(value))
	name := b.prefix + "_" + strconv.FormatUint(uint64(sum[0]%bindingSlots), 16)
	if secure {
		return hostCookiePrefix + name
	}
	return name
}

// cookiePath is where the binding's cookie lives: / for a __Host- cookie, which the
// prefix requires, its route's path otherwise.
func (b browserBinding) cookiePath(secure bool) string {
	if secure {
		return "/"
	}
	return b.path
}

// at is the binding with its cookie at path instead of its own.
func (b browserBinding) at(path string) browserBinding {
	b.path = path
	return b
}

// set gives the browser the binding for value: its secret, in a cookie, Secure as the
// configuration decided (oidcBindingSecure).
func (b browserBinding) set(c fiber.Ctx, value, secret string, secure bool) {
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Cookie(&fiber.Cookie{
		Name:     b.cookieName(secure, value),
		Value:    secret,
		Path:     b.cookiePath(secure),
		HTTPOnly: true,
		Secure:   secure,
		SameSite: "Lax",
		MaxAge:   int(b.ttl / time.Second),
	})
}

// spend expires the binding for value — a binding is good for one step — with the
// attributes it was set with — record says which — which the browser needs to accept
// the deletion.
func (b browserBinding) spend(c fiber.Ctx, value, record string) {
	secure, _, _ := parseBindingRecord(record)
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Cookie(&fiber.Cookie{
		Name:     b.cookieName(secure, value),
		Value:    "",
		Path:     b.cookiePath(secure),
		HTTPOnly: true,
		Secure:   secure,
		SameSite: "Lax",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// matches reports whether the request carries a binding for value whose secret hashes
// to the digest in record, the one the server kept for value: a cookie that is missing,
// or holds another secret, is no match, and nor is an empty value or an unreadable
// record. Only the name the record's Secure setting gives is read, so an unprefixed
// cookie never stands in for a __Host- one, whatever the request. Every cookie of the
// name is looked at, not only the first: a stray one planted under another path, which
// the browser would send first, must not lock the real one out.
func (b browserBinding) matches(c fiber.Ctx, value, record string) bool {
	secure, digest, ok := parseBindingRecord(record)
	if value == "" || !ok {
		return false
	}
	name := b.cookieName(secure, value)
	for held, got := range c.Request().Header.Cookies() {
		if string(held) == name && len(got) > 0 &&
			subtle.ConstantTimeCompare([]byte(bindingDigest(string(got))), []byte(digest)) == 1 {
			return true
		}
	}
	return false
}

// The SSO step-up's proof is bound to the browser that made the round trip by a cookie
// the callback sets (finishStepUp) and the setup reads (useSSOStepUp): HttpOnly,
// SameSite=Strict (the setup is a same-site request from the SPA), __Host- at / when
// Secure, otherwise unprefixed and sent only to the setup routes. The proof keeps only
// its record (bindingRecord), so the session's token alone, or a cookie planted
// unprefixed on a Secure install, cannot use it.
const (
	totpStepUpCookieName = "nexara_totp_step_up"
	totpStepUpCookiePath = "/api/v1/auth/totp/setup"
)

func totpStepUpCookie(secure bool) (name, path string) {
	if secure {
		return hostCookiePrefix + totpStepUpCookieName, "/"
	}
	return totpStepUpCookieName, totpStepUpCookiePath
}

// setTOTPStepUpCookie gives the browser the secret its proof is bound to.
func setTOTPStepUpCookie(c fiber.Ctx, secret string, secure bool, ttl time.Duration) {
	name, path := totpStepUpCookie(secure)
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Cookie(&fiber.Cookie{Name: name, Value: secret, Path: path, HTTPOnly: true, Secure: secure, SameSite: "Strict",
		MaxAge: int(ttl / time.Second)})
}

// totpStepUpCookieRecords are the binding records of every step-up cookie the request
// carries, each under the Secure flag its name says.
func totpStepUpCookieRecords(c fiber.Ctx) []string {
	var records []string
	for held, got := range c.Request().Header.Cookies() {
		if len(got) == 0 {
			continue
		}
		switch string(held) {
		case hostCookiePrefix + totpStepUpCookieName:
			records = append(records, bindingRecord(true, string(got)))
		case totpStepUpCookieName:
			records = append(records, bindingRecord(false, string(got)))
		}
	}
	return records
}

// clearTOTPStepUpCookie expires the step-up cookie, under both names, once its proof is
// used.
func clearTOTPStepUpCookie(c fiber.Ctx) {
	for _, secure := range []bool{true, false} {
		name, path := totpStepUpCookie(secure)
		c.Cookie(&fiber.Cookie{Name: name, Value: "", Path: path, HTTPOnly: true, Secure: secure, SameSite: "Strict",
			Expires: time.Unix(0, 0), MaxAge: -1})
	}
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
