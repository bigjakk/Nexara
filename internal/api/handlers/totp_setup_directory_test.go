package handlers

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// directorySetupApp is the epoch harness with a directory account, carol, who signs in
// as "carol" with racePassword, and the directory's entry for her.
func directorySetupApp(t *testing.T, entryEmail string) (*epochApp, db.User, *atomic.Int32) {
	t.Helper()
	carol := db.User{ID: uuid.New(), Email: "carol@example.com", IsActive: true, Role: "user", AuthSource: "ldap", LdapLoginName: "carol"}
	store := newEpochStore(carol)
	store.ldapConfig = &db.LdapConfig{ID: uuid.New(), Enabled: true}
	a := newEpochApp(t, store, epochOptions{totp: true})
	binds := &atomic.Int32{}
	dir := fakeSignInDirectory{names: map[string]bool{"carol": true},
		entry: auth.LDAPUser{DN: "uid=carol,ou=people,dc=example,dc=com", Email: entryEmail}, password: racePassword, binds: binds}
	a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries,
		signIn: func(auth.LDAPConfig) (ldapSignInSession, error) { return dir, nil }})
	return a, carol, binds
}

func (a *epochApp) beginSetupAs(t *testing.T, user db.User, password string) int {
	t.Helper()
	return a.send(t, http.MethodPost, "/auth/totp/setup", `{"current_password":"`+password+`"}`,
		map[string]string{"X-Test-Acting-User": user.ID.String()}, 20*time.Second).StatusCode
}

// TestTOTPSetup_ADirectoryAccountProvesItsDirectoryPassword: an LDAP account begins a
// setup only with its directory password, bound as its own entry: a wrong one is 403, an
// entry the sign-in name now finds for someone else is refused without a bind, and an
// attempt that cannot be counted is 503 with no bind at all.
func TestTOTPSetup_ADirectoryAccountProvesItsDirectoryPassword(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, entryEmail, password string
		redisDown                  bool
		status                     int
		binds                      int32
	}{
		{"the right password", "carol@example.com", racePassword, false, http.StatusOK, 1},
		{"a wrong password", "carol@example.com", "wrong", false, http.StatusForbidden, 1},
		{"no password", "carol@example.com", "", false, http.StatusBadRequest, 0},
		{"the name finds another account's entry", "dave@example.com", racePassword, false, http.StatusForbidden, 0},
		{"the attempt cannot be counted", "carol@example.com", racePassword, true, http.StatusServiceUnavailable, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, carol, binds := directorySetupApp(t, tc.entryEmail)
			if tc.redisDown {
				a.auth.SetPasswordLockoutStore(a.rdb)
				a.redis.Close()
			}
			if got := a.beginSetupAs(t, carol, tc.password); got != tc.status || binds.Load() != tc.binds {
				t.Errorf("status %d after %d binds, want %d after %d", got, binds.Load(), tc.status, tc.binds)
			}
		})
	}
}

// TestTOTPSetup_ADirectoryPasswordCountsInTheSignInBudget: wrong directory passwords at
// the setup spend the sign-in's budget, so the last of it locks both (429, the right
// password included) and a guesser with a stolen session gets no more tries than at the
// sign-in page.
func TestTOTPSetup_ADirectoryPasswordCountsInTheSignInBudget(t *testing.T) {
	t.Parallel()
	a, carol, _ := directorySetupApp(t, "carol@example.com")
	for i := 1; i <= loginLockoutThreshold; i++ {
		want := http.StatusForbidden
		if i == loginLockoutThreshold {
			want = http.StatusTooManyRequests
		}
		if got := a.beginSetupAs(t, carol, "wrong"); got != want {
			t.Fatalf("wrong password %d = %d, want %d", i, got, want)
		}
	}
	if resp, _ := a.loginAs(t, "carol", true); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("signing in with the right password after the lock = %d, want 429", resp.StatusCode)
	}
	if rows, _ := auditOf(a, "login_locked"); len(rows) != 2 {
		t.Errorf("login_locked rows = %v, want two: the name's lock and the entry's, both armed by the last one", rows)
	}
}

// TestTOTPSetup_ARightDirectoryPasswordGivesBackItsAttempt: the right password after nine
// wrong ones is the tenth attempt counted, and it hands that attempt back, so it does not
// leave signing in locked behind a setup that succeeded.
func TestTOTPSetup_ARightDirectoryPasswordGivesBackItsAttempt(t *testing.T) {
	t.Parallel()
	a, carol, _ := directorySetupApp(t, "carol@example.com")
	for range loginLockoutThreshold - 1 {
		a.beginSetupAs(t, carol, "wrong")
	}
	if got := a.beginSetupAs(t, carol, racePassword); got != http.StatusOK {
		t.Fatalf("the right password = %d, want 200", got)
	}
	if resp, body := a.loginAs(t, "carol", true); resp.StatusCode != http.StatusOK {
		t.Errorf("signing in after the setup = %d %v, want 200: the right password left the lock armed", resp.StatusCode, body)
	}
}

// TestTOTPSetup_AnAttemptNotCheckedLeavesNoLockArmed: when the name's attempt is the one
// that arms its lock and the entry's budget is already locked, no password is checked,
// so the name's attempt is handed back and its lock is not left armed unrecorded.
func TestTOTPSetup_AnAttemptNotCheckedLeavesNoLockArmed(t *testing.T) {
	t.Parallel()
	a, carol, binds := directorySetupApp(t, "carol@example.com")
	store, ctx := a.auth.loginLockoutStore(), t.Context()
	name, entry := a.auth.loginSubject("carol"), a.auth.loginEntrySubject("uid=carol,ou=people,dc=example,dc=com")
	for range loginLockoutThreshold - 1 {
		_, _ = store.reserve(ctx, name)
	}
	for range loginLockoutThreshold {
		_, _ = store.reserve(ctx, entry)
	}
	if got := a.beginSetupAs(t, carol, racePassword); got != http.StatusTooManyRequests || binds.Load() != 0 {
		t.Fatalf("with the entry locked = %d after %d binds, want 429 and no bind", got, binds.Load())
	}
	if res, _ := store.reserve(ctx, name); res.admission == lockoutRefused {
		t.Error("the name's budget is locked although no password was checked against it")
	}
}
