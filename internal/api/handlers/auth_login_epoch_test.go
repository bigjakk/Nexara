package handlers

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The tests of the sign-in paths against a revoke-all that lands after the
// credential check: password login, registration, and the SSO exchange. The TOTP
// second step has its own file (totp_login_epoch_test.go) and the deactivation
// transaction its own (users_deactivate_test.go); the harness is
// auth_epoch_harness_test.go.

// epochArg is the epoch argument of a CreateSessionAtEpoch statement: the last of
// its ten, in the order queries/sessions.sql generates them.
func epochArg(t *testing.T, st epochStatement) int64 {
	t.Helper()
	if len(st.args) != 10 {
		t.Fatalf("CreateSessionAtEpoch got %d arguments, want 10: %v", len(st.args), st.args)
	}
	got, ok := st.args[9].(int64)
	if !ok {
		t.Fatalf("the epoch argument is %T, want int64", st.args[9])
	}
	return got
}

// TestLogin_TheSessionIsCreatedAgainstTheEpochThePasswordCheckRead is the positive
// control for the refusals below, and the one place that pins WHICH epoch a password
// login hands the insert: the one on the row read beside the hash it compared.
func TestLogin_TheSessionIsCreatedAgainstTheEpochThePasswordCheckRead(t *testing.T) {
	user := epochUser(t, epochLoginEmail, 7)
	a := newEpochApp(t, newEpochStore(user), epochOptions{})

	resp := a.post(t, "/auth/login", epochLoginBody(epochLoginEmail))
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
	}
	if tok, _ := body["access_token"].(string); tok == "" {
		t.Errorf("no access token: %v", body)
	}
	cookies := refreshCookies(resp)
	if len(cookies) != 1 || cookies[0].Value == "" || cookieDeleted(cookies[0]) {
		t.Errorf("refresh cookies = %+v, want one live cookie", cookies)
	}

	inserts := a.store.named("CreateSessionAtEpoch")
	if len(inserts) != 1 {
		t.Fatalf("%d session inserts, want 1", len(inserts))
	}
	if got := epochArg(t, inserts[0]); got != 7 {
		t.Errorf("the session was created against epoch %d, want 7: the one the password check read", got)
	}
	if inserts[0].args[8] != user.ID {
		t.Errorf("the session was created for %v, want %v", inserts[0].args[8], user.ID)
	}
	if live := a.store.liveSessionsOf(user.ID); len(live) != 1 {
		t.Errorf("%d live sessions, want 1", len(live))
	}
	if got := a.store.auditActions(); len(got) != 1 || got[0] != "login" {
		t.Errorf("audit actions = %v, want [login]", got)
	}
}

// TestLogin_ARevokeAllAfterTheCredentialCheckRefusesTheSession drives the race the
// change exists for: the password has been compared (it took the better part of a
// hundred milliseconds), and before the session is inserted a revoke-all commits —
// the user changed their password in another tab, signed out of all devices, or an
// administrator deactivated the account. The hook lands the change right behind the
// read of the user, which is exactly where bcrypt leaves the window.
//
// Every row must answer 401 telling the user to sign in again, and must issue
// NOTHING: no access token, no cookie, no session row, no "login" audit row. And the
// session insert must have been ATTEMPTED, against the epoch the check read — the
// refusal is the insert's, not a pre-check the handler made on a re-read row, which
// would leave the narrower window between that read and the insert open.
func TestLogin_ARevokeAllAfterTheCredentialCheckRefusesTheSession(t *testing.T) {
	logs := captureProductionLog(t)

	tests := []struct {
		name string
		land func(s *epochStore, id uuid.UUID) // the writer that lands behind the read
		want int
	}{
		{name: "control: nothing lands", want: http.StatusOK},
		{
			name: "a password change, or a sign-out of all devices: the epoch moves",
			land: func(s *epochStore, id uuid.UUID) { s.users[id].AuthEpoch++ },
			want: http.StatusUnauthorized,
		},
		{
			name: "an administrator deactivates the account: the epoch moves and the account goes inactive",
			land: func(s *epochStore, id uuid.UUID) { s.users[id].AuthEpoch++; s.users[id].IsActive = false },
			want: http.StatusUnauthorized,
		},
		{
			name: "an account deactivated without a revoke, as a directory sync does: the epoch stays",
			land: func(s *epochStore, id uuid.UUID) { s.users[id].IsActive = false },
			want: http.StatusUnauthorized,
		},
		{
			name: "two revoke-alls: the epoch is two ahead, not one",
			land: func(s *epochStore, id uuid.UUID) { s.users[id].AuthEpoch += 2 },
			want: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := epochUser(t, epochLoginEmail, 4)
			store := newEpochStore(user)
			if tt.land != nil {
				store.after["GetUserByEmail"] = func(s *epochStore) { tt.land(s, user.ID) }
			}
			a := newEpochApp(t, store, epochOptions{})

			resp := a.post(t, "/auth/login", epochLoginBody(epochLoginEmail))
			body := decodeObject(t, resp)

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d (body %v)", resp.StatusCode, tt.want, body)
			}
			if tt.want == http.StatusOK {
				if live := store.liveSessionsOf(user.ID); len(live) != 1 {
					t.Errorf("%d live sessions for the control, want 1", len(live))
				}
				if got := store.auditActions(); !reflect.DeepEqual(got, []string{"login"}) {
					t.Errorf("audit actions = %v, want [login]", got)
				}
				return
			}

			msg, _ := body["message"].(string)
			if !strings.Contains(msg, "sign in again") || !strings.Contains(msg, "sessions were ended") {
				t.Errorf("message = %q, want it to say the sessions were ended and tell the user to sign in again", msg)
			}
			if strings.Contains(msg, "Invalid email or password") {
				t.Errorf("message = %q reads as a wrong password: the password was right", msg)
			}
			epochRequireNothingIssued(t, resp, body)
			epochRequireNoLoginAudit(t, store)
			// A correct credential that was refused is a compromise signal: a row of its
			// own, with the email and the ip like a login row, and never a login row.
			if got := store.auditActions(); !reflect.DeepEqual(got, []string{signInRefusedAuditAction}) {
				t.Errorf("audit actions = %v, want [%s]", got, signInRefusedAuditAction)
			}
			if d := store.auditDetailsWritten(); len(d) != 1 || !strings.Contains(d[0], `"email":"`+epochLoginEmail+`"`) || !strings.Contains(d[0], `"ip":`) {
				t.Errorf("audit details = %v, want the email and the ip", d)
			}
			if n := len(store.sessionsOf(user.ID)); n != 0 {
				t.Errorf("%d session rows were created for a refused sign-in", n)
			}
			inserts := store.named("CreateSessionAtEpoch")
			if len(inserts) != 1 {
				t.Fatalf("%d session inserts, want 1: the refusal must be the insert's", len(inserts))
			}
			if got := epochArg(t, inserts[0]); got != 4 {
				t.Errorf("the insert was made against epoch %d, want 4, the one the password check read", got)
			}
			if keys := a.redis.Keys(); len(keys) != 0 {
				t.Errorf("a refused sign-in left Redis rows %v", keys)
			}
			if line := logLinesFor(logs, user.ID.String()); !strings.Contains(line, "sign-in refused") {
				t.Errorf("the refusal was not logged with the user id; lines for it: %q", line)
			}
		})
	}
	if out := logs.String(); strings.Contains(out, racePassword) {
		t.Error("the password reached the log")
	}
}

// TestLogin_ADatabaseFailureOfTheInsertIsNotARefusal holds the three-outcome rule
// where the handler turns the insert's answer into a status: "no row" is the 401
// above, but the database failing is a 503 when it was away or too busy and a 500
// when it was a defect, and neither blames the credential — the message must not tell
// the user to sign in again as if their password had been replaced — nor issues
// anything.
func TestLogin_ADatabaseFailureOfTheInsertIsNotARefusal(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		want        int
		wantMessage string
	}{
		{"the database is away", errRaceTransient, http.StatusServiceUnavailable, "Nothing was issued, so you are not signed in"},
		{"a server that cannot serve now (a deadlock)", pgState("40P01"), http.StatusServiceUnavailable, "Nothing was issued, so you are not signed in"},
		{"a defect", errRaceBug, http.StatusInternalServerError, "Nothing was issued, so you are not signed in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := epochUser(t, epochLoginEmail, 0)
			store := newEpochStore(user)
			store.failOn["CreateSessionAtEpoch"] = tt.err
			a := newEpochApp(t, store, epochOptions{})

			resp := a.post(t, "/auth/login", epochLoginBody(epochLoginEmail))
			body := decodeObject(t, resp)

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d (body %v)", resp.StatusCode, tt.want, body)
			}
			msg, _ := body["message"].(string)
			if !strings.Contains(msg, tt.wantMessage) {
				t.Errorf("message = %q, want it to contain %q", msg, tt.wantMessage)
			}
			if strings.Contains(msg, "sessions were ended") {
				t.Errorf("message = %q blames the credential for a database failure", msg)
			}
			// A failed statement does not prove that no row was written (its reply can be
			// lost after the server committed), so the message must not claim that none was.
			if strings.Contains(msg, "No session was created") {
				t.Errorf("message = %q claims no session exists, which a failed statement cannot prove", msg)
			}
			epochRequireNothingIssued(t, resp, body)
			epochRequireNoLoginAudit(t, store)
			if got := store.auditActions(); len(got) != 0 {
				t.Errorf("audit actions = %v: a database failure is not a compromise signal and writes no row", got)
			}
			if n := len(store.sessionsOf(user.ID)); n != 0 {
				t.Errorf("%d session rows exist after a failed insert", n)
			}
		})
	}
}

// TestLogin_TheSessionInsertIsBounded: the insert is database work that decides the
// answer, and a pool with no free connection or a statement stuck on a lock would
// otherwise hold the request for as long as the context lasts, which in Fiber is for
// ever. The bound turns it into a 503 with nothing issued.
func TestLogin_TheSessionInsertIsBounded(t *testing.T) {
	user := epochUser(t, epochLoginEmail, 0)
	store := newEpochStore(user)
	store.stallOn["CreateSessionAtEpoch"] = true
	a := newEpochApp(t, store, epochOptions{dbTimeout: 100 * time.Millisecond})

	start := time.Now()
	resp := a.send(t, http.MethodPost, "/auth/login", epochLoginBody(epochLoginEmail), nil, 10*time.Second)
	elapsed := time.Since(start)
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
	}
	if elapsed > 5*time.Second {
		t.Errorf("answered after %v with a 100 ms bound", elapsed)
	}
	epochRequireNothingIssued(t, resp, body)
	epochRequireNoLoginAudit(t, store)
}

// TestRegister_TheFirstUserStillGetsItsSession pins that the first registration, the
// one sign-in with no earlier credential check, still creates its session — through
// the same conditional insert as every other, against the epoch the new account was
// created with (0) — and answers as it always did. It is the bootstrap's half of the
// split auth_register_admin_test.go describes: an administrator's registration, which
// has a session of its own already, signs nobody in.
func TestRegister_TheFirstUserStillGetsItsSession(t *testing.T) {
	store := newEpochStore()
	a := newEpochApp(t, store, epochOptions{})

	resp := a.post(t, "/auth/register", `{"email":"`+epochAdminEmail+`","password":"`+racePassword+`","display_name":"First User"}`)
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %v)", resp.StatusCode, body)
	}
	if tok, _ := body["access_token"].(string); tok == "" {
		t.Errorf("no access token: %v", body)
	}
	if exp, _ := body["expires_at"].(float64); exp <= 0 {
		t.Errorf("expires_at = %v, want the access token's expiry", body["expires_at"])
	}
	if user, _ := body["user"].(map[string]any); user["email"] != epochAdminEmail || user["role"] != "admin" {
		t.Errorf("user = %v, want the new administrator", body["user"])
	}
	if cookies := refreshCookies(resp); len(cookies) != 1 || cookies[0].Value == "" {
		t.Errorf("refresh cookies = %+v, want one", cookies)
	}
	if len(store.users) != 1 {
		t.Fatalf("%d accounts exist, want the one that was registered", len(store.users))
	}
	var created db.User
	for _, u := range store.users {
		created = *u
	}
	if created.Role != "admin" {
		t.Errorf("the first user's role = %q, want admin", created.Role)
	}
	inserts := store.named("CreateSessionAtEpoch")
	if len(inserts) != 1 {
		t.Fatalf("%d session inserts, want 1", len(inserts))
	}
	if got := epochArg(t, inserts[0]); got != 0 {
		t.Errorf("the session was created against epoch %d, want 0: the new account's", got)
	}
	if live := store.liveSessionsOf(created.ID); len(live) != 1 {
		t.Errorf("%d live sessions, want 1", len(live))
	}
	if got := store.auditActions(); len(got) != 1 || got[0] != "register" {
		t.Errorf("audit actions = %v, want [register]", got)
	}
	if got := store.poolStatementsWhileTx(); len(got) != 0 {
		t.Errorf("statements went to the POOL while the registration transaction was open: %v", got)
	}
}

// TestRegister_ASessionRefusedAfterTheAccountWasCreatedIsA401 covers the corner the
// uniform insert makes possible: the account exists, and the session cannot be
// created for it (deactivated in the instants since). The answer is the same 401
// telling the caller to sign in, with nothing issued and no "register" audit row for
// a registration that did not hand out a session.
func TestRegister_ASessionRefusedAfterTheAccountWasCreatedIsA401(t *testing.T) {
	store := newEpochStore()
	store.after["CreateUser"] = func(s *epochStore) {
		for _, u := range s.users {
			u.IsActive = false
		}
	}
	a := newEpochApp(t, store, epochOptions{})

	resp := a.post(t, "/auth/register", `{"email":"`+epochAdminEmail+`","password":"`+racePassword+`"}`)
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %v)", resp.StatusCode, body)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "sign in again") {
		t.Errorf("message = %q, want it to tell the caller to sign in", msg)
	}
	epochRequireNothingIssued(t, resp, body)
	epochRequireNoLoginAudit(t, store)
	if got := store.auditActions(); !reflect.DeepEqual(got, []string{signInRefusedAuditAction}) {
		t.Errorf("audit actions = %v, want [%s]: a refused sign-in is audited as one, and no register row is written", got, signInRefusedAuditAction)
	}
	if n := len(store.sessions); n != 0 {
		t.Errorf("%d session rows were created", n)
	}
}

// TestRegister_TheSessionInsertIsBounded is TestLogin_TheSessionInsertIsBounded for
// registration, whose insert used to run on the request context that never ends: a
// pool with no free connection would hold the registration for as long as it took.
// The account exists by then (it is committed first), so the answer says nothing was
// issued and the caller is not signed in — never that nothing was created.
func TestRegister_TheSessionInsertIsBounded(t *testing.T) {
	store := newEpochStore()
	store.stallOn["CreateSessionAtEpoch"] = true
	a := newEpochApp(t, store, epochOptions{dbTimeout: 100 * time.Millisecond})

	start := time.Now()
	resp := a.send(t, http.MethodPost, "/auth/register", `{"email":"`+epochAdminEmail+`","password":"`+racePassword+`"}`, nil, 10*time.Second)
	elapsed := time.Since(start)
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
	}
	if elapsed > 5*time.Second {
		t.Errorf("answered after %v with a 100 ms bound", elapsed)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "Nothing was issued, so you are not signed in") {
		t.Errorf("message = %q", msg)
	}
	epochRequireNothingIssued(t, resp, body)
	epochRequireNoLoginAudit(t, store)
	if len(store.users) != 1 {
		t.Errorf("%d accounts exist, want the one that was registered before the insert stalled", len(store.users))
	}
}

// TestLogin_ARedisThatIsDownDoesNotFailTheSignIn: the Redis row of a session is a
// mirror that nothing reads, written after the insert has committed. When the write
// fails the session exists in PostgreSQL either way, and answering "nothing was
// issued" for it would leave a live session with no token anybody holds. The sign-in
// succeeds, and the failure is logged — with the session id, never a token.
func TestLogin_ARedisThatIsDownDoesNotFailTheSignIn(t *testing.T) {
	logs := captureProductionLog(t)
	user := epochUser(t, epochLoginEmail, 2)
	a := newEpochApp(t, newEpochStore(user), epochOptions{})
	a.redis.Close()

	resp := a.post(t, "/auth/login", epochLoginBody(epochLoginEmail))
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v): a failed mirror must not fail a sign-in that created its session", resp.StatusCode, body)
	}
	if tok, _ := body["access_token"].(string); tok == "" {
		t.Errorf("no access token: %v", body)
	}
	cookies := refreshCookies(resp)
	if len(cookies) != 1 || cookies[0].Value == "" {
		t.Fatalf("refresh cookies = %+v, want one live cookie", cookies)
	}
	live := a.store.liveSessionsOf(user.ID)
	if len(live) != 1 {
		t.Fatalf("%d live sessions, want 1", len(live))
	}
	if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{"login"}) {
		t.Errorf("audit actions = %v, want [login]", got)
	}
	line := logLinesFor(logs, live[0].ID.String())
	if !strings.Contains(line, "Redis mirror") {
		t.Errorf("the failed mirror write was not logged with the session id; lines for it: %q", line)
	}
	if strings.Contains(logs.String(), cookies[0].Value) || strings.Contains(logs.String(), auth.HashToken(cookies[0].Value)) {
		t.Error("the refresh token, or its hash, reached the log")
	}
}

// TestLogin_TheRefusalAuditRunsOnAFollowUpDeadline: the audit row of a refused
// sign-in is written under a deadline of its own, so that a slow audit insert cannot
// hold the 401 the decision has already made.
func TestLogin_TheRefusalAuditRunsOnAFollowUpDeadline(t *testing.T) {
	user := epochUser(t, epochLoginEmail, 4)
	store := newEpochStore(user)
	store.after["GetUserByEmail"] = func(s *epochStore) { s.users[user.ID].AuthEpoch++ }
	store.stallOn["InsertAuditLog"] = true
	a := newEpochApp(t, store, epochOptions{followUpTimeout: 100 * time.Millisecond})

	resp := a.send(t, http.MethodPost, "/auth/login", epochLoginBody(epochLoginEmail), nil, 10*time.Second)
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %v)", resp.StatusCode, body)
	}
	requireStallCutShort(t, store, "InsertAuditLog")
	epochRequireNothingIssued(t, resp, body)
}

// epochSeedExchange stores the one-time code the way the callback does, through the real
// storeExchange.
func epochSeedExchange(t *testing.T, a *epochApp, user db.User) string {
	t.Helper()
	code, err := a.oidc.storeExchange(t.Context(), user)
	if err != nil {
		t.Fatalf("storeExchange: %v", err)
	}
	return code
}

// TestOIDCExchange_TheSessionIsCreatedAgainstTheEpochTheCallbackRead is the SSO
// half. The callback is where the identity provider was consulted and the user read;
// the exchange comes a redirect later and RE-READS the user for the active flag. A
// revoke-all that lands between the two is invisible to that re-read — its row
// already carries the new epoch — so the session must be conditional on the epoch
// the code carries. The row below shows it: the store moves to epoch 4 after the
// callback read 3, the exchange re-reads 4, and the session is still refused.
//
// The control is the same pair without the revoke-all, which creates a session at 3.
func TestOIDCExchange_TheSessionIsCreatedAgainstTheEpochTheCallbackRead(t *testing.T) {
	newUser := func() db.User {
		u := epochUser(t, epochLoginEmail, 3)
		u.AuthSource, u.PasswordHash = "oidc", ""
		return u
	}

	t.Run("the callback's epoch is recorded with the code, which lives five seconds", func(t *testing.T) {
		user := newUser()
		a := newEpochApp(t, newEpochStore(user), epochOptions{})
		code := epochSeedExchange(t, a, user)

		raw, err := a.redis.Get("oidc:exchange:" + code)
		if err != nil {
			t.Fatalf("the exchange code was not stored: %v", err)
		}
		var data map[string]string
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatalf("exchange data is not a JSON object: %v: %s", err, raw)
		}
		if data["user_id"] != user.ID.String() || data["auth_epoch"] != "3" {
			t.Errorf("exchange data = %v, want the user and epoch 3", data)
		}
		if ttl := a.redis.TTL("oidc:exchange:" + code); ttl <= 0 || ttl > oidcExchangeTTL {
			t.Errorf("ttl = %v, want at most %v", ttl, oidcExchangeTTL)
		}
	})

	t.Run("control: nothing lands, a session is created at the callback's epoch", func(t *testing.T) {
		user := newUser()
		a := newEpochApp(t, newEpochStore(user), epochOptions{})
		code := epochSeedExchange(t, a, user)

		resp := a.post(t, "/auth/oidc/token-exchange", `{"code":"`+code+`"}`)
		body := decodeObject(t, resp)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
		}
		inserts := a.store.named("CreateSessionAtEpoch")
		if len(inserts) != 1 || epochArg(t, inserts[0]) != 3 {
			t.Fatalf("session inserts = %v, want one against epoch 3", inserts)
		}
		if got := a.store.auditActions(); len(got) != 1 || got[0] != "oidc_login" {
			t.Errorf("audit actions = %v, want [oidc_login]", got)
		}
	})

	for _, tt := range []struct {
		name string
		land func(u *db.User)
	}{
		{"a revoke-all lands between the callback and the exchange", func(u *db.User) { u.AuthEpoch++ }},
		{"the account is deactivated without a revoke", func(u *db.User) { u.IsActive = false }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			user := newUser()
			a := newEpochApp(t, newEpochStore(user), epochOptions{})
			code := epochSeedExchange(t, a, user)
			a.store.mutate(user.ID, tt.land)

			resp := a.post(t, "/auth/oidc/token-exchange", `{"code":"`+code+`"}`)
			body := decodeObject(t, resp)

			// A deactivated account is told so by the exchange's own check, as it always
			// was; a revoke-all that left it active is refused by the insert.
			if after := a.store.user(user.ID); !after.IsActive {
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("status = %d, want 403 for a disabled account (body %v)", resp.StatusCode, body)
				}
			} else {
				if resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401 (body %v)", resp.StatusCode, body)
				}
				if msg, _ := body["message"].(string); !strings.Contains(msg, "sign in again") {
					t.Errorf("message = %q, want it to tell the user to sign in again", msg)
				}
				inserts := a.store.named("CreateSessionAtEpoch")
				if len(inserts) != 1 || epochArg(t, inserts[0]) != 3 {
					t.Errorf("session inserts = %v, want exactly one, against the epoch the callback read (3), not the re-read user's", inserts)
				}
				if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{signInRefusedAuditAction}) {
					t.Errorf("audit actions = %v, want [%s]", got, signInRefusedAuditAction)
				}
			}
			epochRequireNothingIssued(t, resp, body)
			epochRequireNoLoginAudit(t, a.store)
			if n := len(a.store.sessionsOf(user.ID)); n != 0 {
				t.Errorf("%d session rows were created", n)
			}

			// The code was consumed whatever the answer: a replay finds nothing.
			again := a.post(t, "/auth/oidc/token-exchange", `{"code":"`+code+`"}`)
			if again.StatusCode != http.StatusUnauthorized {
				t.Errorf("a replay answered %d, want 401", again.StatusCode)
			}
		})
	}
}

// TestOIDCExchange_ACodeWithoutAUsableEpochIsRefused: a code written by a release
// that recorded no epoch — in the seconds around an upgrade — or one whose epoch is
// not a number cannot be tied to the callback's read, so the session it would create
// could not be conditional on anything. It is refused like an expired code, with
// nothing issued and no session.
func TestOIDCExchange_ACodeWithoutAUsableEpochIsRefused(t *testing.T) {
	tests := []struct {
		name string
		data func(uuid.UUID) map[string]string
	}{
		{"no epoch recorded (the previous release's code)", func(id uuid.UUID) map[string]string { return map[string]string{"user_id": id.String()} }},
		{"an empty epoch", func(id uuid.UUID) map[string]string {
			return map[string]string{"user_id": id.String(), "auth_epoch": ""}
		}},
		{"an epoch that is not a number", func(id uuid.UUID) map[string]string {
			return map[string]string{"user_id": id.String(), "auth_epoch": "three"}
		}},
		{"an epoch with trailing text", func(id uuid.UUID) map[string]string {
			return map[string]string{"user_id": id.String(), "auth_epoch": "3 OR 1=1"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := epochUser(t, epochLoginEmail, 0)
			user.AuthSource = "oidc"
			a := newEpochApp(t, newEpochStore(user), epochOptions{})
			raw, _ := json.Marshal(tt.data(user.ID))
			if err := a.redis.Set("oidc:exchange:handmade", string(raw)); err != nil {
				t.Fatal(err)
			}

			resp := a.post(t, "/auth/oidc/token-exchange", `{"code":"handmade"}`)
			body := decodeObject(t, resp)

			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body %v)", resp.StatusCode, body)
			}
			if msg, _ := body["message"].(string); !strings.Contains(msg, "Invalid or expired exchange code") {
				t.Errorf("message = %q, want the answer an expired code gets", msg)
			}
			epochRequireNothingIssued(t, resp, body)
			epochRequireNoLoginAudit(t, a.store)
			if n := len(a.store.named("CreateSessionAtEpoch")); n != 0 {
				t.Errorf("%d session inserts were attempted for a code that could not be tied to a check", n)
			}
		})
	}
}

// TestOIDCExchange_ADisabledAccountIsStillTold403 keeps the exchange's own answer for
// an account that was already disabled when the browser came back: 403, as before.
func TestOIDCExchange_ADisabledAccountIsStillTold403(t *testing.T) {
	user := epochUser(t, epochLoginEmail, 3)
	user.AuthSource, user.IsActive = "oidc", false
	a := newEpochApp(t, newEpochStore(user), epochOptions{})
	code := epochSeedExchange(t, a, user)

	resp := a.post(t, "/auth/oidc/token-exchange", `{"code":"`+code+`"}`)
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %v)", resp.StatusCode, body)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "Account is disabled") {
		t.Errorf("message = %q", msg)
	}
	epochRequireNothingIssued(t, resp, body)
}

// TestOIDCExchange_ADatabaseFailureOfTheInsertIsNotARefusal: the exchange answers a
// failed insert like a password login does, 503 or 500, never the 401 that blames
// the credential.
func TestOIDCExchange_ADatabaseFailureOfTheInsertIsNotARefusal(t *testing.T) {
	user := epochUser(t, epochLoginEmail, 3)
	user.AuthSource = "oidc"
	store := newEpochStore(user)
	store.failOn["CreateSessionAtEpoch"] = errRaceTransient
	a := newEpochApp(t, store, epochOptions{})
	code := epochSeedExchange(t, a, user)

	resp := a.post(t, "/auth/oidc/token-exchange", `{"code":"`+code+`"}`)
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
	}
	epochRequireNothingIssued(t, resp, body)
	epochRequireNoLoginAudit(t, store)
}
