package proxmox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ClientConfig holds the configuration for creating a new Proxmox API client.
type ClientConfig struct {
	BaseURL        string
	TokenID        string
	TokenSecret    string
	TLSFingerprint string // SHA-256 fingerprint; empty = use system CA pool.
	Timeout        time.Duration
}

// Client communicates with a single Proxmox VE host.
type Client struct {
	*apiClient
}

// NewClient creates a Client from the given config.
func NewClient(cfg ClientConfig) (*Client, error) {
	ac, err := newAPIClient(cfg, "PVEAPIToken")
	if err != nil {
		return nil, err
	}
	return &Client{apiClient: ac}, nil
}

// validateVMID rejects non-positive VM IDs.
func validateVMID(vmid int) error {
	if vmid <= 0 {
		return fmt.Errorf("invalid VMID: %d", vmid)
	}
	return nil
}

// vmStatusAction sends a POST to /nodes/{node}/qemu/{vmid}/status/{action} and returns the UPID.
func (c *Client) vmStatusAction(ctx context.Context, node string, vmid int, action string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/status/" + action
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("%s VM %d on %s: %w", action, vmid, node, err)
	}
	return upid, nil
}

// StartVM starts a QEMU VM and returns the task UPID.
func (c *Client) StartVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "start")
}

// StopVM forcefully stops a QEMU VM and returns the task UPID.
func (c *Client) StopVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "stop")
}

// ShutdownVM sends an ACPI shutdown to a QEMU VM and returns the task UPID.
func (c *Client) ShutdownVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "shutdown")
}

// RebootVM sends an ACPI reboot to a QEMU VM and returns the task UPID.
func (c *Client) RebootVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "reboot")
}

// ResetVM forcefully resets a QEMU VM and returns the task UPID.
func (c *Client) ResetVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "reset")
}

// SuspendVM suspends a QEMU VM and returns the task UPID.
func (c *Client) SuspendVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "suspend")
}

// ResumeVM resumes a suspended QEMU VM and returns the task UPID.
func (c *Client) ResumeVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "resume")
}

// CloneVM clones a QEMU VM and returns the task UPID.
func (c *Client) CloneVM(ctx context.Context, node string, vmid int, params CloneParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.NewID <= 0 {
		return "", fmt.Errorf("clone requires a positive newid")
	}

	form := url.Values{}
	form.Set("newid", strconv.Itoa(params.NewID))
	if params.Name != "" {
		form.Set("name", params.Name)
	}
	if params.Target != "" {
		form.Set("target", params.Target)
	}
	if params.Full {
		form.Set("full", "1")
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/clone"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("clone VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// DestroyVM deletes a QEMU VM and returns the task UPID.
func (c *Client) DestroyVM(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("destroy VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// ctStatusAction sends a POST to /nodes/{node}/lxc/{vmid}/status/{action} and returns the UPID.
func (c *Client) ctStatusAction(ctx context.Context, node string, vmid int, action string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/status/" + action
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("%s CT %d on %s: %w", action, vmid, node, err)
	}
	return upid, nil
}

// StartCT starts an LXC container and returns the task UPID.
func (c *Client) StartCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "start")
}

// StopCT forcefully stops an LXC container and returns the task UPID.
func (c *Client) StopCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "stop")
}

// ShutdownCT sends a shutdown signal to an LXC container and returns the task UPID.
func (c *Client) ShutdownCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "shutdown")
}

// RebootCT reboots an LXC container and returns the task UPID.
func (c *Client) RebootCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "reboot")
}

// SuspendCT suspends (freezes) an LXC container and returns the task UPID.
func (c *Client) SuspendCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "suspend")
}

// ResumeCT resumes a suspended LXC container and returns the task UPID.
func (c *Client) ResumeCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "resume")
}

// CloneCT clones an LXC container and returns the task UPID.
func (c *Client) CloneCT(ctx context.Context, node string, vmid int, params CloneParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.NewID <= 0 {
		return "", fmt.Errorf("clone requires a positive newid")
	}

	form := url.Values{}
	form.Set("newid", strconv.Itoa(params.NewID))
	if params.Name != "" {
		form.Set("hostname", params.Name)
	}
	if params.Target != "" {
		form.Set("target", params.Target)
	}
	if params.Full {
		form.Set("full", "1")
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/clone"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("clone CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// DestroyCT deletes an LXC container and returns the task UPID.
func (c *Client) DestroyCT(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("destroy CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// MigrateCT migrates an LXC container to another node and returns the task UPID.
func (c *Client) MigrateCT(ctx context.Context, node string, vmid int, params MigrateParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.Target == "" {
		return "", fmt.Errorf("migrate requires a target node")
	}

	form := url.Values{}
	form.Set("target", params.Target)
	if params.Online {
		form.Set("restart", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("migrate CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// MigrateVM migrates a QEMU VM to another node and returns the task UPID.
func (c *Client) MigrateVM(ctx context.Context, node string, vmid int, params MigrateParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.Target == "" {
		return "", fmt.Errorf("migrate requires a target node")
	}

	form := url.Values{}
	form.Set("target", params.Target)
	if params.Online {
		form.Set("online", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("migrate VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// GetTaskStatus returns the status of an async task by its UPID.
func (c *Client) GetTaskStatus(ctx context.Context, node string, upid string) (*TaskStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if upid == "" {
		return nil, fmt.Errorf("UPID cannot be empty")
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/status"
	var status TaskStatus
	if err := c.do(ctx, path, &status); err != nil {
		return nil, fmt.Errorf("get task status on %s: %w", node, err)
	}
	return &status, nil
}

// GetTaskLog returns the log lines for an async task.
func (c *Client) GetTaskLog(ctx context.Context, node string, upid string, start int) ([]TaskLogEntry, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if upid == "" {
		return nil, fmt.Errorf("UPID cannot be empty")
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/log?start=" + strconv.Itoa(start) + "&limit=5000"
	var entries []TaskLogEntry
	if err := c.do(ctx, path, &entries); err != nil {
		return nil, fmt.Errorf("get task log on %s: %w", node, err)
	}
	return entries, nil
}

// validateNodeName rejects empty names and path traversal attempts.
func validateNodeName(node string) error {
	if node == "" {
		return fmt.Errorf("node name cannot be empty")
	}
	if strings.Contains(node, "/") || strings.Contains(node, "..") {
		return fmt.Errorf("invalid node name: %q", node)
	}
	return nil
}

// GetNodes returns all nodes in the cluster.
func (c *Client) GetNodes(ctx context.Context) ([]NodeListEntry, error) {
	var nodes []NodeListEntry
	if err := c.do(ctx, "/nodes", &nodes); err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}
	return nodes, nil
}

// GetNodeStatus returns the detailed status of a single node.
func (c *Client) GetNodeStatus(ctx context.Context, node string) (*NodeStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var status NodeStatus
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/status", &status); err != nil {
		return nil, fmt.Errorf("get node %s status: %w", node, err)
	}
	return &status, nil
}

// GetVMs returns all QEMU virtual machines on a node.
func (c *Client) GetVMs(ctx context.Context, node string) ([]VirtualMachine, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var vms []VirtualMachine
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/qemu?full=1", &vms); err != nil {
		return nil, fmt.Errorf("get VMs on %s: %w", node, err)
	}
	for i := range vms {
		vms[i].Node = node
	}
	return vms, nil
}

// GetContainers returns all LXC containers on a node.
func (c *Client) GetContainers(ctx context.Context, node string) ([]Container, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var cts []Container
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/lxc", &cts); err != nil {
		return nil, fmt.Errorf("get containers on %s: %w", node, err)
	}
	for i := range cts {
		cts[i].Node = node
	}
	return cts, nil
}

// GetClusterResources returns resources across the cluster, optionally filtered by type.
func (c *Client) GetClusterResources(ctx context.Context, resourceType string) ([]ClusterResource, error) {
	path := "/cluster/resources"
	if resourceType != "" {
		q := url.Values{}
		q.Set("type", resourceType)
		path += "?" + q.Encode()
	}
	var resources []ClusterResource
	if err := c.do(ctx, path, &resources); err != nil {
		return nil, fmt.Errorf("get cluster resources: %w", err)
	}
	return resources, nil
}

// GetStoragePools returns all storage pools on a node.
func (c *Client) GetStoragePools(ctx context.Context, node string) ([]StoragePool, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var pools []StoragePool
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/storage", &pools); err != nil {
		return nil, fmt.Errorf("get storage pools on %s: %w", node, err)
	}
	return pools, nil
}

// GetClusterStatus returns the cluster status including node membership.
func (c *Client) GetClusterStatus(ctx context.Context) ([]ClusterStatusEntry, error) {
	var entries []ClusterStatusEntry
	if err := c.do(ctx, "/cluster/status", &entries); err != nil {
		return nil, fmt.Errorf("get cluster status: %w", err)
	}
	return entries, nil
}

// GetHAResources returns all HA-managed resources from GET /cluster/ha/resources.
func (c *Client) GetHAResources(ctx context.Context) ([]HAResource, error) {
	var resources []HAResource
	if err := c.do(ctx, "/cluster/ha/resources", &resources); err != nil {
		return nil, fmt.Errorf("get HA resources: %w", err)
	}
	return resources, nil
}

// SetHAResourceState updates an HA resource's state via PUT /cluster/ha/resources/{sid}.
// Valid states: "started", "stopped", "enabled", "disabled", "ignored".
func (c *Client) SetHAResourceState(ctx context.Context, sid string, state string) error {
	path := "/cluster/ha/resources/" + url.PathEscape(sid)
	form := url.Values{}
	form.Set("state", state)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("set HA resource %s state to %s: %w", sid, state, err)
	}
	return nil
}

// GetHAGroups returns all HA groups from GET /cluster/ha/groups.
func (c *Client) GetHAGroups(ctx context.Context) ([]HAGroup, error) {
	var groups []HAGroup
	if err := c.do(ctx, "/cluster/ha/groups", &groups); err != nil {
		return nil, fmt.Errorf("get HA groups: %w", err)
	}
	return groups, nil
}

// GetHARules returns all HA rules from GET /cluster/ha/rules (PVE 9+).
func (c *Client) GetHARules(ctx context.Context) ([]HARuleEntry, error) {
	var rules []HARuleEntry
	if err := c.do(ctx, "/cluster/ha/rules", &rules); err != nil {
		return nil, fmt.Errorf("get HA rules: %w", err)
	}
	return rules, nil
}

// CreateHARule creates a new HA rule via POST /cluster/ha/rules.
// The ruleType ("node-affinity" or "resource-affinity") is sent as the "type" form parameter.
func (c *Client) CreateHARule(ctx context.Context, ruleType string, params CreateHARuleParams) error {
	form := url.Values{}
	form.Set("rule", params.Rule)
	form.Set("type", ruleType)
	form.Set("resources", params.Resources)
	if params.Nodes != "" {
		form.Set("nodes", params.Nodes)
	}
	if params.Strict != 0 {
		form.Set("strict", strconv.Itoa(params.Strict))
	}
	if params.Affinity != "" {
		form.Set("affinity", params.Affinity)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/cluster/ha/rules", form, nil); err != nil {
		return fmt.Errorf("create HA rule %q: %w", params.Rule, err)
	}
	return nil
}

// SetHARuleDisabled enables or disables an HA rule via PUT /cluster/ha/rules/{rule}.
// SetHARuleDisabled enables or disables an HA rule via PUT /cluster/ha/rules/{rule}.
// The ruleType ("node-affinity" or "resource-affinity") is required by the Proxmox API.
func (c *Client) SetHARuleDisabled(ctx context.Context, ruleID string, ruleType string, disabled bool) error {
	path := "/cluster/ha/rules/" + url.PathEscape(ruleID)
	form := url.Values{}
	form.Set("type", ruleType)
	if disabled {
		form.Set("disable", "1")
	} else {
		form.Set("disable", "0")
	}
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("set HA rule %s disabled=%v: %w", ruleID, disabled, err)
	}
	return nil
}

// DeleteHARule deletes an HA rule via DELETE /cluster/ha/rules/{ruleID}.
func (c *Client) DeleteHARule(ctx context.Context, ruleID string) error {
	path := "/cluster/ha/rules/" + url.PathEscape(ruleID)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete HA rule %q: %w", ruleID, err)
	}
	return nil
}

// GetMachineTypes returns the available QEMU machine types for a node.
func (c *Client) GetMachineTypes(ctx context.Context, node string) ([]MachineType, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var types []MachineType
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/capabilities/qemu/machines", &types); err != nil {
		return nil, fmt.Errorf("get machine types on %s: %w", node, err)
	}
	return types, nil
}

// GetCPUModels returns the available CPU models for a node.
func (c *Client) GetCPUModels(ctx context.Context, node string) ([]CPUModel, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var models []CPUModel
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/capabilities/qemu/cpu", &models); err != nil {
		return nil, fmt.Errorf("get CPU models on %s: %w", node, err)
	}
	return models, nil
}

// GetResourcePools returns the list of resource pools from the Proxmox cluster.
func (c *Client) GetResourcePools(ctx context.Context) ([]ResourcePool, error) {
	var pools []ResourcePool
	if err := c.do(ctx, "/pools", &pools); err != nil {
		return nil, fmt.Errorf("get resource pools: %w", err)
	}
	return pools, nil
}

// GetStorageContent returns the contents of a storage pool on a node.
func (c *Client) GetStorageContent(ctx context.Context, node, storage string) ([]StorageContent, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content"
	var items []StorageContent
	if err := c.do(ctx, path, &items); err != nil {
		return nil, fmt.Errorf("get storage content on %s/%s: %w", node, storage, err)
	}
	return items, nil
}

// UploadToStorage uploads a file (ISO or container template) to a storage pool and returns the task UPID.
func (c *Client) UploadToStorage(ctx context.Context, node, storage, contentType, filename string, reader io.Reader, fileSize int64) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/upload"
	fields := map[string]string{
		"content": contentType,
	}
	var upid string
	if err := c.doMultipart(ctx, path, fields, "filename", filename, reader, fileSize, &upid); err != nil {
		return "", fmt.Errorf("upload to %s/%s: %w", node, storage, err)
	}
	return upid, nil
}

// DeleteStorageContent deletes a volume from a storage pool and returns the task UPID.
func (c *Client) DeleteStorageContent(ctx context.Context, node, storage, volume string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	// Volume IDs contain ":" (storage:path) — PathEscape would over-encode it.
	// Proxmox expects the volume as-is in the URL path.
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content/" + volume
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete volume %s on %s/%s: %w", volume, node, storage, err)
	}
	return upid, nil
}

// --- Cluster-Level Storage Management ---

// GetStorageConfig returns the configuration of a storage pool from GET /storage/{storage}.
func (c *Client) GetStorageConfig(ctx context.Context, storage string) (*StorageConfig, error) {
	if storage == "" {
		return nil, fmt.Errorf("storage name is required")
	}
	var cfg StorageConfig
	if err := c.do(ctx, "/storage/"+url.PathEscape(storage), &cfg); err != nil {
		return nil, fmt.Errorf("get storage config %s: %w", storage, err)
	}
	return &cfg, nil
}

// ListStorageConfigs returns all storage definitions from GET /storage.
func (c *Client) ListStorageConfigs(ctx context.Context) ([]StorageConfig, error) {
	var cfgs []StorageConfig
	if err := c.do(ctx, "/storage", &cfgs); err != nil {
		return nil, fmt.Errorf("list storage configs: %w", err)
	}
	return cfgs, nil
}

// CreateStorage creates a new storage pool via POST /storage.
// The params map contains all type-specific and common parameters.
func (c *Client) CreateStorage(ctx context.Context, params url.Values) error {
	if params.Get("storage") == "" {
		return fmt.Errorf("storage name is required")
	}
	if params.Get("type") == "" {
		return fmt.Errorf("storage type is required")
	}
	if err := c.doPost(ctx, "/storage", params, nil); err != nil {
		return fmt.Errorf("create storage %s: %w", params.Get("storage"), err)
	}
	return nil
}

// UpdateStorage updates a storage pool via PUT /storage/{storage}.
func (c *Client) UpdateStorage(ctx context.Context, storage string, params url.Values) error {
	if storage == "" {
		return fmt.Errorf("storage name is required")
	}
	if err := c.doPut(ctx, "/storage/"+url.PathEscape(storage), params, nil); err != nil {
		return fmt.Errorf("update storage %s: %w", storage, err)
	}
	return nil
}

// DeleteStorage deletes a storage pool via DELETE /storage/{storage}.
func (c *Client) DeleteStorage(ctx context.Context, storage string) error {
	if storage == "" {
		return fmt.Errorf("storage name is required")
	}
	if err := c.doDelete(ctx, "/storage/"+url.PathEscape(storage), nil); err != nil {
		return fmt.Errorf("delete storage %s: %w", storage, err)
	}
	return nil
}

// --- Ceph API Methods ---

// GetCephStatus returns the cluster-wide Ceph status from any node.
func (c *Client) GetCephStatus(ctx context.Context, node string) (*CephStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var status CephStatus
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/status", &status); err != nil {
		return nil, fmt.Errorf("get ceph status on %s: %w", node, err)
	}
	return &status, nil
}

// GetCephOSDs returns the OSD tree from a node.
func (c *Client) GetCephOSDs(ctx context.Context, node string) (*CephOSDResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var resp CephOSDResponse
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/osd", &resp); err != nil {
		return nil, fmt.Errorf("get ceph osds on %s: %w", node, err)
	}
	return &resp, nil
}

// GetCephPools returns all Ceph pools visible from a node.
// Tries /ceph/pool first (PVE 8.x), falls back to /ceph/pools (PVE 7.x).
func (c *Client) GetCephPools(ctx context.Context, node string) ([]CephPool, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var pools []CephPool
	nodePath := "/nodes/" + url.PathEscape(node)
	if err := c.do(ctx, nodePath+"/ceph/pool", &pools); err != nil {
		// Fall back to plural form.
		var pools2 []CephPool
		if err2 := c.do(ctx, nodePath+"/ceph/pools", &pools2); err2 != nil {
			return nil, fmt.Errorf("get ceph pools on %s: %w", node, err)
		}
		return pools2, nil
	}
	return pools, nil
}

// GetCephMonitors returns all Ceph monitors from a node.
// The Proxmox API returns monitor entries with varying fields; we parse
// flexibly and extract what we need.
func (c *Client) GetCephMonitors(ctx context.Context, node string) ([]CephMon, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	// Parse as raw JSON first since Proxmox versions differ in response shape.
	var raw []map[string]interface{}
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/mon", &raw); err != nil {
		return nil, fmt.Errorf("get ceph monitors on %s: %w", node, err)
	}

	mons := make([]CephMon, 0, len(raw))
	for _, entry := range raw {
		mon := CephMon{
			Name: stringVal(entry, "name"),
			Host: stringVal(entry, "host"),
			Addr: stringVal(entry, "addr"),
		}
		if r, ok := entry["rank"]; ok {
			switch v := r.(type) {
			case float64:
				mon.Rank = FlexInt(int(v))
			}
		}
		mons = append(mons, mon)
	}
	return mons, nil
}

// stringVal safely extracts a string from a map entry.
func stringVal(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// GetCephFS returns CephFS filesystems from a node.
func (c *Client) GetCephFS(ctx context.Context, node string) ([]CephFS, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var fs []CephFS
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/fs", &fs); err != nil {
		return nil, fmt.Errorf("get ceph fs on %s: %w", node, err)
	}
	return fs, nil
}

// GetCephCrushRules returns CRUSH rules from a node.
func (c *Client) GetCephCrushRules(ctx context.Context, node string) ([]CephCrushRule, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var rules []CephCrushRule
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/rules", &rules); err != nil {
		return nil, fmt.Errorf("get ceph crush rules on %s: %w", node, err)
	}
	return rules, nil
}

// CreateCephPool creates a new Ceph pool on a node.
func (c *Client) CreateCephPool(ctx context.Context, node string, params CephPoolCreateParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if params.Name == "" {
		return fmt.Errorf("pool name is required")
	}
	form := url.Values{}
	form.Set("name", params.Name)
	form.Set("size", strconv.Itoa(params.Size))
	form.Set("pg_num", strconv.Itoa(params.PGNum))
	if params.MinSize > 0 {
		form.Set("min_size", strconv.Itoa(params.MinSize))
	}
	if params.Application != "" {
		form.Set("application", params.Application)
	}
	if params.CrushRule != "" {
		form.Set("crush_rule_name", params.CrushRule)
	}
	if params.PGAutoScale != "" {
		form.Set("pg_autoscale_mode", params.PGAutoScale)
	}
	path := "/nodes/" + url.PathEscape(node) + "/ceph/pools"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create ceph pool %s on %s: %w", params.Name, node, err)
	}
	return nil
}

// DeleteCephPool deletes a Ceph pool on a node.
func (c *Client) DeleteCephPool(ctx context.Context, node, poolName string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if poolName == "" {
		return fmt.Errorf("pool name is required")
	}
	path := "/nodes/" + url.PathEscape(node) + "/ceph/pools/" + url.PathEscape(poolName)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete ceph pool %s on %s: %w", poolName, node, err)
	}
	return nil
}

// ResizeDisk resizes a VM disk.
func (c *Client) ResizeDisk(ctx context.Context, node string, vmid int, params DiskResizeParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("disk", params.Disk)
	form.Set("size", params.Size)
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/resize"
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("resize disk on VM %d: %w", vmid, err)
	}
	return nil
}

// MoveDisk moves a VM disk to another storage and returns the task UPID.
func (c *Client) MoveDisk(ctx context.Context, node string, vmid int, params DiskMoveParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("disk", params.Disk)
	form.Set("storage", params.Storage)
	if params.Delete {
		form.Set("delete", "1")
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/move_disk"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("move disk on VM %d: %w", vmid, err)
	}
	return upid, nil
}

// MoveCTVolume moves a container volume to another storage and returns the task UPID.
func (c *Client) MoveCTVolume(ctx context.Context, node string, vmid int, params CTVolumeMoveParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("volume", params.Volume)
	form.Set("storage", params.Storage)
	if params.Delete {
		form.Set("delete", "1")
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/move_volume"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("move volume on CT %d: %w", vmid, err)
	}
	return upid, nil
}

// AttachDisk attaches a new disk to a VM by setting the appropriate config key.
func (c *Client) AttachDisk(ctx context.Context, node string, vmid int, params DiskAttachParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	if params.Bus == "" || params.Storage == "" || params.Size == "" {
		return fmt.Errorf("bus, storage, and size are required")
	}

	// Build the volume spec: "storage:size[,format=fmt]"
	volume := params.Storage + ":" + params.Size
	if params.Format != "" {
		volume += ",format=" + params.Format
	}

	diskKey := params.Bus + strconv.Itoa(params.Index)
	fields := map[string]string{
		diskKey: volume,
	}

	return c.SetVMConfig(ctx, node, vmid, fields)
}

// DetachDisk detaches (removes) a disk from a VM config.
func (c *Client) DetachDisk(ctx context.Context, node string, vmid int, disk string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	if disk == "" {
		return fmt.Errorf("disk name is required")
	}

	fields := map[string]string{
		"delete": disk,
	}

	return c.SetVMConfig(ctx, node, vmid, fields)
}

// --- PBS Restore Methods (calls PVE API to restore from PBS) ---

// RestoreVM restores a QEMU VM from a PBS backup and returns the task UPID.
func (c *Client) RestoreVM(ctx context.Context, node string, params RestoreParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("restore requires a positive VMID")
	}
	if params.Archive == "" {
		return "", fmt.Errorf("restore requires an archive path")
	}

	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	form.Set("archive", params.Archive)
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Unique {
		form.Set("unique", "1")
	}
	if params.Force {
		form.Set("force", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("restore VM %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}

// RestoreCT restores an LXC container from a PBS backup and returns the task UPID.
func (c *Client) RestoreCT(ctx context.Context, node string, params RestoreParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("restore requires a positive VMID")
	}
	if params.Archive == "" {
		return "", fmt.Errorf("restore requires an archive path")
	}

	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	form.Set("ostemplate", params.Archive)
	form.Set("restore", "1")
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Unique {
		form.Set("unique", "1")
	}
	if params.Force {
		form.Set("force", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("restore CT %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}

// --- Snapshot Methods ---

// ListVMSnapshots returns all snapshots for a QEMU VM.
func (c *Client) ListVMSnapshots(ctx context.Context, node string, vmid int) ([]Snapshot, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot"
	var snaps []Snapshot
	if err := c.do(ctx, path, &snaps); err != nil {
		return nil, fmt.Errorf("list VM %d snapshots on %s: %w", vmid, node, err)
	}
	return snaps, nil
}

// CreateVMSnapshot creates a snapshot for a QEMU VM and returns the task UPID.
func (c *Client) CreateVMSnapshot(ctx context.Context, node string, vmid int, params SnapshotParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.SnapName == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	form := url.Values{}
	form.Set("snapname", params.SnapName)
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if params.VMState {
		form.Set("vmstate", "1")
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create snapshot on VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// DeleteVMSnapshot deletes a snapshot from a QEMU VM and returns the task UPID.
func (c *Client) DeleteVMSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if snapname == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete snapshot %s on VM %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}

// RollbackVMSnapshot rolls back a QEMU VM to a snapshot and returns the task UPID.
func (c *Client) RollbackVMSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if snapname == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname) + "/rollback"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("rollback snapshot %s on VM %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}

// ListCTSnapshots returns all snapshots for an LXC container.
func (c *Client) ListCTSnapshots(ctx context.Context, node string, vmid int) ([]Snapshot, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot"
	var snaps []Snapshot
	if err := c.do(ctx, path, &snaps); err != nil {
		return nil, fmt.Errorf("list CT %d snapshots on %s: %w", vmid, node, err)
	}
	return snaps, nil
}

// CreateCTSnapshot creates a snapshot for an LXC container and returns the task UPID.
func (c *Client) CreateCTSnapshot(ctx context.Context, node string, vmid int, params SnapshotParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.SnapName == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	form := url.Values{}
	form.Set("snapname", params.SnapName)
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create snapshot on CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// DeleteCTSnapshot deletes a snapshot from an LXC container and returns the task UPID.
func (c *Client) DeleteCTSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if snapname == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete snapshot %s on CT %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}

// RollbackCTSnapshot rolls back an LXC container to a snapshot and returns the task UPID.
func (c *Client) RollbackCTSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if snapname == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname) + "/rollback"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("rollback snapshot %s on CT %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}

// --- Create VM/CT Methods ---

// CreateVM creates a new QEMU VM and returns the task UPID.
func (c *Client) CreateVM(ctx context.Context, node string, params CreateVMParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("VMID is required and must be positive")
	}
	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	if params.Name != "" {
		form.Set("name", params.Name)
	}
	if params.Memory > 0 {
		form.Set("memory", strconv.Itoa(params.Memory))
	}
	if params.Cores > 0 {
		form.Set("cores", strconv.Itoa(params.Cores))
	}
	if params.Sockets > 0 {
		form.Set("sockets", strconv.Itoa(params.Sockets))
	}
	if params.SCSI0 != "" {
		form.Set("scsi0", params.SCSI0)
	}
	if params.IDE2 != "" {
		form.Set("ide2", params.IDE2)
	}
	if params.Net0 != "" {
		form.Set("net0", params.Net0)
	}
	if params.OSType != "" {
		form.Set("ostype", params.OSType)
	}
	if params.Boot != "" {
		form.Set("boot", params.Boot)
	}
	if params.CDRom != "" {
		form.Set("cdrom", params.CDRom)
	}
	if params.Start {
		form.Set("start", "1")
	}
	if params.CIUser != "" {
		form.Set("ciuser", params.CIUser)
	}
	if params.CIPassword != "" {
		form.Set("cipassword", params.CIPassword)
	}
	if params.IPConfig0 != "" {
		form.Set("ipconfig0", params.IPConfig0)
	}
	if params.SSHKeys != "" {
		form.Set("sshkeys", url.QueryEscape(params.SSHKeys))
	}
	if params.CIType != "" {
		form.Set("citype", params.CIType)
	}
	if params.Nameserver != "" {
		form.Set("nameserver", params.Nameserver)
	}
	if params.Searchdomain != "" {
		form.Set("searchdomain", params.Searchdomain)
	}
	// System
	if params.BIOS != "" {
		form.Set("bios", params.BIOS)
	}
	if params.Machine != "" {
		form.Set("machine", params.Machine)
	}
	if params.ScsiHW != "" {
		form.Set("scsihw", params.ScsiHW)
	} else {
		form.Set("scsihw", "virtio-scsi-pci")
	}
	if params.EFIDisk0 != "" {
		form.Set("efidisk0", params.EFIDisk0)
	}
	if params.TPMState0 != "" {
		form.Set("tpmstate0", params.TPMState0)
	}
	if params.Agent != "" {
		form.Set("agent", params.Agent)
	}
	// CPU
	if params.CPUType != "" {
		form.Set("cpu", params.CPUType)
	}
	if params.Numa != nil {
		if *params.Numa {
			form.Set("numa", "1")
		} else {
			form.Set("numa", "0")
		}
	}
	// Memory
	if params.Balloon != nil {
		form.Set("balloon", strconv.Itoa(*params.Balloon))
	}
	// Display
	if params.VGA != "" {
		form.Set("vga", params.VGA)
	}
	// Boot / Options
	if params.OnBoot != nil {
		if *params.OnBoot {
			form.Set("onboot", "1")
		} else {
			form.Set("onboot", "0")
		}
	}
	if params.Hotplug != "" {
		form.Set("hotplug", params.Hotplug)
	}
	if params.Tablet != nil {
		if *params.Tablet {
			form.Set("tablet", "1")
		} else {
			form.Set("tablet", "0")
		}
	}
	// Description / Tags / Pool
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if params.Tags != "" {
		form.Set("tags", params.Tags)
	}
	if params.Pool != "" {
		form.Set("pool", params.Pool)
	}
	// Forward any extra fields (additional disks, CD-ROMs, etc.)
	for k, v := range params.Extra {
		if v != "" {
			form.Set(k, v)
		}
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create VM %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}

// CreateCT creates a new LXC container and returns the task UPID.
func (c *Client) CreateCT(ctx context.Context, node string, params CreateCTParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("VMID is required and must be positive")
	}
	if params.OSTemplate == "" {
		return "", fmt.Errorf("ostemplate is required")
	}
	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	form.Set("ostemplate", params.OSTemplate)
	if params.Hostname != "" {
		form.Set("hostname", params.Hostname)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.RootFS != "" {
		form.Set("rootfs", params.RootFS)
	}
	if params.Memory > 0 {
		form.Set("memory", strconv.Itoa(params.Memory))
	}
	if params.Swap > 0 {
		form.Set("swap", strconv.Itoa(params.Swap))
	}
	if params.Cores > 0 {
		form.Set("cores", strconv.Itoa(params.Cores))
	}
	if params.Net0 != "" {
		form.Set("net0", params.Net0)
	}
	if params.Password != "" {
		form.Set("password", params.Password)
	}
	if params.SSHKeys != "" {
		form.Set("ssh-public-keys", url.QueryEscape(params.SSHKeys))
	}
	if params.Unprivileged {
		form.Set("unprivileged", "1")
	}
	if params.Start {
		form.Set("start", "1")
	}
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if params.Tags != "" {
		form.Set("tags", params.Tags)
	}
	if params.Pool != "" {
		form.Set("pool", params.Pool)
	}
	if params.Nameserver != "" {
		form.Set("nameserver", params.Nameserver)
	}
	if params.Searchdomain != "" {
		form.Set("searchdomain", params.Searchdomain)
	}
	for k, v := range params.Extra {
		if v != "" {
			form.Set(k, v)
		}
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create CT %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}

// --- Remote Migration Methods ---

// RemoteMigrateVM migrates a VM to a remote cluster and returns the task UPID.
func (c *Client) RemoteMigrateVM(ctx context.Context, node string, vmid int, params RemoteMigrateVMParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("target-endpoint", params.TargetEndpoint.String())
	form.Set("target-bridge", params.TargetBridge)
	if params.TargetStorage != "" {
		form.Set("target-storage", params.TargetStorage)
	}
	if params.TargetVMID > 0 {
		form.Set("target-vmid", strconv.Itoa(params.TargetVMID))
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}
	if params.Online {
		form.Set("online", "1")
	}
	if params.Delete {
		form.Set("delete", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/remote_migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("remote migrate VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// RemoteMigrateCT migrates a container to a remote cluster and returns the task UPID.
func (c *Client) RemoteMigrateCT(ctx context.Context, node string, vmid int, params RemoteMigrateCTParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("target-endpoint", params.TargetEndpoint.String())
	form.Set("target-bridge", params.TargetBridge)
	if params.TargetStorage != "" {
		form.Set("target-storage", params.TargetStorage)
	}
	if params.TargetVMID > 0 {
		form.Set("target-vmid", strconv.Itoa(params.TargetVMID))
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}
	if params.Restart {
		form.Set("restart", "1")
	}
	if params.Delete {
		form.Set("delete", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/remote_migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("remote migrate CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// ListNodeUSBDevices returns USB devices on the given node.
func (c *Client) ListNodeUSBDevices(ctx context.Context, node string) ([]NodeUSBDevice, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var devices []NodeUSBDevice
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/hardware/usb", &devices); err != nil {
		return nil, fmt.Errorf("list USB devices on %s: %w", node, err)
	}
	return devices, nil
}

// ListNodePCIDevices returns PCI devices on the given node.
func (c *Client) ListNodePCIDevices(ctx context.Context, node string) ([]NodePCIDevice, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var devices []NodePCIDevice
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/hardware/pci", &devices); err != nil {
		return nil, fmt.Errorf("list PCI devices on %s: %w", node, err)
	}
	return devices, nil
}

// GetNetworkBridges returns only bridge-type network interfaces on a node.
func (c *Client) GetNetworkBridges(ctx context.Context, node string) ([]NetworkInterface, error) {
	ifaces, err := c.GetNetworkInterfaces(ctx, node)
	if err != nil {
		return nil, err
	}
	bridges := make([]NetworkInterface, 0)
	for _, iface := range ifaces {
		if iface.Type == "bridge" {
			bridges = append(bridges, iface)
		}
	}
	return bridges, nil
}

// --- Network Methods ---

// GetNetworkInterfaces returns all network interfaces on a node.
func (c *Client) GetNetworkInterfaces(ctx context.Context, node string) ([]NetworkInterface, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/network"
	var ifaces []NetworkInterface
	if err := c.do(ctx, path, &ifaces); err != nil {
		return nil, fmt.Errorf("get network interfaces on %s: %w", node, err)
	}
	return ifaces, nil
}

// --- Firewall Methods ---

// GetClusterFirewallRules returns firewall rules at the cluster level.
func (c *Client) GetClusterFirewallRules(ctx context.Context) ([]FirewallRule, error) {
	var rules []FirewallRule
	if err := c.do(ctx, "/cluster/firewall/rules", &rules); err != nil {
		return nil, fmt.Errorf("get cluster firewall rules: %w", err)
	}
	return rules, nil
}

// CreateClusterFirewallRule creates a new cluster-level firewall rule.
func (c *Client) CreateClusterFirewallRule(ctx context.Context, rule FirewallRuleParams) error {
	form := firewallRuleToForm(rule)
	if err := c.doPost(ctx, "/cluster/firewall/rules", form, nil); err != nil {
		return fmt.Errorf("create cluster firewall rule: %w", err)
	}
	return nil
}

// UpdateClusterFirewallRule updates a cluster-level firewall rule by position.
func (c *Client) UpdateClusterFirewallRule(ctx context.Context, pos int, rule FirewallRuleParams) error {
	form := firewallRuleToForm(rule)
	path := "/cluster/firewall/rules/" + strconv.Itoa(pos)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update cluster firewall rule %d: %w", pos, err)
	}
	return nil
}

// DeleteClusterFirewallRule deletes a cluster-level firewall rule by position.
func (c *Client) DeleteClusterFirewallRule(ctx context.Context, pos int) error {
	path := "/cluster/firewall/rules/" + strconv.Itoa(pos)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete cluster firewall rule %d: %w", pos, err)
	}
	return nil
}

// GetNodeFirewallRules returns firewall rules for a specific node.
func (c *Client) GetNodeFirewallRules(ctx context.Context, node string) ([]FirewallRule, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/firewall/rules"
	var rules []FirewallRule
	if err := c.do(ctx, path, &rules); err != nil {
		return nil, fmt.Errorf("get firewall rules on %s: %w", node, err)
	}
	return rules, nil
}

// GetVMFirewallRules returns firewall rules for a specific VM.
func (c *Client) GetVMFirewallRules(ctx context.Context, node string, vmid int) ([]FirewallRule, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/firewall/rules"
	var rules []FirewallRule
	if err := c.do(ctx, path, &rules); err != nil {
		return nil, fmt.Errorf("get firewall rules for VM %d on %s: %w", vmid, node, err)
	}
	return rules, nil
}

// CreateVMFirewallRule creates a firewall rule for a specific VM.
func (c *Client) CreateVMFirewallRule(ctx context.Context, node string, vmid int, rule FirewallRuleParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := firewallRuleToForm(rule)
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/firewall/rules"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create firewall rule for VM %d on %s: %w", vmid, node, err)
	}
	return nil
}

// UpdateVMFirewallRule updates a firewall rule for a specific VM by position.
func (c *Client) UpdateVMFirewallRule(ctx context.Context, node string, vmid int, pos int, rule FirewallRuleParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := firewallRuleToForm(rule)
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/firewall/rules/" + strconv.Itoa(pos)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update firewall rule %d for VM %d on %s: %w", pos, vmid, node, err)
	}
	return nil
}

// DeleteVMFirewallRule deletes a firewall rule for a specific VM by position.
func (c *Client) DeleteVMFirewallRule(ctx context.Context, node string, vmid int, pos int) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/firewall/rules/" + strconv.Itoa(pos)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete firewall rule %d for VM %d on %s: %w", pos, vmid, node, err)
	}
	return nil
}

// GetClusterFirewallOptions returns the cluster-level firewall options.
func (c *Client) GetClusterFirewallOptions(ctx context.Context) (*FirewallOptions, error) {
	var opts FirewallOptions
	if err := c.do(ctx, "/cluster/firewall/options", &opts); err != nil {
		return nil, fmt.Errorf("get cluster firewall options: %w", err)
	}
	return &opts, nil
}

// SetClusterFirewallOptions sets cluster-level firewall options.
func (c *Client) SetClusterFirewallOptions(ctx context.Context, opts FirewallOptions) error {
	form := firewallOptionsToForm(opts)
	if err := c.doPut(ctx, "/cluster/firewall/options", form, nil); err != nil {
		return fmt.Errorf("set cluster firewall options: %w", err)
	}
	return nil
}

// --- SDN Methods ---

// GetSDNZones returns all SDN zones.
func (c *Client) GetSDNZones(ctx context.Context) ([]SDNZone, error) {
	var zones []SDNZone
	if err := c.do(ctx, "/cluster/sdn/zones", &zones); err != nil {
		return nil, fmt.Errorf("get SDN zones: %w", err)
	}
	return zones, nil
}

// GetSDNVNets returns all SDN VNets.
func (c *Client) GetSDNVNets(ctx context.Context) ([]SDNVNet, error) {
	var vnets []SDNVNet
	if err := c.do(ctx, "/cluster/sdn/vnets", &vnets); err != nil {
		return nil, fmt.Errorf("get SDN vnets: %w", err)
	}
	return vnets, nil
}

// CreateSDNZone creates a new SDN zone.
func (c *Client) CreateSDNZone(ctx context.Context, params CreateSDNZoneParams) error {
	form := sdnZoneCreateToForm(params)
	if err := c.doPost(ctx, "/cluster/sdn/zones", form, nil); err != nil {
		return fmt.Errorf("create SDN zone %s: %w", params.Zone, err)
	}
	return nil
}

// UpdateSDNZone updates an existing SDN zone.
func (c *Client) UpdateSDNZone(ctx context.Context, zone string, params UpdateSDNZoneParams) error {
	form := sdnZoneUpdateToForm(params)
	path := "/cluster/sdn/zones/" + url.PathEscape(zone)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN zone %s: %w", zone, err)
	}
	return nil
}

// DeleteSDNZone deletes an SDN zone.
func (c *Client) DeleteSDNZone(ctx context.Context, zone string) error {
	path := "/cluster/sdn/zones/" + url.PathEscape(zone)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN zone %s: %w", zone, err)
	}
	return nil
}

// CreateSDNVNet creates a new SDN VNet.
func (c *Client) CreateSDNVNet(ctx context.Context, params CreateSDNVNetParams) error {
	form := sdnVNetCreateToForm(params)
	if err := c.doPost(ctx, "/cluster/sdn/vnets", form, nil); err != nil {
		return fmt.Errorf("create SDN vnet %s: %w", params.VNet, err)
	}
	return nil
}

// UpdateSDNVNet updates an existing SDN VNet.
func (c *Client) UpdateSDNVNet(ctx context.Context, vnet string, params UpdateSDNVNetParams) error {
	form := sdnVNetUpdateToForm(params)
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN vnet %s: %w", vnet, err)
	}
	return nil
}

// DeleteSDNVNet deletes an SDN VNet.
func (c *Client) DeleteSDNVNet(ctx context.Context, vnet string) error {
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN vnet %s: %w", vnet, err)
	}
	return nil
}

// GetSDNSubnets returns all subnets for a VNet.
func (c *Client) GetSDNSubnets(ctx context.Context, vnet string) ([]SDNSubnet, error) {
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet) + "/subnets"
	var subnets []SDNSubnet
	if err := c.do(ctx, path, &subnets); err != nil {
		return nil, fmt.Errorf("get SDN subnets for %s: %w", vnet, err)
	}
	return subnets, nil
}

// CreateSDNSubnet creates a new subnet under a VNet.
func (c *Client) CreateSDNSubnet(ctx context.Context, vnet string, params CreateSDNSubnetParams) error {
	form := sdnSubnetCreateToForm(params)
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet) + "/subnets"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create SDN subnet %s on %s: %w", params.Subnet, vnet, err)
	}
	return nil
}

// UpdateSDNSubnet updates an existing subnet under a VNet.
func (c *Client) UpdateSDNSubnet(ctx context.Context, vnet string, subnet string, params UpdateSDNSubnetParams) error {
	form := sdnSubnetUpdateToForm(params)
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet) + "/subnets/" + url.PathEscape(subnet)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN subnet %s on %s: %w", subnet, vnet, err)
	}
	return nil
}

// DeleteSDNSubnet deletes a subnet under a VNet.
func (c *Client) DeleteSDNSubnet(ctx context.Context, vnet string, subnet string) error {
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet) + "/subnets/" + url.PathEscape(subnet)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN subnet %s on %s: %w", subnet, vnet, err)
	}
	return nil
}

// ApplySDN applies pending SDN configuration changes.
func (c *Client) ApplySDN(ctx context.Context) error {
	if err := c.doPut(ctx, "/cluster/sdn", nil, nil); err != nil {
		return fmt.Errorf("apply SDN config: %w", err)
	}
	return nil
}

// sdnZoneCreateToForm converts SDN zone create params to url.Values.
func sdnZoneCreateToForm(p CreateSDNZoneParams) url.Values {
	form := url.Values{}
	form.Set("zone", p.Zone)
	form.Set("type", p.Type)
	if p.Bridge != "" {
		form.Set("bridge", p.Bridge)
	}
	if p.Peers != "" {
		form.Set("peers", p.Peers)
	}
	if p.Nodes != "" {
		form.Set("nodes", p.Nodes)
	}
	if p.IPAM != "" {
		form.Set("ipam", p.IPAM)
	}
	if p.DNS != "" {
		form.Set("dns", p.DNS)
	}
	if p.ReverseDNS != "" {
		form.Set("reversedns", p.ReverseDNS)
	}
	if p.DNSZone != "" {
		form.Set("dnszone", p.DNSZone)
	}
	if p.VLANProtocol != "" {
		form.Set("vlan-protocol", p.VLANProtocol)
	}
	if p.Controller != "" {
		form.Set("controller", p.Controller)
	}
	if p.ExitNodes != "" {
		form.Set("exitnodes", p.ExitNodes)
	}
	if p.Mac != "" {
		form.Set("mac", p.Mac)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.MTU != 0 {
		form.Set("mtu", strconv.Itoa(p.MTU))
	}
	if p.VRFVxlan != 0 {
		form.Set("vrf-vxlan", strconv.Itoa(p.VRFVxlan))
	}
	if p.AdvSubnets != 0 {
		form.Set("advertise-subnets", strconv.Itoa(p.AdvSubnets))
	}
	if p.DisableArp != 0 {
		form.Set("disable-arp-nd-suppression", strconv.Itoa(p.DisableArp))
	}
	return form
}

// sdnZoneUpdateToForm converts SDN zone update params to url.Values.
func sdnZoneUpdateToForm(p UpdateSDNZoneParams) url.Values {
	form := url.Values{}
	if p.Bridge != "" {
		form.Set("bridge", p.Bridge)
	}
	if p.Peers != "" {
		form.Set("peers", p.Peers)
	}
	if p.Nodes != "" {
		form.Set("nodes", p.Nodes)
	}
	if p.IPAM != "" {
		form.Set("ipam", p.IPAM)
	}
	if p.DNS != "" {
		form.Set("dns", p.DNS)
	}
	if p.ReverseDNS != "" {
		form.Set("reversedns", p.ReverseDNS)
	}
	if p.DNSZone != "" {
		form.Set("dnszone", p.DNSZone)
	}
	if p.VLANProtocol != "" {
		form.Set("vlan-protocol", p.VLANProtocol)
	}
	if p.Controller != "" {
		form.Set("controller", p.Controller)
	}
	if p.ExitNodes != "" {
		form.Set("exitnodes", p.ExitNodes)
	}
	if p.Mac != "" {
		form.Set("mac", p.Mac)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.MTU != 0 {
		form.Set("mtu", strconv.Itoa(p.MTU))
	}
	if p.VRFVxlan != 0 {
		form.Set("vrf-vxlan", strconv.Itoa(p.VRFVxlan))
	}
	if p.AdvSubnets != 0 {
		form.Set("advertise-subnets", strconv.Itoa(p.AdvSubnets))
	}
	if p.DisableArp != 0 {
		form.Set("disable-arp-nd-suppression", strconv.Itoa(p.DisableArp))
	}
	return form
}

// sdnVNetCreateToForm converts SDN VNet create params to url.Values.
func sdnVNetCreateToForm(p CreateSDNVNetParams) url.Values {
	form := url.Values{}
	form.Set("vnet", p.VNet)
	form.Set("zone", p.Zone)
	if p.Alias != "" {
		form.Set("alias", p.Alias)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.VLANAware != 0 {
		form.Set("vlanaware", strconv.Itoa(p.VLANAware))
	}
	if p.Isolate != 0 {
		form.Set("isolate", strconv.Itoa(p.Isolate))
	}
	return form
}

// sdnVNetUpdateToForm converts SDN VNet update params to url.Values.
func sdnVNetUpdateToForm(p UpdateSDNVNetParams) url.Values {
	form := url.Values{}
	if p.Zone != "" {
		form.Set("zone", p.Zone)
	}
	if p.Alias != "" {
		form.Set("alias", p.Alias)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.VLANAware != 0 {
		form.Set("vlanaware", strconv.Itoa(p.VLANAware))
	}
	if p.Isolate != 0 {
		form.Set("isolate", strconv.Itoa(p.Isolate))
	}
	return form
}

// sdnSubnetCreateToForm converts SDN subnet create params to url.Values.
func sdnSubnetCreateToForm(p CreateSDNSubnetParams) url.Values {
	form := url.Values{}
	form.Set("subnet", p.Subnet)
	if p.Gateway != "" {
		form.Set("gateway", p.Gateway)
	}
	if p.Type != "" {
		form.Set("type", p.Type)
	}
	if p.SNAT != 0 {
		form.Set("snat", strconv.Itoa(p.SNAT))
	}
	if p.DHCPRange != "" {
		form.Set("dhcp-range", p.DHCPRange)
	}
	if p.DHCPDNSServer != "" {
		form.Set("dhcp-dns-server", p.DHCPDNSServer)
	}
	return form
}

// sdnSubnetUpdateToForm converts SDN subnet update params to url.Values.
func sdnSubnetUpdateToForm(p UpdateSDNSubnetParams) url.Values {
	form := url.Values{}
	if p.Gateway != "" {
		form.Set("gateway", p.Gateway)
	}
	if p.SNAT != 0 {
		form.Set("snat", strconv.Itoa(p.SNAT))
	}
	if p.DHCPRange != "" {
		form.Set("dhcp-range", p.DHCPRange)
	}
	if p.DHCPDNSServer != "" {
		form.Set("dhcp-dns-server", p.DHCPDNSServer)
	}
	return form
}

// --- SDN Controller Methods ---

// GetSDNControllers returns all SDN controllers.
func (c *Client) GetSDNControllers(ctx context.Context) ([]SDNController, error) {
	var controllers []SDNController
	if err := c.do(ctx, "/cluster/sdn/controllers", &controllers); err != nil {
		return nil, fmt.Errorf("get SDN controllers: %w", err)
	}
	return controllers, nil
}

// CreateSDNController creates a new SDN controller.
func (c *Client) CreateSDNController(ctx context.Context, params CreateSDNControllerParams) error {
	form := url.Values{}
	form.Set("controller", params.Controller)
	form.Set("type", params.Type)
	if params.ASN != 0 {
		form.Set("asn", strconv.Itoa(params.ASN))
	}
	if params.Peers != "" {
		form.Set("peers", params.Peers)
	}
	if params.Nodes != "" {
		form.Set("nodes", params.Nodes)
	}
	if params.ISISDomain != "" {
		form.Set("isis-domain", params.ISISDomain)
	}
	if params.ISISIfaces != "" {
		form.Set("isis-ifaces", params.ISISIfaces)
	}
	if params.ISISNET != "" {
		form.Set("isis-net", params.ISISNET)
	}
	if params.EBGPMultihop != 0 {
		form.Set("ebgp-multihop", strconv.Itoa(params.EBGPMultihop))
	}
	if params.Loopback != "" {
		form.Set("loopback", params.Loopback)
	}
	if params.Node != "" {
		form.Set("node", params.Node)
	}
	if err := c.doPost(ctx, "/cluster/sdn/controllers", form, nil); err != nil {
		return fmt.Errorf("create SDN controller %s: %w", params.Controller, err)
	}
	return nil
}

// UpdateSDNController updates an existing SDN controller.
func (c *Client) UpdateSDNController(ctx context.Context, controller string, params UpdateSDNControllerParams) error {
	form := url.Values{}
	if params.ASN != 0 {
		form.Set("asn", strconv.Itoa(params.ASN))
	}
	if params.Peers != "" {
		form.Set("peers", params.Peers)
	}
	if params.Nodes != "" {
		form.Set("nodes", params.Nodes)
	}
	if params.ISISDomain != "" {
		form.Set("isis-domain", params.ISISDomain)
	}
	if params.ISISIfaces != "" {
		form.Set("isis-ifaces", params.ISISIfaces)
	}
	if params.ISISNET != "" {
		form.Set("isis-net", params.ISISNET)
	}
	if params.EBGPMultihop != 0 {
		form.Set("ebgp-multihop", strconv.Itoa(params.EBGPMultihop))
	}
	if params.Loopback != "" {
		form.Set("loopback", params.Loopback)
	}
	if params.Node != "" {
		form.Set("node", params.Node)
	}
	path := "/cluster/sdn/controllers/" + url.PathEscape(controller)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN controller %s: %w", controller, err)
	}
	return nil
}

// DeleteSDNController deletes an SDN controller.
func (c *Client) DeleteSDNController(ctx context.Context, controller string) error {
	path := "/cluster/sdn/controllers/" + url.PathEscape(controller)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN controller %s: %w", controller, err)
	}
	return nil
}

// --- SDN IPAM Methods ---

// GetSDNIPAMs returns all SDN IPAM plugins.
func (c *Client) GetSDNIPAMs(ctx context.Context) ([]SDNIPAM, error) {
	var ipams []SDNIPAM
	if err := c.do(ctx, "/cluster/sdn/ipams", &ipams); err != nil {
		return nil, fmt.Errorf("get SDN IPAMs: %w", err)
	}
	return ipams, nil
}

// CreateSDNIPAM creates a new IPAM plugin.
func (c *Client) CreateSDNIPAM(ctx context.Context, params CreateSDNIPAMParams) error {
	form := url.Values{}
	form.Set("ipam", params.IPAM)
	form.Set("type", params.Type)
	if params.URL != "" {
		form.Set("url", params.URL)
	}
	if params.Token != "" {
		form.Set("token", params.Token)
	}
	if params.SectionID != 0 {
		form.Set("section", strconv.Itoa(params.SectionID))
	}
	if err := c.doPost(ctx, "/cluster/sdn/ipams", form, nil); err != nil {
		return fmt.Errorf("create SDN IPAM %s: %w", params.IPAM, err)
	}
	return nil
}

// UpdateSDNIPAM updates an IPAM plugin.
func (c *Client) UpdateSDNIPAM(ctx context.Context, ipam string, params UpdateSDNIPAMParams) error {
	form := url.Values{}
	if params.URL != "" {
		form.Set("url", params.URL)
	}
	if params.Token != "" {
		form.Set("token", params.Token)
	}
	if params.SectionID != 0 {
		form.Set("section", strconv.Itoa(params.SectionID))
	}
	path := "/cluster/sdn/ipams/" + url.PathEscape(ipam)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN IPAM %s: %w", ipam, err)
	}
	return nil
}

// DeleteSDNIPAM deletes an IPAM plugin.
func (c *Client) DeleteSDNIPAM(ctx context.Context, ipam string) error {
	path := "/cluster/sdn/ipams/" + url.PathEscape(ipam)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN IPAM %s: %w", ipam, err)
	}
	return nil
}

// --- SDN DNS Methods ---

// GetSDNDNSPlugins returns all SDN DNS plugins.
func (c *Client) GetSDNDNSPlugins(ctx context.Context) ([]SDNDNS, error) {
	var plugins []SDNDNS
	if err := c.do(ctx, "/cluster/sdn/dns", &plugins); err != nil {
		return nil, fmt.Errorf("get SDN DNS plugins: %w", err)
	}
	return plugins, nil
}

// CreateSDNDNS creates a new DNS plugin.
func (c *Client) CreateSDNDNS(ctx context.Context, params CreateSDNDNSParams) error {
	form := url.Values{}
	form.Set("dns", params.DNS)
	form.Set("type", params.Type)
	if params.URL != "" {
		form.Set("url", params.URL)
	}
	if params.Key != "" {
		form.Set("key", params.Key)
	}
	if err := c.doPost(ctx, "/cluster/sdn/dns", form, nil); err != nil {
		return fmt.Errorf("create SDN DNS %s: %w", params.DNS, err)
	}
	return nil
}

// UpdateSDNDNS updates a DNS plugin.
func (c *Client) UpdateSDNDNS(ctx context.Context, dns string, params UpdateSDNDNSParams) error {
	form := url.Values{}
	if params.URL != "" {
		form.Set("url", params.URL)
	}
	if params.Key != "" {
		form.Set("key", params.Key)
	}
	path := "/cluster/sdn/dns/" + url.PathEscape(dns)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN DNS %s: %w", dns, err)
	}
	return nil
}

// DeleteSDNDNS deletes a DNS plugin.
func (c *Client) DeleteSDNDNS(ctx context.Context, dns string) error {
	path := "/cluster/sdn/dns/" + url.PathEscape(dns)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN DNS %s: %w", dns, err)
	}
	return nil
}

// --- Network Interface CRUD Methods ---

// CreateNetworkInterface creates a new network interface on a node.
func (c *Client) CreateNetworkInterface(ctx context.Context, node string, params CreateNetworkInterfaceParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("iface", params.Iface)
	form.Set("type", params.Type)
	if params.Address != "" {
		form.Set("address", params.Address)
	}
	if params.Netmask != "" {
		form.Set("netmask", params.Netmask)
	}
	if params.Gateway != "" {
		form.Set("gateway", params.Gateway)
	}
	if params.CIDR != "" {
		form.Set("cidr", params.CIDR)
	}
	if params.BridgePorts != "" {
		form.Set("bridge_ports", params.BridgePorts)
	}
	if params.BridgeSTP != "" {
		form.Set("bridge_stp", params.BridgeSTP)
	}
	if params.BridgeFD != "" {
		form.Set("bridge_fd", params.BridgeFD)
	}
	if params.Comments != "" {
		form.Set("comments", params.Comments)
	}
	if params.Method != "" {
		form.Set("method", params.Method)
	}
	if params.Method6 != "" {
		form.Set("method6", params.Method6)
	}
	form.Set("autostart", strconv.Itoa(params.Autostart))
	path := "/nodes/" + url.PathEscape(node) + "/network"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create network interface %s on %s: %w", params.Iface, node, err)
	}
	return nil
}

// UpdateNetworkInterface updates a network interface on a node.
func (c *Client) UpdateNetworkInterface(ctx context.Context, node string, iface string, params UpdateNetworkInterfaceParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("type", params.Type)
	if params.Address != "" {
		form.Set("address", params.Address)
	}
	if params.Netmask != "" {
		form.Set("netmask", params.Netmask)
	}
	if params.Gateway != "" {
		form.Set("gateway", params.Gateway)
	}
	if params.CIDR != "" {
		form.Set("cidr", params.CIDR)
	}
	if params.BridgePorts != "" {
		form.Set("bridge_ports", params.BridgePorts)
	}
	if params.BridgeSTP != "" {
		form.Set("bridge_stp", params.BridgeSTP)
	}
	if params.BridgeFD != "" {
		form.Set("bridge_fd", params.BridgeFD)
	}
	if params.Comments != "" {
		form.Set("comments", params.Comments)
	}
	if params.Method != "" {
		form.Set("method", params.Method)
	}
	if params.Method6 != "" {
		form.Set("method6", params.Method6)
	}
	form.Set("autostart", strconv.Itoa(params.Autostart))
	path := "/nodes/" + url.PathEscape(node) + "/network/" + url.PathEscape(iface)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update network interface %s on %s: %w", iface, node, err)
	}
	return nil
}

// DeleteNetworkInterface deletes a network interface on a node.
func (c *Client) DeleteNetworkInterface(ctx context.Context, node string, iface string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	path := "/nodes/" + url.PathEscape(node) + "/network/" + url.PathEscape(iface)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete network interface %s on %s: %w", iface, node, err)
	}
	return nil
}

// ApplyNetworkConfig applies pending network configuration changes on a node.
func (c *Client) ApplyNetworkConfig(ctx context.Context, node string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	path := "/nodes/" + url.PathEscape(node) + "/network"
	if err := c.doPut(ctx, path, nil, nil); err != nil {
		return fmt.Errorf("apply network config on %s: %w", node, err)
	}
	return nil
}

// RevertNetworkConfig reverts pending network configuration changes on a node.
func (c *Client) RevertNetworkConfig(ctx context.Context, node string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	path := "/nodes/" + url.PathEscape(node) + "/network"
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("revert network config on %s: %w", node, err)
	}
	return nil
}

// firewallRuleToForm converts a FirewallRuleParams to url.Values for the Proxmox API.
func firewallRuleToForm(rule FirewallRuleParams) url.Values {
	form := url.Values{}
	if rule.Type != "" {
		form.Set("type", rule.Type)
	}
	if rule.Action != "" {
		form.Set("action", rule.Action)
	}
	if rule.Source != "" {
		form.Set("source", rule.Source)
	}
	if rule.Dest != "" {
		form.Set("dest", rule.Dest)
	}
	if rule.Sport != "" {
		form.Set("sport", rule.Sport)
	}
	if rule.Dport != "" {
		form.Set("dport", rule.Dport)
	}
	if rule.Proto != "" {
		form.Set("proto", rule.Proto)
	}
	form.Set("enable", strconv.Itoa(rule.Enable))
	if rule.Comment != "" {
		form.Set("comment", rule.Comment)
	}
	if rule.Macro != "" {
		form.Set("macro", rule.Macro)
	}
	if rule.Log != "" {
		form.Set("log", rule.Log)
	}
	if rule.Iface != "" {
		form.Set("iface", rule.Iface)
	}
	return form
}

// firewallOptionsToForm converts FirewallOptions to url.Values.
func firewallOptionsToForm(opts FirewallOptions) url.Values {
	form := url.Values{}
	if opts.Enable != nil {
		form.Set("enable", strconv.Itoa(*opts.Enable))
	}
	if opts.PolicyIn != "" {
		form.Set("policy_in", opts.PolicyIn)
	}
	if opts.PolicyOut != "" {
		form.Set("policy_out", opts.PolicyOut)
	}
	if opts.LogLevelIn != "" {
		form.Set("log_level_in", opts.LogLevelIn)
	}
	if opts.LogLevelOut != "" {
		form.Set("log_level_out", opts.LogLevelOut)
	}
	return form
}

// --- VM Config Methods (Cloud-Init) ---

// GetVMConfig returns the full configuration of a QEMU VM.
func (c *Client) GetVMConfig(ctx context.Context, node string, vmid int) (VMConfig, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/config"
	var config VMConfig
	if err := c.do(ctx, path, &config); err != nil {
		return nil, fmt.Errorf("get VM %d config on %s: %w", vmid, node, err)
	}
	return config, nil
}

// GetGuestAgentOSInfo returns OS information from the QEMU guest agent.
// Returns nil, nil when the agent is not running.
// Proxmox wraps agent responses: {"data": {"result": {...}}}.
func (c *Client) GetGuestAgentOSInfo(ctx context.Context, node string, vmid int) (*GuestOSInfo, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/agent/get-osinfo"
	var wrapper struct {
		Result GuestOSInfo `json:"result"`
	}
	if err := c.do(ctx, path, &wrapper); err != nil {
		if isAgentNotRunning(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get guest agent OS info for VM %d on %s: %w", vmid, node, err)
	}
	return &wrapper.Result, nil
}

// GetGuestAgentNetworkInterfaces returns network interfaces from the QEMU guest agent.
// Returns nil, nil when the agent is not running.
// Proxmox wraps agent responses: {"data": {"result": [...]}}.
func (c *Client) GetGuestAgentNetworkInterfaces(ctx context.Context, node string, vmid int) ([]GuestNetworkInterface, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/agent/network-get-interfaces"
	var wrapper struct {
		Result []GuestNetworkInterface `json:"result"`
	}
	if err := c.do(ctx, path, &wrapper); err != nil {
		if isAgentNotRunning(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get guest agent network interfaces for VM %d on %s: %w", vmid, node, err)
	}
	return wrapper.Result, nil
}

// isAgentNotRunning checks if the error indicates the QEMU guest agent is not running.
func isAgentNotRunning(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 500 {
		return strings.Contains(apiErr.Message, "QEMU guest agent is not running") ||
			strings.Contains(apiErr.Message, "guest agent") ||
			strings.Contains(apiErr.Message, "not running")
	}
	return false
}

// GetCTConfig returns the full configuration of a container.
func (c *Client) GetCTConfig(ctx context.Context, node string, vmid int) (VMConfig, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/config"
	var config VMConfig
	if err := c.do(ctx, path, &config); err != nil {
		return nil, fmt.Errorf("get CT %d config on %s: %w", vmid, node, err)
	}
	return config, nil
}

// SetVMConfig updates configuration fields on a QEMU VM.
func (c *Client) SetVMConfig(ctx context.Context, node string, vmid int, fields map[string]string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/config"
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("set VM %d config on %s: %w", vmid, node, err)
	}
	return nil
}

// UpdateVMConfigSync applies configuration changes immediately via POST (hotplug).
// Unlike SetVMConfig (PUT), changes take effect without a reboot where supported.
func (c *Client) UpdateVMConfigSync(ctx context.Context, node string, vmid int, fields map[string]string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/config"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update VM %d config on %s: %w", vmid, node, err)
	}
	return nil
}

// SetContainerConfig updates configuration fields on an LXC container.
func (c *Client) SetContainerConfig(ctx context.Context, node string, vmid int, fields map[string]string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/config"
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("set container %d config on %s: %w", vmid, node, err)
	}
	return nil
}

// --- Backup Job Methods ---

// TriggerBackup starts a vzdump backup on a node and returns the task UPID.
func (c *Client) TriggerBackup(ctx context.Context, node string, params BackupParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID == "" {
		return "", fmt.Errorf("vzdump requires vmid")
	}
	form := url.Values{}
	form.Set("vmid", params.VMID)
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Mode != "" {
		form.Set("mode", params.Mode)
	}
	if params.Compress != "" {
		form.Set("compress", params.Compress)
	}
	path := "/nodes/" + url.PathEscape(node) + "/vzdump"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("trigger backup on %s: %w", node, err)
	}
	return upid, nil
}

// ListBackupJobs returns all cluster-level vzdump backup job schedules.
func (c *Client) ListBackupJobs(ctx context.Context) ([]BackupJob, error) {
	var jobs []BackupJob
	if err := c.do(ctx, "/cluster/backup", &jobs); err != nil {
		return nil, fmt.Errorf("list backup jobs: %w", err)
	}
	return jobs, nil
}

// GetBackupJob returns a single backup job by ID.
func (c *Client) GetBackupJob(ctx context.Context, id string) (*BackupJob, error) {
	if id == "" {
		return nil, fmt.Errorf("backup job ID is required")
	}
	var job BackupJob
	if err := c.do(ctx, "/cluster/backup/"+url.PathEscape(id), &job); err != nil {
		return nil, fmt.Errorf("get backup job %s: %w", id, err)
	}
	return &job, nil
}

// CreateBackupJob creates a new vzdump backup job schedule.
func (c *Client) CreateBackupJob(ctx context.Context, params BackupJobParams) error {
	form := url.Values{}
	if params.Schedule != "" {
		form.Set("schedule", params.Schedule)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Node != "" {
		form.Set("node", params.Node)
	}
	if params.VMID != "" {
		form.Set("vmid", params.VMID)
	}
	if params.Mode != "" {
		form.Set("mode", params.Mode)
	}
	if params.Compress != "" {
		form.Set("compress", params.Compress)
	}
	if params.Enabled != nil {
		form.Set("enabled", strconv.Itoa(*params.Enabled))
	}
	if params.MailNotification != "" {
		form.Set("mailnotification", params.MailNotification)
	}
	if params.MailTo != "" {
		form.Set("mailto", params.MailTo)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Type != "" {
		form.Set("type", params.Type)
	}
	if err := c.doPost(ctx, "/cluster/backup", form, nil); err != nil {
		return fmt.Errorf("create backup job: %w", err)
	}
	return nil
}

// UpdateBackupJob updates an existing vzdump backup job schedule.
func (c *Client) UpdateBackupJob(ctx context.Context, id string, params BackupJobParams) error {
	if id == "" {
		return fmt.Errorf("backup job ID is required")
	}
	form := url.Values{}
	if params.Schedule != "" {
		form.Set("schedule", params.Schedule)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Node != "" {
		form.Set("node", params.Node)
	}
	if params.VMID != "" {
		form.Set("vmid", params.VMID)
	}
	if params.Mode != "" {
		form.Set("mode", params.Mode)
	}
	if params.Compress != "" {
		form.Set("compress", params.Compress)
	}
	if params.Enabled != nil {
		form.Set("enabled", strconv.Itoa(*params.Enabled))
	}
	if params.MailNotification != "" {
		form.Set("mailnotification", params.MailNotification)
	}
	if params.MailTo != "" {
		form.Set("mailto", params.MailTo)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Type != "" {
		form.Set("type", params.Type)
	}
	if err := c.doPut(ctx, "/cluster/backup/"+url.PathEscape(id), form, nil); err != nil {
		return fmt.Errorf("update backup job %s: %w", id, err)
	}
	return nil
}

// RunBackupJob triggers an immediate run of a vzdump backup job schedule.
func (c *Client) RunBackupJob(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("backup job ID is required")
	}
	var upid string
	if err := c.doPost(ctx, "/cluster/backup/"+url.PathEscape(id)+"/run", nil, &upid); err != nil {
		return "", fmt.Errorf("run backup job %s: %w", id, err)
	}
	return upid, nil
}

// GetNodeAptUpdates returns pending package updates for a node from GET /nodes/{node}/apt/update.
func (c *Client) GetNodeAptUpdates(ctx context.Context, node string) ([]AptUpdate, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var updates []AptUpdate
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/apt/update", &updates); err != nil {
		return nil, fmt.Errorf("get apt updates on %s: %w", node, err)
	}
	return updates, nil
}

// RefreshNodeAptIndex triggers an apt-get update on a node via POST /nodes/{node}/apt/update.
// Returns the UPID of the background task.
func (c *Client) RefreshNodeAptIndex(ctx context.Context, node string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	var upid string
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/apt/update", nil, &upid); err != nil {
		return "", fmt.Errorf("refresh apt index on %s: %w", node, err)
	}
	return upid, nil
}

// RebootNode reboots a node via POST /nodes/{node}/status with command=reboot.
func (c *Client) RebootNode(ctx context.Context, node string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("command", "reboot")
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/status", form, nil); err != nil {
		return fmt.Errorf("reboot node %s: %w", node, err)
	}
	return nil
}

// DeleteBackupJob deletes a vzdump backup job schedule.
func (c *Client) DeleteBackupJob(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("backup job ID is required")
	}
	if err := c.doDelete(ctx, "/cluster/backup/"+url.PathEscape(id), nil); err != nil {
		return fmt.Errorf("delete backup job %s: %w", id, err)
	}
	return nil
}

// --- Phase 9: Datacenter Feature Parity ---

// GetClusterOptions returns datacenter.cfg options via GET /cluster/options.
func (c *Client) GetClusterOptions(ctx context.Context) (*ClusterOptions, error) {
	var opts ClusterOptions
	if err := c.do(ctx, "/cluster/options", &opts); err != nil {
		return nil, fmt.Errorf("get cluster options: %w", err)
	}
	return &opts, nil
}

// SetClusterOptions updates datacenter.cfg options via PUT /cluster/options.
func (c *Client) SetClusterOptions(ctx context.Context, params UpdateClusterOptionsParams) error {
	form := url.Values{}
	if params.Console != nil {
		form.Set("console", *params.Console)
	}
	if params.Keyboard != nil {
		form.Set("keyboard", *params.Keyboard)
	}
	if params.Language != nil {
		form.Set("language", *params.Language)
	}
	if params.EmailFrom != nil {
		form.Set("email_from", *params.EmailFrom)
	}
	if params.HTTPProxy != nil {
		form.Set("http_proxy", *params.HTTPProxy)
	}
	if params.MacPrefix != nil {
		form.Set("mac_prefix", *params.MacPrefix)
	}
	if params.Migration != nil {
		form.Set("migration", *params.Migration)
	}
	if params.MigrationType != nil {
		form.Set("migration_type", *params.MigrationType)
	}
	if params.BWLimit != nil {
		form.Set("bwlimit", *params.BWLimit)
	}
	if params.NextID != nil {
		form.Set("next-id", *params.NextID)
	}
	if params.HA != nil {
		form.Set("ha", *params.HA)
	}
	if params.Fencing != nil {
		form.Set("fencing", *params.Fencing)
	}
	if params.CRS != nil {
		form.Set("crs", *params.CRS)
	}
	if params.MaxWorkers != nil {
		form.Set("max_workers", strconv.Itoa(*params.MaxWorkers))
	}
	if params.Description != nil {
		form.Set("description", *params.Description)
	}
	if params.RegisteredTags != nil {
		form.Set("registered-tags", *params.RegisteredTags)
	}
	if params.UserTagAccess != nil {
		form.Set("user-tag-access", *params.UserTagAccess)
	}
	if params.TagStyle != nil {
		form.Set("tag-style", *params.TagStyle)
	}
	if params.Delete != "" {
		form.Set("delete", params.Delete)
	}
	if err := c.doPut(ctx, "/cluster/options", form, nil); err != nil {
		return fmt.Errorf("set cluster options: %w", err)
	}
	return nil
}

// CreateHAResource creates a new HA resource via POST /cluster/ha/resources.
func (c *Client) CreateHAResource(ctx context.Context, params CreateHAResourceParams) error {
	form := url.Values{}
	form.Set("sid", params.SID)
	if params.State != "" {
		form.Set("state", params.State)
	}
	if params.Group != "" {
		form.Set("group", params.Group)
	}
	if params.MaxRestart > 0 {
		form.Set("max_restart", strconv.Itoa(params.MaxRestart))
	}
	if params.MaxRelocate > 0 {
		form.Set("max_relocate", strconv.Itoa(params.MaxRelocate))
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/cluster/ha/resources", form, nil); err != nil {
		return fmt.Errorf("create HA resource %s: %w", params.SID, err)
	}
	return nil
}

// GetHAResource returns a single HA resource via GET /cluster/ha/resources/{sid}.
func (c *Client) GetHAResource(ctx context.Context, sid string) (*HAResource, error) {
	path := "/cluster/ha/resources/" + url.PathEscape(sid)
	var res HAResource
	if err := c.do(ctx, path, &res); err != nil {
		return nil, fmt.Errorf("get HA resource %s: %w", sid, err)
	}
	return &res, nil
}

// UpdateHAResource updates an HA resource via PUT /cluster/ha/resources/{sid}.
func (c *Client) UpdateHAResource(ctx context.Context, sid string, params UpdateHAResourceParams) error {
	form := url.Values{}
	if params.State != nil {
		form.Set("state", *params.State)
	}
	if params.Group != nil {
		form.Set("group", *params.Group)
	}
	if params.MaxRestart != nil {
		form.Set("max_restart", strconv.Itoa(*params.MaxRestart))
	}
	if params.MaxRelocate != nil {
		form.Set("max_relocate", strconv.Itoa(*params.MaxRelocate))
	}
	if params.Comment != nil {
		form.Set("comment", *params.Comment)
	}
	if params.Digest != "" {
		form.Set("digest", params.Digest)
	}
	path := "/cluster/ha/resources/" + url.PathEscape(sid)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update HA resource %s: %w", sid, err)
	}
	return nil
}

// DeleteHAResource deletes an HA resource via DELETE /cluster/ha/resources/{sid}.
func (c *Client) DeleteHAResource(ctx context.Context, sid string) error {
	path := "/cluster/ha/resources/" + url.PathEscape(sid)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete HA resource %s: %w", sid, err)
	}
	return nil
}

// CreateHAGroup creates a new HA group via POST /cluster/ha/groups.
func (c *Client) CreateHAGroup(ctx context.Context, params CreateHAGroupParams) error {
	form := url.Values{}
	form.Set("group", params.Group)
	form.Set("nodes", params.Nodes)
	if params.Restricted != 0 {
		form.Set("restricted", strconv.Itoa(params.Restricted))
	}
	if params.NoFailback != 0 {
		form.Set("nofailback", strconv.Itoa(params.NoFailback))
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/cluster/ha/groups", form, nil); err != nil {
		return fmt.Errorf("create HA group %s: %w", params.Group, err)
	}
	return nil
}

// GetHAGroup returns a single HA group via GET /cluster/ha/groups/{group}.
func (c *Client) GetHAGroup(ctx context.Context, group string) (*HAGroup, error) {
	path := "/cluster/ha/groups/" + url.PathEscape(group)
	var g HAGroup
	if err := c.do(ctx, path, &g); err != nil {
		return nil, fmt.Errorf("get HA group %s: %w", group, err)
	}
	return &g, nil
}

// UpdateHAGroup updates an HA group via PUT /cluster/ha/groups/{group}.
func (c *Client) UpdateHAGroup(ctx context.Context, group string, params UpdateHAGroupParams) error {
	form := url.Values{}
	if params.Nodes != nil {
		form.Set("nodes", *params.Nodes)
	}
	if params.Restricted != nil {
		form.Set("restricted", strconv.Itoa(*params.Restricted))
	}
	if params.NoFailback != nil {
		form.Set("nofailback", strconv.Itoa(*params.NoFailback))
	}
	if params.Comment != nil {
		form.Set("comment", *params.Comment)
	}
	if params.Digest != "" {
		form.Set("digest", params.Digest)
	}
	path := "/cluster/ha/groups/" + url.PathEscape(group)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update HA group %s: %w", group, err)
	}
	return nil
}

// DeleteHAGroup deletes an HA group via DELETE /cluster/ha/groups/{group}.
func (c *Client) DeleteHAGroup(ctx context.Context, group string) error {
	path := "/cluster/ha/groups/" + url.PathEscape(group)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete HA group %s: %w", group, err)
	}
	return nil
}

// GetHAStatus returns the current HA status via GET /cluster/ha/status/current.
func (c *Client) GetHAStatus(ctx context.Context) ([]HAStatusEntry, error) {
	var entries []HAStatusEntry
	if err := c.do(ctx, "/cluster/ha/status/current", &entries); err != nil {
		return nil, fmt.Errorf("get HA status: %w", err)
	}
	return entries, nil
}

// GetFirewallAliases returns all firewall aliases via GET /cluster/firewall/aliases.
func (c *Client) GetFirewallAliases(ctx context.Context) ([]FirewallAlias, error) {
	var aliases []FirewallAlias
	if err := c.do(ctx, "/cluster/firewall/aliases", &aliases); err != nil {
		return nil, fmt.Errorf("get firewall aliases: %w", err)
	}
	return aliases, nil
}

// CreateFirewallAlias creates a firewall alias via POST /cluster/firewall/aliases.
func (c *Client) CreateFirewallAlias(ctx context.Context, params FirewallAliasParams) error {
	form := url.Values{}
	form.Set("name", params.Name)
	form.Set("cidr", params.CIDR)
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/cluster/firewall/aliases", form, nil); err != nil {
		return fmt.Errorf("create firewall alias %s: %w", params.Name, err)
	}
	return nil
}

// GetFirewallAlias returns a single firewall alias via GET /cluster/firewall/aliases/{name}.
func (c *Client) GetFirewallAlias(ctx context.Context, name string) (*FirewallAlias, error) {
	path := "/cluster/firewall/aliases/" + url.PathEscape(name)
	var alias FirewallAlias
	if err := c.do(ctx, path, &alias); err != nil {
		return nil, fmt.Errorf("get firewall alias %s: %w", name, err)
	}
	return &alias, nil
}

// UpdateFirewallAlias updates a firewall alias via PUT /cluster/firewall/aliases/{name}.
func (c *Client) UpdateFirewallAlias(ctx context.Context, name string, params FirewallAliasParams) error {
	form := url.Values{}
	form.Set("cidr", params.CIDR)
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Rename != "" {
		form.Set("rename", params.Rename)
	}
	path := "/cluster/firewall/aliases/" + url.PathEscape(name)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update firewall alias %s: %w", name, err)
	}
	return nil
}

// DeleteFirewallAlias deletes a firewall alias via DELETE /cluster/firewall/aliases/{name}.
func (c *Client) DeleteFirewallAlias(ctx context.Context, name string) error {
	path := "/cluster/firewall/aliases/" + url.PathEscape(name)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete firewall alias %s: %w", name, err)
	}
	return nil
}

// GetFirewallIPSets returns all firewall IP sets via GET /cluster/firewall/ipset.
func (c *Client) GetFirewallIPSets(ctx context.Context) ([]FirewallIPSet, error) {
	var sets []FirewallIPSet
	if err := c.do(ctx, "/cluster/firewall/ipset", &sets); err != nil {
		return nil, fmt.Errorf("get firewall IP sets: %w", err)
	}
	return sets, nil
}

// CreateFirewallIPSet creates a firewall IP set via POST /cluster/firewall/ipset.
func (c *Client) CreateFirewallIPSet(ctx context.Context, name, comment string) error {
	form := url.Values{}
	form.Set("name", name)
	if comment != "" {
		form.Set("comment", comment)
	}
	if err := c.doPost(ctx, "/cluster/firewall/ipset", form, nil); err != nil {
		return fmt.Errorf("create firewall IP set %s: %w", name, err)
	}
	return nil
}

// DeleteFirewallIPSet deletes a firewall IP set via DELETE /cluster/firewall/ipset/{name}.
func (c *Client) DeleteFirewallIPSet(ctx context.Context, name string) error {
	path := "/cluster/firewall/ipset/" + url.PathEscape(name)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete firewall IP set %s: %w", name, err)
	}
	return nil
}

// GetFirewallIPSetEntries returns entries in an IP set via GET /cluster/firewall/ipset/{name}.
func (c *Client) GetFirewallIPSetEntries(ctx context.Context, name string) ([]FirewallIPSetEntry, error) {
	path := "/cluster/firewall/ipset/" + url.PathEscape(name)
	var entries []FirewallIPSetEntry
	if err := c.do(ctx, path, &entries); err != nil {
		return nil, fmt.Errorf("get firewall IP set %s entries: %w", name, err)
	}
	return entries, nil
}

// AddFirewallIPSetEntry adds an entry to an IP set via POST /cluster/firewall/ipset/{name}.
func (c *Client) AddFirewallIPSetEntry(ctx context.Context, setName string, params FirewallIPSetEntryParams) error {
	form := url.Values{}
	form.Set("cidr", params.CIDR)
	if params.NoMatch != nil {
		form.Set("nomatch", strconv.Itoa(*params.NoMatch))
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	path := "/cluster/firewall/ipset/" + url.PathEscape(setName)
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("add entry to IP set %s: %w", setName, err)
	}
	return nil
}

// UpdateFirewallIPSetEntry updates an entry in an IP set via PUT /cluster/firewall/ipset/{name}/{cidr}.
func (c *Client) UpdateFirewallIPSetEntry(ctx context.Context, setName, cidr string, params FirewallIPSetEntryParams) error {
	form := url.Values{}
	if params.NoMatch != nil {
		form.Set("nomatch", strconv.Itoa(*params.NoMatch))
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	path := "/cluster/firewall/ipset/" + url.PathEscape(setName) + "/" + url.PathEscape(cidr)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update entry %s in IP set %s: %w", cidr, setName, err)
	}
	return nil
}

// DeleteFirewallIPSetEntry deletes an entry from an IP set via DELETE /cluster/firewall/ipset/{name}/{cidr}.
func (c *Client) DeleteFirewallIPSetEntry(ctx context.Context, setName, cidr string) error {
	path := "/cluster/firewall/ipset/" + url.PathEscape(setName) + "/" + url.PathEscape(cidr)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete entry %s from IP set %s: %w", cidr, setName, err)
	}
	return nil
}

// GetFirewallSecurityGroups returns all security groups via GET /cluster/firewall/groups.
func (c *Client) GetFirewallSecurityGroups(ctx context.Context) ([]FirewallSecurityGroup, error) {
	var groups []FirewallSecurityGroup
	if err := c.do(ctx, "/cluster/firewall/groups", &groups); err != nil {
		return nil, fmt.Errorf("get firewall security groups: %w", err)
	}
	return groups, nil
}

// CreateFirewallSecurityGroup creates a security group via POST /cluster/firewall/groups.
func (c *Client) CreateFirewallSecurityGroup(ctx context.Context, params FirewallSecurityGroupParams) error {
	form := url.Values{}
	form.Set("group", params.Group)
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/cluster/firewall/groups", form, nil); err != nil {
		return fmt.Errorf("create security group %s: %w", params.Group, err)
	}
	return nil
}

// DeleteFirewallSecurityGroup deletes a security group via DELETE /cluster/firewall/groups/{group}.
func (c *Client) DeleteFirewallSecurityGroup(ctx context.Context, group string) error {
	path := "/cluster/firewall/groups/" + url.PathEscape(group)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete security group %s: %w", group, err)
	}
	return nil
}

// GetSecurityGroupRules returns rules in a security group via GET /cluster/firewall/groups/{group}.
func (c *Client) GetSecurityGroupRules(ctx context.Context, group string) ([]FirewallRule, error) {
	path := "/cluster/firewall/groups/" + url.PathEscape(group)
	var rules []FirewallRule
	if err := c.do(ctx, path, &rules); err != nil {
		return nil, fmt.Errorf("get security group %s rules: %w", group, err)
	}
	return rules, nil
}

// CreateSecurityGroupRule creates a rule in a security group via POST /cluster/firewall/groups/{group}.
func (c *Client) CreateSecurityGroupRule(ctx context.Context, group string, params FirewallRuleParams) error {
	form := firewallRuleToForm(params)
	path := "/cluster/firewall/groups/" + url.PathEscape(group)
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create rule in security group %s: %w", group, err)
	}
	return nil
}

// UpdateSecurityGroupRule updates a rule in a security group via PUT /cluster/firewall/groups/{group}/{pos}.
func (c *Client) UpdateSecurityGroupRule(ctx context.Context, group string, pos int, params FirewallRuleParams) error {
	form := firewallRuleToForm(params)
	path := "/cluster/firewall/groups/" + url.PathEscape(group) + "/" + strconv.Itoa(pos)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update rule %d in security group %s: %w", pos, group, err)
	}
	return nil
}

// DeleteSecurityGroupRule deletes a rule from a security group via DELETE /cluster/firewall/groups/{group}/{pos}.
func (c *Client) DeleteSecurityGroupRule(ctx context.Context, group string, pos int) error {
	path := "/cluster/firewall/groups/" + url.PathEscape(group) + "/" + strconv.Itoa(pos)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete rule %d from security group %s: %w", pos, group, err)
	}
	return nil
}

// GetNodeFirewallLog returns the firewall log for a node via GET /nodes/{node}/firewall/log.
func (c *Client) GetNodeFirewallLog(ctx context.Context, node string, limit, start int) ([]FirewallLogEntry, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/firewall/log"
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if start > 0 {
		q.Set("start", strconv.Itoa(start))
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var entries []FirewallLogEntry
	if err := c.do(ctx, path, &entries); err != nil {
		return nil, fmt.Errorf("get firewall log for node %s: %w", node, err)
	}
	return entries, nil
}

// CreateResourcePool creates a new resource pool via POST /pools.
func (c *Client) CreateResourcePool(ctx context.Context, params CreatePoolParams) error {
	form := url.Values{}
	form.Set("poolid", params.PoolID)
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if err := c.doPost(ctx, "/pools", form, nil); err != nil {
		return fmt.Errorf("create resource pool %s: %w", params.PoolID, err)
	}
	return nil
}

// GetResourcePool returns a single resource pool with members via GET /pools/{poolid}.
func (c *Client) GetResourcePool(ctx context.Context, poolID string) (*ResourcePoolDetail, error) {
	path := "/pools/" + url.PathEscape(poolID)
	var pool ResourcePoolDetail
	if err := c.do(ctx, path, &pool); err != nil {
		return nil, fmt.Errorf("get resource pool %s: %w", poolID, err)
	}
	return &pool, nil
}

// UpdateResourcePool updates a resource pool via PUT /pools/{poolid}.
func (c *Client) UpdateResourcePool(ctx context.Context, poolID string, params UpdatePoolParams) error {
	form := url.Values{}
	if params.Comment != nil {
		form.Set("comment", *params.Comment)
	}
	if params.VMs != "" {
		form.Set("vms", params.VMs)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Delete != "" {
		form.Set("delete", params.Delete)
	}
	path := "/pools/" + url.PathEscape(poolID)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update resource pool %s: %w", poolID, err)
	}
	return nil
}

// DeleteResourcePool deletes a resource pool via DELETE /pools/{poolid}.
func (c *Client) DeleteResourcePool(ctx context.Context, poolID string) error {
	path := "/pools/" + url.PathEscape(poolID)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete resource pool %s: %w", poolID, err)
	}
	return nil
}

// GetReplicationJobs returns all replication jobs via GET /cluster/replication.
func (c *Client) GetReplicationJobs(ctx context.Context) ([]ReplicationJob, error) {
	var jobs []ReplicationJob
	if err := c.do(ctx, "/cluster/replication", &jobs); err != nil {
		return nil, fmt.Errorf("get replication jobs: %w", err)
	}
	return jobs, nil
}

// CreateReplicationJob creates a replication job via POST /cluster/replication.
func (c *Client) CreateReplicationJob(ctx context.Context, params CreateReplicationJobParams) error {
	form := url.Values{}
	form.Set("id", params.ID)
	form.Set("type", params.Type)
	form.Set("target", params.Target)
	if params.Schedule != "" {
		form.Set("schedule", params.Schedule)
	}
	if params.Rate != "" {
		form.Set("rate", params.Rate)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Disable != nil {
		form.Set("disable", strconv.Itoa(*params.Disable))
	}
	if err := c.doPost(ctx, "/cluster/replication", form, nil); err != nil {
		return fmt.Errorf("create replication job %s: %w", params.ID, err)
	}
	return nil
}

// GetReplicationJob returns a single replication job via GET /cluster/replication/{id}.
func (c *Client) GetReplicationJob(ctx context.Context, id string) (*ReplicationJob, error) {
	path := "/cluster/replication/" + url.PathEscape(id)
	var job ReplicationJob
	if err := c.do(ctx, path, &job); err != nil {
		return nil, fmt.Errorf("get replication job %s: %w", id, err)
	}
	return &job, nil
}

// UpdateReplicationJob updates a replication job via PUT /cluster/replication/{id}.
func (c *Client) UpdateReplicationJob(ctx context.Context, id string, params UpdateReplicationJobParams) error {
	form := url.Values{}
	if params.Schedule != "" {
		form.Set("schedule", params.Schedule)
	}
	if params.Rate != "" {
		form.Set("rate", params.Rate)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Disable != nil {
		form.Set("disable", strconv.Itoa(*params.Disable))
	}
	if params.RemoveJob != "" {
		form.Set("remove_job", params.RemoveJob)
	}
	path := "/cluster/replication/" + url.PathEscape(id)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update replication job %s: %w", id, err)
	}
	return nil
}

// DeleteReplicationJob deletes a replication job via DELETE /cluster/replication/{id}.
func (c *Client) DeleteReplicationJob(ctx context.Context, id string) error {
	path := "/cluster/replication/" + url.PathEscape(id)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete replication job %s: %w", id, err)
	}
	return nil
}

// TriggerReplication triggers immediate replication via POST /nodes/{node}/replication/{id}/schedule_now.
func (c *Client) TriggerReplication(ctx context.Context, node, id string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/replication/" + url.PathEscape(id) + "/schedule_now"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("trigger replication %s on %s: %w", id, node, err)
	}
	return upid, nil
}

// GetReplicationStatus returns replication status via GET /nodes/{node}/replication/{id}/status.
func (c *Client) GetReplicationStatus(ctx context.Context, node, id string) (*ReplicationStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/replication/" + url.PathEscape(id) + "/status"
	var status ReplicationStatus
	if err := c.do(ctx, path, &status); err != nil {
		return nil, fmt.Errorf("get replication status %s on %s: %w", id, node, err)
	}
	return &status, nil
}

// GetReplicationLog returns replication log via GET /nodes/{node}/replication/{id}/log.
func (c *Client) GetReplicationLog(ctx context.Context, node, id string, limit int) ([]ReplicationLogEntry, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/replication/" + url.PathEscape(id) + "/log"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var entries []ReplicationLogEntry
	if err := c.do(ctx, path, &entries); err != nil {
		return nil, fmt.Errorf("get replication log %s on %s: %w", id, node, err)
	}
	return entries, nil
}

// GetACMEAccounts returns all ACME accounts via GET /cluster/acme/account.
func (c *Client) GetACMEAccounts(ctx context.Context) ([]ACMEAccount, error) {
	var accounts []ACMEAccount
	if err := c.do(ctx, "/cluster/acme/account", &accounts); err != nil {
		return nil, fmt.Errorf("get ACME accounts: %w", err)
	}
	return accounts, nil
}

// CreateACMEAccount creates an ACME account via POST /cluster/acme/account.
func (c *Client) CreateACMEAccount(ctx context.Context, params CreateACMEAccountParams) (string, error) {
	form := url.Values{}
	form.Set("contact", params.Contact)
	if params.Name != "" {
		form.Set("name", params.Name)
	}
	if params.Directory != "" {
		form.Set("directory", params.Directory)
	}
	if params.TOSUrl != "" {
		form.Set("tos_url", params.TOSUrl)
	}
	var upid string
	if err := c.doPost(ctx, "/cluster/acme/account", form, &upid); err != nil {
		return "", fmt.Errorf("create ACME account: %w", err)
	}
	return upid, nil
}

// GetACMEAccount returns a single ACME account via GET /cluster/acme/account/{name}.
func (c *Client) GetACMEAccount(ctx context.Context, name string) (*ACMEAccount, error) {
	path := "/cluster/acme/account/" + url.PathEscape(name)
	var account ACMEAccount
	if err := c.do(ctx, path, &account); err != nil {
		return nil, fmt.Errorf("get ACME account %s: %w", name, err)
	}
	return &account, nil
}

// UpdateACMEAccount updates an ACME account via PUT /cluster/acme/account/{name}.
func (c *Client) UpdateACMEAccount(ctx context.Context, name string, params UpdateACMEAccountParams) error {
	form := url.Values{}
	if params.Contact != "" {
		form.Set("contact", params.Contact)
	}
	path := "/cluster/acme/account/" + url.PathEscape(name)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update ACME account %s: %w", name, err)
	}
	return nil
}

// DeleteACMEAccount deletes an ACME account via DELETE /cluster/acme/account/{name}.
func (c *Client) DeleteACMEAccount(ctx context.Context, name string) error {
	path := "/cluster/acme/account/" + url.PathEscape(name)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete ACME account %s: %w", name, err)
	}
	return nil
}

// GetACMEPlugins returns all ACME plugins via GET /cluster/acme/plugins.
func (c *Client) GetACMEPlugins(ctx context.Context) ([]ACMEPlugin, error) {
	var plugins []ACMEPlugin
	if err := c.do(ctx, "/cluster/acme/plugins", &plugins); err != nil {
		return nil, fmt.Errorf("get ACME plugins: %w", err)
	}
	return plugins, nil
}

// CreateACMEPlugin creates an ACME plugin via POST /cluster/acme/plugins.
func (c *Client) CreateACMEPlugin(ctx context.Context, params CreateACMEPluginParams) error {
	form := url.Values{}
	form.Set("id", params.ID)
	form.Set("type", params.Type)
	if params.API != "" {
		form.Set("api", params.API)
	}
	if params.Data != "" {
		form.Set("data", base64.StdEncoding.EncodeToString([]byte(params.Data)))
	}
	if params.ValidationDelay != nil {
		form.Set("validation-delay", fmt.Sprintf("%d", *params.ValidationDelay))
	}
	if err := c.doPost(ctx, "/cluster/acme/plugins", form, nil); err != nil {
		return fmt.Errorf("create ACME plugin %s: %w", params.ID, err)
	}
	return nil
}

// GetACMEPlugin returns a single ACME plugin via GET /cluster/acme/plugins/{id}.
func (c *Client) GetACMEPlugin(ctx context.Context, id string) (*ACMEPlugin, error) {
	path := "/cluster/acme/plugins/" + url.PathEscape(id)
	var plugin ACMEPlugin
	if err := c.do(ctx, path, &plugin); err != nil {
		return nil, fmt.Errorf("get ACME plugin %s: %w", id, err)
	}
	return &plugin, nil
}

// UpdateACMEPlugin updates an ACME plugin via PUT /cluster/acme/plugins/{id}.
func (c *Client) UpdateACMEPlugin(ctx context.Context, id string, params UpdateACMEPluginParams) error {
	form := url.Values{}
	if params.API != "" {
		form.Set("api", params.API)
	}
	if params.Data != "" {
		form.Set("data", base64.StdEncoding.EncodeToString([]byte(params.Data)))
	}
	if params.ValidationDelay != nil {
		form.Set("validation-delay", fmt.Sprintf("%d", *params.ValidationDelay))
	}
	if params.Digest != "" {
		form.Set("digest", params.Digest)
	}
	path := "/cluster/acme/plugins/" + url.PathEscape(id)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update ACME plugin %s: %w", id, err)
	}
	return nil
}

// DeleteACMEPlugin deletes an ACME plugin via DELETE /cluster/acme/plugins/{id}.
func (c *Client) DeleteACMEPlugin(ctx context.Context, id string) error {
	path := "/cluster/acme/plugins/" + url.PathEscape(id)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete ACME plugin %s: %w", id, err)
	}
	return nil
}

// GetACMEDirectories returns available ACME directories via GET /cluster/acme/directories.
func (c *Client) GetACMEDirectories(ctx context.Context) ([]ACMEDirectory, error) {
	var dirs []ACMEDirectory
	if err := c.do(ctx, "/cluster/acme/directories", &dirs); err != nil {
		return nil, fmt.Errorf("get ACME directories: %w", err)
	}
	return dirs, nil
}

// GetACMETOS returns the ACME terms of service URL via GET /cluster/acme/tos.
func (c *Client) GetACMETOS(ctx context.Context) (string, error) {
	var tos string
	if err := c.do(ctx, "/cluster/acme/tos", &tos); err != nil {
		return "", fmt.Errorf("get ACME TOS: %w", err)
	}
	return tos, nil
}

// GetACMEChallengeSchema returns challenge plugin schemas via GET /cluster/acme/challenge-schema.
func (c *Client) GetACMEChallengeSchema(ctx context.Context) ([]ACMEChallengeSchema, error) {
	var schemas []ACMEChallengeSchema
	if err := c.do(ctx, "/cluster/acme/challenge-schema", &schemas); err != nil {
		return nil, fmt.Errorf("get ACME challenge schema: %w", err)
	}
	return schemas, nil
}

// GetACMEChallengeSchemaRaw returns challenge plugin schemas as raw JSON.
func (c *Client) GetACMEChallengeSchemaRaw(ctx context.Context, dst *json.RawMessage) error {
	return c.do(ctx, "/cluster/acme/challenge-schema", dst)
}

// GetNodeACMEConfig returns ACME-related fields from GET /nodes/{node}/config.
func (c *Client) GetNodeACMEConfig(ctx context.Context, node string) (*NodeACMEConfig, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var cfg NodeACMEConfig
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/config", &cfg); err != nil {
		return nil, fmt.Errorf("get node %s config: %w", node, err)
	}
	return &cfg, nil
}

// SetNodeACMEConfig updates ACME-related fields via PUT /nodes/{node}/config.
func (c *Client) SetNodeACMEConfig(ctx context.Context, node string, cfg NodeACMEConfig) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form := url.Values{}
	if cfg.ACME != "" {
		form.Set("acme", cfg.ACME)
	}
	if cfg.ACMEDomain0 != "" {
		form.Set("acmedomain0", cfg.ACMEDomain0)
	}
	if cfg.ACMEDomain1 != "" {
		form.Set("acmedomain1", cfg.ACMEDomain1)
	}
	if cfg.ACMEDomain2 != "" {
		form.Set("acmedomain2", cfg.ACMEDomain2)
	}
	if cfg.ACMEDomain3 != "" {
		form.Set("acmedomain3", cfg.ACMEDomain3)
	}
	if cfg.ACMEDomain4 != "" {
		form.Set("acmedomain4", cfg.ACMEDomain4)
	}
	if cfg.ACMEDomain5 != "" {
		form.Set("acmedomain5", cfg.ACMEDomain5)
	}
	if err := c.doPut(ctx, "/nodes/"+url.PathEscape(node)+"/config", form, nil); err != nil {
		return fmt.Errorf("set node %s ACME config: %w", node, err)
	}
	return nil
}

// GetNodeCertificates returns certificate info for a node via GET /nodes/{node}/certificates/info.
func (c *Client) GetNodeCertificates(ctx context.Context, node string) ([]NodeCertificate, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/certificates/info"
	var certs []NodeCertificate
	if err := c.do(ctx, path, &certs); err != nil {
		return nil, fmt.Errorf("get node %s certificates: %w", node, err)
	}
	return certs, nil
}

// OrderNodeCertificate orders an ACME certificate for a node via POST /nodes/{node}/certificates/acme/certificate.
func (c *Client) OrderNodeCertificate(ctx context.Context, node string, force bool) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	form := url.Values{}
	if force {
		form.Set("force", "1")
	}
	path := "/nodes/" + url.PathEscape(node) + "/certificates/acme/certificate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("order certificate for node %s: %w", node, err)
	}
	return upid, nil
}

// RenewNodeCertificate renews an ACME certificate for a node via PUT /nodes/{node}/certificates/acme/certificate.
func (c *Client) RenewNodeCertificate(ctx context.Context, node string, force bool) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	form := url.Values{}
	if force {
		form.Set("force", "1")
	}
	path := "/nodes/" + url.PathEscape(node) + "/certificates/acme/certificate"
	var upid string
	if err := c.doPut(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("renew certificate for node %s: %w", node, err)
	}
	return upid, nil
}

// RevokeNodeCertificate revokes an ACME certificate for a node via DELETE /nodes/{node}/certificates/acme/certificate.
func (c *Client) RevokeNodeCertificate(ctx context.Context, node string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/certificates/acme/certificate"
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("revoke certificate for node %s: %w", node, err)
	}
	return upid, nil
}

// GetMetricServers returns all metric server configs via GET /cluster/metrics/server.
func (c *Client) GetMetricServers(ctx context.Context) ([]MetricServerConfig, error) {
	var servers []MetricServerConfig
	if err := c.do(ctx, "/cluster/metrics/server", &servers); err != nil {
		return nil, fmt.Errorf("get metric servers: %w", err)
	}
	return servers, nil
}

// CreateMetricServer creates a metric server via POST /cluster/metrics/server/{id}.
func (c *Client) CreateMetricServer(ctx context.Context, params CreateMetricServerParams) error {
	form := url.Values{}
	form.Set("type", params.Type)
	form.Set("server", params.Server)
	form.Set("port", strconv.Itoa(params.Port))
	if params.Disable != nil {
		form.Set("disable", strconv.Itoa(*params.Disable))
	}
	if params.MTU > 0 {
		form.Set("mtu", strconv.Itoa(params.MTU))
	}
	if params.Timeout > 0 {
		form.Set("timeout", strconv.Itoa(params.Timeout))
	}
	if params.Proto != "" {
		form.Set("proto", params.Proto)
	}
	if params.Path != "" {
		form.Set("path", params.Path)
	}
	if params.InfluxDBProto != "" {
		form.Set("influxdbproto", params.InfluxDBProto)
	}
	if params.Organization != "" {
		form.Set("organization", params.Organization)
	}
	if params.Bucket != "" {
		form.Set("bucket", params.Bucket)
	}
	if params.Token != "" {
		form.Set("token", params.Token)
	}
	if params.MaxBodySize > 0 {
		form.Set("max-body-size", strconv.Itoa(params.MaxBodySize))
	}
	if params.VerifyCert != nil {
		form.Set("verify-certificate", strconv.Itoa(*params.VerifyCert))
	}
	path := "/cluster/metrics/server/" + url.PathEscape(params.ID)
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create metric server %s: %w", params.ID, err)
	}
	return nil
}

// GetMetricServer returns a single metric server via GET /cluster/metrics/server/{id}.
func (c *Client) GetMetricServer(ctx context.Context, id string) (*MetricServerConfig, error) {
	path := "/cluster/metrics/server/" + url.PathEscape(id)
	var server MetricServerConfig
	if err := c.do(ctx, path, &server); err != nil {
		return nil, fmt.Errorf("get metric server %s: %w", id, err)
	}
	return &server, nil
}

// UpdateMetricServer updates a metric server via PUT /cluster/metrics/server/{id}.
func (c *Client) UpdateMetricServer(ctx context.Context, id string, params UpdateMetricServerParams) error {
	form := url.Values{}
	if params.Server != "" {
		form.Set("server", params.Server)
	}
	if params.Port != nil {
		form.Set("port", strconv.Itoa(*params.Port))
	}
	if params.Disable != nil {
		form.Set("disable", strconv.Itoa(*params.Disable))
	}
	if params.MTU > 0 {
		form.Set("mtu", strconv.Itoa(params.MTU))
	}
	if params.Timeout > 0 {
		form.Set("timeout", strconv.Itoa(params.Timeout))
	}
	if params.Proto != "" {
		form.Set("proto", params.Proto)
	}
	if params.Path != "" {
		form.Set("path", params.Path)
	}
	if params.InfluxDBProto != "" {
		form.Set("influxdbproto", params.InfluxDBProto)
	}
	if params.Organization != "" {
		form.Set("organization", params.Organization)
	}
	if params.Bucket != "" {
		form.Set("bucket", params.Bucket)
	}
	if params.Token != "" {
		form.Set("token", params.Token)
	}
	if params.MaxBodySize > 0 {
		form.Set("max-body-size", strconv.Itoa(params.MaxBodySize))
	}
	if params.VerifyCert != nil {
		form.Set("verify-certificate", strconv.Itoa(*params.VerifyCert))
	}
	if params.Delete != "" {
		form.Set("delete", params.Delete)
	}
	path := "/cluster/metrics/server/" + url.PathEscape(id)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update metric server %s: %w", id, err)
	}
	return nil
}

// DeleteMetricServer deletes a metric server via DELETE /cluster/metrics/server/{id}.
func (c *Client) DeleteMetricServer(ctx context.Context, id string) error {
	path := "/cluster/metrics/server/" + url.PathEscape(id)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete metric server %s: %w", id, err)
	}
	return nil
}

// GetClusterConfig returns cluster configuration via GET /cluster/config.
func (c *Client) GetClusterConfig(ctx context.Context) (*ClusterConfig, error) {
	var cfg ClusterConfig
	if err := c.do(ctx, "/cluster/config", &cfg); err != nil {
		return nil, fmt.Errorf("get cluster config: %w", err)
	}
	return &cfg, nil
}

// GetClusterJoinInfo returns cluster join information via GET /cluster/config/join.
func (c *Client) GetClusterJoinInfo(ctx context.Context) (*ClusterJoinInfo, error) {
	var info ClusterJoinInfo
	if err := c.do(ctx, "/cluster/config/join", &info); err != nil {
		return nil, fmt.Errorf("get cluster join info: %w", err)
	}
	return &info, nil
}

// GetCorosyncNodes returns corosync node list via GET /cluster/config/nodes.
func (c *Client) GetCorosyncNodes(ctx context.Context) ([]CorosyncNode, error) {
	var nodes []CorosyncNode
	if err := c.do(ctx, "/cluster/config/nodes", &nodes); err != nil {
		return nil, fmt.Errorf("get corosync nodes: %w", err)
	}
	return nodes, nil
}
