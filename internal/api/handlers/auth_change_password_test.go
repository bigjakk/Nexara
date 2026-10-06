package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"github.com/bigjakk/nexara/internal/auth"
)

// The password and the end of every session of the user are ONE transaction, run through
// the transaction's own queries: both happen or neither does. They used to be two statements
// on the pool, and these tests pin the three ways that went wrong: a change that landed while
// its revoke failed answered 200 with every other session still live; a change whose answer
// was lost skipped the revoke and refused the user's retry for using the old password; and a
// failure said "nothing was confirmed" about a change it had made. Rows that hash the new
// password run in parallel; the bcrypt work sits OUTSIDE any database bound (see
// ChangePassword), which is what lets a row set a bound far shorter than a hash takes.

const (
	racePassword    = "Old-Passw0rd-Example!"
	raceNewPassword = "New-Passw0rd-Example!"
)

const changeBody = `{"old_password":"` + racePassword + `","new_password":"` + raceNewPassword + `"}`

var (
	racePasswordHashOnce  sync.Once
	racePasswordHashValue string
)

// racePasswordHash is a bcrypt hash of racePassword at the cheapest cost: CheckPassword reads
// the cost out of the hash, so the only expensive step left in a request is hashing the NEW
// password.
func racePasswordHash(t *testing.T) string {
	t.Helper()
	racePasswordHashOnce.Do(func() {
		h, err := bcrypt.GenerateFromPassword([]byte(racePassword), bcrypt.MinCost)
		if err != nil {
			panic(err)
		}
		racePasswordHashValue = string(h)
	})
	return racePasswordHashValue
}

// withPasswordHash gives the harness's user the hash of racePassword.
func withPasswordHash(t *testing.T, tweak func(*raceStore)) func(*raceStore) {
	return func(s *raceStore) {
		s.user.PasswordHash = racePasswordHash(t)
		if tweak != nil {
			tweak(s)
		}
	}
}

// passwordChanged reports whether the user's stored password is the new one.
func (a *authRaceApp) passwordChanged() bool {
	return auth.CheckPassword(a.store.snapshotUser().PasswordHash, raceNewPassword) == nil
}

// sessionKey is the Redis row of the harness's session.
func (a *authRaceApp) sessionKey() string {
	return "nexara:session:" + a.store.session.ID.String()
}

// logLinesFor returns the lines of the captured log that mention needle (a user id, which is
// unique to one row, so rows that run in parallel can share one capture).
func logLinesFor(logs *lockedLog, needle string) string {
	var out []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, needle) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// authRequireReadCommitted fails unless exactly one transaction was begun with options, and
// it named READ COMMITTED. The revoke-all inside it (the epoch bump, the listing, the revoke)
// is only complete under READ COMMITTED, where each statement takes its own snapshot and
// sees a sign-in that committed meanwhile; under REPEATABLE READ the late session stays live
// (internal/db shows it against Postgres). The server default is READ COMMITTED, but a role,
// a database or a connection setting can change it, so the handler asks by name.
func authRequireReadCommitted(t *testing.T, pool *authFakePool, what string) {
	t.Helper()
	opts := pool.beginOptions()
	if len(opts) != 1 {
		t.Fatalf("%d transactions were begun with options, want exactly one", len(opts))
	}
	if opts[0].IsoLevel != pgx.ReadCommitted {
		t.Errorf("the %s transaction asked for isolation %q, want %q by name, not the server's default", what, opts[0].IsoLevel, pgx.ReadCommitted)
	}
}

// TestChangePassword_ThePasswordAndTheSessionsAreOneTransaction drives the change through
// every way its transaction can end, and holds the STATE it leaves to what the answer says.
// Before the COMMIT, and for a commit the server refused, the transaction rolled back and the
// message says the password was NOT changed. For a COMMIT that went out and returns without
// the server's answer the handler reads the user back and compares the hash, so the answer is
// the success it was or NOT changed; only when that read fails too is it "could not be
// confirmed", and its pair of rows shows why it cannot say more: the same answer, opposite states.
func TestChangePassword_ThePasswordAndTheSessionsAreOneTransaction(t *testing.T) {
	logs := captureProductionLog(t)
	t.Cleanup(func() {
		if out := logs.String(); strings.Contains(out, racePassword) || strings.Contains(out, raceNewPassword) {
			t.Errorf("a password reached the log")
		}
	})

	late := fmt.Errorf("timeout: %w", context.DeadlineExceeded)
	const notChanged = "NOT changed"
	const unconfirmed = "could not be confirmed"

	tests := []struct {
		name  string
		tweak func(*raceStore)
		begin error // what Begin fails with, if anything

		want        int
		wantMessage string // a substring
		// What the store holds afterwards.
		changed   bool // the stored password is the new one
		revoked   bool // the session is revoked
		redisGone bool // the session's Redis row was deleted
		audited   bool // password_changed was audited
		// What happened to the transaction.
		wantNoTx      bool
		wantCommitted bool
		wantLog       string // a line the user's id appears in
		// settled marks a row whose COMMIT went out and came back without the server's answer,
		// so the handler ran the locking read that finds out whether it landed. Every other
		// row reads the user once, at the start.
		settled bool
	}{
		{
			name: "everything works: the password changes, every session ends, the Redis row goes, it is audited",
			want: http.StatusOK, wantMessage: "Password changed successfully",
			changed: true, revoked: true, redisGone: true, audited: true, wantCommitted: true,
		},

		// Before the commit: nothing changed, and the message says so.
		{
			name: "the transaction cannot be started: the database is away", begin: errRaceTransient,
			want: http.StatusServiceUnavailable, wantMessage: notChanged, wantNoTx: true,
			wantLog: "could not start the transaction",
		},
		{
			name: "the transaction cannot be started: a defect", begin: errRaceBug,
			want: http.StatusInternalServerError, wantMessage: notChanged, wantNoTx: true,
			wantLog: "failed to start the transaction",
		},
		{
			name:  "the update fails: the database is away",
			tweak: func(s *raceStore) { s.updatePwErr = errRaceTransient },
			want:  http.StatusServiceUnavailable, wantMessage: notChanged,
			wantLog: "could not update the password",
		},
		{
			name:  "the update fails: a defect",
			tweak: func(s *raceStore) { s.updatePwErr = errRaceBug },
			want:  http.StatusInternalServerError, wantMessage: notChanged,
			wantLog: "failed to update the password",
		},
		{
			name:  "listing the sessions fails after the update ran: the password is back",
			tweak: func(s *raceStore) { s.listErr = errRaceTransient },
			want:  http.StatusServiceUnavailable, wantMessage: notChanged,
			wantLog: "could not revoke the user's sessions",
		},
		{
			// The case the old code answered 200: the change stuck, the sessions stayed.
			name:  "the revoke fails after the update ran: the password is back and the sessions stand",
			tweak: func(s *raceStore) { s.revokeAllErr = errRaceTransient },
			want:  http.StatusServiceUnavailable, wantMessage: notChanged,
			wantLog: "could not revoke the user's sessions",
		},
		{
			name:  "the revoke fails for a reason that is not the database being away: a 500, not a 200",
			tweak: func(s *raceStore) { s.revokeAllErr = errRaceBug },
			want:  http.StatusInternalServerError, wantMessage: notChanged,
			wantLog: "failed to revoke the user's sessions",
		},
		{
			name:  "the server refuses the commit (a serialization failure): it did not commit",
			tweak: func(s *raceStore) { s.commitErr = pgState("40001") },
			want:  http.StatusServiceUnavailable, wantMessage: notChanged,
			wantLog: "could not commit the transaction",
		},

		// A commit whose answer did not arrive is settled by reading the user back.
		{
			name:  "the commit does not complete, and the read-back finds the old password: it did not land",
			tweak: func(s *raceStore) { s.commitErr = errRaceTransient },
			want:  http.StatusServiceUnavailable, wantMessage: notChanged,
			wantLog: "reading the user back found the old password in place", settled: true,
		},
		{
			name:  "the commit does not complete, and the read-back finds the new password: it landed, and the change is made",
			tweak: func(s *raceStore) { s.commitErr = errRaceTransient; s.commitLands = true },
			want:  http.StatusOK, wantMessage: "Password changed successfully",
			changed: true, revoked: true, redisGone: true, audited: true, wantCommitted: true,
			wantLog: "reading the user back shows the new password in place", settled: true,
		},
		{
			name:  "the commit runs out of the bound, and the read-back finds the old password",
			tweak: func(s *raceStore) { s.commitErr = late },
			want:  http.StatusServiceUnavailable, wantMessage: notChanged,
			wantLog: "reading the user back found the old password in place", settled: true,
		},
		{
			name:  "the commit fails with an error nobody classified, and the read-back finds the old password: a 500 that says NOT changed",
			tweak: func(s *raceStore) { s.commitErr = errRaceBug },
			want:  http.StatusInternalServerError, wantMessage: notChanged,
			wantLog: "reading the user back found the old password in place", settled: true,
		},

		// pgx calls a connection that closes while the reply is awaited "conn closed" and says
		// SafeToRetry, which does not mean the COMMIT was never sent (internal/db pins this): it
		// is settled like any COMMIT whose answer was lost, not answered "NOT changed".
		{
			name:  "the connection closes while the reply is awaited (conn closed, SafeToRetry), and the read-back finds the new password: it landed",
			tweak: func(s *raceStore) { s.commitErr, s.commitLands = connClosedError{}, true },
			want:  http.StatusOK, wantMessage: "Password changed successfully",
			changed: true, revoked: true, redisGone: true, audited: true, wantCommitted: true,
			wantLog: "reading the user back shows the new password in place", settled: true,
		},
		{
			name:  "the connection closes while the reply is awaited (conn closed, SafeToRetry), and the read-back finds the old password: it did not land",
			tweak: func(s *raceStore) { s.commitErr = connClosedError{} },
			want:  http.StatusServiceUnavailable, wantMessage: notChanged,
			wantLog: "reading the user back found the old password in place", settled: true,
		},

		// The one case left unconfirmed: the read-back fails as well.
		{
			name:  "the commit does not complete and the read-back fails too: unconfirmed, and it did not land",
			tweak: func(s *raceStore) { s.commitErr = errRaceTransient; s.afterCommitUserErr = errRaceTransient },
			want:  http.StatusServiceUnavailable, wantMessage: unconfirmed,
			wantLog: "UNCONFIRMED", settled: true,
		},
		{
			name: "the commit does not complete and the read-back fails too: unconfirmed, and it DID land: the same answer, the opposite state",
			tweak: func(s *raceStore) {
				s.commitErr, s.commitLands, s.afterCommitUserErr = errRaceTransient, true, errRaceTransient
			},
			want: http.StatusServiceUnavailable, wantMessage: unconfirmed,
			changed: true, revoked: true, wantCommitted: true,
			wantLog: "UNCONFIRMED", settled: true,
		},
		{
			// Whichever way the COMMIT went there is no password left to change or sign in
			// with, and "try the new password" would send the user nowhere.
			name:  "the commit does not complete and the account is gone when it is read back: a 409 that says so",
			tweak: func(s *raceStore) { s.commitErr = errRaceTransient; s.afterCommitUserErr = pgx.ErrNoRows },
			want:  http.StatusConflict, wantMessage: "account was removed",
			wantLog: "the account was removed before a commit whose outcome is unknown could be settled", settled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := newAuthRaceApp(t, withPasswordHash(t, tt.tweak))
			a.pool.beginErr = tt.begin
			a.redis.Set(a.sessionKey(), "{}")
			userID := a.store.user.ID.String()

			resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
			body := authRequireStatus(t, resp, tt.want)

			msg, _ := body["message"].(string)
			if !strings.Contains(msg, tt.wantMessage) {
				t.Errorf("message = %q, want it to contain %q", msg, tt.wantMessage)
			}
			// What the message promises must be what the store holds.
			switch tt.wantMessage {
			case notChanged:
				if a.passwordChanged() || a.store.snapshot().IsRevoked {
					t.Errorf("the answer says the password was NOT changed, but the store holds changed=%t revoked=%t",
						a.passwordChanged(), a.store.snapshot().IsRevoked)
				}
			case unconfirmed:
				if strings.Contains(msg, notChanged) {
					t.Errorf("an unconfirmed commit says the password was NOT changed: %q", msg)
				}
				if !strings.Contains(msg, "Sign Out All Devices") || !strings.Contains(msg, "may be in effect") {
					t.Errorf("message = %q, want it to say the change may be in effect and how to make sure", msg)
				}
				// Revoke-all ends EVERY session, the caller's too, so "your other sessions" would
				// be wrong; and a refused new password must mean something to the user.
				if !strings.Contains(msg, "this one included") || !strings.Contains(msg, "old password is still in effect") {
					t.Errorf("message = %q, want it to say every session, this one included, may be signed out, and that if the new password is refused the old one is still in effect", msg)
				}
			}

			if got := a.passwordChanged(); got != tt.changed {
				t.Errorf("password changed = %t, want %t", got, tt.changed)
			}
			if got := a.store.snapshot().IsRevoked; got != tt.revoked {
				t.Errorf("session revoked = %t, want %t: the password and the sessions must move together", got, tt.revoked)
			}
			if got := !a.redis.Exists(a.sessionKey()); got != tt.redisGone {
				t.Errorf("Redis row deleted = %t, want %t: the cleanup follows a commit that is KNOWN to have landed", got, tt.redisGone)
			}
			wantAudit := []string(nil)
			if tt.audited {
				wantAudit = []string{"password_changed"}
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, wantAudit) {
				t.Errorf("audit actions = %v, want %v", got, wantAudit)
			}

			wantSettles := 0
			if tt.settled {
				wantSettles = 1
			}
			if got := len(a.store.named("GetUserByID")); got != 1 {
				t.Errorf("the user was read %d times, want once, at the start", got)
			}
			if got := len(a.store.named("GetPasswordHashForSettle")); got != wantSettles {
				t.Errorf("the locking read that settles a COMMIT ran %d times, want %d: it runs once, and only for a COMMIT whose outcome is unknown", got, wantSettles)
			}

			began := a.pool.txCount()
			if tt.wantNoTx {
				if began != 0 {
					t.Errorf("%d transactions were begun although Begin failed", began)
				}
			} else if began != 1 {
				t.Errorf("%d transactions were begun, want exactly 1", began)
			} else if committed, _ := a.pool.only(t).state(); committed != tt.wantCommitted {
				t.Errorf("transaction committed = %t, want %t", committed, tt.wantCommitted)
			}
			authRequireReadCommitted(t, a.pool, "password change")

			if tt.wantLog != "" {
				if out := logLinesFor(logs, userID); !strings.Contains(out, tt.wantLog) {
					t.Errorf("no log line for this user says %q: %q", tt.wantLog, out)
				}
			}
		})
	}
}

// TestChangePassword_EveryStatementInTheTransactionIsBounded holds each database statement
// of the request to the bound: the read of the user that opens it, then the update, the
// listing of the sessions, the revoke and the commit each wait for a row lock that is never
// granted. The answer comes back within the bound, measured from the moment the statement
// began to wait (the hash before the transaction is CPU time and is not charged), and is
// the answer for where it happened: NOT changed. A commit's outcome is unknown and settled by
// reading the user back, which finds the old password. Whichever it was, nothing was audited
// (not even attempted: a row for a change that was not made is a false record), the session's
// Redis row stays, and a transaction that was begun is rolled back on a context of its own.
func TestChangePassword_EveryStatementInTheTransactionIsBounded(t *testing.T) {
	const bound = 200 * time.Millisecond

	for _, tc := range []struct {
		statement string
		// beforeTx marks the read of the user, which comes before any transaction.
		beforeTx bool
	}{
		{"GetUserByID", true},
		{"UpdatePassword", false},
		{"ListUserSessions", false},
		{"RevokeAllUserSessions", false},
		{"Commit", false},
	} {
		t.Run("the "+tc.statement+" waits for a lock", func(t *testing.T) {
			t.Parallel()
			a := newAuthRaceAppWith(t, withPasswordHash(t, nil), raceOptions{dbTimeout: bound, followUpTimeout: bound})
			w := a.watchRowLock(t, tc.statement)
			a.redis.Set(a.sessionKey(), "{}")

			requestAt := time.Now()
			resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
			answeredAt := time.Now()
			body := decodeObject(t, resp)

			if got := w.waited(); len(got) != 1 || got[0] != tc.statement {
				t.Fatalf("statements that waited = %v, want [%s]: the lock was never met, so this test proved nothing", got, tc.statement)
			}
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
			}
			w.checkAnswered(t, bound, requestAt, answeredAt)
			if msg, _ := body["message"].(string); !strings.Contains(msg, "NOT changed") {
				t.Errorf("message = %q, want it to contain %q", msg, "NOT changed")
			}
			if a.passwordChanged() || a.store.snapshot().IsRevoked {
				t.Error("the password or the sessions changed although the transaction did not commit")
			}
			if got := a.store.auditActions(); len(got) != 0 {
				t.Errorf("audit actions = %v, want none: the password was not changed", got)
			}
			if n := len(a.store.named("InsertAuditLog")); n != 0 {
				t.Errorf("the audit insert was attempted %d times for a change that was not made", n)
			}
			if !a.redis.Exists(a.sessionKey()) {
				t.Error("the session's Redis row was deleted although the session was not revoked")
			}
			if n := a.store.openTransactions(); n != 0 {
				t.Errorf("%d transactions are still open", n)
			}

			switch {
			case tc.beforeTx:
				if n := a.pool.beginAttempts(); n != 0 {
					t.Errorf("a transaction was asked for %d times after a read that did not complete", n)
				}
				if n := len(a.store.named("UpdatePassword")); n != 0 {
					t.Errorf("UpdatePassword was sent %d times after a read that did not complete", n)
				}
			case tc.statement != "Commit":
				// A commit that failed has closed the transaction. For every other statement the
				// handler rolls back, and must do so on a context that can still be used: the
				// request's own has just run out, and a rollback sent on it never leaves.
				called, hasLimit, _, ctxErr := a.pool.only(t).rollbackContext()
				if !called {
					t.Error("the transaction was never rolled back")
				} else if ctxErr != nil || !hasLimit {
					t.Errorf("the rollback ran on a context that had ended (%v) or had no deadline (has one: %t)", ctxErr, hasLimit)
				}
			}
		})
	}
}

// TestChangePassword_TheRollbackAfterABoundedStallIsNotAFailure is the change-password side
// of releaseTx's rule. A statement that runs out of its bound makes pgx close the connection,
// so the rollback that follows finds it closed (pgconn.ErrConnClosed): not a failure, and a
// warning for it on every stall would bury the rollbacks that really fail, which are still
// logged under the handler's name. The rows share one log capture and the warning carries no
// user id, so the count is taken over both: exactly one line, the real failure's.
func TestChangePassword_TheRollbackAfterABoundedStallIsNotAFailure(t *testing.T) {
	logs := captureProductionLog(t)
	const line = "change password: transaction rollback failed"

	t.Run("rows", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			err  error
		}{
			{"the connection pgx closed when the statement ran out of its bound", fmt.Errorf("rollback: %w", pgconn.ErrConnClosed)},
			{"a rollback that really fails", errors.New("connection reset by peer")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				a := newAuthRaceAppWith(t, withPasswordHash(t, func(s *raceStore) { s.rollbackErr = tc.err }),
					raceOptions{dbTimeout: 200 * time.Millisecond, followUpTimeout: 200 * time.Millisecond})
				a.watchRowLock(t, "RevokeAllUserSessions")

				resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)

				if resp.StatusCode != http.StatusServiceUnavailable {
					t.Fatalf("status = %d, want 503: the stall did not happen, so this test proved nothing", resp.StatusCode)
				}
			})
		}
	})

	if got := strings.Count(logs.String(), line); got != 1 {
		t.Errorf("%q was logged %d times, want exactly 1 (the rollback that really failed, and not the one that found the connection closed): %q",
			line, got, logs.String())
	}
}

// TestChangePassword_TheReadOfTheUserIsBoundedAndNothingFollowsAFailedOne holds the read
// that opens the request, before any hashing, to the bound: a pool that cannot answer it is
// a 503 within the bound that says the password was NOT changed, with nothing begun or
// written; any other failure is a 500 that says the same.
func TestChangePassword_TheReadOfTheUserIsBoundedAndNothingFollowsAFailedOne(t *testing.T) {
	const bound = 200 * time.Millisecond

	t.Run("the pool cannot answer it", func(t *testing.T) {
		a := newAuthRaceAppWith(t, withPasswordHash(t, nil), raceOptions{gateSize: 1, dbTimeout: bound})
		a.holdPool(t)

		resp, elapsed := a.postAs(t, "/auth/change-password", changeBody, 5*time.Second)
		body := authRequireStatus(t, resp, http.StatusServiceUnavailable)

		checkBounded(t, elapsed, bound)
		if msg, _ := body["message"].(string); !strings.Contains(msg, "NOT changed") {
			t.Errorf("message = %q, want it to say the password was NOT changed", msg)
		}
		if n := a.pool.beginAttempts(); n != 0 {
			t.Errorf("a transaction was asked for %d times after a read that did not complete", n)
		}
		if n := len(a.store.named("UpdatePassword")); n != 0 {
			t.Errorf("UpdatePassword was sent %d times", n)
		}
	})

	for _, tc := range []struct {
		name    string
		err     error
		want    int
		wantLog string
	}{
		{"the database is away", errRaceTransient, http.StatusServiceUnavailable, "could not read the user"},
		{"a defect", errRaceBug, http.StatusInternalServerError, "failed to read the user"},
	} {
		t.Run("the read fails: "+tc.name, func(t *testing.T) {
			logs := captureProductionLog(t)
			a := newAuthRaceApp(t, withPasswordHash(t, func(s *raceStore) { s.userErr = tc.err }))

			resp, _ := a.postAs(t, "/auth/change-password", changeBody, 5*time.Second)
			body := authRequireStatus(t, resp, tc.want)

			// A 500 that said only "Failed to fetch user" left a user who had just typed a new
			// password to wonder; every failure of this request leaves it unchanged.
			if msg, _ := body["message"].(string); !strings.Contains(msg, "NOT changed") {
				t.Errorf("message = %q, want it to say the password was NOT changed", msg)
			}
			if out := logLinesFor(logs, a.store.user.ID.String()); !strings.Contains(out, tc.wantLog) {
				t.Errorf("no log line for this user says %q: %q", tc.wantLog, out)
			}
			if n := a.pool.beginAttempts(); n != 0 {
				t.Errorf("a transaction was asked for %d times after a read that failed", n)
			}
		})
	}
}

// TestChangePassword_HashingIsNotChargedToTheDatabaseBound sets the bound far shorter than
// hashing the new password takes and the change still goes through: the bound belongs to the
// database work, started twice (the read of the user, the transaction) with the hash between
// them. A single bound started before the hash would be spent by the time the transaction
// began, and every change would end as a 503.
func TestChangePassword_HashingIsNotChargedToTheDatabaseBound(t *testing.T) {
	// The production work factor, not TestMain's cheapest: the hash must outlast the 100 ms
	// bound with or without the race detector.
	defer auth.SetBcryptCostForTesting(0)()
	a := newAuthRaceAppWith(t, withPasswordHash(t, nil), raceOptions{dbTimeout: 100 * time.Millisecond, followUpTimeout: 5 * time.Second})

	start := time.Now()
	resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
	took := time.Since(start)

	if took < 100*time.Millisecond {
		t.Fatalf("the request took %v, inside the bound: the hash did not outlast it, so this test proved nothing", took)
	}
	authRequireStatus(t, resp, http.StatusOK)
	// Not a.passwordChanged(): checking the new cost-12 hash would cost another hash's time.
	if a.store.snapshotUser().PasswordHash == racePasswordHash(t) || !a.store.snapshot().IsRevoked {
		t.Error("the change did not go through")
	}
}

// TestChangePassword_ABeginThatCannotGetAConnectionIsBounded holds the start of the
// transaction to the bound: the read of the user succeeds and the pool is then taken, so
// Begin waits for a connection that never comes. The answer is the 503 that says NOT
// changed, within the bound measured from the moment Begin started to wait, with nothing written.
func TestChangePassword_ABeginThatCannotGetAConnectionIsBounded(t *testing.T) {
	const bound = 200 * time.Millisecond
	a := newAuthRaceAppWith(t, withPasswordHash(t, nil), raceOptions{gateSize: 1, dbTimeout: bound})
	taken, give := a.starveAfter("GetUserByID")
	defer give()
	beginAt := make(chan time.Time, 1)
	a.pool.beforeBegin = func() {
		select {
		case beginAt <- time.Now():
		default:
		}
	}

	requestAt := time.Now()
	resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
	answeredAt := time.Now()
	body := decodeObject(t, resp)

	if !taken() {
		t.Fatal("the pool was never taken: the read of the user did not run, so this test proved nothing")
	}
	var beganAt time.Time
	select {
	case beganAt = <-beginAt:
	default:
		t.Fatal("Begin was never reached")
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
	}
	checkAnsweredAtTheDeadline(t, bound, requestAt, beganAt, a.pool.beginDeadline(), answeredAt)
	if msg, _ := body["message"].(string); !strings.Contains(msg, "NOT changed") {
		t.Errorf("message = %q, want it to say the password was NOT changed", msg)
	}
	if a.passwordChanged() || a.store.snapshot().IsRevoked {
		t.Error("the password or the sessions changed although no transaction ever began")
	}
}

// TestChangePassword_TheCleanupAndTheAuditAreFollowUps pins what comes after the commit. The
// change is made; deleting the revoked sessions' Redis rows and recording the change run
// under deadlines of their own: each is bounded, and neither can take the answer down or be
// starved by a budget the deciding work spent. The last row spends the main budget on
// purpose (the commit waits for its lock until the bound has run out, then is let through)
// so the follow-ups meet a request whose own bound is gone, and must still run.
func TestChangePassword_TheCleanupAndTheAuditAreFollowUps(t *testing.T) {
	const bound = 200 * time.Millisecond

	t.Run("a Redis that never answers the cleanup is bounded, and the change stands", func(t *testing.T) {
		t.Parallel()
		a := newAuthRaceAppWith(t, withPasswordHash(t, nil), raceOptions{dbTimeout: bound * 25, followUpTimeout: bound})
		started := a.stallCleanup()

		requestAt := time.Now()
		resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
		answeredAt := time.Now()

		var start cleanupStart
		select {
		case start = <-started:
		default:
			t.Fatal("the Redis cleanup never ran, so this test proved nothing")
		}
		authRequireStatus(t, resp, http.StatusOK)
		checkAnsweredAtTheDeadline(t, bound, requestAt, start.at, start.deadline, answeredAt)
		if !a.passwordChanged() || !a.store.snapshot().IsRevoked {
			t.Error("the change did not stand")
		}
		want := []string{"password_changed"}
		if !reflect.DeepEqual(start.audits, want) {
			t.Errorf("audit actions when the cleanup started = %v, want %v: the record must come before the cache is touched", start.audits, want)
		}
		if got := a.store.auditActions(); !reflect.DeepEqual(got, want) {
			t.Errorf("audit actions = %v, want %v", got, want)
		}
	})

	t.Run("an audit insert that stalls is bounded, and the change stands", func(t *testing.T) {
		t.Parallel()
		a := newAuthRaceAppWith(t, withPasswordHash(t, nil), raceOptions{dbTimeout: bound * 25, followUpTimeout: bound})
		w := a.watchRowLock(t, "InsertAuditLog")

		requestAt := time.Now()
		resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
		answeredAt := time.Now()

		if len(w.waited()) != 1 {
			t.Fatalf("statements that waited = %v, want the audit insert", w.waited())
		}
		authRequireStatus(t, resp, http.StatusOK)
		w.checkAnswered(t, bound, requestAt, answeredAt)
		if !a.passwordChanged() {
			t.Error("the change did not stand")
		}
	})

	t.Run("with the main budget spent the cleanup still runs and the audit row is still written", func(t *testing.T) {
		t.Parallel()
		a := newAuthRaceAppWith(t, withPasswordHash(t, nil), raceOptions{dbTimeout: bound, followUpTimeout: 5 * time.Second})
		a.redis.Set(a.sessionKey(), "{}")
		spendBudgetAt(a, "Commit")

		resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)

		authRequireStatus(t, resp, http.StatusOK) // the commit landed as the bound ran out
		if a.redis.Exists(a.sessionKey()) {
			t.Error("the revoked session's Redis row was not deleted: the cleanup ran on the spent budget")
		}
		if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{"password_changed"}) {
			t.Errorf("audit actions = %v, want [password_changed]: the audit ran on the spent budget", got)
		}
	})
}

// TestChangePassword_ACommitThatNeverWentOutRolledBack pins, through the handler, that a
// COMMIT which was never sent is not an unknown outcome. The statements before it use up the
// whole bound, so the COMMIT is begun on a context that has ended and pgconn refuses it
// without sending anything (SafeToRetry). The server rolls the transaction back, so the
// answer is NOT changed ("may be in effect" would send the user to a sign-in that cannot
// succeed), and there is nothing to settle: the user is read once, at the start.
func TestChangePassword_ACommitThatNeverWentOutRolledBack(t *testing.T) {
	logs := captureProductionLog(t)
	const bound = 200 * time.Millisecond
	a := newAuthRaceAppWith(t, withPasswordHash(t, nil), raceOptions{dbTimeout: bound, followUpTimeout: 5 * time.Second})
	a.redis.Set(a.sessionKey(), "{}")
	spendBudgetAt(a, "RevokeAllUserSessions")

	resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
	body := authRequireStatus(t, resp, http.StatusServiceUnavailable)

	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "NOT changed") || strings.Contains(msg, "could not be confirmed") {
		t.Errorf("message = %q, want it to say the password was NOT changed: the COMMIT never went out, so the server rolled the transaction back", msg)
	}
	if out := logLinesFor(logs, a.store.user.ID.String()); !strings.Contains(out, "context already done") {
		t.Errorf("no log line for this user shows a COMMIT refused for its ended context: %q, so the row never reached the case it is about", out)
	}
	if a.passwordChanged() || a.store.snapshot().IsRevoked {
		t.Error("the password or the sessions changed although the COMMIT never went out")
	}
	if committed, _ := a.pool.only(t).state(); committed {
		t.Error("the transaction committed")
	}
	if !a.redis.Exists(a.sessionKey()) {
		t.Error("the session's Redis row was deleted although the session was not revoked")
	}
	if got := a.store.auditActions(); len(got) != 0 {
		t.Errorf("audit actions = %v, want none: the password was not changed", got)
	}
	if n := len(a.store.named("GetUserByID")); n != 1 {
		t.Errorf("the user was read %d times, want once", n)
	}
	if n := len(a.store.named("GetPasswordHashForSettle")); n != 0 {
		t.Errorf("the COMMIT was settled %d times: a COMMIT that never went out has nothing to settle", n)
	}
}

// TestChangePassword_TheUpdateIsConditionalOnTheVerifiedPassword pins that the update is a
// compare-and-swap on the hash the old password was checked against. Another request that
// verified the same password, or an administrator, changes it in the gap between this
// request's check and its update; or the account is deleted in that gap. The update then
// matches no row and the request rolls back before revoking anything and answers 409:
// nothing was changed, the other change stands (and the sessions it created are not
// revoked), and a deleted account is not told "Password changed successfully" for a change
// that went nowhere. Every row also pins what the update is sent: the new hash, the user's
// id, and the hash that was verified.
func TestChangePassword_TheUpdateIsConditionalOnTheVerifiedPassword(t *testing.T) {
	logs := captureProductionLog(t)
	const otherHash = "the-hash-of-a-password-another-request-set"
	verified := racePasswordHash(t)

	tests := []struct {
		name string
		// between runs as the update is about to execute, with the store locked: what happened
		// to the account since this request checked the old password.
		between func(*raceStore)
		// want409 says whether the update must match nothing; wantStored is then the hash the
		// account must still hold.
		want409    bool
		wantStored string
	}{
		{name: "nothing intervenes: the update is made, to the password that was verified"},
		{
			name:    "another request changed the password after this one checked the old one",
			between: func(s *raceStore) { s.user.PasswordHash = otherHash },
			want409: true, wantStored: otherHash,
		},
		{
			name:    "the account was deleted after the old password was checked",
			between: func(s *raceStore) { s.userDeleted = true },
			want409: true, wantStored: verified,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := newAuthRaceApp(t, withPasswordHash(t, nil))
			a.redis.Set(a.sessionKey(), "{}")
			userID := a.store.user.ID
			if tt.between != nil {
				a.store.lockWait = func(_ context.Context, name string) error {
					if name == "UpdatePassword" {
						a.store.mu.Lock()
						tt.between(a.store)
						a.store.mu.Unlock()
					}
					return nil
				}
			}

			resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
			body := decodeObject(t, resp)

			updates := a.store.named("UpdatePassword")
			if len(updates) != 1 {
				t.Fatalf("UpdatePassword was sent %d times, want once", len(updates))
			}
			args := updates[0].args
			if args[0] == verified || args[1] != userID || args[2] != verified {
				t.Errorf("UpdatePassword args = (new hash differs: %t, user: %v, expected hash is the verified one: %t): it must send the new hash, the user's id, and the hash the old password was checked against",
					args[0] != verified, args[1], args[2] == verified)
			}

			if !tt.want409 {
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
				}
				if !a.passwordChanged() || !a.store.snapshot().IsRevoked {
					t.Error("the change was not made")
				}
				if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{"password_changed"}) {
					t.Errorf("audit actions = %v, want [password_changed]", got)
				}
				return
			}

			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body %v)", resp.StatusCode, body)
			}
			if msg, _ := body["message"].(string); !strings.Contains(msg, "nothing was changed") {
				t.Errorf("message = %q, want it to say nothing was changed", msg)
			}
			if got := a.store.snapshotUser().PasswordHash; got != tt.wantStored {
				t.Errorf("the account holds %q, want %q: the update must not overwrite what it was not conditional on", got, tt.wantStored)
			}
			if a.passwordChanged() {
				t.Error("the password was changed to the new one")
			}
			if a.store.snapshot().IsRevoked {
				t.Error("a session was revoked by a request that changed nothing")
			}
			if n := len(a.store.named("ListUserSessions")) + len(a.store.named("RevokeAllUserSessions")); n != 0 {
				t.Errorf("the request went on to revoke sessions (%d statements) after an update that matched no row", n)
			}
			if got := a.store.auditActions(); len(got) != 0 {
				t.Errorf("audit actions = %v, want none: nothing was changed", got)
			}
			if !a.redis.Exists(a.sessionKey()) {
				t.Error("the session's Redis row was deleted although the session was not revoked")
			}
			if committed, rolledBack := a.pool.only(t).state(); committed || !rolledBack {
				t.Errorf("transaction committed = %t, rolled back = %t, want it rolled back", committed, rolledBack)
			}
			if out := logLinesFor(logs, userID.String()); !strings.Contains(out, "between the check of the old password and the update") {
				t.Errorf("no log line for this user says the update matched no row: %q", out)
			}
		})
	}
}

// TestChangePassword_TheReadBackRunsOnADeadlineOfItsOwn holds the locking read that settles
// a COMMIT of unknown outcome to a follow-up deadline. The commit's answer is lost as the
// request's own bound runs out, so the read-back meets a request with nothing left: on that
// budget it fails at once and a change that landed would be answered unconfirmed. On a
// deadline of its own it settles (landed is the success, not landed is NOT changed), and
// when it waits for a transaction that never ends, as a locking read does, it costs the
// follow-up bound and no more, and the answer is the honest "unconfirmed".
func TestChangePassword_TheReadBackRunsOnADeadlineOfItsOwn(t *testing.T) {
	const bound = 200 * time.Millisecond

	for _, tc := range []struct {
		name        string
		lands       bool
		want        int
		wantMessage string
		changed     bool
	}{
		{"the budget is spent, the answer to the commit is lost, and it landed", true, http.StatusOK, "Password changed successfully", true},
		{"the budget is spent, the answer to the commit is lost, and it did not land", false, http.StatusServiceUnavailable, "NOT changed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := newAuthRaceAppWith(t, withPasswordHash(t, func(s *raceStore) { s.commitErr, s.commitLands = errRaceTransient, tc.lands }),
				raceOptions{dbTimeout: bound, followUpTimeout: 5 * time.Second})
			a.redis.Set(a.sessionKey(), "{}")
			spendBudgetAt(a, "Commit")

			resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
			body := authRequireStatus(t, resp, tc.want)

			if msg, _ := body["message"].(string); !strings.Contains(msg, tc.wantMessage) {
				t.Errorf("message = %q, want it to contain %q", msg, tc.wantMessage)
			}
			if got := a.passwordChanged(); got != tc.changed {
				t.Errorf("password changed = %t, want %t", got, tc.changed)
			}
			if tc.changed {
				if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{"password_changed"}) {
					t.Errorf("audit actions = %v, want [password_changed]", got)
				}
				if a.redis.Exists(a.sessionKey()) {
					t.Error("the revoked session's Redis row was not deleted")
				}
			}
		})
	}

	t.Run("a read-back that stalls is bounded and leaves the answer unconfirmed", func(t *testing.T) {
		t.Parallel()
		a := newAuthRaceAppWith(t, withPasswordHash(t, func(s *raceStore) { s.commitErr = errRaceTransient }),
			raceOptions{dbTimeout: bound * 25, followUpTimeout: bound})
		w := a.watchRowLock(t, "GetPasswordHashForSettle") // a transaction that stays open: the locking read waits for it

		requestAt := time.Now()
		resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
		answeredAt := time.Now()
		body := authRequireStatus(t, resp, http.StatusServiceUnavailable)

		if len(w.waited()) != 1 {
			t.Fatalf("statements that waited = %v, want the read-back: it never ran, so this test proved nothing", w.waited())
		}
		w.checkAnswered(t, bound, requestAt, answeredAt)
		if msg, _ := body["message"].(string); !strings.Contains(msg, "could not be confirmed") {
			t.Errorf("message = %q, want it to say the change could not be confirmed", msg)
		}
	})
}

// spendBudgetAt makes the named statement take the whole of its request's budget: it waits
// until its context has ended and is then let through, as a statement that completes just as
// the bound runs out would be. Everything after it on that context fails at once; whatever
// must still run has to run on a context of its own.
func spendBudgetAt(a *authRaceApp, name string) {
	a.store.lockWait = func(ctx context.Context, n string) error {
		if n == name {
			<-ctx.Done()
		}
		return nil
	}
}
