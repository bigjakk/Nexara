package handlers

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// stuckRedis is a go-redis hook that holds every command stuck answers true for until
// release is closed: what a Redis that accepts a connection and never answers looks like to
// its caller, the command neither succeeding nor failing, with no context deadline reaching
// it. done receives the outcome of each held command once it has run.
type stuckRedis struct {
	stuck   func(cmd redis.Cmder) bool
	release chan struct{}
	done    chan error
	held    atomic.Int32 // how many commands have been held
}

func (h *stuckRedis) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *stuckRedis) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if !h.stuck(cmd) {
			return next(ctx, cmd)
		}
		h.held.Add(1)
		<-h.release
		err := next(ctx, cmd)
		h.done <- err
		return err
	}
}

func (h *stuckRedis) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// isSessionMirrorWrite is the SET of a session's Redis row.
func isSessionMirrorWrite(cmd redis.Cmder) bool {
	args := cmd.Args()
	if len(args) < 2 || args[0] != "set" {
		return false
	}
	key, _ := args[1].(string)
	return strings.HasPrefix(key, "nexara:session:")
}

// TestLogin_ARedisThatNeverAnswersDoesNotHoldTheSignIn: a Redis that accepts the write of
// the session's row and never answers must delay the response by the mirror wait, a second,
// and not by the client's read timeout, five seconds, tried again. The answer is the 200
// with its token and cookie, the session is live, and the write that was given up on lands
// when Redis answers after all. The hook holds the command rather than a deadline cutting it
// short, because a context deadline does not reach the client's socket: only not waiting
// bounds it. The sibling in internal/auth is TestCreateSession_ARedisThatNeverAnswers….
func TestLogin_ARedisThatNeverAnswersDoesNotHoldTheSignIn(t *testing.T) {
	user := epochUser(t, epochLoginEmail, 2)
	a := newEpochApp(t, newEpochStore(user), epochOptions{})
	a.auth.SetPasswordLockoutStore(a.rdb) // the sign-in's lockout counts in this Redis too

	hook := &stuckRedis{stuck: isSessionMirrorWrite, release: make(chan struct{}), done: make(chan error, 8)}
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(hook.release) }) }
	t.Cleanup(releaseAll)
	a.rdb.AddHook(hook)

	start := time.Now()
	resp := a.send(t, http.MethodPost, "/auth/login", epochLoginBody(epochLoginEmail), nil, 10*time.Second)
	elapsed := time.Since(start)
	body := authRequireStatus(t, resp, http.StatusOK) // a stuck mirror must not fail a sign-in that created its session

	if tok, _ := body["access_token"].(string); tok == "" {
		t.Errorf("no access token: %v", body)
	}
	authRequireCookie(t, resp, cookieNew)
	if elapsed > 3*time.Second {
		t.Errorf("the sign-in took %v: it waited for the stuck write, whose wait is one second", elapsed)
	}
	live := a.store.liveSessionsOf(user.ID)
	if len(live) != 1 {
		t.Fatalf("%d live sessions, want 1", len(live))
	}
	key := "nexara:session:" + live[0].ID.String()
	if a.redis.Exists(key) {
		t.Fatal("the row exists although the write is held: the premise of the test is gone")
	}

	releaseAll()
	select {
	case err := <-hook.done:
		if err != nil {
			t.Errorf("the released write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the released write never ran: it was abandoned, not left to finish")
	}
	if !a.redis.Exists(key) {
		t.Error("the released write never landed: it was abandoned, not left to finish")
	}
}

// TestLogin_AHungRedisDoesNotTellUnknownIdentifiersApart: while Redis does not answer, a
// failed sign-in with an identifier that names no account makes no Redis call beyond
// the attempt's own count, as a known account's does not: a second bounded wait would
// make it a second slower, and the time an oracle for which accounts exist.
func TestLogin_AHungRedisDoesNotTellUnknownIdentifiersApart(t *testing.T) {
	t.Parallel()
	a := newEpochApp(t, newEpochStore(epochUser(t, epochLoginEmail, 0)), epochOptions{})
	a.auth.SetPasswordLockoutStore(a.rdb)
	hook := &stuckRedis{stuck: func(redis.Cmder) bool { return true }, release: make(chan struct{}), done: make(chan error, 8)}
	t.Cleanup(func() { close(hook.release) })
	a.rdb.AddHook(hook)

	if resp, _ := a.loginAs(t, "carol@example.com", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if n := hook.held.Load(); n != 1 {
		t.Errorf("the failed sign-in waited on %d Redis calls, want 1: the attempt's count, as a known account's", n)
	}
}
