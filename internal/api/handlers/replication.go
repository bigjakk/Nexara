package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
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

func (h *ReplicationHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// ListJobs handles GET /clusters/:cluster_id/replication.
func (h *ReplicationHandler) ListJobs(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
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
	return RespondItems(c, jobs)
}

// CreateJob handles POST /clusters/:cluster_id/replication.
func (h *ReplicationHandler) CreateJob(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	// Declared as job_id with "id" as its alias — see the declaration in
	// internal/api/registry_replication.go for why the canonical name
	// cannot be "id". Either spelling arrives here under this key.
	jobID := p.String("job_id")
	target := p.String("target")
	req := proxmox.CreateReplicationJobParams{
		ID: jobID,
		// The schema's Default supplies "local" for a missing type, so the
		// handler's own substitution is gone.
		Type:     p.String("type"),
		Target:   target,
		Schedule: p.String("schedule"),
		Rate:     p.String("rate"),
		Comment:  p.String("comment"),
		// A *int, so omitting the key means "do not send this property"
		// rather than "send 0", which would put a job Proxmox created
		// disabled back on its schedule.
		Disable: optIntPtr(p.OptInt("disable")),
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateReplicationJob(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": jobID, "target": target})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "replication", jobID, "created", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindReplicationChange, "replication", jobID, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// GetJob handles GET /clusters/:cluster_id/replication/:job_id.
func (h *ReplicationHandler) GetJob(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	jobID := p.String("job_id")
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
func (h *ReplicationHandler) UpdateJob(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	jobID := p.String("job_id")
	// Every string field here is dropped by the client when it is empty
	// (see UpdateReplicationJob), so "" and "absent" have always meant the
	// same thing to Proxmox — which is what lets the edit dialog send
	// schedule and comment unconditionally. Only disable is a pointer, and
	// only it needs the supplied/omitted distinction.
	req := proxmox.UpdateReplicationJobParams{
		Schedule:  p.String("schedule"),
		Rate:      p.String("rate"),
		Comment:   p.String("comment"),
		Disable:   optIntPtr(p.OptInt("disable")),
		RemoveJob: p.String("remove_job"),
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateReplicationJob(c.Context(), jobID, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": jobID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "replication", jobID, "updated", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindReplicationChange, "replication", jobID, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteJob handles DELETE /clusters/:cluster_id/replication/:job_id.
func (h *ReplicationHandler) DeleteJob(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	jobID := p.String("job_id")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteReplicationJob(c.Context(), jobID); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": jobID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "replication", jobID, "deleted", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindReplicationChange, "replication", jobID, "deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}

// TriggerSync handles POST /clusters/:cluster_id/replication/:job_id/trigger.
func (h *ReplicationHandler) TriggerSync(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	jobID := p.String("job_id")
	node := p.String("node")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.TriggerReplication(c.Context(), node, jobID)
	if err != nil {
		return mapProxmoxError(err)
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         node,
		ResourceType: "replication",
		ResourceID:   jobID,
		Action:       "triggered",
		UPID:         upid,
		Description:  "Replication " + jobID,
		Extra:        map[string]any{"id": jobID},
	})
	return c.JSON(fiber.Map{"upid": upid})
}

// GetStatus handles GET /clusters/:cluster_id/replication/:job_id/status.
func (h *ReplicationHandler) GetStatus(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	jobID := p.String("job_id")
	node := p.String("node")
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
func (h *ReplicationHandler) GetLog(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	jobID := p.String("job_id")
	node := p.String("node")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	entries, err := pxClient.GetReplicationLog(c.Context(), node, jobID, int(p.Int("limit")))
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, entries)
}
