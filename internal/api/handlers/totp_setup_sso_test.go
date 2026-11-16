package handlers

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// ssoSetupApp is the epoch harness with an SSO account, erin, holding a live session,
// and another SSO account, frank.
func ssoSetupApp(t *testing.T) (a *epochApp, erin db.User, session uuid.UUID) {
	t.Helper()
	erin = db.User{ID: uuid.New(), Email: "erin@example.com", IsActive: true, Role: "user", AuthSource: "oidc"}
	frank := db.User{ID: uuid.New(), Email: "frank@example.com", IsActive: true, Role: "user", AuthSource: "oidc"}
	store := newEpochStore(erin, frank)
	session = uuid.New()
	store.sessions = append(store.sessions, db.Session{ID: session, UserID: erin.ID, ExpiresAt: time.Now().Add(time.Hour)})
	return newEpochApp(t, store, epochOptions{totp: true}), erin, session
}

// beginSetupIn sends a setup from session, carrying cookie ("name=value", or none).
func (a *epochApp) beginSetupIn(t *testing.T, user db.User, session uuid.UUID, cookie string) int {
	t.Helper()
	headers := map[string]string{"X-Test-Acting-User": user.ID.String(), "X-Test-Session": session.String()}
	if cookie != "" {
		headers["Cookie"] = cookie
	}
	return a.send(t, http.MethodPost, "/auth/totp/setup", `{}`, headers, 20*time.Second).StatusCode
}

// stepUpCookieOf is the step-up cookie a response set, as a request's "name=value".
func stepUpCookieOf(resp *http.Response) string {
	for _, ck := range resp.Cookies() {
		if strings.HasSuffix(ck.Name, totpStepUpCookieName) && ck.Value != "" {
			return ck.Name + "=" + ck.Value
		}
	}
	return ""
}

// confirmStepUp runs the callback's decision for erin's session through the probe and
// returns its answer and the cookie it set.
func confirmStepUp(t *testing.T, a *epochApp, record string, info *auth.OIDCUserInfo) (answer, cookie string) {
	t.Helper()
	a.probe = func(c fiber.Ctx) error { return c.SendString(a.oidc.finishStepUp(c, record, info, false)) }
	resp := a.send(t, http.MethodGet, "/probe", "", nil, 20*time.Second)
	return readBody(t, resp.Body), stepUpCookieOf(resp)
}

// TestTOTPSetup_AnSSOAccountProvesAFreshSignInAtItsProvider: the step-up's callback
// records a proof only for an ID token that resolves to the account that asked and whose
// auth_time is recent; the setup then uses it up, once, and only in the session that asked.
// Every refusal is audited with its class.
func TestTOTPSetup_AnSSOAccountProvesAFreshSignInAtItsProvider(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, email string
		authTime    time.Time
		setupIn     string // "asking" or "other": the session the setup comes from
		message     string
		setups      []int // the answers of successive setups
		reason      string
	}{
		{"a fresh sign-in as the account", "erin@example.com", time.Now(), "asking", "", []int{200, 403}, ""},
		{"used from another session", "erin@example.com", time.Now(), "other", "", []int{403}, ""},
		{"another account", "frank@example.com", time.Now(), "asking", ssoStepUpAnotherAccountMessage, []int{403}, "another_account"},
		{"no auth_time", "erin@example.com", time.Time{}, "asking", ssoStepUpNoAuthTimeMessage, []int{403}, "no_auth_time"},
		{"a sign-in ten minutes old", "erin@example.com", time.Now().Add(-10 * time.Minute), "asking", ssoStepUpStaleMessage, []int{403}, "stale_sign_in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, erin, session := ssoSetupApp(t)
			record := stepUpRecord(session, erin.ID)
			got, cookie := confirmStepUp(t, a, record, &auth.OIDCUserInfo{Email: tc.email, AuthTime: tc.authTime, StepUp: record})
			if got != tc.message {
				t.Errorf("the callback's answer = %q, want %q", got, tc.message)
			}
			from := session
			if tc.setupIn == "other" {
				from = uuid.New()
				a.store.sessions = append(a.store.sessions, db.Session{ID: from, UserID: erin.ID, ExpiresAt: time.Now().Add(time.Hour)})
			}
			for i, want := range tc.setups {
				if got := a.beginSetupIn(t, erin, from, cookie); got != want {
					t.Errorf("setup %d = %d, want %d", i+1, got, want)
				}
			}
			if tc.reason != "" {
				if _, details := auditOf(a, ssoStepUpRefusedAction); len(details) != 1 || !strings.Contains(details[0], `"reason":"`+tc.reason+`"`) {
					t.Errorf("refusal rows %v, want one with reason %s", details, tc.reason)
				}
			} else if rows, _ := auditOf(a, ssoStepUpConfirmedAction); len(rows) != 1 || rows[0].actor != erin.ID {
				t.Errorf("confirmation rows %v, want one under the account", rows)
			}
		})
	}
}

// TestOIDCCallback_AStepUpInAnotherBrowserIsRefusedAndNeverASignIn: a step-up's callback
// that arrives without its browser binding goes back to the Security page refused and
// audited, not to the sign-in page, and its state is spent so the URL cannot be replayed.
func TestOIDCCallback_AStepUpInAnotherBrowserIsRefusedAndNeverASignIn(t *testing.T) {
	t.Parallel()
	a, erin, session := ssoSetupApp(t)
	if err := a.redis.Set("oidc:state:the-state", "nonce|verifier|"+bindingRecord(false, "secret")+"|"+stepUpRecord(session, erin.ID)); err != nil {
		t.Fatal(err)
	}
	resp := a.send(t, http.MethodGet, "/auth/oidc/callback?code=idp-code&state=the-state", "", nil, 20*time.Second)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound || loc.Path != ssoStepUpPage || loc.Query().Get("sso_step_up_error") != ssoStepUpNotThisBrowserMessage {
		t.Errorf("answer %d to %q, want a redirect to the Security page saying it was not this browser", resp.StatusCode, loc)
	}
	if _, details := auditOf(a, ssoStepUpRefusedAction); len(details) != 1 || !strings.Contains(details[0], `"reason":"another_browser"`) {
		t.Errorf("refusal rows %v, want one for another browser", details)
	}
	if a.redis.Exists("oidc:state:the-state") {
		t.Error("the refused step-up's state survived: its callback URL can be replayed")
	}
}

// TestTOTPSetup_TheSSOStepUpStartsForTheCallersOwnSession: the route hands the step-up
// the caller's account and session, and refuses an account that does not sign in
// through single sign-on.
func TestTOTPSetup_TheSSOStepUpStartsForTheCallersOwnSession(t *testing.T) {
	t.Parallel()
	a, erin, session := ssoSetupApp(t)
	var gotUser, gotSession uuid.UUID
	a.totp.SetSSOStepUp(func(_ fiber.Ctx, userID, sessionID uuid.UUID) (string, error) {
		gotUser, gotSession = userID, sessionID
		return "https://idp.example.com/auth", nil
	})
	start := func(user uuid.UUID) int {
		return a.send(t, http.MethodPost, "/auth/totp/setup/sso-step-up", `{}`,
			map[string]string{"X-Test-Acting-User": user.String(), "X-Test-Session": session.String()}, 20*time.Second).StatusCode
	}
	if status := start(erin.ID); status != http.StatusOK || gotUser != erin.ID || gotSession != session {
		t.Errorf("start = %d for %v / %v, want 200 for the caller's account and session", status, gotUser, gotSession)
	}
	local := epochUser(t, epochAdminEmail, 0)
	a.store.users[local.ID] = &local
	if status := start(local.ID); status != http.StatusForbidden {
		t.Errorf("a local account's start = %d, want 403", status)
	}
}

// TestTOTPSetup_TheSSOProofIsBoundToTheBrowser: the proof is used only by a setup that
// carries the cookie the callback set: the right session with no cookie, or another
// one, gets nothing and leaves the proof, which the owner's own setup then uses.
func TestTOTPSetup_TheSSOProofIsBoundToTheBrowser(t *testing.T) {
	t.Parallel()
	a, erin, session := ssoSetupApp(t)
	record := stepUpRecord(session, erin.ID)
	_, cookie := confirmStepUp(t, a, record, &auth.OIDCUserInfo{Email: erin.Email, AuthTime: time.Now(), StepUp: record})
	if cookie == "" {
		t.Fatal("the callback set no step-up cookie")
	}
	for _, thief := range []string{"", totpStepUpCookieName + "=a-secret-of-the-thiefs-own"} {
		if got := a.beginSetupIn(t, erin, session, thief); got != http.StatusForbidden {
			t.Errorf("a setup in the session with cookie %q = %d, want 403", thief, got)
		}
	}
	if got := a.beginSetupIn(t, erin, session, cookie); got != http.StatusOK {
		t.Errorf("the owner's setup after them = %d, want 200: the proof was burned", got)
	}
}
