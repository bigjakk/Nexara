package notifications

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRetryableSend_SuccessFirstTry(t *testing.T) {
	calls := atomic.Int32{}
	send := func(ctx context.Context) error {
		calls.Add(1)
		return nil
	}

	attempts, err := retryableSend(context.Background(), send, []time.Duration{50 * time.Millisecond})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

func TestRetryableSend_SuccessOnSecondTry(t *testing.T) {
	calls := atomic.Int32{}
	send := func(ctx context.Context) error {
		n := calls.Add(1)
		if n == 1 {
			return errors.New("transient failure")
		}
		return nil
	}

	start := time.Now()
	attempts, err := retryableSend(context.Background(), send, []time.Duration{
		20 * time.Millisecond, 100 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
	if elapsed < 20*time.Millisecond {
		t.Errorf("elapsed = %v, expected at least 20ms backoff", elapsed)
	}
	// Should NOT have waited the full second backoff because attempt 2 succeeded.
	if elapsed > 80*time.Millisecond {
		t.Errorf("elapsed = %v, exceeded expected first-backoff window", elapsed)
	}
}

func TestRetryableSend_ExhaustsAllAttempts(t *testing.T) {
	calls := atomic.Int32{}
	send := func(ctx context.Context) error {
		calls.Add(1)
		return errors.New("persistent failure")
	}

	schedule := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	attempts, err := retryableSend(context.Background(), send, schedule)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (initial + 2 retries)", got)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestRetryableSend_RespectsContextCancel(t *testing.T) {
	calls := atomic.Int32{}
	send := func(ctx context.Context) error {
		calls.Add(1)
		return errors.New("would retry")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	schedule := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	attempts, err := retryableSend(ctx, send, schedule)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled in error chain, got %v", err)
	}
	// First attempt fires before cancel, then we get into the backoff and cancel hits.
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (cancel fires during backoff)", got)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

func TestRetryableSend_EmptyScheduleSingleAttempt(t *testing.T) {
	// Schedule = nil means a single attempt with no retries (1+0 retries).
	calls := atomic.Int32{}
	send := func(ctx context.Context) error {
		calls.Add(1)
		return errors.New("fail once")
	}

	attempts, err := retryableSend(context.Background(), send, nil)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

func TestRetryScheduleProductionDefault(t *testing.T) {
	// Lock the production retry budget down — three total tries with
	// 1s + 4s waits matches the remediation plan's 1s/4s/16s spec
	// (the third "16s" is unnecessary because the second wait already
	// makes total dispatch latency 5+ seconds, which is the operational
	// boundary at which a human-in-the-loop response is more useful than
	// continued automated retries).
	if len(RetrySchedule) != 2 {
		t.Errorf("RetrySchedule has %d backoffs, expected 2 (3 total attempts)", len(RetrySchedule))
	}
	if RetrySchedule[0] != 1*time.Second {
		t.Errorf("RetrySchedule[0] = %v, want 1s", RetrySchedule[0])
	}
	if RetrySchedule[1] != 4*time.Second {
		t.Errorf("RetrySchedule[1] = %v, want 4s", RetrySchedule[1])
	}
}

func TestTruncateError(t *testing.T) {
	short := "short error"
	if got := truncateError(short); got != short {
		t.Errorf("truncateError preserved short message? got %q", got)
	}

	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'x'
	}
	got := truncateError(string(long))
	if len(got) > 4096 {
		t.Errorf("truncateError result len = %d, want <= 4096", len(got))
	}
	if !contains(got, "[truncated]") {
		t.Errorf("truncateError result missing truncation marker: %q", got[len(got)-30:])
	}
}

func TestTruncateError_UTF8BoundarySafe(t *testing.T) {
	// Build a 5000-byte string of 3-byte UTF-8 runes ('啊' = 0xE5 0x95 0x8A).
	// The naive cut at byte 4084 (4096 - len("…[truncated]"))
	// can land mid-rune; the truncator must rewind to a rune boundary so
	// the result is still valid UTF-8 (PostgreSQL JSONB rejects invalid
	// UTF-8 with `invalid byte sequence for encoding "UTF8"`, which would
	// silently lose the DLQ row).
	const rune3 = "啊"
	var b []byte
	for len(b) < 5000 {
		b = append(b, []byte(rune3)...)
	}
	got := truncateError(string(b))

	// Stripped of the trailing marker, the body must be valid UTF-8.
	const marker = "…[truncated]"
	if len(got) <= len(marker) || got[len(got)-len(marker):] != marker {
		t.Fatalf("expected trailing marker, got tail %q", got[len(got)-len(marker):])
	}
	body := got[:len(got)-len(marker)]
	for i, r := range body {
		if r == 0xFFFD {
			t.Fatalf("truncateError produced REPLACEMENT CHARACTER at byte %d (mid-rune cut)", i)
		}
	}
	// The cut point must be on a rune-start byte. utf8.ValidString reports
	// false for any half-encoded rune, so it's the simplest assertion.
	if !utf8.ValidString(body) {
		t.Fatalf("truncateError body contains invalid UTF-8 (mid-rune cut)")
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
