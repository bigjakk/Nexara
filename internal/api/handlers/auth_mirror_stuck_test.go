package handlers

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// stuckRedis is a go-redis hook that holds every command stuck answers true for until
// release is closed — what a Redis that accepts a connection and never answers looks
// like to its caller: the command neither succeeds nor fails, and no context deadline
// reaches it.
type stuckRedis struct {
	stuck   func(cmd redis.Cmder) bool
	release chan struct{}
}

func (h *stuckRedis) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *stuckRedis) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.stuck(cmd) {
			<-h.release
		}
		return next(ctx, cmd)
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

// TestLogin_ARedisThatNeverAnswersDoesNotHoldTheSignIn is the sign-in that the stuck
// mirror write of internal/auth's TestCreateSession_ARedisThatNeverAnswers… would
// otherwise hold: a Redis that accepts the write of the session's row and never
// answers must delay the response by the mirror wait — a second — and not by the
// client's read timeout, five seconds, tried again. Here it is the whole sign-in:
// the answer is the 200 with its token and its cookie, the session is live, and the
// write that was given up on lands when Redis answers after all.
//
// The hook holds the command rather than a deadline cutting it short, because a
// context deadline does not reach the client's socket: only not waiting bounds it.
func TestLogin_ARedisThatNeverAnswersDoesNotHoldTheSignIn(t *testing.T) {
	user := epochUser(t, epochLoginEmail, 2)
	a := newEpochApp(t, newEpochStore(user), epochOptions{})

	release := make(chan struct{})
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	a.rdb.AddHook(&stuckRedis{stuck: isSessionMirrorWrite, release: release})

	start := time.Now()
	resp := a.send(t, http.MethodPost, "/auth/login", epochLoginBody(epochLoginEmail), nil, 10*time.Second)
	elapsed := time.Since(start)
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v): a stuck mirror must not fail a sign-in that created its session", resp.StatusCode, body)
	}
	if tok, _ := body["access_token"].(string); tok == "" {
		t.Errorf("no access token: %v", body)
	}
	if cookies := refreshCookies(resp); len(cookies) != 1 || cookies[0].Value == "" {
		t.Errorf("refresh cookies = %+v, want one live cookie", cookies)
	}
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
	deadline := time.Now().Add(5 * time.Second)
	for !a.redis.Exists(key) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !a.redis.Exists(key) {
		t.Error("the released write never landed: it was abandoned, not left to finish")
	}
}
