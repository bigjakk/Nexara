package handlers

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// loginAs sends a password sign-in for email; right picks the harness's password.
func (a *epochApp) loginAs(t *testing.T, email string, right bool) (*http.Response, map[string]any) {
	t.Helper()
	password := racePassword + "-wrong"
	if right {
		password = racePassword
	}
	resp := a.post(t, "/auth/login", `{"email":"`+email+`","password":"`+password+`"}`)
	return resp, decodeObject(t, resp)
}

// TestLogin_RepeatedFailuresLockTheIdentifier: the failure that uses up the budget is
// answered 429 with a Retry-After, and so is every attempt after it, the right password
// included — the same for a local account, a directory account and an identifier that
// names none, so the lockout is no oracle for which accounts exist. The lock of an
// account is audited, with neither the identifier nor a password in the row.
func TestLogin_RepeatedFailuresLockTheIdentifier(t *testing.T) {
	t.Parallel()
	ldap := epochUser(t, epochAdminEmail, 0)
	ldap.AuthSource = "ldap"
	const unknown = "carol@example.com"

	answers := make([]string, 0, 6)
	for _, tc := range []struct {
		name, email string
		audited     bool
	}{
		{"a local account", epochLoginEmail, true},
		{"a directory account", epochAdminEmail, true},
		{"no account", unknown, false},
	} {
		a := newEpochApp(t, newEpochStore(epochUser(t, epochLoginEmail, 0), ldap), epochOptions{})
		for i := 1; i < loginLockoutThreshold; i++ {
			if resp, body := a.loginAs(t, tc.email, false); resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s: failure %d = %d %v, want 401", tc.name, i, resp.StatusCode, body)
			}
		}
		for _, right := range []bool{false, true} {
			resp, body := a.loginAs(t, tc.email, right)
			if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "900" {
				t.Errorf("%s (right password: %t) = %d, Retry-After %q, want 429 and 900", tc.name, right, resp.StatusCode, resp.Header.Get("Retry-After"))
			}
			answers = append(answers, messageOf(body))
		}
		var locks []string
		for i, action := range a.store.auditActions() {
			if action == "login_locked" {
				locks = append(locks, a.store.auditDetailsWritten()[i])
			}
		}
		if tc.audited != (len(locks) == 1) || len(locks) > 1 {
			t.Errorf("%s: login_locked rows %v, want one: %t", tc.name, locks, tc.audited)
		}
		for _, d := range locks {
			if strings.Contains(d, tc.email) || strings.Contains(d, racePassword) {
				t.Errorf("%s: the lock's audit row names what was typed: %s", tc.name, d)
			}
		}
	}
	for _, msg := range answers[1:] {
		if msg != answers[0] {
			t.Errorf("the locked answers differ: %q and %q", answers[0], msg)
		}
	}
}

// TestLogin_ASuccessClearsTheCount: failures just short of the lock, a success, and as
// many failures again are all answered 401.
func TestLogin_ASuccessClearsTheCount(t *testing.T) {
	t.Parallel()
	a := newEpochApp(t, newEpochStore(epochUser(t, epochLoginEmail, 0)), epochOptions{})
	a.auth.SetPasswordLockoutStore(a.rdb)
	for round := range 2 {
		for i := 1; i < loginLockoutThreshold; i++ {
			if resp, _ := a.loginAs(t, epochLoginEmail, false); resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("round %d, failure %d = %d, want 401", round, i, resp.StatusCode)
			}
		}
		if resp, body := a.loginAs(t, epochLoginEmail, true); resp.StatusCode != http.StatusOK {
			t.Fatalf("round %d: the right password = %d %v, want 200", round, resp.StatusCode, body)
		}
	}
}

// TestLogin_TheReleaseOfALockIsAudited: the first attempt after a lock has run out
// writes the release's audit row, once.
func TestLogin_TheReleaseOfALockIsAudited(t *testing.T) {
	t.Parallel()
	a := newEpochApp(t, newEpochStore(epochUser(t, epochLoginEmail, 0)), epochOptions{})
	a.auth.SetPasswordLockoutStore(a.rdb)
	for range loginLockoutThreshold {
		a.loginAs(t, epochLoginEmail, false)
	}
	a.redis.FastForward(loginLockoutDuration + time.Second)
	for range 2 {
		if resp, _ := a.loginAs(t, epochLoginEmail, false); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("a failure after the lock ran out = %d, want 401", resp.StatusCode)
		}
	}
	released := 0
	for _, action := range a.store.auditActions() {
		if action == "login_lock_released" {
			released++
		}
	}
	if released != 1 {
		t.Errorf("login_lock_released rows = %d, want 1 (audit actions %v)", released, a.store.auditActions())
	}
}

// TestLogin_ARedisThatCannotCountDoesNotStopTheSignIn: with Redis down the lockout is not
// enforced, the sign-in goes on, and the log says so. Not parallel: it captures the
// process's default logger.
func TestLogin_ARedisThatCannotCountDoesNotStopTheSignIn(t *testing.T) {
	logs := captureProductionLog(t)
	a := newEpochApp(t, newEpochStore(epochUser(t, epochLoginEmail, 0)), epochOptions{})
	a.auth.SetPasswordLockoutStore(a.rdb)
	a.redis.Close()
	if resp, body := a.loginAs(t, epochLoginEmail, true); resp.StatusCode != http.StatusOK {
		t.Fatalf("the sign-in = %d %v, want 200", resp.StatusCode, body)
	}
	if !strings.Contains(logs.String(), "lockout is not enforced") {
		t.Errorf("no warning says the lockout was not enforced: %s", logs.String())
	}
}
