package api

func (s *Server) setupRoutes() {
	// Declaratively registered endpoints first. They and the legacy
	// blocks below coexist — a route belongs to exactly one — and the
	// registry is mounted ahead of them because Fiber matches in
	// registration order: a migrated literal path would otherwise be
	// shadowed by whichever legacy :param route still matches it.
	//
	// The registry is built from this Server's handler instances rather
	// than read off a package-level global; see the note above Register
	// in registry.go for why it cannot be one. It is kept on the Server
	// so the guard tests can read the declarations the running route
	// table was actually built from.
	s.registry = s.buildRegistry()
	mountRegistry(s.app, s.registry, s.authRequired())

	// Health probe — not rate-limited, not behind /api/v1.
	s.app.Get("/healthz", s.handleHealthz)

	// API v1 group.
	v1 := s.app.Group("/api/v1")
	v1.Get("/version", s.handleVersion)

	// Changelog (release notes from GitHub) — public, same as version.
	if s.changelogHandler != nil {
		v1.Get("/changelog", s.changelogHandler.Get)
	}

	// Auth routes.
	if s.authHandler != nil {
		authGroup := v1.Group("/auth")
		authGroup.Post("/register", s.authOptional(), s.authHandler.Register)
		authGroup.Post("/login", s.authHandler.Login)
		authGroup.Post("/refresh", s.authHandler.Refresh)
		// Logout intentionally uses authOptional so a user with an expired
		// access token (but a valid refresh cookie) can still revoke the
		// server-side session. The cookie itself is the auth artefact for
		// this endpoint; the user_id check below only fires when an access
		// token IS present, defending against an attacker with a stolen
		// cookie attempting to log out an unrelated user (covered by
		// SameSite=Strict + same-origin SPA already, but defence-in-depth).
		authGroup.Post("/logout", s.authOptional(), s.authHandler.Logout)
		authGroup.Post("/logout-all", s.authRequired(), s.authHandler.LogoutAll)
		// The caller's own sessions: what is signed in to this account, and
		// revoking one of them individually.
		authGroup.Get("/sessions", s.authRequired(), s.authHandler.ListSessions)
		authGroup.Delete("/sessions/:id", s.authRequired(), s.authHandler.RevokeSessionByID)
		authGroup.Post("/console-token", s.authRequired(), s.authHandler.ConsoleToken)
		authGroup.Post("/ws-token", s.authRequired(), s.authHandler.WSToken)

		authGroup.Get("/me", s.authRequired(), s.authHandler.GetMe)
		authGroup.Put("/profile", s.authRequired(), s.authHandler.UpdateProfile)
		authGroup.Post("/change-password", s.authRequired(), s.authHandler.ChangePassword)
		authGroup.Get("/setup-status", s.authHandler.SetupStatus)
		authGroup.Get("/sso-status", s.authHandler.SSOStatus)

		// OIDC auth flow (public, no auth required)
		if s.oidcHandler != nil {
			authGroup.Get("/oidc/authorize", s.oidcHandler.Authorize)
			authGroup.Get("/oidc/callback", s.oidcHandler.Callback)
			authGroup.Post("/oidc/token-exchange", s.authHandler.OIDCTokenExchange)
		}

		// TOTP 2FA routes
		if s.totpHandler != nil {
			// Public — completes two-step login
			authGroup.Post("/totp/verify-login", s.totpHandler.VerifyLogin)

			// Authenticated — self-service TOTP management
			totpGroup := authGroup.Group("/totp", s.authRequired())
			totpGroup.Post("/setup", s.totpHandler.BeginSetup)
			totpGroup.Post("/setup/verify", s.totpHandler.ConfirmSetup)
			totpGroup.Delete("/", s.totpHandler.Disable)
			totpGroup.Get("/status", s.totpHandler.Status)
			totpGroup.Post("/recovery-codes/regenerate", s.totpHandler.RegenerateRecoveryCodes)
		}
	}

	// Cluster routes — single group for all cluster-scoped endpoints.
	if s.clusterHandler != nil {
		clusters := v1.Group("/clusters", s.authRequired())
		clusters.Post("/", s.clusterCreateLimiter(), s.clusterHandler.Create)
		// Rate-limited: it opens an outbound TLS connection to a
		// caller-supplied host, so the general 600/min budget made it a
		// serviceable port scanner. Looser than the create limiters beside it
		// because it is step 1 of a dialog a human retries.
		clusters.Post("/fetch-fingerprint", s.fingerprintFetchLimiter(), s.clusterHandler.FetchFingerprint)
		clusters.Get("/", s.clusterHandler.List)
		clusters.Get("/:id", s.clusterHandler.Get)
		clusters.Put("/:id", s.clusterHandler.Update)
		// Rate-limited alongside fetch-fingerprint: it opens the same kind of
		// outbound TLS connection, just to an address already on file.
		clusters.Post("/:id/verify-certificate", s.fingerprintFetchLimiter(), s.clusterHandler.VerifyCertificate)
		clusters.Delete("/:id", s.clusterHandler.Delete)

		// Nested resources by cluster.
		//
		// The per-cluster PBS listing is declared in
		// internal/api/registry_pbs.go alongside the five global PBS routes
		// and mounted by mountRegistry above, so there is no block for it
		// here.
		//
		// The 38 node routes are declared in
		// internal/api/registry_nodes.go and mounted by mountRegistry above,
		// so there is no block for them here. The node HARDWARE listings the
		// VM dialogs use (/bridges, /hardware/*, /machine-types, /cpu-models,
		// /cpu-flags, /isos) are VMHandler methods and live in
		// internal/api/registry_vms.go.

		// The VM detail page's Veeam card is declared in
		// internal/api/registry_veeam.go alongside the 24 /veeam-servers
		// routes and mounted by mountRegistry above, so there is no block for
		// it here. It lost the vmHandler condition it used to carry: the card
		// is VeeamHandler's route, and a Server holding one handler but not the
		// other would have silently dropped it.
		if s.guestSnapshotHandler != nil {
			clusters.Post("/:cluster_id/guest-snapshots/resync", s.guestSnapshotHandler.Resync)
		}
		// The 18 container routes are declared in
		// internal/api/registry_containers.go and mounted by
		// mountRegistry above, so there is no block for them here.
		if s.vmFoldersHandler != nil {
			clusters.Get("/:cluster_id/vm-folders", s.vmFoldersHandler.List)
			clusters.Post("/:cluster_id/vm-folders", s.vmFoldersHandler.Create)
			clusters.Patch("/:cluster_id/vm-folders/:folder_id", s.vmFoldersHandler.Update)
			clusters.Delete("/:cluster_id/vm-folders/:folder_id", s.vmFoldersHandler.Delete)
			clusters.Put("/:cluster_id/vms/:vm_id/folder", s.vmFoldersHandler.AssignVM)
		}
		if s.storageHandler != nil {
			// The other 12 storage routes are declared in
			// internal/api/registry_storage.go and mounted by mountRegistry
			// above. This ONE stays here because the registry cannot express
			// it: the volume id is a greedy WILDCARD segment, and
			// checkPathParams refuses one outright — a wildcard is the one
			// piece of a path that reaches a handler unvalidated and
			// un-normalized. Declaring it means reshaping the route to a
			// :volume segment, which every caller's percent-encoding would
			// have to agree on; that is a compatibility decision rather than a
			// migration, and it is scoped separately. The handler keeps its
			// hand-placed delete:storage check.
			clusters.Delete("/:cluster_id/storage/:storage_id/content/*", s.storageHandler.DeleteContent)
		}
		// The 7 guest tools routes are declared in
		// internal/api/registry_guest_tools.go and mounted by mountRegistry
		// above, so there is no block for them here.

		// The 5 per-cluster virtio-win routes are declared in
		// internal/api/registry_virtio_win.go alongside the 3 instance-wide
		// ones and mounted by mountRegistry above, so there is no block for
		// them here.
		// The 11 VM-import routes are declared in
		// internal/api/registry_vm_import.go and mounted by mountRegistry
		// above, so there is no block for them here.
		if s.metricsHandler != nil {
			clusters.Get("/:cluster_id/metrics", s.metricsHandler.GetClusterHistorical)
			clusters.Get("/:cluster_id/vms/:vm_id/metrics", s.metricsHandler.GetVMHistorical)
			clusters.Get("/:cluster_id/nodes/:node_id/metrics", s.metricsHandler.GetNodeHistorical)
		}
		// The 17 Ceph routes are declared in
		// internal/api/registry_ceph.go and mounted by mountRegistry
		// above, so there is no block for them here.

		// 64 of NetworkHandler's 66 routes are declared across four files and
		// mounted by mountRegistry above, so there is no block for any of them
		// here: the 7 node-interface routes in
		// internal/api/registry_networks.go, the 28 firewall ones in
		// internal/api/registry_firewall.go, the 25 SDN ones in
		// internal/api/registry_sdn.go, and 4 of the 6 firewall-template
		// routes — including the cluster-scoped apply — in
		// internal/api/registry_firewall_templates.go. The other two template
		// routes are the block further down.

		// The 10 CVE and security-posture routes are declared in
		// internal/api/registry_cve.go and mounted by mountRegistry
		// above, so there is no block for them here.

		// The 6 cluster-scoped alert and maintenance-window routes are
		// declared in internal/api/registry_alerts.go alongside the 14
		// instance-wide ones and mounted by mountRegistry above, so there is no
		// block for them here.

		// The 10 DRS routes are declared in internal/api/registry_drs.go
		// and mounted by mountRegistry above, so there is no block for
		// them here.

		// The per-cluster migration listing is declared in
		// internal/api/registry_migrations.go alongside the six global
		// migration routes and mounted by mountRegistry above, so there is
		// no block for it here.

		// The 7 cluster-scoped restore and backup-job routes are declared in
		// internal/api/registry_backup.go alongside the 19 PBS ones and the 2
		// instance-wide listings, and mounted by mountRegistry above, so there
		// is no block for them here.

		// Schedule routes.
		if s.scheduleHandler != nil {
			clusters.Post("/:cluster_id/schedules", s.scheduleHandler.Create)
			clusters.Get("/:cluster_id/schedules", s.scheduleHandler.List)
			clusters.Put("/:cluster_id/schedules/:id", s.scheduleHandler.Update)
			clusters.Delete("/:cluster_id/schedules/:id", s.scheduleHandler.Delete)
		}

		// Audit log (cluster-scoped).
		if s.auditHandler != nil {
			clusters.Get("/:cluster_id/audit-log", s.auditHandler.ListByCluster)
		}

		// Rolling update routes.
		if s.rollingUpdateHandler != nil {
			clusters.Get("/:cluster_id/rolling-updates", s.rollingUpdateHandler.ListJobs)
			clusters.Post("/:cluster_id/rolling-updates", s.rollingUpdateHandler.CreateJob)
			clusters.Get("/:cluster_id/rolling-updates/:id", s.rollingUpdateHandler.GetJob)
			clusters.Post("/:cluster_id/rolling-updates/:id/start", s.rollingUpdateHandler.StartJob)
			clusters.Post("/:cluster_id/rolling-updates/:id/cancel", s.rollingUpdateHandler.CancelJob)
			clusters.Post("/:cluster_id/rolling-updates/:id/pause", s.rollingUpdateHandler.PauseJob)
			clusters.Post("/:cluster_id/rolling-updates/:id/resume", s.rollingUpdateHandler.ResumeJob)
			clusters.Get("/:cluster_id/rolling-updates/:id/nodes", s.rollingUpdateHandler.ListNodes)
			clusters.Post("/:cluster_id/rolling-updates/:id/nodes/:node_id/confirm-upgrade", s.rollingUpdateHandler.ConfirmUpgrade)
			clusters.Post("/:cluster_id/rolling-updates/:id/nodes/:node_id/skip", s.rollingUpdateHandler.SkipNode)
			clusters.Post("/:cluster_id/rolling-updates/preflight-ha", s.rollingUpdateHandler.PreflightHA)
			clusters.Get("/:cluster_id/nodes/:node/packages", s.rollingUpdateHandler.PreviewPackages)

			// SSH credential management.
			clusters.Get("/:cluster_id/ssh-credentials", s.rollingUpdateHandler.GetSSHCredentials)
			clusters.Put("/:cluster_id/ssh-credentials", s.rollingUpdateHandler.UpsertSSHCredentials)
			clusters.Delete("/:cluster_id/ssh-credentials", s.rollingUpdateHandler.DeleteSSHCredentials)
			clusters.Post("/:cluster_id/ssh-credentials/test", s.rollingUpdateHandler.TestSSHConnection)

			// SSH known-host (pinned host key) management.
			clusters.Get("/:cluster_id/ssh-known-hosts", s.rollingUpdateHandler.ListSSHKnownHosts)
			clusters.Post("/:cluster_id/ssh-known-hosts", s.rollingUpdateHandler.PinSSHHostKey)
			clusters.Delete("/:cluster_id/ssh-known-hosts/:id", s.rollingUpdateHandler.DeleteSSHKnownHost)
		}

		// The 9 cluster option, tag and corosync routes are declared in
		// internal/api/registry_cluster_options.go and mounted by
		// mountRegistry above, so there is no block for them here.

		// The 17 HA routes are declared in internal/api/registry_ha.go
		// and mounted by mountRegistry above, so there is no block for
		// them here.

		// Resource pool CRUD routes (GET list already registered via vmHandler).
		if s.poolHandler != nil {
			clusters.Post("/:cluster_id/pools", s.poolHandler.CreatePool)
			clusters.Get("/:cluster_id/pools/:pool_id", s.poolHandler.GetPool)
			clusters.Put("/:cluster_id/pools/:pool_id", s.poolHandler.UpdatePool)
			clusters.Delete("/:cluster_id/pools/:pool_id", s.poolHandler.DeletePool)
		}

		// Proxmox access control: PVE users, API tokens, groups, roles, ACLs.
		// Realms are read-only — realm writes need Realm.Allocate, which no
		// built-in PVE role except Administrator carries.
		if s.accessHandler != nil {
			access := clusters.Group("/:cluster_id/access")
			access.Get("/users", s.accessHandler.ListUsers)
			access.Post("/users", s.accessHandler.CreateUser)
			access.Get("/users/:userid", s.accessHandler.GetUser)
			access.Put("/users/:userid", s.accessHandler.UpdateUser)
			access.Delete("/users/:userid", s.accessHandler.DeleteUser)
			access.Get("/users/:userid/tokens", s.accessHandler.ListTokens)
			access.Get("/users/:userid/tokens/:tokenid", s.accessHandler.GetToken)
			access.Post("/users/:userid/tokens/:tokenid", s.accessHandler.CreateToken)
			access.Put("/users/:userid/tokens/:tokenid", s.accessHandler.UpdateToken)
			access.Delete("/users/:userid/tokens/:tokenid", s.accessHandler.DeleteToken)
			access.Get("/groups", s.accessHandler.ListGroups)
			access.Post("/groups", s.accessHandler.CreateGroup)
			access.Get("/groups/:groupid", s.accessHandler.GetGroup)
			access.Put("/groups/:groupid", s.accessHandler.UpdateGroup)
			access.Delete("/groups/:groupid", s.accessHandler.DeleteGroup)
			access.Get("/roles", s.accessHandler.ListRoles)
			access.Post("/roles", s.accessHandler.CreateRole)
			access.Get("/roles/:roleid", s.accessHandler.GetRole)
			access.Put("/roles/:roleid", s.accessHandler.UpdateRole)
			access.Delete("/roles/:roleid", s.accessHandler.DeleteRole)
			access.Get("/acl", s.accessHandler.ListACL)
			access.Put("/acl", s.accessHandler.UpdateACL)
			access.Get("/domains", s.accessHandler.ListDomains)
			access.Get("/domains/:realm", s.accessHandler.GetDomain)
			access.Get("/permissions", s.accessHandler.GetPermissions)
		}

		// The 8 replication routes are declared in
		// internal/api/registry_replication.go and mounted by
		// mountRegistry above, so there is no block for them here.

		// ACME certificate routes.
		if s.acmeHandler != nil {
			clusters.Get("/:cluster_id/acme/accounts", s.acmeHandler.ListAccounts)
			clusters.Post("/:cluster_id/acme/accounts", s.acmeHandler.CreateAccount)
			clusters.Get("/:cluster_id/acme/accounts/:name", s.acmeHandler.GetAccount)
			clusters.Put("/:cluster_id/acme/accounts/:name", s.acmeHandler.UpdateAccount)
			clusters.Delete("/:cluster_id/acme/accounts/:name", s.acmeHandler.DeleteAccount)
			clusters.Get("/:cluster_id/acme/plugins", s.acmeHandler.ListPlugins)
			clusters.Post("/:cluster_id/acme/plugins", s.acmeHandler.CreatePlugin)
			clusters.Put("/:cluster_id/acme/plugins/:plugin_id", s.acmeHandler.UpdatePlugin)
			clusters.Delete("/:cluster_id/acme/plugins/:plugin_id", s.acmeHandler.DeletePlugin)
			clusters.Get("/:cluster_id/acme/challenge-schema", s.acmeHandler.ListChallengeSchema)
			clusters.Get("/:cluster_id/acme/directories", s.acmeHandler.ListDirectories)
			clusters.Get("/:cluster_id/acme/tos", s.acmeHandler.GetTOS)
			clusters.Get("/:cluster_id/nodes/:node/acme-config", s.acmeHandler.GetNodeACMEConfig)
			clusters.Put("/:cluster_id/nodes/:node/acme-config", s.acmeHandler.SetNodeACMEConfig)
			clusters.Get("/:cluster_id/nodes/:node/certificates", s.acmeHandler.ListNodeCertificates)
			clusters.Post("/:cluster_id/nodes/:node/certificates/order", s.acmeHandler.OrderNodeCertificate)
			clusters.Put("/:cluster_id/nodes/:node/certificates/renew", s.acmeHandler.RenewNodeCertificate)
			clusters.Delete("/:cluster_id/nodes/:node/certificates/revoke", s.acmeHandler.RevokeNodeCertificate)
		}

		// APT repository management routes.
		if s.aptRepositoryHandler != nil {
			clusters.Get("/:cluster_id/nodes/:node/apt/repositories", s.aptRepositoryHandler.ListRepositories)
			clusters.Put("/:cluster_id/nodes/:node/apt/repositories", s.aptRepositoryHandler.ToggleRepository)
			clusters.Post("/:cluster_id/nodes/:node/apt/repositories", s.aptRepositoryHandler.AddStandardRepository)
		}

		// Metric server routes.
		if s.metricServerHandler != nil {
			clusters.Get("/:cluster_id/metric-servers", s.metricServerHandler.ListServers)
			clusters.Post("/:cluster_id/metric-servers", s.metricServerHandler.CreateServer)
			clusters.Get("/:cluster_id/metric-servers/:server_id", s.metricServerHandler.GetServer)
			clusters.Put("/:cluster_id/metric-servers/:server_id", s.metricServerHandler.UpdateServer)
			clusters.Delete("/:cluster_id/metric-servers/:server_id", s.metricServerHandler.DeleteServer)
		}
	}

	// Firewall template routes (not cluster-scoped).
	//
	// Four of the six are declared in
	// internal/api/registry_firewall_templates.go and mounted by
	// mountRegistry above. These TWO stay here because the registry cannot
	// express them: their body carries `rules`, a JSON array of OBJECTS, and
	// apischema's Property.Items is restricted to scalar element types
	// (compileItems, "only scalar element types are supported"). Declaring
	// them without `rules` is not an option either — an undeclared key is a
	// 400, which would break the one request these endpoints exist for. Both
	// keep their hand-placed manage:network check, and
	// TestFirewallTemplateWritesAreStillLegacy pins that it is a decision
	// rather than a gap.
	if s.networkHandler != nil {
		templates := v1.Group("/firewall-templates", s.authRequired())
		templates.Post("/", s.networkHandler.CreateTemplate)
		templates.Put("/:id", s.networkHandler.UpdateTemplate)
	}

	// The 7 migration routes are declared in
	// internal/api/registry_migrations.go and mounted by mountRegistry
	// above, so there is no block for them here.

	// Alert routes.
	//
	// 20 of AlertHandler's 22 routes are declared in
	// internal/api/registry_alerts.go and mounted by mountRegistry above. The
	// TWO below stay here because the registry cannot express them: their body
	// carries `escalation_chain`, a JSON array of OBJECTS, and apischema's
	// Property.Items is restricted to scalar element types (compileItems,
	// "only scalar element types are supported"). Declaring them without
	// `escalation_chain` is not an option either — an undeclared key is a 400,
	// which would break the escalation editor on every save. Both keep their
	// hand-placed permission checks, and
	// TestAlertRuleWritesAreStillLegacy pins that it is a decision rather than
	// a gap.
	if s.alertHandler != nil {
		alertRules := v1.Group("/alert-rules", s.authRequired())
		alertRules.Post("/", s.alertHandler.CreateRule)
		alertRules.Put("/:id", s.alertHandler.UpdateRule)
	}

	// Notification dead-letter queue.
	if s.notificationDLQHandler != nil {
		dlq := v1.Group("/notification-dlq", s.authRequired())
		dlq.Get("/", s.notificationDLQHandler.List)
		dlq.Get("/summary", s.notificationDLQHandler.Summary)
		dlq.Post("/:id/retry", s.notificationDLQHandler.Retry)
		dlq.Post("/:id/dismiss", s.notificationDLQHandler.Dismiss)
		dlq.Delete("/:id", s.notificationDLQHandler.Delete)
	}

	// The 12 report routes are declared in
	// internal/api/registry_reports.go and mounted by mountRegistry above,
	// so there is no group for them here.

	// PBS server routes.
	//
	// The 6 PBSHandler routes are declared in internal/api/registry_pbs.go
	// and the 19 BackupHandler routes nested under /pbs-servers/:pbs_id in
	// internal/api/registry_backup.go. Both are mounted by mountRegistry
	// above, so there is no group for them here.

	// Veeam Backup & Replication server routes.
	//
	// All 25 VeeamHandler routes are declared in
	// internal/api/registry_veeam.go — the 24 under /veeam-servers and the VM
	// detail page's Veeam card at /clusters/:cluster_id/vms/:vm_id/veeam — and
	// mounted by mountRegistry above, so there is no group for them here. The
	// two shared rate limiters they carried (veeamConnectLimiter for the three
	// calls that spend a password grant, veeamControlLimiter for the seven job
	// and session calls) are built once in buildRegistry and attached through
	// the declaration's RateLimiter field, which mounts them in the same
	// position this block did: after authentication, ahead of the permission
	// check.

	// The instance-wide PBS snapshot lookup and the backup-coverage report
	// are declared in internal/api/registry_backup.go and mounted by
	// mountRegistry above, so there is no block for them here.

	// Audit log routes.
	if s.auditHandler != nil {
		audit := v1.Group("/audit-log", s.authRequired())
		audit.Get("/recent", s.auditHandler.ListRecent)
		audit.Get("/actions", s.auditHandler.ListActions)
		audit.Get("/users", s.auditHandler.ListUsers)
		audit.Get("/export", s.auditHandler.Export)
		audit.Get("/syslog-config", s.auditHandler.GetSyslogConfig)
		audit.Put("/syslog-config", s.auditHandler.UpdateSyslogConfig)
		audit.Post("/syslog-test", s.auditHandler.TestSyslog)
		audit.Get("/", s.auditHandler.List)
	}

	// Task history routes.
	if s.taskHandler != nil {
		tasks := v1.Group("/tasks", s.authRequired())
		tasks.Get("/", s.taskHandler.List)
		tasks.Post("/", s.taskHandler.Create)
		tasks.Put("/:upid", s.taskHandler.Update)
		tasks.Delete("/", s.taskHandler.ClearCompleted)
	}

	// Central guest snapshot inventory.
	if s.guestSnapshotHandler != nil {
		snaps := v1.Group("/guest-snapshots", s.authRequired())
		snaps.Get("/", s.guestSnapshotHandler.List)
	}

	// The 8 virtio-win routes — the instance-wide catalog and download
	// source, and the 5 per-cluster policy routes — are declared in
	// internal/api/registry_virtio_win.go and mounted by mountRegistry
	// above, so there is no block for them here.

	// RBAC routes.
	if s.rbacHandler != nil {
		rbac := v1.Group("/rbac", s.authRequired())
		rbac.Get("/roles", s.rbacHandler.ListRoles)
		rbac.Post("/roles", s.rbacHandler.CreateRole)
		rbac.Get("/roles/:id", s.rbacHandler.GetRole)
		rbac.Put("/roles/:id", s.rbacHandler.UpdateRole)
		rbac.Delete("/roles/:id", s.rbacHandler.DeleteRole)
		rbac.Get("/permissions", s.rbacHandler.ListPermissions)
		rbac.Get("/users/:user_id/roles", s.rbacHandler.ListUserRoles)
		rbac.Post("/users/:user_id/roles", s.rbacHandler.AssignUserRole)
		rbac.Delete("/users/:user_id/roles/:id", s.rbacHandler.RevokeUserRole)
		rbac.Get("/me/permissions", s.rbacHandler.MyPermissions)
	}

	// LDAP config routes.
	if s.ldapHandler != nil {
		ldap := v1.Group("/ldap", s.authRequired())
		ldap.Get("/configs", s.ldapHandler.List)
		ldap.Post("/configs", s.ldapHandler.Create)
		ldap.Get("/configs/:id", s.ldapHandler.Get)
		ldap.Put("/configs/:id", s.ldapHandler.Update)
		ldap.Delete("/configs/:id", s.ldapHandler.Delete)
		ldap.Post("/configs/:id/test", s.ldapHandler.TestConnection)
		ldap.Post("/configs/:id/sync", s.ldapHandler.Sync)
	}

	// OIDC config routes (admin).
	if s.oidcHandler != nil {
		oidc := v1.Group("/oidc", s.authRequired())
		oidc.Get("/configs", s.oidcHandler.List)
		oidc.Post("/configs", s.oidcHandler.Create)
		oidc.Get("/configs/:id", s.oidcHandler.Get)
		oidc.Put("/configs/:id", s.oidcHandler.Update)
		oidc.Delete("/configs/:id", s.oidcHandler.Delete)
		oidc.Post("/configs/:id/test", s.oidcHandler.TestConnection)
	}

	// Global search.
	if s.searchHandler != nil {
		v1.Get("/search", s.authRequired(), s.searchHandler.GlobalSearch)
	}

	// Per-user favorites (self-service): the caller's own starred clusters,
	// nodes and guests, surfaced above the sidebar tree.
	if s.favoritesHandler != nil {
		favorites := v1.Group("/favorites", s.authRequired())
		favorites.Get("/", s.favoritesHandler.ListFavorites)
		favorites.Post("/", s.favoritesHandler.AddFavorite)
		// The target is identified by query parameters rather than a path, so a
		// node name never has to survive URL path segmentation.
		favorites.Delete("/", s.favoritesHandler.RemoveFavorite)
	}

	// API keys (self-service).
	if s.apiKeyHandler != nil {
		apiKeys := v1.Group("/api-keys", s.authRequired())
		apiKeys.Post("/", s.apiKeyHandler.Create)
		apiKeys.Get("/", s.apiKeyHandler.List)
		apiKeys.Delete("/:id", s.apiKeyHandler.Revoke)
		apiKeys.Delete("/", s.apiKeyHandler.RevokeAll)
	}

	// Admin API key management.
	if s.apiKeyHandler != nil {
		adminKeys := v1.Group("/admin/api-keys", s.authRequired())
		adminKeys.Get("/", s.apiKeyHandler.AdminList)
		adminKeys.Delete("/:id", s.apiKeyHandler.AdminRevoke)
	}

	// API documentation.
	if s.apiDocsHandler != nil {
		// Hand the docs handler the registry's declarations. It happens
		// HERE, in the same function that builds the registry, rather than
		// alongside SetApp in New: every path that registers routes is
		// this one, so a Server built by a test documents the same
		// contract a Server built by New does, and the push cannot be
		// forgotten by a future caller that registers routes some other
		// way. See internal/api/api_docs.go for why the payload is pushed
		// rather than pulled.
		s.apiDocsHandler.SetDeclaredEndpoints(docEndpoints(s.registry))
		v1.Get("/api-docs", s.authRequired(), s.apiDocsHandler.GetDocs)
	}

	// Settings routes.
	if s.settingsHandler != nil {
		settings := v1.Group("/settings", s.authRequired())
		settings.Get("/", s.settingsHandler.ListSettings)
		settings.Get("/branding", s.settingsHandler.GetBranding)
		settings.Get("/branding/logo-file", s.settingsHandler.ServeLogo)
		settings.Get("/branding/favicon-file", s.settingsHandler.ServeFavicon)
		settings.Post("/branding/logo", s.settingsHandler.UploadLogo)
		settings.Post("/branding/favicon", s.settingsHandler.UploadFavicon)
		settings.Get("/:key", s.settingsHandler.GetSetting)
		settings.Put("/:key", s.settingsHandler.UpsertSetting)
		settings.Delete("/:key", s.settingsHandler.DeleteSetting)
	}

	// User management routes.
	if s.userHandler != nil {
		users := v1.Group("/users", s.authRequired())
		users.Get("/", s.userHandler.List)
		users.Get("/:id", s.userHandler.Get)
		users.Put("/:id", s.userHandler.Update)
		users.Delete("/:id", s.userHandler.Delete)
		if s.totpHandler != nil {
			users.Delete("/:id/totp", s.totpHandler.AdminReset)
		}
	}
}
