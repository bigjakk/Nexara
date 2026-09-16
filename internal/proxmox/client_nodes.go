package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
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

// GetNodeReport returns the node's system report: the same bundle `pvereport`
// assembles, as one plain-text document. Proxmox sends it as a single JSON
// string rather than a structured object, so the destination is a string.
//
// The report is expensive to produce (it shells out to a long list of commands
// on the node) and large, so nothing should call this on a schedule or hold it
// in a cache — it exists for an operator who has been asked to produce one.
// Callers should give the client a longer timeout than the default.
//
// It is also a full disclosure of the host: storage configuration, network
// layout, package inventory and guest configs. Gate it accordingly.
func (c *Client) GetNodeReport(ctx context.Context, node string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	var report string
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/report", &report); err != nil {
		return "", fmt.Errorf("get node %s report: %w", node, err)
	}
	return report, nil
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
	if err := validatePathSegment("zfs pool name", poolName); err != nil {
		return "", err
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
	if err := validatePathSegment("volume group name", vgName); err != nil {
		return "", err
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
	if err := validatePathSegment("lvmthin name", name); err != nil {
		return "", err
	}
	// Not a path segment — it goes out as a query parameter, so it needs no
	// traversal guard. Wrapped anyway so an empty one is the 400 its three
	// siblings above return, rather than a 500 for the same mistake.
	if volumeGroup == "" {
		return "", fmt.Errorf("%w: volume group is required", ErrInvalidInput)
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
	if err := c.doPut(ctx, "/nodes/"+url.PathEscape(node)+"/disks/wipedisk", form, &upid); err != nil {
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

// JournalOptions selects a window of the systemd journal for GetNodeJournal.
// A zero field means "not sent", leaving the choice to Proxmox.
type JournalOptions struct {
	Since       int64  // unix seconds, inclusive
	Until       int64  // unix seconds, inclusive
	LastEntries int    // return only the newest N lines
	StartCursor string // opaque journal cursor, exclusive
	EndCursor   string // opaque journal cursor, exclusive
}

// GetNodeJournal reads GET /nodes/{node}/journal, which returns raw journal
// lines as a flat array of strings.
//
// Distinct from GetNodeSyslog in two ways that matter to callers: it takes
// `since`/`until` as unix timestamps rather than a wall-clock string the node
// parses in its own local timezone, and it supports `lastentries` — "the last
// N lines", which the syslog endpoint cannot express at all.
func (c *Client) GetNodeJournal(ctx context.Context, node string, opts JournalOptions) ([]string, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	q := url.Values{}
	if opts.Since > 0 {
		q.Set("since", strconv.FormatInt(opts.Since, 10))
	}
	if opts.Until > 0 {
		q.Set("until", strconv.FormatInt(opts.Until, 10))
	}
	if opts.LastEntries > 0 {
		q.Set("lastentries", strconv.Itoa(opts.LastEntries))
	}
	if opts.StartCursor != "" {
		q.Set("startcursor", opts.StartCursor)
	}
	if opts.EndCursor != "" {
		q.Set("endcursor", opts.EndCursor)
	}
	path := "/nodes/" + url.PathEscape(node) + "/journal"
	if qs := q.Encode(); qs != "" {
		path += "?" + qs
	}
	var lines []string
	if err := c.do(ctx, path, &lines); err != nil {
		return nil, fmt.Errorf("get node %s journal: %w", node, err)
	}
	return lines, nil
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

// nodeACMESetting binds a node config key to the field that carries it.
//
// One table drives the write form, the set-and-clear check, the delete
// allow-list and the audit detail. Before it there were three parallel lists of
// the same seven keys, and dropping a row from any one of them was invisible:
// the key silently stopped being sent, and — because the conflict check read
// the same list — its delete guard silently stopped firing too.
// TestNodeACMESettingsCoverTheStruct holds this table against the struct.
type nodeACMESetting struct {
	key   string
	value func(NodeACMEConfig) string
}

var nodeACMESettings = []nodeACMESetting{
	{"acme", func(c NodeACMEConfig) string { return c.ACME }},
	{"acmedomain0", func(c NodeACMEConfig) string { return c.ACMEDomain0 }},
	{"acmedomain1", func(c NodeACMEConfig) string { return c.ACMEDomain1 }},
	{"acmedomain2", func(c NodeACMEConfig) string { return c.ACMEDomain2 }},
	{"acmedomain3", func(c NodeACMEConfig) string { return c.ACMEDomain3 }},
	{"acmedomain4", func(c NodeACMEConfig) string { return c.ACMEDomain4 }},
	{"acmedomain5", func(c NodeACMEConfig) string { return c.ACMEDomain5 }},
}

// deletableNodeACMEKeys is exactly the settable keys and nothing else: this
// method may clear what it may write, which is the whole of the ACME config.
//
// The allow-list matters more here than it does for network interfaces.
// PUT /nodes/{node}/config takes `delete` as a raw list of option names and
// applies it to the WHOLE node config — `description`, `location`,
// `wakeonlan`, `startall-onboot-delay` and `ballooning-target` live in the
// same file and would go the same way — and the handler binds NodeACMEConfig
// straight from the request body, so `delete` is caller-supplied all the way
// from the HTTP client. Validating here rather than in the handler makes it a
// choke point no future caller can forget.
//
// acmedomain0..5 is the complete set: $MAXDOMAINS is 5 in PVE::NodeConfig.
var deletableNodeACMEKeys = func() map[string]bool {
	m := make(map[string]bool, len(nodeACMESettings))
	for _, s := range nodeACMESettings {
		m[s.key] = true
	}
	return m
}()

// NodeACMESetKeys names the settings a config would write, without their
// values. The audit log takes the names only: the values are ACME domains and
// view:audit is granted to every Viewer by default.
func NodeACMESetKeys(cfg NodeACMEConfig) []string {
	keys := make([]string, 0, len(nodeACMESettings))
	for _, s := range nodeACMESettings {
		if s.value(cfg) != "" {
			keys = append(keys, s.key)
		}
	}
	return keys
}

// SetNodeACMEConfig writes the node's ACME settings.
//
// An empty field means "leave alone": every ACME key is optional in PVE's
// schema, so there is no value that means "remove" and omitting the key is how
// an unchanged setting is expressed. Clearing one goes through cfg.Delete.
//
// Note that clearing "acme" is not "turn ACME off", and it is the one delete
// here that can lose data. PVE's get_acme_conf reads the account with
// `$res->{account} //= 'default'`, so removing the key leaves every
// acmedomainN entry active and reverts the account to default rather than
// disabling anything. It is also a property string that may carry its own
// `domains=a;b` list, which get_acme_conf parses into standalone domains —
// those go with it. The acmedomainN keys hold one domain each and lose only
// that one.
func (c *Client) SetNodeACMEConfig(ctx context.Context, node string, cfg NodeACMEConfig) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form := url.Values{}
	values := make(map[string]string, len(nodeACMESettings))
	for _, s := range nodeACMESettings {
		v := s.value(cfg)
		values[s.key] = v
		if v != "" {
			form.Set(s.key, v)
		}
	}
	for _, k := range cfg.Delete {
		if !deletableNodeACMEKeys[k] {
			return fmt.Errorf("%w: %q is not an ACME setting that can be cleared", ErrInvalidInput, k)
		}
		// PVE applies `delete` *after* the assignments (set_options in
		// PVE/API2/NodeConfig.pm), so a key in both wins as a delete and the
		// value is silently dropped — no error, and a caller that meant to
		// replace a domain would find it gone. Refuse instead.
		if values[k] != "" {
			return fmt.Errorf("%w: %q cannot be set and cleared in the same request", ErrInvalidInput, k)
		}
	}
	if len(cfg.Delete) > 0 {
		form.Set("delete", strings.Join(cfg.Delete, ","))
	}
	if cfg.Digest != "" {
		form.Set("digest", cfg.Digest)
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
