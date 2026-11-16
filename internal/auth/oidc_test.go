package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"

	"github.com/bigjakk/nexara/internal/notifications"
)

// TestEmailUnverified: only a claim that says false refuses the sign-in, the string
// some providers send included; a missing claim does not.
func TestEmailUnverified(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		claims map[string]interface{}
		want   bool
	}{
		{"missing", map[string]interface{}{}, false},
		{"true", map[string]interface{}{"email_verified": true}, false},
		{"false", map[string]interface{}{"email_verified": false}, true},
		{"the string false", map[string]interface{}{"email_verified": "False"}, true},
		{"the string true", map[string]interface{}{"email_verified": "true"}, false},
		{"the number 0", map[string]interface{}{"email_verified": float64(0)}, true},
		{"the number 1", map[string]interface{}{"email_verified": float64(1)}, false},
		{"the string 0", map[string]interface{}{"email_verified": "0"}, true},
		{"the string no", map[string]interface{}{"email_verified": "No"}, true},
	} {
		if got := emailUnverified(tc.claims); got != tc.want {
			t.Errorf("%s: emailUnverified = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestExchangeAndVerify_PostsTheCodeThroughTheProvidersClient: the token endpoint, which
// the discovery document names, is reached through the address-checked client, so a
// loopback one is refused at the dial, confirmation or not, and never hears the code.
func TestExchangeAndVerify_PostsTheCodeThroughTheProvidersClient(t *testing.T) {
	t.Parallel()
	var reached atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		http.Error(w, "no", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := mr.Set(oidcStateKey("state"), "nonce|verifier|digest"); err != nil {
		t.Fatal(err)
	}
	p := &OIDCProvider{
		oauth2Cfg: oauth2.Config{ClientID: "nexara", Endpoint: oauth2.Endpoint{TokenURL: srv.URL + "/token"}},
		rdb:       rdb,
		client:    notifications.SafeHTTPClientAllowingPrivate(5*time.Second, true),
	}

	_, err := p.ExchangeAndVerify(t.Context(), "idp-code", "state")

	if !errors.Is(err, ErrOIDCTokenExchange) || !strings.Contains(err.Error(), "connection blocked") || reached.Load() {
		t.Errorf("err = %v, token endpoint reached: %v; want the dial refused and nothing sent", err, reached.Load())
	}
}

// TestExchangeAndVerify_RefusesAnIDTokenWhoseEmailIsNotVerified: the check runs on the
// verified ID token's claims, for an otherwise good sign-in; the same token saying true
// signs in, carrying its iss and sub (the identity an account is bound to), and one
// without a sub is refused.
func TestExchangeAndVerify_RefusesAnIDTokenWhoseEmailIsNotVerified(t *testing.T) {
	t.Parallel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		verified bool
		sub      string
	}{{false, "alice"}, {true, "alice"}, {true, ""}} {
		verified := tc.verified
		var issuer string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			idToken, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
				"iss": issuer, "aud": "nexara", "sub": tc.sub, "nonce": "nonce",
				"exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(),
				"email": "alice@example.com", "email_verified": verified,
			}).SignedString(key)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": idToken})
		}))
		t.Cleanup(srv.Close)
		issuer = srv.URL
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		if err := mr.Set(oidcStateKey("state"), "nonce|verifier|digest"); err != nil {
			t.Fatal(err)
		}
		p := &OIDCProvider{
			oauth2Cfg: oauth2.Config{ClientID: "nexara", Endpoint: oauth2.Endpoint{TokenURL: srv.URL + "/token"}},
			verifier: oidc.NewVerifier(srv.URL, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}},
				&oidc.Config{ClientID: "nexara"}),
			rdb: rdb, cfg: OIDCConfig{EmailClaim: "email"}, client: srv.Client(),
		}

		info, err := p.ExchangeAndVerify(t.Context(), "idp-code", "state")

		var unverified *OIDCEmailNotVerifiedError
		switch {
		case tc.sub == "":
			if !errors.Is(err, ErrOIDCClaimExtract) {
				t.Errorf("no sub: info %+v, err %v; want ErrOIDCClaimExtract", info, err)
			}
		case errors.As(err, &unverified) == verified || verified && info == nil:
			t.Errorf("email_verified %v: info %+v, err %v; want a refusal only when false", verified, info, err)
		case verified && (info.Issuer != srv.URL || info.Subject != "alice"):
			t.Errorf("issuer %q, subject %q; want %q, alice", info.Issuer, info.Subject, srv.URL)
		}
	}
}

// TestGenerateAuthURL_RefusesAnAuthorizationEndpointThatIsNotHTTP: the URL the browser
// is sent to comes from the discovery document; a javascript: one is refused before
// any state is stored.
func TestGenerateAuthURL_RefusesAnAuthorizationEndpointThatIsNotHTTP(t *testing.T) {
	t.Parallel()
	for endpoint, ok := range map[string]bool{
		"https://idp.example.com/auth": true,
		"javascript:alert(1)//x":       false,
	} {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		p := &OIDCProvider{oauth2Cfg: oauth2.Config{ClientID: "nexara", Endpoint: oauth2.Endpoint{AuthURL: endpoint}}, rdb: rdb}

		authURL, _, err := p.GenerateAuthURL(t.Context(), "0:digest")

		if (err == nil) != ok || ok != strings.HasPrefix(authURL, endpoint) || !ok && len(mr.Keys()) != 0 {
			t.Errorf("%s: url %q, err %v, keys %v; want it refused only when not http(s)", endpoint, authURL, err, mr.Keys())
		}
	}
}

// TestStepUp_TheURLAsksForAFreshSignInAndTheStateSaysItIsAStepUp: a step-up's URL asks
// the provider to authenticate again (prompt=login, max_age=0) and a sign-in's does not;
// its callback's verified token reports the step-up record and auth_time (zero when the
// token has none), so the callback can never take a step-up for a sign-in.
func TestStepUp_TheURLAsksForAFreshSignInAndTheStateSaysItIsAStepUp(t *testing.T) {
	t.Parallel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	authTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	for _, tc := range []struct {
		name, stepUp string
		authTime     any // the claim; nil sends none
	}{
		{"a sign-in", "", nil},
		{"a step-up", "session:user", authTime.Unix()},
		{"a step-up whose token has no auth_time", "session:user", nil},
	} {
		var issuer, nonce string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			claims := jwt.MapClaims{"iss": issuer, "aud": "nexara", "sub": "alice", "nonce": nonce,
				"exp": time.Now().Add(time.Minute).Unix(), "email": "alice@example.com"}
			if tc.authTime != nil {
				claims["auth_time"] = tc.authTime
			}
			idToken, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": idToken})
		}))
		t.Cleanup(srv.Close)
		issuer = srv.URL
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		p := &OIDCProvider{
			oauth2Cfg: oauth2.Config{ClientID: "nexara", Endpoint: oauth2.Endpoint{AuthURL: "https://idp.example.com/auth", TokenURL: srv.URL + "/token"}},
			verifier: oidc.NewVerifier(srv.URL, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}},
				&oidc.Config{ClientID: "nexara"}),
			rdb: rdb, cfg: OIDCConfig{EmailClaim: "email"}, client: srv.Client(),
		}

		authURL, state, err := p.GenerateAuthURL(t.Context(), "0:digest")
		if tc.stepUp != "" {
			authURL, state, err = p.GenerateStepUpURL(t.Context(), "0:digest", tc.stepUp)
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		u, _ := url.Parse(authURL)
		nonce = u.Query().Get("nonce")
		fresh := u.Query().Get("prompt") == "login" && u.Query().Get("max_age") == "0"
		if fresh != (tc.stepUp != "") {
			t.Errorf("%s: the URL asks for a fresh sign-in: %v", tc.name, fresh)
		}
		if _, peeked, _ := OIDCStatePeek(t.Context(), rdb, state); peeked != tc.stepUp {
			t.Errorf("%s: the peek reads step-up %q, want %q", tc.name, peeked, tc.stepUp)
		}
		info, err := p.ExchangeAndVerify(t.Context(), "idp-code", state)
		wantTime := time.Time{}
		if tc.authTime != nil {
			wantTime = authTime
		}
		if err != nil || info.StepUp != tc.stepUp || !info.AuthTime.Equal(wantTime) {
			t.Errorf("%s: info %+v, err %v; want step-up %q, auth_time %v", tc.name, info, err, tc.stepUp, wantTime)
		}
	}
}
