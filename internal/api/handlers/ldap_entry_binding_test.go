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
// after more such failures than the lockout allows. An entry the directory locked or
// inactivated is a refused sign-in (401, audited directory_refused), which spends no
// budget either: the directory's own policy limits it.
func TestLDAPLogin_ADirectoryThatDidNotAnswerSpendsNoBudget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		open, bind error
		want       int
	}{
		{"the user bind timed out", nil, fmt.Errorf("%w: user bind: ldap: connection timed out", auth.ErrLDAPUnavailable), http.StatusServiceUnavailable},
		{"the directory cannot be reached", fmt.Errorf("%w: dial: connection refused", auth.ErrLDAPConnection), nil, http.StatusServiceUnavailable},
		{"the directory refused the entry", nil, fmt.Errorf("%w: account locked", auth.ErrLDAPUserRefused), http.StatusUnauthorized},
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
				if resp, body := a.loginAs(t, "carol", true); resp.StatusCode != tc.want {
					t.Fatalf("attempt %d = %d %v, want %d", i+1, resp.StatusCode, body, tc.want)
				}
			}
			healthy.Store(true)
			if resp, body := a.loginAs(t, "carol", true); resp.StatusCode != http.StatusOK {
				t.Errorf("the right password once the directory answers = %d %v, want 200", resp.StatusCode, body)
			}
			rows, details := auditOf(a, "login_failed")
			if refused := tc.want == http.StatusUnauthorized; refused != (len(rows) == loginLockoutThreshold+1) ||
				refused && !strings.Contains(details[0], `"reason":"directory_refused"`) {
				t.Errorf("login_failed rows %v %v; want one per attempt, reason directory_refused, only for a refusal", rows, details)
			}
		})
	}
}

// secondFactorDirectoryFixture is carol's unbound directory account with a second
// factor (plain is its secret), carol's own entry and another entry with her address
// ("evil"), and directory, which makes the next sign-in find one entry by one name.
type secondFactorDirectoryFixture struct {
	a          *epochApp
	store      *epochStore
	carol      db.User
	cfg        uuid.UUID
	plain      string
	own, other auth.LDAPUser
}

func newSecondFactorDirectoryFixture(t *testing.T, mapping []byte) *secondFactorDirectoryFixture {
	t.Helper()
	encrypted, _, plain, err := auth.NewTOTPService(totpTestKey).GenerateSecret("carol@example.com")
	if err != nil {
		t.Fatal(err)
	}
	f := &secondFactorDirectoryFixture{cfg: uuid.New(), plain: plain}
	f.carol = db.User{ID: uuid.New(), Email: "carol@example.com", DisplayName: "Carol", IsActive: true, Role: "user", AuthSource: "ldap",
		LdapLoginName: "carol", TotpSecret: pgtype.Text{String: encrypted, Valid: true}}
	f.store = newEpochStore(f.carol)
	f.store.ldapConfig = &db.LdapConfig{ID: f.cfg, Enabled: true, UserFilter: mailFilter, EmailAttribute: "mail", GroupRoleMapping: mapping}
	f.a = newEpochApp(t, f.store, epochOptions{totp: true})
	f.own = auth.LDAPUser{DN: "uid=carol,ou=people,dc=example,dc=com", Username: "carol", Email: f.carol.Email, EntryID: entryOfCarol}
	f.other = auth.LDAPUser{DN: "uid=evil,ou=people,dc=example,dc=com", Username: "evil", Email: f.carol.Email, EntryID: entryOfDave}
	return f
}

func (f *secondFactorDirectoryFixture) directory(name string, entry auth.LDAPUser) {
	f.a.auth.SetLDAPHandler(&LDAPHandler{queries: f.a.auth.queries, signIn: func(auth.LDAPConfig) (ldapSignInSession, error) {
		return fakeSignInDirectory{names: map[string]bool{name: true}, entry: entry, password: racePassword}, nil
	}})
}

// TestLDAPLogin_ASecondFactorAccountIsBoundOnlyAfterItsCode: the password step of an
// account with a second factor writes nothing about its directory identity, so another
// entry with its address (signing in as "evil") neither binds it nor leaves a name the
// sync would bind it through; the owner's sign-in binds it once the code has passed.
func TestLDAPLogin_ASecondFactorAccountIsBoundOnlyAfterItsCode(t *testing.T) {
	t.Parallel()
	f := newSecondFactorDirectoryFixture(t, nil)
	a, store, carol, cfg, plain, own, other, directory := f.a, f.store, f.carol, f.cfg, f.plain, f.own, f.other, f.directory

	directory("evil", other)
	epochFirstStep(t, a, "evil")
	if u := store.user(carol.ID); u.LdapConfigID.Valid || u.LdapLoginName != "carol" {
		t.Fatalf("after another entry's password step: bound %t to %q, name %q; want nothing written", u.LdapConfigID.Valid, u.LdapEntryID, u.LdapLoginName)
	}
	_, _, sync := ldapSyncApp(t, store, &fakeDirectory{entries: map[string]auth.LDAPUser{"carol": own, "evil": other}})
	if status := sync(); status != http.StatusOK || store.user(carol.ID).LdapEntryID == entryOfDave {
		t.Fatalf("sync = %d, carol bound to %q; want 200, never to the other entry", status, store.user(carol.ID).LdapEntryID)
	}

	store.mutate(carol.ID, func(u *db.User) { u.LdapConfigID, u.LdapEntryID = pgtype.UUID{}, "" })
	directory("carol", own)
	// Bound to another entry between the password step and the code: refused.
	token := epochFirstStep(t, a, "carol")
	store.mutate(carol.ID, func(u *db.User) { u.LdapConfigID, u.LdapEntryID = pgtype.UUID{Bytes: cfg, Valid: true}, entryOfDave })
	resp := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, token, plain))
	epochRequireNothingIssued(t, resp, authRequireStatus(t, resp, http.StatusForbidden))
	if rows, details := auditOf(a, "login_failed"); len(rows) != 1 || !strings.Contains(details[0], `"reason":"identity_mismatch"`) {
		t.Fatalf("login_failed %v %v; want one identity_mismatch row", rows, details)
	}

	store.mutate(carol.ID, func(u *db.User) { u.LdapConfigID, u.LdapEntryID = pgtype.UUID{}, "" })
	token = epochFirstStep(t, a, "carol")
	if store.user(carol.ID).LdapConfigID.Valid {
		t.Fatal("the owner's password step bound the account before its code")
	}
	if resp := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, token, plain)); resp.StatusCode != http.StatusOK {
		t.Fatalf("verify-login = %d", resp.StatusCode)
	}
	if u := store.user(carol.ID); u.LdapEntryID != entryOfCarol || !boundUnder(u.LdapConfigID, cfg) {
		t.Errorf("after the code: bound to %q under %v; want carol's entry under the config", u.LdapEntryID, u.LdapConfigID)
	}
}

// TestLDAP_ABindingBelongsToItsConfiguration: an account bound under another
// configuration that still exists is that configuration's: a sync of this one neither
// lists nor rebinds it, and a sign-in through this one signs it in by its address, as
// before bindings, and writes no binding over it.
func TestLDAP_ABindingBelongsToItsConfiguration(t *testing.T) {
	t.Parallel()
	a, carol := directoryAccountFixture(t, entryOfCarol)
	other := uuid.New() // the config the store answers for, not carol's
	a.store.mu.Lock()
	a.store.ldapConfig.ID = other
	a.store.mu.Unlock()
	entry := auth.LDAPUser{DN: "uid=carol,ou=people,dc=example,dc=com", Username: "carol", Email: carol.Email, EntryID: entryOfDave}
	_, _, sync := ldapSyncApp(t, a.store, &fakeDirectory{entries: map[string]auth.LDAPUser{carol.Email: entry}})
	a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries, signIn: func(auth.LDAPConfig) (ldapSignInSession, error) {
		return fakeSignInDirectory{names: map[string]bool{"carol": true}, entry: entry, password: racePassword}, nil
	}})
	status := sync()
	resp, body := a.loginAs(t, "carol", true)
	if u := a.store.user(carol.ID); status != http.StatusOK || resp.StatusCode != http.StatusOK ||
		u.LdapEntryID != entryOfCarol || boundUnder(u.LdapConfigID, other) || len(a.store.named("ListLDAPUsers")) != 1 {
		t.Errorf("sync %d, sign-in %d %v, bound to %q under %v; want both 200 and the first binding kept", status, resp.StatusCode, body, u.LdapEntryID, u.LdapConfigID)
	}
}

// TestLDAP_AnAddressAnotherAccountHoldsIsKeptQuietly: when a bound entry's new address
// is another account's, the account keeps its own, and the audit row says so once a
// day without the other account's address (a Viewer reads audit details).
func TestLDAP_AnAddressAnotherAccountHoldsIsKeptQuietly(t *testing.T) {
	t.Parallel()
	a, carol := directoryAccountFixture(t, entryOfCarol)
	const taken = "dave@example.com"
	a.store.mu.Lock()
	dave := db.User{ID: uuid.New(), Email: taken, IsActive: true, AuthSource: "local"}
	a.store.users[dave.ID] = &dave
	a.store.mu.Unlock()
	a.auth.SetPasswordLockoutStore(a.rdb)
	entry := auth.LDAPUser{DN: "uid=carol,ou=people,dc=example,dc=com", Email: taken, EntryID: entryOfCarol}
	a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries, signIn: func(auth.LDAPConfig) (ldapSignInSession, error) {
		return fakeSignInDirectory{names: map[string]bool{"carol": true}, entry: entry, password: racePassword}, nil
	}})
	for range 2 {
		if resp, body := a.loginAs(t, "carol", true); resp.StatusCode != http.StatusOK {
			t.Fatalf("sign-in = %d %v", resp.StatusCode, body)
		}
	}
	_, details := auditOf(a, ldapAddressFollowedAction)
	if a.store.user(carol.ID).Email != carol.Email || len(details) != 1 ||
		!strings.Contains(details[0], `"outcome":"kept"`) || strings.Contains(details[0], taken) {
		t.Errorf("address %q, audit %v; want carol's own kept, one row without the other account's address", a.store.user(carol.ID).Email, details)
	}
}

// TestLDAPSync_ABoundEntryThatNoLongerShowsAnIdentifierIsSkipped: a directory that
// stops showing an identifier (an ACL change) says nothing about whether the account's
// entry is there, so the sync leaves the account as it is.
func TestLDAPSync_ABoundEntryThatNoLongerShowsAnIdentifierIsSkipped(t *testing.T) {
	t.Parallel()
	a, carol := directoryAccountFixture(t, entryOfCarol)
	a.store.mutate(carol.ID, func(u *db.User) { u.LdapLoginName = "carol" })
	_, _, syncNow := ldapSyncApp(t, a.store, &fakeDirectory{entries: map[string]auth.LDAPUser{
		"carol": {DN: "uid=carol,ou=people,dc=example,dc=com", Email: carol.Email}, bystander.Email: {Email: bystander.Email}}})
	a.store.mu.Lock()
	b := bystander
	a.store.users[b.ID] = &b
	a.store.mu.Unlock()
	if status := syncNow(); status != http.StatusOK || !a.store.user(carol.ID).IsActive {
		t.Errorf("sync = %d, carol active %t; want 200 and left as she is", status, a.store.user(carol.ID).IsActive)
	}
}

// TestLDAPLogin_ASecondFactorAccountGetsItsRolesOnlyAfterItsCode: the password step of
// an account with a second factor leaves its name and roles alone, so another entry
// with its address can neither rename it nor replace its roles with its own groups';
// the owner's code applies the owner's entry's name and roles.
func TestLDAPLogin_ASecondFactorAccountGetsItsRolesOnlyAfterItsCode(t *testing.T) {
	t.Parallel()
	admins, ops := uuid.New(), uuid.New()
	f := newSecondFactorDirectoryFixture(t, []byte(`{"cn=admins,dc=example,dc=com":"`+admins.String()+`","cn=ops,dc=example,dc=com":"`+ops.String()+`"}`))
	f.other.Groups, f.other.DisplayName = []string{"cn=admins,dc=example,dc=com"}, "Evil"
	f.own.Groups, f.own.DisplayName = []string{"cn=ops,dc=example,dc=com"}, "Carol Owner"
	untouched := func(when string) {
		t.Helper()
		if n := len(f.store.named("RevokeAllUserRoles")) + len(f.store.named("AssignUserRole")); n != 0 || f.store.user(f.carol.ID).DisplayName != "Carol" {
			t.Fatalf("%s: %d role writes, name %q; want none, Carol", when, n, f.store.user(f.carol.ID).DisplayName)
		}
	}
	f.directory("evil", f.other)
	epochFirstStep(t, f.a, "evil")
	untouched("after another entry's password step")
	f.directory("carol", f.own)
	token := epochFirstStep(t, f.a, "carol")
	untouched("after the owner's password step")
	if resp := f.a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, token, f.plain)); resp.StatusCode != http.StatusOK {
		t.Fatalf("verify-login = %d", resp.StatusCode)
	}
	assigned := f.store.named("AssignUserRole")
	if len(assigned) != 1 || assigned[0].args[1] != ops || f.store.user(f.carol.ID).DisplayName != "Carol Owner" {
		t.Errorf("after the code: role writes %v, name %q; want the owner's ops role and name", assigned, f.store.user(f.carol.ID).DisplayName)
	}
}
