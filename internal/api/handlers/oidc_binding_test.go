package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The SSO sign-in is bound to the browser that starts it (browserBinding): anyone
// can sign in at the identity provider as themselves and send the link the sign-in
// ends in to somebody else, whose browser would otherwise finish it.

// TestOIDCCallback_RefusesASignInThisBrowserDidNotStart: a callback whose state the
// browser holds no binding for is refused before anything else is looked at — a
// stray cookie of the name sent ahead of the real one does not lock it out — and the
// browser that started it gets past, its binding spent at the redirect URI's path.
func TestOIDCCallback_RefusesASignInThisBrowserDidNotStart(t *testing.T) {
	t.Parallel()
	const state = "state-this-browser-started"
	bound := oidcStateBinding.cookieName(state)
	for _, tc := range []struct {
		name, cookie string
		passes       bool
	}{
		{"no binding", "", false},
		{"another sign-in's binding", oidcStateBinding.cookieName("another-state") + "=another-state", false},
		{"this sign-in's cookie holding another value", bound + "=another-state", false},
		{"this sign-in's binding", bound + "=" + state, true},
		{"this sign-in's binding behind a stray one", bound + "=stray; " + bound + "=" + state, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newEpochStore()
			// A proxy's prefix on the redirect URI, where the browser's cookie lives; an
			// undecryptable secret stops the callback right after the binding is spent.
			store.oidcConfig = &db.OidcConfig{RedirectUri: "https://nexara.example.com/nexara/api/v1/auth/oidc/callback",
				ClientSecretEncrypted: "not-a-ciphertext"}
			a := newEpochApp(t, store, epochOptions{})
			headers := map[string]string{}
			if tc.cookie != "" {
				headers["Cookie"] = tc.cookie
			}

			resp := a.send(t, http.MethodGet, "/auth/oidc/callback?code=idp-code&state="+state, "", headers, 10*time.Second)

			refused := resp.Header.Get("Location") == "/login?error="+url.QueryEscape(oidcNotThisBrowserMessage)
			if refused == tc.passes {
				t.Errorf("redirected to %q: refused %v, want %v", resp.Header.Get("Location"), refused, !tc.passes)
			}
			if looked := len(store.named("GetEnabledOIDCConfig")) > 0; looked != tc.passes {
				t.Errorf("the OIDC config was read: %v, want %v", looked, tc.passes)
			}
			spent := slices.ContainsFunc(cookiesNamed(resp, bound), func(c *http.Cookie) bool {
				return cookieDeleted(c) && c.Path == "/nexara/api/v1/auth/oidc/callback"
			})
			if spent != tc.passes {
				t.Errorf("the binding was spent at the redirect URI's path: %v, want %v", spent, tc.passes)
			}
			if cc := resp.Header.Get(fiber.HeaderCacheControl); tc.passes && cc != "no-store" {
				t.Errorf("the response spending the binding has Cache-Control %q, want no-store", cc)
			}
		})
	}
}

// TestOIDCExchange_RefusesACodeThisBrowserWasNotGiven: the exchange code the callback
// stores comes with its binding, and the exchange refuses — before it spends the code —
// one the browser trading it was not given.
func TestOIDCExchange_RefusesACodeThisBrowserWasNotGiven(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		cookie func(given *http.Cookie) string
		status int
	}{
		{"no binding", func(*http.Cookie) string { return "" }, http.StatusUnauthorized},
		{"another code's binding", func(*http.Cookie) string {
			return oidcExchangeBinding.cookieName("another-code") + "=another-code"
		}, http.StatusUnauthorized},
		{"the binding the callback gave", func(given *http.Cookie) string { return given.Name + "=" + given.Value }, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := epochUser(t, epochLoginEmail, 0)
			u.AuthSource, u.PasswordHash = "oidc", ""
			a := newEpochApp(t, newEpochStore(u), epochOptions{})
			var code string
			a.probe = func(c fiber.Ctx) error {
				_, code, _ = a.oidc.provisionAndStoreExchange(c, db.OidcConfig{}, &auth.OIDCUserInfo{Email: u.Email, DisplayName: u.DisplayName})
				return c.SendStatus(http.StatusNoContent)
			}
			probe := a.send(t, http.MethodGet, "/probe", "", nil, 10*time.Second)
			given := cookiesNamed(probe, oidcExchangeBinding.cookieName(code))
			if code == "" || len(given) != 1 || given[0].Value != code {
				t.Fatalf("the callback stored code %q and set bindings %v: want one holding the code", code, given)
			}

			headers := map[string]string{}
			if cookie := tc.cookie(given[0]); cookie != "" {
				headers["Cookie"] = cookie
			}
			resp := a.send(t, http.MethodPost, "/auth/oidc/token-exchange", `{"code":"`+code+`"}`, headers, 10*time.Second)

			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.status)
			}
			if kept := a.redis.Exists("oidc:exchange:" + code); kept != (tc.status != http.StatusOK) {
				t.Errorf("the code is still stored: %v, want it spent only by the browser it was given to", kept)
			}
		})
	}
}

// TestOIDCBrowserBindings_AreLaxHttpOnlyCookiesForTheirOneRoute: what a browser needs
// to send each binding back and nowhere else. Lax, not Strict: the callback is a
// cross-site navigation from the identity provider, which a Strict cookie misses; a
// Max-Age, which no clock skew can expire on arrival; no-store; and a few names.
func TestOIDCBrowserBindings_AreLaxHttpOnlyCookiesForTheirOneRoute(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		binding browserBinding
		route   string
	}{
		{oidcStateBinding, "/api/v1/auth/oidc/callback"},
		{oidcExchangeBinding, "/api/v1/auth/oidc/token-exchange"},
	} {
		app := fiber.New()
		app.Get("/set", func(c fiber.Ctx) error { tc.binding.set(c, "value"); return c.SendStatus(http.StatusNoContent) })
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/set", nil))
		if err != nil {
			t.Fatalf("%s: %v", tc.binding.prefix, err)
		}
		got := cookiesNamed(resp, tc.binding.cookieName("value"))
		if len(got) != 1 || got[0].Path != tc.route || !got[0].HttpOnly || got[0].SameSite != http.SameSiteLaxMode || got[0].MaxAge <= 0 {
			t.Errorf("%s: Set-Cookie = %+v, want one, HttpOnly, SameSite=Lax, with a Max-Age and Path %s", tc.binding.prefix, got, tc.route)
		}
		if cc := resp.Header.Get(fiber.HeaderCacheControl); cc != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", tc.binding.prefix, cc)
		}
		// Sixteen names, picked by the value (bindingSlots): two sign-ins in one browser
		// share one 1 time in 16, and a page that keeps starting sign-ins cannot pile up
		// cookies.
		names := map[string]bool{}
		for i := range 1000 {
			names[tc.binding.cookieName(strconv.Itoa(i))] = true
		}
		if len(names) != 16 {
			t.Errorf("%s: 1000 values got %d cookie names, want 16", tc.binding.prefix, len(names))
		}
	}
}

// TestOIDCCallbackCookiePath_IsTheRedirectURIs: where the browser comes back, a
// reverse proxy's prefix included, and the route's own path when the URI says none.
func TestOIDCCallbackCookiePath_IsTheRedirectURIs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ uri, want string }{
		{"https://nexara.example.com/api/v1/auth/oidc/callback", "/api/v1/auth/oidc/callback"},
		{"https://example.com/nexara/api/v1/auth/oidc/callback/", "/nexara/api/v1/auth/oidc/callback"},
		{"https://example.com/a%20b/api/v1/auth/oidc/callback", "/a%20b/api/v1/auth/oidc/callback"},
		{"https://nexara.example.com", oidcCallbackPath},
		{"::not a url", oidcCallbackPath},
	} {
		if got := oidcCallbackCookiePath(tc.uri); got != tc.want {
			t.Errorf("oidcCallbackCookiePath(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}
