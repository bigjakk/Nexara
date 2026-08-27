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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api"
	"github.com/bigjakk/nexara/internal/app"
	"github.com/bigjakk/nexara/internal/collector"
	"github.com/bigjakk/nexara/internal/config"
	"github.com/bigjakk/nexara/internal/db"
	"github.com/bigjakk/nexara/internal/debug"
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

	// Operator recovery CLI for the automatic startup migrations: inspect
	// state, clear a dirty flag after a failed upgrade, or step the schema
	// up/down without starting the server. See docs/installation.md
	// ("Recovering from a failed upgrade").
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrateCLI(os.Args[2:])
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

	// ---- Composition root ----
	// Every long-lived domain service is constructed once, here, and handed
	// to the API server, the scheduler, the collector, and the WS server.
	// See internal/app for the bugs the previous per-site construction caused.
	// ctx is the process shutdown context: engines root their detached
	// goroutines in it so a graceful SIGTERM cancels in-flight Proxmox/SSH
	// calls instead of orphaning them.
	application := app.New(ctx, cfg, pool, rdb, logger)
	// Tear down the Proxmox client cache's pub/sub goroutine on shutdown.
	// Belt-and-braces: ctx cancellation already exits the runSubscriber loop,
	// but Close also handles paths where ctx might be reused.
	defer application.Close()

	// ---- API server (registers /api/v1/* and /healthz) ----
	srv := api.New(application)

	// ---- WebSocket server (registers /ws/* on the API's Fiber app) ----
	queries := application.Queries
	jwtSvc := application.JWT

	hub := ws.NewHub(logger.With("component", "ws-hub"), cfg.WSMaxConnections)
	hub.Run()

	var consoleHandler *ws.ConsoleHandler
	var vncHandler *ws.VNCHandler
	if cfg.EncryptionKey != "" {
		consoleHandler = ws.NewConsoleHandler(queries, cfg.EncryptionKey, jwtSvc, logger.With("component", "console"))
		vncHandler = ws.NewVNCHandler(queries, cfg.EncryptionKey, jwtSvc, logger.With("component", "vnc"))
		// Share the process-wide Proxmox client cache so WS console/VNC
		// connections reuse the same cached *Client instance that the
		// HTTP handlers do — avoids a fresh TLS handshake every time a
		// user opens a node shell or VM console.
		if cache := application.ProxmoxCache; cache != nil {
			consoleHandler.SetProxmoxCache(cache)
			vncHandler.SetProxmoxCache(cache)
		}
	}

	wsServer := ws.NewServer(hub, jwtSvc, logger.With("component", "ws"), cfg.WSPingInterval, cfg.WSPongTimeout, ws.ServerConfig{
		ConsoleHandler: consoleHandler,
		VNCHandler:     vncHandler,
		// RBAC engine comes from the composition root, so view:cluster
		// permission lookups go through the same Redis-cached engine
		// instance the HTTP handlers use. The WS subscribe path uses it to
		// enforce per-cluster view permissions on metric / alert / event
		// channels (security review H1). If application.RBAC is nil here,
		// the WS server warns at startup and falls open on cluster channels.
		RBACEngine: application.RBAC,
		// Origin allow-list for /ws, /ws/console, /ws/vnc upgrades.
		// Empty/wildcard preserves the legacy "accept any origin"
		// behaviour and triggers a startup warning; explicit values
		// enforce CSRF defence-in-depth at the upgrade boundary.
		AllowedOrigins: ws.ParseAllowedOrigins(cfg.WSAllowedOrigins),
	})
	wsServer.RegisterRoutes(srv.App())

	// Redis subscriber for WS fan-out.
	if rdb != nil {
		subscriber := ws.NewRedisSubscriber(rdb, hub, logger.With("component", "ws-redis"))
		go subscriber.Run(ctx)
	}

	// ---- Embedded frontend (catch-all /*) ----
	// Registered last, after the API and WS routes, so real routes match
	// first. RegisterFrontend serves the SPA shell only for non-API paths;
	// unmatched /api/* requests get a JSON 404 instead of the HTML shell.
	distFS, err := fs.Sub(frontendDist, "dist")
	if err != nil {
		log.Fatalf("failed to load embedded frontend: %v", err)
	}
	srv.RegisterFrontend(distFS)

	// ---- Collector goroutine ----
	go runCollector(ctx, cfg, application, logger.With("component", "collector"))

	// ---- Scheduler goroutine ----
	go runScheduler(ctx, cfg, application, logger.With("component", "scheduler"))

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
func runCollector(ctx context.Context, cfg *config.Config, application *app.App, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("collector panic", "error", r)
		}
	}()

	queries := application.Queries
	syncer := collector.NewSyncer(queries, cfg.EncryptionKey, logger)
	syncer.SetProxmoxCache(application.ProxmoxCache)

	// The process-wide publisher — the API server attached the syslog
	// forwarder to it, so collector-written audit entries reach a configured
	// SIEM instead of being dropped by a private forwarder-less publisher.
	if application.EventPub != nil {
		syncer.SetEventPublisher(application.EventPub)
	}

	publisher := collector.NewPublisher(application.Redis, logger)
	health := collector.NewHealthMonitor(queries, publisher, logger)
	syncer.SetHealthMonitor(health)

	mc := collector.NewMetricCollector(application.Pool, publisher, logger)

	runWithLeaderRetry(ctx, application.Pool, "collector", logger, func(ctx context.Context) {
		logger.Info("collector started",
			"metrics_interval", cfg.MetricsCollectInterval,
			"resource_sync_interval", cfg.ResourceSyncInterval,
			"snapshot_sync_interval", cfg.SnapshotSyncInterval)

		ticker := cfg.NewMetricsTicker()
		defer ticker.Stop()

		// Fast inventory loop: one GET /cluster/resources per cluster per
		// tick, so the tree converges in seconds while the heavy per-node
		// enrichment stays on the slow ticker below. Tied to the leader ctx,
		// so losing leadership stops it with the rest of the collector.
		if cfg.ResourceSyncInterval > 0 {
			fastInterval := cfg.ResourceSyncInterval
			if fastInterval < 2*time.Second {
				fastInterval = 2 * time.Second
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("fast resource sync loop panicked", "panic", r)
					}
				}()
				fastTicker := time.NewTicker(fastInterval)
				defer fastTicker.Stop()
				for {
					select {
					case <-fastTicker.C:
						syncer.SyncAllResources(ctx)
					case <-ctx.Done():
						return
					}
				}
			}()
		}

		// Guest snapshot inventory loop: one snapshot listing per guest per
		// pass (no bulk endpoint exists), so it gets its own slow cadence
		// instead of riding the metrics tick. Floored at 60s; 0 disables.
		// Same leader-ctx lifetime as the fast loop above.
		if cfg.SnapshotSyncInterval > 0 {
			snapInterval := cfg.SnapshotSyncInterval
			if snapInterval < time.Minute {
				snapInterval = time.Minute
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("guest snapshot sync loop panicked", "panic", r)
					}
				}()
				// One immediate pass so the central page has data shortly
				// after boot instead of after the first full interval.
				syncer.SyncAllGuestSnapshots(ctx)
				snapTicker := time.NewTicker(snapInterval)
				defer snapTicker.Stop()
				for {
					select {
					case <-snapTicker.C:
						syncer.SyncAllGuestSnapshots(ctx)
					case <-ctx.Done():
						return
					}
				}
			}()
		}

		// Veeam inventory + session loops. Leader-gated on the same ctx as the
		// loops above, so a follower never polls a VBR server and a lost
		// leadership tears both down with everything else.
		//
		// Two cadences because the two passes cost very different things: the
		// inventory pass fans out a restore-point listing per changed backup
		// object, while a session poll is one filtered, watermarked request.
		if cfg.VeeamSyncInterval > 0 {
			veeamSyncer := collector.NewVeeamSyncer(queries, cfg.EncryptionKey, collector.VeeamSyncConfig{
				RestorePointRetention: cfg.VeeamRestorePointRetention,
				SessionRetention:      cfg.VeeamSessionRetention,
			}, logger)
			if application.EventPub != nil {
				veeamSyncer.SetEventPublisher(application.EventPub)
			}

			veeamInterval := cfg.VeeamSyncInterval
			if veeamInterval < time.Minute {
				veeamInterval = time.Minute
			}
			sessionInterval := cfg.VeeamSessionInterval
			if sessionInterval <= 0 {
				sessionInterval = veeamInterval
			}
			if sessionInterval < 30*time.Second {
				sessionInterval = 30 * time.Second
			}

			go func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("veeam inventory loop panicked", "panic", r)
					}
				}()
				// One pass immediately, so a freshly added server shows data
				// without waiting out a full interval.
				veeamSyncer.SyncInventory(ctx)
				ticker := time.NewTicker(veeamInterval)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						veeamSyncer.SyncInventory(ctx)
					case <-ctx.Done():
						return
					}
				}
			}()

			go func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("veeam session loop panicked", "panic", r)
					}
				}()
				veeamSyncer.SyncSessions(ctx)
				ticker := time.NewTicker(sessionInterval)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						veeamSyncer.SyncSessions(ctx)
					case <-ctx.Done():
						return
					}
				}
			}()
		}

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
func runScheduler(ctx context.Context, cfg *config.Config, application *app.App, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("scheduler panic", "error", r)
		}
	}()

	queries := application.Queries

	// Every engine here is the same instance the HTTP handlers act on, so a
	// manual rolling-update confirmation, an alert-DLQ replay, or an
	// API-triggered CVE scan behaves identically to its scheduled twin.
	sched := scheduler.New(scheduler.Deps{
		Queries:       queries,
		EncryptionKey: cfg.EncryptionKey,
		TaskRetention: cfg.TaskHistoryRetention,
		Logger:        logger,
		EventPub:      application.EventPub,
		Cache:         application.ProxmoxCache,
		DRSEngine:     application.DRSEngine,
		DRSExecutor:   application.DRSExecutor,
		CVEScanner:    application.CVEScanner,
		AlertEngine:   application.AlertEngine,
		ReportGen:     application.ReportGen,
		RollingOrch:   application.RollingOrch,
	})

	runWithLeaderRetry(ctx, application.Pool, "scheduler", logger, func(ctx context.Context) {
		logger.Info("scheduler started",
			"task_interval", "60s",
			"drs_interval", "60s",
			"cve_interval", "6h",
			"kev_interval", "1h",
			"alert_interval", "60s",
			"report_interval", "60s",
			"report_retention_interval", "24h",
			"task_retention_interval", "1h",
			"rolling_update_interval", "15s",
		)

		// Clean up stale DRS history entries from previous interrupted runs.
		if err := queries.CleanupStaleDRSHistory(ctx); err != nil {
			logger.Warn("failed to cleanup stale DRS history", "error", err)
		}

		// Each engine ticks in its own goroutine so one long pass (a rolling
		// drain or DRS migration can block ~30 min per task wait) cannot
		// starve the others — most critically alert evaluation, which matters
		// most during exactly those windows. The per-engine in-flight guard
		// makes overlapping ticks skip, not queue. Panic recovery lives inside
		// each Run* method.
		engine := func(name string, run func(context.Context)) func() {
			var inFlight atomic.Bool
			return func() {
				if !inFlight.CompareAndSwap(false, true) {
					logger.Debug("scheduler tick skipped: previous run still in flight", "engine", name)
					return
				}
				go func() {
					defer inFlight.Store(false)
					run(ctx)
				}()
			}
		}

		runTasks := engine("scheduled_tasks", sched.Run)
		runDRS := engine("drs", sched.RunDRS)
		runKEV := engine("kev_refresh", sched.RunKEVRefresh)
		runCVE := engine("cve_scanning", sched.RunCVEScanning)
		runAlerts := engine("alert_evaluation", sched.RunAlertEvaluation)
		runReports := engine("report_generation", sched.RunReportGeneration)
		runReportRetention := engine("report_retention", sched.RunReportRetention)
		runTaskRetention := engine("task_retention", sched.RunTaskRetention)
		runRolling := engine("rolling_updates", sched.RunRollingUpdates)
		runImports := engine("vm_import_reconcile", sched.RunVMImportReconcile)

		// Run initial checks immediately.
		runTasks()
		runDRS()
		runKEV()
		runCVE()
		runAlerts()
		runReports()
		runReportRetention()
		runTaskRetention()
		runRolling()
		runImports()

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

		taskRetentionTicker := time.NewTicker(1 * time.Hour)
		defer taskRetentionTicker.Stop()

		rollingTicker := time.NewTicker(15 * time.Second)
		defer rollingTicker.Stop()

		importTicker := time.NewTicker(15 * time.Second)
		defer importTicker.Stop()

		for {
			select {
			case <-taskTicker.C:
				runTasks()
			case <-drsTicker.C:
				runDRS()
			case <-cveTicker.C:
				runCVE()
			case <-kevTicker.C:
				runKEV()
			case <-alertTicker.C:
				runAlerts()
			case <-reportTicker.C:
				runReports()
			case <-reportRetentionTicker.C:
				runReportRetention()
			case <-taskRetentionTicker.C:
				runTaskRetention()
			case <-rollingTicker.C:
				runRolling()
			case <-importTicker.C:
				runImports()
			case <-ctx.Done():
				logger.Info("scheduler stopped")
				return
			}
		}
	})
}
