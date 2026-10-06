package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"github.com/bigjakk/nexara/internal/auth"
)

// The change-password lockout: how many wrong current passwords an account may
// be tried with, what the refusal looks like, what resets it, and what it never
// touches. The lockout is a store (Redis in production, process memory for a
// handler that has none) behind ChangePassword, and these tests drive both, since
// the handler must behave the same over either.

// wrongChangeBody is a request whose current password is wrong and whose new one
// is fine, so that the only reason it can fail is the check of the current
// password.
const wrongChangeBody = `{"old_password":"` + racePassword + `-wrong","new_password":"` + raceNewPassword + `"}`

// weakChangeBody has the RIGHT current password and a new one that breaks the
// strength rules: the password is verified and the change is refused.
const weakChangeBody = `{"old_password":"` + racePassword + `","new_password":"weak"}`

// lockoutClock is the clock of a memory store under test.
type lockoutClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *lockoutClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *lockoutClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// lockoutUnderTest is one store, with the way to move its clock.
type lockoutUnderTest struct {
	name    string
	store   passwordLockoutStore
	advance func(time.Duration)
	// mr and rdb are the Redis behind a Redis store, nil for a memory one.
	mr  *miniredis.Miniredis
	rdb *redis.Client
}

// newLockouts returns a fresh store of each kind. Redis is miniredis, whose
// clock only moves when it is told to, which makes every duration exact.
func newLockouts(t *testing.T) []lockoutUnderTest {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	clock := &lockoutClock{now: time.Unix(1_700_000_000, 0)}
	return []lockoutUnderTest{
		{name: "redis", store: redisPasswordLockout{rdb: rdb}, advance: mr.FastForward, mr: mr, rdb: rdb},
		{name: "memory", store: newMemoryPasswordLockout(clock.Now), advance: clock.Advance},
	}
}

// lockoutApp is the change-password harness with its handler counting in the named
// store: "redis" is the harness's own miniredis (a.redis), "memory" a store with a
// clock the test moves. advance moves whichever one it is.
func lockoutApp(t *testing.T, kind string, opts raceOptions) (a *authRaceApp, advance func(time.Duration)) {
	t.Helper()
	return lockoutAppWith(t, kind, nil, opts)
}

// lockoutAppWith is lockoutApp over a store the test has edited first.
func lockoutAppWith(t *testing.T, kind string, tweak func(*raceStore), opts raceOptions) (a *authRaceApp, advance func(time.Duration)) {
	t.Helper()
	a = newAuthRaceAppWith(t, withPasswordHash(t, tweak), opts)
	switch kind {
	case "redis":
		a.handler.SetPasswordLockoutStore(a.rdb)
		return a, a.redis.FastForward
	case "memory":
		clock := &lockoutClock{now: time.Unix(1_700_000_000, 0)}
		a.handler.passwordLockout = newMemoryPasswordLockout(clock.Now)
		return a, clock.Advance
	default:
		t.Fatalf("unknown lockout store %q", kind)
		return nil, nil
	}
}

// change posts a change-password request as the harness's user.
func (a *authRaceApp) change(t *testing.T, body string) (int, map[string]any, http.Header) {
	t.Helper()
	resp, _ := a.postAs(t, "/auth/change-password", body, 120*time.Second)
	return resp.StatusCode, decodeObject(t, resp), resp.Header
}

// changeAs posts one as somebody else.
func (a *authRaceApp) changeAs(t *testing.T, user uuid.UUID, body string) (int, map[string]any) {
	t.Helper()
	resp, _ := a.postTimed(t, "/auth/change-password", body, "", map[string]string{"X-Test-Acting-User": user.String()}, 120*time.Second)
	return resp.StatusCode, decodeObject(t, resp)
}

func messageOf(body map[string]any) string {
	msg, _ := body["message"].(string)
	return msg
}

// lockoutArm counts attempts for id until the one that arms the lock, and returns it.
func lockoutArm(t *testing.T, s passwordLockoutStore, id uuid.UUID) lockoutReservation {
	t.Helper()
	for range passwordLockoutThreshold {
		res, err := s.reserve(context.Background(), id)
		if err != nil {
			t.Fatalf("reserve: %v", err)
		}
		if res.admission == lockoutAdmittedLast {
			return res
		}
	}
	t.Fatalf("%d attempts did not arm the lock", passwordLockoutThreshold)
	return lockoutReservation{}
}

// lockoutSpend sends n wrong current passwords, each counted and checked and answered 403.
func (a *authRaceApp) lockoutSpend(t *testing.T, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if status, body, _ := a.change(t, wrongChangeBody); status != http.StatusForbidden {
			t.Fatalf("wrong attempt %d = %d (%v), want 403", i, status, body)
		}
	}
}

// lockoutArm sends the wrong current passwords that lock the account, the last answered 429.
func (a *authRaceApp) lockoutArm(t *testing.T) {
	t.Helper()
	a.lockoutSpend(t, passwordLockoutThreshold-1)
	if status, body, _ := a.change(t, wrongChangeBody); status != http.StatusTooManyRequests {
		t.Fatalf("wrong attempt %d = %d (%v), want the 429 that locks", passwordLockoutThreshold, status, body)
	}
}

// TestPasswordLockoutStoresAgree runs one script of events against both stores and
// holds them to the same answers, exactly: the budget, the lock, what a refused
// attempt does and does not do, the window, the release of one attempt and the
// clear of all of them. The memory store is what runs when no Redis is wired, so it
// must be the same rule and not a near one; and because it is the SAME script, each
// row is also the contract of the rule itself.
func TestPasswordLockoutStoresAgree(t *testing.T) {
	ctx := context.Background()
	const budget = passwordLockoutThreshold

	type want struct {
		admission  lockoutAdmission
		attempts   int
		retryAfter time.Duration
	}
	admitted := func(n int) want { return want{admission: lockoutAdmitted, attempts: n} }
	last := want{admission: lockoutAdmittedLast, attempts: budget, retryAfter: passwordLockoutDuration}
	refused := func(left time.Duration) want { return want{admission: lockoutRefused, retryAfter: left} }

	// spend counts n attempts for the user and expects each to be admitted.
	spend := func(t *testing.T, s passwordLockoutStore, id uuid.UUID, n int) {
		t.Helper()
		for i := 1; i <= n; i++ {
			res, err := s.reserve(ctx, id)
			if err != nil || res.admission != lockoutAdmitted || res.attempts != i {
				t.Fatalf("attempt %d of %d = (%+v, %v), want admitted as attempt %d", i, n, res, err, i)
			}
		}
	}
	// take counts one attempt, holds it to the answer w and returns it, so a
	// scenario can give it back. Only the attempt that armed the lock carries the token
	// that names that lock.
	take := func(t *testing.T, s passwordLockoutStore, id uuid.UUID, w want) lockoutReservation {
		t.Helper()
		res, err := s.reserve(ctx, id)
		if err != nil {
			t.Fatalf("reserve: %v", err)
		}
		if res.admission != w.admission || res.attempts != w.attempts || res.retryAfter != w.retryAfter {
			t.Fatalf("reserve = {admission %d, attempts %d, retryAfter %v}, want {admission %d, attempts %d, retryAfter %v}",
				res.admission, res.attempts, res.retryAfter, w.admission, w.attempts, w.retryAfter)
		}
		if armed := res.admission == lockoutAdmittedLast; armed != (res.token != "") {
			t.Fatalf("reserve = %+v: the token belongs to the attempt that armed the lock and to no other", res)
		}
		return res
	}
	check := func(t *testing.T, s passwordLockoutStore, id uuid.UUID, w want) {
		t.Helper()
		take(t, s, id, w)
	}
	release := func(t *testing.T, s passwordLockoutStore, id uuid.UUID, res lockoutReservation) {
		t.Helper()
		if err := s.release(ctx, id, res); err != nil {
			t.Fatalf("release: %v", err)
		}
	}

	scenarios := []struct {
		name string
		run  func(t *testing.T, l lockoutUnderTest, id uuid.UUID)
	}{
		{"the last attempt of the budget arms the lock, and everything after it is refused", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			spend(t, l.store, id, budget-1)
			check(t, l.store, id, last)
			check(t, l.store, id, refused(passwordLockoutDuration))
			check(t, l.store, id, refused(passwordLockoutDuration))
		}},
		{"a lock runs down, and refused attempts do not lengthen it", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			spend(t, l.store, id, budget-1)
			check(t, l.store, id, last)
			l.advance(5 * time.Minute)
			check(t, l.store, id, refused(25*time.Minute))
			l.advance(24 * time.Minute)
			check(t, l.store, id, refused(time.Minute))
			l.advance(time.Minute)
			// The lock has ended, and the budget is a whole one again.
			check(t, l.store, id, admitted(1))
		}},
		{"the lock and the window are different lengths, and each is held to its own", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			// The counter that armed the lock is gone the moment it arms it, so the lock
			// is what refuses and ends on its own schedule; the attempts after it are a
			// new window, which ends on a schedule of its own.
			spend(t, l.store, id, budget-1)
			check(t, l.store, id, last)
			l.advance(passwordLockoutDuration - time.Second)
			check(t, l.store, id, refused(time.Second))
			l.advance(time.Second)
			check(t, l.store, id, admitted(1))
			l.advance(passwordLockoutWindow - time.Second)
			check(t, l.store, id, admitted(2))
			l.advance(time.Second)
			check(t, l.store, id, admitted(1))
		}},
		{"attempts are forgotten when their window ends", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			spend(t, l.store, id, budget-1)
			l.advance(passwordLockoutWindow)
			check(t, l.store, id, admitted(1))
		}},
		{"the window is counted from the first attempt, not the latest", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			check(t, l.store, id, admitted(1))
			l.advance(passwordLockoutWindow - time.Minute)
			check(t, l.store, id, admitted(2))
			l.advance(time.Minute)
			// The first attempt's window has ended; the second does not extend it.
			check(t, l.store, id, admitted(1))
		}},
		{"a clear forgets the attempts", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			spend(t, l.store, id, budget-1)
			if err := l.store.clear(ctx, id); err != nil {
				t.Fatalf("clear: %v", err)
			}
			spend(t, l.store, id, budget-1)
			check(t, l.store, id, last)
		}},
		{"a clear lifts a lock: the secret the guesses were aimed at is gone", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			spend(t, l.store, id, budget-1)
			check(t, l.store, id, last)
			if err := l.store.clear(ctx, id); err != nil {
				t.Fatalf("clear: %v", err)
			}
			check(t, l.store, id, admitted(1))
		}},
		{"a clear for an account with nothing to forget is not an error", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			if err := l.store.clear(ctx, id); err != nil {
				t.Fatalf("clear: %v", err)
			}
			check(t, l.store, id, admitted(1))
		}},
		{"a release gives back the one attempt it took, and no other", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			spend(t, l.store, id, budget-2)
			res := take(t, l.store, id, admitted(budget-1))
			release(t, l.store, id, res)
			// The attempts counted before it are still counted: the next attempt is the
			// one it was, not the first of a whole budget.
			check(t, l.store, id, admitted(budget-1))
			check(t, l.store, id, last)
		}},
		{"a release never takes the count below zero", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			res := take(t, l.store, id, admitted(1))
			for range 3 {
				release(t, l.store, id, res)
			}
			// One attempt was given back three times. A count that went to minus two would
			// be a budget of seven.
			spend(t, l.store, id, budget-1)
			check(t, l.store, id, last)
		}},
		{"a release for an account that was never counted creates nothing", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			release(t, l.store, id, lockoutReservation{admission: lockoutAdmitted, attempts: 1})
			spend(t, l.store, id, budget-1)
			check(t, l.store, id, last)
		}},
		{"a release of the attempt that armed the lock lifts that lock and leaves the count one short", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			spend(t, l.store, id, budget-1)
			arming := take(t, l.store, id, last)
			release(t, l.store, id, arming)
			// The guesses counted before the arming attempt are still counted: the next
			// attempt is the last of the budget again, and arms the lock again.
			check(t, l.store, id, last)
			check(t, l.store, id, refused(passwordLockoutDuration))
		}},
		{"a release does not lift a lock its attempt did not arm", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			spend(t, l.store, id, budget-1)
			first := take(t, l.store, id, last)
			l.advance(passwordLockoutDuration)
			spend(t, l.store, id, budget-1)
			second := take(t, l.store, id, last)
			// The first attempt's lock ended long ago, and a second one is in place:
			// giving back the first attempt must not lift the second's.
			release(t, l.store, id, first)
			check(t, l.store, id, refused(passwordLockoutDuration))
			release(t, l.store, id, second)
			check(t, l.store, id, last)
		}},
		{"a release of an attempt that was admitted, after the lock was armed by another, leaves the lock", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			spend(t, l.store, id, budget-3)
			early := take(t, l.store, id, admitted(budget-2))
			check(t, l.store, id, admitted(budget-1))
			check(t, l.store, id, last)
			release(t, l.store, id, early)
			check(t, l.store, id, refused(passwordLockoutDuration))
		}},
		{"a release of a refused attempt gives back nothing", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			spend(t, l.store, id, budget-1)
			check(t, l.store, id, last)
			refusedAttempt := take(t, l.store, id, refused(passwordLockoutDuration))
			release(t, l.store, id, refusedAttempt)
			check(t, l.store, id, refused(passwordLockoutDuration))
		}},
		{"a release of a refused attempt takes nothing from a count that exists by the time it is given back", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			// The scenario above leaves no count behind it to take from — a DECR of a
			// missing key makes minus one and is deleted again, so a release that did
			// not know the attempt was refused would pass it. Here the lock ends, a new
			// window counts two attempts, and only then does the refused attempt's release
			// arrive: the count must still be two.
			spend(t, l.store, id, budget-1)
			check(t, l.store, id, last)
			refusedAttempt := take(t, l.store, id, refused(passwordLockoutDuration))
			l.advance(passwordLockoutDuration)
			spend(t, l.store, id, 2)
			release(t, l.store, id, refusedAttempt)
			check(t, l.store, id, admitted(3))
		}},
		{"a release gives the count it restores a window of its own", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			spend(t, l.store, id, budget-1)
			arming := take(t, l.store, id, last)
			l.advance(time.Minute)
			release(t, l.store, id, arming)
			l.advance(passwordLockoutWindow - time.Second)
			// One second of the restored count's window is left, though the first of those
			// guesses was made over an hour ago: the guesses are still counted, and the
			// next attempt is the last one.
			check(t, l.store, id, last)
		}},
		{"an account is locked alone", func(t *testing.T, l lockoutUnderTest, id uuid.UUID) {
			other := uuid.New()
			spend(t, l.store, id, budget-1)
			check(t, l.store, id, last)
			check(t, l.store, other, admitted(1))
			if err := l.store.clear(ctx, other); err != nil {
				t.Fatalf("clear: %v", err)
			}
			check(t, l.store, id, refused(passwordLockoutDuration))
		}},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			for _, l := range newLockouts(t) {
				t.Run(l.name, func(t *testing.T) { sc.run(t, l, uuid.New()) })
			}
		})
	}

	// Attempts that arrive together are counted one at a time: however many there
	// are, exactly the budget is admitted — the last of them as the last — and the
	// rest are refused. This is the property that counting BEFORE the check exists
	// for, and it holds for a store only if reserve is atomic.
	t.Run("simultaneous attempts are admitted at most a budget's worth", func(t *testing.T) {
		for _, l := range newLockouts(t) {
			t.Run(l.name, func(t *testing.T) {
				const requests = 60
				id := uuid.New()
				results := make([]lockoutReservation, requests)
				errs := make([]error, requests)
				var wg sync.WaitGroup
				start := make(chan struct{})
				for i := range results {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						results[i], errs[i] = l.store.reserve(ctx, id)
					}()
				}
				close(start)
				wg.Wait()

				counts := map[lockoutAdmission]int{}
				attempts := map[int]int{}
				for i, res := range results {
					if errs[i] != nil {
						t.Fatalf("reserve: %v", errs[i])
					}
					counts[res.admission]++
					if res.admission != lockoutRefused {
						attempts[res.attempts]++
					}
				}
				if counts[lockoutAdmitted] != budget-1 || counts[lockoutAdmittedLast] != 1 || counts[lockoutRefused] != requests-budget {
					t.Errorf("admitted %d, last %d, refused %d of %d simultaneous attempts, want %d, 1 and %d",
						counts[lockoutAdmitted], counts[lockoutAdmittedLast], counts[lockoutRefused], requests, budget-1, requests-budget)
				}
				for n := 1; n <= budget; n++ {
					if attempts[n] != 1 {
						t.Errorf("attempt number %d was handed out %d times, want once: the counter must give each number to one request", n, attempts[n])
					}
				}
			})
		}
	})
}

// TestPasswordLockoutRedisKeys pins what the Redis store keeps: its own keys, with
// an expiry set together with the first count, and none of the TOTP lockout's.
func TestPasswordLockoutRedisKeys(t *testing.T) {
	ctx := context.Background()
	l := newLockouts(t)[0]
	id := uuid.New()

	if _, err := l.store.reserve(ctx, id); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if got := l.mr.Keys(); len(got) != 1 || got[0] != "pwchange:user:fail:"+id.String() {
		t.Fatalf("keys after one attempt = %v, want only the attempt counter", got)
	}
	if ttl := l.mr.TTL("pwchange:user:fail:" + id.String()); ttl != passwordLockoutWindow {
		t.Errorf("the counter expires in %v, want its window, %v: a counter without an expiry never goes away", ttl, passwordLockoutWindow)
	}

	// A counter that has somehow lost its expiry gets one back with the next attempt
	// — the TOTP lockout's INCR and separate EXPIRE leave such a counter behind when
	// the EXPIRE fails.
	if err := l.rdb.Persist(ctx, "pwchange:user:fail:"+id.String()).Err(); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if _, err := l.store.reserve(ctx, id); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if ttl := l.mr.TTL("pwchange:user:fail:" + id.String()); ttl != passwordLockoutWindow {
		t.Errorf("a counter with no expiry was given %v, want %v", ttl, passwordLockoutWindow)
	}

	// Locking replaces the counter with the lock, which expires too, and which holds the token
	// of the attempt that armed it.
	arming := lockoutArm(t, l.store, id)
	if arming.token == "" {
		t.Fatalf("the attempt that reached the threshold = %+v, want the last one, carrying a token", arming)
	}
	if got := l.mr.Keys(); len(got) != 1 || got[0] != "pwchange:user:lock:"+id.String() {
		t.Errorf("keys while locked = %v, want only the lock", got)
	}
	if ttl := l.mr.TTL("pwchange:user:lock:" + id.String()); ttl != passwordLockoutDuration {
		t.Errorf("the lock expires in %v, want %v", ttl, passwordLockoutDuration)
	}
	if got, err := l.mr.Get("pwchange:user:lock:" + id.String()); err != nil || got != arming.token {
		t.Errorf("the lock holds %q (%v), want the token of the attempt that armed it, %q", got, err, arming.token)
	}
	for _, key := range l.mr.Keys() {
		if strings.HasPrefix(key, "totp:") {
			t.Errorf("key %q belongs to the TOTP lockout", key)
		}
	}

	// Giving the arming attempt back lifts the lock and restores the counter, one
	// short of the threshold, with an expiry: a counter without one never goes away.
	if err := l.store.release(ctx, id, arming); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := l.mr.Keys(); len(got) != 1 || got[0] != "pwchange:user:fail:"+id.String() {
		t.Fatalf("keys after the arming attempt was given back = %v, want only the attempt counter", got)
	}
	if got, err := l.mr.Get("pwchange:user:fail:" + id.String()); err != nil || got != fmt.Sprint(passwordLockoutThreshold-1) {
		t.Errorf("the counter is %q (%v), want %d: one short of the threshold", got, err, passwordLockoutThreshold-1)
	}
	if ttl := l.mr.TTL("pwchange:user:fail:" + id.String()); ttl != passwordLockoutWindow {
		t.Errorf("the restored counter expires in %v, want its window, %v", ttl, passwordLockoutWindow)
	}

	// Giving back an attempt that took one off a counter that falls to zero leaves no
	// key behind, and on a missing counter creates none.
	other := uuid.New()
	res, err := l.store.reserve(ctx, other)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	for range 2 {
		if err := l.store.release(ctx, other, res); err != nil {
			t.Fatalf("release: %v", err)
		}
	}
	for _, key := range l.mr.Keys() {
		if strings.Contains(key, other.String()) {
			t.Errorf("key %q was left behind by a count that went back to zero", key)
		}
	}
}

// TestChangePassword_LocksTheAccountAfterRepeatedWrongPasswords is the lockout as
// a client sees it, over both stores. Four wrong current passwords are 403; the
// fifth is the 429 that locks the account — a Retry-After in seconds, a message
// that says how long, and one audit row; and from then on every request, the
// right password included and a wrong one too, is refused 429 and changes
// nothing, until the lock has run down. Refusals write no audit row of their own.
func TestChangePassword_LocksTheAccountAfterRepeatedWrongPasswords(t *testing.T) {
	for _, kind := range []string{"redis", "memory"} {
		t.Run(kind, func(t *testing.T) {
			logs := captureProductionLog(t)
			a, advance := lockoutApp(t, kind, raceOptions{})
			a.redis.Set(a.sessionKey(), "{}")
			lockSeconds := "1800"

			for i := 1; i < passwordLockoutThreshold; i++ {
				status, body, header := a.change(t, wrongChangeBody)
				if status != http.StatusForbidden || messageOf(body) != "Current password is incorrect" {
					t.Fatalf("wrong attempt %d = %d %q, want 403 Current password is incorrect", i, status, messageOf(body))
				}
				if got := header.Get("Retry-After"); got != "" {
					t.Errorf("wrong attempt %d carries Retry-After %q: nothing is locked yet", i, got)
				}
			}
			if got := a.store.auditActions(); len(got) != 0 {
				t.Fatalf("audit actions after four wrong attempts = %v, want none", got)
			}

			status, body, header := a.change(t, wrongChangeBody)
			if status != http.StatusTooManyRequests {
				t.Fatalf("the fifth wrong attempt = %d (%v), want 429", status, body)
			}
			if got := header.Get("Retry-After"); got != lockSeconds {
				t.Errorf("Retry-After = %q, want %q seconds: the whole lock", got, lockSeconds)
			}
			if msg := messageOf(body); !strings.Contains(msg, "locked for 30 more minutes") || !strings.Contains(msg, "stay signed in") {
				t.Errorf("message = %q, want it to say how long the lock is and that nothing else is affected", msg)
			}
			if got := a.store.auditActions(); len(got) != 1 || got[0] != "password_change_locked" {
				t.Fatalf("audit actions = %v, want exactly [password_change_locked]", got)
			}

			// Locked: the right password is refused, and so is a wrong one — with 429, not
			// 403 — the answer a wrong password gets — so the check of the password was
			// never reached.
			for _, tc := range []struct{ name, body string }{{"the right password", changeBody}, {"a wrong password", wrongChangeBody}} {
				status, body, header := a.change(t, tc.body)
				if status != http.StatusTooManyRequests {
					t.Errorf("%s while locked = %d (%v), want 429", tc.name, status, body)
				}
				if got := header.Get("Retry-After"); got != lockSeconds {
					t.Errorf("%s while locked: Retry-After = %q, want %q", tc.name, got, lockSeconds)
				}
			}
			if a.passwordChanged() || a.store.snapshot().IsRevoked || !a.redis.Exists(a.sessionKey()) {
				t.Error("the password or the sessions changed while the account was locked")
			}
			if n := len(a.store.named("UpdatePassword")); n != 0 {
				t.Errorf("UpdatePassword was sent %d times while the account was locked", n)
			}
			if got := a.store.auditActions(); len(got) != 1 {
				t.Errorf("audit actions = %v: a refused attempt on a locked account must write nothing", got)
			}

			// The lock runs down, and the answer says so.
			advance(5 * time.Minute)
			status, body, header = a.change(t, changeBody)
			if status != http.StatusTooManyRequests || header.Get("Retry-After") != "1500" || !strings.Contains(messageOf(body), "25 more minutes") {
				t.Errorf("after five minutes = %d, Retry-After %q, %q; want 429, 1500, 25 more minutes", status, header.Get("Retry-After"), messageOf(body))
			}
			advance(25 * time.Minute)
			// The lock has ended and the budget is a whole one again. The right password
			// with a new one that is refused as too weak shows both — it is checked, and
			// answered 400. (A change that succeeds is pinned by the rows of
			// TestChangePassword_WhatBecomesOfTheCountOnceThePasswordVerified.)
			status, body, _ = a.change(t, weakChangeBody)
			if status != http.StatusBadRequest {
				t.Fatalf("the right password after the lock ended = %d (%v), want 400: it must be checked again", status, body)
			}
			if status, _, _ := a.change(t, wrongChangeBody); status != http.StatusForbidden {
				t.Errorf("a wrong password after the lock ended = %d, want 403: the budget is a whole one again", status)
			}

			if out := logs.String(); strings.Contains(out, racePassword) || strings.Contains(out, raceNewPassword) {
				t.Errorf("a password reached the log: %q", out)
			}
			if out := logLinesFor(logs, a.store.user.ID.String()); !strings.Contains(out, "locked out of changing its password") {
				t.Errorf("no log line for this user records the lockout: %q", out)
			}
		})
	}
}

// TestChangePassword_TheLockoutAuditRowSaysNothingSecret holds the row to what it
// is for. Audit details are readable by every Viewer, so the row says that it
// happened, from where, how many attempts and how long it lasts — and not the
// passwords: the wrong one that was tried, or the new one.
func TestChangePassword_TheLockoutAuditRowSaysNothingSecret(t *testing.T) {
	a, _ := lockoutApp(t, "redis", raceOptions{})
	a.lockoutArm(t)

	rows := a.store.auditDetailsWritten()
	if len(rows) != 1 {
		t.Fatalf("audit rows = %v, want one", rows)
	}
	var details map[string]any
	if err := json.Unmarshal([]byte(rows[0]), &details); err != nil {
		t.Fatalf("the details are not JSON: %v: %s", err, rows[0])
	}
	if len(details) != 3 {
		t.Errorf("details = %v, want exactly ip, failed_attempts and lockout_seconds", details)
	}
	if ip, _ := details["ip"].(string); ip == "" {
		t.Errorf("details = %v, want the client's address", details)
	}
	if details["failed_attempts"] != float64(passwordLockoutThreshold) || details["lockout_seconds"] != float64(1800) {
		t.Errorf("details = %v, want failed_attempts %d and lockout_seconds 1800", details, passwordLockoutThreshold)
	}
	for _, secret := range []string{racePassword, raceNewPassword, "-wrong"} {
		if strings.Contains(rows[0], secret) {
			t.Errorf("the audit details %s contain %q", rows[0], secret)
		}
	}
}

// guessesBeforeTheLock sends wrong current passwords until the account answers 429,
// and returns how many were answered 403 first: the budget the account has left.
// It spends that budget.
func (a *authRaceApp) guessesBeforeTheLock(t *testing.T) int {
	t.Helper()
	for n := 0; n <= passwordLockoutThreshold; n++ {
		status, body, _ := a.change(t, wrongChangeBody)
		switch status {
		case http.StatusTooManyRequests:
			return n
		case http.StatusForbidden:
		default:
			t.Fatalf("a wrong current password = %d (%v), want 403 or 429", status, body)
		}
	}
	t.Fatalf("the account was still not locked after %d wrong passwords", passwordLockoutThreshold+1)
	return -1
}

// TestChangePassword_WhatBecomesOfTheCountOnceThePasswordVerified holds the two ends of a
// request with the RIGHT current password to what each is for. Every row counts some wrong
// guesses, sends one request with the right password, and asks how many wrong guesses the
// account has left. A change that COMMITS makes the secret new, so the count is cleared. A
// request that verified the password and committed nothing changed nothing, so the guesses
// already counted are aimed at the SAME secret: it gives back only its own attempt, since
// clearing the lot would hand whoever holds counted guesses a fresh budget each time the owner
// mistypes a new password. The row that arms the lock leaves the four guesses before it counted.
func TestChangePassword_WhatBecomesOfTheCountOnceThePasswordVerified(t *testing.T) {
	// A whole budget is this many wrong guesses answered 403 before the one that locks.
	const fresh = passwordLockoutThreshold - 1

	for _, kind := range []string{"redis", "memory"} {
		for _, tc := range []struct {
			name   string
			before int // wrong guesses counted before the right one
			body   string
			tweak  func(*raceStore)
			status int
			// committed is whether the handler knows the change went through: the count is
			// cleared. When it does not, only the request's own attempt is given back.
			committed bool
			// landedUnseen is a change that did go through while the handler could not
			// find that out, so the store holds the new password and the count is not
			// cleared: the safe side of not knowing.
			landedUnseen bool
		}{
			{name: "the change commits", before: 3, body: changeBody, status: http.StatusOK, committed: true},
			{name: "the change commits as the attempt that armed the lock", before: fresh, body: changeBody, status: http.StatusOK, committed: true},
			{name: "the new password is refused as too weak", before: 2, body: weakChangeBody, status: http.StatusBadRequest},
			{name: "the new password is refused, as the attempt that armed the lock", before: fresh, body: weakChangeBody, status: http.StatusBadRequest},
			{name: "the update fails", before: 2, body: changeBody, status: http.StatusServiceUnavailable,
				tweak: func(s *raceStore) { s.updatePwErr = errRaceTransient }},
			{name: "the commit answer is lost and the change did not land", before: 2, body: changeBody, status: http.StatusServiceUnavailable,
				tweak: func(s *raceStore) { s.commitErr = errRaceTransient }},
			{name: "the commit answer is lost and the change landed", before: 2, body: changeBody, status: http.StatusOK, committed: true,
				tweak: func(s *raceStore) { s.commitErr = errRaceTransient; s.commitLands = true }},
			{name: "the commit answer is lost and nobody can tell: it did not land", before: 2, body: changeBody, status: http.StatusServiceUnavailable,
				tweak: func(s *raceStore) { s.commitErr = errRaceTransient; s.afterCommitUserErr = errRaceTransient }},
			{name: "the commit answer is lost and nobody can tell: it landed", before: 2, body: changeBody, status: http.StatusServiceUnavailable, landedUnseen: true,
				tweak: func(s *raceStore) {
					s.commitErr, s.commitLands, s.afterCommitUserErr = errRaceTransient, true, errRaceTransient
				}},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				a, _ := lockoutAppWith(t, kind, tc.tweak, raceOptions{})

				a.lockoutSpend(t, tc.before)
				status, body, _ := a.change(t, tc.body)
				if status != tc.status {
					t.Fatalf("the right current password = %d (%v), want %d", status, body, tc.status)
				}
				// The store is where the row left it: the new password if the change went
				// through, the old one if not.
				if got := a.passwordChanged(); got != (tc.committed || tc.landedUnseen) {
					t.Fatalf("the password changed = %v, want %v", got, tc.committed || tc.landedUnseen)
				}
				// Every row's wrong guesses below compare against the old password's hash.
				a.store.mu.Lock()
				a.store.user.PasswordHash = racePasswordHash(t)
				a.store.mu.Unlock()

				want := fresh - tc.before
				if tc.committed {
					want = fresh
				}
				if got := a.guessesBeforeTheLock(t); got != want {
					if tc.committed {
						t.Errorf("%d wrong guesses were answered 403 before the lock, want %d: a change that committed makes the secret new, so the whole count must be cleared",
							got, want)
					} else {
						t.Errorf("%d wrong guesses were answered 403 before the lock, want %d: a request that verified the password and changed nothing must give back its own attempt "+
							"and not forgive the %d guesses counted before it", got, want, tc.before)
					}
				}
			})
		}
	}
}

// faultyLockout is a store that cannot give an attempt back or clear the count, to
// show what the handler does with a Redis that fails after the password verified.
type faultyLockout struct {
	passwordLockoutStore
	releaseErr, clearErr error
}

func (f faultyLockout) release(ctx context.Context, id uuid.UUID, res lockoutReservation) error {
	if f.releaseErr != nil {
		return f.releaseErr
	}
	return f.passwordLockoutStore.release(ctx, id, res)
}

func (f faultyLockout) clear(ctx context.Context, id uuid.UUID) error {
	if f.clearErr != nil {
		return f.clearErr
	}
	return f.passwordLockoutStore.clear(ctx, id)
}

// TestChangePassword_TheLockoutIsPerAccountAndOnlyForThisRoute: one account's lock
// is its own, and it is a lock on this route alone. The other account is
// counted from one, and nothing the lockout writes is anything the TOTP lockout
// — which login's second factor reads — looks at.
func TestChangePassword_TheLockoutIsPerAccountAndOnlyForThisRoute(t *testing.T) {
	a, _ := lockoutApp(t, "redis", raceOptions{})
	locked := a.store.user.ID
	a.lockoutArm(t)
	if status, _, _ := a.change(t, changeBody); status != http.StatusTooManyRequests {
		t.Fatalf("the locked account's right password = %d, want 429", status)
	}

	// The harness answers every GetUserByID with its one user row, so this account
	// has the same password; what differs is the id the count is kept under.
	other := uuid.New()
	for i := 1; i < passwordLockoutThreshold; i++ {
		if status, body := a.changeAs(t, other, wrongChangeBody); status != http.StatusForbidden {
			t.Fatalf("another account's wrong attempt %d = %d (%v), want 403: one account's lock must not count against another", i, status, body)
		}
	}

	if (&TOTPHandler{rdb: a.rdb}).isUserTOTPLocked(context.Background(), locked) {
		t.Error("the TOTP lockout reports the account locked: a locked password change must not lock the second factor of a login")
	}
	for _, key := range a.redis.Keys() {
		if strings.HasPrefix(key, "totp:") || strings.HasPrefix(key, "nexara:rbac:") {
			t.Errorf("key %q was written by a change-password attempt", key)
		}
	}
	for _, key := range []string{totpUserFailKey(locked), totpUserLockKey(locked), totpUserFailKey(other), totpUserLockKey(other)} {
		if a.redis.Exists(key) {
			t.Errorf("the TOTP lockout key %q exists", key)
		}
	}
}

// TestChangePassword_AnAttemptThatCannotBeCountedIsNotChecked is the decision for
// a Redis that cannot be reached, in each way it can fail: the password is not
// checked, 503, and nothing changes. The right password is refused like a wrong
// one — a request that fails open here is an oracle for anybody holding a stolen
// token whenever Redis is down, or can be made to be — and the control is the same
// request with Redis healthy.
//
// A wrong password is 503 as well, and not 403: nothing was checked, so nothing is
// said about it.
func TestChangePassword_AnAttemptThatCannotBeCountedIsNotChecked(t *testing.T) {
	// failFast is a Redis client that gives up at the first error: go-redis retries
	// a refused connection and a LOADING for as long as the request's bound lasts,
	// and a row about the ERROR a Redis answers with must not be one that ends at
	// the bound instead. A Redis that never answers is not here: it is the one a
	// stand-in cannot be trusted to model, and
	// TestChangePassword_ACountThatNeverAnswersIsA503AtTheBound has it as a real
	// socket.
	failFast := func(t *testing.T, a *authRaceApp) {
		client := redis.NewClient(&redis.Options{Addr: a.redis.Addr(), MaxRetries: -1})
		t.Cleanup(func() { _ = client.Close() })
		a.handler.SetPasswordLockoutStore(client)
	}

	for _, tc := range []struct {
		name string
		// fail makes Redis fail the way the row says.
		fail func(t *testing.T, a *authRaceApp)
	}{
		{"Redis answers every command with an error", func(t *testing.T, a *authRaceApp) {
			failFast(t, a)
			a.redis.SetError("LOADING Redis is loading the dataset in memory")
		}},
		{"Redis is gone: the connection is refused", func(t *testing.T, a *authRaceApp) {
			failFast(t, a)
			a.redis.Close()
		}},
	} {
		for _, body := range []struct{ name, body string }{{"right password", changeBody}, {"wrong password", wrongChangeBody}} {
			t.Run(tc.name+"/"+body.name, func(t *testing.T) {
				logs := captureProductionLog(t)
				const dbTimeout = 15 * time.Second
				a, _ := lockoutApp(t, "redis", raceOptions{dbTimeout: dbTimeout})
				a.redis.Set(a.sessionKey(), "{}")
				var checked atomic.Int32
				a.handler.passwordChecker = func(hash, password string) error {
					checked.Add(1)
					return auth.CheckPassword(hash, password)
				}
				tc.fail(t, a)

				resp, elapsed := a.postAs(t, "/auth/change-password", body.body, 30*time.Second)
				decoded := decodeObject(t, resp)

				if resp.StatusCode != http.StatusServiceUnavailable {
					t.Fatalf("status = %d (%v), want 503", resp.StatusCode, decoded)
				}
				if msg := messageOf(decoded); !strings.Contains(msg, "NOT changed") || !strings.Contains(msg, "attempt limit") {
					t.Errorf("message = %q, want it to say the attempt limit could not be checked and the password was NOT changed", msg)
				}
				if elapsed >= dbTimeout {
					t.Errorf("answered after %v, at the bound: the error Redis answered with was not what ended the request", elapsed)
				}
				if got := checked.Load(); got != 0 {
					t.Errorf("%d passwords were checked, want none: a password that was not counted must not be checked", got)
				}
				if a.passwordChanged() || a.store.snapshot().IsRevoked {
					t.Error("the password or the sessions changed although the attempt could not be counted")
				}
				if n := len(a.store.named("UpdatePassword")); n != 0 {
					t.Errorf("UpdatePassword was sent %d times", n)
				}
				if got := a.store.auditActions(); len(got) != 0 {
					t.Errorf("audit actions = %v, want none", got)
				}
				out := logLinesFor(logs, a.store.user.ID.String())
				if !strings.Contains(out, "could not be reached") {
					t.Errorf("no log line for this user says the counter could not be reached: %q", out)
				}
				if !strings.Contains(out, "may still have been counted") {
					t.Errorf("the line does not say the attempt may still have been counted, which a 503 cannot: %q", out)
				}
				if out := logs.String(); strings.Contains(out, racePassword) || strings.Contains(out, raceNewPassword) {
					t.Errorf("a password reached the log: %q", out)
				}
			})
		}
	}

	t.Run("control: with Redis healthy the same request is checked", func(t *testing.T) {
		// The right password with a new one that is refused as too weak: answered 400,
		// which only a password that was counted and checked gets.
		a, _ := lockoutApp(t, "redis", raceOptions{})
		if status, body, _ := a.change(t, weakChangeBody); status != http.StatusBadRequest {
			t.Fatalf("status = %d (%v), want 400", status, body)
		}
		if status, _, _ := a.change(t, wrongChangeBody); status != http.StatusForbidden {
			t.Errorf("a wrong password = %d, want 403", status)
		}
	})
}

// TestChangePassword_AHandlerWithoutRedisKeepsALocalLockout: an AuthHandler that
// was never given a Redis — every test that builds one by hand, and nothing in
// production, where it cannot exist without one — still locks. The alternative,
// no lockout at all for a handler somebody forgot to wire, is a guard that
// disappears without a sign.
func TestChangePassword_AHandlerWithoutRedisKeepsALocalLockout(t *testing.T) {
	a := newAuthRaceAppWith(t, withPasswordHash(t, nil), raceOptions{})
	if a.handler.passwordLockout != nil {
		t.Fatal("the harness wired a store: this test proved nothing")
	}
	a.handler.SetPasswordLockoutStore(nil)
	if a.handler.passwordLockout != nil {
		t.Fatal("a nil Redis was installed as the store")
	}

	a.lockoutSpend(t, passwordLockoutThreshold-1)
	status, _, header := a.change(t, wrongChangeBody)
	if status != http.StatusTooManyRequests || header.Get("Retry-After") != "1800" {
		t.Errorf("the fifth wrong attempt = %d, Retry-After %q, want 429 and 1800", status, header.Get("Retry-After"))
	}
	if status, _, _ := a.change(t, changeBody); status != http.StatusTooManyRequests {
		t.Errorf("the right password on a locked account = %d, want 429", status)
	}
	if a.passwordChanged() {
		t.Error("the password changed on a locked account")
	}
	if keys := a.redis.Keys(); len(keys) != 0 {
		t.Errorf("Redis holds %v: a handler without a store must not have written to the harness's", keys)
	}
}

// TestChangePassword_WhatTheLockoutDoesAfterTheDecisionIsBestEffort: once the
// password has verified, giving an attempt back and clearing the count are
// housekeeping, and a Redis that cannot do them must not change what the request
// answers. The bounds they run under are held, against a real socket, in
// password_lockout_bounds_test.go.
func TestChangePassword_WhatTheLockoutDoesAfterTheDecisionIsBestEffort(t *testing.T) {
	t.Run("a release that fails does not fail the request", func(t *testing.T) {
		logs := captureProductionLog(t)
		a, _ := lockoutApp(t, "redis", raceOptions{})
		a.handler.passwordLockout = faultyLockout{passwordLockoutStore: redisPasswordLockout{rdb: a.rdb}, releaseErr: errRaceBug}

		// The right password and a new one refused as too weak: the request goes on
		// past a release that failed, to the answer it would have had — a 400, and not a
		// 500 for the sake of a counter.
		status, body, _ := a.change(t, weakChangeBody)

		if status != http.StatusBadRequest {
			t.Fatalf("status = %d (%v), want 400: the password was verified, and a release that failed must not decide the answer", status, body)
		}
		out := logLinesFor(logs, a.store.user.ID.String())
		if !strings.Contains(out, "could not give back the attempt") {
			t.Errorf("no log line for this user says the release failed: %q", out)
		}
		if !a.redis.Exists("pwchange:user:fail:" + a.store.user.ID.String()) {
			t.Error("the attempt is gone from Redis although the release failed: it must stay counted until its window ends")
		}
	})

	t.Run("a clear that fails does not fail the change", func(t *testing.T) {
		logs := captureProductionLog(t)
		a, _ := lockoutApp(t, "redis", raceOptions{})
		a.handler.passwordLockout = faultyLockout{passwordLockoutStore: redisPasswordLockout{rdb: a.rdb}, clearErr: errRaceBug}

		status, body, _ := a.change(t, changeBody)

		if status != http.StatusOK || !a.passwordChanged() {
			t.Fatalf("status = %d (%v), changed = %v, want 200 and the new password in place: the change is made, and a count that could not be cleared is not the user's to hear about",
				status, body, a.passwordChanged())
		}
		if got := a.store.auditActions(); len(got) != 1 || got[0] != "password_changed" {
			t.Errorf("audit actions = %v, want [password_changed]: the record comes before the clear", got)
		}
		if out := logLinesFor(logs, a.store.user.ID.String()); !strings.Contains(out, "could not clear the failed-attempt count") {
			t.Errorf("no log line for this user says the clear failed: %q", out)
		}
	})
}

// TestChangePassword_EveryWrongPasswordLeavesAWarning: a guess that stays under the
// threshold leaves no lock, no audit row — and, were it not for this line, nothing
// at all, which is how a slow guess goes unseen. Every wrong current password that
// was CHECKED is logged at Warn with who, from where and which attempt of the
// budget it was; a request that was refused unchecked (the lock) and one whose
// password was right are not wrong passwords, and leave no such line; and no line
// carries a password.
func TestChangePassword_EveryWrongPasswordLeavesAWarning(t *testing.T) {
	const message = "change password: wrong current password"
	for _, kind := range []string{"redis", "memory"} {
		t.Run(kind, func(t *testing.T) {
			logs := captureProductionLog(t)
			a, _ := lockoutApp(t, kind, raceOptions{})
			userID := a.store.user.ID.String()

			// A right password first, with a new one that is refused: not a wrong password,
			// and it gives its own attempt back, so the wrong ones below count from one.
			if status, _, _ := a.change(t, weakChangeBody); status != http.StatusBadRequest {
				t.Fatalf("the right password = %d, want 400", status)
			}
			for i := 1; i <= passwordLockoutThreshold; i++ {
				a.change(t, wrongChangeBody)
			}
			// Locked: refused without being checked, a wrong password and the right one.
			a.change(t, wrongChangeBody)
			a.change(t, changeBody)

			var attempts []int
			for _, line := range strings.Split(logLinesFor(logs, userID), "\n") {
				var rec map[string]any
				if json.Unmarshal([]byte(line), &rec) != nil || rec["msg"] != message {
					continue
				}
				if rec["level"] != "WARN" {
					t.Errorf("a wrong password was logged at %v, want WARN: %s", rec["level"], line)
				}
				if rec["user_id"] != userID {
					t.Errorf("the line names user %v, want %s: %s", rec["user_id"], userID, line)
				}
				if ip, _ := rec["ip"].(string); ip == "" {
					t.Errorf("the line carries no client address: %s", line)
				}
				n, _ := rec["attempt"].(float64)
				attempts = append(attempts, int(n))
			}
			want := []int{1, 2, 3, 4, 5}
			if fmt.Sprint(attempts) != fmt.Sprint(want) {
				t.Errorf("attempt numbers logged = %v, want %v: one line per wrong password that was checked, numbered by the budget", attempts, want)
			}
			for _, secret := range []string{racePassword, raceNewPassword} {
				if strings.Contains(logs.String(), secret) {
					t.Errorf("a password reached the log: %q", secret)
				}
			}
		})
	}
}

// TestChangePassword_TheMemoryFallbackIsNotSilent: a handler that was never given a
// Redis counts in the memory of the process — and says so, once, at Warn, the first
// time it does. Counts in one process's memory are not shared with the other
// replicas and are lost on restart, which a deployment that arrived there by a
// refactor would otherwise never see.
func TestChangePassword_TheMemoryFallbackIsNotSilent(t *testing.T) {
	const phrase = "kept in the memory of this process only"
	count := func(logs *lockedLog) int { return strings.Count(logs.String(), phrase) }

	logs := captureProductionLog(t)
	a := newAuthRaceAppWith(t, withPasswordHash(t, nil), raceOptions{})
	if a.handler.passwordLockout != nil {
		t.Fatal("the harness wired a store: this test proved nothing")
	}
	for range 3 {
		a.change(t, wrongChangeBody)
	}
	if got := count(logs); got != 1 {
		t.Fatalf("a handler counting in memory said so %d times in three requests, want once:\n%s", got, logs.String())
	}
	if out := logs.String(); !strings.Contains(out, `"level":"WARN"`) {
		t.Errorf("the notice is not a warning: %s", out)
	}

	// The notice belongs to the handler: another one without a Redis says it again.
	b := newAuthRaceAppWith(t, withPasswordHash(t, nil), raceOptions{})
	b.change(t, wrongChangeBody)
	if got := count(logs); got != 2 {
		t.Errorf("two handlers counting in memory said so %d times in all, want twice", got)
	}

	// And one with a store, which is what production builds, says nothing.
	c, _ := lockoutApp(t, "redis", raceOptions{})
	c.change(t, wrongChangeBody)
	if got := count(logs); got != 2 {
		t.Errorf("a handler with a Redis store said it was counting in memory (%d notices in all, want 2)", got)
	}
}

// TestPasswordLockoutHealsALockThatHasNoExpiry: a lock whose key has lost its expiry
// — nothing the script writes, but a PERSIST or a hand-made SET could — must become
// neither permanent nor ignored. The next attempt reads it as locked for a whole
// lock, and gives it the expiry it should have had, so the lock ends on schedule.
func TestPasswordLockoutHealsALockThatHasNoExpiry(t *testing.T) {
	ctx := context.Background()
	l := newLockouts(t)[0]
	id := uuid.New()
	lockKey := passwordLockKey(id)

	lockoutArm(t, l.store, id)
	if err := l.rdb.Persist(ctx, lockKey).Err(); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if ttl := l.mr.TTL(lockKey); ttl != 0 || !l.mr.Exists(lockKey) {
		t.Fatalf("the lock has TTL %v and exists = %v after PERSIST: the setup did not make a lock with no expiry", ttl, l.mr.Exists(lockKey))
	}

	res, err := l.store.reserve(ctx, id)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if res.admission != lockoutRefused || res.retryAfter != passwordLockoutDuration {
		t.Errorf("an attempt on a lock with no expiry = {admission %d, retryAfter %v}, want it refused for the whole lock, %v: the lock must not be ignored",
			res.admission, res.retryAfter, passwordLockoutDuration)
	}
	if ttl := l.mr.TTL(lockKey); ttl != passwordLockoutDuration {
		t.Errorf("the lock's expiry after the attempt is %v, want %v: it must be given the one it lost", ttl, passwordLockoutDuration)
	}

	l.advance(passwordLockoutDuration)
	res, err = l.store.reserve(ctx, id)
	if err != nil || res.admission != lockoutAdmitted || res.attempts != 1 {
		t.Errorf("an attempt after the healed lock ran out = (%+v, %v), want it admitted as the first: the lock must not be permanent", res, err)
	}
}

// TestPasswordLockoutFiguresAreTheDocumentedOnes keeps the rule's three figures —
// how many wrong passwords, within how long, locked for how long — the same in the
// code and in the two places that tell a client: the API reference and the route's
// declaration. The figures follow the constants, so changing one fails here until
// the prose does.
func TestPasswordLockoutFiguresAreTheDocumentedOnes(t *testing.T) {
	words := map[int]string{3: "three", 4: "four", 5: "five", 6: "six", 7: "seven", 8: "eight", 9: "nine", 10: "ten"}
	word, ok := words[passwordLockoutThreshold]
	if !ok {
		t.Fatalf("passwordLockoutThreshold is %d: add its word to this test, which spells the figure as the prose does", passwordLockoutThreshold)
	}
	want := fmt.Sprintf("%s wrong ones within %d minutes lock the account out of this route for %d minutes",
		word, int(passwordLockoutWindow/time.Minute), int(passwordLockoutDuration/time.Minute))

	for _, file := range []string{"docs/api-reference.md", "internal/api/registry_auth.go"} {
		if !strings.Contains(strings.ToLower(authProse(t, file)), want) {
			t.Errorf("%s does not say %q: the prose and passwordLockoutThreshold, passwordLockoutWindow and passwordLockoutDuration have drifted", file, want)
		}
	}
}

// TestPasswordLockoutTheLockCycleIsTheCeilingOnGuesses holds the one reason the window is as
// long as it is. The audited lock cycle (the threshold's worth of guesses, the lock, again)
// must be the fastest way to guess, and staying UNDER the threshold slower. With a window
// shorter than the lock it is not: an attacker could make one guess fewer than the threshold
// each window and out-guess one who trips the lock, with no lock, no audit row and no line of
// its own in the log. Both attackers are run for a simulated day against the real store and
// its real constants: the one who trips the lock, and the one who stays one short of it for
// the length of a window and starts again when it ends.
func TestPasswordLockoutTheLockCycleIsTheCeilingOnGuesses(t *testing.T) {
	window, lock := passwordLockoutWindow, passwordLockoutDuration
	if window < lock {
		t.Fatalf("the window of the attempts is %v and the lock %v: an attacker who stays under the threshold gets a window's worth of guesses "+
			"for every %v, more often than one who trips the lock gets his", window, lock, window)
	}

	const day = 24 * time.Hour
	// simulate runs an attacker who makes burst guesses at once and then waits period,
	// for a day, and reports how many guesses were checked and how many times the
	// lock was armed.
	simulate := func(burst int, period time.Duration) (checked, locks int) {
		clock := &lockoutClock{now: time.Unix(1_700_000_000, 0)}
		store := newMemoryPasswordLockout(clock.Now)
		id := uuid.New()
		for elapsed := time.Duration(0); elapsed < day; elapsed += period {
			for range burst {
				res, err := store.reserve(context.Background(), id)
				if err != nil {
					t.Fatalf("reserve: %v", err)
				}
				switch res.admission {
				case lockoutAdmitted:
					checked++
				case lockoutAdmittedLast:
					checked++
					locks++
				}
			}
			clock.Advance(period)
		}
		return checked, locks
	}

	tripping, locks := simulate(passwordLockoutThreshold, lock)
	staying, stayingLocks := simulate(passwordLockoutThreshold-1, window)

	if stayingLocks != 0 {
		t.Fatalf("an attacker one guess short of the threshold armed the lock %d times: the simulation proves nothing", stayingLocks)
	}
	if locks == 0 {
		t.Fatal("an attacker who made the threshold's worth of guesses never armed the lock: the simulation proves nothing")
	}
	if staying > tripping {
		t.Errorf("staying under the threshold gets %d guesses a day and tripping the lock %d: the audited lock cycle must be the ceiling, "+
			"or the slowest attack is the one nothing records", staying, tripping)
	}
	// The cycle is what it is documented to be: the lock armed once for every period of it.
	if want := int(day / lock); locks != want {
		t.Errorf("the lock was armed %d times in a day, want %d: one for each %v", locks, want, lock)
	}
}

// TestAReservationNobodyFilledInIsARefusal pins the zero value: a reservation that
// was never filled in — an error path that returned the struct and the caller
// used it — must read as "do not check this password", not as permission.
func TestAReservationNobodyFilledInIsARefusal(t *testing.T) {
	if got := (lockoutReservation{}).admission; got != lockoutRefused {
		t.Errorf("the zero lockoutReservation's admission is %d, want lockoutRefused (%d)", got, lockoutRefused)
	}
}

// TestPasswordLockedRoundsUpAndNeverAnswersZero holds the two figures of a 429 to
// what a client can act on. The Retry-After is whole seconds rounded UP, so a
// client that waits exactly that long is not refused again; the message says the
// minutes left, rounded up the same way; and neither is ever zero, which would
// read as "retry now" for a request that was just refused.
func TestPasswordLockedRoundsUpAndNeverAnswersZero(t *testing.T) {
	for _, tc := range []struct {
		wait       time.Duration
		retryAfter string
		minutes    string
	}{
		{0, "1", "1 more minute"},
		{time.Millisecond, "1", "1 more minute"},
		{time.Second, "1", "1 more minute"},
		{time.Second + time.Millisecond, "2", "1 more minute"},
		{59 * time.Second, "59", "1 more minute"},
		{time.Minute, "60", "1 more minute"},
		{time.Minute + time.Millisecond, "61", "2 more minutes"},
		{29*time.Minute + 30*time.Second, "1770", "30 more minutes"},
		{30 * time.Minute, "1800", "30 more minutes"},
	} {
		t.Run(tc.wait.String(), func(t *testing.T) {
			app := fiber.New()
			app.Get("/locked", func(c fiber.Ctx) error { return passwordLocked(c, tc.wait) })
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/locked", nil))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			raw := make([]byte, 512)
			n, _ := resp.Body.Read(raw)
			msg := string(raw[:n])

			if resp.StatusCode != http.StatusTooManyRequests {
				t.Errorf("status = %d, want 429", resp.StatusCode)
			}
			if got := resp.Header.Get("Retry-After"); got != tc.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tc.retryAfter)
			}
			if !strings.Contains(msg, "locked for "+tc.minutes+";") {
				t.Errorf("message = %q, want it to say %q", msg, tc.minutes)
			}
		})
	}
}

// TestChangePassword_OnlyAnAttemptAtAPasswordIsCounted: a request that is refused
// before a password is in question uses none of the budget — an account whose
// password belongs to an identity provider, or one whose row could not be read —
// and does not touch Redis.
func TestChangePassword_OnlyAnAttemptAtAPasswordIsCounted(t *testing.T) {
	t.Run("an account from an identity provider", func(t *testing.T) {
		a := newAuthRaceAppWith(t, withPasswordHash(t, func(s *raceStore) { s.user.AuthSource = "ldap" }), raceOptions{})
		a.handler.SetPasswordLockoutStore(a.rdb)
		for i := 0; i < passwordLockoutThreshold+2; i++ {
			status, body, _ := a.change(t, wrongChangeBody)
			if status != http.StatusForbidden || !strings.Contains(messageOf(body), "identity provider") {
				t.Fatalf("request %d = %d (%v), want 403 from the identity provider rule, never 429", i+1, status, body)
			}
		}
		if keys := a.redis.Keys(); len(keys) != 0 {
			t.Errorf("Redis holds %v: an account with no password to guess must not be counted", keys)
		}
	})

	t.Run("an account that could not be read", func(t *testing.T) {
		a := newAuthRaceAppWith(t, withPasswordHash(t, func(s *raceStore) { s.userErr = errRaceTransient }), raceOptions{})
		a.handler.SetPasswordLockoutStore(a.rdb)
		for i := 0; i < passwordLockoutThreshold+2; i++ {
			status, body, _ := a.change(t, wrongChangeBody)
			if status != http.StatusServiceUnavailable {
				t.Fatalf("request %d = %d (%v), want 503: nothing was checked, so nothing was counted", i+1, status, body)
			}
		}
		if keys := a.redis.Keys(); len(keys) != 0 {
			t.Errorf("Redis holds %v: an attempt that never reached its password must not be counted", keys)
		}
	})
}

var (
	slowPasswordHashOnce  sync.Once
	slowPasswordHashValue string
)

// slowPasswordHash is a hash of racePassword at a cost where checking a password
// against it takes real time — tens of milliseconds, and seconds under the race
// detector, against the microseconds of everything else a request does — without
// the production cost's quarter of a second a time.
func slowPasswordHash(t *testing.T) string {
	t.Helper()
	slowPasswordHashOnce.Do(func() {
		h, err := bcrypt.GenerateFromPassword([]byte(racePassword), 8)
		if err != nil {
			panic(err)
		}
		slowPasswordHashValue = string(h)
	})
	return slowPasswordHashValue
}

// TestChangePassword_ALockedRequestIsRefusedBeforeAnyPasswordIsChecked is what "the lock is
// read before the password is checked" is worth: a locked request costs no bcrypt. A bcrypt at
// the production cost is a quarter of a second of CPU, and an attacker with a token who is
// locked out would otherwise still spend it once per request, so the lock would bound the
// guesses and not the work. Every answer is the same either way, so only the time tells. The
// account's password is hashed at cost 8; a request that IS checked takes the time of one
// comparison and a locked one must take a small fraction of it, compared relatively (the
// fastest checked request against the slowest locked one) so that a busy machine moves both.
func TestChangePassword_ALockedRequestIsRefusedBeforeAnyPasswordIsChecked(t *testing.T) {
	// The production work factor, not TestMain's cheapest one: hashing the NEW
	// password ahead of the lock must be as visible here as checking the old one.
	defer auth.SetBcryptCostForTesting(0)()
	a := newAuthRaceAppWith(t, func(s *raceStore) { s.user.PasswordHash = slowPasswordHash(t) }, raceOptions{})
	a.handler.passwordLockout = newMemoryPasswordLockout(time.Now)

	checkedFastest := time.Hour
	for i := 1; i <= passwordLockoutThreshold; i++ {
		resp, elapsed := a.postAs(t, "/auth/change-password", wrongChangeBody, 60*time.Second)
		want := http.StatusForbidden
		if i == passwordLockoutThreshold {
			want = http.StatusTooManyRequests
		}
		if resp.StatusCode != want {
			t.Fatalf("attempt %d = %d, want %d", i, resp.StatusCode, want)
		}
		checkedFastest = min(checkedFastest, elapsed)
	}

	var lockedSlowest time.Duration
	for _, body := range []string{wrongChangeBody, changeBody, wrongChangeBody} {
		resp, elapsed := a.postAs(t, "/auth/change-password", body, 60*time.Second)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("a request on a locked account = %d, want 429", resp.StatusCode)
		}
		lockedSlowest = max(lockedSlowest, elapsed)
	}

	if lockedSlowest*3 > checkedFastest {
		t.Errorf("a locked request took %v and the fastest request that was checked %v: the lock must be read before the password is hashed, "+
			"or the lock bounds the guesses and not the work", lockedSlowest, checkedFastest)
	}
}

// TestMemoryPasswordLockoutForgetsWhatHasRunOut: the memory store holds an account
// only while it has attempts in a window or a lock to remember, so a process that
// is asked about many accounts does not keep every one of them for ever.
func TestMemoryPasswordLockoutForgetsWhatHasRunOut(t *testing.T) {
	ctx := context.Background()
	clock := &lockoutClock{now: time.Unix(1_700_000_000, 0)}
	store := newMemoryPasswordLockout(clock.Now)

	counted, locked, probe := uuid.New(), uuid.New(), uuid.New()
	if _, err := store.reserve(ctx, counted); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	lockoutArm(t, store, locked)
	if got := len(store.entries); got != 2 {
		t.Fatalf("the store holds %d accounts, want the counted one and the locked one", got)
	}

	// The window is at least as long as the lock (TestPasswordLockoutTheLockCycleIsTheCeilingOnGuesses
	// holds it there), so the lock ends first: the locked account goes, and the
	// counted one stays while its attempts are still in their window.
	clock.Advance(passwordLockoutDuration)
	if _, err := store.reserve(ctx, probe); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, kept := store.entries[locked]; kept {
		t.Error("an account whose lock had ended was kept")
	}
	if _, kept := store.entries[counted]; !kept {
		t.Error("an account with attempts still in their window was forgotten")
	}

	// Then the window ends, and the counted account goes with it.
	clock.Advance(passwordLockoutWindow - passwordLockoutDuration)
	if _, err := store.reserve(ctx, probe); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, kept := store.entries[counted]; kept {
		t.Error("an account whose window had ended was kept")
	}
	if got := len(store.entries); got != 1 {
		t.Errorf("the store holds %d accounts, want only the one asked about since", got)
	}
}

// TestChangePassword_TheLockoutAuditRunsOnAFollowUpDeadline: the audit row of a
// lockout is a record of a decision already made, so it runs under a deadline of
// its own, like every other audit write of this route. An insert that stalls costs
// the answer the follow-up bound and no more, and the lock stands whether or not
// the row was written.
func TestChangePassword_TheLockoutAuditRunsOnAFollowUpDeadline(t *testing.T) {
	const bound = 200 * time.Millisecond
	a, _ := lockoutApp(t, "memory", raceOptions{followUpTimeout: bound})
	a.lockoutSpend(t, passwordLockoutThreshold-1)
	w := a.watchRowLock(t, "InsertAuditLog")

	requestAt := time.Now()
	resp, _ := a.postTimed(t, "/auth/change-password", wrongChangeBody, "",
		map[string]string{"X-Test-Acting-User": a.store.user.ID.String()}, 10*time.Second)
	answeredAt := time.Now()

	if len(w.waited()) != 1 {
		t.Fatalf("statements that waited = %v, want the audit insert: the lockout wrote no row, so this test proved nothing", w.waited())
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: the lock is in place whether or not its row was written", resp.StatusCode)
	}
	w.checkAnswered(t, bound, requestAt, answeredAt)
	if got := a.store.auditActions(); len(got) != 0 {
		t.Errorf("audit actions = %v, want none: the insert never completed", got)
	}
	if status, _, _ := a.change(t, changeBody); status != http.StatusTooManyRequests {
		t.Errorf("the right password after the lock = %d, want 429", status)
	}
}

// scriptAnswerHook replaces what Redis answered to the lockout's script, to show
// what the store does with an answer it does not understand.
type scriptAnswerHook struct{ answer any }

func (scriptAnswerHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h scriptAnswerHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if err := next(ctx, cmd); err != nil {
			return err
		}
		// EVALSHA first, and EVAL when the server does not have the script yet (a
		// fresh Redis answers NOSCRIPT, which go-redis meets by sending the script).
		if c, ok := cmd.(*redis.Cmd); ok && (strings.EqualFold(cmd.Name(), "evalsha") || strings.EqualFold(cmd.Name(), "eval")) {
			c.SetVal(h.answer)
		}
		return nil
	}
}

func (scriptAnswerHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// TestPasswordLockoutRefusesAScriptAnswerItDoesNotUnderstand: a count that cannot be
// read is a count that could not be kept, so the store reports an error — and the
// handler answers an error with the 503 and checks nothing — rather than guess that
// an answer it has no word for means "admitted".
func TestPasswordLockoutRefusesAScriptAnswerItDoesNotUnderstand(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer any
	}{
		{"an admission it has no name for", []any{int64(7), int64(1), int64(0)}},
		{"too few values", []any{int64(0), int64(1)}},
		{"too many values", []any{int64(0), int64(1), int64(0), int64(0)}},
		{"not a list", "OK"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLockouts(t)[0]
			l.rdb.AddHook(scriptAnswerHook{answer: tc.answer})

			res, err := l.store.reserve(context.Background(), uuid.New())

			if err == nil {
				t.Fatalf("reserve = %+v with no error: an answer that cannot be read must not be taken for permission", res)
			}
			if res.admission != lockoutRefused {
				t.Errorf("reserve returned admission %d beside its error, want the refusal", res.admission)
			}
		})
	}
}
