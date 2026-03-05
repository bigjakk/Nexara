package proxmox

import (
	"context"
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
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/lxc?full=1", &cts); err != nil {
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
func (c *Client) UploadToStorage(ctx context.Context, node, storage, contentType, filename string, reader io.Reader) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/upload"
	fields := map[string]string{
		"content": contentType,
	}
	var upid string
	if err := c.doMultipart(ctx, path, fields, "filename", filename, reader, &upid); err != nil {
		return "", fmt.Errorf("upload to %s/%s: %w", node, storage, err)
	}
	return upid, nil
}

// DeleteStorageContent deletes a volume from a storage pool and returns the task UPID.
func (c *Client) DeleteStorageContent(ctx context.Context, node, storage, volume string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content/" + url.PathEscape(volume)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete volume %s on %s/%s: %w", volume, node, storage, err)
	}
	return upid, nil
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
func (c *Client) GetCephPools(ctx context.Context, node string) ([]CephPool, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var pools []CephPool
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/pools", &pools); err != nil {
		return nil, fmt.Errorf("get ceph pools on %s: %w", node, err)
	}
	return pools, nil
}

// GetCephMonitors returns all Ceph monitors from a node.
func (c *Client) GetCephMonitors(ctx context.Context, node string) ([]CephMon, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var mons []CephMon
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/ceph/mon", &mons); err != nil {
		return nil, fmt.Errorf("get ceph monitors on %s: %w", node, err)
	}
	return mons, nil
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

	path := "/nodes/" + url.PathEscape(node) + "/lxc"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("restore CT %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}
