package handlers

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// VMHandler handles VM CRUD and lifecycle endpoints.
type VMHandler struct {
	queries       *db.Queries
	encryptionKey string
}

// NewVMHandler creates a new VM handler.
func NewVMHandler(queries *db.Queries, encryptionKey string) *VMHandler {
	return &VMHandler{queries: queries, encryptionKey: encryptionKey}
}

type vmResponse struct {
	ID        uuid.UUID `json:"id"`
	ClusterID uuid.UUID `json:"cluster_id"`
	NodeID    uuid.UUID `json:"node_id"`
	Vmid      int32     `json:"vmid"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	CpuCount  int32     `json:"cpu_count"`
	MemTotal  int64     `json:"mem_total"`
	DiskTotal int64     `json:"disk_total"`
	Uptime    int64     `json:"uptime"`
	Template  bool      `json:"template"`
	Tags      string    `json:"tags"`
	HaState   string    `json:"ha_state"`
	Pool      string    `json:"pool"`

	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toVMResponse(v db.Vm) vmResponse {
	return vmResponse{
		ID:         v.ID,
		ClusterID:  v.ClusterID,
		NodeID:     v.NodeID,
		Vmid:       v.Vmid,
		Name:       v.Name,
		Type:       v.Type,
		Status:     v.Status,
		CpuCount:   v.CpuCount,
		MemTotal:   v.MemTotal,
		DiskTotal:  v.DiskTotal,
		Uptime:     v.Uptime,
		Template:   v.Template,
		Tags:       v.Tags,
		HaState:    v.HaState,
		Pool:       v.Pool,
		LastSeenAt: v.LastSeenAt,
		CreatedAt:  v.CreatedAt,
		UpdatedAt:  v.UpdatedAt,
	}
}

// validVMActions is the set of allowed VM status actions.
var validVMActions = map[string]bool{
	"start":    true,
	"stop":     true,
	"shutdown": true,
	"reboot":   true,
	"reset":    true,
	"suspend":  true,
	"resume":   true,
}

type vmActionRequest struct {
	Action string `json:"action"`
}

type vmCloneRequest struct {
	NewID   int    `json:"new_id"`
	Name    string `json:"name"`
	Target  string `json:"target"`
	Full    bool   `json:"full"`
	Storage string `json:"storage"`
}

type vmActionResponse struct {
	UPID   string `json:"upid"`
	Status string `json:"status"`
}

type taskStatusResponse struct {
	Status     string `json:"status"`
	ExitStatus string `json:"exit_status"`
	Type       string `json:"type"`
	UPID       string `json:"upid"`
	Node       string `json:"node"`
	PID        int    `json:"pid"`
	StartTime  int64  `json:"start_time"`
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/vms.
func (h *VMHandler) ListByCluster(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vms, err := h.queries.ListVMsByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list VMs")
	}

	resp := make([]vmResponse, len(vms))
	for i, v := range vms {
		resp[i] = toVMResponse(v)
	}

	return c.JSON(resp)
}

// GetVM handles GET /api/v1/clusters/:cluster_id/vms/:vm_id.
func (h *VMHandler) GetVM(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	vmID, err := uuid.Parse(c.Params("vm_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid VM ID")
	}

	vm, err := h.queries.GetVM(c.Context(), vmID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "VM not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get VM")
	}

	return c.JSON(toVMResponse(vm))
}

// PerformAction handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/status.
func (h *VMHandler) PerformAction(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vmID, err := uuid.Parse(c.Params("vm_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid VM ID")
	}

	var req vmActionRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if !validVMActions[req.Action] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid action; must be one of: start, stop, shutdown, reboot, reset, suspend, resume")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	var upid string
	switch req.Action {
	case "start":
		upid, err = pxClient.StartVM(c.Context(), node.Name, int(vm.Vmid))
	case "stop":
		upid, err = pxClient.StopVM(c.Context(), node.Name, int(vm.Vmid))
	case "shutdown":
		upid, err = pxClient.ShutdownVM(c.Context(), node.Name, int(vm.Vmid))
	case "reboot":
		upid, err = pxClient.RebootVM(c.Context(), node.Name, int(vm.Vmid))
	case "reset":
		upid, err = pxClient.ResetVM(c.Context(), node.Name, int(vm.Vmid))
	case "suspend":
		upid, err = pxClient.SuspendVM(c.Context(), node.Name, int(vm.Vmid))
	case "resume":
		upid, err = pxClient.ResumeVM(c.Context(), node.Name, int(vm.Vmid))
	}
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, "vm", vm.ID.String(), req.Action)

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// CloneVM handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/clone.
func (h *VMHandler) CloneVM(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vmID, err := uuid.Parse(c.Params("vm_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid VM ID")
	}

	var req vmCloneRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.NewID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "new_id is required and must be positive")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CloneVM(c.Context(), node.Name, int(vm.Vmid), proxmox.CloneParams{
		NewID:   req.NewID,
		Name:    req.Name,
		Target:  req.Target,
		Full:    req.Full,
		Storage: req.Storage,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, "vm", vm.ID.String(), "clone")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// DestroyVM handles DELETE /api/v1/clusters/:cluster_id/vms/:vm_id.
func (h *VMHandler) DestroyVM(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	vmID, err := uuid.Parse(c.Params("vm_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid VM ID")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.DestroyVM(c.Context(), node.Name, int(vm.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, "vm", vm.ID.String(), "destroy")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// GetTaskStatus handles GET /api/v1/clusters/:cluster_id/tasks/:upid.
func (h *VMHandler) GetTaskStatus(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	upid := c.Params("upid")
	if upid == "" {
		return fiber.NewError(fiber.StatusBadRequest, "UPID is required")
	}

	// We need a node name to query task status. Extract it from the UPID.
	// UPID format: UPID:<node>:<pid_hex>:<pstart_hex>:<starttime_hex>:<type>:<id>:<user>@<realm>:
	nodeName := extractNodeFromUPID(upid)
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Could not extract node from UPID")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	status, err := pxClient.GetTaskStatus(c.Context(), nodeName, upid)
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(taskStatusResponse{
		Status:     status.Status,
		ExitStatus: status.ExitStatus,
		Type:       status.Type,
		UPID:       status.UPID,
		Node:       status.Node,
		PID:        status.PID,
		StartTime:  status.StartTime,
	})
}

// resolveVM loads the VM, its node, the cluster, and creates a Proxmox client.
func (h *VMHandler) resolveVM(c *fiber.Ctx, clusterID, vmID uuid.UUID) (db.Vm, db.Node, db.Cluster, *proxmox.Client, error) {
	var zeroVM db.Vm
	var zeroNode db.Node
	var zeroCluster db.Cluster

	vm, err := h.queries.GetVM(c.Context(), vmID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zeroVM, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusNotFound, "VM not found")
		}
		return zeroVM, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get VM")
	}

	if vm.ClusterID != clusterID {
		return zeroVM, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusNotFound, "VM not found in this cluster")
	}

	node, err := h.queries.GetNode(c.Context(), vm.NodeID)
	if err != nil {
		return zeroVM, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get node for VM")
	}

	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zeroVM, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return zeroVM, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, h.encryptionKey)
	if err != nil {
		return zeroVM, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt cluster credentials")
	}

	pxClient, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		return zeroVM, zeroNode, zeroCluster, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	return vm, node, cluster, pxClient, nil
}

// createProxmoxClient creates a Proxmox client for the given cluster.
func (h *VMHandler) createProxmoxClient(c *fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
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
		Timeout:        30 * time.Second,
	})
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	return pxClient, nil
}

// extractNodeFromUPID extracts the node name from a Proxmox UPID string.
// UPID format: UPID:<node>:<pid_hex>:<pstart_hex>:<starttime_hex>:<type>:<id>:<user>@<realm>:
func extractNodeFromUPID(upid string) string {
	// Split on colons: UPID, node, pid, pstart, starttime, type, id, user
	parts := splitUPID(upid)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// splitUPID splits a UPID into its colon-separated components.
func splitUPID(upid string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(upid); i++ {
		if upid[i] == ':' {
			parts = append(parts, upid[start:i])
			start = i + 1
		}
	}
	if start < len(upid) {
		parts = append(parts, upid[start:])
	}
	return parts
}

// auditLog writes an audit log entry. Failures are logged but don't fail the request.
func (h *VMHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string) {
	userID, _ := c.Locals("user_id").(string)
	uid, err := uuid.Parse(userID)
	if err != nil {
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

// mapProxmoxError converts a Proxmox client error to an appropriate Fiber error.
func mapProxmoxError(err error) error {
	if errors.Is(err, proxmox.ErrNotFound) {
		return fiber.NewError(fiber.StatusNotFound, "Resource not found on Proxmox")
	}
	if errors.Is(err, proxmox.ErrForbidden) {
		return fiber.NewError(fiber.StatusForbidden, "Proxmox API permission denied")
	}
	if errors.Is(err, proxmox.ErrConnectionFailed) {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to connect to Proxmox")
	}
	var apiErr *proxmox.APIError
	if errors.As(err, &apiErr) {
		return fiber.NewError(fiber.StatusBadGateway, apiErr.Message)
	}
	return fiber.NewError(fiber.StatusInternalServerError, "Proxmox operation failed")
}
