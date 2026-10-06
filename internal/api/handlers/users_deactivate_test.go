package handlers

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The deactivation of an account: the change, the bump of its auth epoch, the listing of its
// live sessions and the revoke of them are ONE transaction, bounded, released before anything
// touches the pool again, and followed only once it has committed by the RBAC cache, the audit
// row and the Redis rows of the revoked sessions. They used to be two steps on the pool, the
// second only logged when it failed, so the answer was a 200 for an account that was disabled
// with every one of its sessions still live.

// deactivationFixture is a store holding the administrator who acts, an active user
// with two live sessions — each with its Redis row — and that user's RBAC cache row.
type deactivationFixture struct {
	a       *epochApp
	admin   db.User
	target  db.User
	session [2]db.Session
}

func newDeactivationFixture(t *testing.T, opts epochOptions, tweak func(*epochStore)) *deactivationFixture {
	t.Helper()
	admin := epochUser(t, epochAdminEmail, 0)
	admin.Role = "admin"
	target := epochUser(t, epochLoginEmail, 4)

	store := newEpochStore(admin, target)
	if tweak != nil {
		tweak(store)
	}
	a := newEpochApp(t, store, opts)

	f := &deactivationFixture{a: a, admin: admin, target: target}
	for i := range f.session {
		s := db.Session{
			ID: uuid.New(), UserID: target.ID, TokenHash: "hash-" + uuid.NewString(),
			ExpiresAt: time.Now().Add(time.Hour), UserRole: "user",
		}
		store.sessions = append(store.sessions, s)
		f.session[i] = s
		if err := a.redis.Set("nexara:session:"+s.ID.String(), "{}"); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.redis.Set("nexara:rbac:"+target.ID.String(), "{}"); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *deactivationFixture) put(t *testing.T, body string) *http.Response {
	t.Helper()
	return f.a.send(t, http.MethodPut, "/users/"+f.target.ID.String(), body,
		map[string]string{"X-Test-Acting-User": f.admin.ID.String()}, 20*time.Second)
}

// requireUntouched fails unless the account is exactly as the fixture made it: still
// active at its epoch, both sessions live, their Redis rows and the RBAC cache row
// there, nothing audited, and no transaction left open.
func (f *deactivationFixture) requireUntouched(t *testing.T) {
	t.Helper()
	u := f.a.store.user(f.target.ID)
	if !u.IsActive || u.AuthEpoch != f.target.AuthEpoch || u.DisplayName != f.target.DisplayName {
		t.Errorf("the account changed: active=%t epoch=%d (was %d) name=%q", u.IsActive, u.AuthEpoch, f.target.AuthEpoch, u.DisplayName)
	}
	if live := f.a.store.liveSessionsOf(f.target.ID); len(live) != 2 {
		t.Errorf("%d live sessions, want both: a failed deactivation must not end any", len(live))
	}
	for _, s := range f.session {
		if !f.a.redis.Exists("nexara:session:" + s.ID.String()) {
			t.Errorf("the Redis row of session %v was deleted although nothing was revoked", s.ID)
		}
	}
	if !f.a.redis.Exists("nexara:rbac:" + f.target.ID.String()) {
		t.Error("the RBAC cache row was invalidated although nothing changed")
	}
	if got := f.a.store.auditActions(); len(got) != 0 {
		t.Errorf("audit actions = %v, want none for a change that was not made", got)
	}
	if _, _, open := f.a.store.txCounts(); open != 0 {
		t.Errorf("%d transactions were left open: their connections never went back to the pool", open)
	}
}

// requireDeactivated fails unless the account is inactive with a moved epoch and both
// sessions revoked.
func (f *deactivationFixture) requireDeactivated(t *testing.T) {
	t.Helper()
	u := f.a.store.user(f.target.ID)
	if u.IsActive {
		t.Error("the account is still active")
	}
	if u.AuthEpoch != f.target.AuthEpoch+1 {
		t.Errorf("the epoch is %d, want %d: a deactivation is a revoke-all and moves it by one", u.AuthEpoch, f.target.AuthEpoch+1)
	}
	if live := f.a.store.liveSessionsOf(f.target.ID); len(live) != 0 {
		t.Errorf("%d sessions are still live after the deactivation", len(live))
	}
}

// TestDeactivate_TheChangeTheBumpAndTheRevokeAreOneTransaction is the success path,
// and the shape of the transaction: the four statements, in this order, all on the
// transaction; one transaction, committed; nothing asked of the pool while it was
// open; and the RBAC cache, the audit row and the Redis rows handled after it.
func TestDeactivate_TheChangeTheBumpAndTheRevokeAreOneTransaction(t *testing.T) {
	f := newDeactivationFixture(t, epochOptions{}, nil)

	resp := f.put(t, `{"is_active":false}`)
	body := authRequireStatus(t, resp, http.StatusOK)
	if body["is_active"] != false || body["id"] != f.target.ID.String() {
		t.Errorf("body = %v, want the deactivated account", body)
	}
	f.requireDeactivated(t)

	committed, rolledBack, open := f.a.store.txCounts()
	if committed != 1 || rolledBack != 0 || open != 0 {
		t.Errorf("transactions: committed=%d rolledBack=%d open=%d, want 1, 0, 0", committed, rolledBack, open)
	}
	if got := f.a.pool.beginAttempts(); got != 1 {
		t.Errorf("%d transactions were begun, want exactly one", got)
	}
	authRequireReadCommitted(t, f.a.pool, "deactivation")

	var inTx, onPool []string
	f.a.store.mu.Lock()
	for _, st := range f.a.store.stmts {
		if st.inTx {
			inTx = append(inTx, st.name)
		} else {
			onPool = append(onPool, st.name)
		}
	}
	f.a.store.mu.Unlock()
	if want := []string{"UpdateUserProfile", "BumpUserAuthEpoch", "ListUserSessions", "RevokeAllUserSessions"}; !reflect.DeepEqual(inTx, want) {
		t.Errorf("the transaction's statements = %v, want %v, in this order", inTx, want)
	}
	if want := []string{"GetUserByID", "InsertAuditLog"}; !reflect.DeepEqual(onPool, want) {
		t.Errorf("the pool's statements = %v, want %v: the read before and the audit row after, nothing else", onPool, want)
	}
	if got := f.a.store.poolStatementsWhileTx(); len(got) != 0 {
		t.Errorf("the handler queried the POOL (%v) while its own transaction still held a connection: with the pool "+
			"at its limit that is a deadlock — every connection held by a request waiting for another", got)
	}

	if got := f.a.store.auditActions(); !reflect.DeepEqual(got, []string{"user_updated"}) {
		t.Errorf("audit actions = %v, want [user_updated]", got)
	}
	if f.a.redis.Exists("nexara:rbac:" + f.target.ID.String()) {
		t.Error("the RBAC cache row survived the change")
	}
	for _, s := range f.session {
		if f.a.redis.Exists("nexara:session:" + s.ID.String()) {
			t.Errorf("the Redis row of revoked session %v survived", s.ID)
		}
	}
}

// TestDeactivate_AFailureAnywhereInTheTransactionChangesNothing is the property the transaction
// is for. A statement that fails, whichever one, the revoke above all (which the old code only
// logged), rolls the whole of it back: the account stays active at its epoch, every session
// live, nothing audited, no Redis row touched, and the answer says NOT changed: a 503 when the
// database was away and a 500 otherwise, never the 200 the old code answered.
func TestDeactivate_AFailureAnywhereInTheTransactionChangesNothing(t *testing.T) {
	for _, stmt := range []string{"UpdateUserProfile", "BumpUserAuthEpoch", "ListUserSessions", "RevokeAllUserSessions"} {
		for _, tc := range []struct {
			name string
			err  error
			want int
		}{
			{"the database is away", errRaceTransient, http.StatusServiceUnavailable},
			{"a defect", errRaceBug, http.StatusInternalServerError},
		} {
			t.Run(stmt+" fails: "+tc.name, func(t *testing.T) {
				f := newDeactivationFixture(t, epochOptions{}, func(s *epochStore) { s.failOn[stmt] = tc.err })

				resp := f.put(t, `{"is_active":false}`)
				body := authRequireStatus(t, resp, tc.want)
				if msg, _ := body["message"].(string); !strings.Contains(msg, "NOT changed") {
					t.Errorf("message = %q, want it to say the account was NOT changed", msg)
				}
				f.requireUntouched(t)
				if committed, rolledBack, _ := f.a.store.txCounts(); committed != 0 || rolledBack != 1 {
					t.Errorf("transactions: committed=%d rolledBack=%d, want 0 and 1", committed, rolledBack)
				}
			})
		}
	}

	t.Run("the transaction cannot be started", func(t *testing.T) {
		f := newDeactivationFixture(t, epochOptions{}, nil)
		f.a.pool.beginErr = errRaceTransient

		resp := f.put(t, `{"is_active":false}`)
		body := authRequireStatus(t, resp, http.StatusServiceUnavailable)
		if msg, _ := body["message"].(string); !strings.Contains(msg, "NOT changed") {
			t.Errorf("message = %q, want it to say the account was NOT changed", msg)
		}
		f.requireUntouched(t)
		if n := len(f.a.store.named("UpdateUserProfile")); n != 0 {
			t.Errorf("%d profile updates were sent without a transaction: the change must not be made outside it", n)
		}
	})
}

// TestDeactivate_IsBounded: a pool with no free connection or a statement stuck on a
// row lock must be a 503 and not a request that waits for ever, and the transaction it
// abandons rolls back — on a context of its own, since the request's has run out — so
// the account is left as it was.
func TestDeactivate_IsBounded(t *testing.T) {
	for _, stalled := range []string{"UpdateUserProfile", "RevokeAllUserSessions"} {
		t.Run("a stall in "+stalled, func(t *testing.T) {
			f := newDeactivationFixture(t, epochOptions{dbTimeout: 100 * time.Millisecond}, func(s *epochStore) { s.stallOn[stalled] = true })

			start := time.Now()
			resp := f.a.send(t, http.MethodPut, "/users/"+f.target.ID.String(), `{"is_active":false}`,
				map[string]string{"X-Test-Acting-User": f.admin.ID.String()}, 10*time.Second)
			elapsed := time.Since(start)
			body := authRequireStatus(t, resp, http.StatusServiceUnavailable)
			if elapsed > 5*time.Second {
				t.Errorf("answered after %v with a 100 ms bound", elapsed)
			}
			if msg, _ := body["message"].(string); !strings.Contains(msg, "NOT changed") {
				t.Errorf("message = %q, want it to say the account was NOT changed", msg)
			}
			f.requireUntouched(t)
			if _, rolledBack, _ := f.a.store.txCounts(); rolledBack != 1 {
				t.Errorf("rolled back %d times, want 1: the abandoned transaction must be released", rolledBack)
			}
		})
	}
}

// TestDeactivate_ALostCommitReplyIsAnUnconfirmedOutcome holds the rule for a COMMIT whose
// answer did not arrive. One that never went out, or that the server answered, is a known "not
// done": NOT changed, and the store agrees. One that went out and came back without an answer
// may have landed, and every row shows the same answer for both states the store can end in:
// "could not be confirmed", never "NOT changed" for a deactivation that happened, never a
// success for one that did not. The change is still RECORDED as a user_updated audit row that
// says the outcome is unconfirmed, so an audit filtered on that action cannot miss a
// deactivation that landed; the RBAC cache is purged, right either way.
func TestDeactivate_ALostCommitReplyIsAnUnconfirmedOutcome(t *testing.T) {
	const notChanged, unconfirmed = "NOT changed", "could not be confirmed"

	tests := []struct {
		name        string
		err         error
		lands       bool
		want        int
		wantMessage string
	}{
		{"a COMMIT that never went out", &neverSentError{err: context.DeadlineExceeded}, false, http.StatusServiceUnavailable, notChanged},
		{"a COMMIT the server answered with an error", pgState("40001"), false, http.StatusServiceUnavailable, notChanged},
		{"the reply was lost, and the commit did not land", errRaceTransient, false, http.StatusServiceUnavailable, unconfirmed},
		{"the reply was lost, and the commit DID land", errRaceTransient, true, http.StatusServiceUnavailable, unconfirmed},
		{"the connection closed while the reply was awaited (SafeToRetry, and it landed)", connClosedError{}, true, http.StatusServiceUnavailable, unconfirmed},
		{"the connection closed while the reply was awaited (SafeToRetry, and it did not)", connClosedError{}, false, http.StatusServiceUnavailable, unconfirmed},
		{"an error nobody classified: a 500, equally unconfirmed", errRaceBug, true, http.StatusInternalServerError, unconfirmed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newDeactivationFixture(t, epochOptions{}, func(s *epochStore) { s.commitErr, s.commitLands = tt.err, tt.lands })

			resp := f.put(t, `{"is_active":false}`)
			body := authRequireStatus(t, resp, tt.want)
			msg, _ := body["message"].(string)
			if !strings.Contains(msg, tt.wantMessage) {
				t.Fatalf("message = %q, want it to contain %q", msg, tt.wantMessage)
			}
			if tt.wantMessage == unconfirmed && strings.Contains(msg, notChanged) {
				t.Errorf("an unconfirmed commit says the account was NOT changed: %q", msg)
			}
			if tt.wantMessage == notChanged {
				f.requireUntouched(t)
				return
			}
			// Unconfirmed: the store is in whichever state the commit left it in.
			if tt.lands {
				f.requireDeactivated(t)
			} else if u := f.a.store.user(f.target.ID); !u.IsActive {
				t.Error("the commit did not land but the account is inactive")
			}
			if got := f.a.store.auditActions(); !reflect.DeepEqual(got, []string{"user_updated"}) {
				t.Errorf("audit actions = %v, want [user_updated]: an audit filtered on the action must not miss a deactivation that landed", got)
			}
			if d := f.a.store.auditDetailsWritten(); len(d) != 1 ||
				!strings.Contains(d[0], `"outcome":"unconfirmed"`) || !strings.Contains(d[0], `"email":"`+epochLoginEmail+`"`) {
				t.Errorf("audit details = %v, want the email and outcome unconfirmed", d)
			}
			if f.a.redis.Exists("nexara:rbac:" + f.target.ID.String()) {
				t.Error("the RBAC cache was not purged: a purge is right whichever way the commit went")
			}
			for _, sess := range f.session {
				if !f.a.redis.Exists("nexara:session:" + sess.ID.String()) {
					t.Errorf("the Redis row of session %v was deleted: the cleanup that follows a CONFIRMED commit ran for an unconfirmed one", sess.ID)
				}
			}
		})
	}
}

// TestDeactivate_TheUnconfirmedAuditRunsOnAFollowUpDeadline: the audit row of a
// deactivation whose COMMIT could not be confirmed is written under a deadline of its
// own, so that a slow audit insert cannot hold the answer that says so. The commit's
// reply is lost, the insert is stalled and the bound is a tenth of a second.
func TestDeactivate_TheUnconfirmedAuditRunsOnAFollowUpDeadline(t *testing.T) {
	f := newDeactivationFixture(t, epochOptions{followUpTimeout: 100 * time.Millisecond}, func(s *epochStore) {
		s.commitErr, s.commitLands = errRaceTransient, true
		s.stallOn["InsertAuditLog"] = true
	})

	resp := f.a.send(t, http.MethodPut, "/users/"+f.target.ID.String(), `{"is_active":false}`,
		map[string]string{"X-Test-Acting-User": f.admin.ID.String()}, 10*time.Second)
	body := authRequireStatus(t, resp, http.StatusServiceUnavailable)
	if msg, _ := body["message"].(string); !strings.Contains(msg, "could not be confirmed") {
		t.Errorf("message = %q, want it to say the outcome could not be confirmed", msg)
	}
	requireStallCutShort(t, f.a.store, "InsertAuditLog")
}

// TestUpdateUser_EditsThatDoNotDeactivateAreUntouched pins that the transaction is
// the deactivation's alone: a name change or a reactivation is the single statement
// it always was, on the pool, with no transaction, no bump and no revoke — a user
// whose display name is edited must not be signed out of every device.
func TestUpdateUser_EditsThatDoNotDeactivateAreUntouched(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{"a display name", `{"display_name":"Renamed User"}`},
		{"a reactivation of an active account", `{"is_active":true}`},
		{"a role change", `{"role":"admin"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newDeactivationFixture(t, epochOptions{}, nil)

			resp := f.put(t, tt.body)
			authRequireStatus(t, resp, http.StatusOK)
			if got := f.a.pool.beginAttempts(); got != 0 {
				t.Errorf("%d transactions were begun for an edit that is not a deactivation", got)
			}
			for _, name := range []string{"BumpUserAuthEpoch", "ListUserSessions", "RevokeAllUserSessions"} {
				if n := len(f.a.store.named(name)); n != 0 {
					t.Errorf("%s was sent %d times for an edit that is not a deactivation", name, n)
				}
			}
			if live := f.a.store.liveSessionsOf(f.target.ID); len(live) != 2 {
				t.Errorf("%d live sessions after the edit, want both", len(live))
			}
			if u := f.a.store.user(f.target.ID); u.AuthEpoch != f.target.AuthEpoch || !u.IsActive {
				t.Errorf("the account's epoch/active moved: epoch=%d active=%t", u.AuthEpoch, u.IsActive)
			}
			if got := f.a.store.auditActions(); !reflect.DeepEqual(got, []string{"user_updated"}) {
				t.Errorf("audit actions = %v, want [user_updated]", got)
			}
		})
	}
}

// TestDeactivate_EndsTheSessionsEvenWithoutASessionManager: the revoke is database
// work and needs only the queries; the old code skipped it entirely when the handler
// had no SessionManager, which left a deactivated account's sessions live. Only the
// Redis cleanup depends on the manager.
func TestDeactivate_EndsTheSessionsEvenWithoutASessionManager(t *testing.T) {
	f := newDeactivationFixture(t, epochOptions{noSessions: true}, nil)

	resp := f.put(t, `{"is_active":false}`)
	authRequireStatus(t, resp, http.StatusOK)
	f.requireDeactivated(t)
}

// TestNewUserHandler_AMissingPoolStaysMissing is TestNewAuthHandler_AMissingPoolStaysMissing
// for the user handler: stored unconditionally, a nil *pgxpool.Pool becomes a NON-nil interface,
// and deactivate's `h.pool == nil` guard stops firing. The second half is the control.
func TestNewUserHandler_AMissingPoolStaysMissing(t *testing.T) {
	var none *pgxpool.Pool
	if h := NewUserHandler(none, nil, nil, nil, nil); h.pool != nil {
		t.Errorf("a nil *pgxpool.Pool became %T in the handler's pool field; it must stay a nil interface so `h.pool == nil` still fires", h.pool)
	}

	// Never used, never dialled: only its identity is compared.
	real := new(pgxpool.Pool)
	if h := NewUserHandler(real, nil, nil, nil, nil); h.pool != txOptionsBeginner(real) {
		t.Errorf("the handler's pool field = %v, want the pool it was given", h.pool)
	}
}

// TestUpdateUser_OnlySuppliedFieldsAreWritten holds the fix for the lost update the handler
// used to make: it read the account, then wrote all three profile columns back from that read,
// so a concurrent edit that landed in between was undone for every field the request had not
// mentioned (a deactivation put back a demoted role; a name edit that had read is_active = true
// re-activated an account deactivated meanwhile). The hook lands the concurrent write right
// behind the handler's read of the account.
func TestUpdateUser_OnlySuppliedFieldsAreWritten(t *testing.T) {
	tests := []struct {
		name string
		body string
		// concurrent is the edit that commits between the handler's read and its write.
		concurrent func(u *db.User)
		check      func(t *testing.T, u db.User)
	}{
		{
			name:       "a name edit does not re-activate an account deactivated in the meantime",
			body:       `{"display_name":"Renamed User"}`,
			concurrent: func(u *db.User) { u.IsActive = false },
			check: func(t *testing.T, u db.User) {
				if u.IsActive {
					t.Error("the account is active again: the name edit wrote back the is_active it had read")
				}
				if u.DisplayName != "Renamed User" {
					t.Errorf("display name = %q, want the edit", u.DisplayName)
				}
			},
		},
		{
			name:       "a name edit does not undo a role change that landed in the meantime",
			body:       `{"display_name":"Renamed User"}`,
			concurrent: func(u *db.User) { u.Role = "admin" },
			check: func(t *testing.T, u db.User) {
				if u.Role != "admin" {
					t.Errorf("role = %q, want the concurrent edit's admin: the name edit wrote back the role it had read", u.Role)
				}
			},
		},
		{
			name:       "a deactivation does not write back a role that was changed in the meantime",
			body:       `{"is_active":false}`,
			concurrent: func(u *db.User) { u.Role = "admin" },
			check: func(t *testing.T, u db.User) {
				if u.IsActive {
					t.Error("the account is still active")
				}
				if u.Role != "admin" {
					t.Errorf("role = %q, want the concurrent edit's admin: the deactivation wrote back the role it had read", u.Role)
				}
			},
		},
		{
			name:       "a deactivation does not write back a name that was changed in the meantime",
			body:       `{"is_active":false}`,
			concurrent: func(u *db.User) { u.DisplayName = "Edited Elsewhere" },
			check: func(t *testing.T, u db.User) {
				if u.DisplayName != "Edited Elsewhere" {
					t.Errorf("display name = %q, want the concurrent edit's", u.DisplayName)
				}
			},
		},
		{
			name:       "a role change does not write back an active flag or a name",
			body:       `{"role":"admin"}`,
			concurrent: func(u *db.User) { u.IsActive = false; u.DisplayName = "Edited Elsewhere" },
			check: func(t *testing.T, u db.User) {
				if u.Role != "admin" {
					t.Errorf("role = %q, want the edit", u.Role)
				}
				if u.IsActive || u.DisplayName != "Edited Elsewhere" {
					t.Errorf("active=%t name=%q: the role change wrote back fields it was not given", u.IsActive, u.DisplayName)
				}
			},
		},
		{
			name:       "an empty body changes nothing",
			body:       `{}`,
			concurrent: func(u *db.User) { u.IsActive = false; u.Role = "admin"; u.DisplayName = "Edited Elsewhere" },
			check: func(t *testing.T, u db.User) {
				if u.IsActive || u.Role != "admin" || u.DisplayName != "Edited Elsewhere" {
					t.Errorf("an empty edit changed the account: active=%t role=%q name=%q", u.IsActive, u.Role, u.DisplayName)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newDeactivationFixture(t, epochOptions{}, nil)
			f.a.store.after["GetUserByID"] = func(s *epochStore) {
				// Only the handler's own read of the account, once.
				tt.concurrent(s.users[f.target.ID])
				delete(s.after, "GetUserByID")
			}

			resp := f.put(t, tt.body)
			authRequireStatus(t, resp, http.StatusOK)
			tt.check(t, f.a.store.user(f.target.ID))
		})
	}
}
