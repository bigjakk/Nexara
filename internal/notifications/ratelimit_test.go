package notifications

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRateLimiter_AllowsBurst(t *testing.T) {
	l := newChannelRateLimiter()
	ch := uuid.New()

	for i := 0; i < ChannelRateLimitBurst; i++ {
		if !l.Allow(ch) {
			t.Fatalf("Allow %d/%d denied; expected full burst to pass", i+1, ChannelRateLimitBurst)
		}
	}
	if l.Allow(ch) {
		t.Error("Allow after burst exhausted; expected throttle")
	}
}

func TestRateLimiter_ChannelsAreIndependent(t *testing.T) {
	l := newChannelRateLimiter()
	chA := uuid.New()
	chB := uuid.New()

	for i := 0; i < ChannelRateLimitBurst; i++ {
		if !l.Allow(chA) {
			t.Fatalf("chA Allow %d denied", i+1)
		}
	}
	// chA exhausted; chB should still have a full burst.
	if !l.Allow(chB) {
		t.Error("chB rejected even though chA exhausted")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	l := newChannelRateLimiter()
	now := time.Now()
	l.now = func() time.Time { return now }

	ch := uuid.New()
	// Drain the bucket.
	for i := 0; i < ChannelRateLimitBurst; i++ {
		_ = l.Allow(ch)
	}
	if l.Allow(ch) {
		t.Fatal("expected drained bucket to deny")
	}
	// Advance time past one refill window.
	now = now.Add(time.Duration(ChannelRateLimitRefillSeconds) * time.Second)
	if !l.Allow(ch) {
		t.Error("expected one refilled token to allow")
	}
	if l.Allow(ch) {
		t.Error("expected only one refilled token, not two")
	}
}

func TestRateLimiter_NeverExceedsBurst(t *testing.T) {
	l := newChannelRateLimiter()
	now := time.Now()
	l.now = func() time.Time { return now }

	ch := uuid.New()
	// Touch once to establish the bucket.
	_ = l.Allow(ch)
	// Wait an absurdly long time — bucket should cap at burst, not unbounded.
	now = now.Add(24 * time.Hour)
	allowed := 0
	for i := 0; i < ChannelRateLimitBurst*5; i++ {
		if l.Allow(ch) {
			allowed++
		}
	}
	if allowed > ChannelRateLimitBurst {
		t.Errorf("allowed %d after long wait; cap should be %d", allowed, ChannelRateLimitBurst)
	}
	if allowed < 1 {
		t.Errorf("allowed %d after refill; expected at least 1", allowed)
	}
}

func TestRateLimiter_NilSafe(t *testing.T) {
	var l *channelRateLimiter
	if !l.Allow(uuid.New()) {
		t.Error("nil rate limiter must default to permissive")
	}
}

func TestRateLimiter_ConcurrentSafe(t *testing.T) {
	l := newChannelRateLimiter()
	ch := uuid.New()
	var wg sync.WaitGroup
	wg.Add(50)
	for i := 0; i < 50; i++ {
		go func() {
			defer wg.Done()
			_ = l.Allow(ch)
		}()
	}
	wg.Wait()
	// We just want the race detector to not flag this; the post-condition
	// is that the bucket isn't corrupted (further calls behave sanely).
	if l.Allow(uuid.New()) != true {
		t.Error("a fresh channel should still get its full burst")
	}
}
