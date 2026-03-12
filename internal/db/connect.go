package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnectWithRetry attempts to connect to PostgreSQL, retrying with exponential
// backoff. This is necessary for orchestrators like Docker Swarm and Kubernetes
// where service startup order is not guaranteed.
func ConnectWithRetry(ctx context.Context, databaseURL string, logger *slog.Logger) (*pgxpool.Pool, error) {
	const maxAttempts = 30
	backoff := time.Second

	var pool *pgxpool.Pool
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pool, err = pgxpool.New(ctx, databaseURL)
		if err == nil {
			if pingErr := pool.Ping(ctx); pingErr == nil {
				if attempt > 1 {
					logger.Info("connected to database", "attempt", attempt)
				}
				return pool, nil
			}
			pool.Close()
			err = fmt.Errorf("ping failed: %w", err)
		}

		if attempt == maxAttempts {
			break
		}

		logger.Warn("database not ready, retrying...", "attempt", attempt, "backoff", backoff, "error", err)

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled waiting for database: %w", ctx.Err())
		case <-time.After(backoff):
		}

		if backoff < 5*time.Second {
			backoff *= 2
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
		}
	}

	return nil, fmt.Errorf("database not reachable after %d attempts: %w", maxAttempts, err)
}
