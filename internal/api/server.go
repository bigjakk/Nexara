package api

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"log/slog"

	"github.com/proxdash/proxdash/internal/api/handlers"
	"github.com/proxdash/proxdash/internal/auth"
	"github.com/proxdash/proxdash/internal/config"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
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
	rbacEngine       *auth.RBACEngine
	eventPub         *events.Publisher
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
		s.auditHandler = handlers.NewAuditHandler(s.queries)
	}

	if s.queries != nil && cfg.EncryptionKey != "" {
		s.drsHandler = handlers.NewDRSHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.migrationHandler = handlers.NewMigrationHandler(s.queries, cfg.EncryptionKey, s.eventPub)
		s.networkHandler = handlers.NewNetworkHandler(s.queries, cfg.EncryptionKey, s.eventPub)
	}

	if s.queries != nil && s.rbacEngine != nil {
		s.rbacHandler = handlers.NewRBACHandler(s.queries, s.rbacEngine, s.eventPub)
		s.userHandler = handlers.NewUserHandler(s.queries, s.rbacEngine, s.eventPub)
	}

	if s.queries != nil && cfg.EncryptionKey != "" && s.rbacEngine != nil {
		s.ldapHandler = handlers.NewLDAPHandler(s.queries, cfg.EncryptionKey, s.rbacEngine, s.eventPub)
	}

	// Wire LDAP handler into auth handler for LDAP-aware login
	if s.authHandler != nil && s.ldapHandler != nil {
		s.authHandler.SetLDAPHandler(s.ldapHandler)
	}

	s.app = fiber.New(fiber.Config{
		ErrorHandler:          errorHandler,
		DisableStartupMessage: true,
		BodyLimit:             4 * 1024 * 1024 * 1024, // 4 GB for ISO uploads
	})

	s.setupMiddleware()
	s.setupRoutes()

	return s
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
