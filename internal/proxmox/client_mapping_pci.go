package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// PCI resource mappings, /cluster/mapping/pci: the PCI counterpart of the USB
// ones (client_mapping.go), there for the same reason. qemu-server's
// check_hostpci_perm (src/PVE/API2/Qemu.pm) returns early for the literal user
// root@pam and otherwise dies "only root can set 'hostpciN' config for
// non-mapped devices" for any hostpciN naming a host device — on the value
// being written and on the one it replaces or deletes — and no API token is
// root@pam. A hostpciN of mapping=<id> is checked against Mapping.Use on
// /mapping/pci/<id> instead. A romfile= stays root@pam's either way.

// pciMappingPathRe is one $PCI_RE of pve-guest-common src/PVE/Mapping/PCI.pm,
// "[a-f0-9]{4,}:[a-f0-9]{2}:[a-f0-9]{2}(?:\.[a-f0-9])?": domain:bus:slot, and
// the function when one function is meant — without it, the whole device, all
// its functions passed through as one. Lowercase hex, the domain required.
// The entry's schema admits a ";"-joined list of these; Nexara writes one
// (PCIMapEntryForDevice says why).
var pciMappingPathRe = regexp.MustCompile(`^[a-f0-9]{4,}:[a-f0-9]{2}:[a-f0-9]{2}(\.[a-f0-9])?$`)

// pciMappingIDRe is $map_fmt's pattern for both id and subsystem-id, the
// same as a USB device id's.
var pciMappingIDRe = regexp.MustCompile(`^[0-9A-Fa-f]{4}:[0-9A-Fa-f]{4}$`)

// pciMappingPathMax bounds a path. The bound is Nexara's — Proxmox's pattern
// has none, its domain being "four hex digits or more" — and refuses nothing
// real: a domain is 16 bits, 32 in the widest kernels, so a whole path is at
// most 16 characters.
const pciMappingPathMax = 64

// ListPCIMappings lists the cluster's PCI mappings, each checked against
// checkNode when it is set (listMappings); the checks are in Checks.
func (c *Client) ListPCIMappings(ctx context.Context, checkNode string) ([]PCIMapping, error) {
	var mappings []PCIMapping
	if err := c.listMappings(ctx, "pci", "PCI", checkNode, &mappings); err != nil {
		return nil, err
	}
	for i := range mappings {
		if mappings[i].Map == nil {
			mappings[i].Map = []string{}
		}
		if mappings[i].Checks == nil {
			mappings[i].Checks = []MappingCheck{}
		}
	}
	return mappings, nil
}

// ListNodePCIDevicesAllClasses lists every PCI device on node: bridges,
// memory controllers and processors too, which GET /nodes/{node}/hardware/pci
// leaves out unless told otherwise (its pci-class-blacklist defaults to
// "05;06;0b", pve-manager PVE/API2/Hardware/PCI.pm). Proxmox's own mapping
// editor reads it this way, with the blacklist emptied.
//
// The node is held to pve-node, not only to validateNodeName's path-segment
// guard: pveproxy picks the host to proxy a /nodes/{node} call to from the
// name BEFORE it validates the parameter, and resolves a name that is no
// member — an IP, a dotted hostname — on its own, so a looser name would have
// the node open a connection wherever the caller pointed it. Proxmox checks
// the parameter with the same format, so this refuses nothing it would take.
func (c *Client) ListNodePCIDevicesAllClasses(ctx context.Context, node string) ([]NodePCIDevice, error) {
	if !mappingNodeRe.MatchString(node) {
		return nil, fmt.Errorf("%w: node name %q is not a Proxmox node name", ErrInvalidInput, node)
	}
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var devices []NodePCIDevice
	path := "/nodes/" + url.PathEscape(node) + "/hardware/pci?pci-class-blacklist="
	if err := c.do(ctx, path, &devices); err != nil {
		return nil, fmt.Errorf("list PCI devices on %s: %w", node, err)
	}
	return devices, nil
}

// PCIMapEntry is one node entry of a PCI mapping, in $map_fmt
// (pve-guest-common src/PVE/Mapping/PCI.pm).
type PCIMapEntry struct {
	Node string
	// Path is domain:bus:slot.function, or domain:bus:slot for the whole
	// device.
	Path string
	// ID is the device's vendor:device id and SubsystemID its subsystem's,
	// "" when the device reports none: lowercase, without "0x".
	ID          string
	SubsystemID string
	// IOMMUGroup is the device's IOMMU group, nil when it is in none.
	IOMMUGroup *int
	// Description is the entry's own description, or "".
	Description string
}

// String writes the entry as a property string: description first, for the
// reason USBMapEntry.String gives — pci.cfg is a section config too — then
// the rest in the order Proxmox's own editor writes them (sorted), an absent
// one left out. Proxmox reads the keys in any order.
func (e PCIMapEntry) String() string {
	var s string
	if e.Description != "" {
		s = "description=" + e.Description + ","
	}
	s += "id=" + e.ID
	if e.IOMMUGroup != nil {
		s += ",iommugroup=" + strconv.Itoa(*e.IOMMUGroup)
	}
	s += ",node=" + e.Node + ",path=" + e.Path
	if e.SubsystemID != "" {
		s += ",subsystem-id=" + e.SubsystemID
	}
	return s
}

// validate holds a PCI entry to $map_fmt before it is written. Stricter than
// Proxmox in three places, each Nexara's: one path, not a list (see
// PCIMapEntryForDevice); a path of at most pciMappingPathMax characters; and
// an IOMMU group that is not negative — the group is the number the device's
// iommu_group link ends in (pci_device_info, pve-common
// src/PVE/SysFSTools.pm), so a negative one can match no device. A comma or a
// line break in the description is refused as well: the first would split
// the entry into another key, the second end the line in pci.cfg.
func (e PCIMapEntry) validate() error {
	if !mappingNodeRe.MatchString(e.Node) {
		return fmt.Errorf("%w: node name %q is not a Proxmox node name", ErrInvalidInput, e.Node)
	}
	if !pciMappingPathRe.MatchString(e.Path) {
		return fmt.Errorf("%w: PCI path %q must be <domain>:<bus>:<slot>[.<function>] in lowercase hex, e.g. 0000:01:00.0", ErrInvalidInput, e.Path)
	}
	if len(e.Path) > pciMappingPathMax {
		return fmt.Errorf("%w: PCI path is longer than %d characters", ErrInvalidInput, pciMappingPathMax)
	}
	if !pciMappingIDRe.MatchString(e.ID) {
		return fmt.Errorf("%w: PCI device id %q must be vendor:device, four hex digits each", ErrInvalidInput, e.ID)
	}
	if e.SubsystemID != "" && !pciMappingIDRe.MatchString(e.SubsystemID) {
		return fmt.Errorf("%w: PCI subsystem id %q must be vendor:device, four hex digits each", ErrInvalidInput, e.SubsystemID)
	}
	if e.IOMMUGroup != nil && *e.IOMMUGroup < 0 {
		return fmt.Errorf("%w: IOMMU group %d is not a group a device can be in", ErrInvalidInput, *e.IOMMUGroup)
	}
	if utf8.RuneCountInString(e.Description) > mappingDescriptionMax {
		return fmt.Errorf("%w: PCI mapping entry description is longer than %d characters", ErrInvalidInput, mappingDescriptionMax)
	}
	if strings.ContainsAny(e.Description, ",\r\n") {
		return fmt.Errorf("%w: a PCI mapping entry's description must be a single line without commas", ErrInvalidInput)
	}
	return nil
}

// pciHexID is one half of a PCI id as the device listing reports it, "0x10de",
// as a mapping entry must hold it: without the prefix, lowercase, and four
// digits, or "" when it is not one.
func pciHexID(raw string) string {
	v := strings.TrimPrefix(strings.ToLower(raw), "0x")
	if len(v) != 4 || strings.Trim(v, "0123456789abcdef") != "" {
		return ""
	}
	return v
}

// PCIMapEntryForDevice builds the entry that passes node's device at path
// through, from the node's device listing, and says whether the mapping must
// carry its mdev flag.
//
// Proxmox refuses to start a VM whose mapped device does not match its entry
// EXACTLY — assert_valid in pve-guest-common src/PVE/Mapping/PCI.pm, run by
// qemu-server's parse_hostpci and by the listing's check. The id and the IOMMU
// group must equal the device's, a subsystem id must be there exactly when
// the device reports one, and the mapping's mdev flag must equal the device's
// mediated-device capability, each way round ("missing expected property",
// "unexpected property", "does not match"). So nothing here comes from the
// caller but the node and the path: the rest is copied from what Proxmox
// reports for the device, as its own mapping editor does
// (www/manager6/window/PCIMapEdit.js):
//   - id and subsystem-id without the listing's "0x" (the entry's pattern
//     admits none) and lowercase (assert_valid compares with `ne` against
//     sysfs's lowercase hex);
//   - subsystem-id only when the device reports both halves, which is when
//     pci_device_info (pve-common src/PVE/SysFSTools.pm) expects one;
//   - iommugroup only when the device is in a group: the listing reports -1
//     for none, and pci_device_info then reports no group at all.
//
// A path without a function maps the whole device, all functions passed
// through as one, and Proxmox checks it against function 0 — so the entry is
// built from that function's record.
//
// One path per entry, where Proxmox's schema admits a ";"-joined list: its
// code passes such a list as one multi-function device rather than as the
// alternatives the schema describes, and its own editor never writes one.
// Several devices on a node are several entries.
func PCIMapEntryForDevice(node, path string, devices []NodePCIDevice) (PCIMapEntry, bool, error) {
	if !mappingNodeRe.MatchString(node) {
		return PCIMapEntry{}, false, fmt.Errorf("%w: node name %q is not a Proxmox node name", ErrInvalidInput, node)
	}
	m := pciMappingPathRe.FindStringSubmatch(path)
	if m == nil || len(path) > pciMappingPathMax {
		return PCIMapEntry{}, false, fmt.Errorf("%w: PCI path %q must be <domain>:<bus>:<slot>[.<function>] in lowercase hex, e.g. 0000:01:00.0", ErrInvalidInput, path)
	}
	record := path
	if m[1] == "" {
		record = path + ".0"
	}
	var dev *NodePCIDevice
	for i := range devices {
		if devices[i].ID == record {
			dev = &devices[i]
			break
		}
	}
	if dev == nil {
		return PCIMapEntry{}, false, fmt.Errorf("%w: node %s has no PCI device %s", ErrInvalidInput, node, record)
	}

	// What the node reported is not the caller's input, so an id Nexara
	// cannot read is ErrInvalidResponse, not ErrInvalidInput, and is quoted
	// only in part: the listing's length is Proxmox's to choose.
	vendor, device := pciHexID(dev.Vendor), pciHexID(dev.Device)
	if vendor == "" || device == "" {
		return PCIMapEntry{}, false, fmt.Errorf("%w: Proxmox reports PCI device %s with an id that is not vendor:device (%.32q:%.32q)", ErrInvalidResponse, record, dev.Vendor, dev.Device)
	}
	entry := PCIMapEntry{Node: node, Path: path, ID: vendor + ":" + device}
	if dev.SubsystemVendor != "" && dev.SubsystemDevice != "" {
		subVendor, subDevice := pciHexID(dev.SubsystemVendor), pciHexID(dev.SubsystemDevice)
		if subVendor == "" || subDevice == "" {
			return PCIMapEntry{}, false, fmt.Errorf("%w: Proxmox reports PCI device %s with a subsystem id that is not vendor:device (%.32q:%.32q)", ErrInvalidResponse, record, dev.SubsystemVendor, dev.SubsystemDevice)
		}
		entry.SubsystemID = subVendor + ":" + subDevice
	}
	if dev.IOMMUGroup >= 0 {
		group := dev.IOMMUGroup
		entry.IOMMUGroup = &group
	}
	return entry, bool(dev.MDev), nil
}

// CreatePCIMappingParams is a new PCI mapping with one node entry.
type CreatePCIMappingParams struct {
	// ID names the mapping; a guest's hostpciN refers to it as mapping=<ID>.
	ID string
	// Description is optional.
	Description string
	// Entry is the node entry, as PCIMapEntryForDevice builds it.
	Entry PCIMapEntry
	// MDev is the mapping's "Use with Mediated Devices" flag, which must be
	// set exactly when the device can provide mediated devices — the second
	// value PCIMapEntryForDevice returns.
	MDev bool
}

// CreatePCIMapping creates a PCI mapping with a single node entry.
//
// Every value is checked here rather than by the route alone, because the
// entry's values are joined into one property string and the mapping is
// cluster-wide configuration. The ids are written lowercase, which is what
// Proxmox compares against the device when a VM starts.
func (c *Client) CreatePCIMapping(ctx context.Context, params CreatePCIMappingParams) error {
	if err := validateMappingID(params.ID); err != nil {
		return err
	}
	if err := validateMappingDescription(params.Description); err != nil {
		return err
	}
	entry := params.Entry
	entry.ID = strings.ToLower(entry.ID)
	entry.SubsystemID = strings.ToLower(entry.SubsystemID)
	if err := entry.validate(); err != nil {
		return err
	}

	form := url.Values{}
	form.Set("id", params.ID)
	form.Set("map", entry.String())
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if params.MDev {
		form.Set("mdev", "1")
	}
	if err := c.doPost(ctx, "/cluster/mapping/pci", form, nil); err != nil {
		return fmt.Errorf("create PCI mapping %s: %w", params.ID, err)
	}
	return nil
}
