package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestOIDCProvision_FindsTheAccountByItsProviderIdentity: which account a verified SSO
// sign-in signs in to. The bound account wins over the address; an unlinked SSO account
// found by address is linked; an account bound to another identity, or of another
// source, is never signed in to or linked; a new account is bound at creation.
func TestOIDCProvision_FindsTheAccountByItsProviderIdentity(t *testing.T) {
	t.Parallel()
	account := func(email, source, issuer, subject string) db.User {
		u := epochUser(t, email, 0)
		u.AuthSource, u.OidcIssuer, u.OidcSubject = source, issuer, subject
		return u
	}
	const carol = "carol@example.com"
	const mismatch = oidcIdentityMismatchMessage
	for _, tc := range []struct {
		name  string
		users []db.User
		// race links the account the address names to another subject right after the
		// lookup: a first sign-in of another identity landing in between.
		race bool
		// want is the index of the account signed in to (-1: a new one), or failure.
		want    int
		failure string
		// unlinked: signed in to, but left unlinked (a later check can still refuse it).
		unlinked bool
	}{
		{name: "the bound account, though the address now names another",
			users: []db.User{account(epochLoginEmail, "oidc", epochIssuer, epochSubject), account(carol, "oidc", "", "")}, want: 0},
		{name: "an unlinked SSO account found by address is linked",
			users: []db.User{account(carol, "oidc", "", "")}, want: 0},
		{name: "an account bound to another subject is refused",
			users: []db.User{account(carol, "oidc", epochIssuer, "subject-of-another")}, failure: mismatch},
		{name: "an account bound under another issuer is refused",
			users: []db.User{account(carol, "oidc", "https://old-sso.example.com", epochSubject)}, failure: mismatch},
		{name: "a local account with the address is never linked",
			users: []db.User{account(carol, "local", "", "")}, failure: "User provisioning failed"},
		{name: "a first sign-in that loses the link to another identity is refused",
			users: []db.User{account(carol, "oidc", "", "")}, race: true, failure: mismatch},
		{name: "a new account is bound to the identity", want: -1},
		{name: "an unlinked account with a second factor is not linked before it",
			users: []db.User{withTOTP(account(carol, "oidc", "", ""))}, unlinked: true},
		{name: "a deactivated unlinked account is not linked",
			users: []db.User{deactivated(account(carol, "oidc", "", ""))}, unlinked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newEpochStore(tc.users...)
			if tc.race {
				store.after["GetUserByEmailAndSource"] = func(s *epochStore) {
					for _, u := range s.users {
						u.OidcIssuer, u.OidcSubject = epochIssuer, "subject-of-another"
					}
				}
			}
			a := newEpochApp(t, store, epochOptions{})
			info := &auth.OIDCUserInfo{Issuer: epochIssuer, Subject: epochSubject, Email: carol, DisplayName: "Example User"}
			var user db.User
			var failure string
			callProvision(t, a, func(c fiber.Ctx) error {
				user, _, failure = a.oidc.provisionAndStoreExchange(c, db.OidcConfig{AutoProvision: true}, info)
				return c.SendStatus(http.StatusNoContent)
			})

			if failure != tc.failure {
				t.Fatalf("failure = %q, want %q", failure, tc.failure)
			}
			if tc.failure != "" {
				requireNoLinkWritten(t, store, tc.users)
				if len(a.redis.Keys()) != 0 || len(store.users) != len(tc.users) {
					t.Errorf("codes %v, %d accounts; want none stored, none created", a.redis.Keys(), len(store.users)-len(tc.users))
				}
				if tc.failure == mismatch {
					requireMismatchAudited(t, store, tc.users[0].ID)
				}
				return
			}
			if tc.want >= 0 && user.ID != tc.users[tc.want].ID {
				t.Errorf("signed in to %v, want %v", user.ID, tc.users[tc.want].ID)
			}
			got := store.user(user.ID)
			if tc.unlinked {
				if got.OidcSubject != "" {
					t.Errorf("the account was linked to %q before its sign-in could be refused", got.OidcSubject)
				}
				return
			}
			if got.OidcIssuer != epochIssuer || got.OidcSubject != epochSubject {
				t.Errorf("the account holds %q %q, want bound to the identity", got.OidcIssuer, got.OidcSubject)
			}
			if tc.want >= 0 && got.Email != tc.users[tc.want].Email || tc.want < 0 && got.Email != carol {
				t.Errorf("the account's address is %q: rewritten, or not the new account's", got.Email)
			}
		})
	}
}

func withTOTP(u db.User) db.User {
	u.TotpSecret = pgtype.Text{String: "secret", Valid: true}
	return u
}

func deactivated(u db.User) db.User {
	u.IsActive = false
	return u
}

// requireNoLinkWritten fails if a refused sign-in linked any account to its identity.
func requireNoLinkWritten(t *testing.T, store *epochStore, before []db.User) {
	t.Helper()
	for _, u := range before {
		if got := store.user(u.ID); got.OidcSubject == epochSubject && u.OidcSubject != epochSubject {
			t.Errorf("account %s was linked to the refused identity", u.Email)
		}
	}
}

// requireMismatchAudited: one login_failed row under the account, with a fixed reason
// and nothing the provider asserted.
func requireMismatchAudited(t *testing.T, store *epochStore, id uuid.UUID) {
	t.Helper()
	rows, details := store.auditRowsWritten(), store.auditDetailsWritten()
	if len(rows) != 1 || rows[0].action != "login_failed" || rows[0].actor != id || rows[0].resourceID != id.String() {
		t.Fatalf("audit rows %v, want one login_failed under %v", rows, id)
	}
	if !strings.Contains(details[0], `"reason":"identity_mismatch"`) || strings.Contains(details[0], epochSubject) ||
		strings.Contains(details[0], epochIssuer) {
		t.Errorf("audit details %s: want the fixed reason and no asserted subject or issuer", details[0])
	}
}
