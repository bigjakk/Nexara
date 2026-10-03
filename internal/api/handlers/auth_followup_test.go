package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
)

// The handlers share one bound for the work that DECIDES their answer, and give
// each step that enforces or records a decision already made — the race check
// behind a refusal, the revoke of a refused account's session, the audit row and
// the event it publishes — a deadline of its own. The reason is what these tests
// make happen: a request whose deciding work took its whole budget. A follow-up on
// that budget would run on nothing, and the harm is not an error that is noticed.
// A race loser's check fails and reads as "not a race", which answers 401 and
// clears the cookie the winner may already have replaced — the wipe the 409 exists
// to prevent. A refused account's session stays live. A sign-out leaves no record.
//
// The Redis rows of the sessions a sign-out ended are deleted as a follow-up of the
// same kind, and last: the revoke is in PostgreSQL and decides, the audit row is
// what must not be lost, and the cleanup is a cache nothing reads that runs on a
// deadline of its own.
//
// spendBudgetAt (see auth_change_password_test.go) is the mechanism: the named
// statement waits until the request's bound has run out and is then let through.
// Each row also checks that the budget WAS spent — the answer took at least the
// bound — so that a row that passes cannot be one that never met the problem.
// That each follow-up is itself bounded is pinned where each is starved: the race
// check in TestRefresh_AStarvedRefusalLookupIsBoundedToo, the revoke in
// TestRefresh_TheRevokeInAGuardBranchIsBounded, the audit row and the event in
// the Logout and LogoutAll tests, the cleanup in the change-password tests.

// TestRefresh_ALoserToARaceIsToldSoWhateverIsLeftOfItsBudget holds the 409 to the
// follow-up: the race check runs on a deadline of its own, so a loser whose
// deciding work spent the whole bound is still told it lost a race, and keeps its
// cookie. Both places a refusal is made are covered: at the rotation, where the
// winner got to the row first, and at the opening lookup, where the token was
// already the previous one when the request arrived.
func TestRefresh_ALoserToARaceIsToldSoWhateverIsLeftOfItsBudget(t *testing.T) {
	const bound = 150 * time.Millisecond

	tests := []struct {
		name   string
		tweak  func(*raceStore)
		cookie string
		spend  string
	}{
		{name: "at the rotation", tweak: raceWinnerRotatesFirst, cookie: raceCurrentToken, spend: "RotateSessionToken"},
		{name: "at the opening lookup", tweak: raceRotatedAgo(2 * time.Second), cookie: racePreviousToken, spend: "GetSessionByTokenHash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceAppWith(t, tt.tweak, raceOptions{dbTimeout: bound})
			spendBudgetAt(a, tt.spend)

			resp, elapsed := a.postTimed(t, "/auth/refresh", "{}", tt.cookie, nil, 5*time.Second)
			body := decodeObject(t, resp)

			if elapsed < bound {
				t.Fatalf("answered after %v, inside the bound of %v: the budget was never spent, so this test proved nothing", elapsed, bound)
			}
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body %v): a race loser must not be told its token is invalid", resp.StatusCode, body)
			}
			if body["error"] != RefreshSupersededCode {
				t.Errorf("error code = %v, want %q", body["error"], RefreshSupersededCode)
			}
			if cookies := refreshCookies(resp); len(cookies) != 0 {
				t.Errorf("Set-Cookie = %+v, want the cookie left alone: the winner's newer one may be in the jar", cookies)
			}
		})
	}
}

// TestRefresh_ARefusedAccountsSessionIsRevokedWhateverIsLeftOfItsBudget holds the
// revoke of each account-refusal branch to the follow-up: the user gone, the
// account disabled, the role changed. The read of the user spends the budget, the
// guard then refuses, and the revoke that ends the session — and, for the role
// change, the audit row that records why — must still happen.
func TestRefresh_ARefusedAccountsSessionIsRevokedWhateverIsLeftOfItsBudget(t *testing.T) {
	const bound = 150 * time.Millisecond

	tests := []struct {
		name      string
		tweak     func(*raceStore)
		wantAudit []string
	}{
		{"the user no longer exists", func(s *raceStore) { s.userErr = pgx.ErrNoRows }, nil},
		{"the account is disabled", func(s *raceStore) { s.user.IsActive = false }, nil},
		{"the user's role changed", func(s *raceStore) { s.user.Role = "viewer" }, []string{"refresh_denied_role_changed"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceAppWith(t, tt.tweak, raceOptions{dbTimeout: bound})
			spendBudgetAt(a, "GetUserByID")

			resp, elapsed := a.postTimed(t, "/auth/refresh", "{}", raceCurrentToken, nil, 5*time.Second)

			if elapsed < bound {
				t.Fatalf("answered after %v, inside the bound of %v: the budget was never spent, so this test proved nothing", elapsed, bound)
			}
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			if cookies := refreshCookies(resp); len(cookies) != 1 || !cookieDeleted(cookies[0]) {
				t.Errorf("Set-Cookie = %+v, want the cookie deleted", cookies)
			}
			if !a.store.snapshot().IsRevoked {
				t.Error("the session is still live: the revoke ran on a budget the deciding work had spent")
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, tt.wantAudit) {
				t.Errorf("audit actions = %v, want %v", got, tt.wantAudit)
			}
		})
	}
}

// TestTheAuditRowIsWrittenWhateverIsLeftOfTheBudget holds the audit row of a
// sign-out and of a sign-out everywhere to the follow-up, and the deletion of the
// ended sessions' Redis rows with it: the revoke spends the request's budget — it
// completes as the bound runs out — and the row that records it is still written,
// and the Redis row still deleted, each under a deadline of its own.
func TestTheAuditRowIsWrittenWhateverIsLeftOfTheBudget(t *testing.T) {
	const bound = 150 * time.Millisecond

	tests := []struct {
		name      string
		path      string
		cookie    string
		spend     string
		wantAudit string
		asOwner   bool
	}{
		{name: "a sign-out", path: "/auth/logout", cookie: raceCurrentToken, spend: "RevokeSession", wantAudit: "logout"},
		{name: "a sign-out everywhere", path: "/auth/logout-all", spend: "RevokeAllUserSessions", wantAudit: "logout_all", asOwner: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceAppWith(t, nil, raceOptions{dbTimeout: bound})
			a.redis.Set(a.sessionKey(), "{}")
			spendBudgetAt(a, tt.spend)
			var headers map[string]string
			if tt.asOwner {
				headers = map[string]string{"X-Test-Acting-User": a.store.user.ID.String()}
			}

			resp, elapsed := a.postTimed(t, tt.path, "{}", tt.cookie, headers, 5*time.Second)

			if elapsed < bound {
				t.Fatalf("answered after %v, inside the bound of %v: the budget was never spent, so this test proved nothing", elapsed, bound)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200: the revoke completed", resp.StatusCode)
			}
			if !a.store.snapshot().IsRevoked {
				t.Error("the session is not revoked")
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{tt.wantAudit}) {
				t.Errorf("audit actions = %v, want [%s]: the audit row ran on a budget the revoke had spent", got, tt.wantAudit)
			}
			if a.redis.Exists(a.sessionKey()) {
				t.Error("the ended session's Redis row was not deleted: the cleanup ran on a budget the revoke had spent")
			}
		})
	}
}

// TestSignOut_ARedisThatNeverAnswersTheCleanupIsBounded starves the deletion of the
// Redis rows a sign-out leaves behind. The revoke has been answered by the database
// by then, so the sign-out is made and the answer is the 200 it earned; what is
// pinned is that the cleanup is held to a deadline of its own and not to the
// request's bound, which here is far longer — a Redis that does not answer must cost
// the follow-up bound and no more — and that it comes after the audit row: the row
// is already written when the deletion starts, so that nothing the cache does can
// lose it.
func TestSignOut_ARedisThatNeverAnswersTheCleanupIsBounded(t *testing.T) {
	const bound = 200 * time.Millisecond

	tests := []struct {
		name      string
		path      string
		cookie    string
		wantAudit string
		asOwner   bool
	}{
		{name: "a sign-out", path: "/auth/logout", cookie: raceCurrentToken, wantAudit: "logout"},
		{name: "a sign-out everywhere", path: "/auth/logout-all", wantAudit: "logout_all", asOwner: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceAppWith(t, nil, raceOptions{dbTimeout: bound * 25, followUpTimeout: bound})
			a.redis.Set(a.sessionKey(), "{}")
			started := make(chan cleanupStart, 1)
			a.rdb.AddHook(newRedisCmdHook("del", func(ctx context.Context) error {
				deadline, _ := ctx.Deadline()
				select {
				case started <- cleanupStart{at: time.Now(), deadline: deadline, audits: a.store.auditActions()}:
				default:
				}
				<-ctx.Done()
				return ctx.Err()
			}))
			var headers map[string]string
			if tt.asOwner {
				headers = map[string]string{"X-Test-Acting-User": a.store.user.ID.String()}
			}

			requestAt := time.Now()
			resp, _ := a.postTimed(t, tt.path, "{}", tt.cookie, headers, 5*time.Second)
			answeredAt := time.Now()

			var start cleanupStart
			select {
			case start = <-started:
			default:
				t.Fatal("the Redis cleanup never ran, so this test proved nothing")
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200: the revoke was made", resp.StatusCode)
			}
			checkAnsweredAtTheDeadline(t, bound, requestAt, start.at, start.deadline, answeredAt)
			if !a.store.snapshot().IsRevoked {
				t.Error("the session is not revoked")
			}
			want := []string{tt.wantAudit}
			if !reflect.DeepEqual(start.audits, want) {
				t.Errorf("audit actions when the cleanup started = %v, want %v: the record must come before the cache is touched", start.audits, want)
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, want) {
				t.Errorf("audit actions = %v, want %v", got, want)
			}
		})
	}
}

// cleanupStart is what a test's Redis hook saw when the cleanup's DEL began: when,
// the deadline its context carried, and which audit rows had been written by then.
type cleanupStart struct {
	at       time.Time
	deadline time.Time
	audits   []string
}

// TestWithFollowUp_RunsOnAFreshDeadlineAndPutsTheRequestContextBack drives the
// helper the audit calls go through, directly. A handler that has spent its whole
// budget runs a follow-up, and the context that follow-up's code reads (c.Context(),
// which is what AuditLogAs and the event it publishes take) must be: not the
// handler's, which has ended; a fresh one, with a deadline of its own that is no
// longer than the follow-up bound; and cancelled once the follow-up is done. The
// request's own context must then be exactly what it was before — the same object,
// not a copy and not the follow-up's.
func TestWithFollowUp_RunsOnAFreshDeadlineAndPutsTheRequestContextBack(t *testing.T) {
	const bound, followUp = 50 * time.Millisecond, 300 * time.Millisecond
	a := newAuthRaceAppWith(t, nil, raceOptions{dbTimeout: bound, followUpTimeout: followUp})
	a.app.Get("/test/followup", func(c fiber.Ctx) error {
		ctx, cancel := a.handler.dbContext(c)
		defer cancel()
		<-ctx.Done() // the deciding work has spent its budget
		spent := c.Context()

		var inner context.Context
		var errDuring error
		var remaining time.Duration
		var hasDeadline bool
		a.handler.withFollowUp(c, func() {
			inner = c.Context()
			errDuring = inner.Err()
			if at, ok := inner.Deadline(); ok {
				hasDeadline, remaining = true, time.Until(at)
			}
		})

		return c.JSON(fiber.Map{
			"fresh":           inner != spent,
			"err_during":      errDuring != nil,
			"has_deadline":    hasDeadline,
			"remaining_ms":    remaining.Milliseconds(),
			"restored":        c.Context() == spent,
			"cancelled_after": inner.Err() != nil,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/test/followup", nil)
	resp, err := a.app.Test(req, fiber.TestConfig{Timeout: 2 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	got := decodeObject(t, resp)

	if got["fresh"] != true {
		t.Error("the follow-up ran on the handler's own context")
	}
	if got["err_during"] != false {
		t.Error("the follow-up's context was already over when it started: it inherited the spent budget instead of getting its own")
	}
	if got["has_deadline"] != true {
		t.Error("the follow-up's context has no deadline: a follow-up that stalls would hold the response for ever")
	} else if ms, _ := got["remaining_ms"].(float64); ms <= 0 || ms > float64(followUp.Milliseconds()) {
		t.Errorf("the follow-up's deadline is %vms away, want within the follow-up bound of %v", ms, followUp)
	}
	if got["restored"] != true {
		t.Error("the request's context was not put back after the follow-up")
	}
	if got["cancelled_after"] != true {
		t.Error("the follow-up's context was not cancelled once it was done: its timer outlives the request")
	}
}

// TestDbContext_CancelEndsTheBoundAndPutsTheRequestContextBack pins the two things
// the cancel function dbContext returns must do, one at a time. It must cancel:
// a cancel that only put the old context back would leave the bound's timer running
// for the whole of its fifteen seconds in every request, a leak that nothing else
// would show. And it must put back the context the request had before — the same
// object — which is what keeps a cancelled one from reaching whatever runs after
// the handler. Before the cancel, the bounded context is the request's own, which
// is what lets AuditLogAs and the event it publishes run inside the bound.
func TestDbContext_CancelEndsTheBoundAndPutsTheRequestContextBack(t *testing.T) {
	a := newAuthRaceApp(t, nil)
	a.app.Get("/test/dbcontext", func(c fiber.Ctx) error {
		original := c.Context()
		ctx, cancel := a.handler.dbContext(c)
		_, hasDeadline := ctx.Deadline()
		installed := c.Context() == ctx
		aliveBefore := ctx.Err() == nil

		cancel()

		return c.JSON(fiber.Map{
			"has_deadline":      hasDeadline,
			"installed":         installed,
			"alive_before":      aliveBefore,
			"cancelled_after":   ctx.Err() != nil,
			"restored":          c.Context() == original,
			"restored_has_none": func() bool { _, ok := c.Context().Deadline(); return !ok }(),
		})
	})

	resp, err := a.app.Test(httptest.NewRequest(http.MethodGet, "/test/dbcontext", nil), fiber.TestConfig{Timeout: time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	got := decodeObject(t, resp)

	for key, want := range map[string]bool{
		"has_deadline":      true,
		"installed":         true,
		"alive_before":      true,
		"cancelled_after":   true,
		"restored":          true,
		"restored_has_none": true,
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %t", key, got[key], want)
		}
	}
}
