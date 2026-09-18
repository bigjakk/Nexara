package handlers

import (
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// NetworkHandler handles network, firewall, and SDN endpoints.
type NetworkHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewNetworkHandler creates a new NetworkHandler.
func NewNetworkHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *NetworkHandler {
	return &NetworkHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

// createProxmoxClient creates a Proxmox client for the given cluster ID.
func (h *NetworkHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// --- Network Interface Endpoints ---

// ListNetworkInterfaces handles GET /api/v1/clusters/:cluster_id/networks.
func (h *NetworkHandler) ListNetworkInterfaces(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	nodes, err := h.queries.ListNodesByCluster(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list nodes")
	}

	type nodeIfaces struct {
		Node       string                     `json:"node"`
		Interfaces []proxmox.NetworkInterface `json:"interfaces"`
	}

	result := make([]nodeIfaces, 0, len(nodes))
	for _, node := range nodes {
		ifaces, err := pxClient.GetNetworkInterfaces(c.Context(), node.Name)
		if err != nil {
			continue
		}
		result = append(result, nodeIfaces{
			Node:       node.Name,
			Interfaces: ifaces,
		})
	}

	return RespondItems(c, result)
}

// ListNodeNetworkInterfaces handles GET /api/v1/clusters/:cluster_id/networks/:node_name.
func (h *NetworkHandler) ListNodeNetworkInterfaces(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	ifaces, err := pxClient.GetNetworkInterfaces(c.Context(), nodeName)
	if err != nil {
		return mapProxmoxError(err)
	}

	return RespondItems(c, ifaces)
}

// networkAuditSettings names the interface settings a request carries a value
// for, in a stable order. Only the names: view:audit is granted to every Viewer
// by default, while reading the interfaces themselves needs view:network, so an
// audit row must not become a way around that. Which knobs an operator touched
// is the useful part anyway.
func networkAuditSettings(o proxmox.NetworkInterfaceOptions) []string {
	set := make([]string, 0, 8)
	add := func(name string, present bool) {
		if present {
			set = append(set, name)
		}
	}
	add("address", o.Address != "")
	add("address6", o.Address6 != "")
	add("bond-primary", o.BondPrimary != "")
	add("bond_mode", o.BondMode != "")
	add("bond_xmit_hash_policy", o.BondXmitHashPolicy != "")
	add("bridge_fd", o.BridgeFD != "")
	add("bridge_ports", o.BridgePorts != "")
	add("bridge_stp", o.BridgeSTP != "")
	add("bridge_vids", o.BridgeVIDs != "")
	add("bridge_vlan_aware", o.BridgeVLANAware != 0)
	add("cidr", o.CIDR != "")
	add("cidr6", o.CIDR6 != "")
	add("comments", o.Comments != "")
	add("comments6", o.Comments6 != "")
	add("gateway", o.Gateway != "")
	add("gateway6", o.Gateway6 != "")
	add("mtu", o.MTU != 0)
	add("netmask", o.Netmask != "")
	add("netmask6", o.Netmask6 != "")
	add("ovs_bonds", o.OVSBonds != "")
	add("ovs_bridge", o.OVSBridge != "")
	add("ovs_options", o.OVSOptions != "")
	add("ovs_ports", o.OVSPorts != "")
	add("ovs_tag", o.OVSTag != 0)
	add("slaves", o.Slaves != "")
	add("vlan-id", o.VLANID != 0)
	add("vlan-raw-device", o.VLANRawDevice != "")
	return set
}

// networkInterfaceOptionsFromParams reads the thirty settings the create and
// the update body share, in the shape the Proxmox client takes. The
// declaration is networkInterfaceOptionParams in
// internal/api/registry_networks.go.
//
// Every key is read with a string literal so that
// registry_paramkey_guard_test.go can check it against each route's own
// schema — the reason internal/api/handlers/params.go takes values rather
// than keys everywhere else.
//
// autostart and bridge_vlan_aware are declared as BOOLEANS and land in an
// int field, because that is the round trip Proxmox itself makes: both are
// boolean parameters it spells 0/1, and networkIfaceOptionsToForm renders
// the int back into that spelling. The two are not symmetric, and the
// declarations say which is which — autostart is always sent, so false
// means "autostart=0"; bridge_vlan_aware is omitted when false, so false
// means "say nothing" and clearing it on an existing bridge goes through
// delete.
func networkInterfaceOptionsFromParams(p *apischema.Params) proxmox.NetworkInterfaceOptions {
	return proxmox.NetworkInterfaceOptions{
		Address:  p.String("address"),
		Netmask:  p.String("netmask"),
		Gateway:  p.String("gateway"),
		CIDR:     p.String("cidr"),
		Address6: p.String("address6"),
		Netmask6: p.String("netmask6"),
		Gateway6: p.String("gateway6"),
		CIDR6:    p.String("cidr6"),

		Autostart: boolToInt(p.Bool("autostart")),
		Comments:  p.String("comments"),
		Comments6: p.String("comments6"),
		MTU:       int(p.Int("mtu")),
		Method:    p.String("method"),
		Method6:   p.String("method6"),

		BridgePorts:     p.String("bridge_ports"),
		BridgeSTP:       p.String("bridge_stp"),
		BridgeFD:        p.String("bridge_fd"),
		BridgeVLANAware: boolToInt(p.Bool("bridge_vlan_aware")),
		BridgeVIDs:      p.String("bridge_vids"),

		Slaves:             p.String("slaves"),
		BondMode:           p.String("bond_mode"),
		BondXmitHashPolicy: p.String("bond_xmit_hash_policy"),
		BondPrimary:        p.String("bond-primary"),

		OVSBridge:  p.String("ovs_bridge"),
		OVSPorts:   p.String("ovs_ports"),
		OVSBonds:   p.String("ovs_bonds"),
		OVSOptions: p.String("ovs_options"),
		OVSTag:     int(p.Int("ovs_tag")),

		VLANID:        int(p.Int("vlan-id")),
		VLANRawDevice: p.String("vlan-raw-device"),
	}
}

// CreateNetworkInterface handles POST /api/v1/clusters/:cluster_id/networks/:node_name.
func (h *NetworkHandler) CreateNetworkInterface(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}

	// iface and type are REQUIRED by the schema on this route, which is the
	// pair the handler refused an empty value for, and type additionally
	// carries the CREATABLE enum — narrower than the editable one the PUT
	// route declares. proxmox.validateCreatableNetworkInterfaceType still
	// runs inside the client call, so a caller that reaches it another way
	// meets the same rule.
	req := proxmox.CreateNetworkInterfaceParams{
		Iface:                   p.String("iface"),
		Type:                    p.String("type"),
		NetworkInterfaceOptions: networkInterfaceOptionsFromParams(p),
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.CreateNetworkInterface(c.Context(), nodeName, req); err != nil {
		return mapNamedOpError("create network interface", err)
	}

	details, _ := json.Marshal(map[string]any{
		"node":     nodeName,
		"iface":    req.Iface,
		"type":     req.Type,
		"settings": networkAuditSettings(req.NetworkInterfaceOptions),
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", fmt.Sprintf("%s/%s", nodeName, req.Iface), "interface_created", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdateNetworkInterface handles PUT /api/v1/clusters/:cluster_id/networks/:node_name/:iface.
func (h *NetworkHandler) UpdateNetworkInterface(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	ifaceName := p.String("iface")

	req := proxmox.UpdateNetworkInterfaceParams{
		Type:                    p.String("type"),
		NetworkInterfaceOptions: networkInterfaceOptionsFromParams(p),
		// The schema's items enum is proxmox.DeletableNetworkInterfaceKeys,
		// so an entry outside the allow-list is a 400 naming it before this
		// runs. validateNetworkInterfaceDeleteKeys still runs inside
		// UpdateNetworkInterface, which is what keeps it a choke point.
		Delete: p.Strings("delete"),
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.UpdateNetworkInterface(c.Context(), nodeName, ifaceName, req); err != nil {
		return mapNamedOpError("update network interface", err)
	}

	details, _ := json.Marshal(map[string]any{
		"node":     nodeName,
		"iface":    ifaceName,
		"type":     req.Type,
		"settings": networkAuditSettings(req.NetworkInterfaceOptions),
		"cleared":  req.Delete,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", fmt.Sprintf("%s/%s", nodeName, ifaceName), "interface_updated", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteNetworkInterface handles DELETE /api/v1/clusters/:cluster_id/networks/:node_name/:iface.
func (h *NetworkHandler) DeleteNetworkInterface(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	ifaceName := p.String("iface")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.DeleteNetworkInterface(c.Context(), nodeName, ifaceName); err != nil {
		return mapNamedOpError("delete network interface", err)
	}

	details, _ := json.Marshal(map[string]string{"node": nodeName, "iface": ifaceName})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", fmt.Sprintf("%s/%s", nodeName, ifaceName), "interface_deleted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// ApplyNetworkConfig handles POST /api/v1/clusters/:cluster_id/networks/:node_name/apply.
func (h *NetworkHandler) ApplyNetworkConfig(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.ApplyNetworkConfig(c.Context(), nodeName); err != nil {
		return mapNamedOpError("apply network configuration", err)
	}

	details, _ := json.Marshal(map[string]string{"node": nodeName})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", nodeName, "network_applied", details)

	return c.JSON(fiber.Map{"status": "ok"})
}

// RevertNetworkConfig handles POST /api/v1/clusters/:cluster_id/networks/:node_name/revert.
func (h *NetworkHandler) RevertNetworkConfig(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	if err := pxClient.RevertNetworkConfig(c.Context(), nodeName); err != nil {
		return mapNamedOpError("revert network configuration", err)
	}

	details, _ := json.Marshal(map[string]string{"node": nodeName})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "network", nodeName, "network_reverted", details)

	return c.JSON(fiber.Map{"status": "ok"})
}
