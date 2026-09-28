package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Cluster resource mappings, /cluster/mapping/<kind>.
//
// A mapping is the only way an API token can pass a real host device through
// to a guest. qemu-server's check_usb_perm (src/PVE/API2/Qemu.pm) returns early
// for the literal user root@pam and otherwise dies
// "only root can set 'usbN' config for real devices" for any usbN whose host=
// is not "spice" — by vendor/device id and by port alike, on the value being
// written and on the one it replaces or deletes. A token authenticates as
// user@realm!tokenname, so not even a token root@pam owns passes, and Nexara
// connects with nothing but tokens. A usbN of mapping=<id> is checked against
// Mapping.Use on /mapping/usb/<id> instead, which a token can hold.

// usbMappingDeviceIDRe, usbMappingPathRe and mappingNodeRe transcribe the
// per-node entry format of a USB mapping, $map_fmt in pve-guest-common
// src/PVE/Mapping/USB.pm:
//
//	id   => pattern qr/^[0-9A-Fa-f]{4}:[0-9A-Fa-f]{4}$/
//	path => pattern qr/^(\d+)\-(\d+(\.\d+)*)$/
//	node => get_standard_option('pve-node')
//
// pve-node is pve_verify_node_name in pve-common src/PVE/JSONSchema.pm,
// ^([a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?)\z. It is transcribed here, where
// validateNodeName deliberately does not (see its comment), because this value
// is not a path segment: it is spliced into the entry's property string, where
// a "," or "=" would add a key of the caller's choosing ("a,path=1-2"). Proxmox
// validates this field with that same format, so the check refuses nothing
// Proxmox would accept.
var (
	usbMappingDeviceIDRe = regexp.MustCompile(`^[0-9A-Fa-f]{4}:[0-9A-Fa-f]{4}$`)
	usbMappingPathRe     = regexp.MustCompile(`^\d+-\d+(\.\d+)*$`)
	mappingNodeRe        = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)
)

// usbMappingPathMax bounds the port. The bound is Nexara's — Proxmox states
// none — and refuses nothing that can name a real port: USB allows seven tiers
// below the root, and even "999-255.255.255.255.255.255.255" is 31 characters.
// It keeps a path that cannot match any device out of usb.cfg and out of the
// audit row, both of which store it verbatim.
const usbMappingPathMax = 64

// mappingIDRe is pve-configid, which every mapping kind's id uses:
// $CONFIGID_RE in pve-common src/PVE/JSONSchema.pm, qr/[a-z][a-z0-9_-]+/i —
// a letter and then at least one more, with no maximum.
var mappingIDRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]+$`)

// mappingDescriptionMax is the description's maxLength in both the mapping
// and its node entries (src/PVE/Mapping/USB.pm).
const mappingDescriptionMax = 4096

func validateMappingID(id string) error {
	if !mappingIDRe.MatchString(id) {
		return fmt.Errorf("%w: mapping id %q must start with a letter and hold only letters, digits, '_' and '-' (2 characters or more)", ErrInvalidInput, id)
	}
	return nil
}

// validateMappingDescription applies the two limits Proxmox does. The line
// feed ban is pve-common src/PVE/SectionConfig.pm check_value ("property
// contains a line feed"): a mapping is one section of a section config, where
// a new line would start another property.
func validateMappingDescription(description string) error {
	if utf8.RuneCountInString(description) > mappingDescriptionMax {
		return fmt.Errorf("%w: mapping description is longer than %d characters", ErrInvalidInput, mappingDescriptionMax)
	}
	if strings.ContainsAny(description, "\r\n") {
		return fmt.Errorf("%w: mapping description must be a single line", ErrInvalidInput)
	}
	return nil
}

// listMappings reads GET /cluster/mapping/<kind> — "usb" or "pci" — into
// out, the kind's own slice type.
//
// With checkNode set, Proxmox runs the listing ON that node (the endpoint's
// proxyto_callback) and reports, per mapping, what would stop it working there:
// a warning "No mapping for node <n>." when it has no entry for that node, and
// an error "Invalid configuration: …" when an entry names hardware the node
// does not have. Without it, no mapping carries any. The two kinds report
// these under different keys, which is why out is typed by the caller.
//
// Proxmox lists only the mappings the token holds Mapping.Modify, Mapping.Use
// or Mapping.Audit on, so a token without them gets an empty list, not a 403.
//
// checkNode travels as a query value, so nothing here guards a path segment;
// it is held to pve-node because Proxmox proxies the request to the node it
// names and validates it with that same format, so the check refuses nothing
// Proxmox would accept.
func (c *Client) listMappings(ctx context.Context, kind, label, checkNode string, out any) error {
	path := "/cluster/mapping/" + kind
	if checkNode != "" {
		if !mappingNodeRe.MatchString(checkNode) {
			return fmt.Errorf("%w: node name %q is not a Proxmox node name", ErrInvalidInput, checkNode)
		}
		q := url.Values{}
		q.Set("check-node", checkNode)
		path += "?" + q.Encode()
	}
	if err := c.do(ctx, path, out); err != nil {
		return fmt.Errorf("list %s mappings: %w", label, err)
	}
	return nil
}

// ListUSBMappings lists the cluster's USB mappings, each checked against
// checkNode when it is set (listMappings); the checks are in Errors.
func (c *Client) ListUSBMappings(ctx context.Context, checkNode string) ([]USBMapping, error) {
	var mappings []USBMapping
	if err := c.listMappings(ctx, "usb", "USB", checkNode, &mappings); err != nil {
		return nil, err
	}
	for i := range mappings {
		if mappings[i].Map == nil {
			mappings[i].Map = []string{}
		}
		if mappings[i].Errors == nil {
			mappings[i].Errors = []MappingCheck{}
		}
	}
	return mappings, nil
}

// CreateUSBMappingParams is a new USB mapping with one node entry.
type CreateUSBMappingParams struct {
	// ID names the mapping; a guest's usbN refers to it as mapping=<ID>.
	ID string
	// Description is optional.
	Description string
	// Node is the node the entry is for. A mapping may carry one usable
	// entry per node: the create stores more, but qemu-server refuses the
	// VM start with "More than one USB mapping per host not supported"
	// (parse_usb_device, src/PVE/QemuServer/USB.pm).
	Node string
	// DeviceID is the device's "vendor:product" id, four hex digits each.
	DeviceID string
	// Path, when set, is a USB port — "<bus>-<port>[.<port>…]" — and ties the
	// mapping to the device on that port, which is how two identical devices
	// are told apart. Proxmox refuses to start the VM if a device with
	// another id is on the port ("'id' does not match", assert_valid in
	// pve-guest-common src/PVE/Mapping/USB.pm).
	Path string
}

// CreateUSBMapping creates a USB mapping with a single node entry.
//
// Every value is checked here rather than by the route alone, because the
// node, device id and port are joined into one property string and the
// mapping is cluster-wide configuration.
func (c *Client) CreateUSBMapping(ctx context.Context, params CreateUSBMappingParams) error {
	if err := validateMappingID(params.ID); err != nil {
		return err
	}
	if err := validateMappingDescription(params.Description); err != nil {
		return err
	}
	if !mappingNodeRe.MatchString(params.Node) {
		return fmt.Errorf("%w: node name %q is not a Proxmox node name", ErrInvalidInput, params.Node)
	}
	if !usbMappingDeviceIDRe.MatchString(params.DeviceID) {
		return fmt.Errorf("%w: USB device id %q must be vendor:product, four hex digits each", ErrInvalidInput, params.DeviceID)
	}
	if params.Path != "" && !usbMappingPathRe.MatchString(params.Path) {
		return fmt.Errorf("%w: USB port %q must be <bus>-<port>, e.g. 1-2 or 1-2.3", ErrInvalidInput, params.Path)
	}
	if len(params.Path) > usbMappingPathMax {
		return fmt.Errorf("%w: USB port is longer than %d characters", ErrInvalidInput, usbMappingPathMax)
	}

	// Lowercased because Proxmox compares it case-sensitively: assert_valid in
	// src/PVE/Mapping/USB.pm tests "$vendid:$prodid" from the node's sysfs,
	// which Linux writes as lowercase hex, with `ne`. Its schema admits
	// "ABCD:EF01", and a mapping stored that way is accepted here and then
	// refuses to start every guest that uses it.
	entry := USBMapEntry{Node: params.Node, ID: strings.ToLower(params.DeviceID), Path: params.Path}

	form := url.Values{}
	form.Set("id", params.ID)
	form.Set("map", entry.String())
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if err := c.doPost(ctx, "/cluster/mapping/usb", form, nil); err != nil {
		return fmt.Errorf("create USB mapping %s: %w", params.ID, err)
	}
	return nil
}

// USBMapEntry is one node entry of a USB mapping, parsed and validated by
// ParseUSBMapEntry.
type USBMapEntry struct {
	Node string
	// ID is the device's vendor:product id, always lowercase: see
	// ParseUSBMapEntry.
	ID string
	// Path is the port, or "" when the entry follows the device to any port.
	Path string
	// Description is the entry's own description, or "". Proxmox's own
	// mapping editor never shows it, but an entry can carry one, and an
	// update rewrites every entry, so it has to survive the round trip.
	Description string
}

// String writes the entry as a property string: description first, then id,
// node and path, an absent one left out. Proxmox reads the keys in any order.
//
// The description goes FIRST so that it is never the end of the line. usb.cfg
// is a section config, and parse_config (pve-common src/PVE/SectionConfig.pm)
// reads each property as m/^\s+(\S+)(\s+(.*\S))?\s*$/, dropping the white
// space that ends a line. An entry ending in its description would come back
// without the description's trailing spaces — and one whose description is
// only spaces as "description=", which parse_property_string refuses: the
// entry is then dropped on every read, and a mapping left with no entry stops
// usb.cfg being written at all. print_property_string's own order (required
// keys sorted, then the rest) is no help: without a path it ends in the
// description too. Written first, the description keeps every byte, and the
// line ends in a node name or a port, which hold no white space.
func (e USBMapEntry) String() string {
	var s string
	if e.Description != "" {
		s = "description=" + e.Description + ","
	}
	s += "id=" + e.ID + ",node=" + e.Node
	if e.Path != "" {
		s += ",path=" + e.Path
	}
	return s
}

// usbMapEntryKeys are the keys $map_fmt declares (pve-guest-common
// src/PVE/Mapping/USB.pm). It declares no default_key and no alias, so a part
// with no key and any other key are both refused.
var usbMapEntryKeys = map[string]bool{"node": true, "id": true, "path": true, "description": true}

// ParseUSBMapEntry reads one node entry of a USB mapping the way Proxmox
// does, refusing what Proxmox would refuse, and returns it with its device id
// lowercased.
//
// The parse transcribes parse_property_string (pve-common
// src/PVE/JSONSchema.pm) for $map_fmt:
//
//	foreach my $part (split(/,/, $data)) {
//	    next if $part =~ /^\s*\z/;
//	    if ($part =~ /\n/) { die "properties must not contain newlines\n"; }
//	    elsif ($part =~ /^([^=]+)=(.+)\z/) { … die "duplicate key …" … die "invalid key …" }
//	    elsif ($part !~ /=/) { … die "value without key, but schema does not define a default key" }
//	    else { die "missing key in comma-separated list property" }
//	}
//
// So a part splits at its FIRST "=" (a description may hold more), neither
// side may be empty, nothing is trimmed (" node" is an unknown key, "pve-01 "
// fails the node format), and a part that is only white space is skipped.
// The string is UTF-8-decoded by then, so Perl's \s is Unicode white space —
// the same set unicode.IsSpace tests. check_object then applies $map_fmt:
// node and id required; node the pve-node format, id and path their patterns,
// description at most 4096 characters.
//
// Stricter than Proxmox in four places, each Nexara's:
//   - A line feed anywhere, even in a part Proxmox would skip as blank
//     (",\n"). Upstream refuses one only inside a part it reads, and a skipped
//     one reaches usb.cfg, where it ends the line — and a blank line ends the
//     section.
//   - A carriage return, which Proxmox's "(.+)" admits. The mapping's own
//     description refuses both (SectionConfig check_value, "property
//     contains a line feed"); an entry's description is held to the same.
//   - The port is at most usbMappingPathMax characters, the bound
//     CreateUSBMapping already applies.
//   - The id comes back lowercase. assert_valid compares it with `ne`
//     against the node's sysfs hex, which Linux writes in lowercase, so an
//     uppercase id is accepted and then refuses to start every VM that uses
//     the mapping. Writing it back lowercase repairs such an entry.
func ParseUSBMapEntry(raw string) (USBMapEntry, error) {
	if strings.ContainsAny(raw, "\r\n") {
		return USBMapEntry{}, fmt.Errorf("%w: USB mapping entry %q must be a single line", ErrInvalidInput, raw)
	}
	values := make(map[string]string, len(usbMapEntryKeys))
	for _, part := range strings.Split(raw, ",") {
		if strings.TrimFunc(part, unicode.IsSpace) == "" {
			continue
		}
		key, value, hasEq := strings.Cut(part, "=")
		switch {
		case !hasEq:
			return USBMapEntry{}, fmt.Errorf("%w: USB mapping entry %q has a value without a key (%q)", ErrInvalidInput, raw, part)
		case key == "" || value == "":
			return USBMapEntry{}, fmt.Errorf("%w: USB mapping entry %q has a key or a value missing (%q)", ErrInvalidInput, raw, part)
		case !usbMapEntryKeys[key]:
			return USBMapEntry{}, fmt.Errorf("%w: USB mapping entry %q has an unknown key %q; the keys are node, id, path and description", ErrInvalidInput, raw, key)
		}
		if _, dup := values[key]; dup {
			return USBMapEntry{}, fmt.Errorf("%w: USB mapping entry %q names %q twice", ErrInvalidInput, raw, key)
		}
		values[key] = value
	}

	e := USBMapEntry{Node: values["node"], ID: values["id"], Path: values["path"], Description: values["description"]}
	if !mappingNodeRe.MatchString(e.Node) {
		return USBMapEntry{}, fmt.Errorf("%w: USB mapping entry %q needs node=<a Proxmox node name>", ErrInvalidInput, raw)
	}
	if !usbMappingDeviceIDRe.MatchString(e.ID) {
		return USBMapEntry{}, fmt.Errorf("%w: USB mapping entry %q needs id=<vendor:product>, four hex digits each", ErrInvalidInput, raw)
	}
	// Tested by key rather than by value: "path=" is refused above, so a
	// present path is never empty, but the check should not rely on that.
	if _, hasPath := values["path"]; hasPath {
		if !usbMappingPathRe.MatchString(e.Path) {
			return USBMapEntry{}, fmt.Errorf("%w: USB mapping entry %q has a port that is not <bus>-<port>, e.g. 1-2 or 1-2.3", ErrInvalidInput, raw)
		}
		if len(e.Path) > usbMappingPathMax {
			return USBMapEntry{}, fmt.Errorf("%w: USB mapping entry %q has a port longer than %d characters", ErrInvalidInput, raw, usbMappingPathMax)
		}
	}
	if utf8.RuneCountInString(e.Description) > mappingDescriptionMax {
		return USBMapEntry{}, fmt.Errorf("%w: USB mapping entry %q has a description longer than %d characters", ErrInvalidInput, raw, mappingDescriptionMax)
	}
	e.ID = strings.ToLower(e.ID)
	return e, nil
}

// CanonicalUSBMap validates a mapping's whole list of node entries and
// returns them as they will be written: each re-serialised by
// USBMapEntry.String, in the order given.
//
// Two refusals on top of ParseUSBMapEntry's:
//   - An empty list. Proxmox's update takes one, and the mapping it stores
//     then corrupts usb.cfg for every later write; its own UI deletes the
//     mapping instead of emptying it, and so must a caller here
//     (DeleteUSBMapping).
//   - Two entries for one node. Proxmox's API stores them, but qemu-server
//     refuses to start a VM using the mapping on that node, "More than one
//     USB mapping per host not supported" (parse_usb_device,
//     src/PVE/QemuServer/USB.pm), and Proxmox's own editor offers one entry
//     per node. Nexara aligns with qemu-server rather than storing a mapping
//     that cannot be used there.
func CanonicalUSBMap(entries []string) ([]string, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: a USB mapping needs at least one node entry; delete the mapping instead", ErrInvalidInput)
	}
	out := make([]string, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, raw := range entries {
		e, err := ParseUSBMapEntry(raw)
		if err != nil {
			return nil, err
		}
		if seen[e.Node] {
			return nil, fmt.Errorf("%w: the mapping has two entries for node %s; Proxmox refuses to start a VM using it there", ErrInvalidInput, e.Node)
		}
		seen[e.Node] = true
		out = append(out, e.String())
	}
	return out, nil
}

// mappingDigestMax is pve-config-digest's maxLength (pve-common
// src/PVE/JSONSchema.pm), the standard option a mapping update declares its
// digest with: room for a sha256, though usb.cfg's is a 40-character sha1.
const mappingDigestMax = 64

// UpdateUSBMappingParams replaces a USB mapping's node entries.
type UpdateUSBMappingParams struct {
	// Map is the mapping's WHOLE list of node entries, not a change to it:
	// Proxmox replaces the list with what is sent, and requires one on every
	// update (check_config runs with $create set, and map is required). See
	// CanonicalUSBMap for what each entry must be.
	Map []string
	// Description, when non-nil, replaces the mapping's description, and an
	// empty one removes it (delete=description). Nil leaves it as it is.
	Description *string
	// Digest is the usb.cfg digest of the listing this update is based on
	// (USBMapping.Digest). Required: Proxmox refuses the write with
	// "detected modified configuration" when any USB mapping changed since —
	// PVE::Tools::assert_if_modified, checked before anything else. Proxmox
	// itself would take an update without one and skip the check; Nexara
	// never sends one, so an edit cannot overwrite a change it was not shown.
	Digest string
}

// UpdateUSBMapping replaces a USB mapping's node entries and, optionally, its
// description: PUT /cluster/mapping/usb/{id}.
//
// Every entry is re-checked and re-serialised here (CanonicalUSBMap) rather
// than forwarded, because the list replaces what Proxmox has and usb.cfg is
// cluster-wide configuration: a malformed entry is refused before anything is
// sent, not stored.
func (c *Client) UpdateUSBMapping(ctx context.Context, id string, params UpdateUSBMappingParams) error {
	if err := validateMappingID(id); err != nil {
		return err
	}
	entries, err := CanonicalUSBMap(params.Map)
	if err != nil {
		return err
	}
	// "0" as well: PVE::Tools::assert_if_modified compares the digests only
	// when both are true in Perl, and "0" is false — Proxmox would take
	// digest=0 as no digest and skip the check this field exists for.
	if params.Digest == "" || params.Digest == "0" {
		return fmt.Errorf("%w: a USB mapping update needs the digest of the listing it is based on", ErrInvalidInput)
	}
	if len(params.Digest) > mappingDigestMax {
		return fmt.Errorf("%w: digest is longer than %d characters", ErrInvalidInput, mappingDigestMax)
	}

	form := url.Values{}
	// One "map" key per entry: that is how Proxmox's HTTP server receives an
	// array parameter.
	for _, e := range entries {
		form.Add("map", e)
	}
	if params.Description != nil {
		if *params.Description == "" {
			// description is optional in the mapping's options, so
			// delete_from_config (pve-common src/PVE/SectionConfig.pm) takes
			// it, and a mapping without one is left as it was.
			form.Set("delete", "description")
		} else {
			if err := validateMappingDescription(*params.Description); err != nil {
				return err
			}
			form.Set("description", *params.Description)
		}
	}
	form.Set("digest", params.Digest)

	if err := c.doPut(ctx, "/cluster/mapping/usb/"+url.PathEscape(id), form, nil); err != nil {
		return fmt.Errorf("update USB mapping %s: %w", id, err)
	}
	return nil
}

// DeleteUSBMapping removes a USB mapping: DELETE /cluster/mapping/usb/{id}.
//
// Proxmox's delete takes no digest, checks no guest that uses the mapping,
// and succeeds whether or not the id exists (it deletes the key when there is
// one and writes the file either way). A caller that must tell a deletion
// from a no-op has to look first.
func (c *Client) DeleteUSBMapping(ctx context.Context, id string) error {
	// The id becomes a path segment: validateMappingID admits only letters,
	// digits, "_" and "-", so no "/", "." or "%" can reach the path.
	if err := validateMappingID(id); err != nil {
		return err
	}
	if err := c.doDelete(ctx, "/cluster/mapping/usb/"+url.PathEscape(id), nil); err != nil {
		return fmt.Errorf("delete USB mapping %s: %w", id, err)
	}
	return nil
}

// usbConfigKeyRe is a guest's USB device key: usb0 … usb13 today, matched
// without a ceiling so a later qemu-server's extra slots are not missed.
var usbConfigKeyRe = regexp.MustCompile(`^usb\d+$`)

// USBMappingKeys returns the guest's usbN keys that pass mapping id through,
// in key order.
//
// A usbN value is a property string ($usb_fmt, qemu-server
// src/PVE/QemuServer/USB.pm) whose "mapping" key names a USB mapping. The id
// is compared exactly: qemu-server looks the mapping up by hash key, so case
// matters to it too.
func (c VMConfig) USBMappingKeys(id string) []string {
	var keys []string
	for key, raw := range c {
		value, ok := raw.(string)
		if !ok || !usbConfigKeyRe.MatchString(key) {
			continue
		}
		for _, part := range strings.Split(value, ",") {
			if k, v, found := strings.Cut(part, "="); found && k == "mapping" && v == id {
				keys = append(keys, key)
				break
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		ni, _ := strconv.Atoi(strings.TrimPrefix(keys[i], "usb"))
		nj, _ := strconv.Atoi(strings.TrimPrefix(keys[j], "usb"))
		return ni < nj
	})
	return keys
}
