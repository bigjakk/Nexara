package redisutil

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// ConnectWithRetry attempts to connect to Redis, retrying with exponential
// backoff. This is necessary for orchestrators like Docker Swarm and Kubernetes
// where service startup order is not guaranteed.
func ConnectWithRetry(ctx context.Context, redisURL string, logger *slog.Logger) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}

	const maxAttempts = 30
	backoff := time.Second
	client := redis.NewClient(opts)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if pingErr := client.Ping(ctx).Err(); pingErr == nil {
			if attempt > 1 {
				logger.Info("connected to Redis", "attempt", attempt)
			}
			return client, nil
		}

		if attempt == maxAttempts {
			client.Close()
			return nil, fmt.Errorf("Redis not reachable after %d attempts", maxAttempts)
		}

		logger.Warn("Redis not ready, retrying...", "attempt", attempt, "backoff", backoff)

		select {
		case <-ctx.Done():
			client.Close()
			return nil, fmt.Errorf("context cancelled waiting for Redis: %w", ctx.Err())
		case <-time.After(backoff):
		}

		if backoff < 5*time.Second {
			backoff *= 2
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
		}
	}

	return nil, fmt.Errorf("Redis not reachable after %d attempts", maxAttempts)
}
