package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// NodeHandler handles node read endpoints.
type NodeHandler struct {
	queries *db.Queries
}

// NewNodeHandler creates a new node handler.
func NewNodeHandler(queries *db.Queries) *NodeHandler {
	return &NodeHandler{queries: queries}
}

type nodeResponse struct {
	ID                 uuid.UUID `json:"id"`
	ClusterID          uuid.UUID `json:"cluster_id"`
	Name               string    `json:"name"`
	Status             string    `json:"status"`
	CPUCount           int32     `json:"cpu_count"`
	CPUModel           string    `json:"cpu_model"`
	CPUCores           int32     `json:"cpu_cores"`
	CPUSockets         int32     `json:"cpu_sockets"`
	CPUThreads         int32     `json:"cpu_threads"`
	CPUMhz             string    `json:"cpu_mhz"`
	MemTotal           int64     `json:"mem_total"`
	DiskTotal          int64     `json:"disk_total"`
	SwapTotal          int64     `json:"swap_total"`
	SwapUsed           int64     `json:"swap_used"`
	SwapFree           int64     `json:"swap_free"`
	PveVersion         string    `json:"pve_version"`
	KernelVersion      string    `json:"kernel_version"`
	DNSServers         string    `json:"dns_servers"`
	DNSSearch          string    `json:"dns_search"`
	Timezone           string    `json:"timezone"`
	SubscriptionStatus string    `json:"subscription_status"`
	SubscriptionLevel  string    `json:"subscription_level"`
	LoadAvg            string    `json:"load_avg"`
	IOWait             float64   `json:"io_wait"`
	Uptime             int64     `json:"uptime"`
	LastSeenAt         time.Time `json:"last_seen_at"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func toNodeResponse(n db.Node) nodeResponse {
	return nodeResponse{
		ID:                 n.ID,
		ClusterID:          n.ClusterID,
		Name:               n.Name,
		Status:             n.Status,
		CPUCount:           n.CpuCount,
		CPUModel:           n.CpuModel,
		CPUCores:           n.CpuCores,
		CPUSockets:         n.CpuSockets,
		CPUThreads:         n.CpuThreads,
		CPUMhz:             n.CpuMhz,
		MemTotal:           n.MemTotal,
		DiskTotal:          n.DiskTotal,
		SwapTotal:          n.SwapTotal,
		SwapUsed:           n.SwapUsed,
		SwapFree:           n.SwapFree,
		PveVersion:         n.PveVersion,
		KernelVersion:      n.KernelVersion,
		DNSServers:         n.DnsServers,
		DNSSearch:          n.DnsSearch,
		Timezone:           n.Timezone,
		SubscriptionStatus: n.SubscriptionStatus,
		SubscriptionLevel:  n.SubscriptionLevel,
		LoadAvg:            n.LoadAvg,
		IOWait:             n.IoWait,
		Uptime:             n.Uptime,
		LastSeenAt:         n.LastSeenAt,
		CreatedAt:          n.CreatedAt,
		UpdatedAt:          n.UpdatedAt,
	}
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/nodes.
func (h *NodeHandler) ListByCluster(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "node"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodes, err := h.queries.ListNodesByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list nodes")
	}

	resp := make([]nodeResponse, len(nodes))
	for i, n := range nodes {
		resp[i] = toNodeResponse(n)
	}

	return c.JSON(resp)
}

// --- Node sub-resource response types ---

type nodeDiskResponse struct {
	ID       uuid.UUID `json:"id"`
	DevPath  string    `json:"dev_path"`
	Model    string    `json:"model"`
	Serial   string    `json:"serial"`
	Size     int64     `json:"size"`
	DiskType string    `json:"disk_type"`
	Health   string    `json:"health"`
	Wearout  string    `json:"wearout"`
	RPM      int32     `json:"rpm"`
	Vendor   string    `json:"vendor"`
	WWN      string    `json:"wwn"`
}

type nodeNetworkInterfaceResponse struct {
	ID          uuid.UUID `json:"id"`
	Iface       string    `json:"iface"`
	IfaceType   string    `json:"iface_type"`
	Active      bool      `json:"active"`
	Autostart   bool      `json:"autostart"`
	Method      string    `json:"method"`
	Method6     string    `json:"method6"`
	Address     string    `json:"address"`
	Netmask     string    `json:"netmask"`
	Gateway     string    `json:"gateway"`
	CIDR        string    `json:"cidr"`
	BridgePorts string    `json:"bridge_ports"`
	Comments    string    `json:"comments"`
}

type nodePCIDeviceResponse struct {
	ID              uuid.UUID `json:"id"`
	PCIID           string    `json:"pci_id"`
	Class           string    `json:"class"`
	DeviceName      string    `json:"device_name"`
	VendorName      string    `json:"vendor_name"`
	Device          string    `json:"device"`
	Vendor          string    `json:"vendor"`
	IOMMUGroup      int32     `json:"iommu_group"`
	SubsystemDevice string    `json:"subsystem_device"`
	SubsystemVendor string    `json:"subsystem_vendor"`
}

// ListNodeDisks handles GET /api/v1/nodes/:node_id/disks.
func (h *NodeHandler) ListNodeDisks(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "node"); err != nil {
		return err
	}
	nodeID, err := uuid.Parse(c.Params("node_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid node ID")
	}
	disks, err := h.queries.ListNodeDisksByNode(c.Context(), nodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list disks")
	}
	resp := make([]nodeDiskResponse, len(disks))
	for i, d := range disks {
		resp[i] = nodeDiskResponse{
			ID: d.ID, DevPath: d.DevPath, Model: d.Model, Serial: d.Serial,
			Size: d.Size, DiskType: d.DiskType, Health: d.Health, Wearout: d.Wearout,
			RPM: d.Rpm, Vendor: d.Vendor, WWN: d.Wwn,
		}
	}
	return c.JSON(resp)
}

// ListNodeNetworkInterfaces handles GET /api/v1/nodes/:node_id/network-interfaces.
func (h *NodeHandler) ListNodeNetworkInterfaces(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "node"); err != nil {
		return err
	}
	nodeID, err := uuid.Parse(c.Params("node_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid node ID")
	}
	ifaces, err := h.queries.ListNodeNetworkInterfacesByNode(c.Context(), nodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list network interfaces")
	}
	resp := make([]nodeNetworkInterfaceResponse, len(ifaces))
	for i, f := range ifaces {
		resp[i] = nodeNetworkInterfaceResponse{
			ID: f.ID, Iface: f.Iface, IfaceType: f.IfaceType,
			Active: f.Active, Autostart: f.Autostart,
			Method: f.Method, Method6: f.Method6,
			Address: f.Address, Netmask: f.Netmask, Gateway: f.Gateway, CIDR: f.Cidr,
			BridgePorts: f.BridgePorts, Comments: f.Comments,
		}
	}
	return c.JSON(resp)
}

// ListNodePCIDevices handles GET /api/v1/nodes/:node_id/pci-devices.
func (h *NodeHandler) ListNodePCIDevices(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "node"); err != nil {
		return err
	}
	nodeID, err := uuid.Parse(c.Params("node_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid node ID")
	}
	devs, err := h.queries.ListNodePCIDevicesByNode(c.Context(), nodeID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list PCI devices")
	}
	resp := make([]nodePCIDeviceResponse, len(devs))
	for i, d := range devs {
		resp[i] = nodePCIDeviceResponse{
			ID: d.ID, PCIID: d.PciID, Class: d.Class,
			DeviceName: d.DeviceName, VendorName: d.VendorName,
			Device: d.Device, Vendor: d.Vendor,
			IOMMUGroup: d.IommuGroup,
			SubsystemDevice: d.SubsystemDevice, SubsystemVendor: d.SubsystemVendor,
		}
	}
	return c.JSON(resp)
}
