package api

import (
	"slices"
	"strconv"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// globalCheck, clusterParams, nodeParams, withParams, optFlag and optString
// — lives in registry_vms.go, where the first migrated domain defined it.
//
// NetworkHandler serves 66 routes, more than any other handler in the tree,
// so they are declared across four files rather than one, grouped by the
// resource they act on:
//
//	registry_networks.go            7   a node's own interfaces
//	registry_firewall.go           28   cluster + guest firewall, aliases,
//	                                    IP sets, security groups, the log
//	registry_sdn.go                25   zones, VNets, subnets, controllers,
//	                                    IPAMs, DNS plugins, apply
//	registry_firewall_templates.go  6   Nexara's own rule templates
//
// Each file registers its own function and carries its own count in
// registryDomainRouteCounts, so a route that moves between them cannot hide
// inside one big total.

// networkScope is the collection every node-interface route hangs off.
// :cluster_id is the FIRST path parameter, which namesACluster
// (permissions.go) requires of a cluster-scoped Check.
const networkScope = clusterScope + "/networks"

// pveObjectNamePattern is the shape every caller-supplied Proxmox object
// NAME in this domain must have — a firewall alias, an IP set, a security
// group, an SDN zone, VNet, subnet, controller, IPAM or DNS plugin.
//
// It exists because these values become a SEGMENT of a Proxmox request path
// (url.PathEscape, in internal/proxmox/client_firewall.go and
// client_network.go) and almost none of them goes through
// proxmox.validatePathSegment on the way. url.PathEscape escapes "/" but
// leaves "." and ".." alone, so an un-anchored name resolves upward once
// pveproxy normalises the path and lands the request on the PARENT
// collection — POST .../ipset/.. creates an IP set instead of adding an
// entry to one, and PUT .../sdn/zones/.. is the SDN apply endpoint. Every
// one of those is reachable with the same permission the intended call
// needs, so this is a correctness anchor rather than an escalation fix, but
// an operation that silently does something else is not a thing to leave
// declarable.
//
// Requiring a leading alphanumeric is what keeps "." and ".." out, which RE2
// cannot express as a negative lookahead; the class excludes both
// separators. It is deliberately WIDER than Proxmox's own formats for these
// ids (pve-fw-alias-name is [A-Za-z][A-Za-z0-9_-]*, pve-sdn-zone-id is
// [a-z][a-z0-9]* capped at 8) — every one of them is a subset of this, so
// nothing Proxmox would accept is refused here, and an object created
// outside Nexara cannot become undeletable through it.
const pveObjectNamePattern = `^[A-Za-z0-9][A-Za-z0-9._-]*$`

// pveObjectNameParam is one such name, with a description of its own.
//
// The 64-character cap is Proxmox's own ceiling for the longest of these
// (pve-fw-alias-name and pve-fw-ipset-name); every other id in this domain is
// capped far shorter by Proxmox itself, so one bound serves them all and is
// here to keep a path segment finite rather than to re-state a per-object
// limit. The one id that needs a WIDER class rather than a wider cap —
// an SDN subnet's derived id, which carries an IPv6 address's colons — is
// sdnSubnetIDParam in registry_sdn.go.
func pveObjectNameParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Pattern:     pveObjectNamePattern,
		MaxLength:   apischema.Ptr(64),
		Typetext:    "<name>",
		Description: description,
	}
}

// ifaceParam is a network interface name.
//
// One property serves both routes that take one, and their sources differ
// without the declaration saying so: on PUT/DELETE the path spells :iface,
// so ResolveSource reads it from the path; on POST the path does not, so it
// is read from the body. That is the whole point of leaving Source at Auto.
//
// The colon is in the class because PVE's `alias` interface type is named
// that way ("eth0:0"), and the dot because a VLAN on a bridge is "vmbr0.100".
// proxmox.DeleteNetworkInterface and UpdateNetworkInterface still run
// validatePathSegment on the value; this states the rule one layer earlier
// and names the field.
var ifaceParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     `^[A-Za-z0-9][A-Za-z0-9.:_-]*$`,
	MaxLength:   apischema.Ptr(64),
	Typetext:    "<name>",
	Description: "Interface name as the node reports it, e.g. vmbr0, bond0 or vmbr0.100.",
}

// interfaceTagParam is an 802.1Q VLAN tag on an interface: ovs_tag and
// vlan-id, which share a range and a sentinel.
//
// ZERO is the sentinel, and it is why the Minimum is 0 rather than 1:
// networkIfaceOptionsToForm omits a key whose value is 0, so 0 has always
// meant "do not send this", and a Minimum of 1 would 400 a caller who has
// been spelling "unset" that way since the endpoint shipped. The real range
// is 1..4094, which proxmox.validateNetworkInterfaceOptions still enforces —
// it is the half of the rule a single numeric bound cannot express.
func interfaceTagParam(description string) apischema.Property {
	return apischema.Property{
		Type:     apischema.Integer,
		Optional: true,
		Default:  0,
		Minimum:  apischema.Ptr(0.0),
		Maximum:  apischema.Ptr(float64(proxmox.MaxVLANTag)),
		Typetext: "<integer>",
		Description: description + " 1 to " + strconv.Itoa(proxmox.MaxVLANTag) +
			"; 0 or omitted leaves it unset.",
	}
}

// networkInterfaceOptionParams are the thirty settings the create and the
// update body share, one per field of proxmox.NetworkInterfaceOptions.
//
// All thirty are declared even though Nexara's own dialog sends fewer: the
// layer this replaces bound the body to that struct with c.Bind().Body, so
// every JSON tag on it has always been an accepted key, and a schema that
// left one out would turn a working request into "unknown parameter".
//
// None of the string settings carries a FORMAT, and that is deliberate
// rather than lazy. Three reasons, in order of how much they would cost:
//
//   - The empty string has to survive. networkIfaceOptionsToForm drops a
//     setting whose value is "", so `{"gateway": ""}` has always meant "do
//     not send a gateway" — and every registered format rejects "", so
//     borrowing the ip or cidr format would 400 a request that has always
//     worked. This is the same trap the node DNS write documents.
//   - Which settings are even legal depends on the interface TYPE, which is
//     a cross-field rule no per-parameter facet can state.
//   - Proxmox owns these vocabularies and versions them (bridge_vids takes
//     ranges, ovs_options is a free-form OVS option string), so a pattern
//     here would date and reject a value the cluster in front of the
//     operator accepts.
//
// What the schema owes them instead is a CLOSED SET and a length bound: a
// misspelled key now comes back as "unknown parameter" rather than silently
// creating an interface that ignores half the request.
func networkInterfaceOptionParams() apischema.Properties {
	return apischema.Properties{
		// ── IPv4 ──────────────────────────────────────────────────────
		"address": optString(64, "<ip>", "IPv4 address, when netmask carries the prefix separately. Empty leaves it unset."),
		"netmask": optString(64, "<netmask>", "IPv4 netmask that goes with address. Empty leaves it unset."),
		"cidr":    optString(64, "<ip/prefix>", "IPv4 address with its prefix, e.g. 192.0.2.10/24. The form Nexara's dialog sends. Empty leaves it unset."),
		"gateway": optString(64, "<ip>", "IPv4 default gateway. Empty leaves it unset."),

		// ── IPv6 ──────────────────────────────────────────────────────
		"address6": optString(64, "<ip>", "IPv6 address, when netmask6 carries the prefix separately. Empty leaves it unset."),
		"netmask6": optString(64, "<prefix>", "IPv6 prefix length that goes with address6. Empty leaves it unset."),
		"cidr6":    optString(64, "<ip/prefix>", "IPv6 address with its prefix. Empty leaves it unset."),
		"gateway6": optString(64, "<ip>", "IPv6 default gateway. Empty leaves it unset."),

		// ── General ───────────────────────────────────────────────────
		"autostart": {
			Type:     apischema.Boolean,
			Optional: true,
			// The one setting ALWAYS sent to Proxmox, so its default is
			// load-bearing rather than cosmetic: networkIfaceOptionsToForm
			// writes autostart unconditionally, and Proxmox's own checkbox
			// spells "do not start on boot" as the explicit 0. Omitting it
			// therefore means autostart=0 on the wire, which is what the
			// Go zero value meant before this declaration existed — so the
			// default states it rather than changing it.
			Default:  false,
			Typetext: "<boolean>",
			Description: "Bring the interface up at boot. ALWAYS sent to Proxmox, so omitting it " +
				"turns autostart OFF rather than leaving it as it was.",
		},
		"comments":  optString(1024, "<string>", "Free-text note stored beside the IPv4 stanza."),
		"comments6": optString(1024, "<string>", "Free-text note stored beside the IPv6 stanza."),
		"mtu": {
			Type:     apischema.Integer,
			Optional: true,
			// 0 is "not set" (the key is omitted from the form), so the
			// floor is 0 and NOT proxmox.MinInterfaceMTU. The real rule is
			// "0, or 1280..65520", which one Minimum cannot say — the 1280
			// floor stays in proxmox.validateNetworkInterfaceOptions, which
			// both the create and the update path call.
			Default:  0,
			Minimum:  apischema.Ptr(0.0),
			Maximum:  apischema.Ptr(float64(proxmox.MaxInterfaceMTU)),
			Typetext: "<integer>",
			Description: "Interface MTU, " + strconv.Itoa(proxmox.MinInterfaceMTU) + " to " +
				strconv.Itoa(proxmox.MaxInterfaceMTU) + ". 0 or omitted leaves Proxmox's default.",
		},
		"method":  optString(32, "<static|dhcp|manual|auto>", "IPv4 configuration method. Empty leaves it unset."),
		"method6": optString(32, "<static|dhcp|manual|auto>", "IPv6 configuration method. Empty leaves it unset."),

		// ── Linux bridge ──────────────────────────────────────────────
		"bridge_ports": optString(512, "<iface>[ <iface>...]", "Space-separated member interfaces of a Linux bridge."),
		"bridge_stp":   optString(16, "<on|off>", "Spanning tree on a Linux bridge."),
		"bridge_fd":    optString(16, "<seconds>", "Bridge forward delay in seconds."),
		"bridge_vlan_aware": {
			Type:     apischema.Boolean,
			Optional: true,
			// false is expressed by OMITTING the key, not by sending 0 —
			// networkIfaceOptionsToForm drops a zero here, and turning it
			// off on an existing bridge goes through `delete` instead. The
			// default therefore restates the existing behaviour: a body
			// without it, and a body with false, both send nothing.
			Default:  false,
			Typetext: "<boolean>",
			Description: "Make a Linux bridge VLAN aware. false is sent as ABSENT rather than as 0, " +
				"so turning it off on an existing bridge means naming bridge_vlan_aware in delete.",
		},
		"bridge_vids": optString(512, "<vlanid[-vlanid]>[ ...]", "VLAN IDs or ranges a VLAN-aware bridge carries."),

		// ── Linux bond ────────────────────────────────────────────────
		"slaves":                optString(512, "<iface>[ <iface>...]", "Space-separated member interfaces of a Linux bond."),
		"bond_mode":             optString(64, "<balance-rr|active-backup|...|lacp-balance-tcp>", "Bonding mode. Linux and OVS bonds take DISJOINT vocabularies, which is why this carries no enum."),
		"bond_xmit_hash_policy": optString(32, "<layer2|layer2+3|layer3+4>", "Transmit hash policy, for the bonding modes that hash."),
		"bond-primary":          optString(64, "<iface>", "Preferred member interface, for active-backup bonding."),

		// ── Open vSwitch ──────────────────────────────────────────────
		"ovs_bridge":  optString(64, "<iface>", "OVS bridge this port or bond attaches to."),
		"ovs_ports":   optString(512, "<iface>[ <iface>...]", "Space-separated ports of an OVS bridge."),
		"ovs_bonds":   optString(512, "<iface>[ <iface>...]", "Space-separated members of an OVS bond."),
		"ovs_options": optString(1024, "<string>", "Raw Open vSwitch options, passed through verbatim."),
		"ovs_tag":     interfaceTagParam("VLAN tag for an OVS port or bond."),

		// ── Linux VLAN ────────────────────────────────────────────────
		"vlan-id":         interfaceTagParam("VLAN ID for a Linux VLAN interface."),
		"vlan-raw-device": optString(64, "<iface>", "Interface a Linux VLAN is built on."),
	}
}

// createNetworkInterfaceParams is the POST body: the shared settings plus
// the two fields the handler already refused an empty value for.
func createNetworkInterfaceParams() apischema.Properties {
	return withParams(networkInterfaceOptionParams(), apischema.Properties{
		"iface": ifaceParam,
		"type": {
			Type: apischema.String,
			// Cloned rather than aliased, for the reason the node service
			// action's enum gives: Property.Enum is only deep-copied on the
			// StdOption path, so sharing the package-level slice would give
			// every Server's schema the same backing array.
			Enum:      slices.Clone(proxmox.CreatableNetworkInterfaceTypes),
			MaxLength: apischema.Ptr(32),
			Typetext:  "<bridge|bond|vlan|OVSBridge|OVSBond|OVSIntPort>",
			Description: "Kind of interface to create. Deliberately NARROWER than the set an existing " +
				"interface may report: Proxmox's own type enum also accepts eth, alias, OVSPort, vnet, " +
				"fabric and unknown, and a POST carrying one of those writes a junk stanza into the " +
				"pending config and answers 200.",
		},
	})
}

// updateNetworkInterfaceParams is the PUT body.
//
// It differs from the create body in exactly two ways, and BOTH are what the
// client layer already enforced rather than anything introduced here:
//
//   - type takes the WIDER editable set. An interface that already exists
//     may be a physical NIC or an OVS port, and giving one an address is a
//     legitimate edit even though neither can be created.
//   - delete is only here. It is the ONLY way to clear a setting: Proxmox
//     ignores a present-but-empty value, so an emptied gateway silently
//     stays put unless its key is named here.
//
// The two type sets are deliberately not unified — see
// proxmox.CreatableNetworkInterfaceTypes.
func updateNetworkInterfaceParams() apischema.Properties {
	return withParams(networkInterfaceOptionParams(), apischema.Properties{
		"iface": ifaceParam,
		"type": {
			Type:      apischema.String,
			Enum:      slices.Clone(proxmox.EditableNetworkInterfaceTypes),
			MaxLength: apischema.Ptr(32),
			Typetext:  "<bridge|bond|vlan|OVSBridge|OVSBond|OVSIntPort|eth|alias|OVSPort|vnet|fabric|unknown>",
			Description: "The interface's own type, as the interface listing reports it. Required: " +
				"Proxmox rewrites the stanza from scratch and needs to know what it is writing.",
		},
		"delete": {
			Type:     apischema.Array,
			Optional: true,
			Items: &apischema.Property{
				Type: apischema.String,
				// The allow-list the client already applied, stated one
				// layer earlier so the rejection names the offending entry.
				// It is an allow-list rather than a pass-through because
				// Proxmox's `delete` takes raw option names: delete=type or
				// delete=iface would corrupt the interface.
				Enum: slices.Clone(proxmox.DeletableNetworkInterfaceKeys),
			},
			// One entry per clearable setting. A longer list can only be
			// repeating itself, and Proxmox joins them into a single form
			// value.
			MaxLength: apischema.Ptr(len(proxmox.DeletableNetworkInterfaceKeys)),
			Typetext:  "<setting>[,<setting>...]",
			Description: "Settings to UNSET on this interface. Sending an empty value for a setting " +
				"does not clear it — Proxmox ignores an empty parameter — so clearing one means naming " +
				"it here. Proxmox applies delete BEFORE the assignments, so a key in both ends up set.",
		},
	})
}

// registerNetworkInterfaceEndpoints declares the 7 routes that act on a
// node's own network configuration.
//
// Every one of them is the uniform shape: resolve the cluster from the path,
// then one static requireClusterPerm. So all 7 declare a plain
// cluster-scoped Check and every hand-placed call is gone from the handler
// bodies — nothing here is Deferred, Advisory, Public, SelfService or
// global.
//
// The permission split is the one the handlers already had and is worth
// naming because it is not uniform: reading is view:network, creating and
// editing are manage:network, and DELETE is delete:network — the only route
// of the seven that needs the third verb. apply and revert are manage rather
// than delete even though revert DISCARDS the pending configuration: what it
// throws away is an unapplied edit, not an interface.
func registerNetworkInterfaceEndpoints(reg *Registry, h *handlers.NetworkHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   networkScope,
		Description: "List every node's live network interfaces, one entry per node. A node Proxmox " +
			"cannot answer for is omitted rather than failing the whole listing.",
		Group:       "Networks",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListNetworkInterfaces,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        networkScope + "/:node_name",
		Description: "List one node's live network interfaces, including any pending, unapplied changes.",
		Group:       "Networks",
		Permissions: clusterCheck("view", "network"),
		Parameters:  nodeParams(nil),
		Handler:     h.ListNodeNetworkInterfaces,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   networkScope + "/:node_name",
		Description: "Create a network interface on a node. The change lands in the node's PENDING " +
			"configuration and does nothing until apply runs.",
		Group:       "Networks",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  nodeParams(createNetworkInterfaceParams()),
		Handler:     h.CreateNetworkInterface,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   networkScope + "/:node_name/:iface",
		Description: "Change a network interface. A setting left out keeps its current value; clearing " +
			"one means naming it in delete. Lands in the pending configuration until apply runs.",
		Group:       "Networks",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  nodeParams(updateNetworkInterfaceParams()),
		Handler:     h.UpdateNetworkInterface,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        networkScope + "/:node_name/:iface",
		Description: "Remove a network interface. Lands in the pending configuration until apply runs.",
		Group:       "Networks",
		Permissions: clusterCheck("delete", "network"),
		Parameters:  nodeParams(apischema.Properties{"iface": ifaceParam}),
		Handler:     h.DeleteNetworkInterface,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   networkScope + "/:node_name/apply",
		Description: "Apply a node's pending network configuration. This reloads the node's networking " +
			"and can cut the node off if the pending change is wrong.",
		Group:       "Networks",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  nodeParams(nil),
		Handler:     h.ApplyNetworkConfig,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   networkScope + "/:node_name/revert",
		Description: "Discard a node's pending network configuration, leaving the running one alone. " +
			"manage:network rather than delete:network: what it throws away is an unapplied edit.",
		Group:       "Networks",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  nodeParams(nil),
		Handler:     h.RevertNetworkConfig,
	})
}
