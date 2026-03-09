package handlers

import (
	"encoding/json"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// ReplicationHandler handles replication job endpoints.
type ReplicationHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewReplicationHandler creates a new ReplicationHandler.
func NewReplicationHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *ReplicationHandler {
	return &ReplicationHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *ReplicationHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

func (h *ReplicationHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, details)
}

// ListJobs handles GET /clusters/:cluster_id/replication.
func (h *ReplicationHandler) ListJobs(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "replication"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	jobs, err := pxClient.GetReplicationJobs(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(jobs)
}

// CreateJob handles POST /clusters/:cluster_id/replication.
func (h *ReplicationHandler) CreateJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "replication"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	var req proxmox.CreateReplicationJobParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.ID == "" || req.Target == "" {
		return fiber.NewError(fiber.StatusBadRequest, "ID and target are required")
	}
	if req.Type == "" {
		req.Type = "local"
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateReplicationJob(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": req.ID, "target": req.Target})
	h.auditLog(c, clusterID, "replication", req.ID, "created", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindReplicationChange, "replication", req.ID, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// GetJob handles GET /clusters/:cluster_id/replication/:job_id.
func (h *ReplicationHandler) GetJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "replication"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	jobID := c.Params("job_id")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	job, err := pxClient.GetReplicationJob(c.Context(), jobID)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(job)
}

// UpdateJob handles PUT /clusters/:cluster_id/replication/:job_id.
func (h *ReplicationHandler) UpdateJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "replication"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	jobID := c.Params("job_id")
	var req proxmox.UpdateReplicationJobParams
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateReplicationJob(c.Context(), jobID, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": jobID})
	h.auditLog(c, clusterID, "replication", jobID, "updated", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindReplicationChange, "replication", jobID, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteJob handles DELETE /clusters/:cluster_id/replication/:job_id.
func (h *ReplicationHandler) DeleteJob(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "replication"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	jobID := c.Params("job_id")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteReplicationJob(c.Context(), jobID); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": jobID})
	h.auditLog(c, clusterID, "replication", jobID, "deleted", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindReplicationChange, "replication", jobID, "deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}

// TriggerSync handles POST /clusters/:cluster_id/replication/:job_id/trigger.
func (h *ReplicationHandler) TriggerSync(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "replication"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	jobID := c.Params("job_id")
	node := c.Query("node")
	if node == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node query parameter is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.TriggerReplication(c.Context(), node, jobID)
	if err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": jobID, "node": node})
	h.auditLog(c, clusterID, "replication", jobID, "triggered", details)
	return c.JSON(fiber.Map{"upid": upid})
}

// GetStatus handles GET /clusters/:cluster_id/replication/:job_id/status.
func (h *ReplicationHandler) GetStatus(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "replication"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	jobID := c.Params("job_id")
	node := c.Query("node")
	if node == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node query parameter is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	status, err := pxClient.GetReplicationStatus(c.Context(), node, jobID)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(status)
}

// GetLog handles GET /clusters/:cluster_id/replication/:job_id/log.
func (h *ReplicationHandler) GetLog(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "replication"); err != nil {
		return err
	}
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	jobID := c.Params("job_id")
	node := c.Query("node")
	if node == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Node query parameter is required")
	}
	limit, _ := strconv.Atoi(c.Query("limit", "500"))
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	entries, err := pxClient.GetReplicationLog(c.Context(), node, jobID, limit)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(entries)
}
