package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Two directory entries' identifiers, in entryIDOf's form.
const (
	entryOfCarol = "entryUUID:6c0a1f6e-0000-4000-8000-0000000000a1"
	entryOfDave  = "entryUUID:6c0a1f6e-0000-4000-8000-0000000000d1"
)

// directoryAccountFixture is carol's directory account, bound to entry under config cfg
// when entry is set, in a store whose enabled config is cfg.
func directoryAccountFixture(t *testing.T, entry string) (*epochApp, db.User) {
	t.Helper()
	cfg := uuid.New()
	carol := db.User{ID: uuid.New(), Email: "carol@example.com", IsActive: true, Role: "user", AuthSource: "ldap"}
	if entry != "" {
		carol.LdapConfigID, carol.LdapEntryID = pgtype.UUID{Bytes: cfg, Valid: true}, entry
	}
	store := newEpochStore(carol)
	store.ldapConfig = &db.LdapConfig{ID: cfg, Enabled: true, UserFilter: mailFilter, EmailAttribute: "mail"}
	return newEpochApp(t, store, epochOptions{}), carol
}

func (s *epochStore) userCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users)
}

// TestLDAPLogin_AnAccountIsItsDirectoryEntry: an account bound to an entry signs in as
// that entry only. Another entry given the same name and address (a new hire with a
// departed person's uid and mail) is refused, 403, with nothing created or rebound,
// and audited as a failed sign-in with a fixed reason. An account not bound yet is
// bound at its sign-in by today's rule, and a directory with no identifier works as before.
func TestLDAPLogin_AnAccountIsItsDirectoryEntry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, stored, presented, after string
		want                           int
	}{
		{"its own entry", entryOfCarol, entryOfCarol, entryOfCarol, http.StatusOK},
		{"another entry with its name and address", entryOfCarol, entryOfDave, entryOfCarol, http.StatusForbidden},
		{"not bound yet", "", entryOfDave, entryOfDave, http.StatusOK},
		{"a directory with no identifier", "", "", "", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, carol := directoryAccountFixture(t, tc.stored)
			entry := auth.LDAPUser{DN: "uid=carol,ou=people,dc=example,dc=com", Email: carol.Email, EntryID: tc.presented}
			a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries, signIn: func(auth.LDAPConfig) (ldapSignInSession, error) {
				return fakeSignInDirectory{names: map[string]bool{"carol": true}, entry: entry, password: racePassword}, nil
			}})
			resp, body := a.loginAs(t, "carol", true)
			after := a.store.user(carol.ID)
			if resp.StatusCode != tc.want || a.store.userCount() != 1 || after.LdapEntryID != tc.after || after.LdapConfigID.Valid != (tc.after != "") {
				t.Fatalf("status %d %v, %d accounts, bound to %q; want %d, one account, bound to %q",
					resp.StatusCode, body, a.store.userCount(), after.LdapEntryID, tc.want, tc.after)
			}
			if tc.want != http.StatusForbidden {
				return
			}
			rows, details := auditOf(a, "login_failed")
			if messageOf(body) != ldapEntryMismatchMessage || len(rows) != 1 || rows[0].actor != carol.ID ||
				!strings.Contains(details[0], `"reason":"identity_mismatch"`) || strings.Contains(details[0], `"carol"`) {
				t.Errorf("message %q, login_failed %v %v; want the mismatch, one row under carol with its reason and nothing typed",
					messageOf(body), rows, details)
			}
			epochRequireNothingIssued(t, resp, body)
			epochRequireNoLoginAudit(t, a.store)
		})
	}
}

// TestLDAP_AnAccountFollowsItsEntrysNewAddress: when the directory changes a bound
// person's mail, a sign-in with the new address and a sync that searches by the old one
// both keep their one account and move it to the new address, audited: the sync used
// to deactivate it, and the sign-in to create a second.
func TestLDAP_AnAccountFollowsItsEntrysNewAddress(t *testing.T) {
	t.Parallel()
	const moved = "carol.moved@example.com"
	for _, via := range []string{"sign-in", "sync"} {
		t.Run(via, func(t *testing.T) {
			t.Parallel()
			a, carol := directoryAccountFixture(t, entryOfCarol)
			a.store.mutate(carol.ID, func(u *db.User) { u.LdapLoginName = carol.Email })
			entry := auth.LDAPUser{DN: "uid=carol,ou=people,dc=example,dc=com", Username: "carol", Email: moved, EntryID: entryOfCarol}
			if via == "sign-in" {
				a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries, signIn: func(auth.LDAPConfig) (ldapSignInSession, error) {
					return fakeSignInDirectory{names: map[string]bool{moved: true}, entry: entry, password: racePassword}, nil
				}})
				if resp, body := a.loginAs(t, moved, true); resp.StatusCode != http.StatusOK {
					t.Fatalf("sign-in with the new address = %d %v", resp.StatusCode, body)
				}
			} else {
				_, _, sync := ldapSyncApp(t, a.store, &fakeDirectory{entries: map[string]auth.LDAPUser{"carol": entry, moved: entry}})
				if status := sync(); status != http.StatusOK {
					t.Fatalf("sync = %d", status)
				}
			}
			after := a.store.user(carol.ID)
			_, details := auditOf(a, ldapAddressFollowedAction)
			if a.store.userCount() != 1 || !after.IsActive || after.Email != moved || len(details) != 1 || !strings.Contains(details[0], `"outcome":"updated"`) {
				t.Errorf("%d accounts, active %t, address %q, audit %v; want carol's one account, active, at %s, one row",
					a.store.userCount(), after.IsActive, after.Email, details, moved)
			}
		})
	}
}

// TestLDAPLogin_ADirectoryThatDidNotAnswerSpendsNoBudget: a user bind that timed out,
// or a directory that could not be reached, has not checked the password: 503, and no
// budget is spent, so the right password signs in once the directory answers again
// after more such failures than the lockout allows.
func TestLDAPLogin_ADirectoryThatDidNotAnswerSpendsNoBudget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		open, bind error
	}{
		{"the user bind timed out", nil, fmt.Errorf("%w: user bind: ldap: connection timed out", auth.ErrLDAPUnavailable)},
		{"the directory cannot be reached", fmt.Errorf("%w: dial: connection refused", auth.ErrLDAPConnection), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, carol := directoryAccountFixture(t, "")
			var healthy atomic.Bool
			a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries, signIn: func(auth.LDAPConfig) (ldapSignInSession, error) {
				dir := fakeSignInDirectory{names: map[string]bool{"carol": true}, password: racePassword,
					entry: auth.LDAPUser{DN: "uid=carol,ou=people,dc=example,dc=com", Email: carol.Email}}
				if healthy.Load() {
					return dir, nil
				}
				if tc.open != nil {
					return nil, tc.open
				}
				dir.bindErr = tc.bind
				return dir, nil
			}})
			for i := 0; i <= loginLockoutThreshold; i++ {
				if resp, body := a.loginAs(t, "carol", true); resp.StatusCode != http.StatusServiceUnavailable {
					t.Fatalf("attempt %d against a directory that does not answer = %d %v, want 503", i+1, resp.StatusCode, body)
				}
			}
			healthy.Store(true)
			if resp, body := a.loginAs(t, "carol", true); resp.StatusCode != http.StatusOK {
				t.Errorf("the right password once the directory answers = %d %v, want 200", resp.StatusCode, body)
			}
			if rows, _ := auditOf(a, "login_failed"); len(rows) != 0 {
				t.Errorf("login_failed rows %v for attempts the directory never checked", rows)
			}
		})
	}
}
