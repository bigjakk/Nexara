package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bigjakk/nexara/internal/collector"
	"github.com/bigjakk/nexara/internal/config"
	"github.com/bigjakk/nexara/internal/db"
	"github.com/bigjakk/nexara/internal/debug"
	dbgen "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/pkg/redisutil"
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

	pool, err := db.ConnectWithRetry(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	logger.Info("connected to database")

	// Redis client for publishing metrics and alerts.
	redisClient, err := redisutil.ConnectWithRetry(ctx, cfg.RedisURL, logger)
	if err != nil {
		logger.Warn("Redis not reachable, metrics will still be collected but not published", "error", err)
	} else {
		defer redisClient.Close()
	}

	// Start pprof if enabled.
	if cfg.PprofEnabled {
		debug.StartPprof(cfg.PprofPort, logger)
	}

	queries := dbgen.New(pool)
	syncer := collector.NewSyncer(queries, cfg.EncryptionKey, logger)

	// Set up event publisher for VM status change notifications.
	eventPub := events.NewPublisher(redisClient, logger)
	syncer.SetEventPublisher(eventPub)

	publisher := collector.NewPublisher(redisClient, logger)
	health := collector.NewHealthMonitor(queries, publisher, logger)
	syncer.SetHealthMonitor(health)

	mc := collector.NewMetricCollector(pool, publisher, logger)

	logger.Info("Nexara collector started",
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
