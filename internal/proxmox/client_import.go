package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// GetStorageContentByType is like GetStorageContent but filters server-side by content
// type via GET /nodes/{node}/storage/{storage}/content?content=<type>. Pass content
// "import" to list the importable OVAs/OVFs on a directory/NFS pool, or the importable
// guests exposed by an ESXi-type storage.
func (c *Client) GetStorageContentByType(ctx context.Context, node, storage, content string) ([]StorageContent, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if storage == "" {
		return nil, fmt.Errorf("storage name is required")
	}
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content"
	if content != "" {
		q := url.Values{}
		q.Set("content", content)
		path += "?" + q.Encode()
	}
	var items []StorageContent
	if err := c.do(ctx, path, &items); err != nil {
		return nil, fmt.Errorf("get %q content on %s/%s: %w", content, node, storage, err)
	}
	return items, nil
}

// GetImportMetadata parses an importable source (OVA/OVF or ESXi VMX) via
// GET /nodes/{node}/storage/{storage}/import-metadata?volume=<volname> and returns the
// guest definition used to pre-fill a create-with-import-from call.
//
// The `volume` parameter must be the storage-RELATIVE volname (e.g. "import/x.ova" or
// "ha-datacenter/ds/VM/VM.vmx"), NOT the full volid ("store:import/x.ova"): the endpoint is
// already scoped to the storage by its URL path, so PVE runs parse_volname on whatever is
// passed and a leading "<storage>:" makes it fail with "unable to parse directory volume
// name". Callers pass the full volid as listed by the content API, so we strip the prefix
// here. It is passed as a query parameter so its ':' and '/' are percent-encoded.
func (c *Client) GetImportMetadata(ctx context.Context, node, storage, volume string) (*ImportMetadata, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if storage == "" {
		return nil, fmt.Errorf("storage name is required")
	}
	if volume == "" {
		return nil, fmt.Errorf("volume is required")
	}
	volname := strings.TrimPrefix(volume, storage+":")
	q := url.Values{}
	q.Set("volume", volname)
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/import-metadata?" + q.Encode()
	var meta ImportMetadata
	if err := c.do(ctx, path, &meta); err != nil {
		return nil, fmt.Errorf("get import metadata for %s on %s/%s: %w", volume, node, storage, err)
	}
	return &meta, nil
}

// GetNextVMID returns the next free guest ID from GET /cluster/nextid. Proxmox returns
// the id as a JSON string in most versions, but a bare number is also accepted.
func (c *Client) GetNextVMID(ctx context.Context) (int, error) {
	var raw json.RawMessage
	if err := c.do(ctx, "/cluster/nextid", &raw); err != nil {
		return 0, fmt.Errorf("get next vmid: %w", err)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		id, convErr := strconv.Atoi(s)
		if convErr != nil {
			return 0, fmt.Errorf("parse next vmid %q: %w", s, convErr)
		}
		return id, nil
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	return 0, fmt.Errorf("unexpected next vmid response: %s", string(raw))
}

// ParsedDisks returns ImportMetadata.Disks decoded into ImportDisk values keyed by bus
// slot (e.g. "scsi0", "efidisk0"). It tolerates both the object form
// ({"volid":..., "size":..., "image-format":...}) and a bare volid string.
func (m *ImportMetadata) ParsedDisks() map[string]ImportDisk {
	out := make(map[string]ImportDisk, len(m.Disks))
	for slot, raw := range m.Disks {
		var d ImportDisk
		if err := json.Unmarshal(raw, &d); err == nil && d.Volid != "" {
			out[slot] = d
			continue
		}
		var volid string
		if err := json.Unmarshal(raw, &volid); err == nil && volid != "" {
			out[slot] = ImportDisk{Volid: volid}
		}
	}
	return out
}

// FlatCreateArgs returns the source guest's create-args with values flattened to plain
// strings, for display in the import wizard and for the import-job detail.
func (m *ImportMetadata) FlatCreateArgs() map[string]string {
	return flattenRawStringMap(m.CreateArgs)
}

// ImportCreateOptions configures BuildImportCreateParams — the target placement and the
// user's choices for a single guest import. The zero value of an override field means "keep
// the value parsed from the source"; a non-zero value replaces it. Adding hardware (extra
// disks/NICs) is intentionally out of scope — only the guest the source defines is created,
// with its settings adjustable.
type ImportCreateOptions struct {
	VMID           int
	Name           string // overrides the metadata name when non-empty
	TargetStorage  string // storage that imported disks are allocated on (required)
	WorkingStorage string // optional import-working-storage (file-based, images content)
	Bridge         string // optional; when set, a net0 is synthesised from the source NIC
	DiskFormat     string // optional target disk format override (qcow2|raw|vmdk)
	StartAfter     bool   // boot the VM once all disks have finished importing
	LiveImport     bool   // boot the VM while disks stream in (live-restore=1) — data-loss on failure

	// Guest-config overrides (empty/zero = keep the source-derived value).
	Cores       int
	Sockets     int
	MemoryMiB   int
	CPUType     string
	OSType      string
	BIOS        string
	Machine     string
	ScsiHW      string
	Pool        string
	Tags        string
	Description string
	OnBoot      *bool
	Agent       *bool
	Numa        *bool

	// Network options applied to the synthesised net0 (only when Bridge is set).
	NetModel   string // overrides the source NIC model (virtio/e1000/vmxnet3/...)
	VLANTag    int    // 802.1q tag; 0 = untagged
	Firewall   *bool  // enable the PVE firewall on the NIC
	MACAddr    string // overrides the source MAC
	RateLimit  string // MB/s cap (PVE "rate"); empty = unlimited
	MTU        int    // 0 = default
	Multiqueue int    // 0 = disabled
}

// BuildImportCreateParams maps a parsed ImportMetadata plus the user's placement choices
// into CreateVMParams for POST /nodes/{node}/qemu. Well-known create-args become typed
// fields; every other create-arg is forwarded verbatim through Extra so the full guest
// definition round-trips (ostype/bios/machine/smbios1/...). Each source disk is emitted
// as "<targetStorage>:0,import-from=<sourceVolid>[,format=...]" on its original bus slot,
// which is what triggers Proxmox's disk conversion/copy.
func BuildImportCreateParams(meta *ImportMetadata, opts ImportCreateOptions) CreateVMParams {
	p := CreateVMParams{VMID: opts.VMID, Extra: map[string]string{}}
	args := flattenRawStringMap(meta.CreateArgs)

	mapped := map[string]bool{
		"name": true, "cores": true, "sockets": true, "memory": true,
		"ostype": true, "bios": true, "scsihw": true, "machine": true,
		"cpu": true, "boot": true,
	}
	p.Name = args["name"]
	p.Cores = atoiSafe(args["cores"])
	p.Sockets = atoiSafe(args["sockets"])
	p.Memory = atoiSafe(args["memory"])
	p.OSType = args["ostype"]
	p.BIOS = args["bios"]
	p.ScsiHW = args["scsihw"]
	p.Machine = args["machine"]
	p.CPUType = args["cpu"]
	p.Boot = args["boot"]

	// Forward remaining create-args verbatim (smbios1, vga, numa, ...). Disk and NIC
	// slots are handled below from the dedicated maps, so skip anything that looks like one.
	for k, v := range args {
		if mapped[k] || v == "" || isGuestSlotKey(k) {
			continue
		}
		p.Extra[k] = v
	}

	if opts.Name != "" {
		p.Name = opts.Name
	}

	// User overrides win over the source-derived values.
	if opts.Cores > 0 {
		p.Cores = opts.Cores
	}
	if opts.Sockets > 0 {
		p.Sockets = opts.Sockets
	}
	if opts.MemoryMiB > 0 {
		p.Memory = opts.MemoryMiB
	}
	if opts.CPUType != "" {
		p.CPUType = opts.CPUType
	}
	if opts.OSType != "" {
		p.OSType = opts.OSType
	}
	if opts.BIOS != "" {
		p.BIOS = opts.BIOS
	}
	if opts.Machine != "" {
		p.Machine = opts.Machine
	}
	if opts.ScsiHW != "" {
		p.ScsiHW = opts.ScsiHW
	}
	if opts.Pool != "" {
		p.Pool = opts.Pool
	}
	if opts.Tags != "" {
		p.Tags = opts.Tags
	}
	if opts.Description != "" {
		p.Description = opts.Description
	}
	if opts.OnBoot != nil {
		p.OnBoot = opts.OnBoot
	}
	if opts.Numa != nil {
		p.Numa = opts.Numa
	}
	if opts.Agent != nil {
		if *opts.Agent {
			p.Agent = "1"
		} else {
			p.Agent = "0"
		}
	}

	for slot, disk := range meta.ParsedDisks() {
		if disk.Volid == "" {
			continue
		}
		spec := opts.TargetStorage + ":0,import-from=" + disk.Volid
		if opts.DiskFormat != "" {
			spec += ",format=" + opts.DiskFormat
		}
		p.Extra[slot] = spec
	}

	// OVMF/UEFI guests need an EFI vars disk to persist their NVRAM boot entries. Source OVAs
	// (e.g. from VMware) don't carry a PVE-compatible one — that's the "efi-state-lost" import
	// warning — so, matching Proxmox's own importer, synthesise a fresh efidisk0 on the target
	// storage when the guest is OVMF and none was imported from the source. Without this the
	// firmware has nowhere to store its boot entry and the guest won't boot. No format= is set
	// so PVE picks the right one per storage (qcow2 on file, raw on Ceph/LVM).
	if p.BIOS == "ovmf" {
		if _, ok := p.Extra["efidisk0"]; !ok {
			p.Extra["efidisk0"] = opts.TargetStorage + ":1,efitype=4m"
		}
	}

	if opts.WorkingStorage != "" {
		p.Extra["import-working-storage"] = opts.WorkingStorage
	}
	if opts.Bridge != "" {
		p.Net0 = buildImportNet(meta, opts)
	}
	// Live import boots the guest while disks stream in, so it already implies start.
	// Emitting both live-restore=1 and start=1 is redundant (and PVE may reject the pair),
	// so the two options are mutually exclusive here — live import wins.
	if opts.LiveImport {
		p.Extra["live-restore"] = "1"
	} else if opts.StartAfter {
		p.Start = true
	}
	return p
}

// buildImportNet synthesises a net0 spec bound to the chosen bridge. The model and MAC
// default to the source's first NIC (preserving them across the migration) but are
// overridable, as are the VLAN tag, firewall, rate limit, MTU and multiqueue — e.g.
// "virtio=AA:BB:..,bridge=vmbr0,tag=10,firewall=1".
func buildImportNet(meta *ImportMetadata, opts ImportCreateOptions) string {
	model := opts.NetModel
	mac := opts.MACAddr
	if model == "" || mac == "" {
		for _, raw := range meta.Net {
			var n struct {
				Model   string `json:"model"`
				Macaddr string `json:"macaddr"`
			}
			if err := json.Unmarshal(raw, &n); err == nil {
				if model == "" && n.Model != "" {
					model = n.Model
				}
				if mac == "" {
					mac = n.Macaddr
				}
			}
			break
		}
	}
	if model == "" {
		model = "virtio"
	}

	spec := model
	if mac != "" {
		spec += "=" + mac
	}
	spec += ",bridge=" + opts.Bridge
	if opts.VLANTag > 0 {
		spec += ",tag=" + strconv.Itoa(opts.VLANTag)
	}
	if opts.Firewall != nil {
		if *opts.Firewall {
			spec += ",firewall=1"
		} else {
			spec += ",firewall=0"
		}
	}
	if opts.RateLimit != "" {
		spec += ",rate=" + opts.RateLimit
	}
	if opts.MTU > 0 {
		spec += ",mtu=" + strconv.Itoa(opts.MTU)
	}
	if opts.Multiqueue > 0 {
		spec += ",queues=" + strconv.Itoa(opts.Multiqueue)
	}
	return spec
}

// isGuestSlotKey reports whether a create-arg key is a disk or NIC slot that this package
// emits from the dedicated Disks/Net maps rather than forwarding verbatim.
func isGuestSlotKey(key string) bool {
	for _, prefix := range []string{"scsi", "virtio", "sata", "ide", "net", "efidisk", "tpmstate"} {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			rest := key[len(prefix):]
			if _, err := strconv.Atoi(rest); err == nil {
				return true
			}
		}
	}
	return false
}

// flattenRawStringMap converts raw JSON create-arg values to plain strings suitable for
// resubmission to POST /qemu (numbers/bools formatted, strings unquoted).
func flattenRawStringMap(m map[string]json.RawMessage) map[string]string {
	out := make(map[string]string, len(m))
	for k, raw := range m {
		out[k] = jsonRawToString(raw)
	}
	return out
}

func jsonRawToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return "0"
	default:
		return string(raw)
	}
}

func atoiSafe(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
