package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/auth"
)

// What Refresh and Logout do to the POOL, the few connections the whole process shares.
// Refresh's transaction holds one from Begin until it commits or rolls back, so a pool
// query made meanwhile needs a second: with one more such request than the pool has
// connections, every connection is held by a request waiting for another and no query in
// the process runs again, because a request's context has no deadline. Two defences, each
// tested: a transaction is released BEFORE any pool query (every harness test checks it, see
// authConnectionComplaints), and the database work of a request is bounded, so a pool
// starved for any other reason answers 503.

// holdPool takes the pool's only connection until release is called or the test ends.
func (a *authRaceApp) holdPool(t *testing.T) (release func()) {
	t.Helper()
	if err := a.gate.acquire(context.Background()); err != nil {
		t.Fatalf("take the pool's only connection: %v", err)
	}
	var once sync.Once
	release = func() { once.Do(a.gate.release) }
	t.Cleanup(release)
	return release
}

// refreshResult is the outcome of one refresh sent from a goroutine.
type refreshResult struct {
	status int
	cookie []*http.Cookie
	err    error
}

// refreshAll sends n refreshes at once, all presenting the session's current cookie, and
// returns when every one has answered. A request that does not answer within timeout is
// reported in its result, so the caller can say what the others did.
func (a *authRaceApp) refreshAll(n int, timeout time.Duration) []refreshResult {
	results := make([]refreshResult, n)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			resp, err := a.app.Test(raceRequest("/auth/refresh", "{}", raceCurrentToken, nil), fiber.TestConfig{Timeout: timeout, FailOnTimeout: true})
			if err != nil {
				results[i] = refreshResult{err: err}
				return
			}
			defer func() { _ = resp.Body.Close() }()
			results[i] = refreshResult{status: resp.StatusCode, cookie: refreshCookies(resp)}
		})
	}
	wg.Wait()
	return results
}

// lineUp makes requests concurrent refreshes meet at a pool of conns connections in the
// worst order, twice over. Nobody begins a transaction until every request has validated;
// and nobody who then holds a connection moves on until all of the pool's are held and
// the rest are queued for one. The moment the pool is seen saturated is latched, because it
// stops being so as soon as the first holder commits and its connection is handed on.
func (a *authRaceApp) lineUp(requests, conns int) {
	var arrived sync.WaitGroup
	arrived.Add(requests)
	a.pool.beforeBegin = func() {
		arrived.Done()
		arrived.Wait()
	}

	var holders atomic.Int32
	saturated := make(chan struct{})
	var latch sync.Once
	a.pool.afterBegin = func() {
		if int(holders.Add(1)) > conns { // only the first conns holders wait
			return
		}
		giveUp := time.After(10 * time.Second)
		for {
			changed := a.gate.changes()
			if a.gate.inUse() == conns && a.gate.waiters() == requests-conns {
				latch.Do(func() { close(saturated) })
			}
			select {
			case <-saturated:
				return
			case <-changed:
			case <-giveUp:
				return
			}
		}
	}
}

// TestRefresh_ConcurrentRefreshesNeverHoldThePoolWhileWaitingForIt is the deadlock, made
// to happen on a pool of a few connections. N requests present one cookie and line up with
// the pool saturated; one wins the rotation, and each loser, holding a connection with its
// rotation affecting no rows, must ask the pool whether it lost to a concurrent refresh. If
// it asks WITHOUT giving its connection back, nothing completes. Exactly one request wins
// and N-1 are answered 409, and every one completes within a timeout far shorter than the
// request's own.
func TestRefresh_ConcurrentRefreshesNeverHoldThePoolWhileWaitingForIt(t *testing.T) {
	tests := []struct {
		name     string
		conns    int
		requests int
	}{
		{"one connection, two refreshes", 1, 2},
		{"two connections, four refreshes", 2, 4},
		{"four connections, which is the pool's production minimum, and five refreshes", 4, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The handler's bound is far shorter than the request timeout, so a deadlock
			// fails as the handler's 503 or 401, naming the cause; and long enough that a
			// healthy run never meets it however the goroutines are scheduled.
			a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: tt.conns, dbTimeout: 5 * time.Second, concurrent: true})
			a.lineUp(tt.requests, tt.conns)

			results := a.refreshAll(tt.requests, 30*time.Second)

			won, spared := 0, 0
			for i, r := range results {
				switch {
				case r.err != nil:
					t.Errorf("request %d did not complete: %v", i, r.err)
				case r.status == http.StatusOK:
					won++
				case r.status == http.StatusConflict:
					spared++
				default:
					t.Errorf("request %d answered %d, want 200 (the winner) or 409 (a loser to a concurrent refresh): "+
						"a pool starved by its own transactions answers 503, or 401 once its lookups give up", i, r.status)
				}
			}
			if won != 1 || spared != tt.requests-1 {
				t.Errorf("%d winners and %d refusals sparing the cookie, want 1 and %d", won, spared, tt.requests-1)
			}
			if n := a.gate.inUse(); n != 0 {
				t.Errorf("%d of the pool's %d connections are still held after every request finished", n, tt.conns)
			}
			if n := a.store.openTransactions(); n != 0 {
				t.Errorf("%d transactions are still open after every request finished", n)
			}
			a.awaitSessionRedisRow(t)
		})
	}
}

// TestRefresh_AnAccountRefusalGivesItsConnectionBackBeforeItRevokes is the same deadlock on
// the other branches that go to the pool with a transaction open: each revokes the session
// through the pool (the last also writes an audit row there), so with as many such requests
// as the pool has connections every one holds a connection and asks for a second. Both
// requests must be answered 401 with the cookie cleared and, the part that fails when the
// request does not let go first, the session must really be revoked, by both: a revoke that
// ran out of the bound is swallowed by the handler and the 401 looks the same.
func TestRefresh_AnAccountRefusalGivesItsConnectionBackBeforeItRevokes(t *testing.T) {
	const conns = 2

	for _, tt := range authRefusedAccounts {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceAppWith(t, tt.tweak, raceOptions{gateSize: conns, dbTimeout: 2 * time.Second, concurrent: true})
			a.lineUp(conns, conns)

			for i, r := range a.refreshAll(conns, 15*time.Second) {
				switch {
				case r.err != nil:
					t.Errorf("request %d did not complete: %v", i, r.err)
				case r.status != http.StatusUnauthorized:
					t.Errorf("request %d answered %d, want 401", i, r.status)
				case len(r.cookie) != 1 || !cookieDeleted(r.cookie[0]):
					t.Errorf("request %d: Set-Cookie = %+v, want the cookie deleted", i, r.cookie)
				}
			}
			if !a.store.snapshot().IsRevoked {
				t.Error("the session is not revoked: a revoke that had to wait for a connection its own request was holding gave up after the bound")
			}
			if got := len(a.store.named("RevokeSession")); got != conns {
				t.Errorf("RevokeSession ran %d times, want once per request (%d)", got, conns)
			}
			wantAudits := 0
			if tt.audit != "" {
				wantAudits = conns
			}
			audits := a.store.auditActions()
			if len(audits) != wantAudits {
				t.Errorf("audit actions = %v, want %d of %q", audits, wantAudits, tt.audit)
			}
			for _, got := range audits {
				if got != tt.audit {
					t.Errorf("audit action %q, want %q", got, tt.audit)
				}
			}
			if n := a.gate.inUse(); n != 0 {
				t.Errorf("%d of the pool's %d connections are still held after every request finished", n, conns)
			}
			if n := a.store.openTransactions(); n != 0 {
				t.Errorf("%d transactions are still open after every request finished", n)
			}
		})
	}
}

// TestRefresh_AStarvedPoolIsA503NotAHang pins the second defence: the database work of a
// refresh is bounded, so a pool that cannot give it a connection is answered ("nothing was
// decided"), not waited on for ever. Another consumer holds the pool's only connection; the
// request must come back 503 after about the handler's bound, cookie untouched and nothing
// written, and the same request succeeds the moment the connection is free (the control).
// Both places a refresh can wait for the pool are covered: the lookup that opens it, and Begin.
func TestRefresh_AStarvedPoolIsA503NotAHang(t *testing.T) {
	const bound = 200 * time.Millisecond

	check := func(t *testing.T, a *authRaceApp, resp *http.Response, elapsed time.Duration) {
		t.Helper()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, decodeObject(t, resp))
		}
		checkBounded(t, elapsed, bound)
		authRequireCookie(t, resp, cookieUntouched)
		if n := len(a.store.named("RotateSessionToken")) + len(a.store.named("RevokeSession")); n != 0 {
			t.Errorf("a starved refresh changed the session (%d writes)", n)
		}
		if keys := a.redis.Keys(); len(keys) != 0 {
			t.Errorf("a starved refresh wrote Redis rows %v", keys)
		}
	}

	t.Run("the pool cannot answer the lookup that opens the refresh", func(t *testing.T) {
		a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: 1, dbTimeout: bound})
		release := a.holdPool(t)

		resp, elapsed := a.postTimed(t, "/auth/refresh", "{}", raceCurrentToken, nil, 5*time.Second)
		check(t, a, resp, elapsed)
		if n := len(a.store.stmts); n != 0 {
			t.Errorf("%d statements reached the database although the pool had no connection to run them on", n)
		}

		release()
		if again := a.post(t, "/auth/refresh", raceCurrentToken, nil); again.StatusCode != http.StatusOK {
			t.Errorf("with the pool free again the refresh answered %d, want 200", again.StatusCode)
		}
	})

	t.Run("the pool cannot give the refresh a connection to begin on", func(t *testing.T) {
		a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: 1, dbTimeout: bound})
		// The opening lookup runs, then another consumer takes the connection just before
		// Begin asks for it.
		taken := false
		a.pool.beforeBegin = func() { taken = a.gate.acquire(context.Background()) == nil }
		t.Cleanup(func() {
			if taken {
				a.gate.release()
			}
		})

		resp, elapsed := a.postTimed(t, "/auth/refresh", "{}", raceCurrentToken, nil, 5*time.Second)
		check(t, a, resp, elapsed)
		if !taken {
			t.Fatal("the pool's connection was never taken: Begin was not reached, so this test proved nothing")
		}
		if got := a.store.named("GetSessionByTokenHash"); len(got) != 1 {
			t.Errorf("the opening lookup ran %d times, want 1: the refresh should have got as far as Begin and no further", len(got))
		}
		if n := len(a.store.named("GetUserByID")); n != 0 {
			t.Errorf("the user was read %d times without a transaction to read it in", n)
		}
	})
}

// TestRefresh_AWriteThatFailsIsNotARefusalAndIssuesNothing pins the ways a refresh can fail
// after it has validated: the transaction cannot be started, the rotation fails, the commit
// fails. Each is a failure, not a refusal: the cookie is left alone, nothing is issued, no
// Redis row is written, and the answer is a 500, or a 503 where the cause is the database
// not answering. Each is logged with the session id and never a token or a hash. The control
// is the first row of TestRefresh_RotationOutcomes.
func TestRefresh_AWriteThatFailsIsNotARefusalAndIssuesNothing(t *testing.T) {
	boom := errRaceTransient
	bug := errRaceBug
	late := fmt.Errorf("timeout: %w", context.DeadlineExceeded)

	tests := []struct {
		name  string
		tweak func(*raceStore)
		begin error // what Begin fails with, if anything
		want  int
		// wantRolledBack is whether a transaction was begun and rolled back (rather than
		// committed or never begun).
		wantRolledBack bool
		wantNoTx       bool
		wantLog        string // the log line, which names the session
		// wantRotated is whether the session's token ended up rotated: only for a commit that
		// landed with its answer lost, where the client never got the new cookie and the
		// handler cannot know.
		wantRotated bool
	}{
		{
			name: "the transaction cannot be started", begin: boom, want: http.StatusServiceUnavailable, wantNoTx: true,
			wantLog: "refresh: transaction start failed; answering 503",
		},
		{
			name: "the transaction cannot be started within the bound", begin: context.DeadlineExceeded, want: http.StatusServiceUnavailable, wantNoTx: true,
			wantLog: "refresh: transaction start failed; answering 503",
		},
		{
			name:  "the rotation fails",
			tweak: func(s *raceStore) { s.rotateErr = bug },
			want:  http.StatusInternalServerError, wantRolledBack: true,
			wantLog: "refresh: rotate refresh token failed; answering 500",
		},
		{
			name:  "the rotation runs out of time",
			tweak: func(s *raceStore) { s.rotateErr = late },
			want:  http.StatusServiceUnavailable, wantRolledBack: true,
			wantLog: "refresh: rotate refresh token failed; answering 503",
		},
		{
			name:    "the commit fails",
			tweak:   func(s *raceStore) { s.commitErr = bug },
			want:    http.StatusInternalServerError,
			wantLog: "refresh: commit failed; its outcome is UNCONFIRMED",
		},
		{
			name:    "the commit runs out of time",
			tweak:   func(s *raceStore) { s.commitErr = late },
			want:    http.StatusServiceUnavailable,
			wantLog: "refresh: commit did not complete; its outcome is UNCONFIRMED",
		},
		{
			name:    "the commit lands and its answer is lost",
			tweak:   func(s *raceStore) { s.commitErr = boom; s.commitLands = true },
			want:    http.StatusServiceUnavailable,
			wantLog: "refresh: commit did not complete; its outcome is UNCONFIRMED", wantRotated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureProductionLog(t)
			a := newAuthRaceApp(t, tt.tweak)
			a.pool.beginErr = tt.begin

			resp := a.post(t, "/auth/refresh", raceCurrentToken, nil)
			body := authRequireStatus(t, resp, tt.want)

			out := logs.String()
			if !strings.Contains(out, tt.wantLog) {
				t.Errorf("no %q line in the log: %q", tt.wantLog, out)
			}
			if !strings.Contains(out, a.store.session.ID.String()) {
				t.Errorf("the log does not name the session: %q", out)
			}
			if strings.Contains(out, raceCurrentToken) || strings.Contains(out, auth.HashToken(raceCurrentToken)) {
				t.Errorf("the log carries the token or its hash: %q", out)
			}
			authRequireCookie(t, resp, cookieUntouched)
			if _, issued := body["access_token"]; issued {
				t.Errorf("a refresh that failed issued an access token: %v", body)
			}
			if keys := a.redis.Keys(); len(keys) != 0 {
				t.Errorf("a refresh that failed wrote Redis rows %v", keys)
			}
			if n := len(a.store.named("RevokeSession")); n != 0 {
				t.Errorf("a refresh that failed revoked the session (%d times)", n)
			}
			if rotated := a.store.snapshot().TokenHash != auth.HashToken(raceCurrentToken); rotated != tt.wantRotated {
				t.Errorf("the session was rotated = %t, want %t", rotated, tt.wantRotated)
			}
			if strings.Contains(tt.wantLog, "UNCONFIRMED") && !strings.Contains(out, "may have been rotated") {
				t.Errorf("the unconfirmed commit's log line does not say the session may have been rotated: %q", out)
			}

			if tt.wantNoTx {
				if began := a.pool.txCount(); began != 0 {
					t.Errorf("%d transactions were begun although Begin failed", began)
				}
				return
			}
			committed, rolledBack := a.pool.only(t).state()
			if committed != tt.wantRotated {
				t.Errorf("the transaction reports committed = %t, want %t", committed, tt.wantRotated)
			}
			if rolledBack != tt.wantRolledBack {
				t.Errorf("rolled back = %t, want %t", rolledBack, tt.wantRolledBack)
			}
		})
	}
}

// TestRefresh_AStarvedRefusalLookupIsBoundedToo covers the one pool query a refresh can
// make after the opening lookup without a transaction: the question a refusal asks, whether
// the stale token was one a concurrent refresh had just replaced. The cookie is a token no
// session holds, so the refusal asks, and the pool is taken between the two. The question
// must not wait for ever: it is answered as stale (401, cookie cleared, the caller already
// knows the token is not current), promptly.
func TestRefresh_AStarvedRefusalLookupIsBoundedToo(t *testing.T) {
	const bound = 200 * time.Millisecond
	a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: 1, dbTimeout: bound, followUpTimeout: bound})
	taken, give := a.starveAfter("GetSessionByTokenHash")
	defer give()

	resp, elapsed := a.postTimed(t, "/auth/refresh", "{}", "a-token-no-session-holds", nil, 5*time.Second)

	if !taken() {
		t.Fatal("the pool was never taken: the opening lookup did not run, so this test proved nothing")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %v)", resp.StatusCode, decodeObject(t, resp))
	}
	authRequireCookie(t, resp, cookieCleared)
	checkBounded(t, elapsed, bound)
	if n := len(a.store.named("GetSessionByPreviousTokenHash")); n != 0 {
		t.Errorf("the refusal's question reached the database %d times although the pool had no connection for it", n)
	}
}

// TestLogout_AStarvedPoolIsA503NotAHang is the same bound on Logout, which holds no
// transaction but waits for the pool like any request, with a context that has no deadline
// either. The pool is starved before the lookup, or goes dark just after it so that the
// revoke cannot get a connection. The answer is a 503 with the cookie cleared (as every
// Logout answer but the 403), a message that does not say "try again" (checkLogoutUnconfirmed),
// and nothing revoked; with the pool free again the same sign-out ends the session.
func TestLogout_AStarvedPoolIsA503NotAHang(t *testing.T) {
	const bound = 200 * time.Millisecond

	for _, tt := range []struct {
		name string
		// starve arms the starvation and returns whether it happened and how to undo it.
		starve func(t *testing.T, a *authRaceApp) (happened func() bool, undo func())
	}{
		{"before the lookup", func(t *testing.T, a *authRaceApp) (func() bool, func()) {
			return func() bool { return true }, a.holdPool(t)
		}},
		{"after the lookup, at the revoke", func(_ *testing.T, a *authRaceApp) (func() bool, func()) {
			taken, give := a.starveAfter("GetSessionByTokenHash")
			return taken, give
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: 1, dbTimeout: bound})
			happened, undo := tt.starve(t, a)
			defer undo()

			resp, elapsed := a.postTimed(t, "/auth/logout", "{}", raceCurrentToken, nil, 5*time.Second)
			body := authRequireStatus(t, resp, http.StatusServiceUnavailable)

			if !happened() {
				t.Fatal("the pool was never taken, so this test proved nothing")
			}
			checkBounded(t, elapsed, bound)
			authRequireCookie(t, resp, cookieCleared)
			checkLogoutUnconfirmed(t, body)
			if n := len(a.store.named("RevokeSession")); n != 0 || a.store.snapshot().IsRevoked {
				t.Errorf("RevokeSession sent %d times, session revoked = %t, for a pool that could not answer", n, a.store.snapshot().IsRevoked)
			}

			undo()
			a.store.afterPool = nil
			if again := a.post(t, "/auth/logout", raceCurrentToken, nil); again.StatusCode != http.StatusOK || !a.store.snapshot().IsRevoked {
				t.Errorf("with the pool free again the sign-out answered %d (revoked = %t), want 200 and the session ended: the control proves nothing otherwise",
					again.StatusCode, a.store.snapshot().IsRevoked)
			}
		})
	}
}

// TestRefresh_ReleasingTheTransactionTwiceIsNotAFailure pins what the double release costs
// in the log: nothing. A refresh gives its connection back before the pool queries that need
// one, and the deferred release that remains as the backstop then finds the transaction
// already closed, as does a winner's after the commit. pgx answers that with ErrTxClosed,
// which is not a failure and must not bury the line that is. The controls are the rollbacks
// that DO fail: one is logged, and a refresh that released early and fails again in the
// backstop logs once, not twice.
func TestRefresh_ReleasingTheTransactionTwiceIsNotAFailure(t *testing.T) {
	const line = "refresh: transaction rollback failed"
	boom := errors.New("connection reset by peer")

	tests := []struct {
		name  string
		tweak func(*raceStore)
		want  int // how many times the line is logged
	}{
		{"a refresh that wins: the commit closed the transaction", nil, 0},
		{"a refresh that loses at the rotation: released early, closed again by the backstop", raceWinnerRotatesFirst, 0},
		{"a refresh that finds the account disabled", func(s *raceStore) { s.user.IsActive = false }, 0},
		{
			"a rollback that fails, in the backstop of a refresh that failed",
			func(s *raceStore) { s.rotateErr = boom; s.rollbackErr = boom },
			1,
		},
		{
			"a rollback that fails when released early, which the backstop must not repeat",
			func(s *raceStore) { raceWinnerRotatesFirst(s); s.rollbackErr = boom },
			1,
		},
		{
			// A statement that ran out of its bound makes pgx close the connection, so the
			// rollback after every bounded stall finds it closed: not a failure.
			"a connection pgx already closed: the rollback finds it closed, which is not a failure",
			func(s *raceStore) {
				s.rotateErr = errRaceTransient
				s.rollbackErr = fmt.Errorf("rollback: %w", pgconn.ErrConnClosed)
			},
			0,
		},
		{
			// The exemption is that one error and no other.
			"a rollback that fails because the network connection is closed is still a failure",
			func(s *raceStore) {
				s.rotateErr = errRaceTransient
				s.rollbackErr = fmt.Errorf("rollback: %w", net.ErrClosed)
			},
			1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureProductionLog(t)
			a := newAuthRaceApp(t, tt.tweak)

			a.post(t, "/auth/refresh", raceCurrentToken, nil)

			if got := strings.Count(logs.String(), line); got != tt.want {
				t.Errorf("%q was logged %d times, want %d: %q", line, got, tt.want, logs.String())
			}
		})
	}
}

// checkLogoutUnconfirmed holds a sign-out's 503 to what it must say. The cookie has been
// cleared by then, so a browser's second attempt carries no token and is answered 200 for
// nothing: the message must say the sign-out could not be confirmed, that the session may
// still be active, and name what to do; it must not tell anyone to try again.
func checkLogoutUnconfirmed(t *testing.T, body map[string]any) {
	t.Helper()
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "could not be confirmed") || !strings.Contains(msg, "may still be active") {
		t.Errorf("message = %q, want it to say the sign-out could not be confirmed and the session may still be active", msg)
	}
	if !strings.Contains(msg, "Sign Out All Devices") {
		t.Errorf("message = %q, want it to name what a browser user can do, \"Sign Out All Devices\"", msg)
	}
	if strings.Contains(strings.ToLower(msg), "try signing out again") {
		t.Errorf("message = %q: it still tells a browser to try signing out again, which carries no token now", msg)
	}
}

// TestLogout_ARevokeThatFailsIsNotASuccess covers the write half of Logout: the lookup
// found the session and revoking it failed. A failure is a 500; running out of the bound is
// the 503 of a lookup that could not be made, with the same message. With neither is the
// session revoked, the audit row written, or the cookie spared.
func TestLogout_ARevokeThatFailsIsNotASuccess(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		want  int
		check func(*testing.T, map[string]any)
	}{
		{
			name: "the revoke fails", err: errors.New("connection reset by peer"), want: http.StatusInternalServerError,
			check: func(t *testing.T, body map[string]any) {
				if msg, _ := body["message"].(string); msg != "Failed to revoke session" {
					t.Errorf("message = %q, want the failure named as one", msg)
				}
			},
		},
		{
			name: "the revoke runs out of time", err: fmt.Errorf("revoking: %w", context.DeadlineExceeded), want: http.StatusServiceUnavailable,
			check: checkLogoutUnconfirmed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceApp(t, func(s *raceStore) { s.revokeErr = tt.err })

			resp := a.post(t, "/auth/logout", raceCurrentToken, nil)
			tt.check(t, authRequireStatus(t, resp, tt.want))

			authRequireCookie(t, resp, cookieCleared)
			if a.store.snapshot().IsRevoked {
				t.Error("the session is revoked although the revoke failed")
			}
			if got := a.store.auditActions(); len(got) != 0 {
				t.Errorf("audit actions = %v for a sign-out that did not happen", got)
			}
		})
	}
}

// redisSetHook runs on every command of one kind sent to Redis (SET unless made with
// newRedisCmdHook), ahead of the command, and decides what it does: wait, fail or panic.
// started is closed when the first one begins.
type redisSetHook struct {
	cmd     string
	on      func(ctx context.Context) error
	started chan struct{}
	once    sync.Once
}

func newRedisSetHook(on func(ctx context.Context) error) *redisSetHook {
	return newRedisCmdHook("set", on)
}

func newRedisCmdHook(cmd string, on func(ctx context.Context) error) *redisSetHook {
	return &redisSetHook{cmd: cmd, on: on, started: make(chan struct{})}
}

func (h *redisSetHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *redisSetHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if strings.EqualFold(cmd.Name(), h.cmd) {
			h.once.Do(func() { close(h.started) })
			if err := h.on(ctx); err != nil {
				return err
			}
		}
		return next(ctx, cmd)
	}
}

func (h *redisSetHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// TestRefresh_ASlowRedisDoesNotDelayTheNewCookie pins that the winner's response does not
// wait for the Redis row: a winner whose Set-Cookie arrived seconds late would leave the
// loser's short retry carrying the OLD cookie into a second 409. Redis never answers a SET
// until released. The refresh must still come back 200 with its new cookie promptly; the
// write must have been attempted (the control that moving it did not drop it), still be
// pending when the response arrives, run under a deadline of its own, and land once Redis
// is released.
func TestRefresh_ASlowRedisDoesNotDelayTheNewCookie(t *testing.T) {
	a := newAuthRaceApp(t, nil)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	type deadlineSeen struct {
		at time.Time
		ok bool
	}
	seen := make(chan deadlineSeen, 1)
	hook := newRedisSetHook(func(ctx context.Context) error {
		at, ok := ctx.Deadline()
		select {
		case seen <- deadlineSeen{at, ok}:
		default:
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	a.rdb.AddHook(hook)
	defer unblock()

	resp, elapsed := a.postTimed(t, "/auth/refresh", "{}", raceCurrentToken, nil, 3*time.Second)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	authRequireCookie(t, resp, cookieNew)
	if elapsed > 1500*time.Millisecond {
		t.Errorf("the response took %v with Redis not answering; the new cookie is waiting on the cache row", elapsed)
	}

	select {
	case <-hook.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the Redis write was never attempted: either it was dropped, or the hook is not on its path")
	}
	if a.redis.Exists("nexara:session:" + a.store.snapshot().ID.String()) {
		t.Error("the row exists although Redis has not answered; the hook is not holding the write")
	}
	select {
	case d := <-seen:
		if !d.ok {
			t.Error("the Redis write runs in a context with no deadline: a Redis that never answers would pin a goroutine per refresh")
		} else if remaining := time.Until(d.at); remaining <= 0 || remaining > 10*time.Second {
			t.Errorf("the Redis write's deadline is %v from now, want a few seconds", remaining)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the Redis write never reported its context")
	}

	unblock()
	a.awaitSessionRedisRow(t)
}

// authLogWatch is the captured production log with a signal on every write, so a test waits for
// a line the code logs from a goroutine instead of polling for it. It must be made before the
// request that logs: a line written through the handler it replaces would never signal.
type authLogWatch struct {
	*lockedLog
	wrote chan struct{}
}

func authWatchLog(t *testing.T) *authLogWatch {
	t.Helper()
	w := &authLogWatch{lockedLog: captureProductionLog(t), wrote: make(chan struct{}, 1)}
	slog.SetDefault(slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return w
}

func (w *authLogWatch) Write(b []byte) (int, error) {
	n, err := w.lockedLog.Write(b)
	select {
	case w.wrote <- struct{}{}:
	default:
	}
	return n, err
}

// await waits until the log holds want, and fails the test if it never does.
func (w *authLogWatch) await(t *testing.T, want string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for !strings.Contains(w.String(), want) {
		select {
		case <-w.wrote:
		case <-timeout:
			t.Fatalf("no %q line in the log: %q", want, w.String())
		}
	}
}

// TestRefresh_ARedisThatFailsOrPanicsCostsTheRefreshNothing pins what is left of the Redis
// write once it is off the request path: a failure is a logged warning and changes nothing
// the client sees, and a panic in it, in a goroutine no request is waiting on where an
// unrecovered one would take the process down, is recovered, logged and equally invisible.
func TestRefresh_ARedisThatFailsOrPanicsCostsTheRefreshNothing(t *testing.T) {
	tests := []struct {
		name string
		on   func(ctx context.Context) error
		want string
	}{
		{"the write fails", func(context.Context) error { return errors.New("redis: connection refused") }, "refresh: redis cache update failed"},
		{"the write panics", func(context.Context) error { panic("redis client exploded") }, "refresh: redis cache update panicked"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := authWatchLog(t)
			a := newAuthRaceApp(t, nil)
			a.rdb.AddHook(newRedisSetHook(tt.on))

			resp, _ := a.postTimed(t, "/auth/refresh", "{}", raceCurrentToken, nil, 3*time.Second)

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			authRequireCookie(t, resp, cookieNew)
			if _, issued := decodeObject(t, resp)["access_token"]; !issued {
				t.Error("a refresh whose cache write failed did not issue its access token")
			}

			logs.await(t, tt.want)
			if out := logs.String(); !strings.Contains(out, a.store.snapshot().ID.String()) {
				t.Errorf("the log line does not name the session: %q", out)
			}
			if a.redis.Exists("nexara:session:" + a.store.snapshot().ID.String()) {
				t.Error("a row was written although the write failed: the hook is not on the path")
			}
		})
	}
}
