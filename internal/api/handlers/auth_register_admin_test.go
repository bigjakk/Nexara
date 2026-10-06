package handlers

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// POST /auth/register answers two callers, and they must not get the same answer. The
// bootstrap caller has no session: the first registration creates the first administrator
// and signs them in (TestRegister_TheFirstUserStillGetsItsSession). An administrator adding
// an account HAS a session, and the endpoint used to hand them one for the new account too:
// its refresh cookie replaced their own on their browser, their next refresh resumed as the
// account they had just created, and what they did from then on was audited as that account.
// An administrator's registration signs nobody in.

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

// TestRegister_AnAdminCreatingAnAccountSignsNobodyIn: with users already present and an
// administrator calling, the account is created and the answer is its record and nothing
// else. No session row, no Redis row, no access token, no Set-Cookie of any kind: the
// administrator's own cookie, which the browser holds, is not replaced.
func TestRegister_AnAdminCreatingAnAccountSignsNobodyIn(t *testing.T) {
	admin := epochUser(t, epochAdminEmail, 5)
	admin.Role = "admin"
	store := newEpochStore(admin)
	a := newEpochApp(t, store, epochOptions{})

	resp := registerAs(t, a, admin.ID, registerAdminBody("carol@example.com"))
	body := authRequireStatus(t, resp, http.StatusCreated)

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

	// The answer is the account's record, and exactly that: a `user` wrapper, an
	// access_token, an expires_at or a permissions list fails it as surely as a wrong value.
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

	// The registration is audited, naming the administrator as the actor and the new account
	// as what was acted on (it used to name the new account as the actor).
	rows := store.auditRowsWritten()
	wantRow := authFakeAuditRow{actor: admin.ID, resourceType: "auth", resourceID: created.ID.String(), action: "register"}
	if !reflect.DeepEqual(rows, []authFakeAuditRow{wantRow}) {
		t.Errorf("audit rows = %+v, want exactly %+v", rows, wantRow)
	}
	if got := store.auditDetailsWritten(); !reflect.DeepEqual(got, []string{`{"email":"carol@example.com","role":"user"}`}) {
		t.Errorf("audit details = %v", got)
	}
	if got := store.poolStatementsWhileTx(); len(got) != 0 {
		t.Errorf("statements went to the POOL while the registration transaction was open: %v", got)
	}
}

// TestRegister_ACallerWhoIsNotAnAdminCreatesNothing: once an account exists, any caller who
// is not an administrator is refused before the password is hashed or the account written,
// and nothing is issued, audited or written to Redis. An anonymous caller has no role at all,
// so a gate that let any AUTHENTICATED caller through (`callerRole != ""` instead of `==
// "admin"`) would refuse every anonymous request and pass that row alone, and would let any
// signed-in user create accounts: the signed-in callers of each other role are rows too.
func TestRegister_ACallerWhoIsNotAnAdminCreatesNothing(t *testing.T) {
	for _, role := range []string{"", "user", "viewer", "operator", "Admin"} {
		name := "role " + role
		if role == "" {
			name = "an anonymous caller"
		}
		t.Run(name, func(t *testing.T) {
			admin := epochUser(t, epochAdminEmail, 0)
			admin.Role = "admin"
			member := epochUser(t, epochLoginEmail, 0)
			member.Role = role
			store := newEpochStore(admin, member)
			a := newEpochApp(t, store, epochOptions{})

			var resp *http.Response
			if role == "" {
				resp = a.post(t, "/auth/register", registerAdminBody("carol@example.com"))
			} else {
				resp = registerAsRole(t, a, member.ID, role, registerAdminBody("carol@example.com"))
			}
			body := decodeObject(t, resp)

			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body %v): a caller who is not an administrator created an account", resp.StatusCode, body)
			}
			if msg, _ := body["message"].(string); msg != "Only admins can register new users" {
				t.Errorf("message = %q", msg)
			}
			if len(store.users) != 2 {
				t.Errorf("%d accounts exist, want only the two that were there", len(store.users))
			}
			for _, name := range []string{"CreateUser", "AssignUserRole", "CreateSessionAtEpoch"} {
				if n := len(store.named(name)); n != 0 {
					t.Errorf("%d %s statements ran for a caller who may not register an account", n, name)
				}
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

// authRegistrations runs fn after each of the two registrations that begin a transaction:
// the first, and an administrator's.
func authRegistrations(t *testing.T, fn func(t *testing.T, a *epochApp)) {
	t.Helper()
	t.Run("the first registration", func(t *testing.T) {
		a := newEpochApp(t, newEpochStore(), epochOptions{})
		authRequireStatus(t, a.post(t, "/auth/register", `{"email":"`+epochAdminEmail+`","password":"`+racePassword+`"}`), http.StatusCreated)
		fn(t, a)
	})
	t.Run("an administrator's registration", func(t *testing.T) {
		admin := epochUser(t, epochAdminEmail, 0)
		admin.Role = "admin"
		a := newEpochApp(t, newEpochStore(admin), epochOptions{})
		authRequireStatus(t, registerAs(t, a, admin.ID, registerAdminBody("carol@example.com")), http.StatusCreated)
		fn(t, a)
	})
}

// TestRegister_TheTransactionIsPinnedToReadCommitted: the count that decides who is first
// has to see the registration that held the advisory lock before this one, and under
// REPEATABLE READ it cannot, because the snapshot is taken by the lock request itself before
// it blocks (internal/db's TestRegister_TwoConcurrentFirstRegistrations shows it against
// Postgres). The server default is READ COMMITTED, but a role, a database or a connection
// setting can change that, so the handler asks for it by name on every registration.
func TestRegister_TheTransactionIsPinnedToReadCommitted(t *testing.T) {
	authRegistrations(t, func(t *testing.T, a *epochApp) {
		authRequireReadCommitted(t, a.pool, "registration")
	})
}

// TestRegister_TheTransactionTakesTheAdvisoryLockBeforeItCounts pins the ORDER of Register's
// transaction in the production handler: the advisory lock, then the count that decides who is
// first, then the account. internal/db shows what the lock and isolation level do against
// Postgres but replays this shape in test code; this is the test that production Register still
// has it (without the lock, two concurrent first registrations both read 0 and both become
// administrator). The lock is held to its whole statement text: the shared variant lets two
// holders in, and a lock on hashtext($1::text) takes a different key from another instance's.
func TestRegister_TheTransactionTakesTheAdvisoryLockBeforeItCounts(t *testing.T) {
	// Written out here and not read from the handler: a test that read it from the handler
	// would pass whatever the handler sent.
	const registerLockSQL = "SELECT pg_advisory_xact_lock($1)"

	authRegistrations(t, func(t *testing.T, a *epochApp) {
		var names []string
		var lock authFakeStmt
		for _, st := range a.store.statements() {
			if st.inTx {
				if len(names) == 0 {
					lock = st
				}
				names = append(names, st.name)
			}
		}
		if want := []string{"SELECT", "CountUsers", "CreateUser"}; !reflect.DeepEqual(names, want) {
			t.Fatalf("the registration transaction ran %v, want %v: the advisory lock first, then the count, then the account", names, want)
		}
		if lock.sql != registerLockSQL {
			t.Errorf("the first statement of the transaction is %q, want exactly %q: the lock must be the exclusive one, on the key as given", lock.sql, registerLockSQL)
		}
		if len(lock.args) != 1 || lock.args[0] != firstUserAdvisoryLockKey {
			t.Errorf("the advisory lock was taken on %v, want the first-user key %#x: a different key would not serialise against another instance's registration", lock.args, firstUserAdvisoryLockKey)
		}
	})
}

// TestRegister_ItsAuditRowsRunOnAFollowUpDeadline: every audit row a registration writes
// comes after the decision (the account exists, and for the first registration so does its
// session), so a slow audit insert must not hold the answer. The insert is stalled, the
// follow-up bound is a tenth of a second, and each site must still answer: the row of the
// first registration, of an administrator's, and of a sign-in refused at the session insert.
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
		// What is carried by the stall assertion below, where the stalled insert was cut, not
		// how long the request took: a site whose audit is not bounded fails by never answering.
		resp := a.send(t, http.MethodPost, "/auth/register", registerAdminBody("carol@example.com"), headers, 10*time.Second)
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

		authRequireCookie(t, answer(t, a, admin.ID, http.StatusCreated), cookieUntouched)
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
