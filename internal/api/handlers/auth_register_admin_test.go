package handlers

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// POST /auth/register answers two callers, and they must not get the same answer.
//
// The bootstrap caller has no session. The first registration creates the first
// administrator and signs them in (TestRegister_TheFirstUserStillGetsItsSession).
//
// An administrator adding an account (the Users page) HAS a session, and the endpoint
// used to hand them one for the new account as well: its access token in the body and,
// the part that did the damage, its refresh cookie on the administrator's browser,
// which replaced their own. Their next refresh resumed as the account they had just
// created, and what they did from then on was audited as that account. These tests pin
// the other side of the split: an administrator's registration signs nobody in.

// registerAdminBody is the request an administrator's Users page sends.
func registerAdminBody(email string) string {
	return `{"email":"` + email + `","password":"` + racePassword + `","display_name":"Carol"}`
}

// registerAs sends a registration as the administrator with the given id.
func registerAs(t *testing.T, a *epochApp, admin uuid.UUID, body string) *http.Response {
	t.Helper()
	return registerAsRole(t, a, admin, "", body)
}

// registerAsRole sends a registration as a signed-in caller with the given id and legacy
// role ("" is the harness's default, admin).
func registerAsRole(t *testing.T, a *epochApp, caller uuid.UUID, role, body string) *http.Response {
	t.Helper()
	headers := map[string]string{"X-Test-Acting-User": caller.String()}
	if role != "" {
		headers["X-Test-Acting-Role"] = role
	}
	return a.send(t, http.MethodPost, "/auth/register", body, headers, 20*time.Second)
}

// TestRegister_AnAdminCreatingAnAccountSignsNobodyIn: with users already present and
// an administrator calling, the account is created and the answer is its record and
// nothing else. No session row, no Redis row, no access token, no Set-Cookie of any
// kind — the administrator's own cookie, which the browser holds, is not replaced.
func TestRegister_AnAdminCreatingAnAccountSignsNobodyIn(t *testing.T) {
	admin := epochUser(t, epochAdminEmail, 5)
	admin.Role = "admin"
	store := newEpochStore(admin)
	a := newEpochApp(t, store, epochOptions{})

	resp := registerAs(t, a, admin.ID, registerAdminBody("carol@example.com"))
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %v)", resp.StatusCode, body)
	}

	// The account exists, as a plain, active, local user.
	var created db.User
	for _, u := range store.users {
		if u.ID != admin.ID {
			created = *u
		}
	}
	if len(store.users) != 2 || created.ID == uuid.Nil {
		t.Fatalf("%d accounts exist, want the administrator's and the one that was registered", len(store.users))
	}
	if created.Email != "carol@example.com" || created.DisplayName != "Carol" || created.Role != "user" || !created.IsActive {
		t.Errorf("the account = %+v, want carol@example.com / Carol / role user / active", created)
	}

	// The answer is the account's record, and exactly that: the comparison is of the
	// whole body, so a `user` wrapper, an access_token, an expires_at or a permissions
	// list fails it as surely as a wrong value.
	want := map[string]any{
		"id":           created.ID.String(),
		"email":        "carol@example.com",
		"display_name": "Carol",
		"role":         "user",
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("body = %v, want exactly the new account's record %v", body, want)
	}

	// Nothing was issued, to anyone.
	if cookies := resp.Header.Values("Set-Cookie"); len(cookies) != 0 {
		t.Errorf("Set-Cookie = %v: the administrator's own refresh cookie would be replaced", cookies)
	}
	if cookies := refreshCookies(resp); len(cookies) != 0 {
		t.Errorf("refresh cookies = %+v, want none", cookies)
	}
	if resp.Header.Get("Authorization") != "" {
		t.Errorf("an Authorization header was set: %q", resp.Header.Get("Authorization"))
	}
	if n := len(store.named("CreateSessionAtEpoch")); n != 0 {
		t.Errorf("%d session inserts were attempted for an account an administrator created", n)
	}
	if n := len(store.sessions); n != 0 {
		t.Errorf("%d session rows exist", n)
	}
	if keys := a.redis.Keys(); len(keys) != 0 {
		t.Errorf("Redis holds %v: a session mirror was written for an account nobody signed in as", keys)
	}

	// The account got the Viewer role, as every registration does, and is not an
	// administrator for it.
	assigns := store.named("AssignUserRole")
	if len(assigns) != 1 {
		t.Fatalf("%d role assignments, want 1", len(assigns))
	}
	if got := assigns[0].args[0]; got != created.ID {
		t.Errorf("the role was assigned to %v, want the new account %v", got, created.ID)
	}
	if got := assigns[0].args[1]; got != uuid.MustParse("a0000000-0000-0000-0000-000000000003") {
		t.Errorf("the role assigned = %v, want the Viewer role", got)
	}

	// The registration is audited — the row is still written — and it names the
	// administrator as the actor and the new account as what was acted on. It used to
	// name the new account as the actor.
	rows := store.auditRowsWritten()
	wantRow := epochAuditRow{actor: admin.ID, resourceType: "auth", resourceID: created.ID.String(), action: "register"}
	if !reflect.DeepEqual(rows, []epochAuditRow{wantRow}) {
		t.Errorf("audit rows = %+v, want exactly %+v", rows, wantRow)
	}
	if got := store.auditDetailsWritten(); !reflect.DeepEqual(got, []string{`{"email":"carol@example.com","role":"user"}`}) {
		t.Errorf("audit details = %v", got)
	}
	if got := store.poolStatementsWhileTx(); len(got) != 0 {
		t.Errorf("statements went to the POOL while the registration transaction was open: %v", got)
	}
}

// TestRegister_ACallerWhoIsNotAnAdminCreatesNothing: once an account exists, an
// anonymous caller — or any caller who is not an administrator — is refused before the
// password is hashed or the account written, and nothing is issued.
func TestRegister_ACallerWhoIsNotAnAdminCreatesNothing(t *testing.T) {
	existing := epochUser(t, epochAdminEmail, 0)
	existing.Role = "admin"
	store := newEpochStore(existing)
	a := newEpochApp(t, store, epochOptions{})

	resp := a.post(t, "/auth/register", registerAdminBody("carol@example.com"))
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %v)", resp.StatusCode, body)
	}
	if msg, _ := body["message"].(string); msg != "Only admins can register new users" {
		t.Errorf("message = %q", msg)
	}
	if len(store.users) != 1 {
		t.Errorf("%d accounts exist, want only the one that was there", len(store.users))
	}
	if n := len(store.named("CreateUser")); n != 0 {
		t.Errorf("%d CreateUser statements ran for a caller who may not create one", n)
	}
	epochRequireNothingIssued(t, resp, body)
	if got := store.auditActions(); len(got) != 0 {
		t.Errorf("audit actions = %v, want none", got)
	}
}

// TestRegister_TheFirstRegistrationIsAuditedAsTheNewAccount: the bootstrap caller has
// no session, so there is no administrator to name; the row says the new account
// registered itself — which is what it did.
func TestRegister_TheFirstRegistrationIsAuditedAsTheNewAccount(t *testing.T) {
	store := newEpochStore()
	a := newEpochApp(t, store, epochOptions{})

	resp := a.post(t, "/auth/register", `{"email":"`+epochAdminEmail+`","password":"`+racePassword+`"}`)
	body := decodeObject(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %v)", resp.StatusCode, body)
	}

	var created db.User
	for _, u := range store.users {
		created = *u
	}
	rows := store.auditRowsWritten()
	wantRow := epochAuditRow{actor: created.ID, resourceType: "auth", resourceID: created.ID.String(), action: "register"}
	if !reflect.DeepEqual(rows, []epochAuditRow{wantRow}) {
		t.Errorf("audit rows = %+v, want exactly %+v", rows, wantRow)
	}
}

// TestRegister_AnAuthenticatedCallerWhoIsNotAnAdminCreatesNothing is the refusal that
// TestRegister_ACallerWhoIsNotAnAdminCreatesNothing cannot show: that caller is
// anonymous, and an anonymous caller has no role at all. A gate that let any
// AUTHENTICATED caller through — `callerRole != ""` instead of `== "admin"` — would
// refuse every anonymous request and pass that test, and would let any signed-in user
// create accounts, which is a privilege escalation. So the caller here is signed in,
// with each role that is not administrator, and is told 403 the same way: nothing is
// created, nothing is issued, nothing is audited, and nothing is written to Redis.
func TestRegister_AnAuthenticatedCallerWhoIsNotAnAdminCreatesNothing(t *testing.T) {
	for _, role := range []string{"user", "viewer", "operator", "Admin"} {
		t.Run("role "+role, func(t *testing.T) {
			admin := epochUser(t, epochAdminEmail, 0)
			admin.Role = "admin"
			member := epochUser(t, epochLoginEmail, 0)
			member.Role = role
			store := newEpochStore(admin, member)
			a := newEpochApp(t, store, epochOptions{})

			resp := registerAsRole(t, a, member.ID, role, registerAdminBody("carol@example.com"))
			body := decodeObject(t, resp)

			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body %v): a signed-in caller who is not an administrator created an account", resp.StatusCode, body)
			}
			if msg, _ := body["message"].(string); msg != "Only admins can register new users" {
				t.Errorf("message = %q", msg)
			}
			if len(store.users) != 2 {
				t.Errorf("%d accounts exist, want only the two that were there", len(store.users))
			}
			if n := len(store.named("CreateUser")); n != 0 {
				t.Errorf("%d CreateUser statements ran for a caller who may not create one", n)
			}
			if n := len(store.named("AssignUserRole")); n != 0 {
				t.Errorf("%d role assignments were made for an account that was not created", n)
			}
			if n := len(store.named("CreateSessionAtEpoch")); n != 0 {
				t.Errorf("%d session inserts", n)
			}
			epochRequireNothingIssued(t, resp, body)
			if rows := store.auditRowsWritten(); len(rows) != 0 {
				t.Errorf("audit rows = %v, want none for a registration that was refused", rows)
			}
			if keys := a.redis.Keys(); len(keys) != 0 {
				t.Errorf("Redis holds %v: something was written for a refused registration", keys)
			}
			if got := store.poolStatementsWhileTx(); len(got) != 0 {
				t.Errorf("statements went to the POOL while the registration transaction was open: %v", got)
			}
		})
	}
}

// TestRegister_TheTransactionIsPinnedToReadCommitted: the count that decides who is
// first has to see the registration that held the advisory lock before this one, and
// under REPEATABLE READ it cannot — the snapshot is taken by the lock request itself,
// before it blocks (internal/db's TestRegister_TwoConcurrentFirstRegistrations shows
// the outcome against Postgres). The server's default is READ COMMITTED, but a role, a
// database or a connection setting can change that default, so the handler asks for
// it by name, on every registration: the first, and an administrator's.
func TestRegister_TheTransactionIsPinnedToReadCommitted(t *testing.T) {
	t.Run("the first registration", func(t *testing.T) {
		a := newEpochApp(t, newEpochStore(), epochOptions{})

		resp := a.post(t, "/auth/register", `{"email":"`+epochAdminEmail+`","password":"`+racePassword+`"}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}

		requireReadCommittedOnce(t, a)
	})

	t.Run("an administrator's registration", func(t *testing.T) {
		admin := epochUser(t, epochAdminEmail, 0)
		admin.Role = "admin"
		a := newEpochApp(t, newEpochStore(admin), epochOptions{})

		resp := registerAs(t, a, admin.ID, registerAdminBody("carol@example.com"))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}

		requireReadCommittedOnce(t, a)
	})
}

// TestRegister_TheTransactionTakesTheAdvisoryLockBeforeItCounts pins the ORDER of
// Register's transaction, in the production handler: the first statement on the
// transaction is the advisory lock, then the count that decides who is first, then
// the account. The two-connection tests in internal/db show what the lock and the
// isolation level do against Postgres, but they replay this shape in test code; this
// is the test that production Register still has it. Without the lock, or with the
// count before it, two concurrent first registrations both read a count of 0 and both
// become administrator, and no other test notices: every single registration behaves.
//
// The lock is held to the whole text of its statement, not to a fragment of it: the
// shared variant (pg_advisory_xact_lock_shared) contains the fragment and lets two
// holders in at once, which is the same failure as no lock, and a lock on
// hashtext($1::text) takes a different key from the one another instance locks on
// during a rolling restart. Both are one edit away from the real statement.
func TestRegister_TheTransactionTakesTheAdvisoryLockBeforeItCounts(t *testing.T) {
	// The exact statement Register sends, written out here and not read from the
	// handler: a test that read it from the handler would pass whatever the handler sent.
	const registerLockSQL = "SELECT pg_advisory_xact_lock($1)"

	check := func(t *testing.T, a *epochApp) {
		t.Helper()
		inTx := a.store.inTxStatements()
		names := make([]string, 0, len(inTx))
		for _, st := range inTx {
			names = append(names, st.name)
		}
		if want := []string{"SELECT", "CountUsers", "CreateUser"}; !reflect.DeepEqual(names, want) {
			t.Fatalf("the registration transaction ran %v, want %v: the advisory lock first, then the count, then the account", names, want)
		}
		lock := inTx[0]
		if lock.sql != registerLockSQL {
			t.Errorf("the first statement of the transaction is %q, want exactly %q: the lock must be the exclusive one, on the key as given", lock.sql, registerLockSQL)
		}
		if len(lock.args) != 1 || lock.args[0] != firstUserAdvisoryLockKey {
			t.Errorf("the advisory lock was taken on %v, want the first-user key %#x: a different key would not serialise against another instance's registration", lock.args, firstUserAdvisoryLockKey)
		}
	}

	t.Run("the first registration", func(t *testing.T) {
		a := newEpochApp(t, newEpochStore(), epochOptions{})
		if resp := a.post(t, "/auth/register", `{"email":"`+epochAdminEmail+`","password":"`+racePassword+`"}`); resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}
		check(t, a)
	})

	t.Run("an administrator's registration", func(t *testing.T) {
		admin := epochUser(t, epochAdminEmail, 0)
		admin.Role = "admin"
		a := newEpochApp(t, newEpochStore(admin), epochOptions{})
		if resp := registerAs(t, a, admin.ID, registerAdminBody("carol@example.com")); resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}
		check(t, a)
	})
}

func requireReadCommittedOnce(t *testing.T, a *epochApp) {
	t.Helper()
	opts := a.pool.beginOptions()
	if len(opts) != 1 {
		t.Fatalf("%d transactions were begun with options, want exactly one", len(opts))
	}
	if opts[0].IsoLevel != pgx.ReadCommitted {
		t.Errorf("the registration transaction asked for isolation %q, want %q by name, not the server's default", opts[0].IsoLevel, pgx.ReadCommitted)
	}
}

// TestRegister_ItsAuditRowsRunOnAFollowUpDeadline: every audit row a registration
// writes comes after the decision — the account exists, and for the first
// registration so does its session — so a slow audit insert must not hold the answer.
// The insert is stalled, the follow-up bound is a tenth of a second, and each site
// must still answer: the row of the first registration, the row of an
// administrator's, and the row of a sign-in that was refused at the session insert.
func TestRegister_ItsAuditRowsRunOnAFollowUpDeadline(t *testing.T) {
	stalled := func(store *epochStore) *epochStore {
		store.stallOn["InsertAuditLog"] = true
		return store
	}
	answer := func(t *testing.T, a *epochApp, caller uuid.UUID, want int) *http.Response {
		t.Helper()
		headers := map[string]string{}
		if caller != uuid.Nil {
			headers["X-Test-Acting-User"] = caller.String()
		}
		// Thirty seconds, not ten: a registration hashes its password, which takes four to
		// five seconds under the race detector, and the property under test is carried by
		// the stall assertion below — where the stalled insert was cut — not by how long
		// the whole request took. A site whose audit is not bounded still fails here, by
		// never answering.
		resp := a.send(t, http.MethodPost, "/auth/register", registerAdminBody("carol@example.com"), headers, 30*time.Second)
		if resp.StatusCode != want {
			t.Fatalf("status = %d, want %d", resp.StatusCode, want)
		}
		requireStallCutShort(t, a.store, "InsertAuditLog")
		return resp
	}
	opts := epochOptions{followUpTimeout: 100 * time.Millisecond}

	t.Run("the first registration's row", func(t *testing.T) {
		a := newEpochApp(t, stalled(newEpochStore()), opts)

		resp := answer(t, a, uuid.Nil, http.StatusCreated)

		if cookies := refreshCookies(resp); len(cookies) != 1 {
			t.Errorf("refresh cookies = %+v, want the bootstrap sign-in's one", cookies)
		}
	})

	t.Run("an administrator's registration's row", func(t *testing.T) {
		admin := epochUser(t, epochAdminEmail, 0)
		admin.Role = "admin"
		a := newEpochApp(t, stalled(newEpochStore(admin)), opts)

		resp := answer(t, a, admin.ID, http.StatusCreated)

		if cookies := refreshCookies(resp); len(cookies) != 0 {
			t.Errorf("refresh cookies = %+v, want none", cookies)
		}
	})

	t.Run("the row of a sign-in refused at the session insert", func(t *testing.T) {
		store := stalled(newEpochStore())
		store.after["CreateUser"] = func(s *epochStore) {
			for _, u := range s.users {
				u.IsActive = false
			}
		}
		a := newEpochApp(t, store, opts)

		resp := answer(t, a, uuid.Nil, http.StatusUnauthorized)

		epochRequireNothingIssued(t, resp, decodeObject(t, resp))
	})
}
