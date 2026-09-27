package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
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

// ListUSBMappings lists the cluster's USB mappings.
//
// With checkNode set, Proxmox runs the listing ON that node (the endpoint's
// proxyto_callback) and reports, per mapping, what would stop it working there:
// a warning "No mapping for node <n>." when it has no entry for that node, and
// an error "Invalid configuration: …" when its entry names hardware the node
// does not have. Without it, no mapping carries any.
//
// Proxmox lists only the mappings the token holds Mapping.Modify, Mapping.Use
// or Mapping.Audit on, so a token without them gets an empty list, not a 403.
//
// checkNode travels as a query value, so nothing here guards a path segment;
// it is held to pve-node because Proxmox proxies the request to the node it
// names and validates it with that same format, so the check refuses nothing
// Proxmox would accept.
func (c *Client) ListUSBMappings(ctx context.Context, checkNode string) ([]USBMapping, error) {
	path := "/cluster/mapping/usb"
	if checkNode != "" {
		if !mappingNodeRe.MatchString(checkNode) {
			return nil, fmt.Errorf("%w: node name %q is not a Proxmox node name", ErrInvalidInput, checkNode)
		}
		q := url.Values{}
		q.Set("check-node", checkNode)
		path += "?" + q.Encode()
	}
	var mappings []USBMapping
	if err := c.do(ctx, path, &mappings); err != nil {
		return nil, fmt.Errorf("list USB mappings: %w", err)
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
	entry := "node=" + params.Node + ",id=" + strings.ToLower(params.DeviceID)
	if params.Path != "" {
		entry += ",path=" + params.Path
	}

	form := url.Values{}
	form.Set("id", params.ID)
	form.Set("map", entry)
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if err := c.doPost(ctx, "/cluster/mapping/usb", form, nil); err != nil {
		return fmt.Errorf("create USB mapping %s: %w", params.ID, err)
	}
	return nil
}
