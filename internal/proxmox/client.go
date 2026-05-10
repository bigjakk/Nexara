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

// validateNodeName rejects empty names and path traversal attempts.
func validateNodeName(node string) error {
	if node == "" {
		return fmt.Errorf("node name cannot be empty")
	}
	if strings.Contains(node, "/") || strings.Contains(node, "..") {
		return fmt.Errorf("invalid node name: %q", node)
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

