package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"path/filepath"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// StorageHandler handles storage pool read endpoints.
type StorageHandler struct {
	queries       *db.Queries
	encryptionKey string
}

// NewStorageHandler creates a new storage handler.
func NewStorageHandler(queries *db.Queries, encryptionKey string) *StorageHandler {
	return &StorageHandler{queries: queries, encryptionKey: encryptionKey}
}

type storageResponse struct {
	ID        uuid.UUID `json:"id"`
	ClusterID uuid.UUID `json:"cluster_id"`
	NodeID    uuid.UUID `json:"node_id"`
	Storage   string    `json:"storage"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	Active    bool      `json:"active"`
	Enabled   bool      `json:"enabled"`
	Shared    bool      `json:"shared"`
	Total     int64     `json:"total"`
	Used      int64     `json:"used"`
	Avail     int64     `json:"avail"`

	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toStorageResponse(s db.StoragePool) storageResponse {
	return storageResponse{
		ID:         s.ID,
		ClusterID:  s.ClusterID,
		NodeID:     s.NodeID,
		Storage:    s.Storage,
		Type:       s.Type,
		Content:    s.Content,
		Active:     s.Active,
		Enabled:    s.Enabled,
		Shared:     s.Shared,
		Total:      s.Total,
		Used:       s.Used,
		Avail:      s.Avail,
		LastSeenAt: s.LastSeenAt,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
	}
}

type storageContentResponse struct {
	Volid   string `json:"volid"`
	Format  string `json:"format"`
	Size    int64  `json:"size"`
	CTime   int64  `json:"ctime"`
	Content string `json:"content"`
	VMID    int    `json:"vmid,omitempty"`
}

type uploadResponse struct {
	UPID   string `json:"upid"`
	Status string `json:"status"`
}

type deleteContentResponse struct {
	UPID   string `json:"upid"`
	Status string `json:"status"`
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/storage.
func (h *StorageHandler) ListByCluster(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pools, err := h.queries.ListStoragePoolsByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list storage pools")
	}

	resp := make([]storageResponse, len(pools))
	for i, p := range pools {
		resp[i] = toStorageResponse(p)
	}

	return c.JSON(resp)
}

// GetContent handles GET /api/v1/clusters/:cluster_id/storage/:storage_id/content.
func (h *StorageHandler) GetContent(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	pool, pxClient, err := h.resolveStorage(c)
	if err != nil {
		return err
	}

	node, err := h.queries.GetNode(c.Context(), pool.NodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get node for storage pool")
	}

	items, err := pxClient.GetStorageContent(c.Context(), node.Name, pool.Storage)
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]storageContentResponse, len(items))
	for i, item := range items {
		resp[i] = storageContentResponse{
			Volid:   item.Volid,
			Format:  item.Format,
			Size:    item.Size,
			CTime:   item.CTime,
			Content: item.Content,
			VMID:    item.VMID,
		}
	}

	return c.JSON(resp)
}

// UploadFile handles POST /api/v1/clusters/:cluster_id/storage/:storage_id/upload.
func (h *StorageHandler) UploadFile(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	pool, pxClient, err := h.resolveStorage(c)
	if err != nil {
		return err
	}

	node, err := h.queries.GetNode(c.Context(), pool.NodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get node for storage pool")
	}

	contentType := c.FormValue("content")
	if contentType != "iso" && contentType != "vztmpl" {
		return fiber.NewError(fiber.StatusBadRequest, "content must be 'iso' or 'vztmpl'")
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "file is required")
	}

	filename := filepath.Base(fileHeader.Filename)
	if filename == "." || filename == "/" || filename == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid filename")
	}

	file, err := fileHeader.Open()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to open uploaded file")
	}
	defer file.Close()

	upid, err := pxClient.UploadToStorage(c.Context(), node.Name, pool.Storage, contentType, filename, file, fileHeader.Size)
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, pool.ClusterID, "storage", pool.ID.String(), "upload")

	return c.JSON(uploadResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// DeleteContent handles DELETE /api/v1/clusters/:cluster_id/storage/:storage_id/content/:volume.
func (h *StorageHandler) DeleteContent(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	pool, pxClient, err := h.resolveStorage(c)
	if err != nil {
		return err
	}

	node, err := h.queries.GetNode(c.Context(), pool.NodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get node for storage pool")
	}

	rawVolume := c.Params("*")
	if rawVolume == "" {
		return fiber.NewError(fiber.StatusBadRequest, "volume is required")
	}
	volume, err := url.PathUnescape(rawVolume)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid volume ID")
	}

	log.Printf("DELETE storage content: node=%s storage=%s volume=%q", node.Name, pool.Storage, volume)
	upid, err := pxClient.DeleteStorageContent(c.Context(), node.Name, pool.Storage, volume)
	if err != nil {
		log.Printf("DELETE storage content error: %v", err)
		return mapProxmoxError(err)
	}

	h.auditLog(c, pool.ClusterID, "storage", pool.ID.String(), "delete_content")

	return c.JSON(deleteContentResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// resolveStorage loads the storage pool from the DB and creates a Proxmox client.
func (h *StorageHandler) resolveStorage(c *fiber.Ctx) (db.StoragePool, *proxmox.Client, error) {
	var zero db.StoragePool

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return zero, nil, fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	storageID, err := uuid.Parse(c.Params("storage_id"))
	if err != nil {
		return zero, nil, fiber.NewError(fiber.StatusBadRequest, "Invalid storage ID")
	}

	pool, err := h.queries.GetStoragePool(c.Context(), storageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, nil, fiber.NewError(fiber.StatusNotFound, "Storage pool not found")
		}
		return zero, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get storage pool")
	}

	if pool.ClusterID != clusterID {
		return zero, nil, fiber.NewError(fiber.StatusNotFound, "Storage pool not found in this cluster")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return zero, nil, err
	}

	return pool, pxClient, nil
}

// createProxmoxClient creates a Proxmox client for the given cluster.
func (h *StorageHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, h.encryptionKey)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt cluster credentials")
	}

	pxClient, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        30 * time.Minute, // large timeout for ISO uploads
	})
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	return pxClient, nil
}

// auditLog writes an audit log entry. Failures are logged but don't fail the request.
func (h *StorageHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string) {
	uid, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return
	}
	_ = h.queries.InsertAuditLog(c.Context(), db.InsertAuditLogParams{
		ClusterID:    clusterID,
		UserID:       uid,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      json.RawMessage(`{}`),
	})
}
