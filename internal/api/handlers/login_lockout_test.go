package handlers

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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

// auditOf is every audit row of action, with its details.
func auditOf(a *epochApp, action string) (rows []authFakeAuditRow, details []string) {
	all, det := a.store.auditRowsWritten(), a.store.auditDetailsWritten()
	for i, r := range all {
		if r.action == action {
			rows, details = append(rows, r), append(details, det[i])
		}
	}
	return rows, details
}

// TestLogin_AFailedSignInIsAudited: a failure of an account is a login_failed row under
// it, with the address, the method and the reason. One that names no account records
// nothing of what was typed, under no account, and once a minute per source address,
// the next minute's row carrying the count of the one before.
func TestLogin_AFailedSignInIsAudited(t *testing.T) {
	t.Parallel()
	user := epochUser(t, epochLoginEmail, 0)
	a := newEpochApp(t, newEpochStore(user), epochOptions{})
	a.auth.SetPasswordLockoutStore(a.rdb)

	a.loginAs(t, epochLoginEmail, false)
	rows, details := auditOf(a, "login_failed")
	if len(rows) != 1 || rows[0].actor != user.ID || rows[0].resourceID != user.ID.String() ||
		!strings.Contains(details[0], `"reason":"wrong_password"`) || !strings.Contains(details[0], `"method":"password"`) ||
		!strings.Contains(details[0], `"ip":`) || strings.Contains(details[0], racePassword) {
		t.Errorf("login_failed rows %+v %v, want one under the account with the reason, the method and the address", rows, details)
	}

	const typed = "Example-Password-1"
	for range 3 {
		a.loginAs(t, typed, false)
	}
	a.redis.FastForward(unknownSignInWindow + time.Second)
	a.loginAs(t, typed, false)
	rows, details = auditOf(a, "login_failed_unknown_account")
	if len(rows) != 2 {
		t.Fatalf("login_failed_unknown_account rows = %d, want one per minute: 2", len(rows))
	}
	for i, d := range details {
		if rows[i].actor != uuid.Nil || strings.Contains(d, typed) || strings.Contains(d, racePassword) {
			t.Errorf("row %d (%+v) %s: it must name no account and nothing that was typed", i, rows[i], d)
		}
	}
	for _, st := range a.store.named("InsertAuditLog") {
		if actor, _ := st.args[1].(pgtype.UUID); st.args[4] == "login_failed_unknown_account" && actor.Valid {
			t.Errorf("the row names user %v: it must have no user, which a user id that exists nowhere would break", actor)
		}
	}
	if !strings.Contains(details[1], `"failures_in_last_window":3`) {
		t.Errorf("the second minute's row %s, want the first minute's count, 3", details[1])
	}
}
