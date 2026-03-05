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
	}

	// Cluster routes.
	if s.clusterHandler != nil {
		clusters := v1.Group("/clusters", s.authRequired())
		clusters.Post("/", s.clusterHandler.Create)
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
			clusters.Get("/:cluster_id/vms/:vm_id", s.vmHandler.GetVM)
			clusters.Post("/:cluster_id/vms/:vm_id/status", s.vmHandler.PerformAction)
			clusters.Post("/:cluster_id/vms/:vm_id/clone", s.vmHandler.CloneVM)
			clusters.Delete("/:cluster_id/vms/:vm_id", s.vmHandler.DestroyVM)
			clusters.Get("/:cluster_id/tasks/:upid", s.vmHandler.GetTaskStatus)
		}
		if s.containerHandler != nil {
			clusters.Get("/:cluster_id/containers", s.containerHandler.ListByCluster)
			clusters.Get("/:cluster_id/containers/:ct_id", s.containerHandler.GetContainer)
			clusters.Post("/:cluster_id/containers/:ct_id/status", s.containerHandler.PerformAction)
			clusters.Post("/:cluster_id/containers/:ct_id/clone", s.containerHandler.CloneContainer)
			clusters.Post("/:cluster_id/containers/:ct_id/migrate", s.containerHandler.MigrateContainer)
			clusters.Delete("/:cluster_id/containers/:ct_id", s.containerHandler.DestroyContainer)
		}
		if s.vmHandler != nil {
			clusters.Post("/:cluster_id/vms/:vm_id/disks/resize", s.vmHandler.ResizeDisk)
			clusters.Post("/:cluster_id/vms/:vm_id/disks/move", s.vmHandler.MoveDisk)
		}
		if s.storageHandler != nil {
			clusters.Get("/:cluster_id/storage", s.storageHandler.ListByCluster)
			clusters.Get("/:cluster_id/storage/:storage_id/content", s.storageHandler.GetContent)
			clusters.Post("/:cluster_id/storage/:storage_id/upload", s.storageHandler.UploadFile)
			clusters.Delete("/:cluster_id/storage/:storage_id/content/:volume", s.storageHandler.DeleteContent)
		}
		if s.metricsHandler != nil {
			clusters.Get("/:cluster_id/metrics", s.metricsHandler.GetClusterHistorical)
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
			pbs.Get("/:pbs_id/snapshots", s.backupHandler.ListSnapshots)
			pbs.Get("/:pbs_id/sync-jobs", s.backupHandler.ListSyncJobs)
			pbs.Post("/:pbs_id/sync-jobs/:job_id/run", s.backupHandler.RunSyncJob)
			pbs.Get("/:pbs_id/verify-jobs", s.backupHandler.ListVerifyJobs)
			pbs.Post("/:pbs_id/verify-jobs/:job_id/run", s.backupHandler.RunVerifyJob)
			pbs.Get("/:pbs_id/tasks", s.backupHandler.ListTasks)
			pbs.Get("/:pbs_id/tasks/:upid", s.backupHandler.GetTaskStatus)
			pbs.Get("/:pbs_id/metrics", s.backupHandler.GetDatastoreMetrics)
		}
	}

	// Restore route under clusters.
	if s.backupHandler != nil && s.clusterHandler != nil {
		clusters := v1.Group("/clusters", s.authRequired())
		clusters.Post("/:cluster_id/restore", s.backupHandler.RestoreBackup)
	}

	// Future route groups:
	// v1.Group("/nodes")
	// v1.Group("/vms")
	// v1.Group("/users")
}
