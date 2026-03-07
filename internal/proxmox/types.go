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
	Flags   string `json:"flags"`
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
	QMPStatus string  `json:"qmpstatus"`
	Lock      string  `json:"lock"`
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

// EffectiveStatus returns the real VM status by checking qmpstatus.
// Proxmox reports status="running" + qmpstatus="paused" for suspended VMs.
func (vm VirtualMachine) EffectiveStatus() string {
	if vm.Status == "running" && vm.QMPStatus == "paused" {
		return "suspended"
	}
	return vm.Status
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
			// Non-numeric string (e.g. CRUSH tree node names) — default to 0.
			*fi = 0
			return nil
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

// StorageConfig represents the full configuration of a storage pool from GET /storage/{storage}.
// Fields are optional because they vary by storage type.
// Some fields are shared across types (e.g. "pool" is used by both zfspool and rbd).
type StorageConfig struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	Nodes   string `json:"nodes,omitempty"`
	Disable int    `json:"disable,omitempty"`
	Shared  int    `json:"shared,omitempty"`
	Digest  string `json:"digest,omitempty"`

	// dir / btrfs
	Path         string `json:"path,omitempty"`
	Mkdir        int    `json:"mkdir,omitempty"`
	IsMountpoint string `json:"is_mountpoint,omitempty"`

	// nfs / cifs / glusterfs / pbs
	Server  string `json:"server,omitempty"`
	Export  string `json:"export,omitempty"`
	Options string `json:"options,omitempty"`

	// cifs/smb
	Share      string `json:"share,omitempty"`
	Username   string `json:"username,omitempty"` // also used by rbd/cephfs/pbs
	Domain     string `json:"domain,omitempty"`
	SMBVersion string `json:"smbversion,omitempty"`
	Password   string `json:"password,omitempty"` // cifs / pbs

	// lvm / lvmthin
	VGName     string `json:"vgname,omitempty"`
	BaseVolume string `json:"base,omitempty"`
	SafeRemove int    `json:"saferemove,omitempty"`
	ThinPool   string `json:"thinpool,omitempty"`

	// zfspool / rbd / cephfs
	Pool      string `json:"pool,omitempty"`
	BlockSize string `json:"blocksize,omitempty"`
	Sparse    int    `json:"sparse,omitempty"`

	// iscsi
	Portal string `json:"portal,omitempty"`
	Target string `json:"target,omitempty"`

	// cephfs / rbd
	MonHost   string `json:"monhost,omitempty"`
	KRBD      int    `json:"krbd,omitempty"`
	Fuse      int    `json:"fuse,omitempty"`
	Subdir    string `json:"subdir,omitempty"`
	FSName    string `json:"fs-name,omitempty"`
	Keyring   string `json:"keyring,omitempty"`
	Namespace string `json:"namespace,omitempty"`

	// glusterfs
	Server2   string `json:"server2,omitempty"`
	Volume    string `json:"volume,omitempty"`
	Transport string `json:"transport,omitempty"`

	// pbs
	Datastore       string `json:"datastore,omitempty"`
	FingerprintPBS  string `json:"fingerprint,omitempty"`
	EncryptionKey   string `json:"encryption-key,omitempty"`
	MasterPubkey    string `json:"master-pubkey,omitempty"`

	// common optional
	Preallocation string `json:"preallocation,omitempty"`
	Format        string `json:"format,omitempty"`
	MaxFiles      int    `json:"maxfiles,omitempty"`
	PruneBackups  string `json:"prune-backups,omitempty"`
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

// MachineType represents a QEMU machine type from GET /nodes/{node}/capabilities/qemu/machines.
type MachineType struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// ResourcePool represents a resource pool from GET /pools.
type ResourcePool struct {
	PoolID  string `json:"poolid"`
	Comment string `json:"comment,omitempty"`
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

// CTVolumeMoveParams holds parameters for an LXC container volume move.
type CTVolumeMoveParams struct {
	Volume  string `json:"volume"`
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
// Proxmox may return num_mons at the top level or nested under a "monmap" sub-object.
type CephMonMap struct {
	NumMons   int              `json:"num_mons"`
	Mons      []json.RawMessage `json:"mons,omitempty"`
	SubMonMap *cephSubMonMap   `json:"monmap,omitempty"`
}

type cephSubMonMap struct {
	NumMons int               `json:"num_mons"`
	Mons    []json.RawMessage `json:"mons,omitempty"`
}

// MonCount returns the number of monitors, checking multiple locations.
func (m *CephMonMap) MonCount() int {
	if m.NumMons > 0 {
		return m.NumMons
	}
	if m.SubMonMap != nil && m.SubMonMap.NumMons > 0 {
		return m.SubMonMap.NumMons
	}
	if len(m.Mons) > 0 {
		return len(m.Mons)
	}
	if m.SubMonMap != nil && len(m.SubMonMap.Mons) > 0 {
		return len(m.SubMonMap.Mons)
	}
	return 0
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
// The "id" field is json.Number because Proxmox returns negative integers for
// non-OSD nodes (root, host, rack) and positive integers for OSDs, but some
// versions return them as strings.
type CephOSDTreeNode struct {
	ID       FlexInt           `json:"id"`
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

// CephPool represents a Ceph pool from GET /nodes/{node}/ceph/pool.
// Many fields use FlexInt because some Proxmox versions return them as strings.
type CephPool struct {
	PoolName     string  `json:"pool_name"`
	Pool         FlexInt `json:"pool"`
	Size         FlexInt `json:"size"`
	MinSize      FlexInt `json:"min_size"`
	PGNum        FlexInt `json:"pg_num"`
	PGAutoScale  string  `json:"pg_autoscale_mode,omitempty"`
	CrushRule    FlexInt `json:"crush_rule"`
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
// Uses json.RawMessage for initial parse to handle varying response formats.
type CephMon struct {
	Name    string  `json:"name"`
	Addr    string  `json:"addr,omitempty"`
	Host    string  `json:"host,omitempty"`
	Rank    FlexInt `json:"rank,omitempty"`
	Quorum  bool    `json:"quorum,omitempty"`
}

// HAResource represents an HA-managed resource from GET /cluster/ha/resources.
type HAResource struct {
	SID         string `json:"sid"`          // "vm:101" or "ct:200"
	Type        string `json:"type"`         // "vm" or "ct"
	State       string `json:"state"`        // "started", "stopped", "enabled", etc.
	Group       string `json:"group"`        // HA group name (may be empty)
	Status      string `json:"status"`
	MaxRelocate int    `json:"max_relocate"`
}

// HAGroup represents an HA group from GET /cluster/ha/groups.
type HAGroup struct {
	Group      string `json:"group"`      // group name
	Nodes      string `json:"nodes"`      // "node1:100,node2:50" or "node1,node2"
	Restricted int    `json:"restricted"` // 1 = VMs can ONLY run on group nodes
	NoFailback int    `json:"nofailback"`
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

	// Cloud-init fields
	CIUser       string `json:"ciuser,omitempty"`
	CIPassword   string `json:"cipassword,omitempty"`
	IPConfig0    string `json:"ipconfig0,omitempty"`
	SSHKeys      string `json:"sshkeys,omitempty"`
	CIType       string `json:"citype,omitempty"`
	Nameserver   string `json:"nameserver,omitempty"`
	Searchdomain string `json:"searchdomain,omitempty"`

	// Description / Tags / Pool
	Description string `json:"description,omitempty"`
	Tags        string `json:"tags,omitempty"`
	Pool        string `json:"pool,omitempty"`

	// Extra allows arbitrary additional Proxmox config fields (e.g. scsi1, ide0, sata0).
	Extra map[string]string `json:"extra,omitempty"`
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

	// Description / Tags / Pool / DNS
	Description  string `json:"description,omitempty"`
	Tags         string `json:"tags,omitempty"`
	Pool         string `json:"pool,omitempty"`
	Nameserver   string `json:"nameserver,omitempty"`
	Searchdomain string `json:"searchdomain,omitempty"`

	// Extra allows arbitrary additional Proxmox LXC parameters (features, cpulimit, arch, etc.).
	Extra map[string]string `json:"extra,omitempty"`
}

// NetworkInterface represents a network interface from GET /nodes/{node}/network.
type NetworkInterface struct {
	Iface     string `json:"iface"`
	Type      string `json:"type"`
	Active    int    `json:"active"`
	Autostart int    `json:"autostart"`
	Method    string `json:"method,omitempty"`
	Method6   string `json:"method6,omitempty"`
	Address   string `json:"address,omitempty"`
	Netmask   string `json:"netmask,omitempty"`
	Gateway   string `json:"gateway,omitempty"`
	CIDR      string `json:"cidr,omitempty"`
	BridgePorts string `json:"bridge_ports,omitempty"`
	BridgeSTP   string `json:"bridge_stp,omitempty"`
	BridgeFD    string `json:"bridge_fd,omitempty"`
	Comments  string `json:"comments,omitempty"`
}

// DiskAttachParams holds parameters for attaching a new disk to a VM.
type DiskAttachParams struct {
	Bus     string `json:"bus"`     // "scsi", "sata", "virtio", "ide"
	Index   int    `json:"index"`   // 0, 1, 2...
	Storage string `json:"storage"` // storage pool
	Size    string `json:"size"`    // "20G"
	Format  string `json:"format"`  // "raw", "qcow2" (optional)
}

// VMConfig represents the full configuration of a QEMU VM from GET /nodes/{node}/qemu/{vmid}/config.
type VMConfig map[string]interface{}

// TargetEndpoint describes a remote Proxmox API endpoint for cross-cluster migration.
type TargetEndpoint struct {
	Host        string `json:"host"`
	APIToken    string `json:"apitoken"`
	Fingerprint string `json:"fingerprint"`
}

// String formats the endpoint as a Proxmox property string:
// apitoken=PVEAPIToken=user@realm!token=SECRET,host=ADDRESS[,fingerprint=HEX]
func (e TargetEndpoint) String() string {
	s := "apitoken=PVEAPIToken=" + e.APIToken + ",host=" + e.Host
	if e.Fingerprint != "" {
		s += ",fingerprint=" + e.Fingerprint
	}
	return s
}

// RemoteMigrateVMParams holds parameters for cross-cluster VM migration via remote_migrate.
type RemoteMigrateVMParams struct {
	TargetBridge  string         `json:"target-bridge,omitempty"`
	TargetStorage string         `json:"target-storage,omitempty"`
	TargetVMID    int            `json:"target-vmid,omitempty"`
	TargetEndpoint TargetEndpoint `json:"target-endpoint"`
	BWLimit       int            `json:"bwlimit,omitempty"`
	Online        bool           `json:"online,omitempty"`
	Delete        bool           `json:"delete,omitempty"`
}

// RemoteMigrateCTParams holds parameters for cross-cluster container migration via remote_migrate.
type RemoteMigrateCTParams struct {
	TargetBridge  string         `json:"target-bridge,omitempty"`
	TargetStorage string         `json:"target-storage,omitempty"`
	TargetVMID    int            `json:"target-vmid,omitempty"`
	TargetEndpoint TargetEndpoint `json:"target-endpoint"`
	BWLimit       int            `json:"bwlimit,omitempty"`
	Restart       bool           `json:"restart,omitempty"`
	Delete        bool           `json:"delete,omitempty"`
}

// GuestOSInfo represents the OS information returned by the QEMU guest agent.
type GuestOSInfo struct {
	Name          string `json:"name"`
	KernelVersion string `json:"kernel-version"`
	KernelRelease string `json:"kernel-release"`
	Machine       string `json:"machine"`
	ID            string `json:"id"`
	PrettyName    string `json:"pretty-name"`
	Version       string `json:"version"`
	VersionID     string `json:"version-id"`
}

// GuestIPAddress represents a single IP address on a guest network interface.
type GuestIPAddress struct {
	IPAddress     string `json:"ip-address"`
	IPAddressType string `json:"ip-address-type"`
	Prefix        int    `json:"prefix"`
}

// GuestNetworkInterface represents a network interface reported by the QEMU guest agent.
type GuestNetworkInterface struct {
	Name            string           `json:"name"`
	HardwareAddress string           `json:"hardware-address"`
	IPAddresses     []GuestIPAddress `json:"ip-addresses"`
}

// FirewallRule represents a firewall rule from the Proxmox API.
type FirewallRule struct {
	Pos     int    `json:"pos"`
	Type    string `json:"type"`
	Action  string `json:"action"`
	Source  string `json:"source,omitempty"`
	Dest    string `json:"dest,omitempty"`
	Sport   string `json:"sport,omitempty"`
	Dport   string `json:"dport,omitempty"`
	Proto   string `json:"proto,omitempty"`
	Enable  int    `json:"enable"`
	Comment string `json:"comment,omitempty"`
	Macro   string `json:"macro,omitempty"`
	Log     string `json:"log,omitempty"`
	Iface   string `json:"iface,omitempty"`
}

// FirewallRuleParams holds parameters for creating/updating a firewall rule.
type FirewallRuleParams struct {
	Type    string `json:"type"`
	Action  string `json:"action"`
	Source  string `json:"source,omitempty"`
	Dest    string `json:"dest,omitempty"`
	Sport   string `json:"sport,omitempty"`
	Dport   string `json:"dport,omitempty"`
	Proto   string `json:"proto,omitempty"`
	Enable  int    `json:"enable"`
	Comment string `json:"comment,omitempty"`
	Macro   string `json:"macro,omitempty"`
	Log     string `json:"log,omitempty"`
	Iface   string `json:"iface,omitempty"`
}

// FirewallOptions represents firewall options for cluster/node/VM.
type FirewallOptions struct {
	Enable     *int   `json:"enable,omitempty"`
	PolicyIn   string `json:"policy_in,omitempty"`
	PolicyOut  string `json:"policy_out,omitempty"`
	LogLevelIn string `json:"log_level_in,omitempty"`
	LogLevelOut string `json:"log_level_out,omitempty"`
}

// SDNZone represents an SDN zone from GET /cluster/sdn/zones.
type SDNZone struct {
	Zone         string `json:"zone"`
	Type         string `json:"type"`
	Nodes        string `json:"nodes,omitempty"`
	IPAM         string `json:"ipam,omitempty"`
	DNS          string `json:"dns,omitempty"`
	ReverseDNS   string `json:"reversedns,omitempty"`
	DNSZone      string `json:"dnszone,omitempty"`
	Bridge       string `json:"bridge,omitempty"`
	Tag          int    `json:"tag,omitempty"`
	VLANProtocol string `json:"vlan-protocol,omitempty"`
	Peers        string `json:"peers,omitempty"`
	MTU          int    `json:"mtu,omitempty"`
}

// SDNVNet represents an SDN VNet from GET /cluster/sdn/vnets.
type SDNVNet struct {
	VNet      string `json:"vnet"`
	Zone      string `json:"zone"`
	Tag       int    `json:"tag,omitempty"`
	Alias     string `json:"alias,omitempty"`
	VLANAware int    `json:"vlanaware,omitempty"`
}

// SDNSubnet represents an SDN subnet from GET /cluster/sdn/vnets/{vnet}/subnets.
type SDNSubnet struct {
	Subnet  string `json:"subnet"`
	Type    string `json:"type,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	SNAT    int    `json:"snat,omitempty"`
	VNet    string `json:"vnet,omitempty"`
}

// CreateSDNZoneParams holds parameters for creating an SDN zone.
type CreateSDNZoneParams struct {
	Zone         string `json:"zone"`
	Type         string `json:"type"`
	Bridge       string `json:"bridge,omitempty"`
	Tag          int    `json:"tag,omitempty"`
	VLANProtocol string `json:"vlan-protocol,omitempty"`
	Peers        string `json:"peers,omitempty"`
	MTU          int    `json:"mtu,omitempty"`
	Nodes        string `json:"nodes,omitempty"`
	IPAM         string `json:"ipam,omitempty"`
}

// UpdateSDNZoneParams holds parameters for updating an SDN zone.
type UpdateSDNZoneParams struct {
	Bridge       string `json:"bridge,omitempty"`
	Tag          int    `json:"tag,omitempty"`
	VLANProtocol string `json:"vlan-protocol,omitempty"`
	Peers        string `json:"peers,omitempty"`
	MTU          int    `json:"mtu,omitempty"`
	Nodes        string `json:"nodes,omitempty"`
	IPAM         string `json:"ipam,omitempty"`
}

// CreateSDNVNetParams holds parameters for creating an SDN VNet.
type CreateSDNVNetParams struct {
	VNet      string `json:"vnet"`
	Zone      string `json:"zone"`
	Tag       int    `json:"tag,omitempty"`
	Alias     string `json:"alias,omitempty"`
	VLANAware int    `json:"vlanaware,omitempty"`
}

// UpdateSDNVNetParams holds parameters for updating an SDN VNet.
type UpdateSDNVNetParams struct {
	Zone      string `json:"zone,omitempty"`
	Tag       int    `json:"tag,omitempty"`
	Alias     string `json:"alias,omitempty"`
	VLANAware int    `json:"vlanaware,omitempty"`
}

// CreateSDNSubnetParams holds parameters for creating an SDN subnet.
type CreateSDNSubnetParams struct {
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway,omitempty"`
	SNAT    int    `json:"snat,omitempty"`
	Type    string `json:"type,omitempty"`
}

// UpdateSDNSubnetParams holds parameters for updating an SDN subnet.
type UpdateSDNSubnetParams struct {
	Gateway string `json:"gateway,omitempty"`
	SNAT    int    `json:"snat,omitempty"`
}

// HARuleEntry represents an HA rule from GET /cluster/ha/rules (PVE 9+).
type HARuleEntry struct {
	Rule      string `json:"rule"`
	Type      string `json:"type"`      // "node-affinity" or "resource-affinity"
	Resources string `json:"resources"` // "vm:100,ct:101"
	Nodes     string `json:"nodes"`     // node-affinity only
	Strict    int    `json:"strict"`
	Affinity  string `json:"affinity"` // resource-affinity: "positive" or "negative"
	Comment   string `json:"comment"`
	Disable   int    `json:"disable"`
}

// CreateHARuleParams holds parameters for creating an HA rule via POST /cluster/ha/rules/{type}.
type CreateHARuleParams struct {
	Rule      string `json:"rule"`
	Resources string `json:"resources"`
	Nodes     string `json:"nodes,omitempty"`
	Strict    int    `json:"strict,omitempty"`
	Affinity  string `json:"affinity,omitempty"`
	Comment   string `json:"comment,omitempty"`
}

// CreateNetworkInterfaceParams holds parameters for creating a network interface.
type CreateNetworkInterfaceParams struct {
	Iface       string `json:"iface"`
	Type        string `json:"type"`
	Address     string `json:"address,omitempty"`
	Netmask     string `json:"netmask,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
	CIDR        string `json:"cidr,omitempty"`
	Autostart   int    `json:"autostart,omitempty"`
	BridgePorts string `json:"bridge_ports,omitempty"`
	BridgeSTP   string `json:"bridge_stp,omitempty"`
	BridgeFD    string `json:"bridge_fd,omitempty"`
	Comments    string `json:"comments,omitempty"`
	Method      string `json:"method,omitempty"`
	Method6     string `json:"method6,omitempty"`
}

// UpdateNetworkInterfaceParams holds parameters for updating a network interface.
type UpdateNetworkInterfaceParams struct {
	Type        string `json:"type"`
	Address     string `json:"address,omitempty"`
	Netmask     string `json:"netmask,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
	CIDR        string `json:"cidr,omitempty"`
	Autostart   int    `json:"autostart,omitempty"`
	BridgePorts string `json:"bridge_ports,omitempty"`
	BridgeSTP   string `json:"bridge_stp,omitempty"`
	BridgeFD    string `json:"bridge_fd,omitempty"`
	Comments    string `json:"comments,omitempty"`
	Method      string `json:"method,omitempty"`
	Method6     string `json:"method6,omitempty"`
}

// BackupParams holds parameters for triggering a vzdump backup.
type BackupParams struct {
	VMID     string `json:"vmid"`
	Storage  string `json:"storage,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Compress string `json:"compress,omitempty"`
}

// BackupJob represents a cluster-level vzdump backup job schedule.
type BackupJob struct {
	ID               string `json:"id"`
	Enabled          int    `json:"enabled,omitempty"`
	Type             string `json:"type,omitempty"`
	Schedule         string `json:"schedule,omitempty"`
	Storage          string `json:"storage,omitempty"`
	Node             string `json:"node,omitempty"`
	VMID             string `json:"vmid,omitempty"`
	Mode             string `json:"mode,omitempty"`
	Compress         string `json:"compress,omitempty"`
	MailNotification string `json:"mailnotification,omitempty"`
	MailTo           string `json:"mailto,omitempty"`
	NextRun          int64  `json:"next-run,omitempty"`
	Comment          string `json:"comment,omitempty"`
}

// BackupJobParams holds parameters for creating or updating a backup job.
type BackupJobParams struct {
	Enabled          *int   `json:"enabled,omitempty"`
	Type             string `json:"type,omitempty"`
	Schedule         string `json:"schedule,omitempty"`
	Storage          string `json:"storage,omitempty"`
	Node             string `json:"node,omitempty"`
	VMID             string `json:"vmid,omitempty"`
	Mode             string `json:"mode,omitempty"`
	Compress         string `json:"compress,omitempty"`
	MailNotification string `json:"mailnotification,omitempty"`
	MailTo           string `json:"mailto,omitempty"`
	Comment          string `json:"comment,omitempty"`
}
