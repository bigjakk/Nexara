package handlers

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestTOTP_AdminReset_EndsTheAccountsSessions: an administrator's reset of a user's
// second factor ends every session of that user, in the reset's own transaction, as
// the SSO link reset does; and a sign-in waiting at its second step is refused 401 with
// its pending token destroyed (it answered 500), even with a right code.
func TestTOTP_AdminReset_EndsTheAccountsSessions(t *testing.T) {
	t.Parallel()
	user, plain := epochTOTPUser(t, epochLoginEmail, 0)
	store := newEpochStore(user)
	a := newEpochApp(t, store, epochOptions{totp: true})
	signedIn := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, epochFirstStep(t, a, epochLoginEmail), plain))
	if signedIn.StatusCode != http.StatusOK || len(store.liveSessionsOf(user.ID)) != 1 {
		t.Fatalf("sign-in = %d with %d live sessions, want 200 and 1", signedIn.StatusCode, len(store.liveSessionsOf(user.ID)))
	}
	pending := epochFirstStep(t, a, epochLoginEmail)

	resp := a.send(t, http.MethodDelete, "/users/"+user.ID.String()+"/totp", "",
		map[string]string{"X-Test-Acting-User": uuid.NewString()}, 20*time.Second)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset = %d, want 200", resp.StatusCode)
	}
	if n := len(store.liveSessionsOf(user.ID)); n != 0 {
		t.Errorf("%d sessions still live after the reset, want none", n)
	}
	for _, st := range []string{"ClearTOTPSecret", "BumpUserAuthEpoch", "RevokeAllUserSessions"} {
		if got := store.named(st); len(got) != 1 || !got[0].inTx {
			t.Errorf("%s ran %d times (in a transaction: %v), want once, in the reset's", st, len(got), got)
		}
	}
	if !slices.Contains(store.auditActions(), "totp_admin_reset") {
		t.Errorf("audit actions = %v, want a totp_admin_reset row", store.auditActions())
	}

	if resp := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, pending, plain)); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the second step after the reset = %d, want 401", resp.StatusCode)
	}
	if a.redis.Exists("totp:pending:" + pending) {
		t.Error("the pending sign-in survived the reset")
	}
}
