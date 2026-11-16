package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// bystander is an LDAP account the directory still has, so that a test whose one user
// is missing is not the mass miss that changes nothing (tooManyMissing).
var bystander = db.User{ID: uuid.MustParse("b7000000-0000-4000-8000-000000000001"), Email: "bystander@example.com", IsActive: true, AuthSource: "ldap"}

func bystanderEntry() map[string]auth.LDAPUser {
	return map[string]auth.LDAPUser{bystander.Email: {Email: bystander.Email}}
}

// mailFilter is a user filter that matches the address, so an account no sign-in has
// named yet is searched by it.
const mailFilter = "(|(uid={{username}})(mail={{username}}))"

// ldapSyncApp is a harness app whose LDAPHandler searches dir, and sync runs Sync Now
// against the store's config, answering its status.
func ldapSyncApp(t *testing.T, store *epochStore, dir *fakeDirectory) (*epochApp, *LDAPHandler, func() int) {
	t.Helper()
	if store.ldapConfig == nil {
		store.ldapConfig = &db.LdapConfig{ID: uuid.New(), UserFilter: mailFilter, EmailAttribute: "mail"}
	}
	a := newEpochApp(t, store, epochOptions{})
	h := &LDAPHandler{queries: a.users.queries, directory: func(auth.LDAPConfig) ldapDirectory { return *dir }}
	params, err := apischema.Properties{"id": {Type: apischema.String}}.Validate(map[string]any{"id": store.ldapConfig.ID.String()})
	if err != nil {
		t.Fatalf("params: %v", err)
	}
	a.probe = func(c fiber.Ctx) error { return h.Sync(c, params) }
	return a, h, func() int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.Header.Set("X-Test-Acting-User", uuid.NewString()) // the administrator, for the audit row
		resp, err := a.app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second})
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
}

// TestLDAPSync_ADisabledUserHasNoSessionToReviveWhenReEnabled: a sync that cannot
// find a user deactivates them, and must END their sessions as an admin's deactivation
// does — flipping is_active alone only suspends them, and the next sync's re-enable would
// bring every one back with no sign-in.
func TestLDAPSync_ADisabledUserHasNoSessionToReviveWhenReEnabled(t *testing.T) {
	t.Parallel()
	user := db.User{ID: uuid.New(), Email: "alice@example.com", IsActive: true, Role: "user", AuthSource: "ldap"}
	store := newEpochStore(user, bystander)
	store.sessions = []db.Session{{ID: uuid.New(), UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)}}
	_, _, sync := ldapSyncApp(t, store, &fakeDirectory{entries: bystanderEntry()})
	if status := sync(); status != http.StatusOK {
		t.Fatalf("sync: status %d", status)
	}
	if store.user(user.ID).IsActive {
		t.Fatal("precondition: the sync did not deactivate the user")
	}
	store.mutate(user.ID, func(u *db.User) { u.IsActive = true }) // the next sync finds them again
	if live := store.liveSessionsOf(user.ID); len(live) != 0 {
		t.Errorf("re-enabled after a sync's deactivation, the user still has %d live session(s)", len(live))
	}
}

// fakeDirectory answers every search with found, or when found is nil with err, or
// with "not in the directory" when err is nil too. With names set, only a search for
// one of them finds anything.
type fakeDirectory struct {
	found *auth.LDAPUser
	err   error
	names map[string]bool
	panic bool
	// entries, when set, is the entry each name finds; any other name finds nobody.
	entries map[string]auth.LDAPUser
}

func (d fakeDirectory) SearchUser(name string) (*auth.LDAPUser, error) {
	if d.panic {
		panic("search panicked")
	}
	if d.entries != nil {
		if e, ok := d.entries[name]; ok {
			return &e, nil
		}
		return nil, fmt.Errorf("%w: no entries matched", auth.ErrLDAPUserNotFound)
	}
	switch {
	case d.names != nil && !d.names[name]:
	case d.found != nil:
		return d.found, nil
	case d.err != nil:
		return nil, d.err
	}
	return nil, fmt.Errorf("%w: no entries matched", auth.ErrLDAPUserNotFound)
}

// TestLDAPSync_AFailedRevokeIsRetriedBeforeTheReEnable: when ending the sessions
// fails at the deactivation, nothing retries it while the user is disabled, so the
// sync that re-enables them must end those sessions first, or it revives them.
func TestLDAPSync_AFailedRevokeIsRetriedBeforeTheReEnable(t *testing.T) {
	t.Parallel()
	user := db.User{ID: uuid.New(), Email: "alice@example.com", IsActive: true, Role: "user", AuthSource: "ldap"}
	store := newEpochStore(user, bystander)
	store.sessions = []db.Session{{ID: uuid.New(), UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)}}
	dir := &fakeDirectory{entries: bystanderEntry()}
	_, _, syncStatus := ldapSyncApp(t, store, dir)
	sync := func() {
		t.Helper()
		if status := syncStatus(); status != http.StatusOK {
			t.Fatalf("sync: status %d", status)
		}
	}

	store.mu.Lock()
	store.failOn["RevokeAllUserSessions"] = errors.New("revoke failed")
	store.mu.Unlock()
	sync() // the user is gone: disabled, and the revoke fails
	store.mu.Lock()
	delete(store.failOn, "RevokeAllUserSessions")
	store.mu.Unlock()
	if store.user(user.ID).IsActive || len(store.liveSessionsOf(user.ID)) != 1 {
		t.Fatal("precondition: the first sync should disable the user and leave the session unrevoked")
	}

	dir.entries[user.Email] = auth.LDAPUser{Email: user.Email}
	sync() // the user is back: re-enabled
	if !store.user(user.ID).IsActive {
		t.Fatal("precondition: the second sync did not re-enable the user")
	}
	if live := store.liveSessionsOf(user.ID); len(live) != 0 {
		t.Errorf("re-enabled, the user has %d session(s) from before the deactivation still live", len(live))
	}
}

// TestLDAPSync_OnlyASearchThatFoundNobodyDeactivates: a search that failed says nothing
// about whether the user is in the directory, so it deactivates nobody; a directory that
// cannot be reached stops the sync and answers 502.
func TestLDAPSync_OnlyASearchThatFoundNobodyDeactivates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err    error
		status int
	}{
		{fmt.Errorf("%w: size limit exceeded", auth.ErrLDAPSearchFailed), http.StatusOK},
		{fmt.Errorf("%w: connection refused", auth.ErrLDAPConnection), http.StatusBadGateway},
	} {
		user := db.User{ID: uuid.New(), Email: "alice@example.com", IsActive: true, Role: "user", AuthSource: "ldap"}
		store := newEpochStore(user)
		_, _, sync := ldapSyncApp(t, store, &fakeDirectory{err: tc.err})
		if status := sync(); status != tc.status || !store.user(user.ID).IsActive {
			t.Errorf("%v: status %d, active %t; want %d and still active", tc.err, status, store.user(user.ID).IsActive, tc.status)
		}
	}
}

// TestLDAPSync_AHandDeactivatedUserIsNotReEnabled: an account an administrator
// deactivated (every account inactive before the upgrade reads so) stays inactive when
// the directory still has it; only one a sync deactivated is re-enabled.
func TestLDAPSync_AHandDeactivatedUserIsNotReEnabled(t *testing.T) {
	t.Parallel()
	user := db.User{ID: uuid.New(), Email: "alice@example.com", IsActive: false, Role: "user", AuthSource: "ldap"}
	store := newEpochStore(user)
	_, _, sync := ldapSyncApp(t, store, &fakeDirectory{found: &auth.LDAPUser{Email: user.Email}})
	if status := sync(); status != http.StatusOK || store.user(user.ID).IsActive {
		t.Errorf("status %d, active %t: a sync re-enabled an account deactivated by hand", status, store.user(user.ID).IsActive)
	}
}

// TestLDAPSync_TheScheduleRunsWhenDueOneSyncAtATime: the scheduled sync runs once its
// interval has passed since the last sync, never with an interval of 0, is audited as
// the system account, and neither it nor Sync Now runs while another sync holds the lease.
func TestLDAPSync_TheScheduleRunsWhenDueOneSyncAtATime(t *testing.T) {
	t.Parallel()
	now := time.Now()
	store := newEpochStore(db.User{ID: uuid.New(), Email: "alice@example.com", IsActive: true, AuthSource: "ldap"})
	store.ldapConfig = &db.LdapConfig{ID: uuid.New(), Enabled: true, SyncIntervalMinutes: 60, UserFilter: mailFilter, EmailAttribute: "mail",
		LastSyncAt: pgtype.Timestamptz{Time: now.Add(-30 * time.Minute), Valid: true}}
	a, h, syncNow := ldapSyncApp(t, store, &fakeDirectory{found: &auth.LDAPUser{}})
	h.now = func() time.Time { return now }
	runs := func() int { return len(store.named("ListLDAPUsers")) }

	h.RunScheduledSync(context.Background())
	if runs() != 0 {
		t.Fatal("a sync ran 30 minutes into a 60-minute interval")
	}
	now = now.Add(31 * time.Minute)
	store.syncLeased = true
	h.RunScheduledSync(context.Background())
	if status := syncNow(); runs() != 0 || status != http.StatusConflict {
		t.Fatalf("with another sync running: %d syncs, Sync Now %d; want none, and 409", runs(), status)
	}
	store.syncLeased = false
	h.RunScheduledSync(context.Background())
	rows, _ := auditOf(a, "ldap_sync")
	if runs() != 1 || len(rows) != 1 || rows[0].actor != auth.SystemUserID {
		t.Errorf("once due: %d syncs, audit rows %v; want one, by the system account", runs(), rows)
	}
	store.ldapConfig.SyncIntervalMinutes = 0
	now = now.Add(24 * time.Hour)
	h.RunScheduledSync(context.Background())
	if runs() != 1 {
		t.Error("a sync ran with the interval at 0 (off)")
	}
}

// TestLDAPSync_SearchesWithTheNameThePersonSignsInWith: with a filter on the account
// name (sAMAccountName, uid), a person signs in as "alice" and is provisioned with the
// entry's address; the sync must search with "alice", which the filter matches, and not
// with the address, which it does not, or it deactivates every healthy user.
func TestLDAPSync_SearchesWithTheNameThePersonSignsInWith(t *testing.T) {
	t.Parallel()
	store := newEpochStore()
	store.ldapConfig = &db.LdapConfig{ID: uuid.New(), Enabled: true, UserFilter: "(sAMAccountName={{username}})", EmailAttribute: "mail"}
	entry := auth.LDAPUser{DN: "uid=alice,ou=people,dc=example,dc=com", Email: epochLoginEmail}
	a, _, sync := ldapSyncApp(t, store, &fakeDirectory{found: &entry, names: map[string]bool{"alice": true}})
	a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries, signIn: func(auth.LDAPConfig) (ldapSignInSession, error) {
		return fakeSignInDirectory{names: map[string]bool{"alice": true}, entry: entry, password: racePassword}, nil
	}})
	if resp, body := a.loginAs(t, "alice", true); resp.StatusCode != http.StatusOK {
		t.Fatalf("signing in as alice = %d %v", resp.StatusCode, body)
	}
	if status := sync(); status != http.StatusOK {
		t.Fatalf("sync: status %d", status)
	}
	for _, u := range store.users {
		if !u.IsActive {
			t.Errorf("the sync deactivated %s, who signs in as alice", u.Email)
		}
	}
}

// TestLDAPSync_AMassMissDeactivatesNobody: when most of the accounts searched for are
// missing (at least three), the sync changes nothing, answers 422 and is audited as
// failed; a minority missing is still deactivated.
func TestLDAPSync_AMassMissDeactivatesNobody(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		users, present int
		status         int
		disabled       int
	}{
		{4, 0, http.StatusUnprocessableEntity, 0},
		{4, 3, http.StatusOK, 1},
		{1, 0, http.StatusUnprocessableEntity, 0}, // the last account it can see
	} {
		entries := map[string]auth.LDAPUser{}
		users := make([]db.User, 0, tc.users)
		for i := range tc.users {
			u := db.User{ID: uuid.New(), Email: fmt.Sprintf("user%d@example.com", i), IsActive: true, AuthSource: "ldap"}
			if i < tc.present {
				entries[u.Email] = auth.LDAPUser{Email: u.Email}
			}
			users = append(users, u)
		}
		store := newEpochStore(users...)
		a, _, sync := ldapSyncApp(t, store, &fakeDirectory{entries: entries})
		status := sync()
		disabled := 0
		for _, u := range users {
			if !store.user(u.ID).IsActive {
				disabled++
			}
		}
		_, details := auditOf(a, "ldap_sync")
		if status != tc.status || disabled != tc.disabled || len(details) != 1 {
			t.Errorf("%d of %d present: status %d, %d disabled, audit %v; want %d, %d, one row", tc.present, tc.users, status, disabled, details, tc.status, tc.disabled)
		}
		if tc.present == 0 && (len(details) != 1 || !strings.Contains(details[0], `"reason":"too_many_missing"`)) {
			t.Errorf("the mass miss is not audited as too_many_missing: %v", details)
		}
	}
}

// TestLDAP_AFailedGroupSearchChangesNoRole: a group search that did not answer says
// nothing about the user's groups, so neither a sync nor a sign-in revokes or assigns a
// role (a revoke there strips hand-assigned and cluster-scoped roles too).
func TestLDAP_AFailedGroupSearchChangesNoRole(t *testing.T) {
	t.Parallel()
	user := db.User{ID: uuid.New(), Email: epochLoginEmail, IsActive: true, AuthSource: "ldap"}
	failed := &auth.LDAPUser{Email: user.Email, GroupLookupFailed: true}

	store := newEpochStore(user)
	_, _, sync := ldapSyncApp(t, store, &fakeDirectory{found: failed})
	if status := sync(); status != http.StatusOK || len(store.named("RevokeAllUserRoles")) != 0 {
		t.Errorf("sync: status %d, %d role revokes; want 200 and none", status, len(store.named("RevokeAllUserRoles")))
	}

	store = newEpochStore(user)
	a := newEpochApp(t, store, epochOptions{})
	a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries})
	var ok bool
	callProvision(t, a, func(c fiber.Ctx) error {
		_, ok = a.auth.provisionLDAPUser(c, db.LdapConfig{}, failed, user.Email)
		return c.SendStatus(http.StatusNoContent)
	})
	if !ok || len(store.named("RevokeAllUserRoles"))+len(store.named("AssignUserRole")) != 0 {
		t.Errorf("sign-in: signed in %t, roles touched %d times; want signed in, none", ok, len(store.named("RevokeAllUserRoles"))+len(store.named("AssignUserRole")))
	}
}

// TestLDAPSync_APanicInTheScheduledSyncIsRecovered: a panic in the sync (here the
// directory's) must not take the process down, and leaves the lease released.
func TestLDAPSync_APanicInTheScheduledSyncIsRecovered(t *testing.T) {
	t.Parallel()
	store := newEpochStore(db.User{ID: uuid.New(), Email: epochLoginEmail, IsActive: true, AuthSource: "ldap"})
	store.ldapConfig = &db.LdapConfig{ID: uuid.New(), Enabled: true, SyncIntervalMinutes: 60, UserFilter: mailFilter, EmailAttribute: "mail"}
	_, h, _ := ldapSyncApp(t, store, &fakeDirectory{panic: true})
	h.RunScheduledSync(context.Background())
	if store.syncLeased {
		t.Error("the lease is still held after the panic")
	}
}

// TestLDAPSync_TheLeaseIsShortRenewedAndNamedInThe409: the sync renews its lease before
// every account and stops, changing nothing, once it is no longer its own; and the 409 of
// a busy config says when the lease ends, so a stopped sync blocks Sync Now no longer.
func TestLDAPSync_TheLeaseIsShortRenewedAndNamedInThe409(t *testing.T) {
	t.Parallel()
	user := db.User{ID: uuid.New(), Email: epochLoginEmail, IsActive: true, AuthSource: "ldap"}
	store := newEpochStore(user)
	a, _, sync := ldapSyncApp(t, store, &fakeDirectory{})
	store.leaseLost = true
	if status := sync(); status == http.StatusOK || !store.user(user.ID).IsActive || len(store.named("RenewLDAPSyncLease")) != 1 {
		t.Errorf("with the lease lost: status %d, active %t, %d renewals; want a failure, active, one renewal",
			status, store.user(user.ID).IsActive, len(store.named("RenewLDAPSyncLease")))
	}

	store.syncLeased = true
	store.ldapConfig.SyncLeaseExpiresAt = pgtype.Timestamptz{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Valid: true}
	resp := a.send(t, http.MethodGet, "/probe", "", map[string]string{"X-Test-Acting-User": uuid.NewString()}, 10*time.Second)
	body := decodeObject(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(messageOf(body), "03:04:05 UTC") {
		t.Errorf("busy: %d %v, want 409 naming when the lease ends", resp.StatusCode, body)
	}
	if got := ldapSyncLease; got > 5*time.Minute {
		t.Errorf("the lease lasts %v past a renewal: a stopped sync blocks the next one that long", got)
	}
}

// TestUserFilterMatchesAddress: an unnamed account is searched by its address only when
// the filter is an equality on the address attribute; anything else it cannot read is
// "cannot tell", which leaves the account alone.
func TestUserFilterMatchesAddress(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		filter, attr string
		want         filterMatch
	}{
		{mailFilter, "mail", filterMatches},
		{"(&(objectClass=user)( Mail = {{username}} ))", "MAIL", filterMatches},
		{"(sAMAccountName={{username}})", "mail", filterDoesNotMatch},
		{"(uid={{username}})", "userPrincipalName", filterDoesNotMatch},
		{"(mail={{username}}*)", "mail", filterCannotTell},
		{"(&(uid={{username}})(!(mail={{username}})))", "mail", filterCannotTell},
		{mailFilter, "", filterCannotTell},
	} {
		if got := userFilterMatchesAddress(tc.filter, tc.attr); got != tc.want {
			t.Errorf("%q with %q = %d, want %d", tc.filter, tc.attr, got, tc.want)
		}
	}
}

// TestLDAPSync_AnAccountNotSignedInSinceIsLeftAloneByAFilterOnAccountNames: with a filter
// on sAMAccountName, ten accounts provisioned before the upgrade, and six of them signed
// in since, no sync deactivates anyone: the four the filter cannot find by address are
// skipped until they sign in, not counted missing.
func TestLDAPSync_AnAccountNotSignedInSinceIsLeftAloneByAFilterOnAccountNames(t *testing.T) {
	t.Parallel()
	entries := map[string]auth.LDAPUser{}
	users := make([]db.User, 0, 10)
	for i := range 10 {
		name := fmt.Sprintf("user%d", i)
		u := db.User{ID: uuid.New(), Email: name + "@example.com", IsActive: true, AuthSource: "ldap"}
		entries[name] = auth.LDAPUser{DN: "uid=" + name + ",dc=example,dc=com", Email: u.Email}
		users = append(users, u)
	}
	store := newEpochStore(users...)
	store.ldapConfig = &db.LdapConfig{ID: uuid.New(), UserFilter: "(sAMAccountName={{username}})", EmailAttribute: "mail"}
	_, _, sync := ldapSyncApp(t, store, &fakeDirectory{entries: entries})
	for round, signedIn := range []int{0, 6} {
		for i := range signedIn {
			store.mutate(users[i].ID, func(u *db.User) { u.LdapLoginName = fmt.Sprintf("user%d", i) })
		}
		sync()
		for _, u := range users {
			if !store.user(u.ID).IsActive {
				t.Errorf("sync %d (%d signed in since): %s was deactivated", round+1, signedIn, u.Email)
			}
		}
	}
}

// TestLDAPSync_ADirectoryDeactivatedAccountRecoversThroughASignIn: an account a sync
// deactivated wrongly keeps its sign-in name at a refused sign-in (the bind succeeded),
// so the next sync finds it by that name and re-enables it.
func TestLDAPSync_ADirectoryDeactivatedAccountRecoversThroughASignIn(t *testing.T) {
	t.Parallel()
	user := db.User{ID: uuid.New(), Email: epochLoginEmail, AuthSource: "ldap", DeactivatedByDirectory: true}
	store := newEpochStore(user)
	store.ldapConfig = &db.LdapConfig{ID: uuid.New(), Enabled: true, UserFilter: "(sAMAccountName={{username}})", EmailAttribute: "mail"}
	entry := auth.LDAPUser{DN: "uid=alice,dc=example,dc=com", Email: user.Email}
	a, _, sync := ldapSyncApp(t, store, &fakeDirectory{entries: map[string]auth.LDAPUser{"alice": entry}})
	a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries, signIn: func(auth.LDAPConfig) (ldapSignInSession, error) {
		return fakeSignInDirectory{names: map[string]bool{"alice": true}, entry: entry, password: racePassword}, nil
	}})
	if resp, _ := a.loginAs(t, "alice", true); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an inactive account's sign-in = %d, want 401", resp.StatusCode)
	}
	if status := sync(); status != http.StatusOK || !store.user(user.ID).IsActive {
		t.Errorf("sync: status %d, active %t; want the account re-enabled", status, store.user(user.ID).IsActive)
	}
}

// TestLDAPSync_AReusedNameFindsSomeoneElse: a stored name the directory gave to a new
// hire finds an entry whose address is not the account's. The account is missing, so
// it is deactivated, and the new hire's groups are never applied to it.
func TestLDAPSync_AReusedNameFindsSomeoneElse(t *testing.T) {
	t.Parallel()
	departed := db.User{ID: uuid.New(), Email: epochLoginEmail, IsActive: true, AuthSource: "ldap", LdapLoginName: "alice"}
	other := db.User{ID: uuid.New(), Email: epochAdminEmail, IsActive: true, AuthSource: "ldap"}
	store := newEpochStore(departed, other)
	_, _, sync := ldapSyncApp(t, store, &fakeDirectory{entries: map[string]auth.LDAPUser{
		"alice":         {Email: "newhire@example.com", Groups: []string{"cn=admins,dc=example,dc=com"}},
		epochAdminEmail: {Email: epochAdminEmail},
	}})
	if status := sync(); status != http.StatusOK || store.user(departed.ID).IsActive {
		t.Errorf("status %d, active %t: the account was kept on someone else's entry", status, store.user(departed.ID).IsActive)
	}
	for _, st := range store.named("RevokeAllUserRoles") {
		if st.args[0] == departed.ID {
			t.Error("the new hire's groups were applied to the departed account")
		}
	}
}
