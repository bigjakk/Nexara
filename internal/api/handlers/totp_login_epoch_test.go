package handlers

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp/totp"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The second factor widens the window between a credential check and the session it earns
// from milliseconds to the five minutes a pending token lives. The password step records the
// epoch it read in the pending token, and the session the second step creates is conditional
// on THAT, not on the epoch of the user row the second step re-reads, which a password change,
// a sign-out of all devices or a deactivation has just moved.

// epochTOTPUser is a local account with a second factor, and the plain secret to
// generate its codes from.
func epochTOTPUser(t *testing.T, email string, epoch int64) (db.User, string) {
	t.Helper()
	encrypted, _, plain, err := auth.NewTOTPService(totpTestKey).GenerateSecret(email)
	if err != nil {
		t.Fatalf("generate a TOTP secret: %v", err)
	}
	u := epochUser(t, email, epoch)
	u.TotpSecret = pgtype.Text{String: encrypted, Valid: true}
	return u, plain
}

// epochVerifyBody is the body of the second step with a valid code.
func epochVerifyBody(t *testing.T, token, plainSecret string) string {
	t.Helper()
	code, err := totp.GenerateCode(plainSecret, time.Now())
	if err != nil {
		t.Fatalf("generate a code: %v", err)
	}
	return `{"totp_pending_token":"` + token + `","code":"` + code + `"}`
}

// epochFirstStep runs the password step and returns the pending token.
func epochFirstStep(t *testing.T, a *epochApp, email string) string {
	t.Helper()
	resp := a.post(t, "/auth/login", epochLoginBody(email))
	body := decodeObject(t, resp)
	if resp.StatusCode != http.StatusOK || body["totp_required"] != true {
		t.Fatalf("the password step answered %d %v, want a TOTP challenge", resp.StatusCode, body)
	}
	token, _ := body["totp_pending_token"].(string)
	if token == "" {
		t.Fatalf("no pending token: %v", body)
	}
	if tok, ok := body["access_token"]; ok && tok != "" {
		t.Fatalf("the password step issued an access token before the second factor: %v", body)
	}
	return token
}

// TestTOTPLogin_ThePendingTokenCarriesThePasswordStepsEpoch pins the write half:
// what the password step stores is the epoch of the row it read — and the audit
// action the second step will record.
func TestTOTPLogin_ThePendingTokenCarriesThePasswordStepsEpoch(t *testing.T) {
	user, _ := epochTOTPUser(t, epochLoginEmail, 5)
	a := newEpochApp(t, newEpochStore(user), epochOptions{totp: true})

	token := epochFirstStep(t, a, epochLoginEmail)

	raw, err := a.redis.Get("totp:pending:" + token)
	if err != nil {
		t.Fatalf("the pending token was not stored: %v", err)
	}
	var pending struct {
		UserID      string `json:"user_id"`
		AuditAction string `json:"audit_action"`
		AuthEpoch   *int64 `json:"auth_epoch"`
	}
	if err := json.Unmarshal([]byte(raw), &pending); err != nil {
		t.Fatalf("pending data is not JSON: %v: %s", err, raw)
	}
	if pending.UserID != user.ID.String() || pending.AuditAction != "login" {
		t.Errorf("pending data = %+v, want the user and the login action", pending)
	}
	if pending.AuthEpoch == nil || *pending.AuthEpoch != 5 {
		t.Errorf("pending epoch = %v, want 5: the epoch the password check read", pending.AuthEpoch)
	}
	// Nothing was created by the first step.
	if n := len(a.store.named("CreateSessionAtEpoch")); n != 0 {
		t.Errorf("%d session inserts at the password step", n)
	}
}

// staleTokenMessage is the whole of what a pending token the account has outgrown is
// told: the answer an expired token gets, word for word, so that it is no oracle.
const staleTokenMessage = "Invalid or expired pending token"

// TestTOTPLogin_AStalePendingTokenIsRefusedBeforeAnyCodeIsSpent is the early half of the
// protection against a revoke-all between the two steps: the password is right at step one,
// the user changes it while the code is typed, and step two, whose re-read shows a DIFFERENT
// epoch from the pending token's, refuses the token before it looks at the code, for each kind
// of code. So no single-use recovery code is spent on a login that cannot be allowed, no
// failure is counted (a stale token cannot lock an account out or test codes against it), and
// the token is destroyed, the answer being the expired-token text word for word. No session
// insert is reached, which makes this killable on its own; the refusal is audited as a refused
// sign-in. The late half is TestTOTPLogin_ARevokeAllBetweenTheStepsRefusesTheSession.
func TestTOTPLogin_AStalePendingTokenIsRefusedBeforeAnyCodeIsSpent(t *testing.T) {
	logs := captureProductionLog(t)

	lands := []struct {
		name string
		land func(u *db.User)
	}{
		{"a password change, or a sign-out of all devices: the epoch moves", func(u *db.User) { u.AuthEpoch++ }},
		{"two revoke-alls", func(u *db.User) { u.AuthEpoch += 2 }},
	}
	kinds := []string{"a valid code", "a wrong code", "a recovery code"}

	for _, l := range lands {
		for _, kind := range kinds {
			t.Run(l.name+" / "+kind, func(t *testing.T) {
				user, plain := epochTOTPUser(t, epochLoginEmail, 5)
				store := newEpochStore(user)
				recoveryPlain, recoveryHashes, err := auth.NewTOTPService(totpTestKey).GenerateRecoveryCodes(1)
				if err != nil {
					t.Fatalf("generate a recovery code: %v", err)
				}
				store.recoveryCodes[user.ID] = []db.ListRecoveryCodesRow{{ID: uuid.New(), CodeHash: recoveryHashes[0]}}
				a := newEpochApp(t, store, epochOptions{totp: true})

				token := epochFirstStep(t, a, epochLoginEmail)
				store.mutate(user.ID, l.land)

				var body string
				switch kind {
				case "a valid code":
					body = epochVerifyBody(t, token, plain)
				case "a wrong code":
					body = `{"totp_pending_token":"` + token + `","code":"` + epochWrongCode(t, plain) + `"}`
				default:
					body = `{"totp_pending_token":"` + token + `","recovery_code":"` + recoveryPlain[0] + `"}`
				}
				resp := a.post(t, "/auth/totp/verify-login", body)
				out := authRequireStatus(t, resp, http.StatusUnauthorized)
				if msg, _ := out["message"].(string); msg != staleTokenMessage {
					t.Errorf("message = %q, want exactly %q: a stale token must read as an expired one", msg, staleTokenMessage)
				}
				epochRequireNothingIssued(t, resp, out)
				if n := len(store.named("CreateSessionAtEpoch")); n != 0 {
					t.Errorf("%d session inserts were attempted for a token the account had outgrown: the refusal must come first", n)
				}
				if n := len(store.sessionsOf(user.ID)); n != 0 {
					t.Errorf("%d session rows were created", n)
				}

				// Nothing was spent.
				if n := store.spentRecoveryCodeCount(); n != 0 {
					t.Errorf("%d recovery codes were spent on a login that was refused", n)
				}
				if left := store.recoveryCodesOf(user.ID); len(left) != 1 {
					t.Errorf("%d recovery codes left, want the 1 the user had", len(left))
				}
				if a.redis.Exists("totp:user:fail:"+user.ID.String()) || a.redis.Exists("totp:user:lock:"+user.ID.String()) {
					t.Error("a failure or a lockout was recorded against the user for a token that was refused before its code was read")
				}
				if n := len(store.named("ListRecoveryCodes")); n != 0 {
					t.Errorf("the recovery codes were listed %d times: the code must not be looked at", n)
				}

				// The token is gone, and a replay is told what an expired token is told.
				if a.redis.Exists("totp:pending:"+token) || a.redis.Exists("totp:attempts:"+token) {
					t.Error("the pending token or its attempt counter survived a refusal: it can never succeed")
				}
				again := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, token, plain))
				if again.StatusCode != http.StatusUnauthorized {
					t.Errorf("a replay answered %d, want 401", again.StatusCode)
				}

				// The refusal is audited as a refused sign-in, never as a login.
				if got := store.auditActions(); !reflect.DeepEqual(got, []string{signInRefusedAuditAction}) {
					t.Errorf("audit actions = %v, want [%s]", got, signInRefusedAuditAction)
				}
				if d := store.auditDetailsWritten(); len(d) != 1 || !strings.Contains(d[0], `"email":"`+epochLoginEmail+`"`) || !strings.Contains(d[0], `"ip":`) {
					t.Errorf("audit details = %v, want the email and the ip", d)
				}
			})
		}
	}
	if out := logs.String(); strings.Contains(out, racePassword) {
		t.Error("the password reached the log")
	}
}

// TestTOTPLogin_TheEarlyRefusalAuditRunsOnAFollowUpDeadline: the refusal of a stale
// pending token is audited as a refused sign-in, and the row is written under a
// deadline of its own so that a slow audit insert cannot hold the 401 the comparison
// has already decided. The insert is stalled and the bound is a tenth of a second.
func TestTOTPLogin_TheEarlyRefusalAuditRunsOnAFollowUpDeadline(t *testing.T) {
	user, plain := epochTOTPUser(t, epochLoginEmail, 5)
	store := newEpochStore(user)
	store.stallOn["InsertAuditLog"] = true
	a := newEpochApp(t, store, epochOptions{totp: true, followUpTimeout: 100 * time.Millisecond})

	token := epochFirstStep(t, a, epochLoginEmail)
	store.mutate(user.ID, func(u *db.User) { u.AuthEpoch++ })

	resp := a.send(t, http.MethodPost, "/auth/totp/verify-login", epochVerifyBody(t, token, plain), nil, 10*time.Second)
	out := authRequireStatus(t, resp, http.StatusUnauthorized)
	if msg, _ := out["message"].(string); msg != staleTokenMessage {
		t.Errorf("message = %q, want %q", msg, staleTokenMessage)
	}
	requireStallCutShort(t, store, "InsertAuditLog")
	epochRequireNothingIssued(t, resp, out)
	if n := len(store.named("CreateSessionAtEpoch")); n != 0 {
		t.Errorf("%d session inserts for a token that was refused", n)
	}
}

// epochWrongCode is a six-digit code that is not the secret's code right now.
func epochWrongCode(t *testing.T, plainSecret string) string {
	t.Helper()
	valid, err := totp.GenerateCode(plainSecret, time.Now())
	if err != nil {
		t.Fatalf("generate a code: %v", err)
	}
	if valid == "000000" {
		return "111111"
	}
	return "000000"
}

// TestTOTPLogin_ARevokeAllBetweenTheStepsRefusesTheSession is the late half, and the
// independent proof of the conditional INSERT: the early comparison cannot see a revoke-all
// that lands after the second step's re-read, and the hook rows put one exactly there, so the
// code is validated, the insert is attempted at the pending token's epoch, and the insert
// alone refuses it. The control is the two steps with nothing landing; an account inactive at
// the re-read is told 403, as it always was. The refusals issue nothing and are audited as
// refused sign-ins.
func TestTOTPLogin_ARevokeAllBetweenTheStepsRefusesTheSession(t *testing.T) {
	logs := captureProductionLog(t)

	tests := []struct {
		name string
		// land is the writer that commits between the steps, or — with hook — right
		// behind the second step's re-read of the user.
		land func(u *db.User)
		hook bool
		want int
	}{
		{name: "control: nothing lands", want: http.StatusOK},
		{
			name: "the account is deactivated: the second step's own check answers 403",
			land: func(u *db.User) { u.AuthEpoch++; u.IsActive = false },
			want: http.StatusForbidden,
		},
		{
			name: "the epoch moves right behind the second step's re-read, which therefore saw the old one",
			land: func(u *db.User) { u.AuthEpoch++ },
			hook: true,
			want: http.StatusUnauthorized,
		},
		{
			name: "the account is deactivated right behind the re-read: the insert refuses it by itself",
			land: func(u *db.User) { u.IsActive = false },
			hook: true,
			want: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, plain := epochTOTPUser(t, epochLoginEmail, 5)
			store := newEpochStore(user)
			a := newEpochApp(t, store, epochOptions{totp: true})

			token := epochFirstStep(t, a, epochLoginEmail)
			if tt.land != nil {
				if tt.hook {
					store.after["GetUserByID"] = func(s *epochStore) { tt.land(s.users[user.ID]) }
				} else {
					store.mutate(user.ID, tt.land)
				}
			}

			resp := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, token, plain))
			body := authRequireStatus(t, resp, tt.want)
			if tt.want == http.StatusOK {
				inserts := store.named("CreateSessionAtEpoch")
				if len(inserts) != 1 || epochArg(t, inserts[0]) != 5 {
					t.Fatalf("session inserts = %v, want exactly one, against epoch 5: the one the password step read", inserts)
				}
				if live := store.liveSessionsOf(user.ID); len(live) != 1 {
					t.Errorf("%d live sessions, want 1", len(live))
				}
				if got := store.auditActions(); len(got) != 1 || got[0] != "login" {
					t.Errorf("audit actions = %v, want [login]", got)
				}
				return
			}

			epochRequireNothingIssued(t, resp, body)
			epochRequireNoLoginAudit(t, store)
			if n := len(store.sessionsOf(user.ID)); n != 0 {
				t.Errorf("%d session rows were created for a refused sign-in", n)
			}
			if tt.want == http.StatusUnauthorized {
				if msg, _ := body["message"].(string); !strings.Contains(msg, "sign in again") || !strings.Contains(msg, "sessions were ended") {
					t.Errorf("message = %q, want the insert's refusal, which tells the user to sign in again", msg)
				}
				inserts := store.named("CreateSessionAtEpoch")
				if len(inserts) != 1 || epochArg(t, inserts[0]) != 5 {
					t.Errorf("session inserts = %v, want exactly one, against epoch 5 — the pending token's", inserts)
				}
				if got := store.auditActions(); !reflect.DeepEqual(got, []string{signInRefusedAuditAction}) {
					t.Errorf("audit actions = %v, want [%s]", got, signInRefusedAuditAction)
				}
				// The token was spent by the attempt that validated its code.
				again := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, token, plain))
				if again.StatusCode != http.StatusUnauthorized {
					t.Errorf("a replay answered %d, want 401", again.StatusCode)
				}
				if got := store.named("CreateSessionAtEpoch"); len(got) != 1 {
					t.Errorf("the replay reached the insert again (%d inserts in all)", len(got))
				}
			}
		})
	}
	if out := logs.String(); strings.Contains(out, racePassword) {
		t.Error("the password reached the log")
	}
}

// TestTOTPLogin_APendingTokenWithoutAnEpochIsRefusedBeforeAnyCodeIsSpent: a token minted by
// a release that recorded no epoch cannot be tied to the password step's check, so the
// session it would create could not be conditional on anything. It is refused as an expired
// token and destroyed, before the user is read or any code looked at (a single-use recovery
// code must not be burned on a login never going to be allowed): the wrong code the row sends
// still hears "expired", and the failure counter does not move.
func TestTOTPLogin_APendingTokenWithoutAnEpochIsRefusedBeforeAnyCodeIsSpent(t *testing.T) {
	user, plain := epochTOTPUser(t, epochLoginEmail, 0)
	a := newEpochApp(t, newEpochStore(user), epochOptions{totp: true})

	legacy, _ := json.Marshal(map[string]string{"user_id": user.ID.String(), "audit_action": "login"})
	if err := a.redis.Set("totp:pending:legacy-token", string(legacy)); err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{
		`{"totp_pending_token":"legacy-token","code":"000000"}`,
		epochVerifyBody(t, "legacy-token", plain),
	} {
		// Re-seed: the first attempt destroys it.
		if err := a.redis.Set("totp:pending:legacy-token", string(legacy)); err != nil {
			t.Fatal(err)
		}
		resp := a.post(t, "/auth/totp/verify-login", body)
		out := authRequireStatus(t, resp, http.StatusUnauthorized)
		if msg, _ := out["message"].(string); !strings.Contains(msg, "Invalid or expired pending token") {
			t.Errorf("message = %q, want the answer an expired token gets", msg)
		}
		epochRequireNothingIssued(t, resp, out)
		if a.redis.Exists("totp:pending:legacy-token") {
			t.Error("the pending token survived: it can never succeed and must not stay to be tried again")
		}
		if a.redis.Exists("totp:user:fail:" + user.ID.String()) {
			t.Error("the failure counter moved: a code was checked on a token that must be refused first")
		}
	}
	if n := len(a.store.named("GetUserByID")) + len(a.store.named("CreateSessionAtEpoch")); n != 0 {
		t.Errorf("%d statements were sent for a token that carries no epoch: it must be refused before the database is touched", n)
	}
}

// TestTOTPLogin_ADatabaseFailureOfTheInsertIsNotARefusal: the second step answers a
// failed insert as every sign-in does. The pending token is spent either way, which
// is the pre-existing behaviour of this step (it is consumed before the tokens are
// issued), so the user signs in again from the password.
func TestTOTPLogin_ADatabaseFailureOfTheInsertIsNotARefusal(t *testing.T) {
	user, plain := epochTOTPUser(t, epochLoginEmail, 5)
	store := newEpochStore(user)
	a := newEpochApp(t, store, epochOptions{totp: true})
	token := epochFirstStep(t, a, epochLoginEmail)
	store.failOn["CreateSessionAtEpoch"] = errRaceTransient

	resp := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, token, plain))
	body := authRequireStatus(t, resp, http.StatusServiceUnavailable)
	if msg, _ := body["message"].(string); strings.Contains(msg, "sessions were ended") {
		t.Errorf("message = %q blames the credential for a database failure", msg)
	}
	epochRequireNothingIssued(t, resp, body)
	epochRequireNoLoginAudit(t, store)
	if got := store.auditActions(); len(got) != 0 {
		t.Errorf("audit actions = %v: a database failure is not a compromise signal and writes no row", got)
	}
}

// The SSO sign-in of a user with a second factor goes through the same pending
// token: its epoch is the callback's, carried by the exchange code, and not the one
// the exchange re-read.
func TestOIDCExchange_ATOTPUsersPendingTokenCarriesTheCallbacksEpoch(t *testing.T) {
	user, _ := epochTOTPUser(t, epochLoginEmail, 3)
	user.AuthSource, user.PasswordHash = "oidc", ""
	a := newEpochApp(t, newEpochStore(user), epochOptions{totp: true})
	code := epochSeedExchange(t, a, user)
	a.store.bump(user.ID) // a revoke-all lands after the callback

	resp := a.post(t, "/auth/oidc/token-exchange", `{"code":"`+code+`"}`)
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusOK || body["totp_required"] != true {
		t.Fatalf("the exchange answered %d %v, want a TOTP challenge", resp.StatusCode, body)
	}
	token, _ := body["totp_pending_token"].(string)
	raw, err := a.redis.Get("totp:pending:" + token)
	if err != nil {
		t.Fatalf("the pending token was not stored: %v", err)
	}
	var pending struct {
		AuditAction string `json:"audit_action"`
		AuthEpoch   *int64 `json:"auth_epoch"`
	}
	if err := json.Unmarshal([]byte(raw), &pending); err != nil {
		t.Fatalf("pending data is not JSON: %v", err)
	}
	if pending.AuthEpoch == nil || *pending.AuthEpoch != 3 {
		t.Errorf("pending epoch = %v, want 3: the callback's, not the re-read user's 4", pending.AuthEpoch)
	}
	if pending.AuditAction != "oidc_login" {
		t.Errorf("pending audit action = %q, want oidc_login", pending.AuditAction)
	}
	if n := len(a.store.named("CreateSessionAtEpoch")); n != 0 {
		t.Errorf("%d session inserts before the second factor", n)
	}
}
