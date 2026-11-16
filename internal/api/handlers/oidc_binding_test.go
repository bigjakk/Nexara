package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
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
// browser holds no binding for — no cookie whose secret hashes to the digest stored with
// the state — is refused before anything else is looked at. The state itself in the
// cookie, which a leaked callback URL gives away, is no binding; a stray cookie of the
// name sent ahead of the real one does not lock it out; the binding is spent at the
// redirect URI's path.
func TestOIDCCallback_RefusesASignInThisBrowserDidNotStart(t *testing.T) {
	t.Parallel()
	const state, secret = "state-this-browser-started", "secret-this-browser-holds"
	bound := oidcStateBinding.cookieName(false, state)
	stored := "nonce|verifier|" + bindingRecord(false, secret)
	for _, tc := range []struct {
		name, cookie, stored string
		passes               bool
	}{
		{"no binding", "", stored, false},
		{"another sign-in's binding", oidcStateBinding.cookieName(false, "another-state") + "=" + secret, stored, false},
		{"this sign-in's cookie holding another secret", bound + "=another-secret", stored, false},
		{"this sign-in's cookie holding the state", bound + "=" + state, stored, false},
		{"this sign-in's binding, its state gone", bound + "=" + secret, "", false},
		{"a state stored before the binding was (an upgrade)", bound + "=" + secret, "nonce|verifier", false},
		{"a Secure binding, the unprefixed cookie only", bound + "=" + secret, "nonce|verifier|" + bindingRecord(true, secret), false},
		{"this sign-in's binding", bound + "=" + secret, stored, true},
		{"this sign-in's binding behind a stray one", bound + "=stray; " + bound + "=" + secret, stored, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newEpochStore()
			// A proxy's prefix on the redirect URI, where the browser's cookie lives; an
			// undecryptable secret stops the callback right after the binding is spent.
			store.oidcConfig = &db.OidcConfig{RedirectUri: "https://nexara.example.com/nexara/api/v1/auth/oidc/callback",
				ClientSecretEncrypted: "not-a-ciphertext"}
			a := newEpochApp(t, store, epochOptions{})
			if tc.stored != "" {
				if err := a.redis.Set("oidc:state:"+state, tc.stored); err != nil {
					t.Fatal(err)
				}
			}
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

// TestOIDCCallback_RedisDownIsNotAnotherBrowser: a state that could not be read is
// answered "try again", not blamed on the browser.
func TestOIDCCallback_RedisDownIsNotAnotherBrowser(t *testing.T) {
	t.Parallel()
	a := newEpochApp(t, newEpochStore(), epochOptions{})
	a.redis.Close()

	resp := a.send(t, http.MethodGet, "/auth/oidc/callback?code=idp-code&state=some-state", "", nil, 10*time.Second)

	if got := resp.Header.Get("Location"); got != "/login?error="+url.QueryEscape(oidcCouldNotCompleteMessage) {
		t.Errorf("redirected to %q, want the could-not-complete refusal", got)
	}
}

// TestOIDCExchange_RefusesACodeThisBrowserWasNotGiven: the exchange code the callback
// stores comes with its binding, a secret the code's data holds only the digest of, and
// the exchange refuses — before it spends the code — one the browser trading it was not
// given, the code itself in the cookie included.
func TestOIDCExchange_RefusesACodeThisBrowserWasNotGiven(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		cookie func(given *http.Cookie, code string) string
		status int
	}{
		{"no binding", func(*http.Cookie, string) string { return "" }, http.StatusUnauthorized},
		{"another code's binding", func(*http.Cookie, string) string {
			return oidcExchangeBinding.cookieName(false, "another-code") + "=another-secret"
		}, http.StatusUnauthorized},
		{"the code in the binding's cookie", func(given *http.Cookie, code string) string { return given.Name + "=" + code }, http.StatusUnauthorized},
		{"the binding the callback gave", func(given *http.Cookie, _ string) string { return given.Name + "=" + given.Value }, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := epochUser(t, epochLoginEmail, 0)
			u.AuthSource, u.PasswordHash = "oidc", ""
			a := newEpochApp(t, newEpochStore(u), epochOptions{})
			var code string
			a.probe = func(c fiber.Ctx) error {
				_, code, _ = a.oidc.provisionAndStoreExchange(c, db.OidcConfig{}, &auth.OIDCUserInfo{Issuer: epochIssuer, Subject: epochSubject, Email: u.Email, DisplayName: u.DisplayName})
				return c.SendStatus(http.StatusNoContent)
			}
			probe := a.send(t, http.MethodGet, "/probe", "", nil, 10*time.Second)
			given := cookiesNamed(probe, oidcExchangeBinding.cookieName(false, code))
			if code == "" || len(given) != 1 || given[0].Value == "" || given[0].Value == code {
				t.Fatalf("the callback stored code %q and set bindings %v: want one holding a secret", code, given)
			}

			headers := map[string]string{}
			if cookie := tc.cookie(given[0], code); cookie != "" {
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
		app.Get("/set", func(c fiber.Ctx) error {
			tc.binding.set(c, "value", "secret", false)
			return c.SendStatus(http.StatusNoContent)
		})
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/set", nil))
		if err != nil {
			t.Fatalf("%s: %v", tc.binding.prefix, err)
		}
		got := cookiesNamed(resp, tc.binding.cookieName(false, "value"))
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
			names[tc.binding.cookieName(false, strconv.Itoa(i))] = true
		}
		if len(names) != 16 {
			t.Errorf("%s: 1000 values got %d cookie names, want 16", tc.binding.prefix, len(names))
		}
	}
}

// TestOIDCBrowserBindings_AreHostPrefixedWhenSecure: a Secure binding is a __Host-
// cookie at / (which the prefix requires), and only that name is read, on a plain-http
// request too, so the same secret under the unprefixed name — which a sibling subdomain
// or a plain-http man in the middle can plant — is no binding.
func TestOIDCBrowserBindings_AreHostPrefixedWhenSecure(t *testing.T) {
	t.Parallel()
	record := bindingRecord(true, "secret")
	app := fiber.New()
	app.Get("/set", func(c fiber.Ctx) error {
		oidcStateBinding.set(c, "value", "secret", true)
		return c.SendStatus(http.StatusNoContent)
	})
	app.Get("/match", func(c fiber.Ctx) error {
		return c.SendString(strconv.FormatBool(oidcStateBinding.matches(c, "value", record)))
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/set", nil))
	if err != nil {
		t.Fatal(err)
	}
	name := oidcStateBinding.cookieName(true, "value")
	got := cookiesNamed(resp, name)
	if !strings.HasPrefix(name, "__Host-") || len(got) != 1 || got[0].Path != "/" || !got[0].Secure || got[0].Domain != "" {
		t.Fatalf("Set-Cookie %s = %+v, want one __Host- cookie, Secure, at /, with no Domain", name, got)
	}
	for cookie, want := range map[string]string{
		name + "=secret": "true",
		oidcStateBinding.cookieName(false, "value") + "=secret": "false",
	} {
		req := httptest.NewRequest(http.MethodGet, "/match", nil)
		req.Header.Set("Cookie", cookie)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != want {
			t.Errorf("matches with %q = %s, want %s", cookie, body, want)
		}
	}

	// Spending clears the cookie that was set: the __Host- one at / for a Secure
	// record, the unprefixed one at its route otherwise.
	for secure, path := range map[bool]string{true: "/", false: oidcCallbackPath} {
		app := fiber.New()
		app.Get("/spend", func(c fiber.Ctx) error {
			oidcStateBinding.spend(c, "value", bindingRecord(secure, "secret"))
			return c.SendStatus(http.StatusNoContent)
		})
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/spend", nil))
		if err != nil {
			t.Fatal(err)
		}
		got := cookiesNamed(resp, oidcStateBinding.cookieName(secure, "value"))
		if len(got) != 1 || got[0].Path != path || got[0].Secure != secure || got[0].MaxAge >= 0 {
			t.Errorf("spend, Secure %v: Set-Cookie %+v, want one at %s, Secure %v, a negative Max-Age", secure, got, path, secure)
		}
	}
}

// TestOIDCBindingSecure_IsTheRedirectURIsUnderAuto: under SECURE_COOKIES=auto the
// bindings are Secure exactly when the redirect URI is https, whatever the request: the
// exchange binding a plain-http request is handed for an https install is __Host-.
func TestOIDCBindingSecure_IsTheRedirectURIsUnderAuto(t *testing.T) {
	t.Parallel()
	for uri, want := range map[string]bool{
		"https://nexara.example.com/api/v1/auth/oidc/callback": true,
		"http://nexara.example.com/api/v1/auth/oidc/callback":  false,
	} {
		if got := oidcBindingSecure(uri); got != want {
			t.Errorf("oidcBindingSecure(%q) = %v, want %v", uri, got, want)
		}
	}
	u := epochUser(t, epochLoginEmail, 0)
	u.AuthSource, u.PasswordHash = "oidc", ""
	a := newEpochApp(t, newEpochStore(u), epochOptions{})
	var code string
	a.probe = func(c fiber.Ctx) error {
		_, code, _ = a.oidc.provisionAndStoreExchange(c,
			db.OidcConfig{RedirectUri: "https://nexara.example.com/api/v1/auth/oidc/callback"},
			&auth.OIDCUserInfo{Issuer: epochIssuer, Subject: epochSubject, Email: u.Email, DisplayName: u.DisplayName})
		return c.SendStatus(http.StatusNoContent)
	}
	probe := a.send(t, http.MethodGet, "/probe", "", nil, 10*time.Second)
	if got := cookiesNamed(probe, oidcExchangeBinding.cookieName(true, code)); len(got) != 1 || !got[0].Secure {
		t.Errorf("the exchange binding for an https install over plain http is %+v, want one Secure __Host- cookie", got)
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

// TestOIDCCallback_AnUnverifiedEmailIsAFailedSignIn: the login page is told to verify
// the address with the provider, and the refusal is audited as a failed sign-in: under
// the SSO account the address names, each time; or under none, with no address (audit
// details are Viewer-readable), once a minute per client address, as the password path.
func TestOIDCCallback_AnUnverifiedEmailIsAFailedSignIn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		account bool
		action  string
		rows    int
	}{
		{"an existing account", true, "login_failed", 2},
		{"no account yet", false, "login_failed_unknown_account", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := epochUser(t, epochLoginEmail, 0)
			u.AuthSource = "oidc"
			store := newEpochStore()
			if tc.account {
				store = newEpochStore(u)
			}
			a := newEpochApp(t, store, epochOptions{})
			var message string
			a.probe = func(c fiber.Ctx) error {
				message = a.oidc.exchangeFailure(c, &auth.OIDCEmailNotVerifiedError{Email: epochLoginEmail})
				return c.SendStatus(http.StatusNoContent)
			}
			a.send(t, http.MethodGet, "/probe", "", nil, 10*time.Second)
			a.send(t, http.MethodGet, "/probe", "", nil, 10*time.Second)

			if message != oidcEmailNotVerifiedMessage {
				t.Errorf("message %q, want the unverified-email one", message)
			}
			rows := store.auditRowsWritten()
			if len(rows) != tc.rows || rows[0].action != tc.action || (rows[0].actor == u.ID) != tc.account {
				t.Fatalf("audit rows %v, want %d %s (under the account: %v)", rows, tc.rows, tc.action, tc.account)
			}
			d := store.auditDetailsWritten()[0]
			if !strings.Contains(d, `"reason":"email_not_verified"`) || strings.Contains(d, `"email"`) == !tc.account {
				t.Errorf("audit details %s, want the reason, and the address only under the account", d)
			}
		})
	}
}

// TestUnknownSignInDetails_EachMethodHasItsOwnBound: a password failure and an SSO
// failure from one address in one minute each get their own row, labelled with their
// own method, so one method cannot take the other's row.
func TestUnknownSignInDetails_EachMethodHasItsOwnBound(t *testing.T) {
	t.Parallel()
	a := newEpochApp(t, newEpochStore(), epochOptions{})
	var got []string
	a.probe = func(c fiber.Ctx) error {
		for _, method := range []string{"oidc", "password", "oidc"} {
			if details, audit := unknownSignInDetails(c, a.rdb, method, "unknown_account"); audit {
				got = append(got, string(details))
			}
		}
		return c.SendStatus(http.StatusNoContent)
	}
	a.send(t, http.MethodGet, "/probe", "", nil, 10*time.Second)

	if len(got) != 2 || !strings.Contains(got[0], `"method":"oidc"`) || !strings.Contains(got[1], `"method":"password"`) {
		t.Errorf("rows %v, want one for oidc and one for password", got)
	}
}
