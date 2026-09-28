package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
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

// --- Stored entries, and changing a mapping -----------------------------------

// pciMapEntryKeys are the keys $map_fmt declares (pve-guest-common
// src/PVE/Mapping/PCI.pm). Like USB's it declares no default_key and no alias,
// so a part with no key and any other key are both refused.
var pciMapEntryKeys = map[string]bool{
	"node": true, "path": true, "id": true, "subsystem-id": true, "iommugroup": true, "description": true,
}

// pciMappingPathListRe is $map_fmt's path pattern as Proxmox checks a stored
// entry: one pciMappingPathRe address, or several joined by ";".
var pciMappingPathListRe = regexp.MustCompile(
	`^[a-f0-9]{4,}:[a-f0-9]{2}:[a-f0-9]{2}(\.[a-f0-9])?(;[a-f0-9]{4,}:[a-f0-9]{2}:[a-f0-9]{2}(\.[a-f0-9])?)*$`)

// ParsePCIMapEntry reads one node entry of a PCI mapping the way Proxmox
// reads a stored one, refusing what Proxmox would refuse, and returns it with
// its ids lowercased.
//
// The parse is ParseUSBMapEntry's transcription of parse_property_string
// (pve-common src/PVE/JSONSchema.pm) — split on ",", each part at its first
// "=", neither side empty, nothing trimmed, a part that is only white space
// skipped — over PCI's $map_fmt: node, path and id required; node the pve-node
// format; id and subsystem-id vendor:device; path one address or a
// ";"-joined list of them (pciMappingPathListRe); iommugroup an integer, a
// sign allowed; description at most 4096 characters.
//
// It is the parse for entries Proxmox already holds, so it takes what Proxmox
// takes there: a list of paths and a group of any sign, which
// PCIMapEntryForDevice never builds and PCIMapEntry.validate refuses for a new
// entry. Stricter than Proxmox in three places, each Nexara's:
//   - A line feed or carriage return anywhere, for ParseUSBMapEntry's reason.
//   - The group's digits are ASCII. Perl's \d takes the digits of every
//     script, and the group is written back as the number it is; one that
//     is not ASCII, or does not fit an int, is refused rather than rewritten.
//   - The ids come back lowercase, as USB's do: assert_valid compares them
//     with `ne` against the node's sysfs hex, so an uppercase one refuses to
//     start every VM that uses the mapping, and writing it back repairs it.
func ParsePCIMapEntry(raw string) (PCIMapEntry, error) {
	e, problem := parsePCIMapEntry(raw)
	if problem != "" {
		return PCIMapEntry{}, fmt.Errorf("%w: PCI mapping entry %q %s", ErrInvalidInput, raw, problem)
	}
	return e, nil
}

// PCIMapEntryProblem says why ParsePCIMapEntry refuses raw, as a sentence
// that does not repeat raw — for a listing that shows the entry beside it —
// or returns "" when it reads. A part of raw it names is cut to 40
// characters.
func PCIMapEntryProblem(raw string) string {
	if _, problem := parsePCIMapEntry(raw); problem != "" {
		return "The entry " + problem + "."
	}
	return ""
}

// parsePCIMapEntry is ParsePCIMapEntry, with what is wrong said as a phrase
// that follows the entry — "", when nothing is.
func parsePCIMapEntry(raw string) (entry PCIMapEntry, problem string) {
	if strings.ContainsAny(raw, "\r\n") {
		return PCIMapEntry{}, "must be a single line"
	}
	values := make(map[string]string, len(pciMapEntryKeys))
	for _, part := range strings.Split(raw, ",") {
		if strings.TrimFunc(part, unicode.IsSpace) == "" {
			continue
		}
		key, value, hasEq := strings.Cut(part, "=")
		switch {
		case !hasEq:
			return PCIMapEntry{}, fmt.Sprintf("has a value without a key (%.40q)", part)
		case key == "" || value == "":
			return PCIMapEntry{}, fmt.Sprintf("has a key or a value missing (%.40q)", part)
		case !pciMapEntryKeys[key]:
			return PCIMapEntry{}, fmt.Sprintf("has an unknown key %.40q; the keys are node, path, id, subsystem-id, "+
				"iommugroup and description", key)
		}
		if _, dup := values[key]; dup {
			return PCIMapEntry{}, fmt.Sprintf("names %q twice", key)
		}
		values[key] = value
	}

	e := PCIMapEntry{
		Node:        values["node"],
		Path:        values["path"],
		ID:          values["id"],
		SubsystemID: values["subsystem-id"],
		Description: values["description"],
	}
	if !mappingNodeRe.MatchString(e.Node) {
		return PCIMapEntry{}, "needs node=<a Proxmox node name>"
	}
	if !pciMappingPathListRe.MatchString(e.Path) {
		return PCIMapEntry{}, "needs path=<domain>:<bus>:<slot>[.<function>] in lowercase hex, or several such " +
			"addresses joined by semicolons"
	}
	if !pciMappingIDRe.MatchString(e.ID) {
		return PCIMapEntry{}, "needs id=<vendor:device>, four hex digits each"
	}
	// Tested by key rather than by value, as ParseUSBMapEntry tests the port.
	if _, has := values["subsystem-id"]; has && !pciMappingIDRe.MatchString(e.SubsystemID) {
		return PCIMapEntry{}, "has a subsystem-id that is not vendor:device, four hex digits each"
	}
	if group, has := values["iommugroup"]; has {
		// Proxmox checks it as a JSONSchema integer — is_integer in pve-common
		// src/PVE/JSONSchema.pm, m/^[+-]?\d+$/ — and strconv.Atoi takes
		// exactly that with \d held to ASCII, in the range of an int.
		n, err := strconv.Atoi(group)
		if err != nil {
			return PCIMapEntry{}, "has an iommugroup that is not a whole number"
		}
		e.IOMMUGroup = &n
	}
	if utf8.RuneCountInString(e.Description) > mappingDescriptionMax {
		return PCIMapEntry{}, fmt.Sprintf("has a description longer than %d characters", mappingDescriptionMax)
	}
	e.ID = strings.ToLower(e.ID)
	e.SubsystemID = strings.ToLower(e.SubsystemID)
	return e, ""
}

// CanonicalPCIMap validates a PCI mapping's whole list of node entries and
// returns them as they will be written: each read as Proxmox reads a stored
// entry (ParsePCIMapEntry) and re-serialised by PCIMapEntry.String, in the
// order given — a node's entries are tried in that order when a VM starts
// (choose_hostpci_devices), so the order is kept as it was.
//
// An empty list is refused, as CanonicalUSBMap refuses one: Proxmox's own UI
// deletes a mapping rather than emptying it, and so must a caller here
// (DeletePCIMapping). Unlike USB's, several entries for one node are fine:
// each is a device a VM there may be given.
func CanonicalPCIMap(entries []string) ([]string, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: a PCI mapping needs at least one node entry; delete the mapping instead", ErrInvalidInput)
	}
	out := make([]string, 0, len(entries))
	for _, raw := range entries {
		e, err := ParsePCIMapEntry(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, e.String())
	}
	return out, nil
}

// UpdatePCIMappingParams replaces a PCI mapping's node entries.
type UpdatePCIMappingParams struct {
	// Map is the mapping's WHOLE list of node entries, not a change to it,
	// as UpdateUSBMappingParams.Map is. See CanonicalPCIMap for what each
	// entry must be.
	Map []string
	// Description, when non-nil, replaces the mapping's description, and an
	// empty one removes it. Nil leaves it as it is.
	Description *string
	// MDev, when non-nil, sets the mapping's "Use with Mediated Devices" flag
	// (true) or removes it (false). Nil leaves it as it is. Proxmox compares
	// the flag with every entry's device when a VM starts (PCIMapping.MDev).
	MDev *bool
	// Digest is the pci.cfg digest of the listing this update is based on
	// (PCIMapping.Digest), required for UpdateUSBMappingParams.Digest's
	// reason.
	Digest string
}

// UpdatePCIMapping replaces a PCI mapping's node entries and, optionally, its
// description and its mdev flag: PUT /cluster/mapping/pci/{id}.
//
// Every entry is re-read and re-serialised here (CanonicalPCIMap) rather than
// forwarded, for UpdateUSBMapping's reason. An option to remove goes into ONE
// delete, which Proxmox reads as a list, and no option is both set and
// removed: the description and the flag are each one or the other.
func (c *Client) UpdatePCIMapping(ctx context.Context, id string, params UpdatePCIMappingParams) error {
	if err := validateMappingID(id); err != nil {
		return err
	}
	entries, err := CanonicalPCIMap(params.Map)
	if err != nil {
		return err
	}
	// "0" as well, for UpdateUSBMapping's reason: Perl reads it as false,
	// and assert_if_modified would skip the check.
	if params.Digest == "" || params.Digest == "0" {
		return fmt.Errorf("%w: a PCI mapping update needs the digest of the listing it is based on", ErrInvalidInput)
	}
	if len(params.Digest) > mappingDigestMax {
		return fmt.Errorf("%w: digest is longer than %d characters", ErrInvalidInput, mappingDigestMax)
	}

	form := url.Values{}
	for _, e := range entries {
		form.Add("map", e)
	}
	var remove []string
	if params.Description != nil {
		if *params.Description == "" {
			remove = append(remove, "description")
		} else {
			if err := validateMappingDescription(*params.Description); err != nil {
				return err
			}
			form.Set("description", *params.Description)
		}
	}
	if params.MDev != nil {
		if *params.MDev {
			form.Set("mdev", "1")
		} else {
			remove = append(remove, "mdev")
		}
	}
	if len(remove) > 0 {
		form.Set("delete", strings.Join(remove, ","))
	}
	form.Set("digest", params.Digest)

	if err := c.doPut(ctx, "/cluster/mapping/pci/"+url.PathEscape(id), form, nil); err != nil {
		return fmt.Errorf("update PCI mapping %s: %w", id, err)
	}
	return nil
}

// DeletePCIMapping removes a PCI mapping: DELETE /cluster/mapping/pci/{id}.
//
// Like DeleteUSBMapping's, Proxmox's delete takes no digest, checks no guest
// that uses the mapping, and succeeds whether or not the id exists.
func (c *Client) DeletePCIMapping(ctx context.Context, id string) error {
	// The id becomes a path segment; validateMappingID keeps "/", "." and
	// "%" out of it.
	if err := validateMappingID(id); err != nil {
		return err
	}
	if err := c.doDelete(ctx, "/cluster/mapping/pci/"+url.PathEscape(id), nil); err != nil {
		return fmt.Errorf("delete PCI mapping %s: %w", id, err)
	}
	return nil
}
