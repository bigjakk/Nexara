package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api"
	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/collector"
	"github.com/bigjakk/nexara/internal/config"
	"github.com/bigjakk/nexara/internal/db"
	"github.com/bigjakk/nexara/internal/debug"
	dbgen "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/scheduler"
	"github.com/bigjakk/nexara/internal/ws"
	"github.com/bigjakk/nexara/pkg/redisutil"
)

func main() {
	// Healthcheck CLI mode for Docker HEALTHCHECK.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		runHealthcheck()
		return
	}

	// Load configuration first so we can apply LOG_LEVEL to the logger.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.SlogLevel(),
	}))
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to PostgreSQL with retry.
	pool, err := db.ConnectWithRetry(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()
	logger.Info("connected to database")

	// Run embedded schema migrations.
	if err := db.EnsureSchema(ctx, pool); err != nil {
		log.Fatalf("failed to ensure database schema: %v", err)
	}

	// Detect and fix data integrity issues (duplicate rows from index corruption
	// or concurrent instances). Safe to run on every startup.
	if err := db.RepairIntegrity(ctx, pool, logger); err != nil {
		logger.Error("integrity repair failed", "error", err)
	}

	// Connect to Redis with retry.
	var rdb *redis.Client
	if cfg.RedisURL != "" {
		rdb, err = redisutil.ConnectWithRetry(ctx, cfg.RedisURL, logger)
		if err != nil {
			logger.Warn("Redis unavailable, continuing without it", "error", err)
			rdb = nil
		} else {
			logger.Info("connected to Redis")
			defer rdb.Close()
		}
	}

	// Start pprof if enabled.
	if cfg.PprofEnabled {
		debug.StartPprof(cfg.PprofPort, logger)
	}

	// ---- API server (registers /api/v1/* and /healthz) ----
	srv := api.New(cfg, pool, rdb)

	// ---- WebSocket server (registers /ws/* on the API's Fiber app) ----
	queries := dbgen.New(pool)
	jwtSvc := auth.NewJWTService(cfg.JWTSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)

	hub := ws.NewHub(logger.With("component", "ws-hub"), cfg.WSMaxConnections)
	hub.Run()

	var consoleHandler *ws.ConsoleHandler
	var vncHandler *ws.VNCHandler
	if cfg.EncryptionKey != "" {
		consoleHandler = ws.NewConsoleHandler(queries, cfg.EncryptionKey, jwtSvc, logger.With("component", "console"))
		vncHandler = ws.NewVNCHandler(queries, cfg.EncryptionKey, jwtSvc, logger.With("component", "vnc"))
	}

	wsServer := ws.NewServer(hub, jwtSvc, logger.With("component", "ws"), cfg.WSPingInterval, cfg.WSPongTimeout, ws.ServerConfig{
		ConsoleHandler: consoleHandler,
		VNCHandler:     vncHandler,
	})
	wsServer.RegisterRoutes(srv.App())

	// Redis subscriber for WS fan-out.
	if rdb != nil {
		subscriber := ws.NewRedisSubscriber(rdb, hub, logger.With("component", "ws-redis"))
		go subscriber.Run(ctx)
	}

	// ---- Embedded frontend (catch-all /*) ----
	distFS, err := fs.Sub(frontendDist, "dist")
	if err != nil {
		log.Fatalf("failed to load embedded frontend: %v", err)
	}
	srv.App().Use("/", filesystem.New(filesystem.Config{
		Root:         http.FS(distFS),
		Browse:       false,
		NotFoundFile: "index.html", // SPA fallback
	}))

	// ---- Collector goroutine ----
	go runCollector(ctx, cfg, pool, rdb, logger.With("component", "collector"))

	// ---- Scheduler goroutine ----
	go runScheduler(ctx, cfg, pool, rdb, logger.With("component", "scheduler"))

	// ---- Start server ----
	addr := fmt.Sprintf(":%d", cfg.APIPort)
	go func() {
		logger.Info("Nexara unified server starting", "addr", addr)
		if listenErr := srv.Listen(addr); listenErr != nil {
			log.Fatalf("server error: %v", listenErr)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	logger.Info("received signal, shutting down", "signal", sig)
	cancel()
	hub.Stop()
	if shutdownErr := srv.Shutdown(); shutdownErr != nil {
		logger.Error("server shutdown error", "error", shutdownErr)
	}
	logger.Info("Nexara stopped")
}

func runHealthcheck() {
	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/healthz", port)) //nolint:gosec // localhost health check
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck failed: status %d\n", resp.StatusCode)
		os.Exit(1)
	}
	fmt.Println("ok")
}

// Advisory lock IDs for leader election. Only one instance across the cluster
// will run the collector or scheduler at any given time; all instances serve API traffic.
const (
	lockIDCollector int64 = 0x4E585241_00000001 // "NXRA" + 1
	lockIDScheduler int64 = 0x4E585241_00000002 // "NXRA" + 2
)

// withAdvisoryLock acquires a session-level advisory lock on a dedicated
// connection, runs fn, then releases the lock. If another instance holds the
// lock the function is skipped silently. Session-level locks are automatically
// released by PostgreSQL if the connection drops, preventing the stuck-lock
// problem that transaction-scoped locks suffer from in cross-host Docker
// networking (where a transaction can become "idle in transaction" forever).
func withAdvisoryLock(ctx context.Context, pool *pgxpool.Pool, lockID int64, fn func()) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&ok); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	if !ok {
		return nil // another instance holds the lock
	}
	defer func() {
		// Use a short independent context so unlock succeeds even if ctx is cancelled.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", lockID)
	}()

	fn()
	return nil
}

// runCollector runs the metric collection loop (mirrors cmd/collector logic).
func runCollector(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, rdb *redis.Client, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("collector panic", "error", r)
		}
	}()

	queries := dbgen.New(pool)
	syncer := collector.NewSyncer(queries, cfg.EncryptionKey, logger)

	// Set up event publisher for VM status change notifications.
	if rdb != nil {
		eventPub := events.NewPublisher(rdb, logger)
		syncer.SetEventPublisher(eventPub)
	}

	publisher := collector.NewPublisher(rdb, logger)
	health := collector.NewHealthMonitor(queries, publisher, logger)
	syncer.SetHealthMonitor(health)

	mc := collector.NewMetricCollector(pool, publisher, logger)

	logger.Info("collector started", "metrics_interval", cfg.MetricsCollectInterval)

	ticker := cfg.NewMetricsTicker()
	defer ticker.Stop()

	// collectorTick runs a sync cycle under a session-level advisory lock.
	// If another instance already holds the lock, we skip silently.
	collectorTick := func() {
		if err := withAdvisoryLock(ctx, pool, lockIDCollector, func() {
			results := syncer.SyncAll(ctx)
			mc.ProcessResults(ctx, results)
			pbsResults := syncer.SyncAllPBS(ctx)
			mc.ProcessPBSResults(ctx, pbsResults)
		}); err != nil {
			logger.Error("collector tick: lock", "error", err)
		}
	}

	// Run initial sync immediately.
	collectorTick()

	for {
		select {
		case <-ticker.C:
			collectorTick()
		case <-ctx.Done():
			logger.Info("collector stopped")
			return
		}
	}
}

// runScheduler runs all scheduler tickers (mirrors cmd/scheduler logic).
func runScheduler(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, rdb *redis.Client, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("scheduler panic", "error", r)
		}
	}()

	queries := dbgen.New(pool)

	var eventPub *events.Publisher
	if rdb != nil {
		eventPub = events.NewPublisher(rdb, logger.With("component", "events"))
	}

	sched := scheduler.New(queries, cfg.EncryptionKey, logger, eventPub)

	logger.Info("scheduler started",
		"task_interval", "60s",
		"drs_interval", "60s",
		"cve_interval", "6h",
		"alert_interval", "60s",
		"report_interval", "60s",
		"rolling_update_interval", "15s",
	)

	// Clean up stale DRS history entries from previous interrupted runs.
	if err := queries.CleanupStaleDRSHistory(ctx); err != nil {
		logger.Warn("failed to cleanup stale DRS history", "error", err)
	}

	// schedulerTick runs a scheduler function under a session-level advisory lock.
	schedulerTick := func(name string, fn func(context.Context)) {
		if err := withAdvisoryLock(ctx, pool, lockIDScheduler, func() {
			fn(ctx)
		}); err != nil {
			logger.Error("scheduler tick: lock", "task", name, "error", err)
		}
	}

	// Run initial checks immediately.
	schedulerTick("tasks", sched.Run)
	schedulerTick("drs", sched.RunDRS)
	schedulerTick("cve", sched.RunCVEScanning)
	schedulerTick("alerts", sched.RunAlertEvaluation)
	schedulerTick("reports", sched.RunReportGeneration)
	schedulerTick("rolling", sched.RunRollingUpdates)

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

	for {
		select {
		case <-taskTicker.C:
			schedulerTick("tasks", sched.Run)
		case <-drsTicker.C:
			schedulerTick("drs", sched.RunDRS)
		case <-cveTicker.C:
			schedulerTick("cve", sched.RunCVEScanning)
		case <-alertTicker.C:
			schedulerTick("alerts", sched.RunAlertEvaluation)
		case <-reportTicker.C:
			schedulerTick("reports", sched.RunReportGeneration)
		case <-rollingTicker.C:
			schedulerTick("rolling", sched.RunRollingUpdates)
		case <-ctx.Done():
			logger.Info("scheduler stopped")
			return
		}
	}
}
