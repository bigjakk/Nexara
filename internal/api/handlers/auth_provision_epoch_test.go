package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The two sign-ins that need an outside party, an identity provider (SSO) and a directory
// (LDAP), cannot be driven end to end here. What can be driven is everything after that party
// has said yes, which is where the epoch is handed on: the account the provisioning returned is
// the account whose epoch the SSO exchange code records, and the row the directory login returns
// carries the epoch it read. A hand-off that dropped it (a code recording 0, a user rebuilt from
// its id) would refuse every sign-in of any user whose epoch has ever moved.

// callProvision runs fn inside a request, so that it has the fiber context the
// provisioning functions read the request context from.
func callProvision(t *testing.T, a *epochApp, fn func(c fiber.Ctx) error) {
	t.Helper()
	a.probe = fn
	resp := a.send(t, http.MethodGet, "/probe", "", nil, 10*time.Second)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("the probe answered %d, want 204", resp.StatusCode)
	}
}

// TestOIDCCallback_TheExchangeCodeCarriesTheEpochOfTheProvisionedUser drives
// provisionAndStoreExchange, which is Callback from the moment the identity provider
// has vouched for the user. The code it stores must record the epoch of the account
// the provisioning RETURNED: the lookup's row, or — when the provider's display name
// differed — the one UpdateOIDCUserProfile … RETURNING * wrote back, or a freshly
// created account's 0.
func TestOIDCCallback_TheExchangeCodeCarriesTheEpochOfTheProvisionedUser(t *testing.T) {
	existing := func() db.User {
		u := epochUser(t, epochLoginEmail, 3)
		u.AuthSource, u.PasswordHash, u.DisplayName = "oidc", "", "Example User"
		return u
	}

	// exchange stores what the callback stores and returns what it recorded.
	exchange := func(t *testing.T, a *epochApp, cfg db.OidcConfig, info *auth.OIDCUserInfo) (user db.User, data map[string]string, failure string) {
		t.Helper()
		var code string
		callProvision(t, a, func(c fiber.Ctx) error {
			user, code, failure = a.oidc.provisionAndStoreExchange(c, cfg, info)
			return c.SendStatus(http.StatusNoContent)
		})
		if failure != "" {
			return user, nil, failure
		}
		raw, err := a.redis.Get("oidc:exchange:" + code)
		if err != nil {
			t.Fatalf("the exchange code was not stored: %v", err)
		}
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatalf("exchange data is not a JSON object: %v: %s", err, raw)
		}
		return user, data, ""
	}

	t.Run("an existing account: the epoch of the row the lookup read", func(t *testing.T) {
		u := existing()
		a := newEpochApp(t, newEpochStore(u), epochOptions{})

		user, data, failure := exchange(t, a, db.OidcConfig{}, &auth.OIDCUserInfo{Email: u.Email, DisplayName: u.DisplayName})

		if failure != "" {
			t.Fatalf("provisioning failed: %q", failure)
		}
		if data["user_id"] != u.ID.String() || data["auth_epoch"] != "3" {
			t.Errorf("exchange data = %v, want the account and epoch 3", data)
		}
		if user.ID != u.ID || user.AuthEpoch != 3 {
			t.Errorf("provisioned user = %v epoch %d, want the existing account at epoch 3", user.ID, user.AuthEpoch)
		}
	})

	t.Run("a changed display name: the epoch of the row UpdateOIDCUserProfile returned", func(t *testing.T) {
		u := existing()
		store := newEpochStore(u)
		// A revoke-all lands between the lookup and the profile update; the update's
		// RETURNING row is the later read, and its epoch is the one recorded.
		store.after["GetUserByEmailAndSource"] = func(s *epochStore) { s.users[u.ID].AuthEpoch++ }
		a := newEpochApp(t, store, epochOptions{})

		_, data, failure := exchange(t, a, db.OidcConfig{}, &auth.OIDCUserInfo{Email: u.Email, DisplayName: "Renamed By The Provider"})

		if failure != "" {
			t.Fatalf("provisioning failed: %q", failure)
		}
		if data["auth_epoch"] != "4" {
			t.Errorf("exchange data = %v, want epoch 4, the row the profile update returned", data)
		}
		if got := store.user(u.ID).DisplayName; got != "Renamed By The Provider" {
			t.Errorf("display name = %q, want the provider's", got)
		}
	})

	t.Run("a first sign-in provisions the account, at epoch 0", func(t *testing.T) {
		a := newEpochApp(t, newEpochStore(), epochOptions{})

		user, data, failure := exchange(t, a, db.OidcConfig{AutoProvision: true}, &auth.OIDCUserInfo{Email: "carol@example.com", DisplayName: "Carol"})

		if failure != "" {
			t.Fatalf("provisioning failed: %q", failure)
		}
		if user.Email != "carol@example.com" || user.AuthSource != "oidc" || data["user_id"] != user.ID.String() || data["auth_epoch"] != "0" {
			t.Errorf("provisioned %+v, exchange data %v, want the new oidc account and epoch 0", user, data)
		}
	})

	t.Run("an unknown user with auto-provisioning off: nothing is stored", func(t *testing.T) {
		a := newEpochApp(t, newEpochStore(), epochOptions{})

		_, _, failure := exchange(t, a, db.OidcConfig{AutoProvision: false}, &auth.OIDCUserInfo{Email: "carol@example.com"})

		if failure != "User provisioning failed" {
			t.Errorf("failure = %q, want the login page's provisioning message", failure)
		}
		if keys := a.redis.Keys(); len(keys) != 0 {
			t.Errorf("an exchange code was stored for a user that was not provisioned: %v", keys)
		}
	})

	t.Run("the code is single use and lives five seconds, as before", func(t *testing.T) {
		u := existing()
		a := newEpochApp(t, newEpochStore(u), epochOptions{})
		var code string
		callProvision(t, a, func(c fiber.Ctx) error {
			_, code, _ = a.oidc.provisionAndStoreExchange(c, db.OidcConfig{}, &auth.OIDCUserInfo{Email: u.Email, DisplayName: u.DisplayName})
			return c.SendStatus(http.StatusNoContent)
		})
		if ttl := a.redis.TTL("oidc:exchange:" + code); ttl <= 0 || ttl > oidcExchangeTTL {
			t.Errorf("ttl = %v, want at most %v", ttl, oidcExchangeTTL)
		}
	})
}

// TestLDAPProvision_TheRowItReturnsCarriesTheEpochItRead drives provisionLDAPUser,
// which is tryLDAPLogin from the moment the directory has said yes. The user it
// returns is the one every later step is conditional on, so its AuthEpoch has to be
// that of the LAST read of the account: the lookup's, the profile update's
// RETURNING row when the directory's display name differed, or a created account's 0.
// A deactivated account is refused, and refused before its roles are synced.
func TestLDAPProvision_TheRowItReturnsCarriesTheEpochItRead(t *testing.T) {
	existing := func() db.User {
		u := epochUser(t, epochLoginEmail, 3)
		u.AuthSource, u.PasswordHash, u.DisplayName = "ldap", "", "Example User"
		return u
	}

	provision := func(t *testing.T, a *epochApp, info *auth.LDAPUser, typed string) (user db.User, ok bool) {
		t.Helper()
		a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries})
		callProvision(t, a, func(c fiber.Ctx) error {
			user, ok = a.auth.provisionLDAPUser(c, db.LdapConfig{}, info, typed)
			return c.SendStatus(http.StatusNoContent)
		})
		return user, ok
	}

	t.Run("an existing account: the epoch of the row the lookup read, and its roles are synced", func(t *testing.T) {
		u := existing()
		a := newEpochApp(t, newEpochStore(u), epochOptions{})

		user, ok := provision(t, a, &auth.LDAPUser{Email: u.Email, DisplayName: u.DisplayName}, "typed@example.com")

		if !ok {
			t.Fatal("the account was refused")
		}
		if user.ID != u.ID || user.AuthEpoch != 3 {
			t.Errorf("returned %v epoch %d, want the existing account at epoch 3", user.ID, user.AuthEpoch)
		}
		if n := len(a.store.named("RevokeAllUserRoles")); n != 1 {
			t.Errorf("the roles were synced %d times, want once", n)
		}
	})

	t.Run("a changed display name: the epoch of the row UpdateLDAPUserProfile returned", func(t *testing.T) {
		u := existing()
		store := newEpochStore(u)
		store.after["GetUserByEmailAndSource"] = func(s *epochStore) { s.users[u.ID].AuthEpoch++ }
		a := newEpochApp(t, store, epochOptions{})

		user, ok := provision(t, a, &auth.LDAPUser{Email: u.Email, DisplayName: "Renamed By The Directory"}, u.Email)

		if !ok {
			t.Fatal("the account was refused")
		}
		if user.AuthEpoch != 4 || user.DisplayName != "Renamed By The Directory" {
			t.Errorf("returned epoch %d name %q, want the profile update's row: epoch 4 and the directory's name", user.AuthEpoch, user.DisplayName)
		}
	})

	t.Run("the address the user typed is used when the directory reports none", func(t *testing.T) {
		u := existing()
		a := newEpochApp(t, newEpochStore(u), epochOptions{})

		user, ok := provision(t, a, &auth.LDAPUser{DisplayName: u.DisplayName}, u.Email)

		if !ok || user.ID != u.ID || user.AuthEpoch != 3 {
			t.Errorf("returned (%v, %t), want the account found by the typed address at epoch 3", user.ID, ok)
		}
	})

	t.Run("a first sign-in creates the account, at epoch 0", func(t *testing.T) {
		a := newEpochApp(t, newEpochStore(), epochOptions{})

		user, ok := provision(t, a, &auth.LDAPUser{Email: "carol@example.com", DisplayName: "Carol"}, "carol@example.com")

		if !ok || user.Email != "carol@example.com" || user.AuthSource != "ldap" || user.AuthEpoch != 0 || !user.IsActive {
			t.Errorf("returned (%+v, %t), want the new active ldap account at epoch 0", user, ok)
		}
	})

	t.Run("a deactivated account is refused, and its roles are not synced", func(t *testing.T) {
		u := existing()
		u.IsActive = false
		a := newEpochApp(t, newEpochStore(u), epochOptions{})

		user, ok := provision(t, a, &auth.LDAPUser{Email: u.Email, DisplayName: u.DisplayName}, u.Email)

		if ok || user.ID != (db.User{}).ID {
			t.Errorf("returned (%v, %t), want a refusal and no user", user.ID, ok)
		}
		if n := len(a.store.named("RevokeAllUserRoles")); n != 0 {
			t.Errorf("the roles of a deactivated account were synced %d times", n)
		}
	})
}
