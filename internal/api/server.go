package api

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/changelog"
	"github.com/bigjakk/nexara/internal/config"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/notifications"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/rolling"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

// Server is the API server that holds all dependencies.
type Server struct {
	app                   *fiber.App
	config                *config.Config
	db                    *pgxpool.Pool
	queries               *db.Queries
	redis                 *redis.Client
	jwtService            *auth.JWTService
	sessionManager        *auth.SessionManager
	authHandler           *handlers.AuthHandler
	clusterHandler        *handlers.ClusterHandler
	pbsHandler            *handlers.PBSHandler
	nodeHandler           *handlers.NodeHandler
	vmHandler             *handlers.VMHandler
	containerHandler      *handlers.ContainerHandler
	storageHandler        *handlers.StorageHandler
	vmFoldersHandler      *handlers.VMFoldersHandler
	metricsHandler        *handlers.MetricsHandler
	cephHandler           *handlers.CephHandler
	backupHandler         *handlers.BackupHandler
	taskHandler           *handlers.TaskHandler
	scheduleHandler       *handlers.ScheduleHandler
	auditHandler          *handlers.AuditHandler
	drsHandler            *handlers.DRSHandler
	migrationHandler      *handlers.MigrationHandler
	networkHandler        *handlers.NetworkHandler
	rbacHandler           *handlers.RBACHandler
	userHandler           *handlers.UserHandler
	ldapHandler           *handlers.LDAPHandler
	oidcHandler           *handlers.OIDCHandler
	totpHandler           *handlers.TOTPHandler
	cveHandler            *handlers.CVEHandler
	alertHandler          *handlers.AlertHandler
	notificationDLQHandler *handlers.NotificationDLQHandler
	reportHandler         *handlers.ReportHandler
	rollingUpdateHandler  *handlers.RollingUpdateHandler
	settingsHandler       *handlers.SettingsHandler
	clusterOptionsHandler *handlers.ClusterOptionsHandler
	haHandler             *handlers.HAHandler
	poolHandler           *handlers.PoolHandler
	replicationHandler    *handlers.ReplicationHandler
	acmeHandler           *handlers.ACMEHandler
	aptRepositoryHandler  *handlers.AptRepositoryHandler
	metricServerHandler   *handlers.MetricServerHandler
	searchHandler         *handlers.SearchHandler
	apiKeyHandler         *handlers.APIKeyHandler
	apiDocsHandler        *handlers.APIDocsHandler
	changelogHandler      *handlers.ChangelogHandler
	mobileDeviceHandler   *handlers.MobileDeviceHandler
	rbacEngine            *auth.RBACEngine
	eventPub              *events.Publisher
	proxmoxCache          *proxmox.ClientCache
}

// serverDeps captures the resolved dependencies that handler factories
// pull from. Centralising them lets each factory state its requirements
// once via a `has*` helper rather than spelling out the same nil checks
// at every call site (which is what made the pre-5.11 New() body 200
// lines of repetitive `if s.queries != nil && cfg.EncryptionKey != ""`
// blocks).
type serverDeps struct {
	cfg           *config.Config
	pool          *pgxpool.Pool
	queries       *db.Queries
	rdb           *redis.Client
	eventPub      *events.Publisher
	jwt           *auth.JWTService
	sessionMgr    *auth.SessionManager
	rbacEngine    *auth.RBACEngine
	encryptionKey string
	shutdownCtx   context.Context
}

// hasDB reports whether the queries struct is wired (DB pool present).
func (d *serverDeps) hasDB() bool { return d.queries != nil }

// hasCrypto reports whether handlers that decrypt cluster credentials
// can be safely constructed (queries + EncryptionKey).
func (d *serverDeps) hasCrypto() bool { return d.queries != nil && d.encryptionKey != "" }

// hasRBAC reports whether RBAC-aware handlers (LDAP, OIDC, RBAC admin,
// User admin) can be safely constructed.
func (d *serverDeps) hasRBAC() bool { return d.queries != nil && d.rbacEngine != nil }

// hasFullSecure reports whether OIDC / TOTP can be constructed: those
// require Redis for state-token / replay-attempt storage on top of
// crypto + RBAC.
func (d *serverDeps) hasFullSecure() bool { return d.hasCrypto() && d.rdb != nil }

// New creates a new API server with the given dependencies. shutdownCtx is
// the per-server context cancelled on SIGTERM; it's passed to handlers and
// orchestrators that need to launch detached goroutines (migrations, DRS,
// rolling updates) so those goroutines abort cleanly on graceful shutdown
// instead of orphaning their work past the lifetime of the process.
//
// Construction order matters in three places (call them out so future
// edits don't accidentally reorder past a hidden dependency):
//   1. eventPub must be built before any handler so handlers receive
//      a non-nil publisher.
//   2. rbacEngine must be built before authHandler so auth can call it
//      directly for permission lookups.
//   3. apiDocsHandler.SetApp must run AFTER setupRoutes so app.GetRoutes()
//      returns the populated route table.
//
// Outside those three constraints, the per-domain factory functions
// (registerAuth, registerInventory, …) can be reordered without ill effect.
func New(shutdownCtx context.Context, cfg *config.Config, pool *pgxpool.Pool, rdb *redis.Client) *Server {
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}

	d := &serverDeps{
		cfg:           cfg,
		pool:          pool,
		rdb:           rdb,
		encryptionKey: cfg.EncryptionKey,
		shutdownCtx:   shutdownCtx,
	}
	if pool != nil {
		d.queries = db.New(pool)
	}
	if rdb != nil {
		d.eventPub = events.NewPublisher(rdb, slog.Default())
	}

	s := &Server{config: cfg, db: pool, redis: rdb, queries: d.queries, eventPub: d.eventPub}

	s.registerInfra(d)
	s.registerAuth(d)
	s.registerInventory(d)
	s.registerOps(d)
	s.registerSecurity(d)
	s.registerSettingsAndKeys(d)
	s.wireAuthCompositions()

	s.app = fiber.New(buildFiberConfig(cfg))
	s.setupMiddleware()
	s.setupRoutes()

	// API docs must be wired AFTER setupRoutes — it walks app.GetRoutes()
	// at request time, which is empty before route registration.
	if s.apiDocsHandler != nil {
		s.apiDocsHandler.SetApp(s.app)
	}

	return s
}

// buildFiberConfig assembles the Fiber-side config struct. EnableTrustedProxyCheck=true with
// empty TrustedProxies means Fiber IGNORES the header for everyone — the safe default for
// direct-to-internet deployments. When TRUSTED_PROXIES names the reverse proxy
// (e.g. "127.0.0.1,10.0.0.0/8"), c.IP() returns the real client IP from X-Forwarded-For, which
// is what the auth/general/refresh/ws-token rate limiters key on (Finding #13).
// EnableIPValidation returns just the first valid IP from the header rather than the raw
// comma-separated list, so a malicious downstream can't craft a header that lands in an
// oddly-keyed bucket.
func buildFiberConfig(cfg *config.Config) fiber.Config {
	return fiber.Config{
		ErrorHandler:                 errorHandler,
		DisableStartupMessage:        true,
		BodyLimit:                    32 * 1024 * 1024, // 32MB — bodies above this are streamed, not buffered
		StreamRequestBody:            true,             // Enable streaming for large uploads (ISO/vztmpl)
		DisablePreParseMultipartForm: true,             // Don't buffer multipart bodies; upload handler parses the stream itself
		ProxyHeader:                  cfg.ProxyHeader,
		EnableTrustedProxyCheck:      true,
		TrustedProxies:               cfg.TrustedProxies,
		EnableIPValidation:           true,
	}
}

// registerInfra constructs the cross-cutting services that downstream
// handlers depend on: the Proxmox client cache, the JWT service, the
// session manager, the RBAC engine, and the syslog forwarder.
func (s *Server) registerInfra(d *serverDeps) {
	// Proxmox client cache: per-cluster *Client / *PBSClient memoised
	// so the per-tick collector/scheduler/scanner/rolling/drs flows
	// reuse the http.Transport idle-conn pool instead of paying TLS
	// handshake + AES key schedule on every call. Subscriber starts
	// immediately so Redis pub/sub invalidations from peer replicas
	// land while the process is alive; ctx cancellation tears it down
	// on SIGTERM.
	if d.hasCrypto() {
		s.proxmoxCache = proxmox.NewClientCache(d.queries, d.encryptionKey, d.rdb, slog.Default().With("component", "proxmox-cache"))
		s.proxmoxCache.StartSubscriber(d.shutdownCtx)
	}

	if d.cfg.JWTSecret != "" {
		s.jwtService = auth.NewJWTService(d.cfg.JWTSecret, d.cfg.AccessTokenTTL, d.cfg.RefreshTokenTTL)
		d.jwt = s.jwtService
	}

	if d.hasDB() && d.rdb != nil {
		s.sessionManager = auth.NewSessionManager(d.queries, d.rdb)
		d.sessionMgr = s.sessionManager
	}

	// rdb may be nil (REDIS_URL unset / parse failure / connection
	// rejected at startup); the engine's read + write-back paths
	// already guard `if e.redis != nil` so it falls through to a
	// straight Postgres lookup. The 5.1 helper-side fail-loud requires
	// the engine itself to be present, so don't gate construction on
	// Redis — that would brick every authenticated request when Redis
	// is misconfigured.
	if d.hasDB() {
		if d.rdb == nil {
			slog.Default().Warn("rbac engine: Redis unavailable, permission lookups will hit Postgres on every check")
		}
		s.rbacEngine = auth.NewRBACEngine(d.queries, d.rdb)
		d.rbacEngine = s.rbacEngine
	}

	// Syslog forwarder is attached to the event publisher and runs
	// best-effort; failure to load config doesn't block the server.
	if s.eventPub != nil {
		fwd := proxsyslog.NewForwarder(slog.Default().With("component", "syslog"))
		s.eventPub.SetSyslogForwarder(fwd)
		if d.hasDB() {
			s.loadSyslogConfig(fwd)
		}
	}
}

// registerAuth builds the user-authentication surface (login / refresh /
// LDAP / OIDC / TOTP). registerInfra must run first.
func (s *Server) registerAuth(d *serverDeps) {
	if d.hasDB() && d.jwt != nil && d.sessionMgr != nil {
		s.authHandler = handlers.NewAuthHandler(d.pool, d.queries, d.jwt, d.sessionMgr, d.rbacEngine)
	}
	if d.hasRBAC() && d.encryptionKey != "" {
		s.ldapHandler = handlers.NewLDAPHandler(d.queries, d.encryptionKey, d.rbacEngine, d.eventPub)
	}
	if d.hasFullSecure() && d.rbacEngine != nil {
		s.oidcHandler = handlers.NewOIDCHandler(d.queries, d.encryptionKey, d.rbacEngine, d.eventPub, d.rdb)
	}
	if d.hasFullSecure() {
		s.totpHandler = handlers.NewTOTPHandler(d.queries, d.encryptionKey, d.rdb, d.eventPub)
	}
}

// registerInventory builds the read/write surface for clusters, PBS,
// VMs/containers, nodes, storage, Ceph, and backup snapshots — the
// inventory layer the SPA browses through.
func (s *Server) registerInventory(d *serverDeps) {
	if d.hasCrypto() {
		s.clusterHandler = handlers.NewClusterHandler(d.queries, d.encryptionKey, d.eventPub)
		s.pbsHandler = handlers.NewPBSHandler(d.queries, d.encryptionKey, d.eventPub)
		s.vmHandler = handlers.NewVMHandler(d.queries, d.encryptionKey, d.eventPub)
		s.containerHandler = handlers.NewContainerHandler(d.queries, d.encryptionKey, d.eventPub)
		s.nodeHandler = handlers.NewNodeHandler(d.queries, d.encryptionKey, d.eventPub)
		s.storageHandler = handlers.NewStorageHandler(d.queries, d.encryptionKey, d.eventPub)
		s.cephHandler = handlers.NewCephHandler(d.queries, d.encryptionKey, d.eventPub)
		s.backupHandler = handlers.NewBackupHandler(d.queries, d.encryptionKey, d.eventPub)
	}
	if d.hasDB() {
		s.metricsHandler = handlers.NewMetricsHandler(d.queries)
		s.taskHandler = handlers.NewTaskHandler(d.queries, d.eventPub, d.cfg.TaskHistoryRetention)
		s.scheduleHandler = handlers.NewScheduleHandler(d.queries, d.eventPub)
		s.auditHandler = handlers.NewAuditHandler(d.queries, d.eventPub)
		s.vmFoldersHandler = handlers.NewVMFoldersHandler(d.queries, d.eventPub)
	}
}

// registerOps builds the orchestration surface — DRS, migration jobs,
// network management, and (further below in registerSecurity) rolling
// updates. These handlers spin up detached goroutines that must honour
// shutdownCtx, so they receive it explicitly.
func (s *Server) registerOps(d *serverDeps) {
	if d.hasCrypto() {
		s.drsHandler = handlers.NewDRSHandler(d.shutdownCtx, d.queries, d.encryptionKey, d.eventPub)
		s.migrationHandler = handlers.NewMigrationHandler(d.shutdownCtx, d.queries, d.encryptionKey, d.eventPub)
		s.networkHandler = handlers.NewNetworkHandler(d.queries, d.encryptionKey, d.eventPub)
	}
	if d.hasRBAC() {
		s.rbacHandler = handlers.NewRBACHandler(d.queries, d.rbacEngine, d.eventPub)
		s.userHandler = handlers.NewUserHandler(d.queries, d.rbacEngine, d.eventPub, d.sessionMgr)
	}
}

// registerSecurity builds the security/automation surface: CVE
// scanning, alert evaluation + DLQ replay, scheduled reports, rolling
// updates, cluster-options/HA/pools/replication/ACME/apt-repo/metric-
// server admin, and global search.
func (s *Server) registerSecurity(d *serverDeps) {
	if !d.hasCrypto() {
		return
	}
	registry := notifications.BuildRegistry(d.queries)
	s.cveHandler = handlers.NewCVEHandler(d.pool, d.queries, d.encryptionKey, d.eventPub, registry)
	s.alertHandler = handlers.NewAlertHandler(d.queries, d.encryptionKey, d.eventPub, registry)
	// The DLQ handler shares an alert engine instance with the
	// scheduler's evaluator. The replay path delegates back to that
	// engine so the same retry schedule applies whether the
	// notification was triggered automatically or by an operator.
	alertEngine := notifications.NewEngine(d.shutdownCtx, d.queries, slog.Default().With("component", "alert-engine-replay"), d.eventPub, registry, d.encryptionKey)
	s.notificationDLQHandler = handlers.NewNotificationDLQHandler(d.queries, alertEngine, d.eventPub)
	s.reportHandler = handlers.NewReportHandler(d.queries, d.encryptionKey, d.eventPub)
	rollingOrch := rolling.NewOrchestrator(d.shutdownCtx, d.queries, d.encryptionKey, slog.Default().With("component", "rolling-update"), d.eventPub, nil)
	s.rollingUpdateHandler = handlers.NewRollingUpdateHandler(d.queries, d.encryptionKey, d.eventPub, rollingOrch)
	s.clusterOptionsHandler = handlers.NewClusterOptionsHandler(d.queries, d.encryptionKey, d.eventPub)
	s.haHandler = handlers.NewHAHandler(d.queries, d.encryptionKey, d.eventPub)
	s.poolHandler = handlers.NewPoolHandler(d.queries, d.encryptionKey, d.eventPub)
	s.replicationHandler = handlers.NewReplicationHandler(d.queries, d.encryptionKey, d.eventPub)
	s.acmeHandler = handlers.NewACMEHandler(d.queries, d.encryptionKey, d.eventPub)
	s.aptRepositoryHandler = handlers.NewAptRepositoryHandler(d.queries, d.encryptionKey, d.eventPub)
	s.metricServerHandler = handlers.NewMetricServerHandler(d.queries, d.encryptionKey, d.eventPub)
	s.searchHandler = handlers.NewSearchHandler(d.queries, d.encryptionKey, d.eventPub)
}

// registerSettingsAndKeys builds the small surface that doesn't fit
// neatly elsewhere: settings, API keys, mobile devices, the
// auto-generated API docs, and the changelog.
func (s *Server) registerSettingsAndKeys(d *serverDeps) {
	if d.hasDB() {
		s.settingsHandler = handlers.NewSettingsHandler(d.queries, d.cfg.DataDir)
		s.apiKeyHandler = handlers.NewAPIKeyHandler(d.queries, d.eventPub)
		s.mobileDeviceHandler = handlers.NewMobileDeviceHandler(d.queries, d.eventPub)
	}
	s.apiDocsHandler = handlers.NewAPIDocsHandler()

	// Changelog service: fetches release notes from GitHub. Repo can be
	// overridden by CHANGELOG_REPO env var; defaults to upstream Nexara.
	s.changelogHandler = handlers.NewChangelogHandler(
		changelog.New(d.cfg.ChangelogRepo, slog.Default().With("component", "changelog")),
	)
}

// wireAuthCompositions hooks the LDAP/OIDC/TOTP handlers into the auth
// handler so login dispatches through them. Each branch is independent
// — partial wiring is fine when a backend isn't configured.
func (s *Server) wireAuthCompositions() {
	if s.authHandler != nil && s.ldapHandler != nil {
		s.authHandler.SetLDAPHandler(s.ldapHandler)
	}
	if s.authHandler != nil && s.oidcHandler != nil {
		s.authHandler.SetOIDCHandler(s.oidcHandler)
	}
	if s.authHandler != nil && s.totpHandler != nil {
		s.authHandler.SetTOTPHandler(s.totpHandler)
		s.totpHandler.SetIssueTokensFn(s.authHandler.IssueTokens)
	}
}

// loadSyslogConfig reads syslog forwarding config from settings and configures the forwarder.
func (s *Server) loadSyslogConfig(fwd *proxsyslog.Forwarder) {
	setting, err := s.queries.GetSetting(context.Background(), db.GetSettingParams{
		Key:   "syslog_forwarding",
		Scope: "global",
	})
	if err != nil {
		return // no config saved yet — forwarder stays disabled
	}

	var cfg proxsyslog.Config
	if err := json.Unmarshal(setting.Value, &cfg); err != nil {
		slog.Warn("syslog: invalid config in settings", "error", err)
		return
	}

	if err := fwd.Configure(cfg); err != nil {
		slog.Warn("syslog: failed to configure forwarder on startup", "error", err)
	}
}

// Listen starts the HTTP server on the given address.
func (s *Server) Listen(addr string) error {
	return s.app.Listen(addr)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	return s.app.Shutdown()
}

// App returns the underlying Fiber app for testing.
func (s *Server) App() *fiber.App {
	return s.app
}

// RBACEngine returns the API server's RBAC engine so other components
// in the unified binary (e.g. the WebSocket server) can perform their
// own permission checks against the same engine instance. May be nil
// if the API server was constructed without a database or Redis.
//
// The WebSocket server uses this for the subscribe-time view:cluster
// check (security review H1) — without it, any authenticated user
// could subscribe to any cluster channel and stream cross-tenant
// metric / event / alert data.
func (s *Server) RBACEngine() *auth.RBACEngine {
	return s.rbacEngine
}

// ProxmoxCache returns the per-server Proxmox client cache. May be nil
// when the API server was constructed without an encryption key. The WS
// console/VNC handlers and the background collectors/orchestrators read
// this so a single cache instance is shared across every call site.
func (s *Server) ProxmoxCache() *proxmox.ClientCache {
	return s.proxmoxCache
}
