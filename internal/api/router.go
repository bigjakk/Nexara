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
			clusters.Get("/:cluster_id/nodes/:node_id/disks", s.nodeHandler.ListNodeDisks)
			clusters.Get("/:cluster_id/nodes/:node_id/network-interfaces", s.nodeHandler.ListNodeNetworkInterfaces)
			clusters.Get("/:cluster_id/nodes/:node_id/pci-devices", s.nodeHandler.ListNodePCIDevices)

			// Node management (DNS, Time, Power) — use node_name (Proxmox name) not UUID.
			clusters.Get("/:cluster_id/nodes/:node_name/dns", s.nodeHandler.GetNodeDNS)
			clusters.Put("/:cluster_id/nodes/:node_name/dns", s.nodeHandler.SetNodeDNS)
			clusters.Get("/:cluster_id/nodes/:node_name/time", s.nodeHandler.GetNodeTime)
			clusters.Put("/:cluster_id/nodes/:node_name/time", s.nodeHandler.SetNodeTimezone)
			clusters.Post("/:cluster_id/nodes/:node_name/shutdown", s.nodeHandler.ShutdownNode)
			clusters.Post("/:cluster_id/nodes/:node_name/reboot", s.nodeHandler.RebootNode)

			// Node disk management (SMART, ZFS, LVM, LVMthin, Init, Wipe).
			clusters.Get("/:cluster_id/nodes/:node_name/disks/list", s.nodeHandler.ListLiveDisks)
			clusters.Get("/:cluster_id/nodes/:node_name/disks/smart", s.nodeHandler.GetDiskSMART)
			clusters.Get("/:cluster_id/nodes/:node_name/disks/zfs", s.nodeHandler.ListZFSPools)
			clusters.Post("/:cluster_id/nodes/:node_name/disks/zfs", s.nodeHandler.CreateZFSPool)
			clusters.Get("/:cluster_id/nodes/:node_name/disks/lvm", s.nodeHandler.ListLVM)
			clusters.Post("/:cluster_id/nodes/:node_name/disks/lvm", s.nodeHandler.CreateLVM)
			clusters.Get("/:cluster_id/nodes/:node_name/disks/lvmthin", s.nodeHandler.ListLVMThin)
			clusters.Post("/:cluster_id/nodes/:node_name/disks/lvmthin", s.nodeHandler.CreateLVMThin)
			clusters.Get("/:cluster_id/nodes/:node_name/disks/directory", s.nodeHandler.ListDirectories)
			clusters.Post("/:cluster_id/nodes/:node_name/disks/directory", s.nodeHandler.CreateDirectory)
			clusters.Post("/:cluster_id/nodes/:node_name/disks/initgpt", s.nodeHandler.InitializeGPT)
			clusters.Put("/:cluster_id/nodes/:node_name/disks/wipe", s.nodeHandler.WipeDisk)

			// Node services.
			clusters.Get("/:cluster_id/nodes/:node_name/services", s.nodeHandler.ListNodeServices)
			clusters.Post("/:cluster_id/nodes/:node_name/services/:service/:action", s.nodeHandler.ServiceAction)

			// Node syslog.
			clusters.Get("/:cluster_id/nodes/:node_name/syslog", s.nodeHandler.GetNodeSyslog)

			// Node firewall.
			clusters.Get("/:cluster_id/nodes/:node_name/firewall/rules", s.nodeHandler.ListNodeFirewallRules)
			clusters.Post("/:cluster_id/nodes/:node_name/firewall/rules", s.nodeHandler.CreateNodeFirewallRule)
			clusters.Put("/:cluster_id/nodes/:node_name/firewall/rules/:pos", s.nodeHandler.UpdateNodeFirewallRule)
			clusters.Delete("/:cluster_id/nodes/:node_name/firewall/rules/:pos", s.nodeHandler.DeleteNodeFirewallRule)
			clusters.Get("/:cluster_id/nodes/:node_name/firewall/log", s.nodeHandler.GetNodeFirewallLog)

			// Node bulk operations.
			clusters.Post("/:cluster_id/nodes/:node_name/evacuate", s.nodeHandler.EvacuateNode)
		}
		if s.vmHandler != nil {
			clusters.Get("/:cluster_id/vms", s.vmHandler.ListByCluster)
			clusters.Post("/:cluster_id/vms", s.vmHandler.CreateVM)
			clusters.Get("/:cluster_id/vms/:vm_id", s.vmHandler.GetVM)
			clusters.Post("/:cluster_id/vms/:vm_id/status", s.vmHandler.PerformAction)
			clusters.Post("/:cluster_id/vms/:vm_id/clone", s.vmHandler.CloneVM)
			clusters.Post("/:cluster_id/vms/:vm_id/convert-to-template", s.vmHandler.ConvertToTemplate)
			clusters.Post("/:cluster_id/vms/:vm_id/clone-to-template", s.vmHandler.CloneToTemplate)
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
			clusters.Post("/:cluster_id/containers/:ct_id/convert-to-template", s.containerHandler.ConvertToTemplate)
			clusters.Post("/:cluster_id/containers/:ct_id/clone-to-template", s.containerHandler.CloneToTemplate)
			clusters.Post("/:cluster_id/containers/:ct_id/migrate", s.containerHandler.MigrateContainer)
			clusters.Delete("/:cluster_id/containers/:ct_id", s.containerHandler.DestroyContainer)
			clusters.Get("/:cluster_id/containers/:ct_id/snapshots", s.containerHandler.ListSnapshots)
			clusters.Post("/:cluster_id/containers/:ct_id/snapshots", s.containerHandler.CreateSnapshot)
			clusters.Delete("/:cluster_id/containers/:ct_id/snapshots/:snap_name", s.containerHandler.DeleteSnapshot)
			clusters.Post("/:cluster_id/containers/:ct_id/snapshots/:snap_name/rollback", s.containerHandler.RollbackSnapshot)
			clusters.Get("/:cluster_id/containers/:ct_id/config", s.containerHandler.GetContainerConfig)
			clusters.Put("/:cluster_id/containers/:ct_id/config", s.containerHandler.SetContainerConfig)
			clusters.Post("/:cluster_id/containers/:ct_id/disks/resize", s.containerHandler.ResizeDisk)
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
			clusters.Get("/:cluster_id/nodes/:node_name/cpu-models", s.vmHandler.ListCPUModels)
			clusters.Get("/:cluster_id/nodes/:node_name/isos", s.vmHandler.ListNodeISOs)
			clusters.Post("/:cluster_id/vms/:vm_id/media", s.vmHandler.ChangeMedia)
			clusters.Put("/:cluster_id/vms/:vm_id/pool", s.vmHandler.SetVMPool)
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

		// Network, Firewall, SDN routes.
		if s.networkHandler != nil {
			clusters.Get("/:cluster_id/networks", s.networkHandler.ListNetworkInterfaces)
			clusters.Get("/:cluster_id/networks/:node_name", s.networkHandler.ListNodeNetworkInterfaces)
			clusters.Post("/:cluster_id/networks/:node_name", s.networkHandler.CreateNetworkInterface)
			clusters.Put("/:cluster_id/networks/:node_name/:iface", s.networkHandler.UpdateNetworkInterface)
			clusters.Delete("/:cluster_id/networks/:node_name/:iface", s.networkHandler.DeleteNetworkInterface)
			clusters.Post("/:cluster_id/networks/:node_name/apply", s.networkHandler.ApplyNetworkConfig)
			clusters.Post("/:cluster_id/networks/:node_name/revert", s.networkHandler.RevertNetworkConfig)

			clusters.Get("/:cluster_id/firewall/rules", s.networkHandler.ListClusterFirewallRules)
			clusters.Post("/:cluster_id/firewall/rules", s.networkHandler.CreateClusterFirewallRule)
			clusters.Put("/:cluster_id/firewall/rules/:pos", s.networkHandler.UpdateClusterFirewallRule)
			clusters.Delete("/:cluster_id/firewall/rules/:pos", s.networkHandler.DeleteClusterFirewallRule)
			clusters.Get("/:cluster_id/firewall/options", s.networkHandler.GetFirewallOptions)
			clusters.Put("/:cluster_id/firewall/options", s.networkHandler.SetFirewallOptions)

			clusters.Get("/:cluster_id/vms/:vm_id/firewall/rules", s.networkHandler.ListVMFirewallRules)
			clusters.Post("/:cluster_id/vms/:vm_id/firewall/rules", s.networkHandler.CreateVMFirewallRule)
			clusters.Put("/:cluster_id/vms/:vm_id/firewall/rules/:pos", s.networkHandler.UpdateVMFirewallRule)
			clusters.Delete("/:cluster_id/vms/:vm_id/firewall/rules/:pos", s.networkHandler.DeleteVMFirewallRule)

			clusters.Get("/:cluster_id/sdn/zones", s.networkHandler.ListSDNZones)
			clusters.Post("/:cluster_id/sdn/zones", s.networkHandler.CreateSDNZone)
			clusters.Put("/:cluster_id/sdn/zones/:zone", s.networkHandler.UpdateSDNZone)
			clusters.Delete("/:cluster_id/sdn/zones/:zone", s.networkHandler.DeleteSDNZone)
			clusters.Get("/:cluster_id/sdn/vnets", s.networkHandler.ListSDNVNets)
			clusters.Post("/:cluster_id/sdn/vnets", s.networkHandler.CreateSDNVNet)
			clusters.Put("/:cluster_id/sdn/vnets/:vnet", s.networkHandler.UpdateSDNVNet)
			clusters.Delete("/:cluster_id/sdn/vnets/:vnet", s.networkHandler.DeleteSDNVNet)
			clusters.Get("/:cluster_id/sdn/vnets/:vnet/subnets", s.networkHandler.ListSDNSubnets)
			clusters.Post("/:cluster_id/sdn/vnets/:vnet/subnets", s.networkHandler.CreateSDNSubnet)
			clusters.Put("/:cluster_id/sdn/vnets/:vnet/subnets/:subnet", s.networkHandler.UpdateSDNSubnet)
			clusters.Delete("/:cluster_id/sdn/vnets/:vnet/subnets/:subnet", s.networkHandler.DeleteSDNSubnet)
			clusters.Put("/:cluster_id/sdn/apply", s.networkHandler.ApplySDN)
			clusters.Get("/:cluster_id/sdn/controllers", s.networkHandler.ListSDNControllers)
			clusters.Post("/:cluster_id/sdn/controllers", s.networkHandler.CreateSDNController)
			clusters.Put("/:cluster_id/sdn/controllers/:controller", s.networkHandler.UpdateSDNController)
			clusters.Delete("/:cluster_id/sdn/controllers/:controller", s.networkHandler.DeleteSDNController)
			clusters.Get("/:cluster_id/sdn/ipams", s.networkHandler.ListSDNIPAMs)
			clusters.Post("/:cluster_id/sdn/ipams", s.networkHandler.CreateSDNIPAM)
			clusters.Put("/:cluster_id/sdn/ipams/:ipam", s.networkHandler.UpdateSDNIPAM)
			clusters.Delete("/:cluster_id/sdn/ipams/:ipam", s.networkHandler.DeleteSDNIPAM)
			clusters.Get("/:cluster_id/sdn/dns", s.networkHandler.ListSDNDNS)
			clusters.Post("/:cluster_id/sdn/dns", s.networkHandler.CreateSDNDNS)
			clusters.Put("/:cluster_id/sdn/dns/:dns", s.networkHandler.UpdateSDNDNS)
			clusters.Delete("/:cluster_id/sdn/dns/:dns", s.networkHandler.DeleteSDNDNS)

			clusters.Post("/:cluster_id/firewall-templates/:id/apply", s.networkHandler.ApplyTemplate)

			// Firewall extras (aliases, IPsets, security groups, log).
			clusters.Get("/:cluster_id/firewall/aliases", s.networkHandler.ListFirewallAliases)
			clusters.Post("/:cluster_id/firewall/aliases", s.networkHandler.CreateFirewallAlias)
			clusters.Put("/:cluster_id/firewall/aliases/:name", s.networkHandler.UpdateFirewallAlias)
			clusters.Delete("/:cluster_id/firewall/aliases/:name", s.networkHandler.DeleteFirewallAlias)
			clusters.Get("/:cluster_id/firewall/ipset", s.networkHandler.ListFirewallIPSets)
			clusters.Post("/:cluster_id/firewall/ipset", s.networkHandler.CreateFirewallIPSet)
			clusters.Delete("/:cluster_id/firewall/ipset/:name", s.networkHandler.DeleteFirewallIPSet)
			clusters.Get("/:cluster_id/firewall/ipset/:name/entries", s.networkHandler.ListFirewallIPSetEntries)
			clusters.Post("/:cluster_id/firewall/ipset/:name/entries", s.networkHandler.AddFirewallIPSetEntry)
			clusters.Delete("/:cluster_id/firewall/ipset/:name/entries/:cidr", s.networkHandler.DeleteFirewallIPSetEntry)
			clusters.Get("/:cluster_id/firewall/groups", s.networkHandler.ListSecurityGroups)
			clusters.Post("/:cluster_id/firewall/groups", s.networkHandler.CreateSecurityGroup)
			clusters.Delete("/:cluster_id/firewall/groups/:group", s.networkHandler.DeleteSecurityGroup)
			clusters.Get("/:cluster_id/firewall/groups/:group/rules", s.networkHandler.ListSecurityGroupRules)
			clusters.Post("/:cluster_id/firewall/groups/:group/rules", s.networkHandler.CreateSecurityGroupRule)
			clusters.Put("/:cluster_id/firewall/groups/:group/rules/:pos", s.networkHandler.UpdateSecurityGroupRule)
			clusters.Delete("/:cluster_id/firewall/groups/:group/rules/:pos", s.networkHandler.DeleteSecurityGroupRule)
			clusters.Get("/:cluster_id/firewall/log", s.networkHandler.GetFirewallLog)
		}

		// CVE scanning routes.
		if s.cveHandler != nil {
			clusters.Get("/:cluster_id/cve-scans", s.cveHandler.ListScans)
			clusters.Post("/:cluster_id/cve-scans", s.cveHandler.TriggerScan)
			clusters.Get("/:cluster_id/cve-scans/:scan_id", s.cveHandler.GetScan)
			clusters.Get("/:cluster_id/cve-scans/:scan_id/vulnerabilities", s.cveHandler.ListVulnerabilities)
			clusters.Delete("/:cluster_id/cve-scans/:scan_id", s.cveHandler.DeleteScan)
			clusters.Get("/:cluster_id/security-posture", s.cveHandler.GetSecurityPosture)
			clusters.Get("/:cluster_id/cve-scan-schedule", s.cveHandler.GetSchedule)
			clusters.Put("/:cluster_id/cve-scan-schedule", s.cveHandler.UpdateSchedule)
		}

		// Cluster-scoped alert routes.
		if s.alertHandler != nil {
			clusters.Get("/:cluster_id/alerts", s.alertHandler.ListAlertsByCluster)
			clusters.Get("/:cluster_id/alerts/count", s.alertHandler.CountActiveAlertsByCluster)
			clusters.Get("/:cluster_id/maintenance-windows", s.alertHandler.ListMaintenanceWindows)
			clusters.Post("/:cluster_id/maintenance-windows", s.alertHandler.CreateMaintenanceWindow)
			clusters.Put("/:cluster_id/maintenance-windows/:id", s.alertHandler.UpdateMaintenanceWindow)
			clusters.Delete("/:cluster_id/maintenance-windows/:id", s.alertHandler.DeleteMaintenanceWindow)
		}

		// DRS routes.
		if s.drsHandler != nil {
			clusters.Get("/:cluster_id/drs/config", s.drsHandler.GetConfig)
			clusters.Put("/:cluster_id/drs/config", s.drsHandler.UpdateConfig)
			clusters.Get("/:cluster_id/drs/rules", s.drsHandler.ListRules)
			clusters.Post("/:cluster_id/drs/rules", s.drsHandler.CreateRule)
			clusters.Delete("/:cluster_id/drs/rules/:rule_id", s.drsHandler.DeleteRule)
			clusters.Post("/:cluster_id/drs/evaluate", s.drsHandler.TriggerEvaluate)
			clusters.Get("/:cluster_id/drs/history", s.drsHandler.ListHistory)
			clusters.Get("/:cluster_id/drs/ha-rules", s.drsHandler.ListHARules)
			clusters.Post("/:cluster_id/drs/ha-rules", s.drsHandler.CreateHARule)
			clusters.Delete("/:cluster_id/drs/ha-rules/:rule_name", s.drsHandler.DeleteHARule)
		}

		// Migration routes under clusters.
		if s.migrationHandler != nil {
			clusters.Get("/:cluster_id/migrations", s.migrationHandler.ListByCluster)
		}

		// Restore and backup job routes under clusters.
		if s.backupHandler != nil {
			clusters.Post("/:cluster_id/restore", s.backupHandler.RestoreBackup)
			clusters.Post("/:cluster_id/backup", s.backupHandler.TriggerBackup)
			clusters.Get("/:cluster_id/backup-jobs", s.backupHandler.ListBackupJobs)
			clusters.Post("/:cluster_id/backup-jobs", s.backupHandler.CreateBackupJob)
			clusters.Put("/:cluster_id/backup-jobs/:job_id", s.backupHandler.UpdateBackupJob)
			clusters.Delete("/:cluster_id/backup-jobs/:job_id", s.backupHandler.DeleteBackupJob)
			clusters.Post("/:cluster_id/backup-jobs/:job_id/run", s.backupHandler.RunBackupJob)
		}

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
		}

		// Cluster options, tags, and config routes.
		if s.clusterOptionsHandler != nil {
			clusters.Get("/:cluster_id/options", s.clusterOptionsHandler.GetOptions)
			clusters.Put("/:cluster_id/options", s.clusterOptionsHandler.UpdateOptions)
			clusters.Get("/:cluster_id/description", s.clusterOptionsHandler.GetDescription)
			clusters.Put("/:cluster_id/description", s.clusterOptionsHandler.UpdateDescription)
			clusters.Get("/:cluster_id/tags", s.clusterOptionsHandler.GetTags)
			clusters.Put("/:cluster_id/tags", s.clusterOptionsHandler.UpdateTags)
			clusters.Get("/:cluster_id/config", s.clusterOptionsHandler.GetClusterConfig)
			clusters.Get("/:cluster_id/config/join", s.clusterOptionsHandler.GetJoinInfo)
			clusters.Get("/:cluster_id/config/nodes", s.clusterOptionsHandler.ListCorosyncNodes)
		}

		// HA management routes.
		if s.haHandler != nil {
			clusters.Get("/:cluster_id/ha/resources", s.haHandler.ListResources)
			clusters.Post("/:cluster_id/ha/resources", s.haHandler.CreateResource)
			clusters.Get("/:cluster_id/ha/resources/:sid", s.haHandler.GetResource)
			clusters.Put("/:cluster_id/ha/resources/:sid", s.haHandler.UpdateResource)
			clusters.Delete("/:cluster_id/ha/resources/:sid", s.haHandler.DeleteResource)
			clusters.Get("/:cluster_id/ha/groups", s.haHandler.ListGroups)
			clusters.Post("/:cluster_id/ha/groups", s.haHandler.CreateGroup)
			clusters.Put("/:cluster_id/ha/groups/:group", s.haHandler.UpdateGroup)
			clusters.Delete("/:cluster_id/ha/groups/:group", s.haHandler.DeleteGroup)
			clusters.Get("/:cluster_id/ha/status", s.haHandler.GetStatus)
			clusters.Get("/:cluster_id/ha/rules", s.haHandler.ListRules)
			clusters.Post("/:cluster_id/ha/rules", s.haHandler.CreateRule)
			clusters.Delete("/:cluster_id/ha/rules/:rule", s.haHandler.DeleteRule)
		}

		// Resource pool CRUD routes (GET list already registered via vmHandler).
		if s.poolHandler != nil {
			clusters.Post("/:cluster_id/pools", s.poolHandler.CreatePool)
			clusters.Get("/:cluster_id/pools/:pool_id", s.poolHandler.GetPool)
			clusters.Put("/:cluster_id/pools/:pool_id", s.poolHandler.UpdatePool)
			clusters.Delete("/:cluster_id/pools/:pool_id", s.poolHandler.DeletePool)
		}

		// Replication routes.
		if s.replicationHandler != nil {
			clusters.Get("/:cluster_id/replication", s.replicationHandler.ListJobs)
			clusters.Post("/:cluster_id/replication", s.replicationHandler.CreateJob)
			clusters.Get("/:cluster_id/replication/:job_id", s.replicationHandler.GetJob)
			clusters.Put("/:cluster_id/replication/:job_id", s.replicationHandler.UpdateJob)
			clusters.Delete("/:cluster_id/replication/:job_id", s.replicationHandler.DeleteJob)
			clusters.Post("/:cluster_id/replication/:job_id/trigger", s.replicationHandler.TriggerSync)
			clusters.Get("/:cluster_id/replication/:job_id/status", s.replicationHandler.GetStatus)
			clusters.Get("/:cluster_id/replication/:job_id/log", s.replicationHandler.GetLog)
		}

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

	// PBS snapshot lookup (cross-server, by backup_id / VMID).
	if s.backupHandler != nil {
		v1.Get("/pbs-snapshots", s.authRequired(), s.backupHandler.ListSnapshotsByBackupID)
		v1.Get("/backup-coverage", s.authRequired(), s.backupHandler.GetBackupCoverage)
	}

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
