package proxmox

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// response is the standard Proxmox API envelope.
type response struct {
	Data json.RawMessage `json:"data"`
}

// Resource type constants for GET /cluster/resources filtering.
const (
	ResourceTypeNode    = "node"
	ResourceTypeQEMU    = "qemu"
	ResourceTypeLXC     = "lxc"
	ResourceTypeStorage = "storage"
)

// NodeListEntry represents a single node from GET /nodes.
type NodeListEntry struct {
	Node           string  `json:"node"`
	Status         string  `json:"status"`
	CPU            float64 `json:"cpu"`
	MaxCPU         int     `json:"maxcpu"`
	Mem            int64   `json:"mem"`
	MaxMem         int64   `json:"maxmem"`
	Disk           int64   `json:"disk"`
	MaxDisk        int64   `json:"maxdisk"`
	Uptime         int64   `json:"uptime"`
	SSLFingerprint string  `json:"ssl_fingerprint"`
}

// NodeStatus represents the full status of a node from GET /nodes/{node}/status.
type NodeStatus struct {
	Node       string  `json:"node"`
	Uptime     int64   `json:"uptime"`
	Kversion   string  `json:"kversion"`
	PVEVersion string  `json:"pveversion"`
	CPUInfo    CPUInfo `json:"cpuinfo"`
	Memory     Memory  `json:"memory"`
	RootFS     RootFS  `json:"rootfs"`
	CPU        float64 `json:"cpu"`
	Wait       float64 `json:"wait"`
	LoadAvg    []string `json:"loadavg"`
}

// CPUInfo represents CPU information from a node status response.
type CPUInfo struct {
	Cores   int    `json:"cores"`
	CPUs    int    `json:"cpus"`
	MHz     string `json:"mhz"`
	Model   string `json:"model"`
	Sockets int    `json:"sockets"`
	Threads int    `json:"threads"`
}

// Memory represents memory usage from a node status response.
type Memory struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
}

// RootFS represents root filesystem usage from a node status response.
type RootFS struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
	Avail int64 `json:"avail"`
}

// VirtualMachine represents a QEMU VM from GET /nodes/{node}/qemu.
type VirtualMachine struct {
	VMID      int     `json:"vmid"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	Node      string  `json:"node"`
	CPU       float64 `json:"cpu"`
	CPUs      int     `json:"cpus"`
	Mem       int64   `json:"mem"`
	MaxMem    int64   `json:"maxmem"`
	Disk      int64   `json:"disk"`
	MaxDisk   int64   `json:"maxdisk"`
	Uptime    int64   `json:"uptime"`
	NetIn     int64   `json:"netin"`
	NetOut    int64   `json:"netout"`
	DiskRead  int64   `json:"diskread"`
	DiskWrite int64   `json:"diskwrite"`
	PID       int     `json:"pid"`
	Template  int     `json:"template"`
	Tags      string  `json:"tags"`
}

// Container represents an LXC container from GET /nodes/{node}/lxc.
type Container struct {
	VMID      int     `json:"vmid"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	Node      string  `json:"node"`
	CPU       float64 `json:"cpu"`
	CPUs      int     `json:"cpus"`
	Mem       int64   `json:"mem"`
	MaxMem    int64   `json:"maxmem"`
	Disk      int64   `json:"disk"`
	MaxDisk   int64   `json:"maxdisk"`
	Swap      int64   `json:"swap"`
	MaxSwap   int64   `json:"maxswap"`
	Uptime    int64   `json:"uptime"`
	NetIn     int64   `json:"netin"`
	NetOut    int64   `json:"netout"`
	DiskRead  int64   `json:"diskread"`
	DiskWrite int64   `json:"diskwrite"`
	PID       int     `json:"pid"`
	Template  int     `json:"template"`
	Tags      string  `json:"tags"`
}

// ClusterResource represents a resource from GET /cluster/resources.
// This is polymorphic — the Type field determines which fields are populated.
type ClusterResource struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	Node       string  `json:"node"`
	Status     string  `json:"status"`
	Name       string  `json:"name"`
	VMID       int     `json:"vmid,omitempty"`
	CPU        float64 `json:"cpu,omitempty"`
	MaxCPU     int     `json:"maxcpu,omitempty"`
	Mem        int64   `json:"mem,omitempty"`
	MaxMem     int64   `json:"maxmem,omitempty"`
	Disk       int64   `json:"disk,omitempty"`
	MaxDisk    int64   `json:"maxdisk,omitempty"`
	Uptime     int64   `json:"uptime,omitempty"`
	Template   int     `json:"template,omitempty"`
	HAState    string  `json:"hastate,omitempty"`
	Pool       string  `json:"pool,omitempty"`
	Storage    string  `json:"storage,omitempty"`
	PluginType string  `json:"plugintype,omitempty"`
}

// StoragePool represents a storage pool from GET /nodes/{node}/storage.
type StoragePool struct {
	Storage    string `json:"storage"`
	Type       string `json:"type"`
	Content    string `json:"content"`
	Active     int    `json:"active"`
	Enabled    int    `json:"enabled"`
	Shared     int    `json:"shared"`
	Total      int64  `json:"total"`
	Used       int64  `json:"used"`
	Avail      int64  `json:"avail"`
	UsedFrac   float64 `json:"used_fraction"`
}

// FlexInt handles Proxmox fields that may be returned as either a JSON number
// or a quoted string (e.g. port: 5900 vs port: "5900").
type FlexInt int

func (fi *FlexInt) UnmarshalJSON(b []byte) error {
	// Try number first.
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*fi = FlexInt(n)
		return nil
	}
	// Try string.
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		parsed, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("FlexInt: cannot parse %q as int: %w", s, err)
		}
		*fi = FlexInt(parsed)
		return nil
	}
	return fmt.Errorf("FlexInt: cannot unmarshal %s", string(b))
}

// TermProxyResponse holds the response from a termproxy or vncproxy POST request.
type TermProxyResponse struct {
	Port     FlexInt `json:"port"`
	Ticket   string  `json:"ticket"`
	UPID     string  `json:"upid"`
	User     string  `json:"user"`
	Password string  `json:"password,omitempty"`
}

// CloneParams holds parameters for a VM clone operation.
type CloneParams struct {
	NewID   int    `json:"newid"`
	Name    string `json:"name,omitempty"`
	Target  string `json:"target,omitempty"`
	Full    bool   `json:"full,omitempty"`
	Storage string `json:"storage,omitempty"`
}

// MigrateParams holds parameters for a CT/VM migration operation.
type MigrateParams struct {
	Target string `json:"target"`
	Online bool   `json:"online,omitempty"`
}

// TaskStatus represents the status of an async Proxmox task.
type TaskStatus struct {
	Status     string `json:"status"`
	ExitStatus string `json:"exitstatus"`
	Type       string `json:"type"`
	UPID       string `json:"upid"`
	Node       string `json:"node"`
	PID        int    `json:"pid"`
	StartTime  int64  `json:"starttime"`
}

// TaskLogEntry represents a single line from the Proxmox task log.
type TaskLogEntry struct {
	N int    `json:"n"`
	T string `json:"t"`
}

// StorageContent represents an item from GET /nodes/{node}/storage/{storage}/content.
type StorageContent struct {
	Volid   string `json:"volid"`
	Format  string `json:"format"`
	Size    int64  `json:"size"`
	CTime   int64  `json:"ctime"`
	Content string `json:"content"`
	VMID    int    `json:"vmid,omitempty"`
}

// DiskResizeParams holds parameters for a VM disk resize operation.
type DiskResizeParams struct {
	Disk string `json:"disk"`
	Size string `json:"size"`
}

// DiskMoveParams holds parameters for a VM disk move operation.
type DiskMoveParams struct {
	Disk    string `json:"disk"`
	Storage string `json:"storage"`
	Delete  bool   `json:"delete,omitempty"`
}

// CephStatus represents the cluster-wide Ceph status from GET /nodes/{node}/ceph/status.
type CephStatus struct {
	Health  CephHealth  `json:"health"`
	PGMap   CephPGMap   `json:"pgmap"`
	OSDMap  CephOSDMap  `json:"osdmap"`
	MonMap  CephMonMap  `json:"monmap"`
	Quorum  []int       `json:"quorum,omitempty"`
}

// CephHealth represents the Ceph health summary.
type CephHealth struct {
	Status string `json:"status"`
}

// CephPGMap represents Ceph placement group statistics.
type CephPGMap struct {
	BytesUsed    int64   `json:"bytes_used"`
	BytesAvail   int64   `json:"bytes_avail"`
	BytesTotal   int64   `json:"bytes_total"`
	ReadBytesSec int64   `json:"read_bytes_sec"`
	WritBytesSec int64   `json:"write_bytes_sec"`
	ReadOpPerSec int64   `json:"read_op_per_sec"`
	WritOpPerSec int64   `json:"write_op_per_sec"`
	PGsPerState  []PGStateCount `json:"pgs_by_state,omitempty"`
	NumPGs       int     `json:"num_pgs"`
	DataBytes    int64   `json:"data_bytes"`
	UsedFraction float64 `json:"used_pct,omitempty"`
}

// PGStateCount represents PG counts by state.
type PGStateCount struct {
	StateName string `json:"state_name"`
	Count     int    `json:"count"`
}

// CephOSDMap represents Ceph OSD map summary.
type CephOSDMap struct {
	Full      bool `json:"full"`
	NearFull  bool `json:"nearfull"`
	NumOSDs   int  `json:"num_osds"`
	NumUpOSDs int  `json:"num_up_osds"`
	NumInOSDs int  `json:"num_in_osds"`
}

// CephMonMap represents Ceph monitor map summary.
type CephMonMap struct {
	NumMons int `json:"num_mons"`
}

// CephOSD represents a single OSD from GET /nodes/{node}/ceph/osd.
type CephOSD struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Host string `json:"host,omitempty"`
	In   int    `json:"in"`
	Up   int    `json:"up"`
	// DevicePath contains the block device path.
	DevicePath string  `json:"device_path,omitempty"`
	Status     string  `json:"status,omitempty"`
	CrushWeight float64 `json:"crush_weight,omitempty"`
}

// CephOSDTreeNode represents a node in the OSD tree from the Proxmox response.
type CephOSDTreeNode struct {
	ID       int               `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Status   string            `json:"status,omitempty"`
	Host     string            `json:"host,omitempty"`
	Children []CephOSDTreeNode `json:"children,omitempty"`
	CrushWeight float64        `json:"crush_weight,omitempty"`
}

// CephOSDResponse wraps the response from GET /nodes/{node}/ceph/osd.
type CephOSDResponse struct {
	Root CephOSDTreeNode `json:"root"`
}

// CephPool represents a Ceph pool from GET /nodes/{node}/ceph/pools.
type CephPool struct {
	PoolName     string  `json:"pool_name"`
	Pool         int     `json:"pool"`
	Size         int     `json:"size"`
	MinSize      int     `json:"min_size"`
	PGNum        int     `json:"pg_num"`
	PGAutoScale  string  `json:"pg_autoscale_mode,omitempty"`
	CrushRule    int     `json:"crush_rule"`
	Type         string  `json:"type,omitempty"`
	BytesUsed    int64   `json:"bytes_used"`
	PercentUsed  float64 `json:"percent_used"`
	ReadBytesSec int64   `json:"read_bytes_sec,omitempty"`
	WritBytesSec int64   `json:"write_bytes_sec,omitempty"`
	ReadOpPerSec int64   `json:"read_op_per_sec,omitempty"`
	WritOpPerSec int64   `json:"write_op_per_sec,omitempty"`
}

// CephPoolCreateParams holds parameters for creating a new Ceph pool.
type CephPoolCreateParams struct {
	Name         string `json:"name"`
	Size         int    `json:"size"`
	MinSize      int    `json:"min_size,omitempty"`
	PGNum        int    `json:"pg_num"`
	Application  string `json:"application,omitempty"`
	CrushRule    string `json:"crush_rule_name,omitempty"`
	PGAutoScale  string `json:"pg_autoscale_mode,omitempty"`
}

// CephFS represents a CephFS filesystem from GET /nodes/{node}/ceph/fs.
type CephFS struct {
	Name       string `json:"name"`
	MetaPool   string `json:"metadata_pool"`
	DataPool   string `json:"data_pool"`
}

// CephCrushRule represents a CRUSH rule from GET /nodes/{node}/ceph/rules.
type CephCrushRule struct {
	RuleID   int    `json:"rule_id"`
	RuleName string `json:"rule_name"`
	Type     int    `json:"type"`
	MinSize  int    `json:"min_size"`
	MaxSize  int    `json:"max_size"`
}

// CephMon represents a Ceph monitor from GET /nodes/{node}/ceph/mon.
type CephMon struct {
	Name    string `json:"name"`
	Addr    string `json:"addr,omitempty"`
	Host    string `json:"host,omitempty"`
	Rank    int    `json:"rank,omitempty"`
	Quorum  bool   `json:"quorum,omitempty"`
}

// ClusterStatusEntry represents an entry from GET /cluster/status.
type ClusterStatusEntry struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	IP      string `json:"ip,omitempty"`
	Level   string `json:"level,omitempty"`
	Local   int    `json:"local,omitempty"`
	NodeID  int    `json:"nodeid,omitempty"`
	Online  int    `json:"online,omitempty"`
	Version int    `json:"version,omitempty"`
	Quorate int    `json:"quorate,omitempty"`
	Nodes   int    `json:"nodes,omitempty"`
}

// Snapshot represents a snapshot from GET /nodes/{node}/qemu|lxc/{vmid}/snapshot.
type Snapshot struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SnapTime    int64  `json:"snaptime,omitempty"`
	VMState     int    `json:"vmstate,omitempty"`
	Parent      string `json:"parent,omitempty"`
}

// SnapshotParams holds parameters for creating a snapshot.
type SnapshotParams struct {
	SnapName    string `json:"snapname"`
	Description string `json:"description,omitempty"`
	VMState     bool   `json:"vmstate,omitempty"`
}

// CreateVMParams holds parameters for creating a QEMU VM.
type CreateVMParams struct {
	VMID    int    `json:"vmid"`
	Name    string `json:"name,omitempty"`
	Memory  int    `json:"memory,omitempty"`
	Cores   int    `json:"cores,omitempty"`
	Sockets int    `json:"sockets,omitempty"`
	SCSI0   string `json:"scsi0,omitempty"`
	IDE2    string `json:"ide2,omitempty"`
	Net0    string `json:"net0,omitempty"`
	OSType  string `json:"ostype,omitempty"`
	Boot    string `json:"boot,omitempty"`
	CDRom   string `json:"cdrom,omitempty"`
	Start   bool   `json:"start,omitempty"`
	// Cloud-init fields
	CIUser     string `json:"ciuser,omitempty"`
	CIPassword string `json:"cipassword,omitempty"`
	IPConfig0  string `json:"ipconfig0,omitempty"`
	SSHKeys    string `json:"sshkeys,omitempty"`
	CIType     string `json:"citype,omitempty"`
}

// CreateCTParams holds parameters for creating an LXC container.
type CreateCTParams struct {
	VMID         int    `json:"vmid"`
	Hostname     string `json:"hostname,omitempty"`
	OSTemplate   string `json:"ostemplate"`
	Storage      string `json:"storage,omitempty"`
	RootFS       string `json:"rootfs,omitempty"`
	Memory       int    `json:"memory,omitempty"`
	Swap         int    `json:"swap,omitempty"`
	Cores        int    `json:"cores,omitempty"`
	Net0         string `json:"net0,omitempty"`
	Password     string `json:"password,omitempty"`
	SSHKeys      string `json:"ssh-public-keys,omitempty"`
	Unprivileged bool   `json:"unprivileged,omitempty"`
	Start        bool   `json:"start,omitempty"`
}

// VMConfig represents the full configuration of a QEMU VM from GET /nodes/{node}/qemu/{vmid}/config.
type VMConfig map[string]interface{}
