package handlers

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// --- Live Disk List (from Proxmox, not DB) ---

type liveDiskResponse struct {
	DevPath string `json:"dev_path"`
	Model   string `json:"model"`
	Serial  string `json:"serial"`
	Size    int64  `json:"size"`
	DiskType string `json:"disk_type"`
	Health  string `json:"health"`
	Wearout string `json:"wearout"`
	GPT     int    `json:"gpt"`
	Used    string `json:"used"`
}

// ListLiveDisks handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/list.
// Returns fresh disk data directly from Proxmox (includes "used" field).
func (h *NodeHandler) ListLiveDisks(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "node", clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	disks, err := pxClient.GetNodeDisks(c.Context(), nodeName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list disks")
	}
	resp := make([]liveDiskResponse, len(disks))
	for i, d := range disks {
		resp[i] = liveDiskResponse{
			DevPath:  d.DevPath,
			Model:    d.Model,
			Serial:   d.Serial,
			Size:     d.Size,
			DiskType: d.Type,
			Health:   d.Health,
			Wearout:  d.Wearout.String(),
			GPT:      d.GPT,
			Used:     d.Used,
		}
	}
	return c.JSON(resp)
}

// --- Disk SMART ---

// GetDiskSMART handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/smart?disk=...
func (h *NodeHandler) GetDiskSMART(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "node", clusterID); err != nil {
		return err
	}
	disk := c.Query("disk")
	if disk == "" {
		return fiber.NewError(fiber.StatusBadRequest, "disk query parameter is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	smart, err := pxClient.GetDiskSMART(c.Context(), nodeName, disk)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get SMART data")
	}
	return c.JSON(smart)
}

// --- ZFS Pools ---

// ListZFSPools handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs.
func (h *NodeHandler) ListZFSPools(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "node", clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	pools, err := pxClient.GetNodeZFSPools(c.Context(), nodeName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list ZFS pools")
	}
	return c.JSON(pools)
}

type createZFSPoolRequest struct {
	Name        string `json:"name"`
	RaidLevel   string `json:"raidlevel"`
	Devices     string `json:"devices"`
	Compression string `json:"compression"`
	Ashift      int    `json:"ashift"`
}

// CreateZFSPool handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs.
func (h *NodeHandler) CreateZFSPool(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
		return err
	}
	var req createZFSPoolRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Name == "" || req.RaidLevel == "" || req.Devices == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name, raidlevel, and devices are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.CreateNodeZFSPool(c.Context(), nodeName, proxmox.CreateZFSPoolParams{
		Name:        req.Name,
		RaidLevel:   req.RaidLevel,
		Devices:     req.Devices,
		Compression: req.Compression,
		Ashift:      req.Ashift,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to create ZFS pool: %v", err))
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "create_zfs_pool",
		UPID:         upid,
		Description:  "Create ZFS pool " + req.Name,
		Extra:        map[string]any{"name": req.Name, "raidlevel": req.RaidLevel},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// DeleteZFSPool handles DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs/:pool_name.
func (h *NodeHandler) DeleteZFSPool(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
		return err
	}
	poolName := c.Params("pool_name")
	if poolName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Pool name is required")
	}
	cleanupDisks := c.QueryBool("cleanup-disks", false)
	cleanupConfig := c.QueryBool("cleanup-config", false)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.DeleteNodeZFSPool(c.Context(), nodeName, poolName, cleanupDisks, cleanupConfig)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to destroy ZFS pool: %v", err))
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "delete_zfs_pool",
		UPID:         upid,
		Description:  "Delete ZFS pool " + poolName,
		Extra:        map[string]any{"pool": poolName},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// --- LVM ---

// ListLVM handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvm.
func (h *NodeHandler) ListLVM(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "node", clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	vgs, err := pxClient.GetNodeLVM(c.Context(), nodeName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list LVM volume groups")
	}
	return c.JSON(vgs)
}

type createLVMRequest struct {
	Name       string `json:"name"`
	Device     string `json:"device"`
	AddStorage bool   `json:"add_storage"`
}

// CreateLVM handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvm.
func (h *NodeHandler) CreateLVM(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
		return err
	}
	var req createLVMRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Name == "" || req.Device == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name and device are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.CreateNodeLVM(c.Context(), nodeName, proxmox.CreateLVMParams{
		Name:       req.Name,
		Device:     req.Device,
		AddStorage: req.AddStorage,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create LVM volume group")
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "create_lvm",
		UPID:         upid,
		Description:  "Create LVM " + req.Name,
		Extra:        map[string]any{"name": req.Name, "device": req.Device},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// DeleteLVM handles DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvm/:vg_name.
func (h *NodeHandler) DeleteLVM(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
		return err
	}
	vgName := c.Params("vg_name")
	if vgName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Volume group name is required")
	}
	cleanupDisks := c.QueryBool("cleanup-disks", false)
	cleanupConfig := c.QueryBool("cleanup-config", false)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.DeleteNodeLVM(c.Context(), nodeName, vgName, cleanupDisks, cleanupConfig)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to destroy LVM volume group: %v", err))
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "delete_lvm",
		UPID:         upid,
		Description:  "Delete LVM " + vgName,
		Extra:        map[string]any{"vg": vgName},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// --- LVM-Thin ---

// ListLVMThin handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvmthin.
func (h *NodeHandler) ListLVMThin(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "node", clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	pools, err := pxClient.GetNodeLVMThin(c.Context(), nodeName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list LVM thin pools")
	}
	return c.JSON(pools)
}

type createLVMThinRequest struct {
	Name       string `json:"name"`
	Device     string `json:"device"`
	AddStorage bool   `json:"add_storage"`
}

// CreateLVMThin handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvmthin.
func (h *NodeHandler) CreateLVMThin(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
		return err
	}
	var req createLVMThinRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Name == "" || req.Device == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name and device are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.CreateNodeLVMThin(c.Context(), nodeName, proxmox.CreateLVMThinParams{
		Name:       req.Name,
		Device:     req.Device,
		AddStorage: req.AddStorage,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create LVM thin pool")
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "create_lvmthin",
		UPID:         upid,
		Description:  "Create LVM-thin " + req.Name,
		Extra:        map[string]any{"name": req.Name, "device": req.Device},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// DeleteLVMThin handles DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvmthin/:pool_name.
func (h *NodeHandler) DeleteLVMThin(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
		return err
	}
	poolName := c.Params("pool_name")
	if poolName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Pool name is required")
	}
	volumeGroup := c.Query("volume-group")
	if volumeGroup == "" {
		return fiber.NewError(fiber.StatusBadRequest, "volume-group query parameter is required")
	}
	cleanupDisks := c.QueryBool("cleanup-disks", false)
	cleanupConfig := c.QueryBool("cleanup-config", false)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.DeleteNodeLVMThin(c.Context(), nodeName, poolName, volumeGroup, cleanupDisks, cleanupConfig)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to destroy LVM-Thin pool: %v", err))
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "delete_lvmthin",
		UPID:         upid,
		Description:  "Delete LVM-thin " + poolName,
		Extra:        map[string]any{"pool": poolName, "vg": volumeGroup},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// --- Directory ---

// ListDirectories handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/directory.
func (h *NodeHandler) ListDirectories(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "node", clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	dirs, err := pxClient.GetNodeDirectories(c.Context(), nodeName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list directories")
	}
	return c.JSON(dirs)
}

type createDirectoryRequest struct {
	Name       string `json:"name"`
	Device     string `json:"device"`
	Filesystem string `json:"filesystem"`
	AddStorage bool   `json:"add_storage"`
}

// CreateDirectory handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/directory.
func (h *NodeHandler) CreateDirectory(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
		return err
	}
	var req createDirectoryRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Name == "" || req.Device == "" || req.Filesystem == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name, device, and filesystem are required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.CreateNodeDirectory(c.Context(), nodeName, proxmox.CreateDirectoryParams{
		Name:       req.Name,
		Device:     req.Device,
		Filesystem: req.Filesystem,
		AddStorage: req.AddStorage,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create directory")
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "create_directory",
		UPID:         upid,
		Description:  "Create directory " + req.Name,
		Extra:        map[string]any{"name": req.Name, "device": req.Device, "filesystem": req.Filesystem},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// --- Disk Init / Wipe ---

type diskActionRequest struct {
	Disk string `json:"disk"`
}

// InitializeGPT handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/initgpt.
func (h *NodeHandler) InitializeGPT(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
		return err
	}
	var req diskActionRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Disk == "" {
		return fiber.NewError(fiber.StatusBadRequest, "disk is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.InitializeGPT(c.Context(), nodeName, req.Disk)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to initialize disk with GPT")
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "initialize_gpt",
		UPID:         upid,
		Description:  "Initialize GPT on " + req.Disk,
		Extra:        map[string]any{"disk": req.Disk},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// WipeDisk handles PUT /api/v1/clusters/:cluster_id/nodes/:node_name/disks/wipe.
func (h *NodeHandler) WipeDisk(c *fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
		return err
	}
	var req diskActionRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Disk == "" {
		return fiber.NewError(fiber.StatusBadRequest, "disk is required")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.WipeDisk(c.Context(), nodeName, req.Disk)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to wipe disk")
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "wipe_disk",
		UPID:         upid,
		Description:  "Wipe disk " + req.Disk,
		Extra:        map[string]any{"disk": req.Disk},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}
