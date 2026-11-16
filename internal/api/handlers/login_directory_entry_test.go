package handlers

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// fakeSignInDirectory is one directory entry that answers to every name in names, with
// password; any other name is not found.
type fakeSignInDirectory struct {
	names    map[string]bool
	entry    auth.LDAPUser
	password string
	binds    *atomic.Int32 // when set, counts the binds made
}

func (d fakeSignInDirectory) Search(username string) (*auth.LDAPUser, error) {
	if !d.names[username] {
		return nil, fmt.Errorf("%w: no entries matched", auth.ErrLDAPUserNotFound)
	}
	e := d.entry
	return &e, nil
}

func (d fakeSignInDirectory) Authenticate(_ *auth.LDAPUser, password string) error {
	if d.binds != nil {
		d.binds.Add(1)
	}
	if password != d.password {
		return fmt.Errorf("%w: invalid credentials", auth.ErrLDAPUserBindFailed)
	}
	return nil
}

func (fakeSignInDirectory) Close() {}

// TestLogin_ADirectoryEntryHasOneBudgetForAllItsNames: an entry answering to a uid and
// to its address spends one budget, whichever name is typed, so ten failures spread over
// both lock it (429, the right password included); and a failure typed as the uid, which
// names no account by itself, is audited under the entry's account.
func TestLogin_ADirectoryEntryHasOneBudgetForAllItsNames(t *testing.T) {
	t.Parallel()
	carol := db.User{ID: uuid.New(), Email: "carol@example.com", IsActive: true, Role: "user", AuthSource: "ldap"}
	store := newEpochStore(carol)
	store.ldapConfig = &db.LdapConfig{ID: uuid.New(), Enabled: true}
	a := newEpochApp(t, store, epochOptions{})
	dir := fakeSignInDirectory{
		names:    map[string]bool{"carol": true, carol.Email: true},
		entry:    auth.LDAPUser{DN: "uid=carol,ou=people,dc=example,dc=com", Email: carol.Email},
		password: racePassword,
	}
	a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries,
		signIn: func(auth.LDAPConfig) (ldapSignInSession, error) { return dir, nil }})

	for i := 1; i <= loginLockoutThreshold; i++ {
		name := "carol"
		if i%2 == 0 {
			name = carol.Email
		}
		want := http.StatusUnauthorized
		if i == loginLockoutThreshold {
			want = http.StatusTooManyRequests
		}
		if resp, body := a.loginAs(t, name, false); resp.StatusCode != want {
			t.Fatalf("failure %d, as %s = %d %v, want %d", i, name, resp.StatusCode, body, want)
		}
	}
	if resp, _ := a.loginAs(t, "carol", true); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("the right password once the entry is locked = %d, want 429", resp.StatusCode)
	}
	rows, _ := auditOf(a, "login_failed")
	if len(rows) != loginLockoutThreshold || rows[0].actor != carol.ID {
		t.Errorf("login_failed rows %v, want %d under the entry's account, the uid's included", rows, loginLockoutThreshold)
	}
}
