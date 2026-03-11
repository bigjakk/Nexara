package handlers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// StorageHandler handles storage pool read endpoints.
type StorageHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewStorageHandler creates a new storage handler.
func NewStorageHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *StorageHandler {
	return &StorageHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
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
	if err := requirePerm(c, "view", "storage"); err != nil {
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
	if err := requirePerm(c, "view", "storage"); err != nil {
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
//
// This handler uses streaming multipart parsing to avoid buffering the entire
// file in memory. With StreamRequestBody enabled on the server, fasthttp
// provides a body stream for requests exceeding BodyLimit. We parse the
// multipart stream directly and pipe the file part to Proxmox.
//
// The frontend must send form fields in order: content, filesize, file.
func (h *StorageHandler) UploadFile(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "storage"); err != nil {
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

	// Parse the multipart boundary from the Content-Type header.
	ct := c.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return fiber.NewError(fiber.StatusBadRequest, "Expected multipart form data")
	}
	boundary := params["boundary"]
	if boundary == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Missing multipart boundary")
	}

	// Get the body stream. For large uploads (> BodyLimit), fasthttp provides
	// a streaming reader that avoids buffering the entire body in memory.
	// For smaller bodies, fall back to the in-memory buffer.
	bodyStream := c.Context().RequestBodyStream()
	if bodyStream == nil {
		bodyStream = bytes.NewReader(c.Body())
	}

	mr := multipart.NewReader(bodyStream, boundary)

	var uploadContent string
	var fileSize int64

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Failed to parse multipart form")
		}

		switch part.FormName() {
		case "content":
			val, _ := io.ReadAll(io.LimitReader(part, 64))
			uploadContent = strings.TrimSpace(string(val))

		case "filesize":
			val, _ := io.ReadAll(io.LimitReader(part, 32))
			fileSize, _ = strconv.ParseInt(strings.TrimSpace(string(val)), 10, 64)

		case "file":
			filename := filepath.Base(part.FileName())
			if filename == "" || filename == "." || filename == "/" {
				return fiber.NewError(fiber.StatusBadRequest, "Invalid filename")
			}
			if uploadContent != "iso" && uploadContent != "vztmpl" {
				return fiber.NewError(fiber.StatusBadRequest, "content must be 'iso' or 'vztmpl'")
			}
			if fileSize <= 0 {
				return fiber.NewError(fiber.StatusBadRequest, "filesize field is required before file")
			}

			// Wrap the part reader in a large buffer to reduce syscalls.
			// multipart.Part does byte-level boundary scanning; buffering
			// amortises that overhead across 256KB chunks.
			bufferedPart := bufio.NewReaderSize(part, 256*1024)

			// Stream the file part directly to Proxmox without buffering.
			upid, uploadErr := pxClient.UploadToStorage(c.Context(), node.Name, pool.Storage, uploadContent, filename, bufferedPart, fileSize)
			if uploadErr != nil {
				return mapProxmoxError(uploadErr)
			}

			h.auditLog(c, pool.ClusterID, pool.ID.String(), "upload")

			return c.JSON(uploadResponse{
				UPID:   upid,
				Status: "dispatched",
			})

		default:
			// Discard unknown form fields.
			_, _ = io.Copy(io.Discard, part)
		}
	}

	return fiber.NewError(fiber.StatusBadRequest, "No file provided in upload")
}

// DeleteContent handles DELETE /api/v1/clusters/:cluster_id/storage/:storage_id/content/:volume.
func (h *StorageHandler) DeleteContent(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "storage"); err != nil {
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

	h.auditLog(c, pool.ClusterID, pool.ID.String(), "delete_content")

	return c.JSON(deleteContentResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// storageConfigResponse wraps the Proxmox storage config for frontend consumption.
type storageConfigResponse struct {
	proxmox.StorageConfig
}

// validStorageTypes defines all Proxmox-supported storage plugin types.
var validStorageTypes = map[string]bool{
	"dir": true, "nfs": true, "cifs": true, "lvm": true, "lvmthin": true,
	"zfspool": true, "iscsi": true, "iscsidirect": true,
	"rbd": true, "cephfs": true, "glusterfs": true, "btrfs": true, "pbs": true,
}

// GetConfig handles GET /api/v1/clusters/:cluster_id/storage/:storage_id/config.
// Returns the Proxmox-level storage configuration (paths, servers, etc.).
func (h *StorageHandler) GetConfig(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "storage"); err != nil {
		return err
	}

	pool, pxClient, err := h.resolveStorage(c)
	if err != nil {
		return err
	}

	cfg, err := pxClient.GetStorageConfig(c.Context(), pool.Storage)
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(storageConfigResponse{*cfg})
}

// createStorageRequest is the JSON body for creating a new storage pool.
type createStorageRequest struct {
	Storage string            `json:"storage"`
	Type    string            `json:"type"`
	Params  map[string]string `json:"params"`
}

// Create handles POST /api/v1/clusters/:cluster_id/storage.
func (h *StorageHandler) Create(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "storage"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req createStorageRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Storage == "" {
		return fiber.NewError(fiber.StatusBadRequest, "storage name is required")
	}
	if !validStorageTypes[req.Type] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid storage type: "+req.Type)
	}

	form := url.Values{}
	form.Set("storage", req.Storage)
	form.Set("type", req.Type)
	for k, v := range req.Params {
		if k != "storage" && k != "type" && v != "" {
			form.Set(k, v)
		}
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateStorage(c.Context(), form); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"storage": req.Storage, "type": req.Type})
	h.auditLogDetails(c, clusterID, "storage", req.Storage, "create", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"status":  "created",
		"storage": req.Storage,
	})
}

// updateStorageRequest is the JSON body for updating a storage pool.
type updateStorageRequest struct {
	Params map[string]string `json:"params"`
	Delete string            `json:"delete,omitempty"` // comma-separated params to remove
}

// Update handles PUT /api/v1/clusters/:cluster_id/storage/:storage_id.
func (h *StorageHandler) Update(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "storage"); err != nil {
		return err
	}

	pool, pxClient, err := h.resolveStorage(c)
	if err != nil {
		return err
	}

	var req updateStorageRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	form := url.Values{}
	for k, v := range req.Params {
		if k != "storage" && k != "type" && v != "" {
			form.Set(k, v)
		}
	}
	if req.Delete != "" {
		form.Set("delete", req.Delete)
	}

	if err := pxClient.UpdateStorage(c.Context(), pool.Storage, form); err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, pool.ClusterID, pool.ID.String(), "update")

	return c.JSON(fiber.Map{
		"status":  "updated",
		"storage": pool.Storage,
	})
}

// Delete handles DELETE /api/v1/clusters/:cluster_id/storage/:storage_id.
func (h *StorageHandler) Delete(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "storage"); err != nil {
		return err
	}

	pool, pxClient, err := h.resolveStorage(c)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteStorage(c.Context(), pool.Storage); err != nil {
		return mapProxmoxError(err)
	}

	// Remove from local DB immediately so it doesn't appear stale.
	// Storage is cluster-level, so delete all rows for this name across nodes.
	_ = h.queries.DeleteStoragePoolsByName(c.Context(), db.DeleteStoragePoolsByNameParams{
		ClusterID: pool.ClusterID,
		Storage:   pool.Storage,
	})

	h.auditLog(c, pool.ClusterID, pool.ID.String(), "delete")

	return c.JSON(fiber.Map{
		"status":  "deleted",
		"storage": pool.Storage,
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
// Uses 30-minute timeout for large ISO uploads.
func (h *StorageHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID, 30*time.Minute)
}

// auditLog writes an audit log entry. Failures are logged but don't fail the request.
func (h *StorageHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceID, action string) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "storage", resourceID, action, nil)
}

// auditLogDetails writes an audit log entry with details.
func (h *StorageHandler) auditLogDetails(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, details)
}
