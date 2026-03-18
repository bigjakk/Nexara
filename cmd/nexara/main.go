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
	"path/filepath"
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
	// CLI subcommands (no full server startup needed).
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			runHealthcheck()
			return
		case "seed-export":
			runSeedExport()
			return
		case "seed-import":
			runSeedImport()
			return
		}
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Load configuration.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

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

	// Auto-import seed data on fresh databases.
	seedPath := filepath.Join(cfg.DataDir, "seed.json")
	if _, statErr := os.Stat(seedPath); statErr == nil {
		if importErr := db.ImportSeed(ctx, pool, seedPath); importErr != nil {
			logger.Warn("seed import failed", "path", seedPath, "error", importErr)
		}
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

func runSeedExport() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	ctx := context.Background()
	pool, err := db.ConnectWithRetry(ctx, cfg.DatabaseURL, slog.Default())
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	path := filepath.Join(cfg.DataDir, "seed.json")
	if len(os.Args) > 2 {
		path = os.Args[2]
	}

	log.Printf("exporting seed data to %s ...", path)
	if err := db.ExportSeed(ctx, pool, path); err != nil {
		log.Fatalf("seed export failed: %v", err)
	}
}

func runSeedImport() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	ctx := context.Background()
	pool, err := db.ConnectWithRetry(ctx, cfg.DatabaseURL, slog.Default())
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	path := filepath.Join(cfg.DataDir, "seed.json")
	if len(os.Args) > 2 {
		path = os.Args[2]
	}

	log.Printf("importing seed data from %s ...", path)
	if err := db.ImportSeed(ctx, pool, path); err != nil {
		log.Fatalf("seed import failed: %v", err)
	}
}

// Advisory lock IDs for leader election. Only one instance across the cluster
// will run the collector or scheduler at any given time; all instances serve API traffic.
const (
	lockIDCollector int64 = 0x4E585241_00000001 // "NXRA" + 1
	lockIDScheduler int64 = 0x4E585241_00000002 // "NXRA" + 2
)

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

	// collectorTick wraps a sync cycle in a transaction that holds an advisory lock.
	// If another instance already holds the lock for this tick, we skip silently.
	collectorTick := func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			logger.Error("collector tick: begin tx", "error", err)
			return
		}
		defer tx.Rollback(ctx) //nolint:errcheck // rollback is best-effort after commit

		var ok bool
		if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", lockIDCollector).Scan(&ok); err != nil {
			logger.Error("collector tick: advisory lock", "error", err)
			return
		}
		if !ok {
			return // another instance is running this tick
		}

		results := syncer.SyncAll(ctx)
		mc.ProcessResults(ctx, results)
		pbsResults := syncer.SyncAllPBS(ctx)
		mc.ProcessPBSResults(ctx, pbsResults)

		_ = tx.Commit(ctx)
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

	// schedulerTick wraps a scheduler function in a transaction-level advisory lock.
	schedulerTick := func(name string, fn func(context.Context)) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			logger.Error("scheduler tick: begin tx", "task", name, "error", err)
			return
		}
		defer tx.Rollback(ctx) //nolint:errcheck // rollback is best-effort after commit

		var ok bool
		if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", lockIDScheduler).Scan(&ok); err != nil {
			logger.Error("scheduler tick: advisory lock", "task", name, "error", err)
			return
		}
		if !ok {
			return // another instance is running this tick
		}

		fn(ctx)
		_ = tx.Commit(ctx)
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
