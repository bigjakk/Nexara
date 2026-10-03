package handlers

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/auth"
)

// A statement can wait without needing a connection: it has one, and the row it
// wants is locked by another transaction. The pool-starvation tests cannot show
// that, because a connection was granted; and a stand-in that ignores the context
// can never show it either, which is how the bound on the statements INSIDE
// Refresh's transaction, and on the revoke in its guard branches, went unpinned —
// putting one of them on a context that never ends passed every test.
//
// The stand-in now models the wait (raceStore.lockWait): a statement named in
// holdRowLock waits for its context to end, as a real lock wait does, and the
// handler must come back within its bound with nothing decided.

// rowLockWatch is what holdRowLock saw of the statements it made wait.
type rowLockWatch struct {
	mu          sync.Mutex
	names       []string
	first       time.Time // when the first began to wait
	deadline    time.Time // the deadline the context of that first statement carried
	hasDeadline bool
}

// waited reports the statements that had to wait, in order.
func (w *rowLockWatch) waited() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.names...)
}

// firstWait reports when the first of them began to wait — the moment from which a
// bound is measured, for a request that did other work (hashing a password) before
// it reached the database.
func (w *rowLockWatch) firstWait() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.first
}

// checkAnswered holds the answer of a request, in which a statement waited for a lock
// that is never granted, to the bound of the context that statement carried (see
// checkAnsweredAtTheDeadline). requestAt is when the request was sent, answeredAt when
// the answer arrived.
func (w *rowLockWatch) checkAnswered(t *testing.T, bound time.Duration, requestAt, answeredAt time.Time) {
	t.Helper()
	w.mu.Lock()
	first, deadline, has := w.first, w.deadline, w.hasDeadline
	w.mu.Unlock()
	if first.IsZero() {
		t.Error("no statement waited, so there is no bound to check")
		return
	}
	if !has {
		deadline = time.Time{}
	}
	checkAnsweredAtTheDeadline(t, bound, requestAt, first, deadline, answeredAt)
}

// checkAnsweredAtTheDeadline holds the answer to a request in which a call waited for
// something that never came to the bound of the context that call carried.
//
// A context's clock starts when it is created, and the call reaches the point where it
// waits some time later — after the request has begun a transaction, run the
// statements before it, hashed a password. So the answer is held to the context's own
// DEADLINE, not to a bound counted from the moment the call began to wait: a correct
// answer comes a few milliseconds before that bound has elapsed since the wait began,
// by however long the request took to get there, and under load that is enough to fail
// a test that has nothing wrong with it. What is checked:
//
//   - the answer does not arrive before the deadline: the handler waited for it;
//   - the deadline is at least bound after the request was sent, and at most bound
//     after the call began to wait — the context was created between the two, so a
//     handler that gave the call a shorter or a longer bound than the one configured
//     is still noticed;
//   - the answer comes within a few seconds of the wait beginning.
//
// A zero deadline is a call that carried none.
func checkAnsweredAtTheDeadline(t *testing.T, bound time.Duration, requestAt, waitStart, deadline, answeredAt time.Time) {
	t.Helper()
	if deadline.IsZero() {
		t.Error("the call that waited carried a context with no deadline: nothing bounded it")
		return
	}
	if answeredAt.Before(deadline) {
		t.Errorf("answered %v before the deadline of the context the waiting call carried: it did not wait for its bound", deadline.Sub(answeredAt))
	}
	if deadline.Before(requestAt.Add(bound)) {
		t.Errorf("the context the waiting call carried ends %v after the request was sent, shorter than the bound of %v it should have been given",
			deadline.Sub(requestAt), bound)
	}
	if deadline.After(waitStart.Add(bound)) {
		t.Errorf("the context the waiting call carried ends %v after the call began to wait, longer than the bound of %v it should have been given",
			deadline.Sub(waitStart), bound)
	}
	if after := answeredAt.Sub(waitStart); after > 3*time.Second {
		t.Errorf("answered %v after the call began to wait, long after its bound", after)
	}
}

// watchRowLock makes every statement named in names wait, as if for a row lock
// another transaction holds, until its context ends or the test does, and returns what
// it saw of them: which had to wait, when the first began, and the deadline its context
// carried.
func (a *authRaceApp) watchRowLock(t *testing.T, names ...string) *rowLockWatch {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	w := &rowLockWatch{}
	a.store.lockWait = func(ctx context.Context, name string) error {
		if !slices.Contains(names, name) {
			return nil
		}
		w.mu.Lock()
		if len(w.names) == 0 {
			w.first = time.Now()
			w.deadline, w.hasDeadline = ctx.Deadline()
		}
		w.names = append(w.names, name)
		w.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}
	return w
}

// holdRowLock is watchRowLock for a test that only needs the two things it always
// did: which statements had to wait, and when the first began.
func (a *authRaceApp) holdRowLock(t *testing.T, names ...string) (waited func() []string, firstWait func() time.Time) {
	t.Helper()
	w := a.watchRowLock(t, names...)
	return w.waited, w.firstWait
}

// TestRefresh_EveryStatementInTheTransactionIsBounded holds the three things
// Refresh does on its transaction to the bound: the read of the user, the
// rotation and the commit. Each waits for a lock that is never granted. The
// answer is a 503 within the bound with the transaction closed, the cookie left
// alone, nothing issued, and — for the first two — nothing committed; and with the
// lock released the same request goes through, which is the control that the 503
// is the wait and not the harness.
//
// The rotation is the realistic one: its UPDATE waits on the session's row when a
// sign-out, a sign-out everywhere or another refresh holds it. The commit's row
// is the same wait seen from the other end.
func TestRefresh_EveryStatementInTheTransactionIsBounded(t *testing.T) {
	const bound = 200 * time.Millisecond

	for _, statement := range []string{"GetUserByID", "RotateSessionToken", "Commit"} {
		t.Run("the "+statement+" waits for a lock", func(t *testing.T) {
			a := newAuthRaceAppWith(t, nil, raceOptions{dbTimeout: bound})
			waited, _ := a.holdRowLock(t, statement)

			resp, elapsed := a.postTimed(t, "/auth/refresh", "{}", raceCurrentToken, nil, 5*time.Second)
			body := decodeObject(t, resp)

			if got := waited(); len(got) != 1 || got[0] != statement {
				t.Fatalf("statements that waited = %v, want [%s]: the lock was never met, so this test proved nothing", got, statement)
			}
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
			}
			checkBounded(t, elapsed, bound)
			if cookies := refreshCookies(resp); len(cookies) != 0 {
				t.Errorf("Set-Cookie = %+v, want the cookie left alone", cookies)
			}
			if _, issued := body["access_token"]; issued {
				t.Errorf("a refresh that did not complete issued an access token: %v", body)
			}
			if keys := a.redis.Keys(); len(keys) != 0 {
				t.Errorf("a refresh that did not complete wrote Redis rows %v", keys)
			}
			committed, rolledBack := a.pool.only(t).state()
			if committed {
				t.Error("the transaction committed")
			}
			if statement != "Commit" && !rolledBack {
				t.Error("the transaction was not rolled back, so its connection never went back to the pool")
			}
			if a.store.snapshot().TokenHash != auth.HashToken(raceCurrentToken) {
				t.Error("the session was rotated although the transaction did not commit")
			}
			if n := a.store.openTransactions(); n != 0 {
				t.Errorf("%d transactions are still open", n)
			}

			// Control: with the lock released the refresh goes through — for the commit
			// too, because the stand-in undoes the writes of a transaction whose commit
			// failed, as the server does.
			a.store.lockWait = nil
			if again := a.post(t, "/auth/refresh", raceCurrentToken, nil); again.StatusCode != http.StatusOK {
				t.Errorf("with the lock released the refresh answered %d, want 200", again.StatusCode)
			}
		})
	}
}

// TestRefresh_TheRevokeInAGuardBranchIsBounded holds the revoke each refusal
// branch makes — the user gone, the account disabled, the role changed — to the
// bound. The refresh is refused 401 with the cookie cleared whatever becomes of
// the revoke, so what is pinned is that it ANSWERS: the revoke waits for a lock
// that is never granted and the handler comes back within its bound instead of
// holding the response for as long as the lock is held. The failed revoke is
// logged, and the session stays live, as the log line says it will.
func TestRefresh_TheRevokeInAGuardBranchIsBounded(t *testing.T) {
	const bound = 200 * time.Millisecond

	tests := []struct {
		name  string
		tweak func(*raceStore)
	}{
		{"the user no longer exists", func(s *raceStore) { s.userErr = pgx.ErrNoRows }},
		{"the account is disabled", func(s *raceStore) { s.user.IsActive = false }},
		{"the user's role changed", func(s *raceStore) { s.user.Role = "viewer" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureProductionLog(t)
			a := newAuthRaceAppWith(t, tt.tweak, raceOptions{dbTimeout: bound, followUpTimeout: bound})
			waited, _ := a.holdRowLock(t, "RevokeSession")

			resp, elapsed := a.postTimed(t, "/auth/refresh", "{}", raceCurrentToken, nil, 5*time.Second)

			if got := waited(); len(got) != 1 {
				t.Fatalf("statements that waited = %v, want the revoke: the lock was never met, so this test proved nothing", got)
			}
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			checkBounded(t, elapsed, bound)
			if cookies := refreshCookies(resp); len(cookies) != 1 || !cookieDeleted(cookies[0]) {
				t.Errorf("Set-Cookie = %+v, want the cookie deleted", cookies)
			}
			if a.store.snapshot().IsRevoked {
				t.Error("the session is revoked although its revoke never got the lock")
			}
			if out := logs.String(); !strings.Contains(out, "refresh: could not revoke the session of a refused refresh") {
				t.Errorf("the revoke that gave up left no trace in the log: %q", out)
			}
		})
	}
}

// TestLogout_ARevokeThatWaitsForALockIsBounded is the sign-out's half: its revoke
// waits for a lock that is never granted. The answer is the 503 that says the
// sign-out could not be confirmed, within the bound, with the cookie cleared and
// the session not revoked.
func TestLogout_ARevokeThatWaitsForALockIsBounded(t *testing.T) {
	const bound = 200 * time.Millisecond
	a := newAuthRaceAppWith(t, nil, raceOptions{dbTimeout: bound})
	waited, _ := a.holdRowLock(t, "RevokeSession")

	resp, elapsed := a.postTimed(t, "/auth/logout", "{}", raceCurrentToken, nil, 5*time.Second)
	body := decodeObject(t, resp)

	if got := waited(); len(got) != 1 {
		t.Fatalf("statements that waited = %v, want the revoke: the lock was never met, so this test proved nothing", got)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
	}
	checkBounded(t, elapsed, bound)
	checkLogoutUnconfirmed(t, body)
	if cookies := refreshCookies(resp); len(cookies) != 1 || !cookieDeleted(cookies[0]) {
		t.Errorf("Set-Cookie = %+v, want the cookie deleted", cookies)
	}
	if a.store.snapshot().IsRevoked {
		t.Error("the session is revoked although its revoke never got the lock")
	}
}

// TestReleaseTx_RollsBackOnAContextOfItsOwn pins the context the rollback runs on.
// It is needed exactly when the request's own has just run out — a statement
// waited for its lock until the bound ended — and a rollback on THAT context
// cannot be sent: the transaction's connection would not go back to the pool, and
// the stall that ended the request would hold a connection of it for good. So it
// is a context of its own: not over, with a deadline, and no more than five
// seconds of it.
func TestReleaseTx_RollsBackOnAContextOfItsOwn(t *testing.T) {
	const bound = 200 * time.Millisecond
	a := newAuthRaceAppWith(t, nil, raceOptions{dbTimeout: bound})
	waited, _ := a.holdRowLock(t, "RotateSessionToken")

	resp, elapsed := a.postTimed(t, "/auth/refresh", "{}", raceCurrentToken, nil, 5*time.Second)

	if got := waited(); len(got) != 1 {
		t.Fatalf("statements that waited = %v, want the rotation: the lock was never met, so this test proved nothing", got)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if elapsed < bound {
		t.Fatalf("answered after %v, inside the bound of %v: the request's budget was never spent", elapsed, bound)
	}
	called, hasDeadline, left, err := a.pool.only(t).rollbackContext()
	if !called {
		t.Fatal("the transaction was never rolled back")
	}
	if err != nil {
		t.Errorf("the rollback ran on a context that was already over (%v): it was the request's, spent by the stall", err)
	}
	if !hasDeadline {
		t.Error("the rollback's context has no deadline: a rollback that stalls would hold the request for ever")
	} else if left <= 0 || left > releaseTxTimeout {
		t.Errorf("the rollback's context has %v left, want a fresh %v", left, releaseTxTimeout)
	}
}
