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

// All four routes are declared in internal/api/registry_pools.go, which states
// their cluster-scoped permission (manage:pool for the three writes, view:pool
// for the read) and their parameters; nothing below re-checks either. The
// LISTING of pools belongs to VMHandler and is declared in registry_vms.go.
//
// What stays here is the audit row and the cluster event each write emits, and
// the mapping of Proxmox's own refusals onto a status code.

// poolAuditTextMax bounds each free-text or list value a pool update records:
// the schema allows far more, and an audit row is never trimmed.
const poolAuditTextMax = 1024

// PoolHandler handles resource pool endpoints.
type PoolHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewPoolHandler creates a new PoolHandler.
func NewPoolHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *PoolHandler {
	return &PoolHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *PoolHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// CreatePool handles POST /clusters/:cluster_id/pools.
func (h *PoolHandler) CreatePool(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	req := proxmox.CreatePoolParams{
		PoolID:  p.String("poolid"),
		Comment: p.String("comment"),
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateResourcePool(c.Context(), req); err != nil {
		return mapDuplicateNameError("A pool with that ID already exists", err)
	}
	details, _ := json.Marshal(map[string]string{"poolid": req.PoolID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pool", req.PoolID, "created", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindPoolChange, "pool", req.PoolID, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// GetPool handles GET /clusters/:cluster_id/pools/:pool_id.
func (h *PoolHandler) GetPool(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	poolID := p.String("pool_id")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	pool, err := pxClient.GetResourcePool(c.Context(), poolID)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(pool)
}

// UpdatePool handles PUT /clusters/:cluster_id/pools/:pool_id.
func (h *PoolHandler) UpdatePool(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	poolID := p.String("pool_id")

	req := proxmox.UpdatePoolParams{
		VMs:     p.String("vms"),
		Storage: p.String("storage"),
		Delete:  p.String("delete"),
	}
	// Comment is a *string on purpose: omitting it leaves the stored comment
	// alone, while sending it empty clears it. A plain string would collapse
	// the two and overwrite every pool comment whose editor never touched the
	// field — the same absent-versus-zero distinction the disks/attach `index`
	// incident turned into a destroyed boot disk.
	req.Comment = optStringPtr(p.OptString("comment"))

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateResourcePool(c.Context(), poolID, req); err != nil {
		return mapProxmoxError(err)
	}
	// What the update touched, as sent: the guests and storages named, and the
	// delete flag that turns them from additions into removals. A pool's History
	// is mostly its membership, and a bare "updated" says none of it.
	detailMap := map[string]string{"poolid": poolID}
	if req.VMs != "" {
		detailMap["vms"] = auditTruncate(req.VMs, poolAuditTextMax)
	}
	if req.Storage != "" {
		detailMap["storage"] = auditTruncate(req.Storage, poolAuditTextMax)
	}
	if req.Delete != "" {
		detailMap["delete"] = req.Delete
	}
	if req.Comment != nil {
		detailMap["comment"] = auditTruncate(*req.Comment, poolAuditTextMax)
	}
	details, _ := json.Marshal(detailMap)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pool", poolID, "updated", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindPoolChange, "pool", poolID, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeletePool handles DELETE /clusters/:cluster_id/pools/:pool_id.
func (h *PoolHandler) DeletePool(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	poolID := p.String("pool_id")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteResourcePool(c.Context(), poolID); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"poolid": poolID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pool", poolID, "deleted", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindPoolChange, "pool", poolID, "deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}
