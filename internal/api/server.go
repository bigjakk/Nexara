package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/handlers"
	// nexapp, not app: Server.app is the Fiber app.
	nexapp "github.com/bigjakk/nexara/internal/app"
	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/changelog"
	"github.com/bigjakk/nexara/internal/config"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

// Server is the API server that holds all dependencies.
type Server struct {
	app *fiber.App

	// registry holds the declaratively registered endpoints setupRoutes
	// mounted, bound to this Server's own handlers. See buildRegistry.
	registry *Registry

	config                 *config.Config
	db                     *pgxpool.Pool
	queries                *db.Queries
	redis                  *redis.Client
	jwtService             *auth.JWTService
	sessionManager         *auth.SessionManager
	authHandler            *handlers.AuthHandler
	clusterHandler         *handlers.ClusterHandler
	pbsHandler             *handlers.PBSHandler
	veeamHandler           *handlers.VeeamHandler
	nodeHandler            *handlers.NodeHandler
	vmHandler              *handlers.VMHandler
	containerHandler       *handlers.ContainerHandler
	storageHandler         *handlers.StorageHandler
	virtioWinHandler       *handlers.VirtioWinHandler
	guestToolsHandler      *handlers.GuestToolsHandler
	vmImportHandler        *handlers.VMImportHandler
	vmFoldersHandler       *handlers.VMFoldersHandler
	favoritesHandler       *handlers.FavoritesHandler
	metricsHandler         *handlers.MetricsHandler
	cephHandler            *handlers.CephHandler
	backupHandler          *handlers.BackupHandler
	guestSnapshotHandler   *handlers.GuestSnapshotHandler
	taskHandler            *handlers.TaskHandler
	scheduleHandler        *handlers.ScheduleHandler
	auditHandler           *handlers.AuditHandler
	drsHandler             *handlers.DRSHandler
	migrationHandler       *handlers.MigrationHandler
	networkHandler         *handlers.NetworkHandler
	rbacHandler            *handlers.RBACHandler
	userHandler            *handlers.UserHandler
	ldapHandler            *handlers.LDAPHandler
	oidcHandler            *handlers.OIDCHandler
	totpHandler            *handlers.TOTPHandler
	cveHandler             *handlers.CVEHandler
	alertHandler           *handlers.AlertHandler
	notificationDLQHandler *handlers.NotificationDLQHandler
	reportHandler          *handlers.ReportHandler
	rollingUpdateHandler   *handlers.RollingUpdateHandler
	settingsHandler        *handlers.SettingsHandler
	clusterOptionsHandler  *handlers.ClusterOptionsHandler
	haHandler              *handlers.HAHandler
	poolHandler            *handlers.PoolHandler
	accessHandler          *handlers.AccessHandler
	replicationHandler     *handlers.ReplicationHandler
	acmeHandler            *handlers.ACMEHandler
	aptRepositoryHandler   *handlers.AptRepositoryHandler
	metricServerHandler    *handlers.MetricServerHandler
	searchHandler          *handlers.SearchHandler
	apiKeyHandler          *handlers.APIKeyHandler
	apiDocsHandler         *handlers.APIDocsHandler
	changelogHandler       *handlers.ChangelogHandler
	rbacEngine             *auth.RBACEngine
	eventPub               *events.Publisher
	proxmoxCache           *proxmox.ClientCache
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

	// app is the composition root. Handlers that need a domain engine take
	// it from here rather than constructing one — see internal/app for why
	// the per-site construction this replaced was a bug source, not a style
	// issue. Never nil: New requires it.
	app *nexapp.App
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

// New creates a new API server over an already-constructed composition root.
// Every long-lived service (engines, caches, RBAC, JWT, the event publisher)
// comes from a; the server constructs only HTTP handlers. a.ShutdownCtx is the
// per-process context cancelled on SIGTERM, threaded into handlers that launch
// detached goroutines so they abort cleanly instead of orphaning their work.
//
// Construction order matters in two places (call them out so future
// edits don't accidentally reorder past a hidden dependency):
//  1. registerInfra must run before registerAuth so authHandler receives
//     the RBAC engine it calls directly for permission lookups.
//  2. apiDocsHandler.SetApp must run AFTER setupRoutes so app.GetRoutes()
//     returns the populated route table.
//
// Outside those two constraints, the per-domain factory functions
// (registerAuth, registerInventory, …) can be reordered without ill effect.
func New(a *nexapp.App) *Server {
	cfg := a.Cfg
	shutdownCtx := a.ShutdownCtx
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}

	d := &serverDeps{
		cfg:           cfg,
		pool:          a.Pool,
		queries:       a.Queries,
		rdb:           a.Redis,
		eventPub:      a.EventPub,
		encryptionKey: cfg.EncryptionKey,
		shutdownCtx:   shutdownCtx,
		app:           a,
	}

	// Apply the refresh-cookie Secure policy (SECURE_COOKIES) once, before serving.
	handlers.SetCookieSecureMode(cfg.SecureCookies)

	s := &Server{config: cfg, db: a.Pool, redis: a.Redis, queries: d.queries, eventPub: d.eventPub}

	s.registerInfra(d)
	s.registerAuth(d)
	s.registerInventory(d)
	s.registerOps(d)
	s.registerSecurity(d)
	s.registerSettingsAndKeys(d)
	s.wireAuthCompositions()

	s.app = fiber.New(buildFiberConfig(cfg))
	// Outside the middleware chain on purpose: it has to see the answers
	// Fiber gives without running the chain. See closeConnectionsLeftMidBody.
	closeConnectionsLeftMidBody(s.app, bodyReadTimeout)
	// fasthttp builds the error it answers an unreadable request with out of
	// the request itself, and Fiber would send that text back as the error
	// envelope's message — the whole buffered head, Cookie and Authorization
	// included, for some heads. What keeps every byte of the request out of
	// that answer is redactServerErrorBodies, which replaces its body.
	redactServerErrorBodies(s.app)
	// SecureErrorLogMessage is a second layer under it: it keeps the snippet of
	// the buffered request fasthttp would otherwise append to most of those
	// errors (headerErrorMsg) out of the error's text, which Fiber's handler
	// still reads to classify the error — so the request is not in the error
	// for anything that ever does print it. It does not keep out the bytes a
	// few errors quote themselves; see redactServerErrorBodies.
	s.app.Server().SecureErrorLogMessage = true
	s.setupMiddleware()
	s.setupRoutes()

	// API docs must be wired AFTER setupRoutes — it walks app.GetRoutes()
	// at request time, which is empty before route registration.
	if s.apiDocsHandler != nil {
		s.apiDocsHandler.SetApp(s.app)
	}

	return s
}

// keepAliveIdleTimeout is how long a kept-alive connection may wait between
// requests before the server closes it.
//
// It has to outlast the reverse proxy's own idle timeout on its connections to
// Nexara: a proxy that reuses a connection just as Nexara closes it has sent a
// request nobody answers, and it may not resend one that is not idempotent.
// The proxies the README configures keep an idle upstream connection for:
//
//   - nginx: 60 s — keepalive_timeout, defaulted in
//     ngx_http_upstream_keepalive_init_main_conf
//     (src/http/modules/ngx_http_upstream_keepalive_module.c). That function
//     turns keepalive on, with 32 cached connections, for every explicit
//     upstream{} block that does not configure it, so such a block reuses
//     connections by default; a proxy_pass straight to an address is an
//     implicit upstream, which it skips, and never reuses one;
//   - Traefik: 90 s — idleConnTimeout, ForwardingTimeouts.SetDefaults
//     (pkg/config/dynamic/http_config.go);
//   - Caddy: 2 min — the transport's keepalive idle timeout,
//     HTTPTransport.NewTransport (modules/caddyhttp/reverseproxy/httptransport.go).
//
// Three minutes outlasts the longest. A proxy configured to keep them longer
// than this should be lowered below it.
//
// What it bounds, read off fasthttp v1.73.0's Server.serveConn, is the wait for
// the first byte of a connection's second and later requests, and nothing
// else: serveConn arms it just before that wait, replaces it with ReadTimeout
// (headReadTimeout) the moment a byte arrives, and when it expires closes the
// connection without a response (ErrNothingRead on a request after the
// first). So it cuts neither a streamed upload nor a WebSocket — /ws,
// /ws/console, /ws/vnc, however long the console session.
const keepAliveIdleTimeout = 3 * time.Minute

// headReadTimeout is how long a request's head, and fasthttp's read-ahead of
// its body (the first 8 KiB), may take to arrive — timed from the request's
// first byte, or from the connection opening for its first request. It is
// buildFiberConfig's ReadTimeout.
//
// Without it nothing bounds those waits, and they come before authentication
// and before any rate limiter: a client could hold a connection, and the
// goroutine serving it, for as long as it liked by never finishing a head.
// When it expires, Server.serveConn answers 408 (App.serverErrorHandler, with
// errorHandler's request_timeout) and closes the connection. A minute is far
// more than any client needs to send a head — capped at ReadBufferSize,
// 16 KiB — and 8 KiB.
//
// It bounds nothing after that. serveConn arms it when a request's first byte
// arrives and would leave it armed while the handler reads the rest of the
// body — cutting an ISO upload streamed through the handler a minute after
// its first byte. closeConnectionsLeftMidBody replaces it immediately before
// the handler runs, with bodyReadTimeout or, for a streamed multipart upload
// (isStreamedUpload), with no deadline at all. A hijacked connection — a
// WebSocket — has every deadline cleared twice more on top of that: by
// serveConn before it starts the hijack handler (c.SetDeadline(zeroTime)), and
// by the fasthttp/websocket upgrader in its own (FastHTTPUpgrader.Upgrade,
// server_fasthttp.go).
const headReadTimeout = 60 * time.Second

// bodyReadTimeout is how long a handler may spend reading a request's body,
// timed from when the handler starts: after the head and fasthttp's read-ahead
// of the first 8 KiB, which headReadTimeout bounds. closeConnectionsLeftMidBody
// arms it as the connection's read deadline on every request but a streamed
// multipart upload (isStreamedUpload), which streams for as long as it takes.
//
// Without it nothing bounds those reads, and on some routes they come before
// authentication and before any rate limiter: logout and the OIDC token
// exchange read their bodies unauthenticated (c.Bind().Body and bodyValues,
// both through c.Body()), so a client could declare a body, send its first
// 8 KiB and stop, and hold a handler goroutine for as long as it liked. When
// the deadline cuts a read, c.Body() hands the handler the read error's text
// in place of the body (Request.bodyBytes), the handler answers as it does a
// malformed body, and the connection is closed after the answer (see
// closeConnectionsLeftMidBody). Five minutes is the operator's choice. Every
// body it bounds is at most 10 MiB (the body-size guard), so it asks of a
// client no more than about 35 KB/s for the largest.
const bodyReadTimeout = 5 * time.Minute

// buildFiberConfig assembles the Fiber-side config struct. TrustProxy=true with
// an empty TrustProxyConfig.Proxies means Fiber IGNORES the proxy header for everyone
// — the safe default for direct-to-internet deployments. When TRUSTED_PROXIES names
// the reverse proxy (e.g. "127.0.0.1,10.0.0.0/8"), c.IP() returns the real client IP
// from X-Forwarded-For, which is what the auth/general/refresh/ws-token rate limiters
// key on (Finding #13). EnableIPValidation makes Fiber walk that header right-to-left
// and return the first entry that is NOT itself a trusted proxy, so a client can't
// prepend a spoofed IP to land in an oddly-keyed bucket — everything it controls sits
// left of the real hop and is skipped. (Fiber v3.5 changed this: it used to return the
// left-most valid IP, which the client could forge.)
//
// Fiber v3 rename: EnableTrustedProxyCheck → TrustProxy, and the TrustedProxies list
// moved into TrustProxyConfig.Proxies. The TrustProxyConfig.{Loopback,LinkLocal,
// Private,UnixSocket} auto-trust toggles all default false, so ONLY the explicit
// Proxies list is trusted — preserving the exact v2 semantics (XFF is honored solely
// for remotes on the TRUSTED_PROXIES allowlist).
//
// It sets two read timeouts: IdleTimeout (keepAliveIdleTimeout) bounds the wait
// between a connection's requests, and ReadTimeout (headReadTimeout) the head
// of each request and fasthttp's read-ahead of its body. fasthttp would leave
// ReadTimeout armed through a handler's reads of the rest of a body, so
// closeConnectionsLeftMidBody replaces it before the handler runs: with
// bodyReadTimeout, and with no deadline for a streamed multipart upload
// (isStreamedUpload), whose ISO streams through the handler for as long as it
// takes.
func buildFiberConfig(cfg *config.Config) fiber.Config {
	return fiber.Config{
		ErrorHandler:      errorHandler,
		BodyLimit:         32 * 1024 * 1024, // 32MB — bodies above this are streamed, not buffered
		StreamRequestBody: true,             // Enable streaming for large uploads (ISO/vztmpl)
		ReadBufferSize:    16 * 1024,        // fasthttp default is 4KB for the whole request line + headers; Bearer JWT + long filter query strings (?vmids= from big folders) overflow it into opaque 431s
		IdleTimeout:       keepAliveIdleTimeout,
		ReadTimeout:       headReadTimeout,

		DisablePreParseMultipartForm: true, // Don't buffer multipart bodies; upload handler parses the stream itself
		ProxyHeader:                  cfg.ProxyHeader,
		TrustProxy:                   true,
		TrustProxyConfig:             fiber.TrustProxyConfig{Proxies: cfg.TrustedProxies},
		EnableIPValidation:           true,
	}
}

// registerInfra wires the cross-cutting services that downstream handlers
// depend on. The Proxmox client cache, JWT service, session manager and RBAC
// engine all come from the composition root; the syslog forwarder is the one
// thing still built here, because loading its destination needs the settings
// tables.
func (s *Server) registerInfra(d *serverDeps) {
	// These all come from the composition root — including the Proxmox client
	// cache, whose pub/sub subscriber app.New already started.
	s.proxmoxCache = d.app.ProxmoxCache
	s.jwtService = d.app.JWT
	d.jwt = d.app.JWT
	s.sessionManager = d.app.SessionMgr
	d.sessionMgr = d.app.SessionMgr
	s.rbacEngine = d.app.RBAC
	d.rbacEngine = d.app.RBAC

	// Syslog forwarding is attached here rather than in app.New because
	// loading the destination needs the settings tables. It mutates the
	// process-wide publisher, so scheduler- and collector-written audit rows
	// are forwarded too — they previously held forwarder-less publishers of
	// their own and never reached a configured SIEM. Best-effort: a config
	// load failure doesn't block the server.
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
		s.authHandler = handlers.NewAuthHandler(d.pool, d.queries, d.jwt, d.sessionMgr, d.rbacEngine, d.eventPub)
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
		s.veeamHandler = handlers.NewVeeamHandler(d.queries, d.encryptionKey, d.eventPub)
		s.vmHandler = handlers.NewVMHandler(d.queries, d.encryptionKey, d.eventPub)
		s.containerHandler = handlers.NewContainerHandler(d.queries, d.encryptionKey, d.eventPub)
		s.nodeHandler = handlers.NewNodeHandler(d.queries, d.encryptionKey, d.eventPub)
		s.storageHandler = handlers.NewStorageHandler(d.queries, d.encryptionKey, d.eventPub)
		s.virtioWinHandler = handlers.NewVirtioWinHandler(d.queries, d.eventPub, d.app.VirtioWin)
		s.guestToolsHandler = handlers.NewGuestToolsHandler(d.queries, d.eventPub, d.app.GuestTools)
		s.vmImportHandler = handlers.NewVMImportHandler(d.queries, d.encryptionKey, d.eventPub)
		s.cephHandler = handlers.NewCephHandler(d.queries, d.encryptionKey, d.eventPub)
		s.backupHandler = handlers.NewBackupHandler(d.queries, d.encryptionKey, d.eventPub)
		s.guestSnapshotHandler = handlers.NewGuestSnapshotHandler(d.queries, d.encryptionKey, d.eventPub)
	}
	if d.hasDB() {
		s.metricsHandler = handlers.NewMetricsHandler(d.queries)
		s.taskHandler = handlers.NewTaskHandler(d.queries, d.eventPub, d.cfg.TaskHistoryRetention)
		s.scheduleHandler = handlers.NewScheduleHandler(d.queries, d.eventPub)
		s.auditHandler = handlers.NewAuditHandler(d.queries, d.eventPub)
		s.vmFoldersHandler = handlers.NewVMFoldersHandler(d.queries, d.eventPub)
		s.favoritesHandler = handlers.NewFavoritesHandler(d.queries)
	}
}

// registerOps builds the orchestration surface — DRS, migration jobs,
// network management, and (further below in registerSecurity) rolling
// updates. These handlers spin up detached goroutines that must honour
// shutdownCtx, so they receive it explicitly.
func (s *Server) registerOps(d *serverDeps) {
	if d.hasCrypto() {
		s.drsHandler = handlers.NewDRSHandler(d.queries, d.encryptionKey, d.eventPub, d.app.DRSEngine)
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
	// Engines come from the composition root. The DLQ handler genuinely does
	// share the scheduler's alert engine now, so an operator replay follows
	// the same retry schedule and rate limits as an automatic dispatch; the
	// rolling orchestrator is the fully-wired one, so a reboot failure
	// confirmed over HTTP sends the job's configured notification instead of
	// silently dropping it against a nil registry.
	s.cveHandler = handlers.NewCVEHandler(d.pool, d.queries, d.encryptionKey, d.eventPub, d.app.NotifyRegistry, d.app.CVEScanner)
	s.alertHandler = handlers.NewAlertHandler(d.queries, d.encryptionKey, d.eventPub, d.app.NotifyRegistry)
	s.notificationDLQHandler = handlers.NewNotificationDLQHandler(d.queries, d.app.AlertEngine, d.eventPub)
	s.reportHandler = handlers.NewReportHandler(d.queries, d.encryptionKey, d.eventPub, d.app.ReportGen)
	s.rollingUpdateHandler = handlers.NewRollingUpdateHandler(d.queries, d.encryptionKey, d.eventPub, d.app.RollingOrch)
	s.clusterOptionsHandler = handlers.NewClusterOptionsHandler(d.queries, d.encryptionKey, d.eventPub)
	s.haHandler = handlers.NewHAHandler(d.queries, d.encryptionKey, d.eventPub)
	s.poolHandler = handlers.NewPoolHandler(d.queries, d.encryptionKey, d.eventPub)
	s.accessHandler = handlers.NewAccessHandler(d.queries, d.encryptionKey, d.eventPub)
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
		s.settingsHandler = handlers.NewSettingsHandler(d.queries, d.eventPub, d.cfg.DataDir)
		s.apiKeyHandler = handlers.NewAPIKeyHandler(d.queries, d.eventPub)
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
//
// Fiber v3 moved DisableStartupMessage out of fiber.Config and into ListenConfig.
func (s *Server) Listen(addr string) error {
	return s.app.Listen(addr, fiber.ListenConfig{DisableStartupMessage: true})
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	return s.app.Shutdown()
}

// App returns the underlying Fiber app for testing.
func (s *Server) App() *fiber.App {
	return s.app
}

// SetVeeamSyncTrigger gives the Veeam handler a way to ask the collector for
// an inventory pass as soon as a server is registered, instead of leaving the
// operator on empty tables until the next tick.
//
// Wired from main after the collector is built, because the API server is
// constructed first. No-op when the Veeam handler is absent (no encryption
// key) — registering a server is impossible in that state anyway.
func (s *Server) SetVeeamSyncTrigger(t handlers.VeeamSyncTrigger) {
	if s.veeamHandler == nil {
		return
	}
	s.veeamHandler.SetSyncTrigger(t)
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
