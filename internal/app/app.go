// Package app is the composition root: the one place where every long-lived
// domain service is constructed and wired. The API server, the scheduler, the
// collector, and the WebSocket server all receive these instances — none of
// them build their own.
//
// This exists because they used to. Before it, the same engine was constructed
// two to four times per process with different dependencies at each site, and
// the differences were silent bugs rather than style problems:
//
//   - the API's rolling.Orchestrator was built with a nil notification
//     registry, so a reboot failure confirmed over HTTP skipped the job's
//     configured notification channel while the identical failure on a
//     scheduler tick notified;
//   - the DRS handler built a fresh engine and executor per request and
//     dispatched migrations outside the scheduler's leader lock and interval
//     bookkeeping, racing the tick for the same guest;
//   - only the API server's events.Publisher carried the syslog forwarder, so
//     audit rows written by the scheduler and collector never reached a
//     configured SIEM while operator-initiated equivalents did;
//   - two scanner.Engine instances meant two copies of the ~80MB in-memory
//     CVE/KEV/EPSS caches, neither able to warm the other.
//
// None of those were typos. The old structure required every construction site
// to independently know every dependency an engine needed, so a site that
// didn't have one handy passed nil and compiled. Handlers can't under-wire an
// engine they don't construct.
//
// Import direction is one-way: app imports leaf domain packages only, never
// internal/api or internal/scheduler, so those can import app without a cycle.
package app

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/config"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/drs"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/notifications"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/reports"
	"github.com/bigjakk/nexara/internal/rolling"
	"github.com/bigjakk/nexara/internal/scanner"
	"github.com/bigjakk/nexara/internal/virtiowin"
)

// App holds the process-wide singletons.
//
// Every field may be nil, and the nil rules are load-bearing rather than
// defensive: Nexara boots without Redis (pub/sub and caches degrade), and the
// API server is constructed with a nil pool in tests. Consumers must nil-check
// what they use — the Has* helpers state the same conditions the fields are
// gated on.
type App struct {
	Cfg         *config.Config
	Pool        *pgxpool.Pool // nil in tests
	Queries     *db.Queries   // nil iff Pool is nil
	Redis       *redis.Client // nil when REDIS_URL is unset or unparseable
	Logger      *slog.Logger
	ShutdownCtx context.Context

	// Infrastructure.
	EventPub     *events.Publisher    // nil iff Redis is nil
	JWT          *auth.JWTService     // nil iff JWT_SECRET is empty
	SessionMgr   *auth.SessionManager // needs Queries + Redis
	RBAC         *auth.RBACEngine     // needs Queries; degrades to Postgres-only without Redis
	ProxmoxCache *proxmox.ClientCache // needs Queries + ENCRYPTION_KEY

	// Domain engines. All are gated on Queries alone — deliberately NOT on
	// ENCRYPTION_KEY. The scheduler dereferences AlertEngine with no nil
	// check, so gating these on the key would turn a keyless install into a
	// scheduler panic instead of a degraded-but-running process. Engines that
	// need the key fail per-operation when they try to decrypt.
	NotifyRegistry *notifications.Registry
	AlertEngine    *notifications.Engine
	CVEScanner     *scanner.Engine
	DRSEngine      *drs.Engine
	DRSExecutor    *drs.Executor
	RollingOrch    *rolling.Orchestrator
	ReportGen      *reports.Generator
	VirtioWin      *virtiowin.Engine
}

// New builds every singleton in dependency order. shutdownCtx is the
// process-wide context cancelled on SIGTERM; engines that spawn detached
// goroutines (DRS executor, rolling orchestrator, alert engine) root them in
// it so a graceful stop aborts in-flight Proxmox and SSH work instead of
// orphaning it.
func New(shutdownCtx context.Context, cfg *config.Config, pool *pgxpool.Pool, rdb *redis.Client, logger *slog.Logger) *App {
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}

	a := &App{
		Cfg:         cfg,
		Pool:        pool,
		Redis:       rdb,
		Logger:      logger,
		ShutdownCtx: shutdownCtx,
	}

	if pool != nil {
		a.Queries = db.New(pool)
	}

	// One publisher for the whole process. The API server attaches the syslog
	// forwarder to it after construction, which is how scheduler- and
	// collector-initiated audit rows reach a configured SIEM — they used to
	// hold their own forwarder-less publishers.
	if rdb != nil {
		a.EventPub = events.NewPublisher(rdb, logger)
	}

	if cfg.JWTSecret != "" {
		a.JWT = auth.NewJWTService(cfg.JWTSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	}
	if a.Queries != nil && rdb != nil {
		a.SessionMgr = auth.NewSessionManager(a.Queries, rdb)
	}
	if a.Queries != nil {
		// Built even when Redis is absent: the engine's read and write-back
		// paths already guard on a nil client and fall through to a straight
		// Postgres lookup. Gating on Redis would fail every authenticated
		// request when Redis is misconfigured.
		if rdb == nil {
			logger.Warn("rbac engine: Redis unavailable, permission lookups will hit Postgres on every check")
		}
		a.RBAC = auth.NewRBACEngine(a.Queries, rdb)
	}

	// Per-cluster *Client / *PBSClient memoised so the collector, scheduler,
	// scanner, rolling, and DRS flows reuse the http.Transport idle-conn pool
	// instead of paying a TLS handshake per call. The subscriber applies
	// invalidations published by peer replicas; shutdownCtx tears it down.
	if a.Queries != nil && cfg.EncryptionKey != "" {
		a.ProxmoxCache = proxmox.NewClientCache(a.Queries, cfg.EncryptionKey, rdb, logger.With("component", "proxmox-cache"))
		a.ProxmoxCache.StartSubscriber(shutdownCtx)
	}

	if a.Queries != nil {
		a.NotifyRegistry = notifications.BuildRegistry()

		a.AlertEngine = notifications.NewEngine(shutdownCtx, a.Queries,
			logger.With("component", "alert-engine"), a.EventPub, a.NotifyRegistry, cfg.EncryptionKey)

		a.CVEScanner = scanner.NewEngine(a.Queries, cfg.EncryptionKey,
			logger.With("component", "cve-scanner"), a.NotifyRegistry)

		a.DRSEngine = drs.NewEngine(a.Queries, cfg.EncryptionKey, logger.With("component", "drs-engine"))
		a.DRSExecutor = drs.NewExecutor(shutdownCtx, a.Queries, logger.With("component", "drs-executor"), a.EventPub)

		a.RollingOrch = rolling.NewOrchestrator(shutdownCtx, a.Queries, cfg.EncryptionKey,
			logger.With("component", "rolling-update"), a.EventPub, a.NotifyRegistry)
		// Refresh security posture as soon as a rolling update finishes rather
		// than waiting for the next 6-hour scan tick.
		a.RollingOrch.SetCVEScanner(a.CVEScanner)

		a.ReportGen = reports.NewGenerator(a.Queries, logger.With("component", "report-gen"))

		a.VirtioWin = virtiowin.NewEngine(a.Queries, cfg.EncryptionKey, logger.With("component", "virtio-win"))

		// Nil-safe on every setter; a keyless or DB-less install simply leaves
		// the engines building clients per call.
		a.CVEScanner.SetProxmoxCache(a.ProxmoxCache)
		a.DRSEngine.SetProxmoxCache(a.ProxmoxCache)
		a.RollingOrch.SetProxmoxCache(a.ProxmoxCache)
		a.VirtioWin.SetProxmoxCache(a.ProxmoxCache)
	}

	return a
}

// HasDB reports whether the database-backed services are wired.
func (a *App) HasDB() bool { return a != nil && a.Queries != nil }

// HasCrypto reports whether services that decrypt cluster credentials can run.
func (a *App) HasCrypto() bool { return a.HasDB() && a.Cfg != nil && a.Cfg.EncryptionKey != "" }

// Close releases the singletons that own background goroutines. Safe to call
// on a partially-constructed App.
func (a *App) Close() {
	if a == nil {
		return
	}
	if a.ProxmoxCache != nil {
		a.ProxmoxCache.Close()
	}
}
