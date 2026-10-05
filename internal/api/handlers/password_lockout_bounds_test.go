package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/pkg/redisutil"
)

// The bounds of the change-password lockout, held against a REAL socket.
//
// A hook that waits for the caller's context and then fails is a model of a Redis
// client that honours the context's deadline, and the one production builds does
// not: pkg/redisutil makes it with redis.ParseURL and its defaults, where the
// context does not reach the socket (ContextTimeoutEnabled is off), a read waits
// for ReadTimeout and a failed one is tried again. A test whose stand-in honoured
// the deadline would pass whether the handler bounded the wait or not. So these
// tests use the client production uses, pointed at a listener that accepts the
// connection and never says a word.

// blackHole listens on a loopback port, accepts every connection and never
// answers, and returns its address. It is a Redis that has stopped replying, or a
// network that has stopped delivering, which is the failure a refused connection
// and an error reply are not: nothing ever comes back.
func blackHole(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	return ln.Addr().String()
}

// productionClientTo builds a Redis client the way cmd/nexara builds its own —
// redisutil.NewClientLazy, which is redis.ParseURL and the defaults it leaves — and
// points it at addr. It is closed with the test, which also ends the calls a stalled
// socket left waiting.
func productionClientTo(t *testing.T, addr string) *redis.Client {
	t.Helper()
	rdb, err := redisutil.NewClientLazy("redis://" + addr + "/0")
	if err != nil {
		t.Fatalf("NewClientLazy: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// TestPasswordLockoutIsBoundedByItsContextAndNotByTheRedisClient: every call the
// store makes to Redis stops waiting when its context ends — the count, the release
// of an attempt (an ordinary one, and the one that armed a lock) and the clear — and
// says so with the context's own error, whatever the client would have waited.
func TestPasswordLockoutIsBoundedByItsContextAndNotByTheRedisClient(t *testing.T) {
	const bound = 150 * time.Millisecond
	addr := blackHole(t)

	// The control is what makes the rows below mean something: the same client,
	// given the same short context, is STILL waiting long after it ended. If it
	// returned at the context's deadline by itself, the rows would pass without the
	// store bounding anything.
	t.Run("control: the client keeps waiting past its context", func(t *testing.T) {
		rdb := productionClientTo(t, addr)
		ctx, cancel := context.WithTimeout(context.Background(), bound)
		defer cancel()
		returned := make(chan error, 1)
		go func() { returned <- rdb.Ping(ctx).Err() }()
		select {
		case err := <-returned:
			t.Fatalf("the production Redis client returned after %v (%v), when its context ended at %v: it now honours the context on a stalled socket, "+
				"so the rows below no longer show the store bounding anything — if that is so, boundedByContext is redundant and may go, with them",
				time.Second, err, bound)
		case <-time.After(time.Second):
		}
	})

	id := uuid.New()
	for _, op := range []struct {
		name string
		run  func(ctx context.Context, store redisPasswordLockout) error
	}{
		{"the count", func(ctx context.Context, store redisPasswordLockout) error {
			_, err := store.reserve(ctx, id)
			return err
		}},
		{"the release of an attempt", func(ctx context.Context, store redisPasswordLockout) error {
			return store.release(ctx, id, lockoutReservation{admission: lockoutAdmitted, attempts: 1})
		}},
		{"the release of the attempt that armed the lock", func(ctx context.Context, store redisPasswordLockout) error {
			return store.release(ctx, id, lockoutReservation{admission: lockoutAdmittedLast, attempts: passwordLockoutThreshold, token: "token"})
		}},
		{"the clear", func(ctx context.Context, store redisPasswordLockout) error {
			return store.clear(ctx, id)
		}},
	} {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			store := redisPasswordLockout{rdb: productionClientTo(t, addr)}
			ctx, cancel := context.WithTimeout(context.Background(), bound)
			defer cancel()

			began := time.Now()
			err := op.run(ctx, store)
			waited := time.Since(began)

			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("error = %v, want it to wrap the context's deadline: the call ended for some other reason", err)
			}
			if waited < bound {
				t.Errorf("returned after %v, before its context ended at %v: it did not wait at all", waited, bound)
			}
			if waited > 2*time.Second {
				t.Errorf("returned after %v, long after its context ended at %v: the store waited for the Redis client's own timeouts", waited, bound)
			}
		})
	}
}

// TestPasswordLockoutBoundedCallsDoNotLeakOrPanic: a call given up on is still
// running when its caller has moved on, and what it does then must be harmless: a
// panic in it is an error, not a crashed process, and its answer arriving after
// nobody is waiting is dropped without blocking.
func TestPasswordLockoutBoundedCallsDoNotLeakOrPanic(t *testing.T) {
	t.Run("a panic in the call is an error", func(t *testing.T) {
		_, err := boundedByContext(context.Background(), func(context.Context) (int, error) {
			panic("a Redis client bug")
		})
		if err == nil || !strings.Contains(err.Error(), "panicked") {
			t.Errorf("error = %v, want one that says the call panicked", err)
		}
	})

	t.Run("an answer that arrives after the caller gave up is dropped, and nothing is left running", func(t *testing.T) {
		// Many calls are given up on and then let go. Each one's answer must go into a
		// buffer nobody reads, and each goroutine must end: an unbuffered channel would
		// leave every one of them blocked on a send for ever, one per request a stalled
		// Redis was asked about.
		const abandoned = 40
		before := runtime.NumGoroutine()
		release := make(chan struct{})
		var letGo sync.Once
		let := func() { letGo.Do(func() { close(release) }) }
		// A call that is NOT given up on when its context ends waits here for ever; the
		// safety lets it go after a few seconds, so that the test fails with its error
		// instead of hanging.
		safety := time.AfterFunc(3*time.Second, let)
		defer safety.Stop()
		var returned sync.WaitGroup
		for range abandoned {
			returned.Add(1)
			ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
			_, err := boundedByContext(ctx, func(context.Context) (int, error) {
				defer returned.Done()
				<-release
				return 7, nil
			})
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want the context's deadline", err)
			}
		}
		let()
		returned.Wait()

		// The goroutines end a moment after their calls return; there is no event to wait
		// for, so what is observed is the count, against a limit.
		deadline := time.Now().Add(5 * time.Second)
		for runtime.NumGoroutine() > before+abandoned/4 {
			if time.Now().After(deadline) {
				t.Fatalf("%d goroutines are running, %d before the test: the abandoned calls never ended — their answers must not block on a reader that left",
					runtime.NumGoroutine(), before)
			}
			time.Sleep(time.Millisecond)
		}
	})

	t.Run("an answer that is ready wins over a context that has not ended", func(t *testing.T) {
		got, err := boundedByContext(context.Background(), func(context.Context) (int, error) { return 7, nil })
		if err != nil || got != 7 {
			t.Errorf("boundedByContext = (%d, %v), want (7, nil)", got, err)
		}
	})
}

// timedLockout wraps a store and records, for each call, the time the call was
// allowed (the context's deadline when it began) and the time it took.
type timedLockout struct {
	passwordLockoutStore
	mu    sync.Mutex
	calls []timedCall
}

type timedCall struct {
	name        string
	hasDeadline bool          // the context carried a deadline at all
	allowed     time.Duration // the deadline, from the start of the call
	spent       time.Duration
	err         error
}

func (l *timedLockout) record(ctx context.Context, name string, began time.Time, err error) {
	call := timedCall{name: name, spent: time.Since(began), err: err}
	if d, ok := ctx.Deadline(); ok {
		call.hasDeadline, call.allowed = true, d.Sub(began)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call)
}

func (l *timedLockout) named(name string) []timedCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []timedCall
	for _, c := range l.calls {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

func (l *timedLockout) reserve(ctx context.Context, id uuid.UUID) (lockoutReservation, error) {
	began := time.Now()
	res, err := l.passwordLockoutStore.reserve(ctx, id)
	l.record(ctx, "reserve", began, err)
	return res, err
}

func (l *timedLockout) release(ctx context.Context, id uuid.UUID, res lockoutReservation) error {
	began := time.Now()
	err := l.passwordLockoutStore.release(ctx, id, res)
	l.record(ctx, "release", began, err)
	return err
}

func (l *timedLockout) clear(ctx context.Context, id uuid.UUID) error {
	began := time.Now()
	err := l.passwordLockoutStore.clear(ctx, id)
	l.record(ctx, "clear", began, err)
	return err
}

// splitLockout counts in one store and gives back and clears in another: the count
// in a healthy Redis, so that the request gets as far as a verified password, and the
// steps after the decision in one that never answers.
type splitLockout struct {
	count, after passwordLockoutStore
}

func (s splitLockout) reserve(ctx context.Context, id uuid.UUID) (lockoutReservation, error) {
	return s.count.reserve(ctx, id)
}

func (s splitLockout) release(ctx context.Context, id uuid.UUID, res lockoutReservation) error {
	return s.after.release(ctx, id, res)
}

func (s splitLockout) clear(ctx context.Context, id uuid.UUID) error {
	return s.after.clear(ctx, id)
}

// TestChangePassword_ACountThatNeverAnswersIsA503AtTheBound: the count is part of
// the deciding work and inside its bound. Against a Redis that accepts and never
// answers, the request is answered 503 when the deciding bound ends — not when the
// Redis client gives up, which is seconds later and retried — with the password
// unchecked and unchanged, whichever password it carries.
func TestChangePassword_ACountThatNeverAnswersIsA503AtTheBound(t *testing.T) {
	const bound = 300 * time.Millisecond
	for _, body := range []struct{ name, body string }{{"the right password", changeBody}, {"a wrong password", wrongChangeBody}} {
		t.Run(body.name, func(t *testing.T) {
			logs := captureProductionLog(t)
			a, _ := lockoutApp(t, "redis", raceOptions{dbTimeout: bound})
			a.handler.SetPasswordLockoutStore(productionClientTo(t, blackHole(t)))
			a.redis.Set(a.sessionKey(), "{}")
			var checked atomic.Int32
			a.handler.passwordChecker = func(hash, password string) error {
				checked.Add(1)
				return auth.CheckPassword(hash, password)
			}

			resp, elapsed := a.postAs(t, "/auth/change-password", body.body, 3*time.Second)
			decoded := decodeObject(t, resp)

			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d (%v), want 503", resp.StatusCode, decoded)
			}
			if msg := messageOf(decoded); !strings.Contains(msg, "NOT changed") || !strings.Contains(msg, "attempt limit") {
				t.Errorf("message = %q, want it to say the attempt limit could not be checked and the password was NOT changed", msg)
			}
			checkBounded(t, elapsed, bound)
			if got := checked.Load(); got != 0 {
				t.Errorf("%d passwords were checked, want none: a password that was not counted must not be checked", got)
			}
			if a.passwordChanged() || a.store.snapshot().IsRevoked || !a.redis.Exists(a.sessionKey()) {
				t.Error("the password or the sessions changed although the attempt could not be counted")
			}
			if got := a.store.auditActions(); len(got) != 0 {
				t.Errorf("audit actions = %v, want none", got)
			}
			out := logLinesFor(logs, a.store.user.ID.String())
			if !strings.Contains(out, "could not be reached") {
				t.Errorf("no log line for this user says the counter could not be reached: %q", out)
			}
			if !strings.Contains(out, "may still have been counted") || !strings.Contains(out, `"abandoned":true`) {
				t.Errorf("the line does not say the count was abandoned and may still land: %q", out)
			}
		})
	}
}

// lateCount is a store whose count is given up on — the call answers that its
// deadline ended, at once — and which still lands in Redis afterwards, when the test
// lets it: what boundedByContext's abandoned call does when the Redis it asked is
// slow, and not dead.
type lateCount struct {
	passwordLockoutStore
	land   chan struct{} // closed by the test: the abandoned count now reaches Redis
	landed chan struct{} // closed once it has
}

func (l *lateCount) reserve(_ context.Context, id uuid.UUID) (lockoutReservation, error) {
	go func() {
		<-l.land
		_, _ = l.passwordLockoutStore.reserve(context.Background(), id)
		close(l.landed)
	}()
	return lockoutReservation{}, fmt.Errorf("count a password attempt: %w", context.DeadlineExceeded)
}

// TestChangePassword_AnAbandonedCountThatLandsLaterCanArmTheLockUnrecorded pins the
// residual boundedByContext and passwordLockoutUnavailable document, so that the
// documentation is a statement the code keeps and not a hope. The request is
// answered 503 because its count could not be made in time; the count then reaches
// Redis after all, and being the fifth it arms the lock — with no audit row, and
// with a 429 for whoever asks next that says "too many incorrect attempts" about an
// account whose owner made none. The 503's own log line is the one place that says it
// could happen.
//
// If this ever fails because the lock is no longer armed unrecorded (a marker for
// counts in flight, an audit row from the late arming), the comments on
// boundedByContext, reserve and passwordLockoutUnavailable, and the 503 line, say
// something that is no longer true: change them together.
func TestChangePassword_AnAbandonedCountThatLandsLaterCanArmTheLockUnrecorded(t *testing.T) {
	logs := captureProductionLog(t)
	a, _ := lockoutApp(t, "redis", raceOptions{})
	real := redisPasswordLockout{rdb: a.rdb}
	id := a.store.user.ID
	// Four attempts are counted already: the abandoned one is the fifth.
	for range passwordLockoutThreshold - 1 {
		if _, err := real.reserve(context.Background(), id); err != nil {
			t.Fatalf("reserve: %v", err)
		}
	}
	late := &lateCount{passwordLockoutStore: real, land: make(chan struct{}), landed: make(chan struct{})}
	a.handler.passwordLockout = late

	status, body, _ := a.change(t, wrongChangeBody)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("the request whose count was abandoned = %d (%v), want 503", status, body)
	}
	if out := logLinesFor(logs, id.String()); !strings.Contains(out, "may still have been counted") {
		t.Errorf("the 503's log line does not say the attempt may still have been counted: %q", out)
	}
	if a.redis.Exists(passwordLockKey(id)) {
		t.Fatal("the lock exists before the abandoned count landed: this test proved nothing")
	}

	close(late.land)
	select {
	case <-late.landed:
	case <-time.After(10 * time.Second):
		t.Fatal("the abandoned count never reached Redis")
	}

	a.handler.passwordLockout = real
	status, body, header := a.change(t, wrongChangeBody)
	if status != http.StatusTooManyRequests || header.Get("Retry-After") != "1800" {
		t.Fatalf("the next request = %d (%v), Retry-After %q, want 429 and 1800: the late count was the fifth, and armed the lock",
			status, body, header.Get("Retry-After"))
	}
	if got := a.store.auditActions(); len(got) != 0 {
		t.Errorf("audit actions = %v: the lock was armed by a count nobody answered, and nothing records it", got)
	}
}

// TestChangePassword_AReleaseThatNeverAnswersIsHeldToTheFollowUpBound: the password
// verified and the change did not commit — the new one is too weak — so the request
// gives its attempt back, and the Redis it asks never answers. The answer is the one
// the request had decided on, 400, and the wait is the follow-up bound (the call is
// given that deadline, not the transaction's or none), not the seconds the Redis
// client would have spent.
func TestChangePassword_AReleaseThatNeverAnswersIsHeldToTheFollowUpBound(t *testing.T) {
	const followUp = 200 * time.Millisecond
	logs := captureProductionLog(t)
	a, _ := lockoutApp(t, "redis", raceOptions{followUpTimeout: followUp})
	counting := redisPasswordLockout{rdb: a.rdb}
	timed := &timedLockout{passwordLockoutStore: splitLockout{count: counting, after: redisPasswordLockout{rdb: productionClientTo(t, blackHole(t))}}}
	a.handler.passwordLockout = timed

	resp, elapsed := a.postAs(t, "/auth/change-password", weakChangeBody, 3*time.Second)
	decoded := decodeObject(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d (%v), want 400: the decision was made before the release, and a release that never answers must not change it", resp.StatusCode, decoded)
	}
	calls := timed.named("release")
	if len(calls) != 1 {
		t.Fatalf("release was called %d times, want once: the password verified and nothing was committed, so this test proved nothing", len(calls))
	}
	if c := calls[0]; !c.hasDeadline || c.allowed > followUp {
		t.Errorf("the release was allowed %v (a deadline at all: %v), want a deadline of at most the follow-up bound, %v", c.allowed, c.hasDeadline, followUp)
	} else if c.spent > c.allowed+time.Second || !errors.Is(c.err, context.DeadlineExceeded) {
		t.Errorf("the release spent %v of the %v it was allowed and ended with %v: it must stop at the end of its context", c.spent, c.allowed, c.err)
	}
	if elapsed < followUp || elapsed > 3*time.Second {
		t.Errorf("answered after %v: the wait for the release should be the follow-up bound, %v, and no longer", elapsed, followUp)
	}
	if out := logLinesFor(logs, a.store.user.ID.String()); !strings.Contains(out, "could not give back the attempt") {
		t.Errorf("no log line for this user says the release failed: %q", out)
	}
	if !a.redis.Exists("pwchange:user:fail:" + a.store.user.ID.String()) {
		t.Error("the attempt is gone from the count although the release never reached Redis")
	}
}

// TestChangePassword_AClearThatNeverAnswersIsHeldToTheFollowUpBound: the change
// committed, the count is to be cleared with the record of it, and the Redis it asks
// never answers. The change is made and answered 200; the record is written first;
// and the clear is held to the follow-up deadline it shares with the record, and is
// stopped there.
func TestChangePassword_AClearThatNeverAnswersIsHeldToTheFollowUpBound(t *testing.T) {
	const followUp = 500 * time.Millisecond
	logs := captureProductionLog(t)
	a, _ := lockoutApp(t, "redis", raceOptions{followUpTimeout: followUp})
	timed := &timedLockout{passwordLockoutStore: splitLockout{
		count: redisPasswordLockout{rdb: a.rdb},
		after: redisPasswordLockout{rdb: productionClientTo(t, blackHole(t))},
	}}
	a.handler.passwordLockout = timed

	status, body, _ := a.change(t, changeBody)

	if status != http.StatusOK || !a.passwordChanged() {
		t.Fatalf("status = %d (%v), changed = %v, want 200 and the new password in place: a clear that never answers must not undo a change that is made", status, body, a.passwordChanged())
	}
	if got := a.store.auditActions(); len(got) != 1 || got[0] != "password_changed" {
		t.Errorf("audit actions = %v, want [password_changed]: the record must be written before the clear is tried", got)
	}
	calls := timed.named("clear")
	if len(calls) != 1 {
		t.Fatalf("clear was called %d times, want once", len(calls))
	}
	if c := calls[0]; !c.hasDeadline || c.allowed > followUp {
		t.Errorf("the clear was allowed %v (a deadline at all: %v), want a deadline of at most the follow-up bound, %v: it shares the record's", c.allowed, c.hasDeadline, followUp)
	} else if c.spent > max(c.allowed, 0)+time.Second || !errors.Is(c.err, context.DeadlineExceeded) {
		t.Errorf("the clear spent %v of the %v it was allowed and ended with %v: it must stop at the end of its context", c.spent, c.allowed, c.err)
	}
	if got := timed.named("release"); len(got) != 0 {
		t.Errorf("release was called %d times for a change that committed: a committed change clears, it does not give one attempt back", len(got))
	}
	if out := logLinesFor(logs, a.store.user.ID.String()); !strings.Contains(out, "could not clear the failed-attempt count") {
		t.Errorf("no log line for this user says the clear failed: %q", out)
	}
}
