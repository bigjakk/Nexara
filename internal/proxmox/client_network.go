package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) GetNetworkBridges(ctx context.Context, node string) ([]NetworkInterface, error) {
	ifaces, err := c.GetNetworkInterfaces(ctx, node)
	if err != nil {
		return nil, err
	}
	bridges := make([]NetworkInterface, 0)
	for _, iface := range ifaces {
		if iface.Type == "bridge" {
			bridges = append(bridges, iface)
		}
	}
	return bridges, nil
}
func (c *Client) GetNetworkInterfaces(ctx context.Context, node string) ([]NetworkInterface, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/network"
	var ifaces []NetworkInterface
	if err := c.do(ctx, path, &ifaces); err != nil {
		return nil, fmt.Errorf("get network interfaces on %s: %w", node, err)
	}
	return ifaces, nil
}
func (c *Client) GetSDNZones(ctx context.Context) ([]SDNZone, error) {
	var zones []SDNZone
	if err := c.do(ctx, "/cluster/sdn/zones", &zones); err != nil {
		return nil, fmt.Errorf("get SDN zones: %w", err)
	}
	return zones, nil
}
func (c *Client) GetSDNVNets(ctx context.Context) ([]SDNVNet, error) {
	var vnets []SDNVNet
	if err := c.do(ctx, "/cluster/sdn/vnets", &vnets); err != nil {
		return nil, fmt.Errorf("get SDN vnets: %w", err)
	}
	return vnets, nil
}
func (c *Client) CreateSDNZone(ctx context.Context, params CreateSDNZoneParams) error {
	form := sdnZoneCreateToForm(params)
	if err := c.doPost(ctx, "/cluster/sdn/zones", form, nil); err != nil {
		return fmt.Errorf("create SDN zone %s: %w", params.Zone, err)
	}
	return nil
}
func (c *Client) UpdateSDNZone(ctx context.Context, zone string, params UpdateSDNZoneParams) error {
	form := sdnZoneUpdateToForm(params)
	path := "/cluster/sdn/zones/" + url.PathEscape(zone)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN zone %s: %w", zone, err)
	}
	return nil
}
func (c *Client) DeleteSDNZone(ctx context.Context, zone string) error {
	path := "/cluster/sdn/zones/" + url.PathEscape(zone)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN zone %s: %w", zone, err)
	}
	return nil
}
func (c *Client) CreateSDNVNet(ctx context.Context, params CreateSDNVNetParams) error {
	form := sdnVNetCreateToForm(params)
	if err := c.doPost(ctx, "/cluster/sdn/vnets", form, nil); err != nil {
		return fmt.Errorf("create SDN vnet %s: %w", params.VNet, err)
	}
	return nil
}
func (c *Client) UpdateSDNVNet(ctx context.Context, vnet string, params UpdateSDNVNetParams) error {
	form := sdnVNetUpdateToForm(params)
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN vnet %s: %w", vnet, err)
	}
	return nil
}
func (c *Client) DeleteSDNVNet(ctx context.Context, vnet string) error {
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN vnet %s: %w", vnet, err)
	}
	return nil
}
func (c *Client) GetSDNSubnets(ctx context.Context, vnet string) ([]SDNSubnet, error) {
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet) + "/subnets"
	var subnets []SDNSubnet
	if err := c.do(ctx, path, &subnets); err != nil {
		return nil, fmt.Errorf("get SDN subnets for %s: %w", vnet, err)
	}
	return subnets, nil
}
func (c *Client) CreateSDNSubnet(ctx context.Context, vnet string, params CreateSDNSubnetParams) error {
	form := sdnSubnetCreateToForm(params)
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet) + "/subnets"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create SDN subnet %s on %s: %w", params.Subnet, vnet, err)
	}
	return nil
}
func (c *Client) UpdateSDNSubnet(ctx context.Context, vnet string, subnet string, params UpdateSDNSubnetParams) error {
	form := sdnSubnetUpdateToForm(params)
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet) + "/subnets/" + url.PathEscape(subnet)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN subnet %s on %s: %w", subnet, vnet, err)
	}
	return nil
}
func (c *Client) DeleteSDNSubnet(ctx context.Context, vnet string, subnet string) error {
	path := "/cluster/sdn/vnets/" + url.PathEscape(vnet) + "/subnets/" + url.PathEscape(subnet)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN subnet %s on %s: %w", subnet, vnet, err)
	}
	return nil
}
func (c *Client) ApplySDN(ctx context.Context) error {
	if err := c.doPut(ctx, "/cluster/sdn", nil, nil); err != nil {
		return fmt.Errorf("apply SDN config: %w", err)
	}
	return nil
}
func (c *Client) GetSDNControllers(ctx context.Context) ([]SDNController, error) {
	var controllers []SDNController
	if err := c.do(ctx, "/cluster/sdn/controllers", &controllers); err != nil {
		return nil, fmt.Errorf("get SDN controllers: %w", err)
	}
	return controllers, nil
}
func (c *Client) CreateSDNController(ctx context.Context, params CreateSDNControllerParams) error {
	form := url.Values{}
	form.Set("controller", params.Controller)
	form.Set("type", params.Type)
	if params.ASN != 0 {
		form.Set("asn", strconv.Itoa(params.ASN))
	}
	if params.Peers != "" {
		form.Set("peers", params.Peers)
	}
	if params.Nodes != "" {
		form.Set("nodes", params.Nodes)
	}
	if params.ISISDomain != "" {
		form.Set("isis-domain", params.ISISDomain)
	}
	if params.ISISIfaces != "" {
		form.Set("isis-ifaces", params.ISISIfaces)
	}
	if params.ISISNET != "" {
		form.Set("isis-net", params.ISISNET)
	}
	if params.EBGPMultihop != 0 {
		form.Set("ebgp-multihop", strconv.Itoa(params.EBGPMultihop))
	}
	if params.Loopback != "" {
		form.Set("loopback", params.Loopback)
	}
	if params.Node != "" {
		form.Set("node", params.Node)
	}
	if err := c.doPost(ctx, "/cluster/sdn/controllers", form, nil); err != nil {
		return fmt.Errorf("create SDN controller %s: %w", params.Controller, err)
	}
	return nil
}
func (c *Client) UpdateSDNController(ctx context.Context, controller string, params UpdateSDNControllerParams) error {
	form := url.Values{}
	if params.ASN != 0 {
		form.Set("asn", strconv.Itoa(params.ASN))
	}
	if params.Peers != "" {
		form.Set("peers", params.Peers)
	}
	if params.Nodes != "" {
		form.Set("nodes", params.Nodes)
	}
	if params.ISISDomain != "" {
		form.Set("isis-domain", params.ISISDomain)
	}
	if params.ISISIfaces != "" {
		form.Set("isis-ifaces", params.ISISIfaces)
	}
	if params.ISISNET != "" {
		form.Set("isis-net", params.ISISNET)
	}
	if params.EBGPMultihop != 0 {
		form.Set("ebgp-multihop", strconv.Itoa(params.EBGPMultihop))
	}
	if params.Loopback != "" {
		form.Set("loopback", params.Loopback)
	}
	if params.Node != "" {
		form.Set("node", params.Node)
	}
	path := "/cluster/sdn/controllers/" + url.PathEscape(controller)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN controller %s: %w", controller, err)
	}
	return nil
}
func (c *Client) DeleteSDNController(ctx context.Context, controller string) error {
	path := "/cluster/sdn/controllers/" + url.PathEscape(controller)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN controller %s: %w", controller, err)
	}
	return nil
}
func (c *Client) GetSDNIPAMs(ctx context.Context) ([]SDNIPAM, error) {
	var ipams []SDNIPAM
	if err := c.do(ctx, "/cluster/sdn/ipams", &ipams); err != nil {
		return nil, fmt.Errorf("get SDN IPAMs: %w", err)
	}
	return ipams, nil
}
func (c *Client) CreateSDNIPAM(ctx context.Context, params CreateSDNIPAMParams) error {
	form := url.Values{}
	form.Set("ipam", params.IPAM)
	form.Set("type", params.Type)
	if params.URL != "" {
		form.Set("url", params.URL)
	}
	if params.Token != "" {
		form.Set("token", params.Token)
	}
	if params.SectionID != 0 {
		form.Set("section", strconv.Itoa(params.SectionID))
	}
	if err := c.doPost(ctx, "/cluster/sdn/ipams", form, nil); err != nil {
		return fmt.Errorf("create SDN IPAM %s: %w", params.IPAM, err)
	}
	return nil
}
func (c *Client) UpdateSDNIPAM(ctx context.Context, ipam string, params UpdateSDNIPAMParams) error {
	form := url.Values{}
	if params.URL != "" {
		form.Set("url", params.URL)
	}
	if params.Token != "" {
		form.Set("token", params.Token)
	}
	if params.SectionID != 0 {
		form.Set("section", strconv.Itoa(params.SectionID))
	}
	path := "/cluster/sdn/ipams/" + url.PathEscape(ipam)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN IPAM %s: %w", ipam, err)
	}
	return nil
}
func (c *Client) DeleteSDNIPAM(ctx context.Context, ipam string) error {
	path := "/cluster/sdn/ipams/" + url.PathEscape(ipam)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN IPAM %s: %w", ipam, err)
	}
	return nil
}
func (c *Client) GetSDNDNSPlugins(ctx context.Context) ([]SDNDNS, error) {
	var plugins []SDNDNS
	if err := c.do(ctx, "/cluster/sdn/dns", &plugins); err != nil {
		return nil, fmt.Errorf("get SDN DNS plugins: %w", err)
	}
	return plugins, nil
}
func (c *Client) CreateSDNDNS(ctx context.Context, params CreateSDNDNSParams) error {
	form := url.Values{}
	form.Set("dns", params.DNS)
	form.Set("type", params.Type)
	if params.URL != "" {
		form.Set("url", params.URL)
	}
	if params.Key != "" {
		form.Set("key", params.Key)
	}
	if err := c.doPost(ctx, "/cluster/sdn/dns", form, nil); err != nil {
		return fmt.Errorf("create SDN DNS %s: %w", params.DNS, err)
	}
	return nil
}
func (c *Client) UpdateSDNDNS(ctx context.Context, dns string, params UpdateSDNDNSParams) error {
	form := url.Values{}
	if params.URL != "" {
		form.Set("url", params.URL)
	}
	if params.Key != "" {
		form.Set("key", params.Key)
	}
	path := "/cluster/sdn/dns/" + url.PathEscape(dns)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update SDN DNS %s: %w", dns, err)
	}
	return nil
}
func (c *Client) DeleteSDNDNS(ctx context.Context, dns string) error {
	path := "/cluster/sdn/dns/" + url.PathEscape(dns)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete SDN DNS %s: %w", dns, err)
	}
	return nil
}

// networkIfaceOptionsToForm encodes the settings shared by the create and
// update calls. Only non-empty values are sent: Proxmox treats a present-but-
// empty parameter as a validation error for most keys, and clearing a value on
// an existing interface goes through UpdateNetworkInterfaceParams.Delete.
func networkIfaceOptionsToForm(form url.Values, o NetworkInterfaceOptions) {
	strs := map[string]string{
		"address":               o.Address,
		"netmask":               o.Netmask,
		"gateway":               o.Gateway,
		"cidr":                  o.CIDR,
		"address6":              o.Address6,
		"netmask6":              o.Netmask6,
		"gateway6":              o.Gateway6,
		"cidr6":                 o.CIDR6,
		"comments":              o.Comments,
		"comments6":             o.Comments6,
		"method":                o.Method,
		"method6":               o.Method6,
		"bridge_ports":          o.BridgePorts,
		"bridge_stp":            o.BridgeSTP,
		"bridge_fd":             o.BridgeFD,
		"bridge_vids":           o.BridgeVIDs,
		"slaves":                o.Slaves,
		"bond_mode":             o.BondMode,
		"bond_xmit_hash_policy": o.BondXmitHashPolicy,
		"bond-primary":          o.BondPrimary,
		"ovs_bridge":            o.OVSBridge,
		"ovs_ports":             o.OVSPorts,
		"ovs_bonds":             o.OVSBonds,
		"ovs_options":           o.OVSOptions,
		"vlan-raw-device":       o.VLANRawDevice,
	}
	for k, v := range strs {
		if v != "" {
			form.Set(k, v)
		}
	}

	ints := map[string]int{
		"mtu":     o.MTU,
		"ovs_tag": o.OVSTag,
		"vlan-id": o.VLANID,
		// Off is expressed by omitting the key, and turning it off on an
		// existing bridge goes through Delete — the same split Proxmox's own
		// dialog makes (uncheckedValue for autostart, deleteEmpty for this).
		"bridge_vlan_aware": o.BridgeVLANAware,
	}
	for k, v := range ints {
		if v != 0 {
			form.Set(k, strconv.Itoa(v))
		}
	}

	// autostart is the one flag always sent: Proxmox's checkbox uses an
	// unchecked value of 0, so 0 is how "do not start on boot" is expressed.
	form.Set("autostart", strconv.Itoa(o.Autostart))
}

func (c *Client) CreateNetworkInterface(ctx context.Context, node string, params CreateNetworkInterfaceParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateCreatableNetworkInterfaceType(params.Type); err != nil {
		return err
	}
	if err := validateNetworkInterfaceOptions(params.NetworkInterfaceOptions); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("iface", params.Iface)
	form.Set("type", params.Type)
	networkIfaceOptionsToForm(form, params.NetworkInterfaceOptions)
	path := "/nodes/" + url.PathEscape(node) + "/network"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		return fmt.Errorf("create network interface %s on %s: %w", params.Iface, node, err)
	}
	return nil
}

func (c *Client) UpdateNetworkInterface(ctx context.Context, node string, iface string, params UpdateNetworkInterfaceParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validatePathSegment("interface name", iface); err != nil {
		return err
	}
	if err := validateEditableNetworkInterfaceType(params.Type); err != nil {
		return err
	}
	if err := validateNetworkInterfaceOptions(params.NetworkInterfaceOptions); err != nil {
		return err
	}
	if err := validateNetworkInterfaceDeleteKeys(params.Delete); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("type", params.Type)
	networkIfaceOptionsToForm(form, params.NetworkInterfaceOptions)
	if len(params.Delete) > 0 {
		// No set-and-clear refusal here, unlike SetNodeACMEConfig. The two
		// endpoints order it oppositely: PVE::API2::Network's update_network
		// applies `delete` FIRST and assigns the parameters afterwards, so a
		// key in both ends up set; PVE/API2/NodeConfig.pm's set_options deletes
		// last, so there the set value is silently dropped and has to be
		// refused.
		form.Set("delete", strings.Join(params.Delete, ","))
	}
	path := "/nodes/" + url.PathEscape(node) + "/network/" + url.PathEscape(iface)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update network interface %s on %s: %w", iface, node, err)
	}
	return nil
}

func (c *Client) DeleteNetworkInterface(ctx context.Context, node string, iface string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	// Without this, iface=".." collapses the path to /nodes/{node}/network —
	// Proxmox's revert-pending-config endpoint, which Nexara gates behind a
	// different permission than this delete.
	if err := validatePathSegment("interface name", iface); err != nil {
		return err
	}
	path := "/nodes/" + url.PathEscape(node) + "/network/" + url.PathEscape(iface)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete network interface %s on %s: %w", iface, node, err)
	}
	return nil
}
func (c *Client) ApplyNetworkConfig(ctx context.Context, node string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	path := "/nodes/" + url.PathEscape(node) + "/network"
	if err := c.doPut(ctx, path, nil, nil); err != nil {
		return fmt.Errorf("apply network config on %s: %w", node, err)
	}
	return nil
}
func (c *Client) RevertNetworkConfig(ctx context.Context, node string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	path := "/nodes/" + url.PathEscape(node) + "/network"
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("revert network config on %s: %w", node, err)
	}
	return nil
}

// creatableNetworkInterfaceTypes are the interface types Proxmox's own UI
// offers under Create. The API's `type` enum is wider — it also accepts eth,
// alias, vlan, OVSPort, vnet, fabric and unknown — but those describe
// interfaces that already exist (a physical NIC, an OVS port implied by its
// bridge) rather than ones an operator creates by hand.
var creatableNetworkInterfaceTypes = map[string]bool{
	"bridge":     true,
	"bond":       true,
	"vlan":       true,
	"OVSBridge":  true,
	"OVSBond":    true,
	"OVSIntPort": true,
}

// editableNetworkInterfaceTypes is every type an existing interface may report:
// the creatable ones plus the ones that only ever come into being on their own.
// Editing is allowed for all of them — a physical NIC gets an address, an OVS
// port gets a VLAN tag. Derived from the creatable set rather than restated, so
// adding a creatable type cannot leave it uneditable.
var editableNetworkInterfaceTypes = func() map[string]bool {
	types := map[string]bool{
		"eth":     true,
		"alias":   true,
		"OVSPort": true,
		"vnet":    true,
		"fabric":  true,
		"unknown": true,
	}
	for t := range creatableNetworkInterfaceTypes {
		types[t] = true
	}
	return types
}()

// validateCreatableNetworkInterfaceType rejects a type POST cannot sensibly
// create.
//
// Of the three checks here this is the one PVE will not make for you: eth,
// alias, OVSPort, vnet, fabric and unknown are all in its own type enum
// ($network_type_enum in PVE::API2::Network), so a POST carrying one passes
// schema validation and create_network writes the stanza into the pending
// config and answers 200. Nothing surfaces until someone reads the interfaces
// back — or applies the pending config with the junk entry in it. OVSPort is
// the one that does not go quietly: create_network dies early for any OVS*
// type on a node without openvswitch-switch, which arrives as a 502.
func validateCreatableNetworkInterfaceType(t string) error {
	if !creatableNetworkInterfaceTypes[t] {
		return fmt.Errorf("%w: %q is not a network interface type that can be created", ErrInvalidInput, t)
	}
	return nil
}

// validateEditableNetworkInterfaceType rejects a type PUT does not accept. The
// edit set is deliberately wider than the create set — a physical NIC can still
// be given an address — but it is not open-ended.
func validateEditableNetworkInterfaceType(t string) error {
	if !editableNetworkInterfaceTypes[t] {
		return fmt.Errorf("%w: %q is not a known network interface type", ErrInvalidInput, t)
	}
	return nil
}

// deletableNetworkInterfaceKeys are the settings PUT may unset. Proxmox's
// `delete` parameter takes raw option names, so it is allow-listed rather than
// passed through: `delete=type` or `delete=iface` would corrupt the interface.
var deletableNetworkInterfaceKeys = map[string]bool{
	"address": true, "netmask": true, "gateway": true, "cidr": true,
	"address6": true, "netmask6": true, "gateway6": true, "cidr6": true,
	"comments": true, "comments6": true, "mtu": true,
	"bridge_ports": true, "bridge_stp": true, "bridge_fd": true,
	"bridge_vlan_aware": true, "bridge_vids": true,
	"slaves": true, "bond_mode": true, "bond_xmit_hash_policy": true, "bond-primary": true,
	"ovs_bridge": true, "ovs_ports": true, "ovs_bonds": true, "ovs_options": true, "ovs_tag": true,
	"vlan-id": true, "vlan-raw-device": true,
}

// validateNetworkInterfaceDeleteKeys checks every entry of an update's Delete
// list against the allow-list above.
//
// UpdateNetworkInterface calls this itself rather than leaving it to callers,
// so it is a choke point none of them can bypass. It is unexported to keep it
// one: an exported checker invites the opt-in shape it replaced, where a
// second caller that forgets the call silently gets the unguarded behaviour.
func validateNetworkInterfaceDeleteKeys(keys []string) error {
	for _, k := range keys {
		if !deletableNetworkInterfaceKeys[k] {
			return fmt.Errorf("%w: %q is not a network setting that can be cleared", ErrInvalidInput, k)
		}
	}
	return nil
}

// Proxmox's own bounds, echoed here so the value is rejected before the round
// trip. Unlike the create-type check above, PVE does enforce these itself —
// mtu carries minimum/maximum in the schema, so it answers 400 either way.
// Checking here names the bound that was missed rather than relaying Proxmox's
// wording for it.
const (
	minInterfaceMTU = 1280
	maxInterfaceMTU = 65520
	maxVLANTag      = 4094
)

// validateNetworkInterfaceOptions range-checks the numeric settings. Zero means
// "not set" for all three and is always allowed.
//
// Create and update both carry these options, so both call it: a check only one
// path ran would leave the other unguarded.
func validateNetworkInterfaceOptions(o NetworkInterfaceOptions) error {
	if o.MTU != 0 && (o.MTU < minInterfaceMTU || o.MTU > maxInterfaceMTU) {
		return fmt.Errorf("%w: MTU %d is outside %d-%d", ErrInvalidInput, o.MTU, minInterfaceMTU, maxInterfaceMTU)
	}
	for _, tag := range []struct {
		name  string
		value int
	}{{"ovs_tag", o.OVSTag}, {"vlan-id", o.VLANID}} {
		if tag.value != 0 && (tag.value < 1 || tag.value > maxVLANTag) {
			return fmt.Errorf("%w: %s %d is outside 1-%d", ErrInvalidInput, tag.name, tag.value, maxVLANTag)
		}
	}
	return nil
}
