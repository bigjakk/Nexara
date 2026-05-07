package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

	// Maintenance CLI: full integrity repair including hypertable REINDEX.
	// Long-running and AccessExclusiveLock-blocking — never invoked from the
	// normal startup path.
	if len(os.Args) > 1 && os.Args[1] == "repair-integrity" {
		runRepairIntegrity()
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
	if err := db.EnsureSchema(ctx, pool, cfg.DatabaseURL, logger); err != nil {
		log.Fatalf("failed to ensure database schema: %v", err)
	}

	// Detect and remove duplicate inventory rows (cheap, no-op in normal
	// operation). The hypertable REINDEX is intentionally NOT run here —
	// it holds AccessExclusiveLock and can take hours. Operators must invoke
	// `nexara repair-integrity` explicitly to run that path.
	if err := db.RepairIntegrity(ctx, pool, logger, db.RepairOptions{}); err != nil {
		logger.Error("integrity repair failed", "error", err)
	}

	// Construct the Redis client without blocking on connectivity. We probe
	// the connection in a background goroutine so the HTTP listener (and
	// `/healthz`, which only depends on the DB) can come up immediately.
	//
	// This makes Nexara tolerate transient orchestrator-side DNS gaps —
	// notably the Docker Swarm libnetwork resolver desync that can occur
	// during rolling updates — instead of failing healthchecks while
	// stuck inside Redis's own retry budget. go-redis v9 dials lazily on
	// the first command, so handlers and the WS subscriber will start
	// working as soon as the Redis name resolves.
	var rdb *redis.Client
	if cfg.RedisURL != "" {
		client, parseErr := redisutil.NewClientLazy(cfg.RedisURL)
		if parseErr != nil {
			logger.Warn("invalid Redis URL, continuing without Redis", "error", parseErr)
		} else {
			rdb = client
			defer rdb.Close()
			go func() {
				if err := redisutil.WaitUntilReady(ctx, rdb, logger); err != nil {
					logger.Warn("Redis not reachable; events and pub/sub will resume when it returns", "error", err)
				}
			}()
		}
	}

	// Start pprof if enabled.
	if cfg.PprofEnabled {
		debug.StartPprof(cfg.PprofPort, logger)
	}

	// ---- API server (registers /api/v1/* and /healthz) ----
	// ctx is the per-server shutdown context; the API server threads it
	// into handlers and orchestrators that spawn detached goroutines
	// (migration, DRS, rolling update) so a graceful SIGTERM cancels
	// in-flight Proxmox/SSH calls instead of orphaning them.
	srv := api.New(ctx, cfg, pool, rdb)

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
		// RBAC engine is reused from the API server so view:cluster
		// permission lookups go through the same Redis-cached engine
		// instance. The WS subscribe path uses it to enforce per-cluster
		// view permissions on metric / alert / event channels (security
		// review H1). If srv.RBACEngine() is nil here, the WS server
		// will warn at startup and fall open on cluster channels.
		RBACEngine: srv.RBACEngine(),
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

// runRepairIntegrity executes the full integrity repair, including the
// hypertable REINDEX that the normal startup path skips. Intended for
// operator-triggered maintenance windows: invoke via
// `docker exec <nexara-container> /nexara repair-integrity`.
//
// The hypertable REINDEX holds AccessExclusiveLock for the duration and
// blocks all writes to node_metrics / vm_metrics — collector ingest will
// stall until it completes.
func runRepairIntegrity() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "repair-integrity: failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.SlogLevel(),
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Surface SIGINT/SIGTERM so an operator can abort a long-running REINDEX.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig, ok := <-quit
		if !ok {
			return
		}
		logger.Warn("repair-integrity received signal, cancelling", "signal", sig)
		cancel()
	}()

	pool, err := db.ConnectWithRetry(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "repair-integrity: failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	logger.Info("starting full integrity repair (hypertable REINDEX will block writes)")
	if err := db.RepairIntegrity(ctx, pool, logger, db.RepairOptions{ReindexHypertables: true}); err != nil {
		logger.Error("integrity repair failed", "error", err)
		os.Exit(1)
	}
	logger.Info("integrity repair completed")
}

// Heartbeat-based leader election. Only one instance across the cluster
// runs the collector or scheduler at any given time; all instances serve
// API traffic. We replaced session-scoped pg_try_advisory_lock with a
// heartbeat row because a hard-killed leader's TCP session can survive on
// the Postgres side until kernel keepalives expire (~2h default), leaving
// followers unable to take over for hours. With a heartbeat row, takeover
// happens within leaderTakeoverAfter regardless of TCP state.
const (
	leaderHeartbeatInterval = 5 * time.Second
	leaderTakeoverAfter     = 30 * time.Second
	leaderRetryInterval     = 10 * time.Second
	leaderHeartbeatGrace    = 3 // consecutive heartbeat failures before stepping down
)

// tryBecomeLeader attempts to insert (or steal a stale row) the
// leader_election row for the given role. Returns true iff our holder_id
// is the current owner after the upsert. The takeover branch only fires
// when the existing row is older than leaderTakeoverAfter, so a healthy
// leader can't be displaced.
func tryBecomeLeader(ctx context.Context, pool *pgxpool.Pool, role string, holderID uuid.UUID) (bool, error) {
	var owner uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO leader_election (role, holder_id, last_heartbeat)
         VALUES ($1, $2, now())
         ON CONFLICT (role) DO UPDATE
            SET holder_id = $2, last_heartbeat = now()
            WHERE leader_election.holder_id = $2
               OR leader_election.last_heartbeat < now() - make_interval(secs => $3)
         RETURNING holder_id`,
		role, holderID, int(leaderTakeoverAfter.Seconds()),
	).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		// Existing leader is fresh — we don't own it.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return owner == holderID, nil
}

// heartbeatLeader keeps the leader_election row fresh for as long as we
// hold the role. Returns when ctx is cancelled, when a heartbeat reports
// 0 rows (we were stolen), or after leaderHeartbeatGrace consecutive
// transient failures.
func heartbeatLeader(ctx context.Context, pool *pgxpool.Pool, role string, holderID uuid.UUID, logger *slog.Logger) {
	ticker := time.NewTicker(leaderHeartbeatInterval)
	defer ticker.Stop()
	consecutiveFailures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tag, err := pool.Exec(ctx,
				`UPDATE leader_election SET last_heartbeat = now()
                 WHERE role = $1 AND holder_id = $2`,
				role, holderID,
			)
			if err != nil {
				consecutiveFailures++
				logger.Warn("leader heartbeat failed", "role", role, "consecutive_failures", consecutiveFailures, "error", err)
				if consecutiveFailures >= leaderHeartbeatGrace {
					logger.Warn("leader stepping down after repeated heartbeat failures", "role", role)
					return
				}
				continue
			}
			consecutiveFailures = 0
			if tag.RowsAffected() == 0 {
				logger.Warn("leader role lost (taken over by another instance)", "role", role)
				return
			}
		}
	}
}

// releaseLeader removes our leader row so another instance can take over
// immediately on graceful shutdown, instead of waiting leaderTakeoverAfter.
func releaseLeader(ctx context.Context, pool *pgxpool.Pool, role string, holderID uuid.UUID, logger *slog.Logger) {
	if _, err := pool.Exec(ctx,
		`DELETE FROM leader_election WHERE role = $1 AND holder_id = $2`,
		role, holderID,
	); err != nil {
		logger.Warn("leader release failed", "role", role, "error", err)
	}
}

// runWithLeaderRetry continuously attempts to become leader for a role.
// Once acquired, it runs `run` and a heartbeat goroutine concurrently;
// when either exits, both exit and the function loops back to retry.
func runWithLeaderRetry(ctx context.Context, pool *pgxpool.Pool, role string, logger *slog.Logger, run func(ctx context.Context)) {
	holderID := uuid.New()
	logger = logger.With("holder_id", holderID.String())

	for ctx.Err() == nil {
		acquired, err := tryBecomeLeader(ctx, pool, role, holderID)
		if err != nil {
			logger.Warn("leader acquire query failed", "role", role, "error", err)
		}
		if !acquired {
			select {
			case <-ctx.Done():
				return
			case <-time.After(leaderRetryInterval):
				continue
			}
		}

		logger.Info("acquired leader role", "role", role)

		runCtx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			defer cancel()
			run(runCtx)
		}()

		go func() {
			defer wg.Done()
			defer cancel()
			heartbeatLeader(runCtx, pool, role, holderID, logger)
		}()

		wg.Wait()

		// Release with a fresh context so a cancelled parent ctx
		// doesn't stop us from clearing the row on shutdown.
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		releaseLeader(releaseCtx, pool, role, holderID, logger)
		releaseCancel()

		logger.Info("released leader role", "role", role)
	}
}

// runCollector runs the metric collection loop. Uses leader election so only
// one instance across the Swarm cluster runs the collector at any time.
func runCollector(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, rdb *redis.Client, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("collector panic", "error", r)
		}
	}()

	queries := dbgen.New(pool)
	syncer := collector.NewSyncer(queries, cfg.EncryptionKey, logger)

	if rdb != nil {
		eventPub := events.NewPublisher(rdb, logger)
		syncer.SetEventPublisher(eventPub)
	}

	publisher := collector.NewPublisher(rdb, logger)
	health := collector.NewHealthMonitor(queries, publisher, logger)
	syncer.SetHealthMonitor(health)

	mc := collector.NewMetricCollector(pool, publisher, logger)

	runWithLeaderRetry(ctx, pool, "collector", logger, func(ctx context.Context) {
		logger.Info("collector started", "metrics_interval", cfg.MetricsCollectInterval)

		ticker := cfg.NewMetricsTicker()
		defer ticker.Stop()

		// Run initial sync immediately.
		results := syncer.SyncAll(ctx)
		mc.ProcessResults(ctx, results)
		pbsResults := syncer.SyncAllPBS(ctx)
		mc.ProcessPBSResults(ctx, pbsResults)

		for {
			select {
			case <-ticker.C:
				results := syncer.SyncAll(ctx)
				mc.ProcessResults(ctx, results)
				pbsResults := syncer.SyncAllPBS(ctx)
				mc.ProcessPBSResults(ctx, pbsResults)
			case <-ctx.Done():
				logger.Info("collector stopped")
				return
			}
		}
	})
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

	sched := scheduler.New(ctx, queries, cfg.EncryptionKey, logger, eventPub)

	runWithLeaderRetry(ctx, pool, "scheduler", logger, func(ctx context.Context) {
		logger.Info("scheduler started",
			"task_interval", "60s",
			"drs_interval", "60s",
			"cve_interval", "6h",
			"kev_interval", "1h",
			"alert_interval", "60s",
			"report_interval", "60s",
			"report_retention_interval", "24h",
			"rolling_update_interval", "15s",
		)

		// Clean up stale DRS history entries from previous interrupted runs.
		if err := queries.CleanupStaleDRSHistory(ctx); err != nil {
			logger.Warn("failed to cleanup stale DRS history", "error", err)
		}

		// Run initial checks immediately.
		sched.Run(ctx)
		sched.RunDRS(ctx)
		sched.RunKEVRefresh(ctx)
		sched.RunCVEScanning(ctx)
		sched.RunAlertEvaluation(ctx)
		sched.RunReportGeneration(ctx)
		sched.RunReportRetention(ctx)
		sched.RunRollingUpdates(ctx)

		taskTicker := time.NewTicker(60 * time.Second)
		defer taskTicker.Stop()

		drsTicker := time.NewTicker(60 * time.Second)
		defer drsTicker.Stop()

		cveTicker := time.NewTicker(6 * time.Hour)
		defer cveTicker.Stop()

		kevTicker := time.NewTicker(1 * time.Hour)
		defer kevTicker.Stop()

		alertTicker := time.NewTicker(60 * time.Second)
		defer alertTicker.Stop()

		reportTicker := time.NewTicker(60 * time.Second)
		defer reportTicker.Stop()

		reportRetentionTicker := time.NewTicker(24 * time.Hour)
		defer reportRetentionTicker.Stop()

		rollingTicker := time.NewTicker(15 * time.Second)
		defer rollingTicker.Stop()

		for {
			select {
			case <-taskTicker.C:
				sched.Run(ctx)
			case <-drsTicker.C:
				sched.RunDRS(ctx)
			case <-cveTicker.C:
				sched.RunCVEScanning(ctx)
			case <-kevTicker.C:
				sched.RunKEVRefresh(ctx)
			case <-alertTicker.C:
				sched.RunAlertEvaluation(ctx)
			case <-reportTicker.C:
				sched.RunReportGeneration(ctx)
			case <-reportRetentionTicker.C:
				sched.RunReportRetention(ctx)
			case <-rollingTicker.C:
				sched.RunRollingUpdates(ctx)
			case <-ctx.Done():
				logger.Info("scheduler stopped")
				return
			}
		}
	})
}
