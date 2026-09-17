package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
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
	ID           uuid.UUID `json:"id"`
	ClusterID    uuid.UUID `json:"cluster_id"`
	NodeID       uuid.UUID `json:"node_id"`
	Vmid         int32     `json:"vmid"`
	Name         string    `json:"name"`
	Type         string    `json:"type"`
	Status       string    `json:"status"`
	CPUCount     int32     `json:"cpu_count"`
	MemTotal     int64     `json:"mem_total"`
	DiskTotal    int64     `json:"disk_total"`
	Uptime       int64     `json:"uptime"`
	Template     bool      `json:"template"`
	Tags         string    `json:"tags"`
	HaState      string    `json:"ha_state"`
	Pool         string    `json:"pool"`
	OSType       string    `json:"ostype"`
	ConfigOSType string    `json:"config_ostype"`

	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toVMResponse(v db.Vm) vmResponse {
	return vmResponse{
		ID:           v.ID,
		ClusterID:    v.ClusterID,
		NodeID:       v.NodeID,
		Vmid:         v.Vmid,
		Name:         v.Name,
		Type:         v.Type,
		Status:       v.Status,
		CPUCount:     v.CpuCount,
		MemTotal:     v.MemTotal,
		DiskTotal:    v.DiskTotal,
		Uptime:       v.Uptime,
		Template:     v.Template,
		Tags:         v.Tags,
		HaState:      v.HaState,
		Pool:         v.Pool,
		OSType:       v.Ostype,
		ConfigOSType: v.ConfigOstype,
		LastSeenAt:   v.LastSeenAt,
		CreatedAt:    v.CreatedAt,
		UpdatedAt:    v.UpdatedAt,
	}
}

// VMStatusActions is the set of power actions POST .../vms/:vm_id/status
// accepts, in the order the API documentation lists them.
//
// It is exported so that the endpoint's declaration in
// internal/api/registry_vms.go can use it as the parameter's enum: the
// list that validates the request and the list the switch in
// PerformAction covers are then the same list, and a new action cannot be
// accepted by one and dropped by the other.
var VMStatusActions = []string{"start", "stop", "shutdown", "reboot", "reset", "suspend", "resume"}

// vmCloneParams reads the body shared by clone and clone-to-template.
func vmCloneParams(p *apischema.Params) proxmox.CloneParams {
	return proxmox.CloneParams{
		NewID:   int(p.Int("new_id")),
		Name:    p.String("name"),
		Target:  p.String("target"),
		Full:    p.Bool("full"),
		Storage: p.String("storage"),
	}
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

// Every handler in this file is a registry endpoint: it is declared in
// internal/api/registry_vms.go, which states its parameters and its
// permission, and it receives the validated parameters instead of
// re-parsing the request.
//
// Two things that used to be at the top of each of these functions are
// deliberately absent. The require*Perm call now runs as route
// middleware, attached from the declaration — a handler that forgets it
// can no longer ship. And the body bind is gone: apischema has already
// coerced, format-checked and default-filled every parameter, and
// rejected any the endpoint does not declare.

// ListByCluster handles GET /api/v1/clusters/:cluster_id/vms.
func (h *VMHandler) ListByCluster(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	vms, err := h.queries.ListVMsByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list VMs")
	}

	resp := make([]vmResponse, len(vms))
	for i, v := range vms {
		resp[i] = toVMResponse(v)
	}

	return RespondItems(c, resp)
}

// guestInCluster loads a guest row and refuses one that belongs to a
// different cluster than the path named.
//
// The permission gate authorizes the cluster in the PATH, and nothing
// about a guest uuid says which cluster it belongs to — so a caller
// holding a grant on cluster A can put A in the path and B's guest id in
// it and, without this, be served B's row. resolveVM has always made this
// check; the two handlers that look a guest up directly did not, which
// was invisible while the permission check sat inline above them.
func (h *VMHandler) guestInCluster(c fiber.Ctx, clusterID, vmID uuid.UUID) (db.Vm, error) {
	vm, err := h.queries.GetVM(c.Context(), vmID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Vm{}, fiber.NewError(fiber.StatusNotFound, "VM not found")
		}
		return db.Vm{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to get VM")
	}
	if vm.ClusterID != clusterID {
		// 404 rather than 403, matching resolveVM: whether a guest exists
		// in a cluster the caller has no grant on is not theirs to learn.
		return db.Vm{}, fiber.NewError(fiber.StatusNotFound, "VM not found in this cluster")
	}
	return vm, nil
}

// GetVM handles GET /api/v1/clusters/:cluster_id/vms/:vm_id.
func (h *VMHandler) GetVM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}

	vm, err := h.guestInCluster(c, clusterID, vmID)
	if err != nil {
		return err
	}

	return c.JSON(toVMResponse(vm))
}

// PerformAction handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/status.
func (h *VMHandler) PerformAction(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}
	// The schema's enum is VMStatusActions, the same list the switch below
	// covers, so an unknown action never reaches here.
	action := p.String("action")

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	var upid string
	switch action {
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

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "vm",
		ResourceID:   vm.ID.String(),
		ResourceName: vm.Name,
		Action:       action,
		UPID:         upid,
		Description:  guestActionDesc(action, vm),
		Extra:        map[string]any{"vmid": vm.Vmid},
	})

	// Watch the task in the background and update the DB when it completes.
	// The watcher publishes a vm_state_change event only after the DB is updated
	// with the real status, avoiding premature refetches of stale data.
	watchTaskAndUpdateStatus(h.queries, h.eventPub, pxClient, node.Name, upid, vm.ID, cluster.ID, action, "vm")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// requireGuestKindPerm re-checks the permission for what the guest
// ACTUALLY is, on the two routes that serve both kinds through one path.
//
// POST .../vms/:vm_id/convert-to-template and .../clone-to-template both
// branch on vm.Type and call the LXC client method for a container, so a
// route documented and gated as manage:vm can irreversibly convert a
// container — and manage:container is a separate permission row that a
// cluster-scoped or custom role can withhold. The path spells "vms"
// because Nexara's inventory keeps both kinds in one table, not because
// the object is a VM.
//
// This is why both routes declare Permissions.Deferred: the resource
// cannot be known until the guest row is loaded, and a gate that ran
// before the lookup would have to guess. The static half (manage:vm) is
// still checked first, in the handler, so the route is never reachable on
// container rights alone.
//
// It is NOT expressed as Alternatives: "manage:vm OR manage:container"
// would widen the route where it needs narrowing — the point is that a
// container conversion demands container rights, not that either will do.
func requireGuestKindPerm(c fiber.Ctx, action, guestType string, clusterID uuid.UUID) error {
	if guestType != "lxc" {
		return nil
	}
	return requireClusterPerm(c, action, "container", clusterID)
}

// guestActionDesc builds a concise task description for a guest (VM/CT) action,
// e.g. "clone linux11 (103)". Shared by the VM and container handlers.
func guestActionDesc(action string, vm db.Vm) string {
	if vm.Name != "" {
		return action + " " + vm.Name + " (" + strconv.Itoa(int(vm.Vmid)) + ")"
	}
	return action + " " + strconv.Itoa(int(vm.Vmid))
}

// CloneVM handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/clone.
func (h *VMHandler) CloneVM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}
	clone := vmCloneParams(p)

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CloneVM(c.Context(), node.Name, int(vm.Vmid), clone)
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "vm",
		ResourceID:   vm.ID.String(),
		ResourceName: vm.Name,
		Action:       "clone",
		UPID:         upid,
		Description:  guestActionDesc("clone", vm),
		Extra:        map[string]any{"vmid": vm.Vmid, "new_id": clone.NewID},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "vm", vm.ID.String(), "clone")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// ConvertToTemplate handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/convert-to-template.
// This converts a stopped VM to a template. The operation is irreversible in Proxmox.
func (h *VMHandler) ConvertToTemplate(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "vm", clusterID); err != nil {
		return err
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}
	if err := requireGuestKindPerm(c, "manage", vm.Type, clusterID); err != nil {
		return err
	}

	if vm.Template {
		return fiber.NewError(fiber.StatusConflict, "Resource is already a template")
	}

	if vm.Status != "stopped" {
		return fiber.NewError(fiber.StatusConflict, "Resource must be stopped before converting to template")
	}

	var upid string
	if vm.Type == "lxc" {
		upid, err = pxClient.ConvertCTToTemplate(c.Context(), node.Name, int(vm.Vmid))
	} else {
		upid, err = pxClient.ConvertVMToTemplate(c.Context(), node.Name, int(vm.Vmid))
	}
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "vm",
		ResourceID:   vm.ID.String(),
		ResourceName: vm.Name,
		Action:       "convert-to-template",
		UPID:         upid,
		Description:  guestActionDesc("convert-to-template", vm),
		Extra:        map[string]any{"vmid": vm.Vmid},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "vm", vm.ID.String(), "convert-to-template")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// CloneToTemplate handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/clone-to-template.
// This clones a VM/CT and then automatically converts the clone to a template.
func (h *VMHandler) CloneToTemplate(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "vm", clusterID); err != nil {
		return err
	}
	clone := vmCloneParams(p)

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}
	if err := requireGuestKindPerm(c, "manage", vm.Type, clusterID); err != nil {
		return err
	}

	// Step 1: Clone the VM/CT
	var cloneUpid string
	if vm.Type == "lxc" {
		cloneUpid, err = pxClient.CloneCT(c.Context(), node.Name, int(vm.Vmid), clone)
	} else {
		cloneUpid, err = pxClient.CloneVM(c.Context(), node.Name, int(vm.Vmid), clone)
	}
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "vm",
		ResourceID:   vm.ID.String(),
		ResourceName: vm.Name,
		Action:       "clone-to-template",
		UPID:         cloneUpid,
		Description:  guestActionDesc("clone-to-template", vm),
		Extra:        map[string]any{"vmid": vm.Vmid, "new_id": clone.NewID, "clone_to_template": true},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "vm", vm.ID.String(), "clone-to-template")

	// Step 2: Background goroutine polls clone task then converts clone to template
	targetNode := node.Name
	if clone.Target != "" {
		targetNode = clone.Target
	}
	go h.convertCloneToTemplate(pxClient, targetNode, clone.NewID, vm.Type, cluster.ID.String()) //nolint:gosec // G118: intentionally detached — clone→template conversion must outlive the request (Fiber recycles the request context)

	return c.JSON(vmActionResponse{
		UPID:   cloneUpid,
		Status: "dispatched",
	})
}

// convertCloneToTemplate polls a clone task and converts the result to a template.
func (h *VMHandler) convertCloneToTemplate(pxClient *proxmox.Client, node string, newVMID int, vmType string, clusterID string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("clone-to-template watcher panicked",
				"vmid", newVMID, "cluster_id", clusterID, "panic", r)
		}
	}()

	// Use a detached context with a generous timeout for the background operation
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Wait for the clone to appear as a stopped VM on the node (Proxmox doesn't
	// return a UPID for template conversion if the VM doesn't exist yet)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check if the clone VM exists and is stopped by trying to get its status
			var vms []proxmox.VirtualMachine
			var cts []proxmox.Container
			var err error

			if vmType == "lxc" {
				cts, err = pxClient.GetContainers(ctx, node)
				if err != nil {
					continue
				}
				found := false
				for _, ct := range cts {
					if ct.VMID == newVMID && ct.Status == "stopped" && ct.Template == 0 {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			} else {
				vms, err = pxClient.GetVMs(ctx, node)
				if err != nil {
					continue
				}
				found := false
				for _, vm := range vms {
					if vm.VMID == newVMID && vm.Status == "stopped" && vm.Template == 0 {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			// Clone exists and is stopped — convert to template
			if vmType == "lxc" {
				_, _ = pxClient.ConvertCTToTemplate(ctx, node, newVMID)
			} else {
				_, _ = pxClient.ConvertVMToTemplate(ctx, node, newVMID)
			}
			h.eventPub.ClusterEvent(ctx, clusterID, events.KindInventoryChange, "vm", "", "clone-to-template-complete")
			return
		}
	}
}

// DestroyVM handles DELETE /api/v1/clusters/:cluster_id/vms/:vm_id.
func (h *VMHandler) DestroyVM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.DestroyVM(c.Context(), node.Name, int(vm.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "vm",
		ResourceID:   vm.ID.String(),
		ResourceName: vm.Name,
		Action:       "destroy",
		UPID:         upid,
		Description:  guestActionDesc("destroy", vm),
		Extra:        map[string]any{"vmid": vm.Vmid},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindInventoryChange, "vm", vm.ID.String(), "destroy")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// taskUPID reads the :upid path parameter and the node it names.
//
// The schema deliberately puts no pattern on it: the frontend
// percent-encodes the UPID's colons, Fiber does not decode path
// parameters, and the only check worth making is that a node name falls
// out of the decoded form — which is what this does.
func taskUPID(p *apischema.Params) (upid, node string, err error) {
	raw := p.String("upid")
	upid, unescapeErr := url.PathUnescape(raw)
	if unescapeErr != nil {
		upid = raw // fall back to raw value
	}

	// UPID format: UPID:<node>:<pid_hex>:<pstart_hex>:<starttime_hex>:<type>:<id>:<user>@<realm>:
	node = extractNodeFromUPID(upid)
	if node == "" {
		return "", "", fiber.NewError(fiber.StatusBadRequest, "Could not extract node from UPID")
	}
	return upid, node, nil
}

// GetTaskStatus handles GET /api/v1/clusters/:cluster_id/tasks/:upid.
func (h *VMHandler) GetTaskStatus(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	upid, nodeName, err := taskUPID(p)
	if err != nil {
		return err
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
	// Proxmox emits progress in several formats:
	//   1. "progress 0.50"                                                                  (generic tasks)
	//   2. "drive-ide0: transferred 1.0 GiB of 32.0 GiB (1.00%) in 5s"                    (move disk)
	//   3. "transferred 1.0 GiB of 100.0 GiB (1.00%)"                                     (clone)
	//   4. "migration active, transferred 5.2 GiB of 16.0 GiB VM-state, 833.4 MiB/s"      (live migration)
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
				// Parse "(Z%)" anywhere in the line — covers disk move/clone formats.
				if pctEnd := strings.Index(line, "%)"); pctEnd != -1 {
					if pctStart := strings.LastIndex(line[:pctEnd], "("); pctStart != -1 {
						pctStr := line[pctStart+1 : pctEnd]
						if pct, parseErr := strconv.ParseFloat(pctStr, 64); parseErr == nil {
							p := pct / 100.0
							resp.Progress = &p
						}
						break
					}
				}
				// Parse "transferred X <unit> of Y <unit>" without percentage — live migrations.
				if p := parseTransferredProgress(line); p != nil {
					resp.Progress = p
					break
				}
			}
		}
	}

	return c.JSON(resp)
}

// GetTaskLog returns the log lines for a Proxmox task.
func (h *VMHandler) GetTaskLog(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	upid, nodeName, err := taskUPID(p)
	if err != nil {
		return err
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

	return RespondItems(c, result)
}

// ResizeDisk handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/resize.
func (h *VMHandler) ResizeDisk(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	if err := pxClient.ResizeDisk(c.Context(), node.Name, int(vm.Vmid), proxmox.DiskResizeParams{
		Disk: p.String("disk"),
		Size: p.String("size"),
	}); err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(cluster.ID), "vm", vm.ID.String(), "disk_resize", nil)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "disk_resize")

	return c.JSON(vmActionResponse{
		UPID:   "",
		Status: "completed",
	})
}

// withVMID adds the guest's VMID to a disk-move detail map. The move spec is
// storage-level and VMID-agnostic; every recorded task wants it alongside.
func withVMID(extra map[string]any, vmid int32) map[string]any {
	extra["vmid"] = vmid
	return extra
}

// MoveDisk handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/move.
func (h *VMHandler) MoveDisk(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}

	spec := proxmox.DiskMoveSpec{
		Disk:          p.String("disk"),
		TargetStorage: p.String("storage"),
		Format:        p.String("format"),
		DeleteSource:  p.Bool("delete"),
		BWLimitKiB:    int(p.Int("bwlimit_kib")),
	}
	// Still validated here, not only in the schema: the format vocabulary
	// lives in proxmox.ValidImageFormat, and this is the choke point every
	// caller of MoveDisk goes through.
	if err := spec.Validate(); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.MoveDisk(c.Context(), node.Name, int(vm.Vmid), spec.VMParams())
	if err != nil {
		return mapProxmoxError(err)
	}

	description := "Move disk " + spec.Summary()
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "vm",
		ResourceID:   vm.ID.String(),
		ResourceName: vm.Name,
		Action:       "disk_move",
		UPID:         upid,
		TaskType:     "qmmove",
		Description:  description,
		Extra:        withVMID(spec.AuditExtra(), vm.Vmid),
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "disk_move")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// --- Disk Attach/Detach ---

// AttachDisk handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/attach.
//
// The decisions live in planDiskAttach (vm_disk_attach.go), which is
// where the reasoning for each of them is written down; this function is
// the wiring. Note what it does NOT do: it never reads an index without
// also asking whether the caller supplied one. That single distinction is
// what separates "attach a disk" from "silently replace the boot disk",
// and it is only expressible because the schema declares index as
// Optional with no default.
//
// It records an AuditLog rather than a TrackTask, and that is correct:
// AttachDisk writes the VM's config with a synchronous PUT and Proxmox
// returns no UPID, so there is no task to track. See the TrackTask rule
// in internal/api/CLAUDE.md, enforced by tracktask_guard_test.go.
func (h *VMHandler) AttachDisk(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}

	req, err := diskAttachRequestFrom(p)
	if err != nil {
		return err
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	attach, err := planDiskAttach(c.Context(), pxClient, node.Name, int(vm.Vmid), req)
	if err != nil {
		return err
	}

	if err := pxClient.AttachDisk(c.Context(), node.Name, int(vm.Vmid), attach); err != nil {
		return mapProxmoxError(err)
	}

	// The slot, the storage and the size are recorded because the incident
	// that motivated this endpoint's rewrite left no record of WHICH slot
	// had been written — only that a disk_attach had happened. None of
	// these is a secret (view:audit is granted to every Viewer by default;
	// see the note on Params.Raw), and all three are what the next reader
	// needs.
	attachDetails, _ := json.Marshal(map[string]any{
		"disk":      attach.DiskKey(),
		"storage":   attach.Storage,
		"size_gib":  req.SizeGiB,
		"slot_auto": !req.HasIndex,
		"vmid":      vm.Vmid,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(cluster.ID), "vm", vm.ID.String(), "disk_attach", attachDetails)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "disk_attach")

	return c.JSON(fiber.Map{
		"upid":   "",
		"status": "completed",
		"disk":   attach.DiskKey(),
	})
}

// DetachDisk handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/detach.
func (h *VMHandler) DetachDisk(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	if err := pxClient.DetachDisk(c.Context(), node.Name, int(vm.Vmid), p.String("disk")); err != nil {
		return mapProxmoxError(err)
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(cluster.ID), "vm", vm.ID.String(), "disk_detach", nil)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "disk_detach")

	return c.JSON(vmActionResponse{
		UPID:   "",
		Status: "completed",
	})
}

// resolveVM loads the VM, its node, the cluster, and creates a Proxmox client.
func (h *VMHandler) resolveVM(c fiber.Ctx, clusterID, vmID uuid.UUID) (db.Vm, db.Node, db.Cluster, *proxmox.Client, error) {
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

	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
	if err != nil {
		return zeroVM, zeroNode, zeroCluster, nil, err
	}

	return vm, node, cluster, pxClient, nil
}

// createProxmoxClient creates a Proxmox client for the given cluster.
func (h *VMHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
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

// parseTransferredProgress extracts progress from Proxmox log lines like:
//
//	"migration active, transferred 5.2 GiB of 16.0 GiB VM-state, 833.4 MiB/s"
//
// These lines don't include a percentage — we compute transferred/total.
func parseTransferredProgress(line string) *float64 {
	idx := strings.Index(line, "transferred ")
	if idx == -1 {
		return nil
	}
	rest := line[idx+len("transferred "):]

	ofIdx := strings.Index(rest, " of ")
	if ofIdx == -1 {
		return nil
	}

	xferStr := rest[:ofIdx]
	afterOf := rest[ofIdx+len(" of "):]

	xfer := parseSizeToBytes(xferStr)
	total := parseSizeToBytes(afterOf)
	if xfer < 0 || total <= 0 {
		return nil
	}
	p := xfer / total
	if p > 1.0 {
		p = 1.0
	}
	return &p
}

// parseSizeToBytes parses a Proxmox size string like "5.2 GiB" or "736.3 MiB"
// into a byte count as float64. Returns -1 on failure.
func parseSizeToBytes(s string) float64 {
	s = strings.TrimSpace(s)
	// Split at first space or non-numeric/dot character to get "5.2" and "GiB ..."
	numEnd := 0
	for numEnd < len(s) && (s[numEnd] == '.' || (s[numEnd] >= '0' && s[numEnd] <= '9')) {
		numEnd++
	}
	if numEnd == 0 {
		return -1
	}
	val, err := strconv.ParseFloat(s[:numEnd], 64)
	if err != nil {
		return -1
	}

	unit := strings.TrimSpace(s[numEnd:])
	// Stop at first space or comma after the unit (e.g. "GiB VM-state," → "GiB")
	if spIdx := strings.IndexAny(unit, " ,"); spIdx != -1 {
		unit = unit[:spIdx]
	}

	switch strings.ToLower(unit) {
	case "b":
		return val
	case "kib":
		return val * 1024
	case "mib":
		return val * 1024 * 1024
	case "gib":
		return val * 1024 * 1024 * 1024
	case "tib":
		return val * 1024 * 1024 * 1024 * 1024
	default:
		return -1
	}
}

// --- Snapshot handlers ---

type snapshotResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SnapTime    int64  `json:"snap_time,omitempty"`
	VMState     int    `json:"vmstate,omitempty"`
	Parent      string `json:"parent,omitempty"`
}

// snapshotNameRE mirrors Proxmox's pve-configid format for snapshot names
// (a leading letter, then letters, digits, '-' or '_') plus the API's
// 40-character cap.
var snapshotNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,39}$`)

// validateSnapshotName rejects names Proxmox would refuse, with an
// actionable message instead of PVE's "invalid configid" error.
func validateSnapshotName(name string) error {
	switch {
	case name == "":
		return errors.New("snap_name is required")
	case name == "current":
		return errors.New(`snap_name "current" is reserved by Proxmox`)
	case !snapshotNameRE.MatchString(name):
		return errors.New("snap_name must start with a letter and contain only letters, digits, '-' and '_' (no spaces), 2-40 characters")
	}
	return nil
}

type snapshotCapabilityResponse struct {
	Supported       bool     `json:"supported"`
	BlockingVolumes []string `json:"blocking_volumes"`
}

// fileBackedStorageTypes are storage types where guest volumes live as plain
// files: only qcow2 images support snapshots there — raw (and vmdk) do not.
var fileBackedStorageTypes = map[string]bool{
	"dir":       true,
	"nfs":       true,
	"cifs":      true,
	"glusterfs": true,
}

// volumeKeyRE matches config keys referencing guest volumes relevant to the
// snapshot-capability hint (disks, EFI vars, TPM state, CT rootfs/mounts).
var volumeKeyRE = regexp.MustCompile(`^(scsi|ide|sata|virtio|mp)\d+$|^(efidisk0|tpmstate0|rootfs)$`)

// snapshotBlockingVolumes lists volumes that likely prevent snapshots:
// non-qcow2 images on file-backed storage and passthrough devices. It is a
// best-effort hint for the UI — Proxmox's feature check is the authority on
// whether the guest can snapshot at all.
func snapshotBlockingVolumes(config proxmox.VMConfig, storageTypes map[string]string) []string {
	blocking := []string{}
	for key, raw := range config {
		if !volumeKeyRE.MatchString(key) {
			continue
		}
		val, ok := raw.(string)
		if !ok || strings.Contains(val, "media=cdrom") {
			continue
		}
		volume, _, _ := strings.Cut(val, ",")
		if strings.HasPrefix(volume, "/") {
			blocking = append(blocking, key+" (passthrough device)")
			continue
		}
		storage, _, found := strings.Cut(volume, ":")
		if !found || !fileBackedStorageTypes[storageTypes[storage]] {
			continue
		}
		if strings.HasSuffix(volume, ".qcow2") {
			continue
		}
		blocking = append(blocking, key+" on "+storage)
	}
	sort.Strings(blocking)
	return blocking
}

// storageTypesByName maps a cluster's storage pool names to their types from
// the collector-synced inventory. Best-effort: empty on error, which just
// suppresses the blocking-volume hint.
func storageTypesByName(c fiber.Ctx, queries *db.Queries, clusterID uuid.UUID) map[string]string {
	types := map[string]string{}
	pools, err := queries.ListStoragePoolsByCluster(c.Context(), clusterID)
	if err != nil {
		return types
	}
	for _, p := range pools {
		types[p.Storage] = p.Type
	}
	return types
}

// GetSnapshotCapability handles GET /api/v1/clusters/:cluster_id/vms/:vm_id/snapshot-capability.
func (h *VMHandler) GetSnapshotCapability(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}

	vm, node, _, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	supported, err := pxClient.GetVMSnapshotFeature(c.Context(), node.Name, int(vm.Vmid))
	if err != nil {
		return mapProxmoxError(err)
	}

	resp := snapshotCapabilityResponse{Supported: supported, BlockingVolumes: []string{}}
	if !supported {
		if config, cfgErr := pxClient.GetVMConfig(c.Context(), node.Name, int(vm.Vmid)); cfgErr == nil {
			resp.BlockingVolumes = snapshotBlockingVolumes(config, storageTypesByName(c, h.queries, clusterID))
		}
	}
	return c.JSON(resp)
}

// ListSnapshots handles GET /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots.
func (h *VMHandler) ListSnapshots(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
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

	return RespondItems(c, resp)
}

// CreateSnapshot handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots.
func (h *VMHandler) CreateSnapshot(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}

	// The schema's "pve-configid" format covers the shape; this covers the
	// one rule that is not a shape — Proxmox reserves the name "current"
	// for the live state, and a snapshot called that can never be rolled
	// back to.
	snapName := p.String("snap_name")
	if err := validateSnapshotName(snapName); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CreateVMSnapshot(c.Context(), node.Name, int(vm.Vmid), proxmox.SnapshotParams{
		SnapName:    snapName,
		Description: p.String("description"),
		VMState:     p.Bool("vmstate"),
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "vm",
		ResourceID:   vm.ID.String(),
		ResourceName: vm.Name,
		Action:       "snapshot_create",
		UPID:         upid,
		Description:  guestActionDesc("snapshot_create", vm),
		Extra:        map[string]any{"vmid": vm.Vmid, "snap_name": snapName},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "snapshot_create")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// DeleteSnapshot handles DELETE /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots/:snap_name.
func (h *VMHandler) DeleteSnapshot(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}
	snapName := p.String("snap_name")

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.DeleteVMSnapshot(c.Context(), node.Name, int(vm.Vmid), snapName)
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "vm",
		ResourceID:   vm.ID.String(),
		ResourceName: vm.Name,
		Action:       "snapshot_delete",
		UPID:         upid,
		Description:  guestActionDesc("snapshot_delete", vm),
		Extra:        map[string]any{"vmid": vm.Vmid, "snap_name": snapName},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "snapshot_delete")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// RollbackSnapshot handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots/:snap_name/rollback.
func (h *VMHandler) RollbackSnapshot(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}
	snapName := p.String("snap_name")

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.RollbackVMSnapshot(c.Context(), node.Name, int(vm.Vmid), snapName)
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "vm",
		ResourceID:   vm.ID.String(),
		ResourceName: vm.Name,
		Action:       "snapshot_rollback",
		UPID:         upid,
		Description:  guestActionDesc("snapshot_rollback", vm),
		Extra:        map[string]any{"vmid": vm.Vmid, "snap_name": snapName},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "snapshot_rollback")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// --- Create VM handler ---

// CreateVM handles POST /api/v1/clusters/:cluster_id/vms.
func (h *VMHandler) CreateVM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// extra carries whatever Proxmox config keys the schema does not name
	// — further disks, CD-ROM slots — as a flat object.
	extra, err := stringMap("extra", p.Object("extra"))
	if err != nil {
		return err
	}

	vmid := int(p.Int("vmid"))
	node := p.String("node")
	name := p.String("name")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	upid, err := pxClient.CreateVM(c.Context(), node, proxmox.CreateVMParams{
		VMID:    vmid,
		Name:    name,
		Memory:  int(p.Int("memory")),
		Cores:   int(p.Int("cores")),
		Sockets: int(p.Int("sockets")),
		SCSI0:   p.String("scsi0"),
		IDE2:    p.String("ide2"),
		Net0:    p.String("net0"),
		OSType:  p.String("ostype"),
		Boot:    p.String("boot"),
		CDRom:   p.String("cdrom"),
		Start:   p.Bool("start"),
		// System
		BIOS:      p.String("bios"),
		Machine:   p.String("machine"),
		ScsiHW:    p.String("scsihw"),
		EFIDisk0:  p.String("efidisk0"),
		TPMState0: p.String("tpmstate0"),
		Agent:     p.String("agent"),
		// CPU. Numa, OnBoot and Tablet are *bool because "the caller did
		// not mention it" and "the caller said false" are different
		// instructions to Proxmox, and optBoolPtr keeps them apart.
		CPUType: p.String("cpu"),
		Numa:    optBoolPtr(p.OptBool("numa")),
		// Memory
		Balloon: optIntPtr(p.OptInt("balloon")),
		// Display
		VGA: p.String("vga"),
		// Boot / Options
		OnBoot:  optBoolPtr(p.OptBool("onboot")),
		Hotplug: p.String("hotplug"),
		Tablet:  optBoolPtr(p.OptBool("tablet")),
		// Cloud-Init
		CIUser:       p.String("ciuser"),
		CIPassword:   p.String("cipassword"),
		SSHKeys:      p.String("sshkeys"),
		IPConfig0:    p.String("ipconfig0"),
		Nameserver:   p.String("nameserver"),
		Searchdomain: p.String("searchdomain"),
		// Meta
		Description: p.String("description"),
		Tags:        p.String("tags"),
		Pool:        p.String("pool"),
		// Extra (additional disks, CD-ROMs, etc.)
		Extra: extra,
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         node,
		ResourceType: "vm",
		ResourceID:   strconv.Itoa(vmid),
		ResourceName: name,
		Action:       "create",
		UPID:         upid,
		Description:  "create VM " + strconv.Itoa(vmid),
		Extra:        map[string]any{"vmid": vmid},
	})
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindInventoryChange, "vm", strconv.Itoa(vmid), "create")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// --- VM Config handlers (Cloud-Init) ---

// GetVMConfig handles GET /api/v1/clusters/:cluster_id/vms/:vm_id/config.
func (h *VMHandler) GetVMConfig(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
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

// SetVMConfig handles PUT /api/v1/clusters/:cluster_id/vms/:vm_id/config.
func (h *VMHandler) SetVMConfig(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}

	fields, err := stringMap("fields", p.Object("fields"))
	if err != nil {
		return err
	}
	// The schema requires the parameter; it cannot require the object to
	// hold anything, and a config write with nothing in it would be a
	// no-op Proxmox round trip that still writes an audit row claiming a
	// change.
	if len(fields) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "fields map is required")
	}

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	if err := pxClient.SetVMConfig(c.Context(), node.Name, int(vm.Vmid), fields); err != nil {
		return mapProxmoxError(err)
	}

	configDetails, _ := json.Marshal(map[string]interface{}{"fields": fields})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(cluster.ID), "vm", vm.ID.String(), "config_update", configDetails)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), "config_update")

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Machine types ---

type machineTypeResponse struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// ListMachineTypes handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/machine-types.
func (h *VMHandler) ListMachineTypes(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeName := p.String("node_name")

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

	return RespondItems(c, result)
}

// --- CPU models ---

type cpuModelResponse struct {
	Name   string `json:"name"`
	Vendor string `json:"vendor"`
	Custom bool   `json:"custom"`
}

// ListCPUModels handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/cpu-models.
func (h *VMHandler) ListCPUModels(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeName := p.String("node_name")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	models, err := pxClient.GetCPUModels(c.Context(), nodeName)
	if err != nil {
		// Graceful fallback: older Proxmox versions (< 8.1) don't implement
		// the capabilities/qemu/cpus endpoint. Return an empty list so the
		// frontend falls back to its built-in CPU model list.
		var apiErr *proxmox.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 501 {
			return RespondItems(c, []cpuModelResponse{})
		}
		return mapProxmoxError(err)
	}

	result := make([]cpuModelResponse, 0, len(models))
	for _, m := range models {
		result = append(result, cpuModelResponse{
			Name:   m.Name,
			Vendor: m.Vendor,
			Custom: m.Custom != 0,
		})
	}

	return RespondItems(c, result)
}

// --- CPU flags ---

type cpuFlagResponse struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Pointer so the three outcomes stay distinct: a node list, an empty list
	// (Proxmox checked and no node supports the flag), and null (Proxmox did
	// not report support at all). Collapsing the last two would let the UI
	// claim a flag is unsupported everywhere when it simply does not know.
	SupportedOn *[]string `json:"supported_on"`
}

// ListCPUFlags handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/cpu-flags.
func (h *VMHandler) ListCPUFlags(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeName := p.String("node_name")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	flags, err := pxClient.GetCPUFlags(c.Context(), nodeName)
	if err != nil {
		// Graceful fallback, matching ListCPUModels: Proxmox versions without
		// the capabilities/qemu/cpu-flags endpoint answer 501. Return an empty
		// list so the frontend falls back to its built-in flag list.
		var apiErr *proxmox.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 501 {
			return RespondItems(c, []cpuFlagResponse{})
		}
		return mapProxmoxError(err)
	}

	result := make([]cpuFlagResponse, 0, len(flags))
	for _, f := range flags {
		result = append(result, cpuFlagResponse{
			Name:        f.Name,
			Description: f.Description,
			SupportedOn: f.SupportedOn,
		})
	}

	return RespondItems(c, result)
}

// --- Resource pools ---

type resourcePoolResponse struct {
	PoolID  string `json:"poolid"`
	Comment string `json:"comment,omitempty"`
}

// ListResourcePools handles GET /api/v1/clusters/:cluster_id/pools.
func (h *VMHandler) ListResourcePools(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
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

	return RespondItems(c, result)
}

// --- Network bridges ---

type networkBridgeResponse struct {
	Iface   string `json:"iface"`
	Active  bool   `json:"active"`
	Address string `json:"address,omitempty"`
	CIDR    string `json:"cidr,omitempty"`
}

// ListBridges handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/bridges.
func (h *VMHandler) ListBridges(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeName := p.String("node_name")

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

	return RespondItems(c, bridges)
}

// --- Guest Agent ---

type guestAgentResponse struct {
	Running           bool                            `json:"running"`
	OSInfo            *proxmox.GuestOSInfo            `json:"os_info,omitempty"`
	NetworkInterfaces []proxmox.GuestNetworkInterface `json:"network_interfaces,omitempty"`
}

// GetGuestAgentInfo handles GET /api/v1/clusters/:cluster_id/vms/:vm_id/agent.
func (h *VMHandler) GetGuestAgentInfo(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
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

// --- ISO Listing ---

type isoResponse struct {
	Volid   string `json:"volid"`
	Storage string `json:"storage"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	CTime   int64  `json:"ctime"`
}

// ListNodeISOs aggregates ISO images from all ISO-capable storage pools on a node.
func (h *VMHandler) ListNodeISOs(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeName := p.String("node_name")

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
			isos = append(isos, isoResponse{
				Volid:   item.Volid,
				Storage: pool.Storage,
				Name:    proxmox.VolumeFilename(item.Volid),
				Size:    item.Size,
				CTime:   item.CTime,
			})
		}
	}

	if isos == nil {
		isos = []isoResponse{}
	}

	return RespondItems(c, isos)
}

// ChangeMedia mounts or ejects a CD-ROM ISO on a VM.
// It detects the existing CD-ROM device from the VM config and writes the
// change synchronously, so the "ok" it answers with means the media really
// changed — and a hotplug rejection comes back as this request's 400 rather
// than as a 200 followed by a task nobody opened. A running guest sees the
// change without a reboot either way: hotplug is a property of the config
// update, not of the verb that asked for it.
func (h *VMHandler) ChangeMedia(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}
	volid := p.String("volid")

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
	if cdromKey == "" && volid == "none" {
		return c.JSON(fiber.Map{"status": "ok"})
	}

	// Default to ide2 if no CD-ROM device exists yet.
	if cdromKey == "" {
		cdromKey = "ide2"
	}

	var value string
	if volid == "none" {
		value = "none,media=cdrom"
	} else {
		value = volid + ",media=cdrom"
	}

	// Synchronous: a media change is a config edit, not a long-running job, so
	// there is no task worth tracking and every failure mode belongs in this
	// response rather than in an audit row that already claimed success.
	if err := pxClient.SetVMConfig(c.Context(), node.Name, int(vm.Vmid), map[string]string{
		cdromKey: value,
	}); err != nil {
		return mapProxmoxError(err)
	}

	// Audit log.
	action := "media_mount"
	if volid == "none" {
		action = "media_eject"
	}
	mediaDetails, _ := json.Marshal(map[string]interface{}{
		"device": cdromKey,
		"volid":  volid,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(cluster.ID), "vm", vm.ID.String(), action, mediaDetails)
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindVMStateChange, "vm", vm.ID.String(), action)

	return c.JSON(fiber.Map{"status": "ok", "device": cdromKey})
}

// MigrateVM handles POST /api/v1/clusters/:cluster_id/vms/:vm_id/migrate.
func (h *VMHandler) MigrateVM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}
	target := p.String("target")

	vm, node, cluster, pxClient, err := h.resolveVM(c, clusterID, vmID)
	if err != nil {
		return err
	}

	upid, err := pxClient.MigrateVM(c.Context(), node.Name, int(vm.Vmid), proxmox.MigrateParams{
		Target: target,
		Online: p.Bool("online"),
	})
	if err != nil {
		return mapProxmoxError(err)
	}

	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    cluster.ID,
		Node:         node.Name,
		ResourceType: "vm",
		ResourceID:   vm.ID.String(),
		ResourceName: vm.Name,
		Action:       "migrate",
		UPID:         upid,
		TaskType:     "qmigrate",
		Description:  "Migrate VM " + strconv.Itoa(int(vm.Vmid)) + " → " + target,
		Extra:        map[string]any{"vmid": vm.Vmid, "target": target},
	})
	h.eventPub.ClusterEvent(c.Context(), cluster.ID.String(), events.KindMigrationUpdate, "vm", vm.ID.String(), "migrate")

	return c.JSON(vmActionResponse{
		UPID:   upid,
		Status: "dispatched",
	})
}

// ListNodeUSBDevices handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/hardware/usb.
func (h *VMHandler) ListNodeUSBDevices(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeName := p.String("node_name")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	devices, err := pxClient.ListNodeUSBDevices(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, devices)
}

// ListNodePCIDevices handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/hardware/pci.
func (h *VMHandler) ListNodePCIDevices(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	nodeName := p.String("node_name")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	devices, err := pxClient.ListNodePCIDevices(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, devices)
}

// SetVMPool handles PUT /api/v1/clusters/:cluster_id/vms/:vm_id/pool.
// Moves a VM/CT into a pool (or removes from current pool if pool is empty).
func (h *VMHandler) SetVMPool(c fiber.Ctx, p *apischema.Params) error {
	clusterID, vmID, err := guestIDs(p)
	if err != nil {
		return err
	}
	// An EMPTY pool is the meaningful value here — it removes the guest
	// from whatever pool it is in — so the parameter carries no format;
	// every registered format rejects the empty string.
	newPool := strings.TrimSpace(p.String("pool"))

	// In this cluster, specifically. The VMID read off this row is sent to
	// the PATH cluster's Proxmox below, so a guest resolved from another
	// cluster would have its number applied to whatever guest happens to
	// carry it here.
	vm, err := h.guestInCluster(c, clusterID, vmID)
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	vmidStr := strconv.Itoa(int(vm.Vmid))
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
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "vm", vmID.String(), "set_pool", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindVMStateChange, "vm", vmID.String(), "set_pool")

	return c.JSON(fiber.Map{"pool": newPool})
}
