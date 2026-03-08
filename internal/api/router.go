package api

func (s *Server) setupRoutes() {
	// Health probe — not rate-limited, not behind /api/v1.
	s.app.Get("/healthz", s.handleHealthz)

	// API v1 group.
	v1 := s.app.Group("/api/v1")
	v1.Get("/version", s.handleVersion)

	// Auth routes.
	if s.authHandler != nil {
		authGroup := v1.Group("/auth")
		authGroup.Post("/register", s.authOptional(), s.authHandler.Register)
		authGroup.Post("/login", s.authHandler.Login)
		authGroup.Post("/refresh", s.authHandler.Refresh)
		authGroup.Post("/logout", s.authRequired(), s.authHandler.Logout)
		authGroup.Post("/logout-all", s.authRequired(), s.authHandler.LogoutAll)
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

	// Cluster routes.
	if s.clusterHandler != nil {
		clusters := v1.Group("/clusters", s.authRequired())
		clusters.Post("/", s.clusterHandler.Create)
		clusters.Post("/fetch-fingerprint", s.clusterHandler.FetchFingerprint)
		clusters.Get("/", s.clusterHandler.List)
		clusters.Get("/:id", s.clusterHandler.Get)
		clusters.Put("/:id", s.clusterHandler.Update)
		clusters.Delete("/:id", s.clusterHandler.Delete)

		// Nested resources by cluster.
		if s.pbsHandler != nil {
			clusters.Get("/:cluster_id/pbs-servers", s.pbsHandler.ListByCluster)
		}
		if s.nodeHandler != nil {
			clusters.Get("/:cluster_id/nodes", s.nodeHandler.ListByCluster)
		}
		if s.vmHandler != nil {
			clusters.Get("/:cluster_id/vms", s.vmHandler.ListByCluster)
			clusters.Post("/:cluster_id/vms", s.vmHandler.CreateVM)
			clusters.Get("/:cluster_id/vms/:vm_id", s.vmHandler.GetVM)
			clusters.Post("/:cluster_id/vms/:vm_id/status", s.vmHandler.PerformAction)
			clusters.Post("/:cluster_id/vms/:vm_id/clone", s.vmHandler.CloneVM)
			clusters.Post("/:cluster_id/vms/:vm_id/migrate", s.vmHandler.MigrateVM)
			clusters.Delete("/:cluster_id/vms/:vm_id", s.vmHandler.DestroyVM)
			clusters.Get("/:cluster_id/vms/:vm_id/snapshots", s.vmHandler.ListSnapshots)
			clusters.Post("/:cluster_id/vms/:vm_id/snapshots", s.vmHandler.CreateSnapshot)
			clusters.Delete("/:cluster_id/vms/:vm_id/snapshots/:snap_name", s.vmHandler.DeleteSnapshot)
			clusters.Post("/:cluster_id/vms/:vm_id/snapshots/:snap_name/rollback", s.vmHandler.RollbackSnapshot)
			clusters.Get("/:cluster_id/vms/:vm_id/config", s.vmHandler.GetVMConfig)
			clusters.Put("/:cluster_id/vms/:vm_id/config", s.vmHandler.SetVMConfig)
			clusters.Get("/:cluster_id/vms/:vm_id/agent", s.vmHandler.GetGuestAgentInfo)
			clusters.Get("/:cluster_id/tasks/:upid", s.vmHandler.GetTaskStatus)
			clusters.Get("/:cluster_id/tasks/:upid/log", s.vmHandler.GetTaskLog)
		}
		if s.containerHandler != nil {
			clusters.Get("/:cluster_id/containers", s.containerHandler.ListByCluster)
			clusters.Post("/:cluster_id/containers", s.containerHandler.CreateContainer)
			clusters.Get("/:cluster_id/containers/:ct_id", s.containerHandler.GetContainer)
			clusters.Post("/:cluster_id/containers/:ct_id/status", s.containerHandler.PerformAction)
			clusters.Post("/:cluster_id/containers/:ct_id/clone", s.containerHandler.CloneContainer)
			clusters.Post("/:cluster_id/containers/:ct_id/migrate", s.containerHandler.MigrateContainer)
			clusters.Delete("/:cluster_id/containers/:ct_id", s.containerHandler.DestroyContainer)
			clusters.Get("/:cluster_id/containers/:ct_id/snapshots", s.containerHandler.ListSnapshots)
			clusters.Post("/:cluster_id/containers/:ct_id/snapshots", s.containerHandler.CreateSnapshot)
			clusters.Delete("/:cluster_id/containers/:ct_id/snapshots/:snap_name", s.containerHandler.DeleteSnapshot)
			clusters.Post("/:cluster_id/containers/:ct_id/snapshots/:snap_name/rollback", s.containerHandler.RollbackSnapshot)
			clusters.Put("/:cluster_id/containers/:ct_id/config", s.containerHandler.SetContainerConfig)
			clusters.Post("/:cluster_id/containers/:ct_id/volumes/move", s.containerHandler.MoveVolume)
		}
		if s.vmHandler != nil {
			clusters.Post("/:cluster_id/vms/:vm_id/disks/resize", s.vmHandler.ResizeDisk)
			clusters.Post("/:cluster_id/vms/:vm_id/disks/move", s.vmHandler.MoveDisk)
			clusters.Post("/:cluster_id/vms/:vm_id/disks/attach", s.vmHandler.AttachDisk)
			clusters.Post("/:cluster_id/vms/:vm_id/disks/detach", s.vmHandler.DetachDisk)
			clusters.Get("/:cluster_id/nodes/:node_name/bridges", s.vmHandler.ListBridges)
			clusters.Get("/:cluster_id/nodes/:node_name/hardware/usb", s.vmHandler.ListNodeUSBDevices)
			clusters.Get("/:cluster_id/nodes/:node_name/hardware/pci", s.vmHandler.ListNodePCIDevices)
			clusters.Get("/:cluster_id/nodes/:node_name/machine-types", s.vmHandler.ListMachineTypes)
			clusters.Get("/:cluster_id/nodes/:node_name/isos", s.vmHandler.ListNodeISOs)
			clusters.Post("/:cluster_id/vms/:vm_id/media", s.vmHandler.ChangeMedia)
			clusters.Get("/:cluster_id/pools", s.vmHandler.ListResourcePools)
		}
		if s.storageHandler != nil {
			clusters.Get("/:cluster_id/storage", s.storageHandler.ListByCluster)
			clusters.Post("/:cluster_id/storage", s.storageHandler.Create)
			clusters.Get("/:cluster_id/storage/:storage_id/config", s.storageHandler.GetConfig)
			clusters.Put("/:cluster_id/storage/:storage_id", s.storageHandler.Update)
			clusters.Delete("/:cluster_id/storage/:storage_id", s.storageHandler.Delete)
			clusters.Get("/:cluster_id/storage/:storage_id/content", s.storageHandler.GetContent)
			clusters.Post("/:cluster_id/storage/:storage_id/upload", s.storageHandler.UploadFile)
			clusters.Delete("/:cluster_id/storage/:storage_id/content/*", s.storageHandler.DeleteContent)
		}
		if s.metricsHandler != nil {
			clusters.Get("/:cluster_id/metrics", s.metricsHandler.GetClusterHistorical)
			clusters.Get("/:cluster_id/vms/:vm_id/metrics", s.metricsHandler.GetVMHistorical)
			clusters.Get("/:cluster_id/nodes/:node_id/metrics", s.metricsHandler.GetNodeHistorical)
		}
		if s.cephHandler != nil {
			ceph := clusters.Group("/:cluster_id/ceph")
			ceph.Get("/status", s.cephHandler.GetStatus)
			ceph.Get("/osds", s.cephHandler.ListOSDs)
			ceph.Get("/pools", s.cephHandler.ListPools)
			ceph.Get("/monitors", s.cephHandler.ListMonitors)
			ceph.Get("/fs", s.cephHandler.ListFS)
			ceph.Get("/rules", s.cephHandler.ListCrushRules)
			ceph.Post("/pools", s.cephHandler.CreatePool)
			ceph.Delete("/pools/:pool_name", s.cephHandler.DeletePool)
			ceph.Get("/metrics", s.cephHandler.GetHistorical)
			ceph.Get("/osds/metrics", s.cephHandler.GetOSDMetrics)
			ceph.Get("/pools/metrics", s.cephHandler.GetPoolMetrics)
		}
	}

	// Network, Firewall, SDN routes under clusters.
	if s.networkHandler != nil && s.clusterHandler != nil {
		netClusters := v1.Group("/clusters", s.authRequired())
		netClusters.Get("/:cluster_id/networks", s.networkHandler.ListNetworkInterfaces)
		netClusters.Get("/:cluster_id/networks/:node_name", s.networkHandler.ListNodeNetworkInterfaces)
		netClusters.Post("/:cluster_id/networks/:node_name", s.networkHandler.CreateNetworkInterface)
		netClusters.Put("/:cluster_id/networks/:node_name/:iface", s.networkHandler.UpdateNetworkInterface)
		netClusters.Delete("/:cluster_id/networks/:node_name/:iface", s.networkHandler.DeleteNetworkInterface)
		netClusters.Post("/:cluster_id/networks/:node_name/apply", s.networkHandler.ApplyNetworkConfig)
		netClusters.Post("/:cluster_id/networks/:node_name/revert", s.networkHandler.RevertNetworkConfig)

		netClusters.Get("/:cluster_id/firewall/rules", s.networkHandler.ListClusterFirewallRules)
		netClusters.Post("/:cluster_id/firewall/rules", s.networkHandler.CreateClusterFirewallRule)
		netClusters.Put("/:cluster_id/firewall/rules/:pos", s.networkHandler.UpdateClusterFirewallRule)
		netClusters.Delete("/:cluster_id/firewall/rules/:pos", s.networkHandler.DeleteClusterFirewallRule)
		netClusters.Get("/:cluster_id/firewall/options", s.networkHandler.GetFirewallOptions)
		netClusters.Put("/:cluster_id/firewall/options", s.networkHandler.SetFirewallOptions)

		netClusters.Get("/:cluster_id/vms/:vm_id/firewall/rules", s.networkHandler.ListVMFirewallRules)
		netClusters.Post("/:cluster_id/vms/:vm_id/firewall/rules", s.networkHandler.CreateVMFirewallRule)
		netClusters.Put("/:cluster_id/vms/:vm_id/firewall/rules/:pos", s.networkHandler.UpdateVMFirewallRule)
		netClusters.Delete("/:cluster_id/vms/:vm_id/firewall/rules/:pos", s.networkHandler.DeleteVMFirewallRule)

		netClusters.Get("/:cluster_id/sdn/zones", s.networkHandler.ListSDNZones)
		netClusters.Post("/:cluster_id/sdn/zones", s.networkHandler.CreateSDNZone)
		netClusters.Put("/:cluster_id/sdn/zones/:zone", s.networkHandler.UpdateSDNZone)
		netClusters.Delete("/:cluster_id/sdn/zones/:zone", s.networkHandler.DeleteSDNZone)
		netClusters.Get("/:cluster_id/sdn/vnets", s.networkHandler.ListSDNVNets)
		netClusters.Post("/:cluster_id/sdn/vnets", s.networkHandler.CreateSDNVNet)
		netClusters.Put("/:cluster_id/sdn/vnets/:vnet", s.networkHandler.UpdateSDNVNet)
		netClusters.Delete("/:cluster_id/sdn/vnets/:vnet", s.networkHandler.DeleteSDNVNet)
		netClusters.Get("/:cluster_id/sdn/vnets/:vnet/subnets", s.networkHandler.ListSDNSubnets)
		netClusters.Post("/:cluster_id/sdn/vnets/:vnet/subnets", s.networkHandler.CreateSDNSubnet)
		netClusters.Put("/:cluster_id/sdn/vnets/:vnet/subnets/:subnet", s.networkHandler.UpdateSDNSubnet)
		netClusters.Delete("/:cluster_id/sdn/vnets/:vnet/subnets/:subnet", s.networkHandler.DeleteSDNSubnet)
		netClusters.Put("/:cluster_id/sdn/apply", s.networkHandler.ApplySDN)

		netClusters.Post("/:cluster_id/firewall-templates/:id/apply", s.networkHandler.ApplyTemplate)
	}

	// Firewall template routes (not cluster-scoped).
	if s.networkHandler != nil {
		templates := v1.Group("/firewall-templates", s.authRequired())
		templates.Get("/", s.networkHandler.ListTemplates)
		templates.Post("/", s.networkHandler.CreateTemplate)
		templates.Get("/:id", s.networkHandler.GetTemplate)
		templates.Put("/:id", s.networkHandler.UpdateTemplate)
		templates.Delete("/:id", s.networkHandler.DeleteTemplate)
	}

	// Migration routes.
	if s.migrationHandler != nil {
		migrations := v1.Group("/migrations", s.authRequired())
		migrations.Post("/", s.migrationHandler.Create)
		migrations.Get("/", s.migrationHandler.List)
		migrations.Get("/:id", s.migrationHandler.Get)
		migrations.Post("/:id/check", s.migrationHandler.RunCheck)
		migrations.Post("/:id/execute", s.migrationHandler.Execute)
		migrations.Post("/:id/cancel", s.migrationHandler.Cancel)
	}

	// CVE scanning routes.
	if s.cveHandler != nil && s.clusterHandler != nil {
		cveClusters := v1.Group("/clusters", s.authRequired())
		cveClusters.Get("/:cluster_id/cve-scans", s.cveHandler.ListScans)
		cveClusters.Post("/:cluster_id/cve-scans", s.cveHandler.TriggerScan)
		cveClusters.Get("/:cluster_id/cve-scans/:scan_id", s.cveHandler.GetScan)
		cveClusters.Get("/:cluster_id/cve-scans/:scan_id/vulnerabilities", s.cveHandler.ListVulnerabilities)
		cveClusters.Delete("/:cluster_id/cve-scans/:scan_id", s.cveHandler.DeleteScan)
		cveClusters.Get("/:cluster_id/security-posture", s.cveHandler.GetSecurityPosture)
		cveClusters.Get("/:cluster_id/cve-scan-schedule", s.cveHandler.GetSchedule)
		cveClusters.Put("/:cluster_id/cve-scan-schedule", s.cveHandler.UpdateSchedule)
	}

	// Alert routes.
	if s.alertHandler != nil {
		alerts := v1.Group("/alerts", s.authRequired())
		alerts.Get("/", s.alertHandler.ListAlerts)
		alerts.Get("/summary", s.alertHandler.GetAlertSummary)
		alerts.Get("/:id", s.alertHandler.GetAlert)
		alerts.Post("/:id/acknowledge", s.alertHandler.AcknowledgeAlert)
		alerts.Post("/:id/resolve", s.alertHandler.ResolveAlert)

		alertRules := v1.Group("/alert-rules", s.authRequired())
		alertRules.Get("/", s.alertHandler.ListRules)
		alertRules.Post("/", s.alertHandler.CreateRule)
		alertRules.Get("/:id", s.alertHandler.GetRule)
		alertRules.Put("/:id", s.alertHandler.UpdateRule)
		alertRules.Delete("/:id", s.alertHandler.DeleteRule)

		notifChannels := v1.Group("/notification-channels", s.authRequired())
		notifChannels.Get("/", s.alertHandler.ListChannels)
		notifChannels.Post("/", s.alertHandler.CreateChannel)
		notifChannels.Get("/:id", s.alertHandler.GetChannel)
		notifChannels.Put("/:id", s.alertHandler.UpdateChannel)
		notifChannels.Delete("/:id", s.alertHandler.DeleteChannel)
		notifChannels.Post("/:id/test", s.alertHandler.TestChannel)

		// Cluster-scoped alert routes
		if s.clusterHandler != nil {
			alertClusters := v1.Group("/clusters", s.authRequired())
			alertClusters.Get("/:cluster_id/alerts", s.alertHandler.ListAlertsByCluster)
			alertClusters.Get("/:cluster_id/alerts/count", s.alertHandler.CountActiveAlertsByCluster)
			alertClusters.Get("/:cluster_id/maintenance-windows", s.alertHandler.ListMaintenanceWindows)
			alertClusters.Post("/:cluster_id/maintenance-windows", s.alertHandler.CreateMaintenanceWindow)
			alertClusters.Put("/:cluster_id/maintenance-windows/:id", s.alertHandler.UpdateMaintenanceWindow)
			alertClusters.Delete("/:cluster_id/maintenance-windows/:id", s.alertHandler.DeleteMaintenanceWindow)
		}
	}

	// Report routes.
	if s.reportHandler != nil {
		rpts := v1.Group("/reports", s.authRequired())
		rpts.Get("/schedules", s.reportHandler.ListSchedules)
		rpts.Post("/schedules", s.reportHandler.CreateSchedule)
		rpts.Get("/schedules/:id", s.reportHandler.GetSchedule)
		rpts.Put("/schedules/:id", s.reportHandler.UpdateSchedule)
		rpts.Delete("/schedules/:id", s.reportHandler.DeleteSchedule)
		rpts.Post("/generate", s.reportHandler.GenerateReport)
		rpts.Get("/runs", s.reportHandler.ListRuns)
		rpts.Get("/runs/:id", s.reportHandler.GetRun)
		rpts.Get("/runs/:id/html", s.reportHandler.GetRunHTML)
		rpts.Get("/runs/:id/csv", s.reportHandler.GetRunCSV)
	}

	// DRS routes.
	if s.drsHandler != nil && s.clusterHandler != nil {
		drsClusters := v1.Group("/clusters", s.authRequired())
		drsClusters.Get("/:cluster_id/drs/config", s.drsHandler.GetConfig)
		drsClusters.Put("/:cluster_id/drs/config", s.drsHandler.UpdateConfig)
		drsClusters.Get("/:cluster_id/drs/rules", s.drsHandler.ListRules)
		drsClusters.Post("/:cluster_id/drs/rules", s.drsHandler.CreateRule)
		drsClusters.Delete("/:cluster_id/drs/rules/:rule_id", s.drsHandler.DeleteRule)
		drsClusters.Post("/:cluster_id/drs/evaluate", s.drsHandler.TriggerEvaluate)
		drsClusters.Get("/:cluster_id/drs/history", s.drsHandler.ListHistory)
		drsClusters.Get("/:cluster_id/drs/ha-rules", s.drsHandler.ListHARules)
		drsClusters.Post("/:cluster_id/drs/ha-rules", s.drsHandler.CreateHARule)
		drsClusters.Delete("/:cluster_id/drs/ha-rules/:rule_name", s.drsHandler.DeleteHARule)
	}

	// PBS server routes.
	if s.pbsHandler != nil {
		pbs := v1.Group("/pbs-servers", s.authRequired())
		pbs.Post("/", s.pbsHandler.Create)
		pbs.Get("/", s.pbsHandler.List)
		pbs.Get("/:id", s.pbsHandler.Get)
		pbs.Put("/:id", s.pbsHandler.Update)
		pbs.Delete("/:id", s.pbsHandler.Delete)

		// Backup management routes nested under PBS servers.
		if s.backupHandler != nil {
			pbs.Get("/:pbs_id/datastores", s.backupHandler.ListDatastores)
			pbs.Get("/:pbs_id/datastores/status", s.backupHandler.GetDatastoreStatus)
			pbs.Post("/:pbs_id/datastores/:store/gc", s.backupHandler.TriggerGC)
			pbs.Delete("/:pbs_id/datastores/:store/snapshots", s.backupHandler.DeleteSnapshot)
			pbs.Put("/:pbs_id/datastores/:store/snapshots/protect", s.backupHandler.ProtectSnapshot)
			pbs.Put("/:pbs_id/datastores/:store/snapshots/notes", s.backupHandler.UpdateSnapshotNotes)
			pbs.Post("/:pbs_id/datastores/:store/prune", s.backupHandler.PruneDatastore)
			pbs.Get("/:pbs_id/datastores/:store/rrd", s.backupHandler.GetDatastoreRRD)
			pbs.Get("/:pbs_id/datastores/:store/config", s.backupHandler.GetDatastoreConfig)
			pbs.Get("/:pbs_id/snapshots", s.backupHandler.ListSnapshots)
			pbs.Get("/:pbs_id/sync-jobs", s.backupHandler.ListSyncJobs)
			pbs.Post("/:pbs_id/sync-jobs/:job_id/run", s.backupHandler.RunSyncJob)
			pbs.Get("/:pbs_id/verify-jobs", s.backupHandler.ListVerifyJobs)
			pbs.Post("/:pbs_id/verify-jobs/:job_id/run", s.backupHandler.RunVerifyJob)
			pbs.Get("/:pbs_id/tasks", s.backupHandler.ListTasks)
			pbs.Get("/:pbs_id/tasks/:upid", s.backupHandler.GetTaskStatus)
			pbs.Get("/:pbs_id/tasks/:upid/log", s.backupHandler.GetTaskLog)
			pbs.Get("/:pbs_id/metrics", s.backupHandler.GetDatastoreMetrics)
		}
	}

	// Migration routes under clusters.
	if s.migrationHandler != nil && s.clusterHandler != nil {
		migClusters := v1.Group("/clusters", s.authRequired())
		migClusters.Get("/:cluster_id/migrations", s.migrationHandler.ListByCluster)
	}

	// PBS snapshot lookup (cross-server, by backup_id / VMID).
	if s.backupHandler != nil {
		v1.Get("/pbs-snapshots", s.authRequired(), s.backupHandler.ListSnapshotsByBackupID)
		v1.Get("/backup-coverage", s.authRequired(), s.backupHandler.GetBackupCoverage)
	}

	// Restore and backup job routes under clusters.
	if s.backupHandler != nil && s.clusterHandler != nil {
		clusters := v1.Group("/clusters", s.authRequired())
		clusters.Post("/:cluster_id/restore", s.backupHandler.RestoreBackup)
		clusters.Post("/:cluster_id/backup", s.backupHandler.TriggerBackup)
		clusters.Get("/:cluster_id/backup-jobs", s.backupHandler.ListBackupJobs)
		clusters.Post("/:cluster_id/backup-jobs", s.backupHandler.CreateBackupJob)
		clusters.Put("/:cluster_id/backup-jobs/:job_id", s.backupHandler.UpdateBackupJob)
		clusters.Delete("/:cluster_id/backup-jobs/:job_id", s.backupHandler.DeleteBackupJob)
		clusters.Post("/:cluster_id/backup-jobs/:job_id/run", s.backupHandler.RunBackupJob)
	}

	// Schedule routes (under clusters).
	if s.scheduleHandler != nil && s.clusterHandler != nil {
		schedClusters := v1.Group("/clusters", s.authRequired())
		schedClusters.Post("/:cluster_id/schedules", s.scheduleHandler.Create)
		schedClusters.Get("/:cluster_id/schedules", s.scheduleHandler.List)
		schedClusters.Put("/:cluster_id/schedules/:id", s.scheduleHandler.Update)
		schedClusters.Delete("/:cluster_id/schedules/:id", s.scheduleHandler.Delete)
	}

	// Audit log routes.
	if s.auditHandler != nil {
		audit := v1.Group("/audit-log", s.authRequired())
		audit.Get("/recent", s.auditHandler.ListRecent)
		audit.Get("/", s.auditHandler.List)

		if s.clusterHandler != nil {
			auditClusters := v1.Group("/clusters", s.authRequired())
			auditClusters.Get("/:cluster_id/audit-log", s.auditHandler.ListByCluster)
		}
	}

	// Task history routes.
	if s.taskHandler != nil {
		tasks := v1.Group("/tasks", s.authRequired())
		tasks.Get("/", s.taskHandler.List)
		tasks.Post("/", s.taskHandler.Create)
		tasks.Put("/:upid", s.taskHandler.Update)
		tasks.Delete("/", s.taskHandler.ClearCompleted)
	}

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

	// Rolling update routes.
	if s.rollingUpdateHandler != nil && s.clusterHandler != nil {
		ruClusters := v1.Group("/clusters", s.authRequired())
		ruClusters.Get("/:cluster_id/rolling-updates", s.rollingUpdateHandler.ListJobs)
		ruClusters.Post("/:cluster_id/rolling-updates", s.rollingUpdateHandler.CreateJob)
		ruClusters.Get("/:cluster_id/rolling-updates/:id", s.rollingUpdateHandler.GetJob)
		ruClusters.Post("/:cluster_id/rolling-updates/:id/start", s.rollingUpdateHandler.StartJob)
		ruClusters.Post("/:cluster_id/rolling-updates/:id/cancel", s.rollingUpdateHandler.CancelJob)
		ruClusters.Post("/:cluster_id/rolling-updates/:id/pause", s.rollingUpdateHandler.PauseJob)
		ruClusters.Post("/:cluster_id/rolling-updates/:id/resume", s.rollingUpdateHandler.ResumeJob)
		ruClusters.Get("/:cluster_id/rolling-updates/:id/nodes", s.rollingUpdateHandler.ListNodes)
		ruClusters.Post("/:cluster_id/rolling-updates/:id/nodes/:node_id/confirm-upgrade", s.rollingUpdateHandler.ConfirmUpgrade)
		ruClusters.Post("/:cluster_id/rolling-updates/:id/nodes/:node_id/skip", s.rollingUpdateHandler.SkipNode)
		ruClusters.Post("/:cluster_id/rolling-updates/preflight-ha", s.rollingUpdateHandler.PreflightHA)
		ruClusters.Get("/:cluster_id/nodes/:node/packages", s.rollingUpdateHandler.PreviewPackages)

		// SSH credential management.
		ruClusters.Get("/:cluster_id/ssh-credentials", s.rollingUpdateHandler.GetSSHCredentials)
		ruClusters.Put("/:cluster_id/ssh-credentials", s.rollingUpdateHandler.UpsertSSHCredentials)
		ruClusters.Delete("/:cluster_id/ssh-credentials", s.rollingUpdateHandler.DeleteSSHCredentials)
		ruClusters.Post("/:cluster_id/ssh-credentials/test", s.rollingUpdateHandler.TestSSHConnection)
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
