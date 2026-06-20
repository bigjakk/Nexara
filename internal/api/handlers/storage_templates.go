package handlers

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// --- Request/response types ---

type pullOCIRequest struct {
	Reference string `json:"reference"`
	FileName  string `json:"file_name,omitempty"`
}

type downloadURLRequest struct {
	URL                    string `json:"url"`
	Content                string `json:"content"`
	Filename               string `json:"filename"`
	Checksum               string `json:"checksum,omitempty"`
	ChecksumAlgorithm      string `json:"checksum_algorithm,omitempty"`
	DecompressionAlgorithm string `json:"decompression_algorithm,omitempty"`
	VerifyCertificates     *bool  `json:"verify_certificates,omitempty"`
}

type downloadApplianceRequest struct {
	Template string `json:"template"`
}

type templateTaskResponse struct {
	UPID   string `json:"upid"`
	Status string `json:"status"`
}

// applianceResponse is the JSON shape returned by ListAppliances. It mirrors
// Proxmox's /aplinfo entries but uses snake_case for frontend consumers.
type applianceResponse struct {
	Template     string `json:"template"`
	OS           string `json:"os"`
	Type         string `json:"type"`
	Version      string `json:"version"`
	Section      string `json:"section"`
	Package      string `json:"package"`
	Description  string `json:"description"`
	Headline     string `json:"headline"`
	InfoPage     string `json:"info_page,omitempty"`
	Source       string `json:"source,omitempty"`
	Location     string `json:"location,omitempty"`
	ManageURL    string `json:"manage_url,omitempty"`
	SHA512Sum    string `json:"sha512sum,omitempty"`
	Architecture string `json:"architecture,omitempty"`
}

// --- Handlers ---

// PullOCI handles POST /api/v1/clusters/:cluster_id/storage/:storage_id/oci-pull.
// Triggers an async skopeo-backed pull on Proxmox. Requires PVE 9.1+ on the node.
func (h *StorageHandler) PullOCI(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "storage", clusterID); err != nil {
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

	if !storageHasContent(pool.Content, "vztmpl") {
		return fiber.NewError(fiber.StatusBadRequest, "Storage does not support vztmpl content; enable it in storage configuration")
	}

	var req pullOCIRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Reference == "" {
		return fiber.NewError(fiber.StatusBadRequest, "reference is required")
	}

	upid, err := pxClient.PullOCIImage(c.Context(), node.Name, pool.Storage, proxmox.OCIPullParams{
		Reference: req.Reference,
		FileName:  req.FileName,
	})
	if err != nil {
		return mapTemplateError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    pool.ClusterID,
		Node:         node.Name,
		ResourceType: "storage",
		ResourceID:   pool.ID.String(),
		Action:       "pull_oci",
		UPID:         upid,
		Description:  "Pull OCI image " + req.Reference,
		Extra:        map[string]any{"reference": req.Reference, "filename": req.FileName, "storage": pool.Storage},
	})

	return c.JSON(templateTaskResponse{UPID: upid, Status: "dispatched"})
}

// DownloadURL handles POST /api/v1/clusters/:cluster_id/storage/:storage_id/download-url.
// Triggers an async download of an arbitrary URL into the storage as iso, vztmpl, or import.
func (h *StorageHandler) DownloadURL(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "storage", clusterID); err != nil {
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

	var req downloadURLRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	switch req.Content {
	case "iso", "vztmpl", "import":
	default:
		return fiber.NewError(fiber.StatusBadRequest, "content must be iso, vztmpl, or import")
	}
	if !storageHasContent(pool.Content, req.Content) {
		return fiber.NewError(fiber.StatusBadRequest, "Storage does not support "+req.Content+" content")
	}

	upid, err := pxClient.DownloadURLToStorage(c.Context(), node.Name, pool.Storage, proxmox.URLDownloadParams{
		URL:                    req.URL,
		Content:                req.Content,
		Filename:               req.Filename,
		Checksum:               req.Checksum,
		ChecksumAlgorithm:      req.ChecksumAlgorithm,
		DecompressionAlgorithm: req.DecompressionAlgorithm,
		VerifyCertificates:     req.VerifyCertificates,
	})
	if err != nil {
		return mapTemplateError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    pool.ClusterID,
		Node:         node.Name,
		ResourceType: "storage",
		ResourceID:   pool.ID.String(),
		Action:       "download_url",
		UPID:         upid,
		Description:  "Download " + req.URL,
		Extra:        map[string]any{"url": req.URL, "content": req.Content, "filename": req.Filename, "storage": pool.Storage},
	})

	return c.JSON(templateTaskResponse{UPID: upid, Status: "dispatched"})
}

// ListAppliances handles GET /api/v1/clusters/:cluster_id/appliances.
// Returns the official Proxmox appliance catalog (Debian/Ubuntu/Alpine/Turnkey/...).
// Cluster-scoped because the catalog is identical across nodes; we pick any online node.
func (h *StorageHandler) ListAppliances(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "storage", clusterID); err != nil {
		return err
	}

	pxClient, nodeName, err := h.resolveOnlineNode(c, clusterID)
	if err != nil {
		return err
	}

	apps, err := pxClient.GetAppliances(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]applianceResponse, len(apps))
	for i, a := range apps {
		resp[i] = applianceResponse{
			Template:     a.Template,
			OS:           a.OS,
			Type:         a.Type,
			Version:      a.Version,
			Section:      a.Section,
			Package:      a.Package,
			Description:  a.Description,
			Headline:     a.Headline,
			InfoPage:     a.InfoPage,
			Source:       a.Source,
			Location:     a.Location,
			ManageURL:    a.ManageURL,
			SHA512Sum:    a.SHA512Sum,
			Architecture: a.Architecture,
		}
	}
	return c.JSON(resp)
}

// DownloadAppliance handles POST /api/v1/clusters/:cluster_id/storage/:storage_id/appliances.
// Downloads a Proxmox-catalog appliance template into the named storage.
func (h *StorageHandler) DownloadAppliance(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "storage", clusterID); err != nil {
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

	if !storageHasContent(pool.Content, "vztmpl") {
		return fiber.NewError(fiber.StatusBadRequest, "Storage does not support vztmpl content")
	}

	var req downloadApplianceRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Template == "" {
		return fiber.NewError(fiber.StatusBadRequest, "template is required")
	}

	upid, err := pxClient.DownloadAppliance(c.Context(), node.Name, pool.Storage, req.Template)
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    pool.ClusterID,
		Node:         node.Name,
		ResourceType: "storage",
		ResourceID:   pool.ID.String(),
		Action:       "download_appliance",
		UPID:         upid,
		Description:  "Download appliance " + req.Template,
		Extra:        map[string]any{"template": req.Template, "storage": pool.Storage},
	})

	return c.JSON(templateTaskResponse{UPID: upid, Status: "dispatched"})
}

// --- helpers ---

// resolveOnlineNode picks the first online node in a cluster for endpoints that
// need a node but the data isn't node-specific (e.g. /aplinfo, /version).
func (h *StorageHandler) resolveOnlineNode(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, string, error) {
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return nil, "", err
	}
	nodes, err := h.queries.ListNodesByCluster(c.Context(), clusterID)
	if err != nil || len(nodes) == 0 {
		return nil, "", fiber.NewError(fiber.StatusNotFound, "No nodes found in cluster")
	}
	nodeName := nodes[0].Name
	for _, n := range nodes {
		if n.Status == "online" {
			nodeName = n.Name
			break
		}
	}
	return pxClient, nodeName, nil
}

// storageHasContent returns true if the comma-separated `content` field includes
// the given content type. Proxmox stores this as e.g. "iso,vztmpl,backup".
func storageHasContent(content, want string) bool {
	for _, part := range strings.Split(content, ",") {
		if strings.TrimSpace(part) == want {
			return true
		}
	}
	return false
}

// mapTemplateError adds template-specific error-message massaging on top of
// mapProxmoxError. Surfaces skopeo-missing and feature-not-supported cases with
// clearer guidance than the raw Proxmox bubble-up.
func mapTemplateError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "skopeo") && (strings.Contains(low, "not found") || strings.Contains(low, "no such") || strings.Contains(low, "command not")):
		return fiber.NewError(fiber.StatusBadRequest, "skopeo is not installed on the node. Install it with: apt install skopeo")
	case strings.Contains(low, "oci-registry-pull") && strings.Contains(low, "404"):
		return fiber.NewError(fiber.StatusBadRequest, "OCI image support requires Proxmox VE 9.1 or newer")
	case strings.Contains(low, "no such method") || strings.Contains(low, "not implemented"):
		return fiber.NewError(fiber.StatusBadRequest, "This operation is not supported by the Proxmox version on this node")
	}
	return mapProxmoxError(err)
}
