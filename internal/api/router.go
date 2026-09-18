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
	//
	// The build version probe and the changelog are declared in
	// internal/api/registry_version.go and
	// internal/api/registry_changelog.go and mounted by mountRegistry above,
	// so there are no routes on this group here. It is kept because the
	// legacy blocks below still hang off it.
	v1 := s.app.Group("/api/v1")

	// Auth routes.
	//
	// 13 of AuthHandler's 15 routes — login, refresh, the two login-page status
	// probes, the OIDC code exchange, the caller's own profile/password/session
	// routes and the console-token mint — are declared in
	// internal/api/registry_auth.go and mounted by mountRegistry above. The six
	// TOTP routes under /auth/totp are in internal/api/registry_totp.go and
	// /auth/oidc/authorize is in internal/api/registry_oidc.go.
	//
	// THREE routes stay here, for two different reasons.
	//
	// Register and Logout are mounted with authOptional, which the Permissions
	// vocabulary cannot express: it parses a session IF one is presented and
	// lets the request through either way. Public installs no authentication
	// middleware at all, so c.Locals("role") would be empty and Register would
	// refuse every admin-created account after the first; declaring them
	// authenticated would 401 the logout a valid refresh cookie must still be
	// able to perform once the access token has expired. Both keep their
	// hand-written bodies, and TestAuthOptionalRoutesAreStillLegacy pins that
	// this is a decision rather than a gap.
	//
	// The OIDC CALLBACK stays for a different reason: its query string is
	// composed by the IDENTITY PROVIDER, and the registry answers an undeclared
	// key with a 400 (PVE's additionalProperties => 0). RFC 9207 adds `iss`,
	// session management adds `session_state`, an error response carries
	// `error`/`error_description`, and a provider may add its own — so the
	// parameter set is open by construction and any declaration would turn
	// "this provider sends one extra parameter" into "SSO login returns 400".
	// TestOIDCCallbackIsStillLegacy pins that one.
	if s.authHandler != nil {
		authGroup := v1.Group("/auth")
		authGroup.Post("/register", s.authOptional(), s.authHandler.Register)
		// Logout intentionally uses authOptional so a user with an expired
		// access token (but a valid refresh cookie) can still revoke the
		// server-side session. The cookie itself is the auth artefact for
		// this endpoint; the user_id check in the handler only fires when an
		// access token IS present, defending against an attacker with a stolen
		// cookie attempting to log out an unrelated user (covered by
		// SameSite=Strict + same-origin SPA already, but defence-in-depth).
		authGroup.Post("/logout", s.authOptional(), s.authHandler.Logout)

		if s.oidcHandler != nil {
			authGroup.Get("/oidc/callback", s.oidcHandler.Callback)
		}
	}

	// Cluster routes — single group for the cluster-scoped endpoints that are
	// still legacy.
	//
	// ClusterHandler's own 7 routes are declared in
	// internal/api/registry_clusters.go and mounted by mountRegistry above,
	// together with the two rate limiters they carry, so there are no routes
	// for them here. The group survives because the legacy blocks below still
	// hang off it.
	if s.clusterHandler != nil {
		clusters := v1.Group("/clusters", s.authRequired())

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
		// The per-guest snapshot resync is declared in
		// internal/api/registry_guest_snapshots.go alongside the central
		// inventory listing and mounted by mountRegistry above, so there is no
		// block for it here.
		// The 18 container routes are declared in
		// internal/api/registry_containers.go and mounted by
		// mountRegistry above, so there is no block for them here.
		if s.vmFoldersHandler != nil {
			// The other 4 VM-folder routes are declared in
			// internal/api/registry_vm_folders.go and mounted by mountRegistry
			// above. This ONE stays here because apischema cannot express it:
			// its `parent_id` is a THREE-state field — absent leaves the folder
			// where it is, an explicit null moves it to the top level, a uuid
			// moves it under that folder — and apischema's present() reads an
			// explicit JSON null as ABSENT (validate.go). Declaring it would
			// silently turn "move this folder to the top level" into a request
			// that answers 200 and changes nothing. The handler keeps its
			// jsonNullUUID decoder and its hand-placed manage:vm_folder check,
			// and TestVMFolderReparentIsStillLegacy pins that it is a decision
			// rather than a gap.
			clusters.Patch("/:cluster_id/vm-folders/:folder_id", s.vmFoldersHandler.Update)
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
		// The 3 historical-metric routes are declared in
		// internal/api/registry_metrics.go and mounted by mountRegistry
		// above, so there is no block for them here.
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

		// The 4 scheduled-task routes are declared in
		// internal/api/registry_schedules.go and mounted by mountRegistry
		// above, so there is no block for them here.

		// The per-cluster audit listing is declared in
		// internal/api/registry_audit.go alongside the 8 instance-wide audit
		// routes and mounted by mountRegistry above, so there is no block for
		// it here.

		// The 19 rolling-update routes — 12 for the jobs and the node package
		// preview, 7 for the SSH credentials and pinned host keys they run
		// with — are declared in internal/api/registry_rolling_update.go and
		// mounted by mountRegistry above, so there is no block for them here.

		// The 9 cluster option, tag and corosync routes are declared in
		// internal/api/registry_cluster_options.go and mounted by
		// mountRegistry above, so there is no block for them here.

		// The 17 HA routes are declared in internal/api/registry_ha.go
		// and mounted by mountRegistry above, so there is no block for
		// them here.

		// The 4 resource-pool CRUD routes are declared in
		// internal/api/registry_pools.go and mounted by mountRegistry above,
		// so there is no block for them here. The pool LISTING is VMHandler's
		// and lives in internal/api/registry_vms.go.

		// The 25 Proxmox access-control routes — PVE users, API tokens,
		// groups, roles, ACLs and the read-only realm listing — are declared
		// in internal/api/registry_access.go and mounted by mountRegistry
		// above, so there is no block for them here.

		// The 8 replication routes are declared in
		// internal/api/registry_replication.go and mounted by
		// mountRegistry above, so there is no block for them here.

		// The 18 ACME routes — accounts, challenge plugins, the directory and
		// challenge-schema catalogues, and the per-node certificate and
		// acme-config routes — are declared in internal/api/registry_acme.go
		// and mounted by mountRegistry above, so there is no block for them
		// here.

		// The 3 APT repository routes are declared in
		// internal/api/registry_apt_repositories.go and the 5 metric-server
		// routes in internal/api/registry_metric_servers.go. Both are mounted
		// by mountRegistry above, so there is no block for either here.
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

	// The 5 notification dead-letter queue routes are declared in
	// internal/api/registry_notification_dlq.go and mounted by mountRegistry
	// above, so there is no group for them here.

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

	// The 9 audit-log routes — 8 instance-wide and the per-cluster listing —
	// are declared in internal/api/registry_audit.go and mounted by
	// mountRegistry above, so there is no group for them here.

	// The 4 task-history routes are declared in
	// internal/api/registry_tasks.go and mounted by mountRegistry above, so
	// there is no group for them here.

	// The central guest snapshot inventory is declared in
	// internal/api/registry_guest_snapshots.go and mounted by mountRegistry
	// above, so there is no group for it here.

	// The 8 virtio-win routes — the instance-wide catalog and download
	// source, and the 5 per-cluster policy routes — are declared in
	// internal/api/registry_virtio_win.go and mounted by mountRegistry
	// above, so there is no block for them here.

	// The 10 RBAC routes — Nexara's own roles, the permission catalogue, the
	// per-user assignments and the caller's own grants — are declared in
	// internal/api/registry_rbac.go and mounted by mountRegistry above, so
	// there is no group for them here.

	// The 7 LDAP directory routes are declared in
	// internal/api/registry_ldap.go and mounted by mountRegistry above, so
	// there is no group for them here.

	// The 6 OIDC provider-configuration routes are declared in
	// internal/api/registry_oidc.go, alongside the anonymous /auth/oidc/authorize
	// that starts the login redirect, and mounted by mountRegistry above.

	// Global search is declared in internal/api/registry_search.go and
	// mounted by mountRegistry above, so there is no route for it here.

	// The 3 per-user favorites routes are declared in
	// internal/api/registry_favorites.go and mounted by mountRegistry above,
	// so there is no group for them here.

	// The 6 API key routes — 4 for the caller's own keys and 2 for the
	// instance-wide admin view — are declared in
	// internal/api/registry_api_keys.go and mounted by mountRegistry above, so
	// there are no groups for them here.

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
	//
	// THREE of the nine are declared in internal/api/registry_settings.go — the
	// two branding uploads, whose manage:settings check is unconditional, and
	// the delete, which is Deferred because its check depends on the ?scope= the
	// caller sends. The SIX below stay here, and each for its own reason. See
	// registerSettingsEndpoints for the full account and
	// TestSettingsReadsAreStillLegacy, which pins every one of them.
	//
	//   - GET /settings and GET /settings/:key perform NO permission check on
	//     any path: settingScopeID gates only when `write && adminOnly`, and
	//     both pass write=false. They are instanceSharedRoutes-shaped for
	//     ?scope=global and self-service for ?scope=user, chosen per request,
	//     and Permissions has no shape for either half — declaring them Deferred
	//     would render as "deferred" to an operator, which claims a runtime
	//     check that does not exist. The reads being ungated is reported as a
	//     GAP in its own right rather than closed here: adding a gate is a
	//     behaviour change that could break a working deployment, and it is not
	//     this migration's to make.
	//   - PUT /settings/:key is the same conditional as the delete and would
	//     declare the same Deferred, but its `value` is arbitrary JSON — the
	//     branding page stores a string, the appearance page an object — and
	//     apischema's Type vocabulary has no "any JSON value" member.
	//   - GET /settings/branding and the two branding file routes are
	//     instanceSharedRoutes-shaped: instance data identical for every caller,
	//     with no subject to authorize. Permissions has no shape for that
	//     either, and the comment on instanceSharedRoutes explains why folding
	//     them into selfServiceRoutes would make that list's invariant false.
	if s.settingsHandler != nil {
		settings := v1.Group("/settings", s.authRequired())
		settings.Get("/", s.settingsHandler.ListSettings)
		settings.Get("/branding", s.settingsHandler.GetBranding)
		settings.Get("/branding/logo-file", s.settingsHandler.ServeLogo)
		settings.Get("/branding/favicon-file", s.settingsHandler.ServeFavicon)
		settings.Get("/:key", s.settingsHandler.GetSetting)
		settings.Put("/:key", s.settingsHandler.UpsertSetting)
	}

	// The 4 user-management routes are declared in
	// internal/api/registry_users.go, and the admin TOTP reset that shares
	// their path prefix in internal/api/registry_totp.go — it is TOTPHandler's
	// route, not UserHandler's, and nesting it in the users block used to mean
	// a Server holding one handler but not the other silently dropped it. Both
	// are mounted by mountRegistry above, so there is no group for them here.
}
