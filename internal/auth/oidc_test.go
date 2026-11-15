package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
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
