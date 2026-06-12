package redisutil

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewClientLazy parses the URL and constructs a *redis.Client without
// dialing. The first command on the returned client will dial; until then
// no network I/O happens.
func NewClientLazy(redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	return redis.NewClient(opts), nil
}

// WaitUntilReady pings the client with exponential backoff until Redis
// responds or the attempt budget is exhausted. Safe to call from a
// background goroutine — the caller doesn't need to block on it.
func WaitUntilReady(ctx context.Context, client *redis.Client, logger *slog.Logger) error {
	const maxAttempts = 30
	backoff := time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if pingErr := client.Ping(ctx).Err(); pingErr == nil {
			if attempt > 1 {
				logger.Info("connected to Redis", "attempt", attempt)
			}
			return nil
		}

		if attempt == maxAttempts {
			return fmt.Errorf("redis not reachable after %d attempts", maxAttempts)
		}

		logger.Warn("Redis not ready, retrying...", "attempt", attempt, "backoff", backoff)

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled waiting for Redis: %w", ctx.Err())
		case <-time.After(backoff):
		}

		if backoff < 5*time.Second {
			backoff *= 2
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
		}
	}

	return fmt.Errorf("redis not reachable after %d attempts", maxAttempts)
}
