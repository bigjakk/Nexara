package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The shared declaration vocabulary lives in registry_vms.go; the four-file
// split of NetworkHandler's 66 routes is explained in registry_networks.go,
// which also owns pveObjectNameParam — the path-segment anchor every id
// below uses.

// sdnScope is the collection every SDN route hangs off. :cluster_id is the
// FIRST path parameter, which namesACluster (permissions.go) requires of a
// cluster-scoped Check.
const sdnScope = clusterScope + "/sdn"

// maxVXLANVNI is the widest identifier any `tag` in this domain carries: a
// VXLAN VNI is 24 bits, while a VLAN tag is 12. Which of the two a given
// zone or VNet takes depends on the ZONE TYPE, so the schema can only state
// the looser bound — Proxmox applies the type-specific one, and a tag that
// is legal for a VXLAN zone must not be refused here.
const maxVXLANVNI = 16777215

// sdnSubnetIDParam is a subnet's id as a PATH parameter — the dashed string
// Proxmox DERIVES from the CIDR when the subnet is created, e.g.
// "myzone-192.0.2.0-24". It is not the CIDR the create body takes.
//
// It is pveObjectNameParam's class plus the COLON, and that is the one place
// in this domain where the shared anchor is too tight. An IPv6 subnet's
// derived id carries the address's colons, and refusing them would make an
// existing IPv6 subnet un-editable and un-deletable through this API —
// exactly the failure the pve-object-id entry says the anchor exists to
// avoid. A colon is not a path separator and cannot introduce a traversal,
// and the leading-alphanumeric anchor still keeps "." and ".." out.
//
// The widened rule is the catalogue's pve-object-id-colon, shared with
// ifaceParam (registry_networks.go), which reaches the same class from the
// other direction — an alias interface is named "eth0:0".
var sdnSubnetIDParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     apischema.Rule("pve-object-id-colon"),
	MaxLength:   apischema.Ptr(96),
	Typetext:    "<id>",
	Description: "Subnet id, as the subnet listing reports it — Proxmox derives it from the CIDR.",
}

// sdnZeroMeansUnsetParam is an optional integer whose ZERO is the "do not
// send this key" sentinel.
//
// Every numeric SDN setting works that way: the form builders in
// internal/proxmox/client.go test `if p.X != 0` before writing the field, so
// 0 has always meant "leave it to Proxmox". A Minimum of 1 would therefore
// 400 a caller who has been spelling "unset" that way since the endpoint
// shipped. What the Minimum of 0 DOES close is a negative value, which the
// struct binder passed straight through to Proxmox.
func sdnZeroMeansUnsetParam(maxValue float64, description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.Integer,
		Optional:    true,
		Default:     0,
		Minimum:     apischema.Ptr(0.0),
		Maximum:     apischema.Ptr(maxValue),
		Typetext:    "<integer>",
		Description: description + " 0 or omitted leaves it unset.",
	}
}

// sdnFlagParam is sdnZeroMeansUnsetParam for the settings Proxmox spells as
// a 0/1 boolean. They are declared as INTEGERS rather than booleans because
// the zero is a sentinel with a third meaning here — the key is omitted
// entirely — and because that is the shape the listing hands back, which an
// edit form sends straight in again.
//
// The ceiling of 1 is deliberate and is NOT what the firewall rule `enable`
// field does two files over: that one is left open to the int32 range
// because Proxmox treats it as a counter where anything above 0 enables (see
// firewallRuleBody in registry_firewall.go). These are plain PVE booleans, so
// 2 is a value Proxmox would reject and there is nothing to relay.
func sdnFlagParam(description string) apischema.Property {
	return sdnZeroMeansUnsetParam(1, description)
}

// sdnTypeParam is the plugin type of a zone, controller, IPAM or DNS entry.
//
// No Enum, for the reason the node ZFS raidlevel gives: Proxmox owns this
// vocabulary and versions it — EVPN zones arrived after VLAN ones, PVE 9
// added fabric — so a copy here would date and reject a type the cluster in
// front of the operator accepts.
func sdnTypeParam(typetext, description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		MinLength:   apischema.Ptr(1),
		MaxLength:   apischema.Ptr(32),
		Typetext:    typetext,
		Description: description,
	}
}

// sdnZoneSettings are the sixteen settings a zone create and a zone update
// share, one per optional field of proxmox.CreateSDNZoneParams.
//
// The update body is exactly this set and the create body is this set plus
// zone and type, which mirrors UpdateSDNZoneParams being CreateSDNZoneParams
// without its two identifiers — and mirrors what the dialog does, which
// builds one object and strips those two keys for an edit.
//
// None of the strings carries a format, for the reason the network
// interface settings give at length: the EMPTY STRING is how "leave this
// unset" is spelled (the form builders drop an empty value), and every
// registered format rejects "". mac is the one that looks like it should
// borrow mac-addr and cannot.
func sdnZoneSettings() apischema.Properties {
	return apischema.Properties{
		"bridge":        optString(64, "<iface>", "Linux or OVS bridge a VLAN or QinQ zone runs on."),
		"tag":           sdnZeroMeansUnsetParam(maxVXLANVNI, "Service-VLAN tag for a QinQ zone."),
		"vlan-protocol": optString(32, "<802.1q|802.1ad>", "VLAN encapsulation for a QinQ zone."),
		"peers":         optString(512, "<ip>[,<ip>...]", "Peer addresses for a VXLAN or EVPN zone."),
		"mtu":           sdnZeroMeansUnsetParam(float64(proxmox.MaxInterfaceMTU), "MTU for the zone's interfaces."),
		"nodes":         optString(512, "<node>[,<node>...]", "Nodes the zone is deployed on. Empty means all of them."),
		"ipam":          optString(64, "<ipam>", "IPAM plugin that allocates addresses in this zone."),
		"dns":           optString(64, "<dns>", "DNS plugin that registers records for this zone."),
		"reversedns":    optString(64, "<dns>", "DNS plugin for reverse records."),
		"dnszone":       optString(255, "<domain>", "DNS zone records are registered under."),
		"controller":    optString(64, "<controller>", "SDN controller that drives an EVPN zone."),
		"vrf-vxlan":     sdnZeroMeansUnsetParam(maxVXLANVNI, "VNI of the VRF an EVPN zone routes in."),
		"exitnodes":     optString(512, "<node>[,<node>...]", "Nodes that route an EVPN zone's traffic out."),
		"mac":           optString(64, "<mac>", "Anycast MAC address for an EVPN zone's logical router."),
		"advertise-subnets": sdnFlagParam(
			"1 advertises an EVPN zone's subnets into BGP."),
		"disable-arp-nd-suppression": sdnFlagParam(
			"1 turns OFF ARP/ND suppression on an EVPN zone."),
	}
}

// sdnVNetSettings are the four settings a VNet create and update share.
func sdnVNetSettings() apischema.Properties {
	return apischema.Properties{
		"tag":       sdnZeroMeansUnsetParam(maxVXLANVNI, "VLAN tag or VXLAN VNI, depending on the zone's type."),
		"alias":     optString(255, "<string>", "Free-text label for the VNet."),
		"vlanaware": sdnFlagParam("1 makes the VNet carry tagged traffic through to the guest."),
		"isolate":   sdnFlagParam("1 stops guests on this VNet from reaching each other."),
	}
}

// sdnSubnetSettings are the four settings a subnet create and update share.
func sdnSubnetSettings() apischema.Properties {
	return apischema.Properties{
		"gateway":         optString(64, "<ip>", "Gateway address inside the subnet."),
		"snat":            sdnFlagParam("1 source-NATs traffic leaving the subnet."),
		"dhcp-range":      optString(255, "start-address=<ip>,end-address=<ip>", "Address range the built-in DHCP server hands out."),
		"dhcp-dns-server": optString(64, "<ip>", "DNS server the built-in DHCP server advertises."),
	}
}

// sdnControllerSettings are the nine settings a controller create and update
// share.
func sdnControllerSettings() apischema.Properties {
	return apischema.Properties{
		// 4294967295 is the 32-bit AS number ceiling; a negative one used to
		// reach Proxmox verbatim.
		"asn":         sdnZeroMeansUnsetParam(4294967295, "Local BGP autonomous system number."),
		"peers":       optString(512, "<ip>[,<ip>...]", "BGP peer addresses."),
		"nodes":       optString(512, "<node>[,<node>...]", "Nodes the controller runs on."),
		"isis-domain": optString(255, "<string>", "IS-IS routing domain."),
		"isis-ifaces": optString(512, "<iface>[,<iface>...]", "Interfaces IS-IS runs on."),
		"isis-net":    optString(255, "<net>", "IS-IS network entity title."),
		// PVE's own ebgp-multihop is a small TTL; 255 is the IP hop limit.
		"ebgp-multihop": sdnZeroMeansUnsetParam(255, "TTL for multihop eBGP sessions."),
		"loopback":      optString(64, "<iface>", "Loopback interface the controller sources sessions from."),
		"node": {
			Type:     apischema.String,
			Optional: true,
			// Empty is the "not set" spelling the form builder drops, so this
			// cannot borrow the node-name format — every registered format
			// rejects "". emptyOrNodeName is the pattern the VM clone target
			// already uses for exactly this shape.
			Pattern:     emptyOrNodeName,
			MaxLength:   apischema.Ptr(63),
			Typetext:    "<name>",
			Description: "Single node this controller is bound to. Empty or omitted leaves it unset.",
		},
	}
}

// sdnIPAMSettings are the three settings an IPAM create and update share.
//
// token is a CREDENTIAL — a phpIPAM or NetBox API token — and it is worth
// saying so in one place: it is stored on the Proxmox cluster, not by
// Nexara, and the audit rows these routes write record only the plugin's
// name and type. view:audit is granted to every Viewer by default, so a
// secret in an audit payload would be a disclosure rather than a detail.
func sdnIPAMSettings() apischema.Properties {
	return apischema.Properties{
		"url":     optString(512, "<url>", "Base URL of the external IPAM. Proxmox contacts it, not Nexara."),
		"token":   optString(512, "<token>", "API token for the external IPAM. Stored on the Proxmox cluster; never recorded in an audit row."),
		"section": sdnZeroMeansUnsetParam(2147483647, "phpIPAM section id to allocate from."),
	}
}

// sdnDNSSettings are the two settings a DNS create and update share. key is
// a CREDENTIAL, for the reason sdnIPAMSettings gives about token.
func sdnDNSSettings() apischema.Properties {
	return apischema.Properties{
		"url": optString(512, "<url>", "Base URL of the DNS service. Proxmox contacts it, not Nexara."),
		"key": optString(1024, "<key>", "Authentication key for the DNS service. Stored on the Proxmox cluster; never recorded in an audit row."),
	}
}

// registerSDNEndpoints declares the 25 SDN routes served by NetworkHandler.
//
// Every one of them is the uniform shape: resolve the cluster from the path,
// then one static requireClusterPerm. So all 25 declare a plain
// cluster-scoped Check — nothing here is Deferred, Advisory, Public,
// SelfService or global.
//
// The permission split is regular for once: reading is view:network,
// creating and changing are manage:network, and every DELETE is
// delete:network. apply is manage:network, like the node network apply it
// mirrors.
//
// The one thing worth naming is what the path parameters buy. None of the
// SDN client methods runs proxmox.validatePathSegment, and url.PathEscape
// leaves "." and ".." alone. pveproxy takes such a segment literally (see
// proxmox.validatePathSegment), but behind a normalising proxy PUT
// /sdn/zones/.. normalises to PUT /cluster/sdn, which is the APPLY endpoint.
// That endpoint takes only lock-token and release-lock (pve-network
// src/PVE/API2/Network/SDN.pm, reload), so a zone update carrying any other
// field is refused there, and one carrying none applies the pending SDN
// configuration. Same permission, so it is not an escalation, but "update
// this zone" landing on "apply everything" is not a thing to leave
// declarable. pveObjectNameParam's leading-alphanumeric anchor is what closes
// it.
func registerSDNEndpoints(reg *Registry, h *handlers.NetworkHandler) {
	// ── Zones ─────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        sdnScope + "/zones",
		Description: "List the cluster's SDN zones with their type and settings.",
		Group:       "SDN",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListSDNZones,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   sdnScope + "/zones",
		Description: "Create an SDN zone. The change lands in the cluster's PENDING SDN configuration " +
			"and does nothing until apply runs.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnZoneSettings(), apischema.Properties{
			"zone": pveObjectNameParam("Name for the zone. Proxmox caps this at 8 characters for most zone types."),
			"type": sdnTypeParam("<simple|vlan|qinq|vxlan|evpn>", "Zone plugin. Which of the settings below apply depends on it."),
		})),
		Handler: h.CreateSDNZone,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   sdnScope + "/zones/:zone",
		Description: "Change an SDN zone. A setting left out is not sent, so Proxmox keeps its current " +
			"value. The zone's type cannot be changed.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnZoneSettings(), apischema.Properties{
			"zone": pveObjectNameParam("Zone to change."),
			// No `type` here, mirroring proxmox.UpdateSDNZoneParams: the
			// handler bound a struct that has no Type field, so a body
			// carrying one was silently dropped and is now a 400.
		})),
		Handler: h.UpdateSDNZone,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        sdnScope + "/zones/:zone",
		Description: "Delete an SDN zone. Proxmox refuses while a VNet still uses it.",
		Group:       "SDN",
		Permissions: clusterCheck("delete", "network"),
		Parameters:  clusterParams(apischema.Properties{"zone": pveObjectNameParam("Zone to delete.")}),
		Handler:     h.DeleteSDNZone,
	})

	// ── VNets ─────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        sdnScope + "/vnets",
		Description: "List the cluster's SDN VNets with the zone each belongs to.",
		Group:       "SDN",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListSDNVNets,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        sdnScope + "/vnets",
		Description: "Create an SDN VNet inside a zone. Lands in the pending SDN configuration until apply runs.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnVNetSettings(), apischema.Properties{
			"vnet": pveObjectNameParam("Name for the VNet, which becomes the bridge guests attach to."),
			"zone": pveObjectNameParam("Zone the VNet belongs to."),
		})),
		Handler: h.CreateSDNVNet,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   sdnScope + "/vnets/:vnet",
		Description: "Change an SDN VNet, including moving it to another zone. A setting left out is not " +
			"sent, so Proxmox keeps its current value.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnVNetSettings(), apischema.Properties{
			"vnet": pveObjectNameParam("VNet to change."),
			// Optional here and required on create, mirroring
			// proxmox.UpdateSDNVNetParams: sdnVNetUpdateToForm sends zone only
			// when it is non-empty, so an omitted OR empty zone keeps the
			// current one. Hence empty-or-name rather than
			// pveObjectNameParam's bare rule, which refuses "" — the
			// empty-means-unset rule sdnZoneSettings states for this domain.
			"zone": pveObjectNameOrEmptyParam("Move the VNet to this zone. Empty or omitted keeps the current one."),
		})),
		Handler: h.UpdateSDNVNet,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        sdnScope + "/vnets/:vnet",
		Description: "Delete an SDN VNet and the subnets under it.",
		Group:       "SDN",
		Permissions: clusterCheck("delete", "network"),
		Parameters:  clusterParams(apischema.Properties{"vnet": pveObjectNameParam("VNet to delete.")}),
		Handler:     h.DeleteSDNVNet,
	})

	// ── Subnets ───────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        sdnScope + "/vnets/:vnet/subnets",
		Description: "List the subnets configured on one SDN VNet.",
		Group:       "SDN",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(apischema.Properties{"vnet": pveObjectNameParam("VNet to read.")}),
		Handler:     h.ListSDNSubnets,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   sdnScope + "/vnets/:vnet/subnets",
		Description: "Add a subnet to an SDN VNet. Proxmox derives the subnet's id from the CIDR, which " +
			"is why the id in the path of the update and delete routes looks nothing like what is sent here.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnSubnetSettings(), apischema.Properties{
			"vnet": pveObjectNameParam("VNet to add the subnet to."),
			"subnet": {
				Type: apischema.String,
				// The one place in this domain where a real format applies:
				// this value is a CIDR, unlike the :subnet PATH parameter,
				// which is the dashed id Proxmox derives from it.
				Format:      "cidr",
				MaxLength:   apischema.Ptr(64),
				Typetext:    "<ip/prefix>",
				Description: "Network in CIDR form, e.g. 192.0.2.0/24.",
			},
			"type": {
				Type:      apischema.String,
				Optional:  true,
				MaxLength: apischema.Ptr(32),
				// The default the HANDLER used to fill in, stated here
				// instead, so the docs answer what omitting it does. Proxmox
				// accepts only this one value today, which is why it is a
				// default rather than an enum — the enum would be a list of
				// one that dates the moment Proxmox adds a second.
				//
				// A Default applies only to an ABSENT key, and the handler
				// also substituted "subnet" for an EMPTY one; it still does
				// (CreateSDNSubnet), which is why there is no MinLength here.
				Default:     "subnet",
				Typetext:    "<subnet>",
				Description: "Subnet plugin. Empty or omitted means subnet.",
			},
		})),
		Handler: h.CreateSDNSubnet,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   sdnScope + "/vnets/:vnet/subnets/:subnet",
		Description: "Change a subnet's gateway, SNAT or DHCP settings. The network itself cannot be " +
			"changed — delete the subnet and add the new one.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnSubnetSettings(), apischema.Properties{
			"vnet": pveObjectNameParam("VNet the subnet belongs to."),
			// NOT the CIDR the create body takes — see sdnSubnetIDParam.
			"subnet": sdnSubnetIDParam,
			// No `subnet` CIDR and no `type` here, mirroring
			// proxmox.UpdateSDNSubnetParams, which has neither field.
		})),
		Handler: h.UpdateSDNSubnet,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        sdnScope + "/vnets/:vnet/subnets/:subnet",
		Description: "Delete a subnet from an SDN VNet.",
		Group:       "SDN",
		Permissions: clusterCheck("delete", "network"),
		Parameters: clusterParams(apischema.Properties{
			"vnet":   pveObjectNameParam("VNet the subnet belongs to."),
			"subnet": sdnSubnetIDParam,
		}),
		Handler: h.DeleteSDNSubnet,
	})

	// ── Apply ─────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   sdnScope + "/apply",
		Description: "Apply the cluster's pending SDN configuration to every node. Until this runs, a " +
			"zone, VNet or subnet change exists only on paper.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.ApplySDN,
	})

	// ── Controllers ───────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        sdnScope + "/controllers",
		Description: "List the cluster's SDN controllers — the BGP/EVPN and IS-IS speakers zones bind to.",
		Group:       "SDN",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListSDNControllers,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        sdnScope + "/controllers",
		Description: "Create an SDN controller.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnControllerSettings(), apischema.Properties{
			"controller": pveObjectNameParam("Name for the controller."),
			"type":       sdnTypeParam("<bgp|evpn|isis>", "Controller plugin."),
		})),
		Handler: h.CreateSDNController,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        sdnScope + "/controllers/:controller",
		Description: "Change an SDN controller. A setting left out is not sent, so Proxmox keeps its current value.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnControllerSettings(), apischema.Properties{
			"controller": pveObjectNameParam("Controller to change."),
		})),
		Handler: h.UpdateSDNController,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        sdnScope + "/controllers/:controller",
		Description: "Delete an SDN controller. Proxmox refuses while a zone still binds to it.",
		Group:       "SDN",
		Permissions: clusterCheck("delete", "network"),
		Parameters:  clusterParams(apischema.Properties{"controller": pveObjectNameParam("Controller to delete.")}),
		Handler:     h.DeleteSDNController,
	})

	// ── IPAM plugins ──────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        sdnScope + "/ipams",
		Description: "List the cluster's IPAM plugins — what allocates addresses inside an SDN subnet.",
		Group:       "SDN",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListSDNIPAMs,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   sdnScope + "/ipams",
		Description: "Create an IPAM plugin. An external plugin's token is stored on the Proxmox cluster " +
			"and is not recorded in Nexara's audit log.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnIPAMSettings(), apischema.Properties{
			"ipam": pveObjectNameParam("Name for the IPAM plugin."),
			"type": sdnTypeParam("<pve|phpipam|netbox>", "IPAM plugin. pve is Proxmox's built-in allocator and needs no url or token."),
		})),
		Handler: h.CreateSDNIPAM,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        sdnScope + "/ipams/:ipam",
		Description: "Change an IPAM plugin's endpoint or credentials. A setting left out is not sent.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnIPAMSettings(), apischema.Properties{
			"ipam": pveObjectNameParam("IPAM plugin to change."),
		})),
		Handler: h.UpdateSDNIPAM,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        sdnScope + "/ipams/:ipam",
		Description: "Delete an IPAM plugin. Proxmox refuses while a zone still uses it.",
		Group:       "SDN",
		Permissions: clusterCheck("delete", "network"),
		Parameters:  clusterParams(apischema.Properties{"ipam": pveObjectNameParam("IPAM plugin to delete.")}),
		Handler:     h.DeleteSDNIPAM,
	})

	// ── DNS plugins ───────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        sdnScope + "/dns",
		Description: "List the cluster's SDN DNS plugins — what registers records for an SDN subnet.",
		Group:       "SDN",
		Permissions: clusterCheck("view", "network"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListSDNDNS,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   sdnScope + "/dns",
		Description: "Create an SDN DNS plugin. The authentication key is stored on the Proxmox cluster " +
			"and is not recorded in Nexara's audit log.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnDNSSettings(), apischema.Properties{
			"dns":  pveObjectNameParam("Name for the DNS plugin."),
			"type": sdnTypeParam("<powerdns>", "DNS plugin."),
		})),
		Handler: h.CreateSDNDNS,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        sdnScope + "/dns/:dns",
		Description: "Change an SDN DNS plugin's endpoint or key. A setting left out is not sent.",
		Group:       "SDN",
		Permissions: clusterCheck("manage", "network"),
		Parameters: clusterParams(withParams(sdnDNSSettings(), apischema.Properties{
			"dns": pveObjectNameParam("DNS plugin to change."),
		})),
		Handler: h.UpdateSDNDNS,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        sdnScope + "/dns/:dns",
		Description: "Delete an SDN DNS plugin. Proxmox refuses while a zone still uses it.",
		Group:       "SDN",
		Permissions: clusterCheck("delete", "network"),
		Parameters:  clusterParams(apischema.Properties{"dns": pveObjectNameParam("DNS plugin to delete.")}),
		Handler:     h.DeleteSDNDNS,
	})
}
