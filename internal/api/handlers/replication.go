package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

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
	// The job as it stood and as Proxmox stored it, to name what the edit changed and
	// not what the dialog sent along or how it was spelled. A job that cannot be read
	// is no reason to refuse the edit, which Proxmox answers for itself, nor to wait
	// on it; the row then names no field.
	before := readReplicationJob(c.Context(), replicationReadTimeout, slog.Default(), pxClient, clusterID, jobID, "before")
	if err := pxClient.UpdateReplicationJob(c.Context(), jobID, req); err != nil {
		return mapProxmoxError(err)
	}
	var after *proxmox.ReplicationJob
	if before != nil {
		after = readReplicationJob(c.Context(), replicationReadTimeout, slog.Default(), pxClient, clusterID, jobID, "after")
	}
	details, _ := json.Marshal(map[string]any{"id": jobID, "fields": changedReplicationFields(before, after, req)})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "replication", jobID, "updated", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindReplicationChange, "replication", jobID, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// replicationReadTimeout bounds each read of a job around an edit: the audit row is
// owed whether or not they answer.
const replicationReadTimeout = 5 * time.Second

// readReplicationJob reads a job within timeout. A read that fails, or runs out of
// time, is logged as a warning and answers nil.
func readReplicationJob(ctx context.Context, timeout time.Duration, logger *slog.Logger, pxClient *proxmox.Client, clusterID uuid.UUID, jobID, when string) *proxmox.ReplicationJob {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	job, err := pxClient.GetReplicationJob(ctx, jobID)
	if err != nil {
		logger.Warn("replication update: could not read the job "+when+" the edit; its audit row names no field",
			"cluster_id", clusterID, "job_id", jobID, "error", err)
		return nil
	}
	return job
}

// changedReplicationFields names the fields an edit changed: those it sent that
// differ between the job before the edit and after it, as Proxmox stored them. The
// job's History explains a change seen in Proxmox by an update that names the field
// (ClassifyClusterObjectChanges), and the edit dialog sends schedule and comment
// whatever was touched: naming those, or a rate by how it was typed ("10.0" for the
// 10 Proxmox keeps), would let this edit stand for a later change to the field made
// in Proxmox. A field the edit did not send is not its, whatever happened to it
// meanwhile. A job that could not be read on either side names nothing.
func changedReplicationFields(before, after *proxmox.ReplicationJob, req proxmox.UpdateReplicationJobParams) []string {
	fields := make([]string, 0, 4)
	if before == nil || after == nil {
		return fields
	}
	for _, f := range []struct {
		name    string
		changed bool
	}{
		{"schedule", req.Schedule != "" && before.Schedule != after.Schedule},
		{"rate", req.Rate != "" && !sameNumber(before.Rate.String(), after.Rate.String())},
		{"comment", req.Comment != "" && before.Comment != after.Comment},
		{"disable", req.Disable != nil && before.Disable != after.Disable},
	} {
		if f.changed {
			fields = append(fields, f.name)
		}
	}
	return fields
}

// sameNumber reports whether two spellings of a setting are the same number, or the
// same text when either is not one.
func sameNumber(a, b string) bool {
	x, errA := strconv.ParseFloat(a, 64)
	y, errB := strconv.ParseFloat(b, 64)
	if errA != nil || errB != nil {
		return a == b
	}
	return x == y
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
	// schedule_now only moves the job's next run up (pve-guest-common,
	// ReplicationState::schedule_job_now) and spawns no task: pvesr runs the job
	// from its timer and logs to the job's own log. So upid is normally empty,
	// and TrackTask then records the trigger as the audit row it is.
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
