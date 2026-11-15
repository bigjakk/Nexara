package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestLDAPSync_ADisabledUserHasNoSessionToReviveWhenReEnabled: a sync that cannot
// find a user (here: a directory that refuses the connection, which reads as "gone")
// deactivates them, and must END their sessions as an admin's deactivation does —
// flipping is_active alone only suspends them, and the next sync's re-enable would
// bring every one back with no sign-in.
func TestLDAPSync_ADisabledUserHasNoSessionToReviveWhenReEnabled(t *testing.T) {
	t.Parallel()
	user := db.User{ID: uuid.New(), Email: "alice@example.com", IsActive: true, Role: "user", AuthSource: "ldap"}
	store := newEpochStore(user)
	store.sessions = []db.Session{{ID: uuid.New(), UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)}}
	configID := uuid.New()
	store.ldapConfig = &db.LdapConfig{ID: configID, ServerUrl: "ldap://127.0.0.1:1"}
	a := newEpochApp(t, store, epochOptions{})
	h := &LDAPHandler{queries: a.users.queries}
	params, err := apischema.Properties{"id": {Type: apischema.String}}.Validate(map[string]any{"id": configID.String()})
	if err != nil {
		t.Fatalf("params: %v", err)
	}
	a.probe = func(c fiber.Ctx) error { return h.Sync(c, params) }

	resp, err := a.app.Test(httptest.NewRequest(http.MethodGet, "/probe", nil), fiber.TestConfig{Timeout: 30 * time.Second})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sync: %v, status %v", err, resp)
	}
	if store.user(user.ID).IsActive {
		t.Fatal("precondition: the sync did not deactivate the user")
	}
	store.mutate(user.ID, func(u *db.User) { u.IsActive = true }) // the next sync finds them again
	if live := store.liveSessionsOf(user.ID); len(live) != 0 {
		t.Errorf("re-enabled after a sync's deactivation, the user still has %d live session(s)", len(live))
	}
}

// fakeDirectory answers every search with found, or with an error when found is nil.
type fakeDirectory struct{ found *auth.LDAPUser }

func (d fakeDirectory) SearchUser(string) (*auth.LDAPUser, error) {
	if d.found == nil {
		return nil, errors.New("not in the directory")
	}
	return d.found, nil
}

// TestLDAPSync_AFailedRevokeIsRetriedBeforeTheReEnable: when ending the sessions
// fails at the deactivation, nothing retries it while the user is disabled, so the
// sync that re-enables them must end those sessions first, or it revives them.
func TestLDAPSync_AFailedRevokeIsRetriedBeforeTheReEnable(t *testing.T) {
	t.Parallel()
	user := db.User{ID: uuid.New(), Email: "alice@example.com", IsActive: true, Role: "user", AuthSource: "ldap"}
	store := newEpochStore(user)
	store.sessions = []db.Session{{ID: uuid.New(), UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)}}
	configID := uuid.New()
	store.ldapConfig = &db.LdapConfig{ID: configID}
	a := newEpochApp(t, store, epochOptions{})
	dir := &fakeDirectory{}
	h := &LDAPHandler{queries: a.users.queries, directory: func(auth.LDAPConfig) ldapDirectory { return *dir }}
	params, err := apischema.Properties{"id": {Type: apischema.String}}.Validate(map[string]any{"id": configID.String()})
	if err != nil {
		t.Fatalf("params: %v", err)
	}
	a.probe = func(c fiber.Ctx) error { return h.Sync(c, params) }
	sync := func() {
		t.Helper()
		resp, err := a.app.Test(httptest.NewRequest(http.MethodGet, "/probe", nil), fiber.TestConfig{Timeout: 30 * time.Second})
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("sync: %v, status %v", err, resp)
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

	dir.found = &auth.LDAPUser{Email: user.Email}
	sync() // the user is back: re-enabled
	if !store.user(user.ID).IsActive {
		t.Fatal("precondition: the second sync did not re-enable the user")
	}
	if live := store.liveSessionsOf(user.ID); len(live) != 0 {
		t.Errorf("re-enabled, the user has %d session(s) from before the deactivation still live", len(live))
	}
}
