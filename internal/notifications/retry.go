package notifications

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RetrySchedule is the fixed exponential-backoff schedule for dispatcher
// retries: try, wait 1s, retry, wait 4s, retry. Three total attempts.
// Sized so a transient outage on Slack/PagerDuty/SMTP (typically <5s) is
// absorbed without operator intervention, while a persistent failure
// surfaces in the DLQ within ~5 seconds.
//
// Exposed for tests (override via withRetrySchedule). Production code uses
// the package default.
var RetrySchedule = []time.Duration{1 * time.Second, 4 * time.Second}

// retryableSend wraps a single dispatcher attempt in the retry schedule.
// Returns the final attempt count (1-based) and the last error, or nil on
// success. The send function is invoked at least once even if RetrySchedule
// is empty.
//
// retryableSend respects ctx cancellation: if ctx is cancelled mid-backoff,
// it returns the context error wrapped around the prior send error so the
// DLQ entry distinguishes "operator shut down" from "endpoint dead".
func retryableSend(
	ctx context.Context,
	send func(context.Context) error,
	schedule []time.Duration,
) (attempts int, lastErr error) {
	// First attempt always runs.
	attempts = 1
	if err := send(ctx); err == nil {
		return attempts, nil
	} else {
		lastErr = err
	}

	// Each entry in schedule gates one additional attempt.
	for _, backoff := range schedule {
		select {
		case <-ctx.Done():
			return attempts, fmt.Errorf("ctx cancelled after %d attempts: %w (last err: %v)",
				attempts, ctx.Err(), lastErr)
		case <-time.After(backoff):
		}

		attempts++
		if err := send(ctx); err == nil {
			return attempts, nil
		} else {
			lastErr = err
		}
	}

	if lastErr == nil {
		lastErr = errors.New("retryableSend: no attempts made")
	}
	return attempts, lastErr
}
