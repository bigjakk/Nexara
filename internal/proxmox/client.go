package proxmox

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ClientConfig holds the configuration for creating a new Proxmox API client.
type ClientConfig struct {
	BaseURL        string
	TokenID        string
	TokenSecret    string
	TLSFingerprint string // SHA-256 fingerprint; empty = use system CA pool.
	Timeout        time.Duration
}

// Client communicates with a single Proxmox VE host.
type Client struct {
	*apiClient
}

// NewClient creates a Client from the given config.
func NewClient(cfg ClientConfig) (*Client, error) {
	ac, err := newAPIClient(cfg, "PVEAPIToken")
	if err != nil {
		return nil, err
	}
	return &Client{apiClient: ac}, nil
}

// validateVMID rejects non-positive VM IDs.
func validateVMID(vmid int) error {
	if vmid <= 0 {
		return fmt.Errorf("invalid VMID: %d", vmid)
	}
	return nil
}

// validateNodeName guards a caller-supplied node name before it becomes
// exactly one segment of a Proxmox request path. 144 methods on this client
// open with it — every one that addresses a node.
//
// It DELEGATES rather than carrying a rule of its own, which it did until this
// change, and the difference was a live gap and not a stylistic one. The rule
// it carried refused "", any "/", and ".." as a SUBSTRING — so a bare "." went
// through, and a "." is a path segment that disappears: /nodes/./tasks/{upid}
// resolves onto /nodes/tasks/{upid} the moment pveproxy normalises the path.
// Two callers CHOOSE this value rather than reading it back from Proxmox, so
// the gap was reachable:
//
//   - extractNodeFromUPID (internal/api/handlers/vms.go) takes colon-field 1 of
//     a UPID that taskUPID has already percent-DECODED, so
//     "UPID:.:0:0:0:x::root@pam:" names the node ".". The UPID guard cannot
//     catch that — validateTaskUPID sees a string with no separator in it, and
//     passes — and the two GET task routes are gated on view:task, which the
//     built-in Viewer role holds.
//   - POST /api/v1/tasks (registry_tasks.go) declares "node" as an optional
//     string with a length cap and NO pattern, files the row as running, and
//     reconcileRunningTasks (internal/collector/task_reconcile.go) replays it
//     through GetTaskStatus on every sync tick with the server's own
//     credentials and nobody watching. As with a UPID, the traversal need not
//     be issued by the caller who wrote it.
//
// Delegating also fixes the status code. This was the one member of the family
// returning a bare fmt.Errorf instead of wrapping ErrInvalidInput, and
// mapProxmoxError (internal/api/handlers/proxmox_error.go) turns ErrInvalidInput
// into a 400 and everything it does not recognise into a 500 — so an empty node
// name surfaced as a 500 reading like a Proxmox outage rather than as the
// caller's own bad input.
//
// No pattern and no length cap, deliberately. validateHAConfigID records the
// no-cap half at length; do NOT read it for the other half, because it reaches
// the opposite conclusion there and says so — it chose a pattern precisely so
// the client would not be looser than the declarations it replaced. The
// no-pattern reasoning for a node name is the next three sentences and nothing
// else. Proxmox's own rule is pve_verify_node_name in pve-common
// src/PVE/JSONSchema.pm — `^([a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?)\z`:
// letters, digits and dash, alphanumeric at both ends, with no maximum stated.
// Transcribing it HERE would buy nothing a segment guard does not already buy —
// such a name cannot leave its path segment however long it is — while handing
// the client a way to refuse a name Proxmox itself minted. The collector, the
// scheduler and the DRS, rolling and migration engines all reach these methods
// with node names PVE chose and no route declaration in between, and an
// over-tight guard on that path fails SILENTLY: see the note on validateTaskUPID,
// where the same reasoning is worked through for the unattended replay. The
// documented limit belongs at the route declarations, which carry apischema's
// node-name format (internal/api/apischema/catalogue.go); this function only has
// to keep the value inside its own segment.
//
// A "%" is not refused, unlike in validateVolumeID. Every path this package
// builds from a node name writes url.PathEscape(node), which re-encodes a
// percent to %25 so it arrives as the character the caller meant — the same
// distinction validatePathSegmentAllowingSlash records.
//
// The ".."-as-a-SUBSTRING ban is gone ON PURPOSE; do not put it back. ".."
// traverses only as a whole segment, this value is always escaped into one
// segment, and validatePathSegment refuses the whole-segment form outright.
// What the substring form refused instead was "pve..01" — a name the node-name
// format itself accepts, and which failed as a 500 before this change.
func validateNodeName(node string) error {
	return validatePathSegment("node name", node)
}

// validatePathSegment guards a caller-supplied value that becomes exactly one
// segment of a Proxmox request path.
//
// url.PathEscape alone is not enough for this. It escapes "/" but leaves "."
// and ".." untouched, so an escaped ".." still resolves upward once pveproxy
// normalises the path — landing the request on the parent collection, which is
// often a different endpoint with different permissions. DELETE
// /nodes/{node}/network/.. is the worked example: it becomes DELETE
// /nodes/{node}/network, Proxmox's "revert pending network config".
//
// The control-character refusal is not about the path — url.PathEscape encodes
// those — but about where the value goes AFTERWARDS. Callers put it in a
// TrackTask description and an audit row with %s, and view:audit is granted to
// every Viewer by default, so a newline or an ANSI escape smuggled into a name
// is text other people read. It became reachable when the API layer started
// percent-DECODING these path parameters: the declarations match the value as
// it arrives, and no pattern in the catalogue can see a control byte through a
// "%0A". The sibling below makes the same refusal.
func validatePathSegment(kind, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidInput, kind)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%w: %q is not a valid %s", ErrInvalidInput, value, kind)
	}
	if strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("%w: %s %q must not contain a path separator", ErrInvalidInput, kind, value)
	}
	if hasControlChar(value) {
		return fmt.Errorf("%w: %s %q contains a control character", ErrInvalidInput, kind, value)
	}
	return nil
}

// validatePathSegmentAllowingSlash is validatePathSegment for the one shape it
// cannot express: a value that becomes exactly one segment of a Proxmox request
// path after url.PathEscape, and whose own text legitimately contains a slash.
//
// A firewall IP set entry is the only such value in this client. Its id is a
// CIDR — "192.0.2.0/24" — and the entry lives at
// /cluster/firewall/ipset/{name}/{cidr}, so the slash has to reach Proxmox as
// %2F rather than be refused; that is exactly what PVE's own UI sends, and
// url.PathEscape produces it. The blanket separator ban validatePathSegment
// makes would turn every CIDR entry into a 400.
//
// The slash still cannot be waved through unchecked, because Proxmox decodes
// the escape before it resolves the path. The capture-server run that proved
// it is recorded on forbiddenVolumeIDChars (client_storage.go): a volume id is
// interpolated RAW, so "%2e%2e%2f" reached Proxmox byte-for-byte and decoded
// to "../" there. That measures the far side, which is the half that matters —
// an escape this client produces arrives at the same decoder. So the guard is
// the per-component one
// validateVolumeID makes — every "/"-delimited piece has to be a real name —
// which refuses "../.." while taking "192.0.2.0/24".
//
// A "%" needs no exclusion here, unlike in validateVolumeID: that value is
// interpolated raw, this one goes through url.PathEscape, which re-encodes the
// percent so it arrives as the literal character the caller meant.
func validatePathSegmentAllowingSlash(kind, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidInput, kind)
	}
	if strings.Contains(value, `\`) {
		return fmt.Errorf("%w: %s %q must not contain a backslash", ErrInvalidInput, kind, value)
	}
	if hasControlChar(value) {
		return fmt.Errorf("%w: %s %q contains a control character", ErrInvalidInput, kind, value)
	}
	components := strings.Split(value, "/")
	// ONE slash, which is what "AllowingSlash" means: a CIDR is "addr/len" and
	// an entry that is neither a CIDR nor an address is an alias name with no
	// slash at all. Refusing the rest is about DESCENT rather than ascent — the
	// per-component check below stops "../..", but "a/b/c/d" passes it and,
	// once Proxmox decodes the escapes, lands the request three levels deeper
	// than the one segment this value is positioned in.
	if len(components) > 2 {
		return fmt.Errorf("%w: %s %q has more than one path separator", ErrInvalidInput, kind, value)
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("%w: %s %q has a bad path segment %q", ErrInvalidInput, kind, value, component)
		}
	}
	return nil
}

// stringVal safely extracts a string from a map entry.
func stringVal(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// sdnZoneCreateToForm converts SDN zone create params to url.Values.
func sdnZoneCreateToForm(p CreateSDNZoneParams) url.Values {
	form := url.Values{}
	form.Set("zone", p.Zone)
	form.Set("type", p.Type)
	if p.Bridge != "" {
		form.Set("bridge", p.Bridge)
	}
	if p.Peers != "" {
		form.Set("peers", p.Peers)
	}
	if p.Nodes != "" {
		form.Set("nodes", p.Nodes)
	}
	if p.IPAM != "" {
		form.Set("ipam", p.IPAM)
	}
	if p.DNS != "" {
		form.Set("dns", p.DNS)
	}
	if p.ReverseDNS != "" {
		form.Set("reversedns", p.ReverseDNS)
	}
	if p.DNSZone != "" {
		form.Set("dnszone", p.DNSZone)
	}
	if p.VLANProtocol != "" {
		form.Set("vlan-protocol", p.VLANProtocol)
	}
	if p.Controller != "" {
		form.Set("controller", p.Controller)
	}
	if p.ExitNodes != "" {
		form.Set("exitnodes", p.ExitNodes)
	}
	if p.Mac != "" {
		form.Set("mac", p.Mac)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.MTU != 0 {
		form.Set("mtu", strconv.Itoa(p.MTU))
	}
	if p.VRFVxlan != 0 {
		form.Set("vrf-vxlan", strconv.Itoa(p.VRFVxlan))
	}
	if p.AdvSubnets != 0 {
		form.Set("advertise-subnets", strconv.Itoa(p.AdvSubnets))
	}
	if p.DisableArp != 0 {
		form.Set("disable-arp-nd-suppression", strconv.Itoa(p.DisableArp))
	}
	return form
}

// sdnZoneUpdateToForm converts SDN zone update params to url.Values.
func sdnZoneUpdateToForm(p UpdateSDNZoneParams) url.Values {
	form := url.Values{}
	if p.Bridge != "" {
		form.Set("bridge", p.Bridge)
	}
	if p.Peers != "" {
		form.Set("peers", p.Peers)
	}
	if p.Nodes != "" {
		form.Set("nodes", p.Nodes)
	}
	if p.IPAM != "" {
		form.Set("ipam", p.IPAM)
	}
	if p.DNS != "" {
		form.Set("dns", p.DNS)
	}
	if p.ReverseDNS != "" {
		form.Set("reversedns", p.ReverseDNS)
	}
	if p.DNSZone != "" {
		form.Set("dnszone", p.DNSZone)
	}
	if p.VLANProtocol != "" {
		form.Set("vlan-protocol", p.VLANProtocol)
	}
	if p.Controller != "" {
		form.Set("controller", p.Controller)
	}
	if p.ExitNodes != "" {
		form.Set("exitnodes", p.ExitNodes)
	}
	if p.Mac != "" {
		form.Set("mac", p.Mac)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.MTU != 0 {
		form.Set("mtu", strconv.Itoa(p.MTU))
	}
	if p.VRFVxlan != 0 {
		form.Set("vrf-vxlan", strconv.Itoa(p.VRFVxlan))
	}
	if p.AdvSubnets != 0 {
		form.Set("advertise-subnets", strconv.Itoa(p.AdvSubnets))
	}
	if p.DisableArp != 0 {
		form.Set("disable-arp-nd-suppression", strconv.Itoa(p.DisableArp))
	}
	return form
}

// sdnVNetCreateToForm converts SDN VNet create params to url.Values.
func sdnVNetCreateToForm(p CreateSDNVNetParams) url.Values {
	form := url.Values{}
	form.Set("vnet", p.VNet)
	form.Set("zone", p.Zone)
	if p.Alias != "" {
		form.Set("alias", p.Alias)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.VLANAware != 0 {
		form.Set("vlanaware", strconv.Itoa(p.VLANAware))
	}
	if p.Isolate != 0 {
		form.Set("isolate", strconv.Itoa(p.Isolate))
	}
	return form
}

// sdnVNetUpdateToForm converts SDN VNet update params to url.Values.
func sdnVNetUpdateToForm(p UpdateSDNVNetParams) url.Values {
	form := url.Values{}
	if p.Zone != "" {
		form.Set("zone", p.Zone)
	}
	if p.Alias != "" {
		form.Set("alias", p.Alias)
	}
	if p.Tag != 0 {
		form.Set("tag", strconv.Itoa(p.Tag))
	}
	if p.VLANAware != 0 {
		form.Set("vlanaware", strconv.Itoa(p.VLANAware))
	}
	if p.Isolate != 0 {
		form.Set("isolate", strconv.Itoa(p.Isolate))
	}
	return form
}

// sdnSubnetCreateToForm converts SDN subnet create params to url.Values.
func sdnSubnetCreateToForm(p CreateSDNSubnetParams) url.Values {
	form := url.Values{}
	form.Set("subnet", p.Subnet)
	if p.Gateway != "" {
		form.Set("gateway", p.Gateway)
	}
	if p.Type != "" {
		form.Set("type", p.Type)
	}
	if p.SNAT != 0 {
		form.Set("snat", strconv.Itoa(p.SNAT))
	}
	if p.DHCPRange != "" {
		form.Set("dhcp-range", p.DHCPRange)
	}
	if p.DHCPDNSServer != "" {
		form.Set("dhcp-dns-server", p.DHCPDNSServer)
	}
	return form
}

// sdnSubnetUpdateToForm converts SDN subnet update params to url.Values.
func sdnSubnetUpdateToForm(p UpdateSDNSubnetParams) url.Values {
	form := url.Values{}
	if p.Gateway != "" {
		form.Set("gateway", p.Gateway)
	}
	if p.SNAT != 0 {
		form.Set("snat", strconv.Itoa(p.SNAT))
	}
	if p.DHCPRange != "" {
		form.Set("dhcp-range", p.DHCPRange)
	}
	if p.DHCPDNSServer != "" {
		form.Set("dhcp-dns-server", p.DHCPDNSServer)
	}
	return form
}

// firewallRuleToForm converts a FirewallRuleParams to url.Values for the Proxmox API.
func firewallRuleToForm(rule FirewallRuleParams) url.Values {
	form := url.Values{}
	if rule.Type != "" {
		form.Set("type", rule.Type)
	}
	if rule.Action != "" {
		form.Set("action", rule.Action)
	}
	if rule.Source != "" {
		form.Set("source", rule.Source)
	}
	if rule.Dest != "" {
		form.Set("dest", rule.Dest)
	}
	if rule.Sport != "" {
		form.Set("sport", rule.Sport)
	}
	if rule.Dport != "" {
		form.Set("dport", rule.Dport)
	}
	if rule.Proto != "" {
		form.Set("proto", rule.Proto)
	}
	form.Set("enable", strconv.Itoa(rule.Enable))
	if rule.Comment != "" {
		form.Set("comment", rule.Comment)
	}
	if rule.Macro != "" {
		form.Set("macro", rule.Macro)
	}
	if rule.Log != "" {
		form.Set("log", rule.Log)
	}
	if rule.Iface != "" {
		form.Set("iface", rule.Iface)
	}
	return form
}

// firewallOptionsToForm converts FirewallOptions to url.Values.
func firewallOptionsToForm(opts FirewallOptions) url.Values {
	form := url.Values{}
	if opts.Enable != nil {
		form.Set("enable", strconv.Itoa(*opts.Enable))
	}
	if opts.PolicyIn != "" {
		form.Set("policy_in", opts.PolicyIn)
	}
	if opts.PolicyOut != "" {
		form.Set("policy_out", opts.PolicyOut)
	}
	if opts.LogLevelIn != "" {
		form.Set("log_level_in", opts.LogLevelIn)
	}
	if opts.LogLevelOut != "" {
		form.Set("log_level_out", opts.LogLevelOut)
	}
	return form
}

// isAgentNotRunning checks if the error indicates the QEMU guest agent is not running.
func isAgentNotRunning(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 500 {
		return strings.Contains(apiErr.Message, "QEMU guest agent is not running") ||
			strings.Contains(apiErr.Message, "guest agent") ||
			strings.Contains(apiErr.Message, "not running")
	}
	return false
}
