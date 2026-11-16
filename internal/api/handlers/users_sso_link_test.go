package handlers

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestResetSSOLink_ClearsTheLinkAndEndsTheSessionsInOneTransaction: an administrator's
// reset — of one account, or of every SSO account at once — clears a linked account's
// identity and ends its sessions, audited without the identity; a reset that cannot end
// the sessions changes nothing; an account with no link, and the caller's own, are
// refused.
func TestResetSSOLink_ClearsTheLinkAndEndsTheSessionsInOneTransaction(t *testing.T) {
	t.Parallel()
	linked := func(s *epochStore) {
		for _, u := range s.users {
			if u.Email == epochLoginEmail {
				u.AuthSource, u.OidcIssuer, u.OidcSubject = "oidc", epochIssuer, epochSubject
			}
		}
	}
	for _, tc := range []struct {
		name  string
		tweak func(*epochStore)
		self  bool
		all   bool // DELETE /oidc/links
		want  int
	}{
		{name: "a linked SSO account", tweak: linked, want: http.StatusOK},
		{name: "every linked account at once", tweak: linked, all: true, want: http.StatusOK},
		{name: "the sessions cannot be ended", want: http.StatusInternalServerError, tweak: func(s *epochStore) {
			linked(s)
			s.failOn["RevokeAllUserSessions"] = errors.New("revoke failed")
		}},
		{name: "an account with no link", want: http.StatusBadRequest},
		{name: "the caller's own", tweak: linked, self: true, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newDeactivationFixture(t, epochOptions{}, tc.tweak)
			acting := f.admin.ID
			if tc.self {
				acting = f.target.ID
			}
			before := f.a.store.user(f.target.ID)
			path, action, resource := "/users/"+f.target.ID.String()+"/sso-link", "user_sso_link_reset", f.target.ID.String()
			if tc.all {
				path, action, resource = "/oidc/links", "oidc_links_reset", "links"
			}
			resp := f.a.send(t, http.MethodDelete, path, "",
				map[string]string{"X-Test-Acting-User": acting.String()}, 20*time.Second)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}

			u, live := f.a.store.user(f.target.ID), f.a.store.liveSessionsOf(f.target.ID)
			if tc.want != http.StatusOK {
				if u.OidcSubject != before.OidcSubject || len(live) != 2 || len(f.a.store.auditActions()) != 0 {
					t.Errorf("subject %q, %d live sessions, audits %v; want all as before", u.OidcSubject, len(live), f.a.store.auditActions())
				}
				return
			}
			if u.OidcIssuer != "" || u.OidcSubject != "" || len(live) != 0 || u.AuthEpoch != before.AuthEpoch+1 {
				t.Errorf("issuer %q subject %q, %d live sessions, epoch %d; want cleared, none, %d",
					u.OidcIssuer, u.OidcSubject, len(live), u.AuthEpoch, before.AuthEpoch+1)
			}
			for _, s := range f.session {
				if f.a.redis.Exists("nexara:session:" + s.ID.String()) {
					t.Errorf("the Redis row of revoked session %v is still there", s.ID)
				}
			}
			rows, details := f.a.store.auditRowsWritten(), f.a.store.auditDetailsWritten()
			if len(rows) != 1 || rows[0].action != action || rows[0].resourceID != resource ||
				rows[0].actor != f.admin.ID || strings.Contains(details[0], epochSubject) {
				t.Errorf("audit %v %v; want one %s of %s, without the subject", rows, details, action, resource)
			}
		})
	}
}

// TestUserList_SaysWhichAccountsAreLinkedOnlyToAUserManager: sso_linked names the
// unclaimed SSO accounts, so a caller without manage:user (a Viewer) gets no such field.
func TestUserList_SaysWhichAccountsAreLinkedOnlyToAUserManager(t *testing.T) {
	t.Parallel()
	for role, want := range map[string]bool{"admin": true, "viewer": false} {
		f := newDeactivationFixture(t, epochOptions{}, nil)
		resp := f.a.send(t, http.MethodGet, "/users", "",
			map[string]string{"X-Test-Acting-User": f.admin.ID.String(), "X-Test-Acting-Role": role}, 20*time.Second)
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || strings.Contains(string(raw), `"sso_linked"`) != want {
			t.Errorf("%s: %d %s; want sso_linked present %t", role, resp.StatusCode, raw, want)
		}
	}
}
