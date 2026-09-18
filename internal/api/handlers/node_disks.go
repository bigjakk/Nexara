package handlers

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// --- Live Disk List (from Proxmox, not DB) ---

type liveDiskResponse struct {
	DevPath  string `json:"dev_path"`
	Model    string `json:"model"`
	Serial   string `json:"serial"`
	Size     int64  `json:"size"`
	DiskType string `json:"disk_type"`
	Health   string `json:"health"`
	Wearout  string `json:"wearout"`
	GPT      int    `json:"gpt"`
	Used     string `json:"used"`
}

// ListLiveDisks handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/list.
// Returns fresh disk data directly from Proxmox (includes "used" field).
func (h *NodeHandler) ListLiveDisks(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	disks, err := pxClient.GetNodeDisks(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
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
	return RespondItems(c, resp)
}

// --- Disk SMART ---

// GetDiskSMART handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/smart?disk=...
func (h *NodeHandler) GetDiskSMART(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	smart, err := pxClient.GetDiskSMART(c.Context(), nodeName, p.String("disk"))
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(smart)
}

// --- ZFS Pools ---

// ListZFSPools handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs.
func (h *NodeHandler) ListZFSPools(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	pools, err := pxClient.GetNodeZFSPools(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, pools)
}

// CreateZFSPool handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs.
func (h *NodeHandler) CreateZFSPool(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	name, raidLevel := p.String("name"), p.String("raidlevel")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.CreateNodeZFSPool(c.Context(), nodeName, proxmox.CreateZFSPoolParams{
		Name:        name,
		RaidLevel:   raidLevel,
		Devices:     p.String("devices"),
		Compression: p.String("compression"),
		// 0 is the schema's default and the client's "not chosen" sentinel:
		// it only sends ashift when the value is positive.
		Ashift: int(p.Int("ashift")),
	})
	if err != nil {
		return mapDuplicateNameError("A ZFS pool with that name already exists", err)
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "create_zfs_pool",
		UPID:         upid,
		Description:  "Create ZFS pool " + name,
		Extra:        map[string]any{"name": name, "raidlevel": raidLevel},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// DeleteZFSPool handles DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs/:pool_name.
func (h *NodeHandler) DeleteZFSPool(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	poolName := p.String("pool_name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.DeleteNodeZFSPool(c.Context(), nodeName, poolName,
		p.Bool("cleanup-disks"), p.Bool("cleanup-config"))
	if err != nil {
		return mapProxmoxError(err)
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
func (h *NodeHandler) ListLVM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	vgs, err := pxClient.GetNodeLVM(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, vgs)
}

// CreateLVM handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvm.
func (h *NodeHandler) CreateLVM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	name, device := p.String("name"), p.String("device")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.CreateNodeLVM(c.Context(), nodeName, proxmox.CreateLVMParams{
		Name:       name,
		Device:     device,
		AddStorage: p.Bool("add_storage"),
	})
	if err != nil {
		return mapDuplicateNameError("A volume group or storage entry with that name already exists on this node", err)
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "create_lvm",
		UPID:         upid,
		Description:  "Create LVM " + name,
		Extra:        map[string]any{"name": name, "device": device},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// DeleteLVM handles DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvm/:vg_name.
func (h *NodeHandler) DeleteLVM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	vgName := p.String("vg_name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.DeleteNodeLVM(c.Context(), nodeName, vgName,
		p.Bool("cleanup-disks"), p.Bool("cleanup-config"))
	if err != nil {
		return mapProxmoxError(err)
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
func (h *NodeHandler) ListLVMThin(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	pools, err := pxClient.GetNodeLVMThin(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, pools)
}

// CreateLVMThin handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvmthin.
func (h *NodeHandler) CreateLVMThin(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	name, device := p.String("name"), p.String("device")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.CreateNodeLVMThin(c.Context(), nodeName, proxmox.CreateLVMThinParams{
		Name:       name,
		Device:     device,
		AddStorage: p.Bool("add_storage"),
	})
	if err != nil {
		return mapDuplicateNameError("A thin pool or storage entry with that name already exists on this node", err)
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "create_lvmthin",
		UPID:         upid,
		Description:  "Create LVM-thin " + name,
		Extra:        map[string]any{"name": name, "device": device},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// DeleteLVMThin handles DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvmthin/:pool_name.
func (h *NodeHandler) DeleteLVMThin(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	poolName, volumeGroup := p.String("pool_name"), p.String("volume-group")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.DeleteNodeLVMThin(c.Context(), nodeName, poolName, volumeGroup,
		p.Bool("cleanup-disks"), p.Bool("cleanup-config"))
	if err != nil {
		return mapProxmoxError(err)
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
func (h *NodeHandler) ListDirectories(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	dirs, err := pxClient.GetNodeDirectories(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, dirs)
}

// CreateDirectory handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/directory.
func (h *NodeHandler) CreateDirectory(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	name, device, filesystem := p.String("name"), p.String("device"), p.String("filesystem")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.CreateNodeDirectory(c.Context(), nodeName, proxmox.CreateDirectoryParams{
		Name:       name,
		Device:     device,
		Filesystem: filesystem,
		AddStorage: p.Bool("add_storage"),
	})
	if err != nil {
		return mapDuplicateNameError("A directory, mount unit, or storage entry with that name already exists on this node", err)
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "create_directory",
		UPID:         upid,
		Description:  "Create directory " + name,
		Extra:        map[string]any{"name": name, "device": device, "filesystem": filesystem},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// --- Disk Init / Wipe ---

// InitializeGPT handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/initgpt.
func (h *NodeHandler) InitializeGPT(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	disk := p.String("disk")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.InitializeGPT(c.Context(), nodeName, disk)
	if err != nil {
		return mapNamedOpError("initialize disk", err)
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "initialize_gpt",
		UPID:         upid,
		Description:  "Initialize GPT on " + disk,
		Extra:        map[string]any{"disk": disk},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// WipeDisk handles PUT /api/v1/clusters/:cluster_id/nodes/:node_name/disks/wipe.
func (h *NodeHandler) WipeDisk(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	disk := p.String("disk")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.WipeDisk(c.Context(), nodeName, disk)
	if err != nil {
		return mapNamedOpError("wipe disk", err)
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "wipe_disk",
		UPID:         upid,
		Description:  "Wipe disk " + disk,
		Extra:        map[string]any{"disk": disk},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}
