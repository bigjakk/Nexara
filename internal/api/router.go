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
	}

	// PBS server routes.
	if s.pbsHandler != nil {
		pbs := v1.Group("/pbs-servers", s.authRequired())
		pbs.Post("/", s.pbsHandler.Create)
		pbs.Get("/", s.pbsHandler.List)
		pbs.Get("/:id", s.pbsHandler.Get)
		pbs.Put("/:id", s.pbsHandler.Update)
		pbs.Delete("/:id", s.pbsHandler.Delete)
	}

	// Future route groups:
	// v1.Group("/nodes")
	// v1.Group("/vms")
	// v1.Group("/users")
}
