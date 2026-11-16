package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// ssoSetupApp is the epoch harness with an SSO account, erin, bound to the identity
// erinIdentity and holding a live session, and another SSO account, frank, bound to his.
func ssoSetupApp(t *testing.T) (a *epochApp, erin db.User, session uuid.UUID) {
	t.Helper()
	erin = db.User{ID: uuid.New(), Email: "erin@example.com", IsActive: true, Role: "user", AuthSource: "oidc",
		OidcIssuer: epochIssuer, OidcSubject: "subject-of-erin"}
	frank := db.User{ID: uuid.New(), Email: "frank@example.com", IsActive: true, Role: "user", AuthSource: "oidc",
		OidcIssuer: epochIssuer, OidcSubject: "subject-of-frank"}
	store := newEpochStore(erin, frank)
	session = uuid.New()
	store.sessions = append(store.sessions, db.Session{ID: session, UserID: erin.ID, ExpiresAt: time.Now().Add(time.Hour)})
	return newEpochApp(t, store, epochOptions{totp: true}), erin, session
}

// beginSetupIn sends a setup from session, carrying cookie ("name=value", or none).
func (a *epochApp) beginSetupIn(t *testing.T, user db.User, session uuid.UUID, cookie string) int {
	t.Helper()
	return a.beginSetupResp(t, user, session, cookie).StatusCode
}

// beginSetupResp is beginSetupIn's whole response.
func (a *epochApp) beginSetupResp(t *testing.T, user db.User, session uuid.UUID, cookie string) *http.Response {
	t.Helper()
	headers := map[string]string{"X-Test-Acting-User": user.ID.String(), "X-Test-Session": session.String()}
	if cookie != "" {
		headers["Cookie"] = cookie
	}
	return a.send(t, http.MethodPost, "/auth/totp/setup", `{}`, headers, 20*time.Second)
}

// erinIdentity is the step-up's identity of erin, as the provider vouches for it.
func erinIdentity(record string, authTime time.Time) *auth.OIDCUserInfo {
	return &auth.OIDCUserInfo{Issuer: epochIssuer, Subject: "subject-of-erin", Email: "erin@example.com", AuthTime: authTime, StepUp: record}
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
func confirmStepUp(t *testing.T, a *epochApp, record string, info *auth.OIDCUserInfo, relaxed ...bool) (answer, cookie string) {
	t.Helper()
	a.probe = func(c fiber.Ctx) error {
		return c.SendString(a.oidc.finishStepUp(c, record, info, "", len(relaxed) > 0 && relaxed[0]))
	}
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
		name, subject, email string
		authTime             time.Time
		setupIn              string // "asking" or "other": the session the setup comes from
		setups               []int  // the answers of successive setups
		reason               string // the refusal's class, "" for none
	}{
		{"a fresh sign-in as the account", "subject-of-erin", "erin@example.com", time.Now(), "asking", []int{200, 403}, ""},
		{"used from another session", "subject-of-erin", "erin@example.com", time.Now(), "other", []int{403}, ""},
		{"another account", "subject-of-frank", "frank@example.com", time.Now(), "asking", []int{403}, "another_account"},
		{"an identity bound to another account, with this address", "subject-of-frank", "erin@example.com", time.Now(), "asking", []int{403}, "another_account"},
		{"this address, bound to another identity", "subject-of-a-stranger", "erin@example.com", time.Now(), "asking", []int{403}, "another_account"},
		{"the account is not linked yet", "", "erin@example.com", time.Now(), "asking", []int{403}, "not_linked"},
		{"no auth_time", "subject-of-erin", "erin@example.com", time.Time{}, "asking", []int{403}, "no_auth_time"},
		{"a sign-in ten minutes old", "subject-of-erin", "erin@example.com", time.Now().Add(-10 * time.Minute), "asking", []int{403}, "stale_sign_in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, erin, session := ssoSetupApp(t)
			record := stepUpRecord(session, erin.ID)
			info := &auth.OIDCUserInfo{Issuer: epochIssuer, Subject: tc.subject, Email: tc.email, AuthTime: tc.authTime, StepUp: record}
			if tc.subject == "" { // erin's session predates links: she is unlinked, the provider still names her
				a.store.mutate(erin.ID, func(u *db.User) { u.OidcIssuer, u.OidcSubject = "", "" })
				info.Subject = "subject-of-erin"
			}
			got, cookie := confirmStepUp(t, a, record, info)
			if got != tc.reason {
				t.Errorf("the callback's answer = %q, want %q", got, tc.reason)
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
	if resp.StatusCode != http.StatusFound || loc.Path != ssoStepUpPage || loc.Query().Get("sso_step_up_error") != "another_browser" {
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
	_, cookie := confirmStepUp(t, a, record, erinIdentity(record, time.Now()))
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

// TestTOTPSetup_TheRelaxedStepUpAcceptsARoundTripWithoutAuthTime: with the operator's
// relaxed_step_up, a completed round trip whose token has no auth_time records a proof
// the owner's setup uses, audited as the relaxed rule; without it, it is refused.
func TestTOTPSetup_TheRelaxedStepUpAcceptsARoundTripWithoutAuthTime(t *testing.T) {
	t.Parallel()
	for _, relaxed := range []bool{true, false} {
		a, erin, session := ssoSetupApp(t)
		record := stepUpRecord(session, erin.ID)
		got, cookie := confirmStepUp(t, a, record, erinIdentity(record, time.Time{}), relaxed)
		want, setup := "", http.StatusOK
		if !relaxed {
			want, setup = "no_auth_time", http.StatusForbidden
		}
		if got != want || a.beginSetupIn(t, erin, session, cookie) != setup {
			t.Errorf("relaxed %v: the callback answered %q, want %q, and the setup was not %d", relaxed, got, want, setup)
		}
		if _, details := auditOf(a, ssoStepUpConfirmedAction); relaxed && (len(details) != 1 || !strings.Contains(details[0], `"rule":"relaxed"`)) {
			t.Errorf("confirmation rows %v, want one under the relaxed rule", details)
		}
	}
}

// TestTOTPSetup_TheStepUpCookieIsLaxAndReachesTheSetup: the step-up cookie is set on the
// response to the provider's cross-site redirect, so it is Lax (a browser may drop a
// Strict one), HttpOnly, with a Max-Age, and sent to the setup route, under the
// redirect URI's prefix when a reverse proxy serves Nexara under one.
func TestTOTPSetup_TheStepUpCookieIsLaxAndReachesTheSetup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		secure      bool
		redirectURI string
		name, path  string
	}{
		{false, "http://nexara.example.com/api/v1/auth/oidc/callback", totpStepUpCookieName, "/api/v1/auth/totp/setup"},
		{false, "http://nexara.example.com/nexara/api/v1/auth/oidc/callback", totpStepUpCookieName, "/nexara/api/v1/auth/totp/setup"},
		{true, "https://nexara.example.com/nexara/api/v1/auth/oidc/callback", hostCookiePrefix + totpStepUpCookieName, "/"},
	} {
		app := fiber.New()
		app.Get("/set", func(c fiber.Ctx) error {
			setTOTPStepUpCookie(c, "secret", tc.secure, time.Minute, tc.redirectURI)
			return c.SendStatus(http.StatusNoContent)
		})
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/set", nil))
		if err != nil {
			t.Fatal(err)
		}
		got := cookiesNamed(resp, tc.name)
		if len(got) != 1 || got[0].Path != tc.path || !got[0].HttpOnly || got[0].Secure != tc.secure ||
			got[0].SameSite != http.SameSiteLaxMode || got[0].MaxAge <= 0 {
			t.Errorf("secure %v: Set-Cookie = %+v, want one %s, HttpOnly, SameSite=Lax, with a Max-Age, at %s", tc.secure, got, tc.name, tc.path)
		}
	}
}
