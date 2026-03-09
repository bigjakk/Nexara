package handlers

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/proxmox"
)

// VMHandler handles VM CRUD and lifecycle endpoints.
type VMHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewVMHandler creates a new VM handler.
func NewVMHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *VMHandler {
	return &VMHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

type vmResponse struct {
	ID        uuid.UUID `json:"id"`
	ClusterID uuid.UUID `json:"cluster_id"`
	NodeID    uuid.UUID `json:"node_id"`
	Vmid      int32     `json:"vmid"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	CPUCount  int32     `json:"cpu_count"`
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
		CPUCount:   v.CpuCount,
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
	Status     string   `json:"status"`
	ExitStatus string   `json:"exit_status"`
	Type       string   `json:"type"`
	UPID       string   `json:"upid"`
	Node       string   `json:"node"`
	PID        int      `json:"pid"`
	StartTime  int64    `json:"start_time"`
	Progress   *float64 `json:"progress,omitempty"`
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/vms.
func (h *VMHandler) ListByCluster(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "vm"); err != nil {
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
	if err := requirePerm(c, "view", "vm"); err != nil {
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
	if err := requirePerm(c, "execute", "vm"); err != nil {
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

	detailsJSON, _ := json.Marshal(map[string]any{"upid": upid, "node": node.Name, "vmid": vm.Vmid})
	h.auditLog(c, cluster.ID, vm.ID.String(), req.Action, detailsJSON)

	// Watch the task in the background and update the DB when it completes.
	// The watcher publishes a vm_state_change event only after the DB is updated
	// with the real status, avoiding premature refetches of stale data.
	watchTaskAndUpdateStatus(h.queries, h.eventPub, pxClient, node.Name, upid, vm.ID, cluster.ID, req.Action, "vm")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// CloneVM handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/clone.
func (h *VMHandler) CloneVM(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "vm"); err != nil {
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

	detailsJSON, _ := json.Marshal(map[string]any{"upid": upid, "node": node.Name, "vmid": vm.Vmid})
	h.auditLog(c, cluster.ID, vm.ID.String(), "clone", detailsJSON)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "vm", vm.ID.String(), "clone")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// DestroyVM handles DELETE /api/v1/clusters/:cluster_id/vms/:vm_id.
func (h *VMHandler) DestroyVM(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "vm"); err != nil {
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

	detailsJSON, _ := json.Marshal(map[string]any{"upid": upid, "node": node.Name, "vmid": vm.Vmid})
	h.auditLog(c, cluster.ID, vm.ID.String(), "destroy", detailsJSON)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "vm", vm.ID.String(), "destroy")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// GetTaskStatus handles GET /api/v1/clusters/:cluster_id/tasks/:upid.
func (h *VMHandler) GetTaskStatus(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "task"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	rawUPID := c.Params("upid")
	if rawUPID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "UPID is required")
	}
	// Fiber (fasthttp) doesn't auto-decode route params; the frontend
	// URL-encodes the UPID so colons arrive as %3A, etc.
	upid, err := url.PathUnescape(rawUPID)
	if err != nil {
		upid = rawUPID // fall back to raw value
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

	resp := taskStatusResponse{
		Status:     status.Status,
		ExitStatus: status.ExitStatus,
		Type:       status.Type,
		UPID:       status.UPID,
		Node:       status.Node,
		PID:        status.PID,
		StartTime:  status.StartTime,
	}

	// For running tasks, fetch the log to extract progress (e.g. clone operations).
	// Proxmox emits progress in two formats:
	//   1. "progress 0.50"                                  (generic tasks)
	//   2. "transferred 1.0 GiB of 100.0 GiB (1.00%)"     (clone/move disk)
	if status.Status == "running" {
		if logEntries, logErr := pxClient.GetTaskLog(c.Context(), nodeName, upid, 0); logErr == nil {
			for i := len(logEntries) - 1; i >= 0; i-- {
				line := logEntries[i].T
				if strings.HasPrefix(line, "progress ") {
					if pct, parseErr := strconv.ParseFloat(strings.TrimPrefix(line, "progress "), 64); parseErr == nil {
						resp.Progress = &pct
					}
					break
				}
				// Parse "transferred X of Y (Z%)" lines from clone operations.
				if pctIdx := strings.LastIndex(line, "("); pctIdx != -1 && strings.HasSuffix(line, "%)") {
					pctStr := line[pctIdx+1 : len(line)-2] // extract "1.00"
					if pct, parseErr := strconv.ParseFloat(pctStr, 64); parseErr == nil {
						p := pct / 100.0
						resp.Progress = &p
					}
					break
				}
			}
		}
	}

	return c.JSON(resp)
}

// GetTaskLog returns the log lines for a Proxmox task.
func (h *VMHandler) GetTaskLog(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "task"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	rawUPID := c.Params("upid")
	if rawUPID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "UPID is required")
	}
	upid, err := url.PathUnescape(rawUPID)
	if err != nil {
		upid = rawUPID
	}

	nodeName := extractNodeFromUPID(upid)
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Could not extract node from UPID")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	entries, err := pxClient.GetTaskLog(c.Context(), nodeName, upid, 0)
	if err != nil {
		return mapProxmoxError(err)
	}

	type logLine struct {
		N int    `json:"n"`
		T string `json:"t"`
	}
	result := make([]logLine, len(entries))
	for i, e := range entries {
		result[i] = logLine{N: e.N, T: e.T}
	}

	return c.JSON(result)
}

type diskResizeRequest struct {
	Disk string `json:"disk"`
	Size string `json:"size"`
}

type diskMoveRequest struct {
	Disk    string `json:"disk"`
	Storage string `json:"storage"`
	Delete  bool   `json:"delete"`
}

// ResizeDisk handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/resize.
func (h *VMHandler) ResizeDisk(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "vm"); err != nil {
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

	var req diskResizeRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Disk == "" || req.Size == "" {
		return fiber.NewError(fiber.StatusBadRequest, "disk and size are required")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	if err := pxClient.ResizeDisk(c.Context(), node.Name, int(vm.Vmid), proxmox.DiskResizeParams{
		Disk: req.Disk,
		Size: req.Size,
	}); err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, vm.ID.String(), "disk_resize", nil)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "disk_resize")

	return c.JSON(vmActionResponse{
		UPID:   "",
		Status: "completed",
	})
}

// MoveDisk handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/move.
func (h *VMHandler) MoveDisk(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "vm"); err != nil {
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

	var req diskMoveRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Disk == "" || req.Storage == "" {
		return fiber.NewError(fiber.StatusBadRequest, "disk and storage are required")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.MoveDisk(c.Context(), node.Name, int(vm.Vmid), proxmox.DiskMoveParams{
		Disk:    req.Disk,
		Storage: req.Storage,
		Delete:  req.Delete,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	detailsJSON, _ := json.Marshal(map[string]any{"upid": upid, "node": node.Name, "vmid": vm.Vmid})
	h.auditLog(c, cluster.ID, vm.ID.String(), "disk_move", detailsJSON)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "disk_move")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// --- Disk Attach/Detach ---

type diskAttachRequest struct {
	Bus     string `json:"bus"`
	Index   int    `json:"index"`
	Storage string `json:"storage"`
	Size    string `json:"size"`
	Format  string `json:"format"`
}

type diskDetachRequest struct {
	Disk string `json:"disk"`
}

// AttachDisk handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/attach.
func (h *VMHandler) AttachDisk(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "vm"); err != nil {
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

	var req diskAttachRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Bus == "" || req.Storage == "" || req.Size == "" {
		return fiber.NewError(fiber.StatusBadRequest, "bus, storage, and size are required")
	}

	validBus := map[string]bool{"scsi": true, "sata": true, "virtio": true, "ide": true}
	if !validBus[req.Bus] {
		return fiber.NewError(fiber.StatusBadRequest, "bus must be one of: scsi, sata, virtio, ide")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	if err := pxClient.AttachDisk(c.Context(), node.Name, int(vm.Vmid), proxmox.DiskAttachParams{
		Bus:     req.Bus,
		Index:   req.Index,
		Storage: req.Storage,
		Size:    req.Size,
		Format:  req.Format,
	}); err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, vm.ID.String(), "disk_attach", nil)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "disk_attach")

	return c.JSON(vmActionResponse{
		UPID:   "",
		Status: "completed",
	})
}

// DetachDisk handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/detach.
func (h *VMHandler) DetachDisk(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "vm"); err != nil {
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

	var req diskDetachRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Disk == "" {
		return fiber.NewError(fiber.StatusBadRequest, "disk is required")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	if err := pxClient.DetachDisk(c.Context(), node.Name, int(vm.Vmid), req.Disk); err != nil {
		return mapProxmoxError(err)
	}

	h.auditLog(c, cluster.ID, vm.ID.String(), "disk_detach", nil)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "disk_detach")

	return c.JSON(vmActionResponse{
		UPID:   "",
		Status: "completed",
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
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
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

// auditLog writes an audit log entry and publishes an audit_entry event.
func (h *VMHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm", resourceID, action, details)
}

// --- Snapshot handlers ---

type snapshotRequest struct {
	SnapName    string `json:"snap_name"`
	Description string `json:"description"`
	VMState     bool   `json:"vmstate"`
}

type snapshotResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SnapTime    int64  `json:"snap_time,omitempty"`
	VMState     int    `json:"vmstate,omitempty"`
	Parent      string `json:"parent,omitempty"`
}

// ListSnapshots handles GET /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots.
func (h *VMHandler) ListSnapshots(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "vm"); err != nil {
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

	vm, node, _, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	snaps, err := pxClient.ListVMSnapshots(c.Context(), node.Name, int(vm.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := make([]snapshotResponse, 0, len(snaps))
	for _, s := range snaps {
		if s.Name == "current" {
			continue
		}
		resp = append(resp, snapshotResponse{
			Name:        s.Name,
			Description: s.Description,
			SnapTime:    s.SnapTime,
			VMState:     s.VMState,
			Parent:      s.Parent,
		})
	}

	return c.JSON(resp)
}

// CreateSnapshot handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots.
func (h *VMHandler) CreateSnapshot(c *fiber.Ctx) error {
	if err := requirePerm(c, "execute", "vm"); err != nil {
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

	var req snapshotRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.SnapName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "snap_name is required")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CreateVMSnapshot(c.Context(), node.Name, int(vm.Vmid), proxmox.SnapshotParams{
		SnapName:    req.SnapName,
		Description: req.Description,
		VMState:     req.VMState,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	detailsJSON, _ := json.Marshal(map[string]any{"upid": upid, "node": node.Name, "vmid": vm.Vmid})
	h.auditLog(c, cluster.ID, vm.ID.String(), "snapshot_create", detailsJSON)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "snapshot_create")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// DeleteSnapshot handles DELETE /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots/:snap_name.
func (h *VMHandler) DeleteSnapshot(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "vm"); err != nil {
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

	snapName := c.Params("snap_name")
	if snapName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Snapshot name is required")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.DeleteVMSnapshot(c.Context(), node.Name, int(vm.Vmid), snapName)
	if err != nil {
		return mapProxmoxError(err)
	}

	detailsJSON, _ := json.Marshal(map[string]any{"upid": upid, "node": node.Name, "vmid": vm.Vmid})
	h.auditLog(c, cluster.ID, vm.ID.String(), "snapshot_delete", detailsJSON)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "snapshot_delete")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// RollbackSnapshot handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots/:snap_name/rollback.
func (h *VMHandler) RollbackSnapshot(c *fiber.Ctx) error {
	if err := requirePerm(c, "execute", "vm"); err != nil {
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

	snapName := c.Params("snap_name")
	if snapName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Snapshot name is required")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.RollbackVMSnapshot(c.Context(), node.Name, int(vm.Vmid), snapName)
	if err != nil {
		return mapProxmoxError(err)
	}

	detailsJSON, _ := json.Marshal(map[string]any{"upid": upid, "node": node.Name, "vmid": vm.Vmid})
	h.auditLog(c, cluster.ID, vm.ID.String(), "snapshot_rollback", detailsJSON)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "snapshot_rollback")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// --- Create VM handler ---

type createVMRequest struct {
	VMID    int    `json:"vmid"`
	Name    string `json:"name"`
	Node    string `json:"node"`
	Memory  int    `json:"memory"`
	Cores   int    `json:"cores"`
	Sockets int    `json:"sockets"`
	SCSI0   string `json:"scsi0"`
	IDE2    string `json:"ide2"`
	Net0    string `json:"net0"`
	OSType  string `json:"ostype"`
	Boot    string `json:"boot"`
	CDRom   string `json:"cdrom"`
	Start   bool   `json:"start"`

	// System
	BIOS      string `json:"bios,omitempty"`
	Machine   string `json:"machine,omitempty"`
	ScsiHW    string `json:"scsihw,omitempty"`
	EFIDisk0  string `json:"efidisk0,omitempty"`
	TPMState0 string `json:"tpmstate0,omitempty"`
	Agent     string `json:"agent,omitempty"`

	// CPU
	CPUType string `json:"cpu,omitempty"`
	Numa    *bool  `json:"numa,omitempty"`

	// Memory
	Balloon *int `json:"balloon,omitempty"`

	// Display
	VGA string `json:"vga,omitempty"`

	// Boot / Options
	OnBoot  *bool  `json:"onboot,omitempty"`
	Hotplug string `json:"hotplug,omitempty"`
	Tablet  *bool  `json:"tablet,omitempty"`

	// Cloud-Init
	CIUser       string `json:"ciuser,omitempty"`
	CIPassword   string `json:"cipassword,omitempty"`
	SSHKeys      string `json:"sshkeys,omitempty"`
	IPConfig0    string `json:"ipconfig0,omitempty"`
	Nameserver   string `json:"nameserver,omitempty"`
	Searchdomain string `json:"searchdomain,omitempty"`

	// Meta
	Description string `json:"description,omitempty"`
	Tags        string `json:"tags,omitempty"`
	Pool        string `json:"pool,omitempty"`

	// Extra allows arbitrary additional Proxmox config fields (e.g. scsi1, ide0, sata0).
	Extra map[string]string `json:"extra,omitempty"`
}

// CreateVM handles POST /api/v1/clusters/:cluster_id/vms.
func (h *VMHandler) CreateVM(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "vm"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req createVMRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.VMID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "vmid is required and must be positive")
	}
	if req.Node == "" {
		return fiber.NewError(fiber.StatusBadRequest, "node is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CreateVM(c.Context(), req.Node, proxmox.CreateVMParams{
		VMID:    req.VMID,
		Name:    req.Name,
		Memory:  req.Memory,
		Cores:   req.Cores,
		Sockets: req.Sockets,
		SCSI0:   req.SCSI0,
		IDE2:    req.IDE2,
		Net0:    req.Net0,
		OSType:  req.OSType,
		Boot:    req.Boot,
		CDRom:   req.CDRom,
		Start:   req.Start,
		// System
		BIOS:      req.BIOS,
		Machine:   req.Machine,
		ScsiHW:    req.ScsiHW,
		EFIDisk0:  req.EFIDisk0,
		TPMState0: req.TPMState0,
		Agent:     req.Agent,
		// CPU
		CPUType: req.CPUType,
		Numa:    req.Numa,
		// Memory
		Balloon: req.Balloon,
		// Display
		VGA: req.VGA,
		// Boot / Options
		OnBoot:  req.OnBoot,
		Hotplug: req.Hotplug,
		Tablet:  req.Tablet,
		// Cloud-Init
		CIUser:       req.CIUser,
		CIPassword:   req.CIPassword,
		SSHKeys:      req.SSHKeys,
		IPConfig0:    req.IPConfig0,
		Nameserver:   req.Nameserver,
		Searchdomain: req.Searchdomain,
		// Meta
		Description: req.Description,
		Tags:        req.Tags,
		Pool:        req.Pool,
		// Extra (additional disks, CD-ROMs, etc.)
		Extra: req.Extra,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	detailsJSON, _ := json.Marshal(map[string]any{"upid": upid, "node": req.Node, "vmid": req.VMID})
	h.auditLog(c, clusterID, strconv.Itoa(req.VMID), "create", detailsJSON)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindInventoryChange, "vm", strconv.Itoa(req.VMID), "create")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// --- VM Config handlers (Cloud-Init) ---

// GetVMConfig handles GET /api/v1/clusters/:cluster_id/vms/:vm_id/config.
func (h *VMHandler) GetVMConfig(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "vm"); err != nil {
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

	vm, node, _, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	config, err := pxClient.GetVMConfig(c.Context(), node.Name, int(vm.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(config)
}

type setVMConfigRequest struct {
	Fields map[string]string `json:"fields"`
}

// SetVMConfig handles PUT /api/v1/clusters/:cluster_id/vms/:vm_id/config.
func (h *VMHandler) SetVMConfig(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "vm"); err != nil {
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

	var req setVMConfigRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if len(req.Fields) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "fields map is required")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	if err := pxClient.SetVMConfig(c.Context(), node.Name, int(vm.Vmid), req.Fields); err != nil {
		return mapProxmoxError(err)
	}

	configDetails, _ := json.Marshal(map[string]interface{}{"fields": req.Fields})
	h.auditLog(c, cluster.ID, vm.ID.String(), "config_update", configDetails)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "config_update")

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Machine types ---

type machineTypeResponse struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// ListMachineTypes handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/machine-types.
func (h *VMHandler) ListMachineTypes(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "node"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodeName := c.Params("node_name")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "node_name is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	types, err := pxClient.GetMachineTypes(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	result := make([]machineTypeResponse, 0, len(types))
	for _, t := range types {
		result = append(result, machineTypeResponse{
			ID:   t.ID,
			Type: t.Type,
		})
	}

	return c.JSON(result)
}

// --- Resource pools ---

type resourcePoolResponse struct {
	PoolID  string `json:"poolid"`
	Comment string `json:"comment,omitempty"`
}

// ListResourcePools handles GET /api/v1/clusters/:cluster_id/pools.
func (h *VMHandler) ListResourcePools(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cluster"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	pools, err := pxClient.GetResourcePools(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	result := make([]resourcePoolResponse, 0, len(pools))
	for _, p := range pools {
		result = append(result, resourcePoolResponse{
			PoolID:  p.PoolID,
			Comment: p.Comment,
		})
	}

	return c.JSON(result)
}

// --- Network bridges ---

type networkBridgeResponse struct {
	Iface   string `json:"iface"`
	Active  bool   `json:"active"`
	Address string `json:"address,omitempty"`
	CIDR    string `json:"cidr,omitempty"`
}

// ListBridges handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/bridges.
func (h *VMHandler) ListBridges(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "node"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodeName := c.Params("node_name")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "node_name is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	ifaces, err := pxClient.GetNetworkInterfaces(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	var bridges []networkBridgeResponse
	for _, iface := range ifaces {
		if iface.Type == "bridge" {
			bridges = append(bridges, networkBridgeResponse{
				Iface:   iface.Iface,
				Active:  iface.Active == 1,
				Address: iface.Address,
				CIDR:    iface.CIDR,
			})
		}
	}

	return c.JSON(bridges)
}

// --- Guest Agent ---

type guestAgentResponse struct {
	Running           bool                            `json:"running"`
	OSInfo            *proxmox.GuestOSInfo            `json:"os_info,omitempty"`
	NetworkInterfaces []proxmox.GuestNetworkInterface `json:"network_interfaces,omitempty"`
}

// GetGuestAgentInfo handles GET /api/v1/clusters/:cluster_id/vms/:vm_id/agent.
func (h *VMHandler) GetGuestAgentInfo(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "vm"); err != nil {
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

	vm, node, _, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	osInfo, err := pxClient.GetGuestAgentOSInfo(c.Context(), node.Name, int(vm.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	if osInfo == nil {
		return c.JSON(guestAgentResponse{Running: false})
	}

	ifaces, err := pxClient.GetGuestAgentNetworkInterfaces(c.Context(), node.Name, int(vm.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(guestAgentResponse{
		Running:           true,
		OSInfo:            osInfo,
		NetworkInterfaces: ifaces,
	})
}

// mapProxmoxError converts a Proxmox client error to an appropriate Fiber error.
func mapProxmoxError(err error) error {
	if errors.Is(err, proxmox.ErrNotFound) {
		return fiber.NewError(fiber.StatusNotFound, "Resource not found on Proxmox")
	}
	if errors.Is(err, proxmox.ErrForbidden) {
		if unwrapped := err.Error(); unwrapped != "" && unwrapped != "forbidden" {
			return fiber.NewError(fiber.StatusForbidden, "Proxmox API: "+unwrapped)
		}
		return fiber.NewError(fiber.StatusForbidden, "Proxmox API permission denied")
	}
	if errors.Is(err, proxmox.ErrConnectionFailed) {
		return fiber.NewError(fiber.StatusBadGateway, "Failed to connect to Proxmox")
	}
	var apiErr *proxmox.APIError
	if errors.As(err, &apiErr) {
		// Try to parse Proxmox validation error format:
		// {"errors":{"field":"message"},"message":"Parameter verification failed.\n","data":null}
		var pxResp struct {
			Message string            `json:"message"`
			Errors  map[string]string `json:"errors"`
		}
		if jsonErr := json.Unmarshal([]byte(apiErr.Message), &pxResp); jsonErr == nil && len(pxResp.Errors) > 0 {
			var parts []string
			for field, msg := range pxResp.Errors {
				parts = append(parts, field+": "+msg)
			}
			return fiber.NewError(fiber.StatusBadRequest, strings.Join(parts, "; "))
		}
		return fiber.NewError(fiber.StatusBadGateway, apiErr.Message)
	}
	return fiber.NewError(fiber.StatusInternalServerError, "Proxmox operation failed: "+err.Error())
}

// --- ISO Listing ---

type isoResponse struct {
	Volid   string `json:"volid"`
	Storage string `json:"storage"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	CTime   int64  `json:"ctime"`
}

// ListNodeISOs aggregates ISO images from all ISO-capable storage pools on a node.
func (h *VMHandler) ListNodeISOs(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "node"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodeName := c.Params("node_name")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "node_name is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	pools, err := pxClient.GetStoragePools(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	var isos []isoResponse
	for _, pool := range pools {
		if !strings.Contains(pool.Content, "iso") {
			continue
		}
		items, err := pxClient.GetStorageContent(c.Context(), nodeName, pool.Storage)
		if err != nil {
			// Skip pools that error (e.g. offline storage)
			continue
		}
		for _, item := range items {
			if item.Content != "iso" {
				continue
			}
			name := item.Volid
			if idx := strings.LastIndex(item.Volid, "/"); idx >= 0 {
				name = item.Volid[idx+1:]
			}
			isos = append(isos, isoResponse{
				Volid:   item.Volid,
				Storage: pool.Storage,
				Name:    name,
				Size:    item.Size,
				CTime:   item.CTime,
			})
		}
	}

	if isos == nil {
		isos = []isoResponse{}
	}

	return c.JSON(isos)
}

type changeMediaRequest struct {
	Volid string `json:"volid"` // "local:iso/file.iso" or "none" to eject
}

// ChangeMedia mounts or ejects a CD-ROM ISO on a VM.
// It detects the existing CD-ROM device from the VM config and uses POST for
// immediate hotplug (no reboot required).
func (h *VMHandler) ChangeMedia(c *fiber.Ctx) error {
	if err := requirePerm(c, "execute", "vm"); err != nil {
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

	var req changeMediaRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Volid == "" {
		return fiber.NewError(fiber.StatusBadRequest, "volid is required")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	// Read current config to find existing CD-ROM device key.
	config, err := pxClient.GetVMConfig(c.Context(), node.Name, int(vm.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	cdromKey := ""
	for key, val := range config {
		valStr, ok := val.(string)
		if !ok {
			continue
		}
		if strings.Contains(valStr, "media=cdrom") {
			cdromKey = key
			break
		}
	}

	// If no existing CD-ROM device and ejecting, nothing to do.
	if cdromKey == "" && req.Volid == "none" {
		return c.JSON(fiber.Map{"status": "ok"})
	}

	// Default to ide2 if no CD-ROM device exists yet.
	if cdromKey == "" {
		cdromKey = "ide2"
	}

	var value string
	if req.Volid == "none" {
		value = "none,media=cdrom"
	} else {
		value = req.Volid + ",media=cdrom"
	}

	// Use POST (UpdateVMConfigSync) for immediate hotplug — no reboot needed.
	if err := pxClient.UpdateVMConfigSync(c.Context(), node.Name, int(vm.Vmid), map[string]string{
		cdromKey: value,
	}); err != nil {
		return mapProxmoxError(err)
	}

	// Audit log.
	action := "media_mount"
	if req.Volid == "none" {
		action = "media_eject"
	}
	mediaDetails, _ := json.Marshal(map[string]interface{}{
		"device": cdromKey,
		"volid":  req.Volid,
	})
	h.auditLog(c, cluster.ID, vm.ID.String(), action, mediaDetails)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), action)

	return c.JSON(fiber.Map{"status": "ok", "device": cdromKey})
}

type vmMigrateRequest struct {
	Target string `json:"target"`
	Online bool   `json:"online"`
}

// MigrateVM handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/migrate.
func (h *VMHandler) MigrateVM(c *fiber.Ctx) error {
	if err := requirePerm(c, "execute", "vm"); err != nil {
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

	var req vmMigrateRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Target == "" {
		return fiber.NewError(fiber.StatusBadRequest, "target node is required")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.MigrateVM(c.Context(), node.Name, int(vm.Vmid), proxmox.MigrateParams{
		Target: req.Target,
		Online: req.Online,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	detailsJSON, _ := json.Marshal(map[string]any{"upid": upid, "node": node.Name, "vmid": vm.Vmid, "target": req.Target})
	h.auditLog(c, cluster.ID, vm.ID.String(), "migrate", detailsJSON)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindMigrationUpdate, "vm", vm.ID.String(), "migrate")

	// Track in activity panel.
	description := "Migrate VM " + strconv.Itoa(int(vm.Vmid)) + " → " + req.Target
	uid, _ := c.Locals("user_id").(uuid.UUID)
	_, _ = h.queries.InsertTaskHistory(c.Context(), db.InsertTaskHistoryParams{
		ClusterID:   cluster.ID,
		UserID:      uid,
		Upid:        upid,
		Description: description,
		Status:      "running",
		Node:        node.Name,
		TaskType:    "qmigrate",
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindTaskCreated, "task", upid, "qmigrate")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// ListNodeUSBDevices handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/hardware/usb.
func (h *VMHandler) ListNodeUSBDevices(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "node"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodeName := c.Params("node_name")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "node_name is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	devices, err := pxClient.ListNodeUSBDevices(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(devices)
}

// ListNodePCIDevices handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/hardware/pci.
func (h *VMHandler) ListNodePCIDevices(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "node"); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	nodeName := c.Params("node_name")
	if nodeName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "node_name is required")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	devices, err := pxClient.ListNodePCIDevices(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	return c.JSON(devices)
}

// SetVMPool handles PUT /api/v1/clusters/:cluster_id/vms/:vm_id/pool.
// Moves a VM/CT into a pool (or removes from current pool if pool is empty).
func (h *VMHandler) SetVMPool(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "pool"); err != nil {
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

	var body struct {
		Pool string `json:"pool"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	vm, err := h.queries.GetVM(c.Context(), vmID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "VM not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to look up VM")
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	vmidStr := strconv.Itoa(int(vm.Vmid))
	newPool := strings.TrimSpace(body.Pool)
	oldPool := vm.Pool

	// Remove from old pool if it had one.
	if oldPool != "" && oldPool != newPool {
		if err := pxClient.UpdateResourcePool(c.Context(), oldPool, proxmox.UpdatePoolParams{
			VMs:    vmidStr,
			Delete: "1",
		}); err != nil {
			return mapProxmoxError(err)
		}
	}

	// Add to new pool if specified.
	if newPool != "" && newPool != oldPool {
		if err := pxClient.UpdateResourcePool(c.Context(), newPool, proxmox.UpdatePoolParams{
			VMs: vmidStr,
		}); err != nil {
			return mapProxmoxError(err)
		}
	}

	// Update local DB immediately so the UI reflects the change.
	if err := h.queries.UpdateVMPool(c.Context(), db.UpdateVMPoolParams{
		ID:   vmID,
		Pool: newPool,
	}); err != nil {
		// Non-fatal: collector will sync eventually.
		_ = err
	}

	details, _ := json.Marshal(map[string]string{"old_pool": oldPool, "new_pool": newPool})
	h.auditLog(c, clusterID, vmID.String(), "set_pool", details)

	return c.JSON(fiber.Map{"pool": newPool})
}
