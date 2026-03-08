package proxmox

import (
	"context"
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
	form := sdnZoneToForm(params.Zone, params.Type, params.Bridge, params.Peers, params.Nodes, params.IPAM, params.VLANProtocol, params.Tag, params.MTU)
	if err := c.doPost(ctx, "/cluster/sdn/zones", form, nil); err != nil {
		return fmt.Errorf("create SDN zone %s: %w", params.Zone, err)
	}
	return nil
}

// UpdateSDNZone updates an existing SDN zone.
func (c *Client) UpdateSDNZone(ctx context.Context, zone string, params UpdateSDNZoneParams) error {
	form := sdnZoneToForm("", "", params.Bridge, params.Peers, params.Nodes, params.IPAM, params.VLANProtocol, params.Tag, params.MTU)
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
	form := sdnVNetToForm(params.VNet, params.Zone, params.Alias, params.Tag, params.VLANAware)
	if err := c.doPost(ctx, "/cluster/sdn/vnets", form, nil); err != nil {
		return fmt.Errorf("create SDN vnet %s: %w", params.VNet, err)
	}
	return nil
}

// UpdateSDNVNet updates an existing SDN VNet.
func (c *Client) UpdateSDNVNet(ctx context.Context, vnet string, params UpdateSDNVNetParams) error {
	form := sdnVNetToForm("", params.Zone, params.Alias, params.Tag, params.VLANAware)
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
	form := sdnSubnetToForm(params.Subnet, params.Gateway, params.Type, params.SNAT)
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet) + "/subnets"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create SDN subnet %s on %s: %w", params.Subnet, vnet, err)
	}
	return nil
}

// UpdateSDNSubnet updates an existing subnet under a VNet.
func (c *Client) UpdateSDNSubnet(ctx context.Context, vnet string, subnet string, params UpdateSDNSubnetParams) error {
	form := sdnSubnetToForm("", params.Gateway, "", params.SNAT)
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

// sdnZoneToForm converts SDN zone parameters to url.Values.
func sdnZoneToForm(zone, zoneType, bridge, peers, nodes, ipam, vlanProtocol string, tag, mtu int) url.Values {
	form := url.Values{}
	if zone != "" {
		form.Set("zone", zone)
	}
	if zoneType != "" {
		form.Set("type", zoneType)
	}
	if bridge != "" {
		form.Set("bridge", bridge)
	}
	if peers != "" {
		form.Set("peers", peers)
	}
	if nodes != "" {
		form.Set("nodes", nodes)
	}
	if ipam != "" {
		form.Set("ipam", ipam)
	}
	if vlanProtocol != "" {
		form.Set("vlan-protocol", vlanProtocol)
	}
	if tag != 0 {
		form.Set("tag", strconv.Itoa(tag))
	}
	if mtu != 0 {
		form.Set("mtu", strconv.Itoa(mtu))
	}
	return form
}

// sdnVNetToForm converts SDN VNet parameters to url.Values.
func sdnVNetToForm(vnet, zone, alias string, tag, vlanAware int) url.Values {
	form := url.Values{}
	if vnet != "" {
		form.Set("vnet", vnet)
	}
	if zone != "" {
		form.Set("zone", zone)
	}
	if alias != "" {
		form.Set("alias", alias)
	}
	if tag != 0 {
		form.Set("tag", strconv.Itoa(tag))
	}
	if vlanAware != 0 {
		form.Set("vlanaware", strconv.Itoa(vlanAware))
	}
	return form
}

// sdnSubnetToForm converts SDN subnet parameters to url.Values.
func sdnSubnetToForm(subnet, gateway, subnetType string, snat int) url.Values {
	form := url.Values{}
	if subnet != "" {
		form.Set("subnet", subnet)
	}
	if gateway != "" {
		form.Set("gateway", gateway)
	}
	if subnetType != "" {
		form.Set("type", subnetType)
	}
	if snat != 0 {
		form.Set("snat", strconv.Itoa(snat))
	}
	return form
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
