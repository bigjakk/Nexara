package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// ssoSecondFactorStart runs an SSO sign-in up to its second factor, as the callback
// (provisionAndStoreExchange) and the exchange do, and returns the pending token.
func ssoSecondFactorStart(t *testing.T, a *epochApp, info *auth.OIDCUserInfo) string {
	t.Helper()
	var code string
	a.probe = func(c fiber.Ctx) error {
		var failure string
		if _, code, failure = a.oidc.provisionAndStoreExchange(c, *a.store.oidcConfig, info); failure != "" {
			t.Errorf("the callback refused: %q", failure)
		}
		return c.SendStatus(http.StatusNoContent)
	}
	given := cookiesNamed(a.send(t, http.MethodGet, "/probe", "", nil, 10*time.Second), oidcExchangeBinding.cookieName(false, code))
	if len(given) != 1 {
		t.Fatalf("the callback set bindings %v, want one", given)
	}
	resp := a.send(t, http.MethodPost, "/auth/oidc/token-exchange", `{"code":"`+code+`"}`,
		map[string]string{"Cookie": given[0].Name + "=" + given[0].Value}, 10*time.Second)
	body := decodeObject(t, resp)
	token, _ := body["totp_pending_token"].(string)
	if resp.StatusCode != http.StatusOK || token == "" {
		t.Fatalf("the exchange answered %d %v, want a TOTP challenge", resp.StatusCode, body)
	}
	return token
}

// TestSSOSignIn_IsFinishedOnlyOnceTheSecondFactorPasses: an unlinked SSO account with a
// second factor is linked, and its roles replaced from the provider's groups, only after
// the code passes, with the identity the callback verified; a wrong code changes nothing;
// and an account another identity took in between is refused with no session. An account
// already bound is synced at the callback, but its oidc_callback row waits for the code.
func TestSSOSignIn_IsFinishedOnlyOnceTheSecondFactorPasses(t *testing.T) {
	t.Parallel()
	adminRole, operatorRole := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name       string
		groups     []string
		wrongFirst bool
		taken      bool // another identity links the account between the callback and the code
		bound      bool // the account is bound to the identity already
		wantRole   uuid.UUID
	}{
		{name: "linked and synced after the code, untouched after a wrong one", groups: []string{"admins"},
			wrongFirst: true, wantRole: adminRole},
		{name: "dropped from the group mapped to Admin: the role is gone", groups: []string{"operators"},
			wantRole: operatorRole},
		{name: "another identity took the account: refused", groups: []string{"admins"}, taken: true},
		{name: "a bound account: its sign-in row waits for the code", groups: []string{"admins"},
			bound: true, wrongFirst: true, wantRole: adminRole},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			user, plain := epochTOTPUser(t, "carol@example.com", 2)
			user.AuthSource, user.PasswordHash = "oidc", ""
			if tc.bound {
				user.OidcIssuer, user.OidcSubject = epochIssuer, epochSubject
			}
			store := newEpochStore(user)
			mapping, _ := json.Marshal(map[string]string{"admins": adminRole.String(), "operators": operatorRole.String()})
			store.oidcConfig = &db.OidcConfig{ID: uuid.New(), Enabled: true, IssuerUrl: epochIssuer, GroupRoleMapping: mapping}
			a := newEpochApp(t, store, epochOptions{totp: true})
			token := ssoSecondFactorStart(t, a, &auth.OIDCUserInfo{Issuer: epochIssuer, Subject: epochSubject,
				Email: user.Email, DisplayName: "Carol From The Provider", Groups: tc.groups})

			untouched := func(when string) {
				t.Helper()
				if n := callbackRows(store); n != 0 {
					t.Errorf("%s: %d oidc_callback rows before the sign-in was finished", when, n)
				}
				if tc.bound {
					return
				}
				got := store.user(user.ID)
				if got.OidcSubject != "" && !tc.taken || len(store.named("RevokeAllUserRoles")) != 0 || got.DisplayName != user.DisplayName {
					t.Errorf("%s: subject %q, role syncs %d, name %q: changed before the second factor passed",
						when, got.OidcSubject, len(store.named("RevokeAllUserRoles")), got.DisplayName)
				}
			}
			untouched("after the callback")
			if n := linkRows(store); n != 0 {
				t.Errorf("%d oidc_identity_linked rows before the code", n)
			}
			if tc.wrongFirst {
				code, _ := totp.GenerateCode(plain, time.Now())
				wrong := string(rune('0'+(code[0]-'0'+1)%10)) + code[1:]
				resp := a.post(t, "/auth/totp/verify-login", `{"totp_pending_token":"`+token+`","code":"`+wrong+`"}`)
				epochRequireNothingIssued(t, resp, authRequireStatus(t, resp, http.StatusUnauthorized))
				untouched("after a wrong code")
			}
			if tc.taken {
				store.mutate(user.ID, func(u *db.User) { u.OidcIssuer, u.OidcSubject = epochIssuer, "subject-of-another" })
			}

			resp := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, token, plain))

			if tc.taken {
				body := authRequireStatus(t, resp, http.StatusForbidden)
				epochRequireNothingIssued(t, resp, body)
				if n := len(store.liveSessionsOf(user.ID)); n != 0 || body["message"] != oidcIdentityMismatchMessage {
					t.Errorf("%d sessions, answer %v; want none and the mismatch answer", n, body)
				}
				untouched("after a refused code")
				return
			}
			authRequireStatus(t, resp, http.StatusOK)
			got := store.user(user.ID)
			if got.OidcIssuer != epochIssuer || got.OidcSubject != epochSubject || got.DisplayName != "Carol From The Provider" {
				t.Errorf("the account holds %q %q %q, want linked to the callback's identity with its name", got.OidcIssuer, got.OidcSubject, got.DisplayName)
			}
			var assigned []uuid.UUID
			for _, st := range store.named("AssignUserRole") {
				assigned = append(assigned, st.args[1].(uuid.UUID))
			}
			if len(store.named("RevokeAllUserRoles")) != 1 || len(assigned) != 1 || assigned[0] != tc.wantRole {
				t.Errorf("roles revoked %d times, assigned %v; want replaced by %v alone", len(store.named("RevokeAllUserRoles")), assigned, tc.wantRole)
			}
			if want := map[bool]int{false: 1, true: 0}[tc.bound]; linkRows(store) != want {
				t.Errorf("%d oidc_identity_linked rows, want %d: one for a link written, none for an account already bound", linkRows(store), want)
			}
			if n := len(store.liveSessionsOf(user.ID)); n != 1 || callbackRows(store) != 1 {
				t.Errorf("%d sessions, %d oidc_callback rows; want 1 and 1", n, callbackRows(store))
			}
		})
	}
}

func callbackRows(store *epochStore) int { return auditActionCount(store, "oidc_callback") }

func linkRows(store *epochStore) int { return auditActionCount(store, "oidc_identity_linked") }

func auditActionCount(store *epochStore, action string) (n int) {
	for _, got := range store.auditActions() {
		if got == action {
			n++
		}
	}
	return n
}
