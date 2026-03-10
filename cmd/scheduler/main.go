package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/config"
	"github.com/bigjakk/nexara/internal/debug"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/scheduler"
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

	queries := db.New(pool)

	// Connect to Redis for event publishing.
	var eventPub *events.Publisher
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			logger.Error("failed to parse Redis URL", "error", err)
			os.Exit(1)
		}
		rdb := redis.NewClient(opts)
		if err := rdb.Ping(ctx).Err(); err != nil {
			logger.Warn("Redis unavailable, events disabled", "error", err)
		} else {
			eventPub = events.NewPublisher(rdb, logger.With("component", "events"))
			defer rdb.Close()
		}
	}

	// Start pprof if enabled.
	if cfg.PprofEnabled {
		debug.StartPprof(cfg.PprofPort, logger)
	}

	sched := scheduler.New(queries, cfg.EncryptionKey, logger, eventPub)

	logger.Info("Nexara scheduler started", "task_interval", "60s", "drs_interval", "60s", "cve_interval", "6h", "alert_interval", "60s", "report_interval", "60s", "rolling_update_interval", "15s")

	// Clean up stale DRS history entries from previous interrupted runs.
	if err := queries.CleanupStaleDRSHistory(ctx); err != nil {
		logger.Warn("failed to cleanup stale DRS history", "error", err)
	}

	// Run initial checks immediately.
	sched.Run(ctx)
	sched.RunDRS(ctx)
	sched.RunCVEScanning(ctx)
	sched.RunAlertEvaluation(ctx)
	sched.RunReportGeneration(ctx)
	sched.RunRollingUpdates(ctx)

	taskTicker := time.NewTicker(60 * time.Second)
	defer taskTicker.Stop()

	drsTicker := time.NewTicker(60 * time.Second)
	defer drsTicker.Stop()

	cveTicker := time.NewTicker(6 * time.Hour)
	defer cveTicker.Stop()

	alertTicker := time.NewTicker(60 * time.Second)
	defer alertTicker.Stop()

	reportTicker := time.NewTicker(60 * time.Second)
	defer reportTicker.Stop()

	rollingTicker := time.NewTicker(15 * time.Second)
	defer rollingTicker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-taskTicker.C:
			sched.Run(ctx)
		case <-drsTicker.C:
			sched.RunDRS(ctx)
		case <-cveTicker.C:
			sched.RunCVEScanning(ctx)
		case <-alertTicker.C:
			sched.RunAlertEvaluation(ctx)
		case <-reportTicker.C:
			sched.RunReportGeneration(ctx)
		case <-rollingTicker.C:
			sched.RunRollingUpdates(ctx)
		case sig := <-sigCh:
			logger.Info("received signal, shutting down", "signal", sig)
			cancel()
			return
		}
	}
}
