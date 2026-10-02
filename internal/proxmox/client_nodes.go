package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
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
func (c *Client) DeleteNodeFirewallRule(ctx context.Context, node string, pos int, digest string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	path := firewallRuleDeletePath("/nodes/"+url.PathEscape(node)+"/firewall/rules/"+strconv.Itoa(pos), digest)
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
// same file and would go the same way. Those five are SetNodeOptions's, with an
// allow-list of their own (deletableNodeOptionKeys) that is disjoint from this
// one: each method may clear what it may write and nothing of the other's, and
// TestNodeConfigAllowListsPartitionPVEsKeys holds the pair against PVE's whole
// key set, so a key PVE adds to $confdesc has to be given to one of them rather
// than being clearable by both or by neither. The handler binds NodeACMEConfig
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
//
// Every refusal this method makes is ValidateNodeACMEConfig's, which the node
// name's check precedes and the write follows, with nothing sent for a refused
// request. One of them is a request that changes nothing — no setting and
// nothing to clear, a digest alone being neither — because Proxmox would still
// rewrite the whole config file for it, and the caller's audit row would name
// nothing (SetNodeOptions refuses the same request for the same reasons).
//
// Another is a value that holds a line break or any other control character —
// C0, DEL and C1, and U+2028 and U+2029 with them (hasLineBreakOrControl) — or
// that is not valid UTF-8. It is the refusal SetNodeOptions makes of its own
// values, in the same words, and it is made of every setting nodeACMESettings
// names.
//
// The settings are written raw into the node's line-oriented config file, and
// write_node_config in pve-manager's PVE/NodeConfig.pm dies with "detected invalid
// newline inside property" on "\n" and on nothing else. What reaches it past
// Proxmox's own checks is a whitespace-class character or a NUL, in three places
// where a value is not held to an anchored format:
//
//   - a segment of a property string that is nothing but whitespace, in acme and in
//     an acmedomainN alike. parse_property_string (pve-common's PVE/JSONSchema.pm)
//     skips it (`next if $part =~ /^\s*\z/`), so "domain=a.example.com,\r" and
//     "account=default,\r" are valid, and are written with the "\r". A "\n" in its
//     place is valid too, and is what write_node_config then refuses with a plain
//     500, which the handler would show as a 502.
//   - the domains list of acme. pve-acme-domain-list is the list form of
//     pve-acme-domain, and check_format (pve-common's PVE/JSONSchema.pm) checks a
//     list form by splitting the value with split_list (pve-common's
//     PVE/ParseUtils.pm) and checking each entry, so a whitespace-class character at
//     its end is a separator that yields no entry:
//     "account=default,domains=a.example.com;b.example.com\r" is valid.
//   - the NUL branch of split_list: a list that holds a NUL is split on NUL alone, so
//     "account=default,domains=a.example.com\x00b.example.com" is valid too.
//
// So what reached the file, and is refused now, was TAB, VT, FF, CR, NEL, U+2028,
// U+2029 and NUL, while "\n" got as far as the write, which refused it. Refusing
// those is the fix, and a check of the whole value covers all three places at once.
// (The plain space and the Unicode space separators reached the file too, and pass,
// by decision: see buildNodeACMEForm.) Refusing the rest of the control characters is
// defence in depth: ESC, CSI, OSC and DEL never reached the file, because the
// anchored formats reject them wherever a value is checked: pve-acme-domain and
// pve-acme-alias in PVE/NodeConfig.pm, and pve-configid, in pve-common's
// PVE/JSONSchema.pm, for the plugin there and for the account, whose format
// defaultACMEAccountName in internal/api/handlers/acme.go records from pve-manager's
// PVE/CertHelpers.pm. So no legitimate account, domain, plugin id or alias holds a
// control character of any kind, and refusing one costs nothing.
//
// What the caller is told changes accordingly. For TAB, VT, FF, CR, NEL, U+2028,
// U+2029 and NUL it is a 400 where there was a write, and for "\n" a 400 in place of
// a 502. For ESC, CSI, OSC and DEL the status is unchanged, Proxmox having answered
// them with a 400 of its own (mapProxmoxError surfaces a parameter rejection as
// one), but that 400 now comes from Nexara, before anything is sent, in its own
// words.
//
// A value that is not valid UTF-8 is refused for the reason SetNodeOptions gives. Over
// HTTP that half never fires: encoding/json has already replaced a bad byte with
// U+FFFD before the handler reads the body, so it serves the direct callers of the
// client, where every caller is held to the same rule.
func (c *Client) SetNodeACMEConfig(ctx context.Context, node string, cfg NodeACMEConfig) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form, err := buildNodeACMEForm(cfg)
	if err != nil {
		return err
	}
	if err := c.doPut(ctx, "/nodes/"+url.PathEscape(node)+"/config", form, nil); err != nil {
		return fmt.Errorf("set node %s ACME config: %w", node, err)
	}
	return nil
}

// ValidateNodeACMEConfig reports what SetNodeACMEConfig would refuse about cfg
// before sending anything: a request that changes nothing, a key to clear that is
// no ACME setting, a key that is both set and cleared, a value that is not valid
// UTF-8 or that holds a line break or any other control character, a body too
// large for Proxmox. It is for a caller that has something to do between
// deciding to write and writing — the handler re-reads the node config's digest
// to check a save token — and wants a refused request to cost no Proxmox read.
// SetNodeACMEConfig runs the same checks itself, so it stays the choke point
// whether a caller asks first or not. A caller that means to substitute a digest
// afterwards should give cfg one of the length Proxmox's will be (40 hex
// characters), and not the one it has now: the digest is part of the encoded
// body whose size is checked.
func ValidateNodeACMEConfig(cfg NodeACMEConfig) error {
	_, err := buildNodeACMEForm(cfg)
	return err
}

// buildNodeACMEForm is the form SetNodeACMEConfig sends, and the one place that
// decides whether it may be.
func buildNodeACMEForm(cfg NodeACMEConfig) (url.Values, error) {
	form := url.Values{}
	values := make(map[string]string, len(nodeACMESettings))
	for _, s := range nodeACMESettings {
		v := s.value(cfg)
		values[s.key] = v
		if v != "" {
			// SetNodeOptions's two refusals, in its order and its words, for the reason
			// SetNodeACMEConfig gives. They are made of every key nodeACMESettings
			// names, so a key added to it is covered with no further change here.
			//
			// What they leave alone is a decision, and not a gap: the plain space, and the
			// Unicode space separators that are not control characters — U+00A0, U+1680,
			// U+2000 to U+200A, U+202F, U+205F and U+3000. Perl's \s reads them as
			// whitespace in the two places a value is not held to an anchored format (a
			// whitespace-only segment, the domains list), so they can reach the file; but
			// they have no line structure and no terminal effect, every anchored position
			// rejects them, and refusing them would be stricter than the options form's
			// refusal that this one matches. Nor is hasLineBreakOrControl to be widened for
			// them: a location's name is free text, and holds them legitimately.
			//
			// In the domains list they do what a plain space does. Proxmox's check splits
			// that list on any whitespace (split_list, pve-common's PVE/ParseUtils.pm), but
			// get_acme_conf (PVE/NodeConfig.pm) reads the stored value split on ";" only, so
			// a list that was validated as N domains is one identifier when it is read. That
			// is Proxmox's, it is reachable with a plain space, and refusing these would not
			// close it.
			// TestSetNodeACMEConfig_AcceptsUnicodeSpaceSeparators pins the set.
			if !utf8.ValidString(v) {
				return nil, fmt.Errorf("%w: %q is not valid UTF-8", ErrInvalidInput, s.key)
			}
			if hasLineBreakOrControl(v) {
				return nil, fmt.Errorf("%w: %q cannot contain a line break or a control character", ErrInvalidInput, s.key)
			}
			form.Set(s.key, v)
		}
	}
	// A request that sets nothing and clears nothing is refused, as SetNodeOptions
	// refuses its own: set_options would still rewrite the whole file (moving the
	// digest under every open dialog), and the audit row would say "updated" and
	// name nothing. A digest is neither a setting nor a clear, and is not in the
	// form yet. It stays here, in the client, so that it is made after the cluster
	// is resolved (the route sweep sends this route an empty body) and for every
	// caller of the client.
	if len(form) == 0 && len(cfg.Delete) == 0 {
		return nil, fmt.Errorf("%w: nothing to change: set a setting or name one in delete", ErrInvalidInput)
	}
	for _, k := range cfg.Delete {
		if !deletableNodeACMEKeys[k] {
			return nil, fmt.Errorf("%w: %q is not an ACME setting that can be cleared", ErrInvalidInput, k)
		}
		// PVE applies `delete` *after* the assignments (set_options in
		// PVE/API2/NodeConfig.pm), so a key in both wins as a delete and the
		// value is silently dropped — no error, and a caller that meant to
		// replace a domain would find it gone. Refuse instead.
		if values[k] != "" {
			return nil, fmt.Errorf("%w: %q cannot be set and cleared in the same request", ErrInvalidInput, k)
		}
	}
	if len(cfg.Delete) > 0 {
		form.Set("delete", strings.Join(cfg.Delete, ","))
	}
	// The digest is not held to the value checks above: it is no setting, and it is
	// never written to the file. set_options extracts it and hands it to
	// PVE::Tools::assert_if_modified (pve-common's PVE/Tools.pm), which only
	// compares it with the file's own, and write_node_config skips the key. When
	// SetNodeACMEConfig is called by the handler it is Proxmox's own sha1_hex, put
	// there in place of the save token, and for ValidateNodeACMEConfig it is a
	// placeholder; whatever else a caller gives it can only fail that comparison.
	if cfg.Digest != "" {
		form.Set("digest", cfg.Digest)
	}
	if err := checkRequestSize(form); err != nil {
		return nil, err
	}
	return form, nil
}

// maxNodeConfigBody is the largest request body every Proxmox VE accepts:
// pveproxy refuses a non-multipart body over $limit_max_post with a 501 "for data
// too large" (pve-http-server's src/PVE/APIServer/AnyEvent.pm,
// authenticate_and_handle_request), 64 KiB before libpve-http-server-perl 5.2.1
// and 512 KiB since (commit 2650923, "fix #6230: increase allowed post size").
// It is the larger of the two, so a request refused against it is refused by
// every version, and the client is never stricter than Proxmox.
const maxNodeConfigBody = 512 * 1024

// checkRequestSize refuses a form whose encoded body is over maxNodeConfigBody,
// which is the length pveproxy compares (it checks Content-Length, and doPut sends
// form.Encode()). The check is not only an economy. pveproxy answers its 501 after
// the request head, without reading the body, so a sender still writing a body
// that large may see the connection reset instead of the answer — a 502 for what
// is the caller's own request. Refusing here gives the same answer every time.
//
// A body of exactly the limit is allowed, as Proxmox allows it: its test is
// `$len > $limit_max_post`.
func checkRequestSize(form url.Values) error {
	if n := len(form.Encode()); n > maxNodeConfigBody {
		return fmt.Errorf("%w: the request is %d bytes once encoded, and Proxmox refuses a body over %d",
			ErrRequestTooLarge, n, maxNodeConfigBody)
	}
	return nil
}

// GetNodeOptions reads the node's own settings — the five non-ACME keys of
// GET /nodes/{node}/config — and the digest of the file they live in. The ACME
// keys come back in the same reply and are dropped, because NodeOptions names
// none of them; NodeACMEConfig reads them from the same endpoint.
//
// Proxmox forwards this read to the node itself (`proxyto => 'node'` in
// PVE/API2/NodeConfig.pm) and wants Sys.Audit on /, so an offline node answers
// with an error rather than an empty config. An online node that has no config
// file at all answers {} with no digest, which reads back as the zero struct.
func (c *Client) GetNodeOptions(ctx context.Context, node string) (*NodeOptions, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var opts NodeOptions
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/config", &opts); err != nil {
		return nil, fmt.Errorf("get node %s options: %w", node, err)
	}
	return &opts, nil
}

// nodeOptionSetting binds a node config key to the field that carries it, as
// nodeACMESetting does for the ACME keys.
//
// value reports the form value AND whether the key is set, where
// nodeACMESetting's reports a bare string. The pair is the difference between
// the two families: an ACME key is set when its string is non-empty, but 0 is a
// value for both integers here, so "set" is decided by the pointer and never by
// what the value renders as. A table that treated "0" or "" as unset would
// stop sending a delay of 0 — the value that turns the delay off — and would
// stop refusing a request that sets it and clears it too.
//
// One table drives the write form, the set-and-clear check, the delete
// allow-list and the audit names. TestNodeOptionSettingsCoverTheStruct holds it
// against the struct.
type nodeOptionSetting struct {
	key   string
	value func(NodeOptions) (string, bool)
}

var nodeOptionSettings = []nodeOptionSetting{
	{"startall-onboot-delay", func(o NodeOptions) (string, bool) { return flexIntFormValue(o.StartallOnbootDelay) }},
	{"ballooning-target", func(o NodeOptions) (string, bool) { return flexIntFormValue(o.BallooningTarget) }},
	{"wakeonlan", func(o NodeOptions) (string, bool) { return o.WakeOnLAN, o.WakeOnLAN != "" }},
	{"location", func(o NodeOptions) (string, bool) { return o.Location, o.Location != "" }},
	{"description", func(o NodeOptions) (string, bool) { return o.Description, o.Description != "" }},
}

// flexIntFormValue is the form value of an optional integer, and whether it is
// set: nil is not, and a pointer to 0 is, written as "0".
func flexIntFormValue(v *FlexInt) (string, bool) {
	if v == nil {
		return "", false
	}
	return strconv.Itoa(int(*v)), true
}

// deletableNodeOptionKeys is exactly the keys SetNodeOptions may write, and
// nothing else — see deletableNodeACMEKeys for why `delete` needs an allow-list
// at all. It is disjoint from that one on purpose: the two methods share
// PUT /nodes/{node}/config and a file, and neither may clear the other's keys.
var deletableNodeOptionKeys = func() map[string]bool {
	m := make(map[string]bool, len(nodeOptionSettings))
	for _, s := range nodeOptionSettings {
		m[s.key] = true
	}
	return m
}()

// NodeOptionsSetKeys names the settings the options would write, in table
// order and without their values. The audit row records these names: the
// notes, the location and the Wake-on-LAN MAC are free text, view:audit is
// granted to every Viewer by default, and a MAC or a position identifies a
// machine. A setting at 0 counts as set. The result is never nil, so a row with
// nothing set reads [] rather than null.
func NodeOptionsSetKeys(opts NodeOptions) []string {
	keys := make([]string, 0, len(nodeOptionSettings))
	for _, s := range nodeOptionSettings {
		if _, ok := s.value(opts); ok {
			keys = append(keys, s.key)
		}
	}
	return keys
}

// NodeOptionsClearKeys names the settings the options would clear, in request
// order, keeping only names SetNodeOptions would accept. The audit row records
// these rather than opts.Delete itself: Delete is the caller's own strings, the
// row is readable by every Viewer, and a filter here means a string that is not
// a setting's name can never reach it, whatever calls the builder or however
// it is later moved. A key named twice is named twice, as it was sent. The
// result is never nil, so a row with nothing cleared reads [] rather than null.
func NodeOptionsClearKeys(opts NodeOptions) []string {
	keys := make([]string, 0, len(opts.Delete))
	for _, k := range opts.Delete {
		if deletableNodeOptionKeys[k] {
			keys = append(keys, k)
		}
	}
	return keys
}

// hasLineBreakOrControl reports whether v holds a control character — the C0
// range, DEL and the C1 range, so "\n", "\r", "\t", ESC and U+0085 are all among
// them — or one of the Unicode line and paragraph separators, U+2028 and
// U+2029, which unicode.IsControl does not count.
func hasLineBreakOrControl(v string) bool {
	for _, r := range v {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return true
		}
	}
	return false
}

// SetNodeOptions writes the node's own settings.
//
// A field left out, or an EMPTY string, leaves the stored value alone: every
// key is optional in PVE's schema, so no value means "remove". Clearing one
// goes through opts.Delete. An integer has no empty form: nil is absent, and a
// pointer to 0 is a value, sent as "0".
//
// Everything this method refuses is refused here, with ErrInvalidInput (the last
// of these, ErrRequestTooLarge) and before anything is sent, so that every
// caller of the client gets the same answer whatever route it came through:
//
//   - A request that changes nothing: no setting and nothing to clear, a digest
//     alone being neither. Proxmox would still rewrite the whole file — set_options
//     ends in write_config, which writes the file out again from what it parsed
//     — and that can move the digest under every dialog that is open on the
//     node, for a save that changed nothing; and Nexara would record a change
//     whose row names nothing.
//   - A key that is both set and cleared. PVE's set_options assigns every
//     supplied key and applies `delete` afterwards, so the value would be
//     dropped silently, and the 200 would say it was written.
//   - A `delete` entry that is not one of the five keys of nodeOptionSettings.
//     PVE applies `delete` to the WHOLE file, so the ACME keys beside these
//     would go with them; those are SetNodeACMEConfig's and are named in the
//     refusal.
//   - A value that is not valid UTF-8, the description included. Nothing decoded
//     from the API's JSON can be one today, since Go replaces a bad byte with
//     U+FFFD, and the client is where every future caller is held to the same
//     rule: what Proxmox would make of such bytes in the file, or of its own
//     %-encoding of them in a note, is not something to find out on a node.
//   - A line break or any other control character in anything but the
//     description, and U+2028 and U+2029 with them.
//   - A request whose encoded body is over 512 KiB, the most any Proxmox accepts
//     (ErrRequestTooLarge, checkRequestSize).
//
// The control-character refusal is deliberately stricter than Proxmox.
// write_node_config in pve-manager's PVE/NodeConfig.pm dies with "detected
// invalid newline inside property" on "\n" and on nothing else, and the format
// check lets one through: parse_property_string skips a segment that is
// whitespace-only (`next if $part =~ /^\s*\z/`), so "<mac>,\n" is a valid
// wakeonlan that the write then refuses with a plain 500 — and "<mac>,\r" or
// "<mac>,\t" is a valid one that is written. A location's `name` is free text up
// to 128 characters, and takes anything. These values are written raw into a
// line-oriented file, no legitimate one holds such a character, and one that did
// would let a manage:node caller plant terminal escapes in the output of `pvenode
// config get` or `cat`, or a line break that other line-oriented readers split
// on. The description is exempt: write_node_config writes each of its lines as
// '#' plus encode_text (pve-common's PVE/ParseUtils.pm), which %-escapes control
// characters, and a note is stored as lines.
//
// A PVE too old to know a key (location before pve-manager 9.1.13,
// ballooning-target before 8.3.6) refuses it with a 400 that names it; that is
// passed on rather than guessed at here. The write needs Sys.Modify on /, which
// PVEAdmin lacks, and Proxmox forwards it to the node itself, so an offline node
// cannot be written. Digest makes it a compare-and-swap over the WHOLE file.
//
// What only Proxmox can refuse is not decided here. A body over the post limit of
// an older pveproxy — 64 KiB before libpve-http-server-perl 5.2.1 — is answered
// 501 "for data too large", counted after form encoding, where a line break takes
// three bytes and a non-ASCII character at least six. The handler maps that
// (mapNodeConfigError).
//
// opts.Digest is the digest Proxmox is to compare, in Proxmox's own form: it
// goes to assert_if_modified as it is. What a caller of the API sends back is not
// that but a save token, and the handler turns one into the other.
func (c *Client) SetNodeOptions(ctx context.Context, node string, opts NodeOptions) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form, err := buildNodeOptionsForm(opts)
	if err != nil {
		return err
	}
	if err := c.doPut(ctx, "/nodes/"+url.PathEscape(node)+"/config", form, nil); err != nil {
		return fmt.Errorf("set node %s options: %w", node, err)
	}
	return nil
}

// ValidateNodeOptions reports what SetNodeOptions would refuse about opts before
// sending anything: every refusal listed there. It is for a caller that has
// something to do between deciding to write and writing — the handler re-reads
// the node config's digest to check a save token — and wants a refused request to
// cost no Proxmox read. SetNodeOptions runs the same checks itself, so it stays
// the choke point whether a caller asks first or not. A caller that means to
// substitute a digest afterwards should give opts one of the length Proxmox's will
// be (40 hex characters), and not the one it has now: the digest is part of the
// encoded body whose size is checked, and the one it will carry is not the one it
// has now.
func ValidateNodeOptions(opts NodeOptions) error {
	_, err := buildNodeOptionsForm(opts)
	return err
}

// buildNodeOptionsForm is the form SetNodeOptions sends, and the one place that
// decides whether it may be.
func buildNodeOptionsForm(opts NodeOptions) (url.Values, error) {
	form := url.Values{}
	set := make(map[string]bool, len(nodeOptionSettings))
	for _, s := range nodeOptionSettings {
		v, ok := s.value(opts)
		if !ok {
			continue
		}
		set[s.key] = true
		if !utf8.ValidString(v) {
			return nil, fmt.Errorf("%w: %q is not valid UTF-8", ErrInvalidInput, s.key)
		}
		if s.key != "description" && hasLineBreakOrControl(v) {
			return nil, fmt.Errorf("%w: %q cannot contain a line break or a control character", ErrInvalidInput, s.key)
		}
		form.Set(s.key, v)
	}
	if len(set) == 0 && len(opts.Delete) == 0 {
		return nil, fmt.Errorf("%w: nothing to change: set a setting or name one in delete", ErrInvalidInput)
	}
	for _, k := range opts.Delete {
		// The allow-list is the authority; the ACME check below only picks the
		// message. Asking the ACME list first would refuse an ACME key even if
		// one had slipped into deletableNodeOptionKeys, and so hide that
		// corruption from every test that sends one — it is exactly that key
		// this refusal exists to keep out.
		if !deletableNodeOptionKeys[k] {
			if deletableNodeACMEKeys[k] {
				return nil, fmt.Errorf("%w: %q is an ACME setting; clear it through the node's ACME config", ErrInvalidInput, k)
			}
			return nil, fmt.Errorf("%w: %q is not a node option that can be cleared", ErrInvalidInput, k)
		}
		if set[k] {
			return nil, fmt.Errorf("%w: %q cannot be set and cleared in the same request", ErrInvalidInput, k)
		}
	}
	if len(opts.Delete) > 0 {
		form.Set("delete", strings.Join(opts.Delete, ","))
	}
	if opts.Digest != "" {
		form.Set("digest", opts.Digest)
	}
	if err := checkRequestSize(form); err != nil {
		return nil, err
	}
	return form, nil
}

// GetNodeConfigDigest reads the digest of the node's config file as Proxmox
// computes it now — the SHA1 of the whole file, ACME keys and notes included —
// and nothing else: the reply carries the whole file, and everything but the
// digest is dropped as it is decoded. It is empty for an online node that has no
// config file, which answers {} with no digest.
//
// It exists for the save check. The digest this returns is Proxmox's raw one, and
// it is what the PUT's `digest` parameter is compared with by assert_if_modified;
// it must never reach a caller of the API, to whom it would be a way to test
// guesses at the file's content.
func (c *Client) GetNodeConfigDigest(ctx context.Context, node string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	var cfg struct {
		Digest string `json:"digest"`
	}
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/config", &cfg); err != nil {
		return "", fmt.Errorf("get node %s config digest: %w", node, err)
	}
	return cfg.Digest, nil
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
