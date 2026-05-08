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

// New creates a new API server with the given dependencies. shutdownCtx is
// the per-server context cancelled on SIGTERM; it's passed to handlers and
// orchestrators that need to launch detached goroutines (migrations, DRS,
// rolling updates) so those goroutines abort cleanly on graceful shutdown
// instead of orphaning their work past the lifetime of the process.
func New(shutdownCtx context.Context, cfg *config.Config, pool *pgxpool.Pool, rdb *redis.Client) *Server {
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}
	s := &Server{
		config: cfg,
		db:     pool,
		redis:  rdb,
	}

	if pool != nil {
		s.queries = db.New(pool)
	}

	if rdb != nil {
		s.eventPub = events.NewPublisher(rdb, slog.Default())
	}

	// Proxmox client cache: per-cluster *Client / *PBSClient memoised so
	// the per-tick collector/scheduler/scanner/rolling/drs flows reuse
	// the http.Transport idle-conn pool instead of paying TLS handshake
	// + AES key schedule on every call. Subscriber starts immediately
	// so Redis pub/sub invalidations from peer replicas land while the
	// process is alive; ctx cancellation tears it down on SIGTERM.
	if s.queries != nil && cfg.EncryptionKey != "" {
		s.proxmoxCache = proxmox.NewClientCache(s.queries, cfg.EncryptionKey, rdb, slog.Default().With("component", "proxmox-cache"))
		s.proxmoxCache.StartSubscriber(shutdownCtx)
	}

	// Initialize auth services when dependencies are available.
	if cfg.JWTSecret != "" {
		s.jwtService = auth.NewJWTService(cfg.JWTSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	}

	if s.queries != nil && rdb != nil {
		s.sessionManager = auth.NewSessionManager(s.queries, rdb)
	}

	if s.queries != nil && rdb != nil {
		s.rbacEngine = auth.NewRBACEngine(s.queries, rdb)
	}

	if s.queries != nil && s.jwtService != nil && s.sessionManager != nil {
		s.authHandler = handlers.NewAuthHandler(pool, s.queries, s.jwtService, s.sessionManager, s.rbacEngine)
	}

	if s.queries != nil && cfg.EncryptionKey != "" {
		s.clusterHandler = handlers.NewClusterHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.pbsHandler = handlers.NewPBSHandler(s.queries, cfg.EncryptionKey, s.eventPub)
	}

	if s.queries != nil && cfg.EncryptionKey != "" {
		s.vmHandler = handlers.NewVMHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.containerHandler = handlers.NewContainerHandler(s.queries, cfg.EncryptionKey, s.eventPub)
	}

	if s.queries != nil && cfg.EncryptionKey != "" {
		s.nodeHandler = handlers.NewNodeHandler(s.queries, cfg.EncryptionKey, s.eventPub)
	}
	if s.queries != nil {
		s.metricsHandler = handlers.NewMetricsHandler(s.queries)
	}

	if s.queries != nil && cfg.EncryptionKey != "" {
		s.storageHandler = handlers.NewStorageHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.cephHandler = handlers.NewCephHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.backupHandler = handlers.NewBackupHandler(s.queries, cfg.EncryptionKey, s.eventPub)
	}

	if s.queries != nil {
		s.taskHandler = handlers.NewTaskHandler(s.queries, s.eventPub)
		s.scheduleHandler = handlers.NewScheduleHandler(s.queries, s.eventPub)
		s.auditHandler = handlers.NewAuditHandler(s.queries, s.eventPub)
	}

	// Initialize syslog forwarder and attach to event publisher.
	if s.eventPub != nil {
		syslogFwd := proxsyslog.NewForwarder(slog.Default().With("component", "syslog"))
		s.eventPub.SetSyslogForwarder(syslogFwd)

		// Load syslog config from settings if available.
		if s.queries != nil {
			s.loadSyslogConfig(syslogFwd)
		}
	}

	if s.queries != nil && cfg.EncryptionKey != "" {
		s.drsHandler = handlers.NewDRSHandler(shutdownCtx, s.queries, cfg.EncryptionKey, s.eventPub)
		s.migrationHandler = handlers.NewMigrationHandler(shutdownCtx, s.queries, cfg.EncryptionKey, s.eventPub)
		s.networkHandler = handlers.NewNetworkHandler(s.queries, cfg.EncryptionKey, s.eventPub)
	}

	if s.queries != nil && s.rbacEngine != nil {
		s.rbacHandler = handlers.NewRBACHandler(s.queries, s.rbacEngine, s.eventPub)
		s.userHandler = handlers.NewUserHandler(s.queries, s.rbacEngine, s.eventPub, s.sessionManager)
	}

	if s.queries != nil && cfg.EncryptionKey != "" && s.rbacEngine != nil {
		s.ldapHandler = handlers.NewLDAPHandler(s.queries, cfg.EncryptionKey, s.rbacEngine, s.eventPub)
	}

	if s.queries != nil && cfg.EncryptionKey != "" && s.rbacEngine != nil && rdb != nil {
		s.oidcHandler = handlers.NewOIDCHandler(s.queries, cfg.EncryptionKey, s.rbacEngine, s.eventPub, rdb)
	}

	if s.queries != nil && cfg.EncryptionKey != "" && rdb != nil {
		s.totpHandler = handlers.NewTOTPHandler(s.queries, cfg.EncryptionKey, rdb, s.eventPub)
	}

	if s.queries != nil {
		s.settingsHandler = handlers.NewSettingsHandler(s.queries, cfg.DataDir)
	}

	if s.queries != nil && cfg.EncryptionKey != "" {
		registry := notifications.BuildRegistry(s.queries)
		s.cveHandler = handlers.NewCVEHandler(s.db, s.queries, cfg.EncryptionKey, s.eventPub, registry)
		s.alertHandler = handlers.NewAlertHandler(s.queries, cfg.EncryptionKey, s.eventPub, registry)
		// The DLQ handler shares an alert engine instance with the
		// scheduler's evaluator. The replay path delegates back to that
		// engine so the same retry schedule applies whether the
		// notification was triggered automatically or by an operator.
		alertEngine := notifications.NewEngine(shutdownCtx, s.queries, slog.Default().With("component", "alert-engine-replay"), s.eventPub, registry, cfg.EncryptionKey)
		s.notificationDLQHandler = handlers.NewNotificationDLQHandler(s.queries, alertEngine, s.eventPub)
		s.reportHandler = handlers.NewReportHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		rollingOrch := rolling.NewOrchestrator(shutdownCtx, s.queries, cfg.EncryptionKey, slog.Default().With("component", "rolling-update"), s.eventPub, nil)
		s.rollingUpdateHandler = handlers.NewRollingUpdateHandler(s.queries, cfg.EncryptionKey, s.eventPub, rollingOrch)
		s.clusterOptionsHandler = handlers.NewClusterOptionsHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.haHandler = handlers.NewHAHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.poolHandler = handlers.NewPoolHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.replicationHandler = handlers.NewReplicationHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.acmeHandler = handlers.NewACMEHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.aptRepositoryHandler = handlers.NewAptRepositoryHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.metricServerHandler = handlers.NewMetricServerHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.searchHandler = handlers.NewSearchHandler(s.queries, cfg.EncryptionKey, s.eventPub)
	}

	if s.queries != nil {
		s.apiKeyHandler = handlers.NewAPIKeyHandler(s.queries, s.eventPub)
	}
	if s.queries != nil {
		s.mobileDeviceHandler = handlers.NewMobileDeviceHandler(s.queries, s.eventPub)
	}
	s.apiDocsHandler = handlers.NewAPIDocsHandler()

	// Changelog service: fetches release notes from GitHub. Repo can be
	// overridden by CHANGELOG_REPO env var; defaults to upstream Nexara.
	s.changelogHandler = handlers.NewChangelogHandler(
		changelog.New(cfg.ChangelogRepo, slog.Default().With("component", "changelog")),
	)

	// Wire LDAP handler into auth handler for LDAP-aware login
	if s.authHandler != nil && s.ldapHandler != nil {
		s.authHandler.SetLDAPHandler(s.ldapHandler)
	}

	// Wire OIDC handler into auth handler for SSO-aware login
	if s.authHandler != nil && s.oidcHandler != nil {
		s.authHandler.SetOIDCHandler(s.oidcHandler)
	}

	// Wire TOTP handler into auth handler for TOTP-aware login
	if s.authHandler != nil && s.totpHandler != nil {
		s.authHandler.SetTOTPHandler(s.totpHandler)
		s.totpHandler.SetIssueTokensFn(s.authHandler.IssueTokens)
	}

	// Trust the operator-supplied proxy list for ProxyHeader handling.
	// EnableTrustedProxyCheck=true with empty TrustedProxies means Fiber
	// IGNORES the header for everyone — the safe default for direct-to-
	// internet deployments. When TRUSTED_PROXIES names the reverse proxy
	// (e.g. "127.0.0.1,10.0.0.0/8"), c.IP() returns the real client IP
	// from X-Forwarded-For, which is what the auth/general/refresh/
	// ws-token rate limiters key on (Finding #13). EnableIPValidation
	// returns just the first valid IP from the header rather than the raw
	// comma-separated list, so a malicious downstream can't craft a header
	// that lands in an oddly-keyed bucket.
	s.app = fiber.New(fiber.Config{
		ErrorHandler:                 errorHandler,
		DisableStartupMessage:        true,
		BodyLimit:                    32 * 1024 * 1024, // 32MB — bodies above this are streamed, not buffered
		StreamRequestBody:            true,             // Enable streaming for large uploads (ISO/vztmpl)
		DisablePreParseMultipartForm: true,             // Don't buffer multipart bodies; upload handler parses the stream itself
		ProxyHeader:                  cfg.ProxyHeader,
		EnableTrustedProxyCheck:      true,
		TrustedProxies:               cfg.TrustedProxies,
		EnableIPValidation:           true,
	})

	s.setupMiddleware()
	s.setupRoutes()

	return s
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
