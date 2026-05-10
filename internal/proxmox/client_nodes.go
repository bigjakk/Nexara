package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

func (c *Client) GetNodes(ctx context.Context) ([]NodeListEntry, error) {
	var nodes []NodeListEntry
	if err := c.do(ctx, "/nodes", &nodes); err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}
	return nodes, nil
}
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
func (c *Client) GetNodeDNS(ctx context.Context, node string) (*NodeDNS, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var dns NodeDNS
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/dns", &dns); err != nil {
		return nil, fmt.Errorf("get node %s dns: %w", node, err)
	}
	return &dns, nil
}
func (c *Client) GetNodeTime(ctx context.Context, node string) (*NodeTime, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var t NodeTime
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/time", &t); err != nil {
		return nil, fmt.Errorf("get node %s time: %w", node, err)
	}
	return &t, nil
}
func (c *Client) SetNodeDNS(ctx context.Context, node, search, dns1, dns2, dns3 string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("search", search)
	if dns1 != "" {
		form.Set("dns1", dns1)
	}
	if dns2 != "" {
		form.Set("dns2", dns2)
	}
	if dns3 != "" {
		form.Set("dns3", dns3)
	}
	if err := c.doPut(ctx, "/nodes/"+url.PathEscape(node)+"/dns", form, nil); err != nil {
		return fmt.Errorf("set node %s dns: %w", node, err)
	}
	return nil
}
func (c *Client) SetNodeTimezone(ctx context.Context, node, timezone string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("timezone", timezone)
	if err := c.doPut(ctx, "/nodes/"+url.PathEscape(node)+"/time", form, nil); err != nil {
		return fmt.Errorf("set node %s timezone: %w", node, err)
	}
	return nil
}
func (c *Client) ShutdownNode(ctx context.Context, node string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("command", "shutdown")
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/status", form, nil); err != nil {
		return fmt.Errorf("shutdown node %s: %w", node, err)
	}
	return nil
}
func (c *Client) GetNodeSubscription(ctx context.Context, node string) (*NodeSubscription, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var sub NodeSubscription
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/subscription", &sub); err != nil {
		return nil, fmt.Errorf("get node %s subscription: %w", node, err)
	}
	return &sub, nil
}
func (c *Client) GetNodeDisks(ctx context.Context, node string) ([]NodeDisk, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var disks []NodeDisk
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/disks/list", &disks); err != nil {
		return nil, fmt.Errorf("get node %s disks: %w", node, err)
	}
	return disks, nil
}
func (c *Client) GetDiskSMART(ctx context.Context, node, disk string) (*DiskSMARTData, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var smart DiskSMARTData
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/disks/smart?disk="+url.QueryEscape(disk), &smart); err != nil {
		return nil, fmt.Errorf("get node %s disk %s smart: %w", node, disk, err)
	}
	return &smart, nil
}
func (c *Client) GetNodeZFSPools(ctx context.Context, node string) ([]ZFSPool, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var pools []ZFSPool
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/disks/zfs", &pools); err != nil {
		return nil, fmt.Errorf("get node %s zfs pools: %w", node, err)
	}
	return pools, nil
}
func (c *Client) CreateNodeZFSPool(ctx context.Context, node string, params CreateZFSPoolParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("name", params.Name)
	form.Set("raidlevel", params.RaidLevel)
	form.Set("devices", params.Devices)
	if params.Compression != "" {
		form.Set("compression", params.Compression)
	}
	if params.Ashift > 0 {
		form.Set("ashift", fmt.Sprintf("%d", params.Ashift))
	}
	var upid string
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/disks/zfs", form, &upid); err != nil {
		return "", fmt.Errorf("create zfs pool on node %s: %w", node, err)
	}
	return upid, nil
}
func (c *Client) DeleteNodeZFSPool(ctx context.Context, node, poolName string, cleanupDisks, cleanupConfig bool) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if poolName == "" {
		return "", fmt.Errorf("pool name is required")
	}
	params := url.Values{}
	if cleanupDisks {
		params.Set("cleanup-disks", "1")
	}
	if cleanupConfig {
		params.Set("cleanup-config", "1")
	}
	path := "/nodes/" + url.PathEscape(node) + "/disks/zfs/" + url.PathEscape(poolName)
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete zfs pool %s on %s: %w", poolName, node, err)
	}
	return upid, nil
}
func (c *Client) GetNodeLVM(ctx context.Context, node string) ([]LVMVolumeGroup, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var wrapper struct {
		Children []lvmVGRaw `json:"children"`
	}
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/disks/lvm", &wrapper); err != nil {
		return nil, fmt.Errorf("get node %s lvm: %w", node, err)
	}
	vgs := make([]LVMVolumeGroup, 0, len(wrapper.Children))
	for _, raw := range wrapper.Children {
		vgs = append(vgs, LVMVolumeGroup{
			Name:    raw.Name,
			Size:    raw.Size,
			Free:    raw.Free,
			PVCount: len(raw.Children),
			LVCount: raw.LVCount,
		})
	}
	return vgs, nil
}
func (c *Client) CreateNodeLVM(ctx context.Context, node string, params CreateLVMParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("name", params.Name)
	form.Set("device", params.Device)
	if params.AddStorage {
		form.Set("add_storage", "1")
	}
	var upid string
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/disks/lvm", form, &upid); err != nil {
		return "", fmt.Errorf("create lvm on node %s: %w", node, err)
	}
	return upid, nil
}
func (c *Client) DeleteNodeLVM(ctx context.Context, node, vgName string, cleanupDisks, cleanupConfig bool) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if vgName == "" {
		return "", fmt.Errorf("volume group name is required")
	}
	params := url.Values{}
	if cleanupDisks {
		params.Set("cleanup-disks", "1")
	}
	if cleanupConfig {
		params.Set("cleanup-config", "1")
	}
	path := "/nodes/" + url.PathEscape(node) + "/disks/lvm/" + url.PathEscape(vgName)
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete lvm vg %s on %s: %w", vgName, node, err)
	}
	return upid, nil
}
func (c *Client) GetNodeLVMThin(ctx context.Context, node string) ([]LVMThinPool, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var pools []LVMThinPool
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/disks/lvmthin", &pools); err != nil {
		return nil, fmt.Errorf("get node %s lvmthin: %w", node, err)
	}
	return pools, nil
}
func (c *Client) CreateNodeLVMThin(ctx context.Context, node string, params CreateLVMThinParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("name", params.Name)
	form.Set("device", params.Device)
	if params.AddStorage {
		form.Set("add_storage", "1")
	}
	var upid string
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/disks/lvmthin", form, &upid); err != nil {
		return "", fmt.Errorf("create lvmthin on node %s: %w", node, err)
	}
	return upid, nil
}
func (c *Client) DeleteNodeLVMThin(ctx context.Context, node, name, volumeGroup string, cleanupDisks, cleanupConfig bool) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if name == "" {
		return "", fmt.Errorf("lvmthin name is required")
	}
	if volumeGroup == "" {
		return "", fmt.Errorf("volume group is required")
	}
	params := url.Values{}
	params.Set("volume-group", volumeGroup)
	if cleanupDisks {
		params.Set("cleanup-disks", "1")
	}
	if cleanupConfig {
		params.Set("cleanup-config", "1")
	}
	path := "/nodes/" + url.PathEscape(node) + "/disks/lvmthin/" + url.PathEscape(name) + "?" + params.Encode()
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete lvmthin %s on %s: %w", name, node, err)
	}
	return upid, nil
}
func (c *Client) GetNodeDirectories(ctx context.Context, node string) ([]DirectoryEntry, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var dirs []DirectoryEntry
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/disks/directory", &dirs); err != nil {
		return nil, fmt.Errorf("get node %s directories: %w", node, err)
	}
	return dirs, nil
}
func (c *Client) CreateNodeDirectory(ctx context.Context, node string, params CreateDirectoryParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("device", params.Device)
	form.Set("name", params.Name)
	form.Set("filesystem", params.Filesystem)
	if params.AddStorage {
		form.Set("add_storage", "1")
	}
	var upid string
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/disks/directory", form, &upid); err != nil {
		return "", fmt.Errorf("create directory on node %s: %w", node, err)
	}
	return upid, nil
}
func (c *Client) InitializeGPT(ctx context.Context, node, disk string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("disk", disk)
	var upid string
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/disks/initgpt", form, &upid); err != nil {
		return "", fmt.Errorf("initialize gpt on node %s disk %s: %w", node, disk, err)
	}
	return upid, nil
}
func (c *Client) WipeDisk(ctx context.Context, node, disk string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("disk", disk)
	var upid string
	if err := c.doPut(ctx, "/nodes/"+url.PathEscape(node)+"/disks/smart", form, &upid); err != nil {
		return "", fmt.Errorf("wipe disk on node %s disk %s: %w", node, disk, err)
	}
	return upid, nil
}
func (c *Client) MigrateAllGuests(ctx context.Context, node, targetNode string, maxWorkers int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	form := url.Values{}
	if targetNode != "" {
		form.Set("target", targetNode)
	}
	if maxWorkers > 0 {
		form.Set("maxworkers", fmt.Sprintf("%d", maxWorkers))
	}
	var upid string
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/migrateall", form, &upid); err != nil {
		return "", fmt.Errorf("migrate all guests off node %s: %w", node, err)
	}
	return upid, nil
}
func (c *Client) GetNodeServices(ctx context.Context, node string) ([]NodeService, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var svcs []NodeService
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/services", &svcs); err != nil {
		return nil, fmt.Errorf("get node %s services: %w", node, err)
	}
	return svcs, nil
}
func (c *Client) ServiceAction(ctx context.Context, node, service, action string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	var upid string
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/services/"+url.PathEscape(service)+"/"+url.PathEscape(action), nil, &upid); err != nil {
		return "", fmt.Errorf("%s service %s on node %s: %w", action, service, node, err)
	}
	return upid, nil
}
func (c *Client) GetNodeSyslog(ctx context.Context, node string, start, limit int, since, until, service string) ([]SyslogEntry, int, error) {
	if err := validateNodeName(node); err != nil {
		return nil, 0, err
	}
	basePath := "/nodes/" + url.PathEscape(node) + "/syslog"

	buildQuery := func(s, l int) string {
		q := url.Values{}
		if s > 0 {
			q.Set("start", fmt.Sprintf("%d", s))
		}
		if l > 0 {
			q.Set("limit", fmt.Sprintf("%d", l))
		}
		if since != "" {
			q.Set("since", since)
		}
		if until != "" {
			q.Set("until", until)
		}
		if service != "" {
			q.Set("service", service)
		}
		if qs := q.Encode(); qs != "" {
			return basePath + "?" + qs
		}
		return basePath
	}

	// When start == -1, fetch the newest entries by getting total first.
	if start < 0 {
		total, err := c.doGetTotal(ctx, buildQuery(0, 1))
		if err != nil {
			return nil, 0, fmt.Errorf("get node %s syslog total: %w", node, err)
		}
		realStart := total - limit
		if realStart < 0 {
			realStart = 0
		}
		var entries []SyslogEntry
		if err := c.do(ctx, buildQuery(realStart, limit), &entries); err != nil {
			return nil, 0, fmt.Errorf("get node %s syslog: %w", node, err)
		}
		return entries, total, nil
	}

	var entries []SyslogEntry
	total, err := c.doWithTotal(ctx, buildQuery(start, limit), &entries)
	if err != nil {
		return nil, 0, fmt.Errorf("get node %s syslog: %w", node, err)
	}
	return entries, total, nil
}
func (c *Client) GetNodePCIDevices(ctx context.Context, node string) ([]NodePCIDevice, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var devs []NodePCIDevice
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/hardware/pci", &devs); err != nil {
		return nil, fmt.Errorf("get node %s pci devices: %w", node, err)
	}
	return devs, nil
}
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
func (c *Client) CreateNodeFirewallRule(ctx context.Context, node string, rule FirewallRuleParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form := firewallRuleToForm(rule)
	path := "/nodes/" + url.PathEscape(node) + "/firewall/rules"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create firewall rule on %s: %w", node, err)
	}
	return nil
}
func (c *Client) DeleteNodeFirewallRule(ctx context.Context, node string, pos int) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	path := "/nodes/" + url.PathEscape(node) + "/firewall/rules/" + strconv.Itoa(pos)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete firewall rule %d on %s: %w", pos, node, err)
	}
	return nil
}
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
func (c *Client) GetNodeAptChangelog(ctx context.Context, node, pkg, version string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if pkg == "" {
		return "", fmt.Errorf("package name is required")
	}
	params := url.Values{}
	params.Set("name", pkg)
	if version != "" {
		params.Set("version", version)
	}
	var changelog string
	path := "/nodes/" + url.PathEscape(node) + "/apt/changelog?" + params.Encode()
	if err := c.do(ctx, path, &changelog); err != nil {
		return "", fmt.Errorf("get changelog for %s=%s on %s: %w", pkg, version, node, err)
	}
	return changelog, nil
}
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
func (c *Client) GetNodeAptRepositories(ctx context.Context, node string) (*AptRepositoryResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var resp AptRepositoryResponse
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/apt/repositories", &resp); err != nil {
		return nil, fmt.Errorf("get apt repositories on %s: %w", node, err)
	}
	return &resp, nil
}
func (c *Client) SetNodeAptRepository(ctx context.Context, node, filePath string, index int, enabled bool, digest string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	params := url.Values{}
	params.Set("path", filePath)
	params.Set("index", strconv.Itoa(index))
	if enabled {
		params.Set("enabled", "1")
	} else {
		params.Set("enabled", "0")
	}
	if digest != "" {
		params.Set("digest", digest)
	}
	if err := c.doPost(ctx, "/nodes/"+url.PathEscape(node)+"/apt/repositories", params, nil); err != nil {
		return fmt.Errorf("set apt repository on %s: %w", node, err)
	}
	return nil
}
func (c *Client) AddNodeAptStandardRepository(ctx context.Context, node, handle, digest string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	params := url.Values{}
	params.Set("handle", handle)
	if digest != "" {
		params.Set("digest", digest)
	}
	if err := c.doPut(ctx, "/nodes/"+url.PathEscape(node)+"/apt/repositories", params, nil); err != nil {
		return fmt.Errorf("add standard apt repository %q on %s: %w", handle, node, err)
	}
	return nil
}
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
