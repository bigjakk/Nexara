package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
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
func (c *Client) CreateNetworkInterface(ctx context.Context, node string, params CreateNetworkInterfaceParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("iface", params.Iface)
	form.Set("type", params.Type)
	if params.Address != "" {
		form.Set("address", params.Address)
	}
	if params.Netmask != "" {
		form.Set("netmask", params.Netmask)
	}
	if params.Gateway != "" {
		form.Set("gateway", params.Gateway)
	}
	if params.CIDR != "" {
		form.Set("cidr", params.CIDR)
	}
	if params.BridgePorts != "" {
		form.Set("bridge_ports", params.BridgePorts)
	}
	if params.BridgeSTP != "" {
		form.Set("bridge_stp", params.BridgeSTP)
	}
	if params.BridgeFD != "" {
		form.Set("bridge_fd", params.BridgeFD)
	}
	if params.Comments != "" {
		form.Set("comments", params.Comments)
	}
	if params.Method != "" {
		form.Set("method", params.Method)
	}
	if params.Method6 != "" {
		form.Set("method6", params.Method6)
	}
	form.Set("autostart", strconv.Itoa(params.Autostart))
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
	form := url.Values{}
	form.Set("type", params.Type)
	if params.Address != "" {
		form.Set("address", params.Address)
	}
	if params.Netmask != "" {
		form.Set("netmask", params.Netmask)
	}
	if params.Gateway != "" {
		form.Set("gateway", params.Gateway)
	}
	if params.CIDR != "" {
		form.Set("cidr", params.CIDR)
	}
	if params.BridgePorts != "" {
		form.Set("bridge_ports", params.BridgePorts)
	}
	if params.BridgeSTP != "" {
		form.Set("bridge_stp", params.BridgeSTP)
	}
	if params.BridgeFD != "" {
		form.Set("bridge_fd", params.BridgeFD)
	}
	if params.Comments != "" {
		form.Set("comments", params.Comments)
	}
	if params.Method != "" {
		form.Set("method", params.Method)
	}
	if params.Method6 != "" {
		form.Set("method6", params.Method6)
	}
	form.Set("autostart", strconv.Itoa(params.Autostart))
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
