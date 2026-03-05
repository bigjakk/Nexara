package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/proxdash/proxdash/internal/collector"
	"github.com/proxdash/proxdash/internal/config"
	db "github.com/proxdash/proxdash/internal/db/generated"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logger.Error("failed to ping database", "error", err)
		os.Exit(1)
	}

	// Redis client for publishing metrics and alerts.
	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("failed to parse Redis URL", "error", err)
		os.Exit(1)
	}
	redisClient := redis.NewClient(redisOpts)
	defer redisClient.Close()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Warn("Redis not reachable, metrics will still be collected but not published", "error", err)
	}

	queries := db.New(pool)
	syncer := collector.NewSyncer(queries, cfg.EncryptionKey, logger)

	publisher := collector.NewPublisher(redisClient, logger)
	health := collector.NewHealthMonitor(queries, publisher, logger)
	syncer.SetHealthMonitor(health)

	mc := collector.NewMetricCollector(pool, publisher, logger)

	logger.Info("ProxDash collector started",
		"metrics_interval", cfg.MetricsCollectInterval,
	)

	// Run initial sync immediately.
	results := syncer.SyncAll(ctx)
	mc.ProcessResults(ctx, results)
	pbsResults := syncer.SyncAllPBS(ctx)
	mc.ProcessPBSResults(ctx, pbsResults)

	ticker := cfg.NewMetricsTicker()
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			results := syncer.SyncAll(ctx)
			mc.ProcessResults(ctx, results)
			pbsResults := syncer.SyncAllPBS(ctx)
			mc.ProcessPBSResults(ctx, pbsResults)
		case sig := <-sigCh:
			logger.Info("received signal, shutting down", "signal", sig)
			cancel()
			return
		}
	}
}
