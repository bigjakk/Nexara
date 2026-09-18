package handlers

import (
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The Software Defined Networking routes: zones, VNets, subnets,
// controllers, IPAM and DNS plugins, and the apply that makes any of it
// real. The declarations are registerSDNEndpoints in
// internal/api/registry_sdn.go.
//
// Each of the six object kinds reads the same settings on create and on
// update, so each one has a single *FromParams helper below and the two
// handlers share it. Every key is read with a string literal so that
// registry_paramkey_guard_test.go can check it against each route's own
// schema — the reason internal/api/handlers/params.go takes values rather
// than keys everywhere else.
//
// HAZARD, named because nothing currently guards it: each Create* handler
// then COPIES that settings value field by field into the create struct,
// because proxmox's Create*Params and Update*Params are two flat structs
// rather than one embedding the other. Adding a setting means touching four
// places — both param structs, the declaration, and the *FromParams helper —
// and a fifth, the copy block, that no test covers:
// TestSDNBodiesDeclareEveryProxmoxParamField checks the SCHEMA declares every
// JSON tag, not that the handler carries it through, so a forgotten copy line
// drops the value on create only and every test still passes. All six copies
// were verified complete when this file was written. The shape that removes
// the failure mode is the one CreateNetworkInterfaceParams already uses —
// embedding the shared options struct — but CreateSDNVNetParams and
// UpdateSDNVNetParams both carry Zone, so embedding shadows a field there and
// it is a change to make deliberately rather than in passing.

// --- SDN Zones ---

// sdnZoneSettingsFromParams reads the sixteen settings a zone create and
// update share. The create adds zone and type; the update has neither,
// because proxmox.UpdateSDNZoneParams has neither.
func sdnZoneSettingsFromParams(p *apischema.Params) proxmox.UpdateSDNZoneParams {
	return proxmox.UpdateSDNZoneParams{
		Bridge:       p.String("bridge"),
		Tag:          int(p.Int("tag")),
		VLANProtocol: p.String("vlan-protocol"),
		Peers:        p.String("peers"),
		MTU:          int(p.Int("mtu")),
		Nodes:        p.String("nodes"),
		IPAM:         p.String("ipam"),
		DNS:          p.String("dns"),
		ReverseDNS:   p.String("reversedns"),
		DNSZone:      p.String("dnszone"),
		Controller:   p.String("controller"),
		VRFVxlan:     int(p.Int("vrf-vxlan")),
		ExitNodes:    p.String("exitnodes"),
		Mac:          p.String("mac"),
		AdvSubnets:   int(p.Int("advertise-subnets")),
		DisableArp:   int(p.Int("disable-arp-nd-suppression")),
	}
}

// ListSDNZones handles GET /api/v1/clusters/:cluster_id/sdn/zones.
func (h *NetworkHandler) ListSDNZones(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	zones, err := pxClient.GetSDNZones(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, zones)
}

// CreateSDNZone handles POST /api/v1/clusters/:cluster_id/sdn/zones.
func (h *NetworkHandler) CreateSDNZone(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	// zone and type are REQUIRED by the schema, which is the pair the
	// handler refused an empty value for.
	settings := sdnZoneSettingsFromParams(p)
	req := proxmox.CreateSDNZoneParams{
		Zone:         p.String("zone"),
		Type:         p.String("type"),
		Bridge:       settings.Bridge,
		Tag:          settings.Tag,
		VLANProtocol: settings.VLANProtocol,
		Peers:        settings.Peers,
		MTU:          settings.MTU,
		Nodes:        settings.Nodes,
		IPAM:         settings.IPAM,
		DNS:          settings.DNS,
		ReverseDNS:   settings.ReverseDNS,
		DNSZone:      settings.DNSZone,
		Controller:   settings.Controller,
		VRFVxlan:     settings.VRFVxlan,
		ExitNodes:    settings.ExitNodes,
		Mac:          settings.Mac,
		AdvSubnets:   settings.AdvSubnets,
		DisableArp:   settings.DisableArp,
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateSDNZone(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"zone": req.Zone, "type": req.Type})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", req.Zone, "sdn_zone_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNZone handles PUT /api/v1/clusters/:cluster_id/sdn/zones/:zone.
func (h *NetworkHandler) UpdateSDNZone(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	zone := p.String("zone")
	req := sdnZoneSettingsFromParams(p)

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateSDNZone(c.Context(), zone, req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"zone": zone})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", zone, "sdn_zone_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNZone handles DELETE /api/v1/clusters/:cluster_id/sdn/zones/:zone.
func (h *NetworkHandler) DeleteSDNZone(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	zone := p.String("zone")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteSDNZone(c.Context(), zone); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"zone": zone})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", zone, "sdn_zone_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- SDN VNets ---

// sdnVNetSettingsFromParams reads the four settings a VNet create and update
// share. zone is read separately: it is required on create and optional on
// update, which is what proxmox.UpdateSDNVNetParams already expressed by
// sending it only when non-empty.
func sdnVNetSettingsFromParams(p *apischema.Params) proxmox.UpdateSDNVNetParams {
	return proxmox.UpdateSDNVNetParams{
		Tag:       int(p.Int("tag")),
		Alias:     p.String("alias"),
		VLANAware: int(p.Int("vlanaware")),
		Isolate:   int(p.Int("isolate")),
	}
}

// ListSDNVNets handles GET /api/v1/clusters/:cluster_id/sdn/vnets.
func (h *NetworkHandler) ListSDNVNets(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	vnets, err := pxClient.GetSDNVNets(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, vnets)
}

// CreateSDNVNet handles POST /api/v1/clusters/:cluster_id/sdn/vnets.
func (h *NetworkHandler) CreateSDNVNet(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	settings := sdnVNetSettingsFromParams(p)
	req := proxmox.CreateSDNVNetParams{
		VNet:      p.String("vnet"),
		Zone:      p.String("zone"),
		Tag:       settings.Tag,
		Alias:     settings.Alias,
		VLANAware: settings.VLANAware,
		Isolate:   settings.Isolate,
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateSDNVNet(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": req.VNet, "zone": req.Zone})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", req.VNet, "sdn_vnet_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNVNet handles PUT /api/v1/clusters/:cluster_id/sdn/vnets/:vnet.
func (h *NetworkHandler) UpdateSDNVNet(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vnet := p.String("vnet")

	req := sdnVNetSettingsFromParams(p)
	// Optional on this route only; an omitted zone stays empty and the form
	// builder drops it, which is how "keep the current zone" is spelled.
	req.Zone = p.String("zone")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateSDNVNet(c.Context(), vnet, req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": vnet})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", vnet, "sdn_vnet_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNVNet handles DELETE /api/v1/clusters/:cluster_id/sdn/vnets/:vnet.
func (h *NetworkHandler) DeleteSDNVNet(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vnet := p.String("vnet")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteSDNVNet(c.Context(), vnet); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": vnet})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", vnet, "sdn_vnet_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- SDN Subnets ---

// sdnSubnetSettingsFromParams reads the four settings a subnet create and
// update share.
func sdnSubnetSettingsFromParams(p *apischema.Params) proxmox.UpdateSDNSubnetParams {
	return proxmox.UpdateSDNSubnetParams{
		Gateway:       p.String("gateway"),
		SNAT:          int(p.Int("snat")),
		DHCPRange:     p.String("dhcp-range"),
		DHCPDNSServer: p.String("dhcp-dns-server"),
	}
}

// ListSDNSubnets handles GET /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets.
func (h *NetworkHandler) ListSDNSubnets(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vnet := p.String("vnet")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	subnets, err := pxClient.GetSDNSubnets(c.Context(), vnet)
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, subnets)
}

// CreateSDNSubnet handles POST /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets.
func (h *NetworkHandler) CreateSDNSubnet(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vnet := p.String("vnet")

	settings := sdnSubnetSettingsFromParams(p)
	req := proxmox.CreateSDNSubnetParams{
		// Carries apischema's cidr format, so it is normalized before it
		// gets here — and the "subnet is required" check the handler used to
		// make is the schema's now.
		Subnet: p.String("subnet"),
		// The handler filled this in when the body left it empty; the
		// declaration states the same default instead, so the docs answer
		// what omitting it does.
		Type:          p.String("type"),
		Gateway:       settings.Gateway,
		SNAT:          settings.SNAT,
		DHCPRange:     settings.DHCPRange,
		DHCPDNSServer: settings.DHCPDNSServer,
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateSDNSubnet(c.Context(), vnet, req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": vnet, "subnet": req.Subnet})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", fmt.Sprintf("%s/%s", vnet, req.Subnet), "sdn_subnet_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNSubnet handles PUT /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets/:subnet.
func (h *NetworkHandler) UpdateSDNSubnet(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vnet, subnet := p.String("vnet"), p.String("subnet")
	req := sdnSubnetSettingsFromParams(p)

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateSDNSubnet(c.Context(), vnet, subnet, req); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": vnet, "subnet": subnet})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", fmt.Sprintf("%s/%s", vnet, subnet), "sdn_subnet_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNSubnet handles DELETE /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets/:subnet.
func (h *NetworkHandler) DeleteSDNSubnet(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	vnet, subnet := p.String("vnet"), p.String("subnet")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteSDNSubnet(c.Context(), vnet, subnet); err != nil {
		return mapProxmoxError(err)
	}

	details, _ := json.Marshal(map[string]string{"vnet": vnet, "subnet": subnet})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", fmt.Sprintf("%s/%s", vnet, subnet), "sdn_subnet_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// ApplySDN handles PUT /api/v1/clusters/:cluster_id/sdn/apply.
func (h *NetworkHandler) ApplySDN(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.ApplySDN(c.Context()); err != nil {
		return mapNamedOpError("apply SDN configuration", err)
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", "cluster", "sdn_applied", nil)

	return c.JSON(fiber.Map{"status": "ok"})
}

// --- SDN Controllers ---

// sdnControllerSettingsFromParams reads the nine settings a controller
// create and update share.
func sdnControllerSettingsFromParams(p *apischema.Params) proxmox.UpdateSDNControllerParams {
	return proxmox.UpdateSDNControllerParams{
		ASN:          int(p.Int("asn")),
		Peers:        p.String("peers"),
		Nodes:        p.String("nodes"),
		ISISDomain:   p.String("isis-domain"),
		ISISIfaces:   p.String("isis-ifaces"),
		ISISNET:      p.String("isis-net"),
		EBGPMultihop: int(p.Int("ebgp-multihop")),
		Loopback:     p.String("loopback"),
		Node:         p.String("node"),
	}
}

// ListSDNControllers handles GET /api/v1/clusters/:cluster_id/sdn/controllers.
func (h *NetworkHandler) ListSDNControllers(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	controllers, err := pxClient.GetSDNControllers(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, controllers)
}

// CreateSDNController handles POST /api/v1/clusters/:cluster_id/sdn/controllers.
func (h *NetworkHandler) CreateSDNController(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	settings := sdnControllerSettingsFromParams(p)
	req := proxmox.CreateSDNControllerParams{
		Controller:   p.String("controller"),
		Type:         p.String("type"),
		ASN:          settings.ASN,
		Peers:        settings.Peers,
		Nodes:        settings.Nodes,
		ISISDomain:   settings.ISISDomain,
		ISISIfaces:   settings.ISISIfaces,
		ISISNET:      settings.ISISNET,
		EBGPMultihop: settings.EBGPMultihop,
		Loopback:     settings.Loopback,
		Node:         settings.Node,
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateSDNController(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"controller": req.Controller, "type": req.Type})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", req.Controller, "sdn_controller_created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNController handles PUT /api/v1/clusters/:cluster_id/sdn/controllers/:controller.
func (h *NetworkHandler) UpdateSDNController(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	controller := p.String("controller")
	req := sdnControllerSettingsFromParams(p)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateSDNController(c.Context(), controller, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"controller": controller})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", controller, "sdn_controller_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNController handles DELETE /api/v1/clusters/:cluster_id/sdn/controllers/:controller.
func (h *NetworkHandler) DeleteSDNController(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	controller := p.String("controller")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteSDNController(c.Context(), controller); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"controller": controller})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", controller, "sdn_controller_deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- SDN IPAM plugins ---

// sdnIPAMSettingsFromParams reads the three settings an IPAM create and
// update share. token is a credential and is deliberately absent from every
// audit row these handlers write.
func sdnIPAMSettingsFromParams(p *apischema.Params) proxmox.UpdateSDNIPAMParams {
	return proxmox.UpdateSDNIPAMParams{
		URL:       p.String("url"),
		Token:     p.String("token"),
		SectionID: int(p.Int("section")),
	}
}

// ListSDNIPAMs handles GET /api/v1/clusters/:cluster_id/sdn/ipams.
func (h *NetworkHandler) ListSDNIPAMs(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	ipams, err := pxClient.GetSDNIPAMs(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, ipams)
}

// CreateSDNIPAM handles POST /api/v1/clusters/:cluster_id/sdn/ipams.
func (h *NetworkHandler) CreateSDNIPAM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	settings := sdnIPAMSettingsFromParams(p)
	req := proxmox.CreateSDNIPAMParams{
		IPAM:      p.String("ipam"),
		Type:      p.String("type"),
		URL:       settings.URL,
		Token:     settings.Token,
		SectionID: settings.SectionID,
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateSDNIPAM(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	// Name and type only: view:audit is granted to every Viewer by default,
	// and req.Token is a credential.
	details, _ := json.Marshal(map[string]string{"ipam": req.IPAM, "type": req.Type})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", req.IPAM, "sdn_ipam_created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNIPAM handles PUT /api/v1/clusters/:cluster_id/sdn/ipams/:ipam.
func (h *NetworkHandler) UpdateSDNIPAM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	ipam := p.String("ipam")
	req := sdnIPAMSettingsFromParams(p)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateSDNIPAM(c.Context(), ipam, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"ipam": ipam})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", ipam, "sdn_ipam_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNIPAM handles DELETE /api/v1/clusters/:cluster_id/sdn/ipams/:ipam.
func (h *NetworkHandler) DeleteSDNIPAM(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	ipam := p.String("ipam")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteSDNIPAM(c.Context(), ipam); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"ipam": ipam})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", ipam, "sdn_ipam_deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- SDN DNS plugins ---

// sdnDNSSettingsFromParams reads the two settings a DNS create and update
// share. key is a credential, for the reason sdnIPAMSettingsFromParams gives
// about token.
func sdnDNSSettingsFromParams(p *apischema.Params) proxmox.UpdateSDNDNSParams {
	return proxmox.UpdateSDNDNSParams{
		URL: p.String("url"),
		Key: p.String("key"),
	}
}

// ListSDNDNS handles GET /api/v1/clusters/:cluster_id/sdn/dns.
func (h *NetworkHandler) ListSDNDNS(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	plugins, err := pxClient.GetSDNDNSPlugins(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, plugins)
}

// CreateSDNDNS handles POST /api/v1/clusters/:cluster_id/sdn/dns.
func (h *NetworkHandler) CreateSDNDNS(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	settings := sdnDNSSettingsFromParams(p)
	req := proxmox.CreateSDNDNSParams{
		DNS:  p.String("dns"),
		Type: p.String("type"),
		URL:  settings.URL,
		Key:  settings.Key,
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateSDNDNS(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	// Name and type only: req.Key is a credential.
	details, _ := json.Marshal(map[string]string{"dns": req.DNS, "type": req.Type})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", req.DNS, "sdn_dns_created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateSDNDNS handles PUT /api/v1/clusters/:cluster_id/sdn/dns/:dns.
func (h *NetworkHandler) UpdateSDNDNS(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	dns := p.String("dns")
	req := sdnDNSSettingsFromParams(p)
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateSDNDNS(c.Context(), dns, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"dns": dns})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", dns, "sdn_dns_updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteSDNDNS handles DELETE /api/v1/clusters/:cluster_id/sdn/dns/:dns.
func (h *NetworkHandler) DeleteSDNDNS(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	dns := p.String("dns")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteSDNDNS(c.Context(), dns); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"dns": dns})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "sdn", dns, "sdn_dns_deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}
