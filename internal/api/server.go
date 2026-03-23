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
	"github.com/bigjakk/nexara/internal/config"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/notifications"
	"github.com/bigjakk/nexara/internal/rolling"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

// Server is the API server that holds all dependencies.
type Server struct {
	app            *fiber.App
	config         *config.Config
	db             *pgxpool.Pool
	queries        *db.Queries
	redis          *redis.Client
	jwtService     *auth.JWTService
	sessionManager *auth.SessionManager
	authHandler    *handlers.AuthHandler
	clusterHandler *handlers.ClusterHandler
	pbsHandler     *handlers.PBSHandler
	nodeHandler    *handlers.NodeHandler
	vmHandler        *handlers.VMHandler
	containerHandler *handlers.ContainerHandler
	storageHandler   *handlers.StorageHandler
	metricsHandler *handlers.MetricsHandler
	cephHandler    *handlers.CephHandler
	backupHandler   *handlers.BackupHandler
	taskHandler     *handlers.TaskHandler
	scheduleHandler *handlers.ScheduleHandler
	auditHandler    *handlers.AuditHandler
	drsHandler       *handlers.DRSHandler
	migrationHandler *handlers.MigrationHandler
	networkHandler   *handlers.NetworkHandler
	rbacHandler      *handlers.RBACHandler
	userHandler      *handlers.UserHandler
	ldapHandler      *handlers.LDAPHandler
	oidcHandler      *handlers.OIDCHandler
	totpHandler      *handlers.TOTPHandler
	cveHandler       *handlers.CVEHandler
	alertHandler        *handlers.AlertHandler
	reportHandler       *handlers.ReportHandler
	rollingUpdateHandler *handlers.RollingUpdateHandler
	settingsHandler      *handlers.SettingsHandler
	clusterOptionsHandler *handlers.ClusterOptionsHandler
	haHandler            *handlers.HAHandler
	poolHandler          *handlers.PoolHandler
	replicationHandler   *handlers.ReplicationHandler
	acmeHandler          *handlers.ACMEHandler
	aptRepositoryHandler *handlers.AptRepositoryHandler
	metricServerHandler  *handlers.MetricServerHandler
	searchHandler        *handlers.SearchHandler
	apiKeyHandler        *handlers.APIKeyHandler
	apiDocsHandler       *handlers.APIDocsHandler
	rbacEngine          *auth.RBACEngine
	eventPub            *events.Publisher
}

// New creates a new API server with the given dependencies.
func New(cfg *config.Config, pool *pgxpool.Pool, rdb *redis.Client) *Server {
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
		s.authHandler = handlers.NewAuthHandler(s.queries, s.jwtService, s.sessionManager, s.rbacEngine)
	}

	if s.queries != nil && cfg.EncryptionKey != "" {
		s.clusterHandler = handlers.NewClusterHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.pbsHandler = handlers.NewPBSHandler(s.queries, cfg.EncryptionKey, s.eventPub)
	}

	if s.queries != nil && cfg.EncryptionKey != "" {
		s.vmHandler = handlers.NewVMHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.containerHandler = handlers.NewContainerHandler(s.queries, cfg.EncryptionKey, s.eventPub)
	}

	if s.queries != nil {
		s.nodeHandler = handlers.NewNodeHandler(s.queries)
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
		s.drsHandler = handlers.NewDRSHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.migrationHandler = handlers.NewMigrationHandler(s.queries, cfg.EncryptionKey, s.eventPub)
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
		s.cveHandler = handlers.NewCVEHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.alertHandler = handlers.NewAlertHandler(s.queries, cfg.EncryptionKey, s.eventPub, newDispatcherRegistry())
		s.reportHandler = handlers.NewReportHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		rollingOrch := rolling.NewOrchestrator(s.queries, cfg.EncryptionKey, slog.Default().With("component", "rolling-update"), s.eventPub, nil)
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
	s.apiDocsHandler = handlers.NewAPIDocsHandler()

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

	s.app = fiber.New(fiber.Config{
		ErrorHandler:                 errorHandler,
		DisableStartupMessage:        true,
		BodyLimit:                    32 * 1024 * 1024, // 32MB — bodies above this are streamed, not buffered
		StreamRequestBody:            true,             // Enable streaming for large uploads (ISO/vztmpl)
		DisablePreParseMultipartForm: true,             // Don't buffer multipart bodies; upload handler parses the stream itself
	})

	s.setupMiddleware()
	s.setupRoutes()

	return s
}

func newDispatcherRegistry() *notifications.Registry {
	r := notifications.NewRegistry()
	r.Register(&notifications.SMTPDispatcher{})
	r.Register(&notifications.SlackDispatcher{})
	r.Register(&notifications.DiscordDispatcher{})
	r.Register(&notifications.TeamsDispatcher{})
	r.Register(&notifications.TelegramDispatcher{})
	r.Register(&notifications.WebhookDispatcher{})
	r.Register(&notifications.PagerDutyDispatcher{})
	return r
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
