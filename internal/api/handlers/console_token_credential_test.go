package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestConsoleToken_BindsTheCallersCredential: a console token names the session or
// API key it was asked for with, which its console is held to while it is open
// (internal/ws/console_credential.go). A session that has ended, or an access token
// naming none, is given no token — the 401 sends the SPA to the refresh that ends,
// or names, its session.
func TestConsoleToken_BindsTheCallersCredential(t *testing.T) {
	t.Parallel()
	keyID := uuid.New()
	tests := []struct {
		name    string
		caller  func(live, revoked db.Session) map[string]string
		fail    error // the liveness check fails with it
		status  int
		want    func(live db.Session) auth.ConsoleCredential // the token's credential, on 200
		checked bool                                         // whether the session was checked
	}{
		{"a live session", func(live, _ db.Session) map[string]string {
			return map[string]string{"X-Test-Session": live.ID.String()}
		}, nil, http.StatusOK, func(live db.Session) auth.ConsoleCredential {
			return auth.ConsoleCredential{SessionID: live.ID}
		}, true},
		{"a revoked session", func(_, revoked db.Session) map[string]string {
			return map[string]string{"X-Test-Session": revoked.ID.String()}
		}, nil, http.StatusUnauthorized, nil, true},
		{"an access token that names no session", func(db.Session, db.Session) map[string]string {
			return map[string]string{"X-Test-Session": "none"}
		}, nil, http.StatusUnauthorized, nil, false},
		// authenticateAPIKey checked the key in this same request.
		{"an API key", func(db.Session, db.Session) map[string]string {
			return map[string]string{"X-Test-API-Key": keyID.String()}
		}, nil, http.StatusOK, func(db.Session) auth.ConsoleCredential {
			return auth.ConsoleCredential{APIKeyID: keyID}
		}, false},
		{"a session that could not be checked", func(live, _ db.Session) map[string]string {
			return map[string]string{"X-Test-Session": live.ID.String()}
		}, context.DeadlineExceeded, http.StatusServiceUnavailable, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			user := epochUser(t, epochLoginEmail, 0)
			store := newEpochStore(user)
			live := db.Session{ID: uuid.New(), UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)}
			revoked := db.Session{ID: uuid.New(), UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour), IsRevoked: true}
			store.sessions = append(store.sessions, live, revoked)
			if tt.fail != nil {
				store.failOn["IsConsoleCredentialLive"] = tt.fail
			}
			a := newEpochApp(t, store, epochOptions{})

			headers := tt.caller(live, revoked)
			headers["X-Test-Acting-User"] = user.ID.String()
			resp := a.send(t, http.MethodPost, "/auth/console-token",
				fmt.Sprintf(`{"cluster_id":%q,"node":"pve-01","type":"node_shell"}`, uuid.New()), headers, 5*time.Second)

			if resp.StatusCode != tt.status {
				t.Fatalf("status %d, want %d", resp.StatusCode, tt.status)
			}
			if checked := len(store.named("IsConsoleCredentialLive")) > 0; checked != tt.checked {
				t.Errorf("session checked: %v, want %v", checked, tt.checked)
			}
			minted := slices.Contains(store.auditActions(), "console_token_mint")
			if minted != (tt.want != nil) {
				t.Errorf("console_token_mint audited: %v, want %v", minted, tt.want != nil)
			}
			if tt.want == nil {
				return
			}
			var body consoleTokenResponse
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			claims, err := a.jwt.ValidateAccessToken(body.Token)
			if err != nil {
				t.Fatalf("the minted token does not validate: %v", err)
			}
			if got := claims.ConsoleCredential(); got != tt.want(live) {
				t.Errorf("the token names %+v, want %+v", got, tt.want(live))
			}
		})
	}
}
