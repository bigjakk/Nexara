package handlers

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestEmail_CaseIsIgnoredAtSignInAndRegistration: an address typed in another case signs
// in to the account and spends that account's sign-in budget, not a fresh one; and a new
// account whose address differs from an existing one only by case is refused 409.
func TestEmail_CaseIsIgnoredAtSignInAndRegistration(t *testing.T) {
	t.Parallel()
	admin := epochUser(t, epochAdminEmail, 0)
	store := newEpochStore(epochUser(t, epochLoginEmail, 0), admin)
	a := newEpochApp(t, store, epochOptions{})
	if resp, body := a.loginAs(t, "ALICE@Example.com", true); resp.StatusCode != http.StatusOK {
		t.Fatalf("signing in as ALICE@Example.com = %d %v, want 200", resp.StatusCode, body)
	}
	for range loginLockoutThreshold {
		a.loginAs(t, "Alice@example.com", false)
	}
	if resp, _ := a.loginAs(t, epochLoginEmail, true); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("after ten failures typed in another case, the account's own spelling = %d, want 429", resp.StatusCode)
	}

	before := len(store.users)
	if resp := registerAs(t, a, admin.ID, registerAdminBody("ALICE@example.com")); resp.StatusCode != http.StatusConflict {
		t.Errorf("registering ALICE@example.com beside alice@example.com = %d, want 409", resp.StatusCode)
	}
	if len(store.users) != before {
		t.Error("an account differing only by case was created")
	}
}

// TestLDAPProvision_RefusesAnAddressAnotherAccountHasInAnotherCase: a directory user
// whose address differs from a local account's only by case is not provisioned.
func TestLDAPProvision_RefusesAnAddressAnotherAccountHasInAnotherCase(t *testing.T) {
	t.Parallel()
	store := newEpochStore(epochUser(t, epochLoginEmail, 0))
	a := newEpochApp(t, store, epochOptions{})
	a.auth.SetLDAPHandler(&LDAPHandler{queries: a.auth.queries})
	var ok bool
	callProvision(t, a, func(c fiber.Ctx) error {
		_, ok = a.auth.provisionLDAPUser(c, db.LdapConfig{}, &auth.LDAPUser{Email: "Alice@Example.com"}, "Alice@Example.com")
		return c.SendStatus(http.StatusNoContent)
	})
	if ok || len(store.named("CreateLDAPUser")) != 0 {
		t.Errorf("provisioned %t, %d accounts created; want refused, none", ok, len(store.named("CreateLDAPUser")))
	}
}

// TestOIDCProvision_RefusesAnAddressAnotherAccountHasInAnotherCase: an SSO identity
// whose address differs from a local account's only by case gets no account and no
// exchange code, and the login page's provisioning message, as an exact duplicate does.
func TestOIDCProvision_RefusesAnAddressAnotherAccountHasInAnotherCase(t *testing.T) {
	t.Parallel()
	store := newEpochStore(epochUser(t, epochLoginEmail, 0))
	a := newEpochApp(t, store, epochOptions{})
	var failure string
	callProvision(t, a, func(c fiber.Ctx) error {
		_, _, failure = a.oidc.provisionAndStoreExchange(c, db.OidcConfig{AutoProvision: true}, &auth.OIDCUserInfo{Email: "Alice@Example.com"})
		return c.SendStatus(http.StatusNoContent)
	})
	if failure != "User provisioning failed" || len(store.named("CreateOIDCUser")) != 0 || len(a.redis.Keys()) != 0 {
		t.Errorf("failure %q, %d accounts created, codes %v; want refused, none", failure, len(store.named("CreateOIDCUser")), a.redis.Keys())
	}
}
