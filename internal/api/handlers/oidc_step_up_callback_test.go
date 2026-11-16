package handlers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// stepUpProvider is an identity provider on loopback: discovery, its signing key, and a
// token endpoint that signs an ID token for email with the nonce of the last
// authorization URL handed to it (setNonce) and, unless zero, authTime.
type stepUpProvider struct {
	srv      *httptest.Server
	mu       sync.Mutex
	nonce    string
	email    string
	authTime time.Time
}

func newStepUpProvider(t *testing.T, email string) *stepUpProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &stepUpProvider{email: email, authTime: time.Now()}
	b64 := base64.RawURLEncoding.EncodeToString
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/auth",
			"token_endpoint": p.srv.URL + "/token", "jwks_uri": p.srv.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes())}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		claims := jwt.MapClaims{"iss": p.srv.URL, "aud": "nexara", "sub": "sub-1", "nonce": p.nonce, "email": p.email,
			"exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix()}
		if !p.authTime.IsZero() {
			claims["auth_time"] = p.authTime.Unix()
		}
		p.mu.Unlock()
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "k1"
		signed, _ := tok.SignedString(key)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": signed})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// TestOIDCCallback_AStepUpRoundTripRecordsAProofAndSignsNobodyIn drives the whole round
// trip: the step-up route, the provider, and the callback in the browser that began it.
// The callback must go back to the Security page with a proof the setup then uses, and
// must never be completed as a sign-in: no exchange code, no session.
func TestOIDCCallback_AStepUpRoundTripRecordsAProofAndSignsNobodyIn(t *testing.T) {
	t.Parallel()
	a, erin, session := ssoSetupApp(t)
	idp := newStepUpProvider(t, erin.Email)
	a.store.mutate(erin.ID, func(u *db.User) { u.OidcIssuer, u.OidcSubject = idp.srv.URL, "sub-1" })
	a.store.oidcConfig = &db.OidcConfig{ID: uuid.New(), Enabled: true, IssuerUrl: idp.srv.URL, ClientID: "nexara",
		RedirectUri: "http://nexara.example.com/api/v1/auth/oidc/callback", EmailClaim: "email"}
	a.oidc.newProvider = func(ctx context.Context, cfg auth.OIDCConfig, rdb *redis.Client) (*auth.OIDCProvider, error) {
		return auth.NewOIDCProviderWithClient(ctx, cfg, rdb, idp.srv.Client())
	}
	a.totp.SetSSOStepUp(a.oidc.StartStepUp)

	start := a.send(t, http.MethodPost, "/auth/totp/setup/sso-step-up", `{}`,
		map[string]string{"X-Test-Acting-User": erin.ID.String(), "X-Test-Session": session.String()}, 20*time.Second)
	var started struct {
		RedirectURL string `json:"redirect_url"`
	}
	_ = json.NewDecoder(start.Body).Decode(&started)
	authURL, err := url.Parse(started.RedirectURL)
	if err != nil || start.StatusCode != http.StatusOK || authURL.Query().Get("prompt") != "login" {
		t.Fatalf("start = %d, %q: want 200 and an authorization URL with prompt=login", start.StatusCode, started.RedirectURL)
	}
	idp.mu.Lock()
	idp.nonce = authURL.Query().Get("nonce")
	idp.mu.Unlock()
	binding := make([]string, 0, len(start.Cookies()))
	for _, ck := range start.Cookies() {
		binding = append(binding, ck.Name+"="+ck.Value)
	}
	sessionsBefore := len(a.store.sessions)

	resp := a.send(t, http.MethodGet, "/auth/oidc/callback?code=idp-code&state="+url.QueryEscape(authURL.Query().Get("state")), "",
		map[string]string{"Cookie": strings.Join(binding, "; ")}, 20*time.Second)

	if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusFound || loc != ssoStepUpPage+"?sso_step_up=confirmed" {
		t.Fatalf("callback = %d to %q, want the Security page, confirmed", resp.StatusCode, loc)
	}
	if keys := a.redis.Keys(); len(keys) != 0 && strings.Contains(strings.Join(keys, " "), "oidc:exchange:") {
		t.Errorf("an exchange code was stored: %v", keys)
	}
	if len(a.store.sessions) != sessionsBefore || len(a.store.stepUpProofs) != 1 {
		t.Errorf("sessions %d (was %d), proofs %d: want no session made and one proof", len(a.store.sessions), sessionsBefore, len(a.store.stepUpProofs))
	}
	if got := a.beginSetupIn(t, erin, session, stepUpCookieOf(resp)); got != http.StatusOK {
		t.Errorf("the setup with the proof = %d, want 200", got)
	}
}
