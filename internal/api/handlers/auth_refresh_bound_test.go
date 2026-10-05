package handlers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/events"
)

// The tests in this file hold the rest of the auth handlers' database work to the
// same bound as Refresh's and Logout's lookups (see authDBTimeout), and Refresh's
// last read to the place it must happen.
//
// Three things share a mechanism. dbContext installs the bounded context as the
// request's own (c.SetContext), because the helpers every handler shares — the
// audit row, the event it publishes, the permission read — take c.Context(), which
// in Fiber never ends unless a handler gives it an end. A stall in any of them is
// then bounded wherever it happens, and the tests below stall each one: the audit
// insert and the event publish in Logout, the audit insert in LogoutAll, the
// permission read in Refresh.

// starveAfter makes the pool unusable from the moment the named POOL statement has
// completed: the next statement that needs a connection waits for one until its
// context ends. taken reports whether the statement was reached, give puts the
// connection back (call it in a defer), and stalledAt is when the pool went dark.
func (a *authRaceApp) starveAfter(name string) (taken func() bool, give func(), stalledAt func() time.Time) {
	var mu sync.Mutex
	held := false
	var at time.Time
	a.store.afterPool = func(n string) {
		mu.Lock()
		defer mu.Unlock()
		if n == name && !held {
			if err := a.gate.acquire(context.Background()); err == nil {
				held = true
				at = time.Now()
			}
		}
	}
	taken = func() bool {
		mu.Lock()
		defer mu.Unlock()
		return held
	}
	give = func() {
		mu.Lock()
		defer mu.Unlock()
		if held {
			a.gate.release()
			held = false
		}
	}
	stalledAt = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return at
	}
	return taken, give, stalledAt
}

// checkBounded holds an answer that waited for a stalled pool to the handler's
// bound: not before it (it would not have waited at all) and not long after it.
func checkBounded(t *testing.T, waited, bound time.Duration) {
	t.Helper()
	if waited < bound {
		t.Errorf("answered after %v, before the handler's bound of %v: it did not wait for the pool at all", waited, bound)
	}
	if waited > 3*time.Second {
		t.Errorf("answered after %v, long after the handler's bound of %v", waited, bound)
	}
}

// TestRefresh_ThePermissionsAreReadBeforeAnythingIsChanged pins where Refresh's
// last database read happens, and what a failure of it is.
//
// The permissions go out in the response. They used to be read after the commit,
// where a stall withheld the response from a session that had already been rotated
// — its new cookie never reached the browser, the old one became only the previous
// token, and the session was stranded: a 409 for a few seconds, a 401 after. And a
// read that failed was answered with an empty list and a success. Now the read
// comes first, from the pool and before the transaction, and a failure of it is a
// refresh that could not be completed: 503, nothing rotated, nothing issued.
// After the commit nothing touches the database.
//
// The first row is the control for the rest: the same harness, healthy, answers
// 200 with the permissions in the body in the order the statements ran.
func TestRefresh_ThePermissionsAreReadBeforeAnythingIsChanged(t *testing.T) {
	const bound = 200 * time.Millisecond
	boom := errRaceTransient

	// nothingChanged is what a 503 for a read owes: the cookie left alone, no token
	// issued, nothing rotated, no transaction begun and no Redis row written.
	nothingChanged := func(t *testing.T, a *authRaceApp, resp *http.Response, body map[string]any) {
		t.Helper()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
		}
		if _, issued := body["access_token"]; issued {
			t.Errorf("a refresh that could not read the permissions issued an access token: %v", body)
		}
		if _, listed := body["permissions"]; listed {
			t.Errorf("the 503 carries a permissions field: %v", body)
		}
		if cookies := refreshCookies(resp); len(cookies) != 0 {
			t.Errorf("Set-Cookie = %+v, want the cookie left alone", cookies)
		}
		if n := len(a.store.named("RotateSessionToken")); n != 0 {
			t.Errorf("RotateSessionToken was sent %d times", n)
		}
		if n := a.pool.txCount(); n != 0 {
			t.Errorf("%d transactions were begun before the permissions were read", n)
		}
		if keys := a.redis.Keys(); len(keys) != 0 {
			t.Errorf("a refresh that issued nothing wrote Redis rows %v", keys)
		}
		if a.store.snapshot().TokenHash != auth.HashToken(raceCurrentToken) {
			t.Error("the session's token was changed")
		}
		if n := a.pool.beginAttempts(); n != 0 {
			t.Errorf("a transaction was asked for %d times: the refresh went on past a permission read that did not complete", n)
		}
	}

	t.Run("they come back as an array, read before the transaction, and nothing is read after the commit", func(t *testing.T) {
		a := newAuthRaceApp(t, nil)

		resp := a.post(t, "/auth/refresh", raceCurrentToken, nil)
		body := decodeObject(t, resp)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
		}
		got, _ := body["permissions"].([]any)
		if want := []any{"view:cluster", "manage:node"}; !reflect.DeepEqual(got, want) {
			t.Errorf("permissions = %v, want %v", body["permissions"], want)
		}

		stmts := a.store.statements()
		index := func(name string) int {
			for i, s := range stmts {
				if s.name == name {
					return i
				}
			}
			return -1
		}
		perms, user, rotate := index("GetUserPermissions"), index("GetUserByID"), index("RotateSessionToken")
		if perms < 0 || user < 0 || rotate < 0 {
			t.Fatalf("statements = %v: want the permission read, the user read and the rotation among them", stmts)
		}
		if stmts[perms].inTx {
			t.Error("the permission read ran on the transaction; it belongs on the pool, before the transaction")
		}
		if perms >= user || user >= rotate {
			t.Errorf("order = permissions %d, user %d, rotation %d; the permissions must be read before the transaction's first statement", perms, user, rotate)
		}
		if last := stmts[len(stmts)-1]; last.name != "RotateSessionToken" {
			t.Errorf("the last statement is %s: after the commit nothing may touch the database, "+
				"because a stall there withholds the new cookie from a session that has already been rotated", last.name)
		}
	})

	t.Run("a user with no permissions gets an empty array, not null", func(t *testing.T) {
		a := newAuthRaceApp(t, func(s *raceStore) { s.permissions = nil })

		resp := a.post(t, "/auth/refresh", raceCurrentToken, nil)
		body := decodeObject(t, resp)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
		}
		got, isArray := body["permissions"].([]any)
		if !isArray || len(got) != 0 {
			t.Errorf("permissions = %#v, want an empty JSON array: the UI contract is an array, never null or absent", body["permissions"])
		}
	})

	t.Run("a read that fails is a 503, not a success with an empty list", func(t *testing.T) {
		logs := captureProductionLog(t)
		a := newAuthRaceApp(t, func(s *raceStore) { s.permsErr = boom })

		resp := a.post(t, "/auth/refresh", raceCurrentToken, nil)
		body := decodeObject(t, resp)

		nothingChanged(t, a, resp, body)
		if !strings.Contains(logs.String(), "refresh: permission lookup failed") {
			t.Errorf("the failed read left no trace in the log: %q", logs.String())
		}
	})

	t.Run("a read that stalls is a 503 within the bound, and the same request succeeds when the pool is free", func(t *testing.T) {
		a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: 1, dbTimeout: bound})
		taken, give, _ := a.starveAfter("GetSessionByTokenHash")
		defer give()

		resp, elapsed := a.postTimed(t, "/auth/refresh", "{}", raceCurrentToken, nil, 5*time.Second)
		body := decodeObject(t, resp)

		if !taken() {
			t.Fatal("the pool was never taken: the opening lookup did not run, so this test proved nothing")
		}
		nothingChanged(t, a, resp, body)
		checkBounded(t, elapsed, bound)
		if n := len(a.store.named("GetUserByID")); n != 0 {
			t.Errorf("the user was read %d times: the refresh went on past a permission read that never finished", n)
		}

		// Control: with the pool back, the same request is answered.
		give()
		a.store.afterPool = nil
		if again := a.post(t, "/auth/refresh", raceCurrentToken, nil); again.StatusCode != http.StatusOK {
			t.Errorf("with the pool free again the refresh answered %d, want 200", again.StatusCode)
		}
	})
}

// TestHandlersPutTheRequestContextBackWhenTheyReturn pins the other half of
// installing a bounded context as the request's own: it is not left there. Anything
// that runs once the handler has returned — middleware on the way out — reads
// c.Context() too, and must find the context it had before, not one that has been
// cancelled and not one that still carries the handler's bound.
//
// Each of the four handlers that call dbContext is driven to a successful answer
// and to a failed one, through a middleware that looks at the context after the
// handler returns.
func TestHandlersPutTheRequestContextBackWhenTheyReturn(t *testing.T) {
	boom := errRaceTransient
	ownerHeader := func(a *authRaceApp) map[string]string {
		return map[string]string{"X-Test-Acting-User": a.store.user.ID.String()}
	}

	tests := []struct {
		name   string
		path   string
		tweak  func(*raceStore)
		cookie string
		body   string
		hdr    func(*authRaceApp) map[string]string
		// slow marks a row that hashes a new password: it runs in parallel.
		slow bool
	}{
		{name: "a refresh that succeeds", path: "/auth/refresh", cookie: raceCurrentToken},
		{name: "a refresh that is refused", path: "/auth/refresh", cookie: "a-token-no-session-holds"},
		{name: "a refresh that fails", path: "/auth/refresh", cookie: raceCurrentToken, tweak: func(s *raceStore) { s.permsErr = boom }},
		{name: "a sign-out", path: "/auth/logout", cookie: raceCurrentToken},
		{name: "a sign-out that fails", path: "/auth/logout", cookie: raceCurrentToken, tweak: func(s *raceStore) { s.currentErr = boom }},
		{name: "a sign-out everywhere", path: "/auth/logout-all", hdr: ownerHeader},
		{name: "a sign-out everywhere that fails", path: "/auth/logout-all", hdr: ownerHeader, tweak: func(s *raceStore) { s.revokeAllErr = boom }},
		{
			// The old password does not match, so this ends before any hashing: what is
			// under test is the context, not the password.
			name: "a password change that is refused", path: "/auth/change-password", hdr: ownerHeader,
			body:  `{"old_password":"` + racePassword + `-wrong","new_password":"` + raceNewPassword + `"}`,
			tweak: withPasswordHash(t, nil),
		},
		{
			// Both phases of the handler install a bounded context and put the old one
			// back: the second phase starts from whatever the first left, so a first
			// phase that left its cancelled context behind would make this fail outright,
			// and the context seen after the handler returns is the second phase's.
			name: "a password change that succeeds, through both phases", path: "/auth/change-password", hdr: ownerHeader,
			body: changeBody, tweak: withPasswordHash(t, nil), slow: true,
		},
		{
			name: "a password change that fails inside its transaction", path: "/auth/change-password", hdr: ownerHeader,
			body: changeBody, tweak: withPasswordHash(t, func(s *raceStore) { s.revokeAllErr = boom }), slow: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.slow {
				t.Parallel()
			}
			a := newAuthRaceApp(t, tt.tweak)
			var headers map[string]string
			if tt.hdr != nil {
				headers = tt.hdr(a)
			}
			body := tt.body
			if body == "" {
				body = "{}"
			}

			// A row that hashes a new password needs longer than the harness's default
			// second.
			timeout := time.Second
			if tt.slow {
				timeout = 120 * time.Second
			}
			resp, _ := a.postTimed(t, tt.path, body, tt.cookie, headers, timeout)
			if tt.path == "/auth/refresh" && resp.StatusCode == http.StatusOK {
				a.awaitSessionRedisRow(t)
			}
			if tt.name == "a password change that succeeds, through both phases" && resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200: a second phase that inherited a cancelled context would fail here", resp.StatusCode)
			}

			a.afterMu.Lock()
			got, seen := a.afterCtx[tt.path]
			a.afterMu.Unlock()
			if !seen {
				t.Fatal("the middleware after the handler never ran")
			}
			if got.err != nil {
				t.Errorf("the request's context is %v once the handler has returned: a cancelled context was left behind for whatever runs next", got.err)
			}
			if got.hasDeadline {
				t.Error("the request's context still carries the handler's bound once the handler has returned")
			}
		})
	}
}

// TestLogout_AStalledAuditWriteIsBoundedToo is the audit row's half of the bound.
// AuditLogAs is shared by every handler and takes c.Context() — in Fiber a context
// that never ends — so before dbContext installed its own, a stall in the insert
// held a sign-out that had already been confirmed for as long as the database took.
//
// The pool goes dark the moment the revoke has completed, so the next statement,
// the audit insert, waits for a connection until the bound ends. The answer is the
// 200 the sign-out earned, within the bound, with the session revoked, the cookie
// cleared, and the lost audit row in the log as AuditLogAs says it: the action was
// performed and not recorded. The control is the same request with the pool free,
// which writes the row and logs no such line.
func TestLogout_AStalledAuditWriteIsBoundedToo(t *testing.T) {
	const bound = 200 * time.Millisecond

	t.Run("the audit insert stalls", func(t *testing.T) {
		logs := captureProductionLog(t)
		a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: 1, dbTimeout: bound, followUpTimeout: bound})
		taken, give, _ := a.starveAfter("RevokeSession")
		defer give()

		resp, elapsed := a.postTimed(t, "/auth/logout", "{}", raceCurrentToken, nil, 5*time.Second)

		if !taken() {
			t.Fatal("the pool was never taken: the revoke did not run, so this test proved nothing")
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: the sign-out was confirmed before the audit insert stalled", resp.StatusCode)
		}
		checkBounded(t, elapsed, bound)
		if cookies := refreshCookies(resp); len(cookies) != 1 || !cookieDeleted(cookies[0]) {
			t.Errorf("Set-Cookie = %+v, want the cookie deleted", cookies)
		}
		if !a.store.snapshot().IsRevoked {
			t.Error("the session is not revoked")
		}
		if got := a.store.auditActions(); len(got) != 0 {
			t.Errorf("audit actions = %v although the pool had no connection for the insert", got)
		}
		if out := logs.String(); !strings.Contains(out, "audit log insert failed") || !strings.Contains(out, `"action":"logout"`) {
			t.Errorf("the lost audit row left no trace in the log: %q", out)
		}
	})

	t.Run("control: with the pool free the audit row is written", func(t *testing.T) {
		logs := captureProductionLog(t)
		a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: 1, dbTimeout: bound, followUpTimeout: bound})

		resp := a.post(t, "/auth/logout", raceCurrentToken, nil)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{"logout"}) {
			t.Errorf("audit actions = %v, want [logout]", got)
		}
		if strings.Contains(logs.String(), "audit log insert failed") {
			t.Errorf("a healthy sign-out logged a failed audit insert: %q", logs.String())
		}
	})
}

// TestLogout_AStalledEventPublishIsBoundedToo is the same bound on the event
// AuditLogAs publishes: a Redis PUBLISH that never answers. The publish takes
// c.Context() like the insert before it, so it is inside the bound only because
// dbContext put its bound there.
//
// Redis here holds every PUBLISH until the context it was given ends. The sign-out
// must still answer 200 within the bound, with its audit row written (the pool is
// healthy) and the publish attempted — the control that the hook is on the path.
func TestLogout_AStalledEventPublishIsBoundedToo(t *testing.T) {
	const bound = 200 * time.Millisecond
	logs := captureProductionLog(t)
	a := newAuthRaceAppWith(t, nil, raceOptions{dbTimeout: bound, followUpTimeout: bound})
	a.handler.eventPub = events.NewPublisher(a.rdb, slog.Default())
	hook := newRedisCmdHook("publish", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	a.rdb.AddHook(hook)

	resp, elapsed := a.postTimed(t, "/auth/logout", "{}", raceCurrentToken, nil, 5*time.Second)

	select {
	case <-hook.started:
	default:
		t.Fatal("the event was never published: the hook is not on the path, so this test proved nothing")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	checkBounded(t, elapsed, bound)
	if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{"logout"}) {
		t.Errorf("audit actions = %v, want [logout]: the insert must not wait on the publish", got)
	}
	if out := logs.String(); !strings.Contains(out, "failed to publish event") {
		t.Errorf("the publish that ran out of time left no trace in the log: %q", out)
	}
}

// TestRefresh_ARefusedAccountsRevokeFailureIsLogged pins what the three
// account-refusal branches do when ending the session fails. The refresh is
// refused 401 with the cookie cleared whatever happens, but a revoke that fails
// leaves the session live — and the bound makes failure possible: a stalled
// database is now a timeout. Nothing else ends such a session before it expires
// except the next refresh being refused the same way, so the failure is logged
// with the session id and the reason, never a token or a hash. The control row
// ends the session and logs nothing.
func TestRefresh_ARefusedAccountsRevokeFailureIsLogged(t *testing.T) {
	boom := errRaceTransient
	const line = "refresh: could not revoke the session of a refused refresh"

	tests := []struct {
		name   string
		tweak  func(*raceStore)
		reason string
	}{
		{"the user no longer exists", func(s *raceStore) { s.userErr = pgx.ErrNoRows }, "user not found"},
		{"the account is disabled", func(s *raceStore) { s.user.IsActive = false }, "account disabled"},
		{"the user's role changed", func(s *raceStore) { s.user.Role = "viewer" }, "role changed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureProductionLog(t)
			a := newAuthRaceApp(t, func(s *raceStore) { tt.tweak(s); s.revokeErr = boom })

			resp := a.post(t, "/auth/refresh", raceCurrentToken, nil)

			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			if cookies := refreshCookies(resp); len(cookies) != 1 || !cookieDeleted(cookies[0]) {
				t.Errorf("Set-Cookie = %+v, want the cookie deleted", cookies)
			}
			out := logs.String()
			if !strings.Contains(out, line) {
				t.Fatalf("the failed revoke left no trace in the log: %q", out)
			}
			if !strings.Contains(out, a.store.session.ID.String()) {
				t.Errorf("the log line does not name the session: %q", out)
			}
			if !strings.Contains(out, `"reason":"`+tt.reason+`"`) {
				t.Errorf("the log line does not give the reason %q: %q", tt.reason, out)
			}
			if strings.Contains(out, raceCurrentToken) || strings.Contains(out, auth.HashToken(raceCurrentToken)) {
				t.Errorf("the log carries the token or its hash: %q", out)
			}
			if a.store.snapshot().IsRevoked {
				t.Error("the session is revoked although the revoke failed")
			}
		})

		t.Run(tt.name+", and the revoke works: nothing is logged", func(t *testing.T) {
			logs := captureProductionLog(t)
			a := newAuthRaceApp(t, tt.tweak)

			resp := a.post(t, "/auth/refresh", raceCurrentToken, nil)

			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			if !a.store.snapshot().IsRevoked {
				t.Error("the control did not end the session, so the rows above prove nothing")
			}
			if strings.Contains(logs.String(), line) {
				t.Errorf("a revoke that worked was logged as a failure: %q", logs.String())
			}
		})
	}
}

// postAs sends a POST as the account the harness's store holds.
func (a *authRaceApp) postAs(t *testing.T, path, body string, timeout time.Duration) (*http.Response, time.Duration) {
	t.Helper()
	return a.postTimed(t, path, body, "", map[string]string{"X-Test-Acting-User": a.store.user.ID.String()}, timeout)
}

// TestLogoutAll_IsBoundedAndSaysWhenItCouldNotConfirm holds the remedy Logout's own
// 503 points a user to — sign out everywhere — to the bound it points them away
// from. It lists the user's sessions, revokes them and deletes a Redis row per
// session, all of which used to wait on a context that never ends.
//
// A revoke that ran out of the bound is a 503 that says the sessions may still be
// active and that the caller is still signed in (the cookie is left alone, so the
// request can simply be repeated); any other failure is the 500 it always was.
// With the pool free it ends every session, clears the cookie and audits. And the
// audit insert after the revoke is inside the same bound.
func TestLogoutAll_IsBoundedAndSaysWhenItCouldNotConfirm(t *testing.T) {
	const bound = 200 * time.Millisecond
	bug := errRaceBug

	t.Run("control: it ends the session, clears the cookie and audits", func(t *testing.T) {
		a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: 1, dbTimeout: bound})

		resp, _ := a.postAs(t, "/auth/logout-all", "{}", 5*time.Second)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if cookies := refreshCookies(resp); len(cookies) != 1 || !cookieDeleted(cookies[0]) {
			t.Errorf("Set-Cookie = %+v, want the cookie deleted", cookies)
		}
		if !a.store.snapshot().IsRevoked {
			t.Error("the session is not revoked")
		}
		if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{"logout_all"}) {
			t.Errorf("audit actions = %v, want [logout_all]", got)
		}
	})

	t.Run("a pool that cannot answer is a 503 within the bound, and the request can be repeated", func(t *testing.T) {
		a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: 1, dbTimeout: bound})
		if err := a.gate.acquire(context.Background()); err != nil {
			t.Fatalf("take the pool's only connection: %v", err)
		}
		held := true
		defer func() {
			if held {
				a.gate.release()
			}
		}()

		resp, elapsed := a.postAs(t, "/auth/logout-all", "{}", 5*time.Second)
		body := decodeObject(t, resp)

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
		}
		checkBounded(t, elapsed, bound)
		msg, _ := body["message"].(string)
		if !strings.Contains(msg, "may still be active") || !strings.Contains(msg, "try again") {
			t.Errorf("message = %q, want it to say the sessions may still be active and that the request can be repeated", msg)
		}
		if cookies := refreshCookies(resp); len(cookies) != 0 {
			t.Errorf("Set-Cookie = %+v, want the cookie left alone: the caller is still signed in", cookies)
		}
		if a.store.snapshot().IsRevoked {
			t.Error("the session is revoked although the pool had no connection for it")
		}

		a.gate.release()
		held = false
		if again, _ := a.postAs(t, "/auth/logout-all", "{}", 5*time.Second); again.StatusCode != http.StatusOK {
			t.Errorf("with the pool free again the sign-out everywhere answered %d, want 200", again.StatusCode)
		}
	})

	t.Run("a revoke that ran out of time is the same 503", func(t *testing.T) {
		a := newAuthRaceApp(t, func(s *raceStore) { s.revokeAllErr = fmt.Errorf("revoking: %w", context.DeadlineExceeded) })

		resp, _ := a.postAs(t, "/auth/logout-all", "{}", 5*time.Second)

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", resp.StatusCode)
		}
		if cookies := refreshCookies(resp); len(cookies) != 0 {
			t.Errorf("Set-Cookie = %+v, want the cookie left alone", cookies)
		}
	})

	t.Run("any other failure is the 500 it always was", func(t *testing.T) {
		a := newAuthRaceApp(t, func(s *raceStore) { s.revokeAllErr = bug })

		resp, _ := a.postAs(t, "/auth/logout-all", "{}", 5*time.Second)
		body := decodeObject(t, resp)

		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (body %v)", resp.StatusCode, body)
		}
		if msg := body["message"]; msg != "Failed to revoke sessions" {
			t.Errorf("message = %v, want the failure named as one", msg)
		}
		if cookies := refreshCookies(resp); len(cookies) != 0 {
			t.Errorf("Set-Cookie = %+v, want the cookie left alone", cookies)
		}
	})

	t.Run("the audit insert after the revoke is inside the bound", func(t *testing.T) {
		logs := captureProductionLog(t)
		a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: 1, dbTimeout: bound, followUpTimeout: bound})
		taken, give, _ := a.starveAfter("RevokeAllUserSessions")
		defer give()

		resp, elapsed := a.postAs(t, "/auth/logout-all", "{}", 5*time.Second)

		if !taken() {
			t.Fatal("the pool was never taken: the revoke did not run, so this test proved nothing")
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: every session was revoked before the audit insert stalled", resp.StatusCode)
		}
		checkBounded(t, elapsed, bound)
		if !a.store.snapshot().IsRevoked {
			t.Error("the session is not revoked")
		}
		if !strings.Contains(logs.String(), "audit log insert failed") {
			t.Errorf("the lost audit row left no trace in the log: %q", logs.String())
		}
	})

	t.Run("without an account it is refused before the database is touched", func(t *testing.T) {
		a := newAuthRaceApp(t, nil)

		resp := a.post(t, "/auth/logout-all", "", nil)

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		if n := len(a.store.statements()); n != 0 {
			t.Errorf("%d statements were sent for a caller with no account", n)
		}
	})
}

// TestLoadPerms_AnEmptyListIsAlwaysAnArray pins the contract the response field
// keeps whichever way the permissions are loaded: a JSON array, never null and
// never absent. loadPermsStrict is what Refresh uses and says when a read fails;
// loadPerms, which Login and the SSO exchange use, answers a failed read with an
// empty list instead, so a sign-in does not fail over its permission display.
// Neither may hand back a nil slice, which encodes as null.
func TestLoadPerms_AnEmptyListIsAnArray(t *testing.T) {
	boom := errRaceTransient

	t.Run("an engine that is not wired is a user with none", func(t *testing.T) {
		got, err := (&AuthHandler{}).loadPermsStrict(context.Background(), uuid.New())
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("loadPermsStrict with no engine = (%#v, %v), want an empty non-nil list and no error", got, err)
		}
	})

	t.Run("a failed read is an error for Refresh and an empty array for the others", func(t *testing.T) {
		a := newAuthRaceApp(t, func(s *raceStore) { s.permsErr = boom })
		a.app.Get("/test/perms", func(c fiber.Ctx) error { return c.JSON(a.handler.loadPerms(c, a.store.user.ID)) })

		if _, err := a.handler.loadPermsStrict(context.Background(), a.store.user.ID); err == nil {
			t.Error("loadPermsStrict hid a failed read")
		}
		resp, err := a.app.Test(httptest.NewRequest(http.MethodGet, "/test/perms", nil), fiber.TestConfig{Timeout: time.Second, FailOnTimeout: true})
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		raw, _ := io.ReadAll(resp.Body)
		if strings.TrimSpace(string(raw)) != "[]" {
			t.Errorf("loadPerms after a failed read encoded as %s, want []", raw)
		}
	})

	t.Run("a healthy read returns the rows", func(t *testing.T) {
		a := newAuthRaceApp(t, nil)
		got, err := a.handler.loadPermsStrict(context.Background(), a.store.user.ID)
		if err != nil || !reflect.DeepEqual(got, []string{"view:cluster", "manage:node"}) {
			t.Errorf("loadPermsStrict = (%v, %v), want the two rows", got, err)
		}
	})
}

// TestTheDocumentedWorstCaseIsWhatTheBoundsAddUpTo pins the figures a client is
// told to size its timeout from. The 15 seconds is the bound on the work that
// DECIDES an answer; what records or enforces the decision afterwards has bounds
// of its own, so the answer can take longer, and a client that sized its timeout
// from "15 seconds" would give up on requests the server is still completing.
//
// Five figures follow from the constants, each the longest path of its endpoints:
//
//   - a follow-up on its own: authFollowUpTimeout;
//   - a sign-out: the deciding bound and two follow-ups, the audit entry and the
//     Redis cleanup;
//   - a sign-out everywhere: the same, and before them the question about the
//     refresh cookie, which has a bound of its own (a follow-up's, from a fresh
//     start) so that a stalled lookup cannot spend the revoke's;
//   - a refresh: the deciding bound, then, for a refused account whose role changed,
//     the rollback of its transaction (releaseTxTimeout, on a context of its own),
//     the revoke of its session and the audit entry, a follow-up each — the rollback
//     is a term of its own and is the one that was missing from "25";
//   - a password change: two deciding phases (the read of the account, then the
//     transaction) and three follow-ups, the read that settles a lost COMMIT, the
//     audit entry and the Redis cleanup.
//
// The figures are derived from the constants, so changing one fails here until the
// prose follows: every "up to N seconds" in the two files must be one of the
// values, and each value must be stated as often as the prose states it today, so
// that rewording a sentence out of the pattern is a failure and not a way past the
// check. Two figures can be the same number (a refresh and a sign-out everywhere are
// both thirty today), and then the file must state it once for each.
//
// Sign-out is Logout's, a legacy route that registry_auth.go does not declare, so
// only the API reference states it.
func TestTheDocumentedWorstCaseIsWhatTheBoundsAddUpTo(t *testing.T) {
	deciding := int(authDBTimeout / time.Second)
	followUp := int(authFollowUpTimeout / time.Second)
	rollback := int(releaseTxTimeout / time.Second)
	signOut := deciding + 2*followUp
	signOutAll := followUp + deciding + 2*followUp
	refresh := deciding + rollback + 2*followUp
	passwordChange := 2*deciding + 3*followUp
	mention := regexp.MustCompile(`up to (\d+) seconds`)

	// How many times each file states each figure, at least. Two figures can be the
	// same number, and the minimums for it then add up.
	figures := []struct {
		value, docs, registry int
	}{
		{followUp, 3, 3},
		{signOut, 1, 0},
		{signOutAll, 1, 1},
		{refresh, 1, 1},
		{passwordChange, 1, 1},
	}
	known := map[int]bool{}
	docsMin, registryMin := map[int]int{}, map[int]int{}
	for _, fig := range figures {
		known[fig.value] = true
		docsMin[fig.value] += fig.docs
		registryMin[fig.value] += fig.registry
	}

	for _, f := range []struct {
		file string
		// min is how many times the file states each figure.
		min map[int]int
	}{
		{"docs/api-reference.md", docsMin},
		{"internal/api/registry_auth.go", registryMin},
	} {
		raw, err := os.ReadFile(filepath.Join(repoRoot, f.file))
		if err != nil {
			t.Fatalf("read %s: %v", f.file, err)
		}
		// registry_auth.go wraps its strings, so a phrase can straddle a break.
		text := strings.Join(strings.Fields(strings.NewReplacer(`" +`, " ", `"`, " ").Replace(string(raw))), " ")

		seen := map[int]int{}
		for _, m := range mention.FindAllStringSubmatch(text, -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatalf("%s: %q is not a number", f.file, m[1])
			}
			seen[n]++
			if !known[n] {
				t.Errorf("%s says %q; the bounds add up to %d for a sign-out, %d for a sign-out everywhere, %d for a refresh, %d for a password change and %d for a follow-up",
					f.file, m[0], signOut, signOutAll, refresh, passwordChange, followUp)
			}
		}
		for figure, min := range f.min {
			if seen[figure] < min {
				t.Errorf("%s states \"up to %d seconds\" %d times, want at least %d: a rewording that no longer names the worst case would pass this test silently",
					f.file, figure, seen[figure], min)
			}
		}
	}
}

// TestTheDocumentedBoundIsTheBoundTheHandlersUse keeps the figure the API
// reference and the route descriptions give for the database bound equal to
// authDBTimeout. They said "a few seconds" while the bound was fifteen, which a
// client sizing its own timeout would have believed. The check is by value, so
// changing the constant fails here until the prose follows.
//
// EVERY mention of the bound must carry the figure, not just one of them: a file
// that says "15 seconds" in one paragraph and "30" in another is wrong in the
// second, and a test that asks only whether the right number appears somewhere
// passes it. A mention is a number before "seconds" after one of the phrases the
// prose states the bound with, and each file must have at least as many as it has
// today, so that rewording a sentence out of the pattern is a failure here and
// not a silent way past the check. Other durations in the same files — the 60
// seconds a request's head gets, the metrics interval — are not the bound and are
// not matched.
func TestTheDocumentedBoundIsTheBoundTheHandlersUse(t *testing.T) {
	figure := strconv.Itoa(int(authDBTimeout / time.Second))
	mention := regexp.MustCompile(`(?:answer within|the same|gets|gets the same) (\d+) seconds`)

	for _, f := range []struct {
		file string
		// min is how many times the file states the bound.
		min int
	}{
		{"docs/api-reference.md", 5},
		{"internal/api/registry_auth.go", 4},
	} {
		raw, err := os.ReadFile(filepath.Join(repoRoot, f.file))
		if err != nil {
			t.Fatalf("read %s: %v", f.file, err)
		}
		// registry_auth.go wraps its strings, so a phrase can straddle a break.
		text := strings.Join(strings.Fields(strings.NewReplacer(`" +`, " ", `"`, " ").Replace(string(raw))), " ")

		mentions := mention.FindAllStringSubmatch(text, -1)
		if len(mentions) < f.min {
			t.Errorf("%s states the bound %d times, want at least %d: a rewording that no longer names it would pass this test silently",
				f.file, len(mentions), f.min)
		}
		for _, m := range mentions {
			if m[1] != figure {
				t.Errorf("%s says the database bound is %q seconds in %q; it is %s", f.file, m[1], m[0], figure)
			}
		}
		if strings.Contains(text, "does not answer within a few seconds") {
			t.Errorf("%s still says the database bound is \"a few seconds\"; it is %s seconds", f.file, figure)
		}
	}
}
