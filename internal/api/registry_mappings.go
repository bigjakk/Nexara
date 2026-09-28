package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// usbMappingIDParam is a USB mapping id as a PATH parameter, on the routes
// that act on a mapping that already exists — haConfigIDParam's shape, for
// its reasons: the catalogue's pve-configid-existing rule is looser than the
// pve-configid the create body mints by one character, so this route can
// never refuse a name that create made, and it admits no "/", "." or "%", so
// the segment reaches the client's path unescaped and whole. The 128 is the
// create rule's ceiling, which TestGuard_ExistingRuleSitesAcceptWhatTheCreateRuleMints
// holds this MaxLength to. Proxmox itself states no maximum, so a mapping made
// there with a longer id is out of this API's reach; the SPA says so for such
// a mapping rather than offering actions that would 400.
var usbMappingIDParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     apischema.Rule("pve-configid-existing"),
	MaxLength:   apischema.Ptr(128),
	Typetext:    "<mapping id>",
	Description: "The mapping's id, as a VM's usbN names it in mapping=<mapping_id>.",
}

// Proxmox cluster resource mappings, for passing host devices through.
//
// They exist because nothing else works for a token. qemu-server's
// check_usb_perm (src/PVE/API2/Qemu.pm) lets only the literal user root@pam
// set a usbN whose host= names a real device — by vendor/device id or by port
// — and dies "only root can set 'usbN' config for real devices" for everyone
// else, on the new value and on the one it replaces or deletes. An API token
// is user@realm!tokenname, never root@pam, not even one root owns, and Nexara
// connects with nothing but tokens. A usbN of mapping=<id> is checked against
// Mapping.Use on /mapping/usb/<id> instead, so the Add USB Device dialog lists
// these mappings and creates one when the operator picks a host device.
//
// The cluster's Resource Mappings tab manages them afterwards: the cluster-wide
// listing, update, delete and usage routes below.
//
// They are declared from registerVMEndpoints, which VMHandler also serves, so
// they count toward vmRouteCount.
//
// usageLimiter caps the usage route per user (usbMappingUsageLimiter in
// middleware.go): each call reads every VM's configuration.
func registerResourceMappingEndpoints(reg *Registry, h *handlers.VMHandler, usageLimiter fiber.Handler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/nodes/:node_name/usb-mappings",
		Description: "List the cluster's USB resource mappings, each checked against this node: a mapping's " +
			"errors say why it would not work here — a warning when it has no entry for the node, an error " +
			"when its entry names hardware the node does not have. Proxmox lists only the mappings the " +
			"cluster's token holds a Mapping privilege on.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.ListNodeUSBMappings,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/usb-mappings",
		// The manage:cluster gate is LOOSER than Proxmox's own role split on
		// purpose, and the Description says so for whoever reads the docs.
		// Proxmox keeps Mapping.Modify out of PVEAdmin ("only Administrator,
		// root@pam and PVEMappingAdmin should be able to use that for now",
		// create_roles in pve-access-control src/PVE/AccessControl.pm), while
		// Nexara's built-in Operator holds manage:cluster. The operator chose
		// this on 2026-09-27: Operator already manages nodes and storage and can
		// attach any existing mapping through manage:vm, so a new permission
		// would add friction without narrowing much.
		Description: "Create a USB resource mapping with one node entry, so a VM can pass that node's device " +
			"through as mapping=<mapping_id>. Without a path the mapping follows the device to any port; with " +
			"one it is tied to the device on that port, and Proxmox refuses to start the VM if a device with " +
			"another id is there. Proxmox also refuses to start a VM whose mapped device is missing. Needs the " +
			"token to hold Mapping.Modify, which Proxmox's Administrator role has and PVEAdmin does not; " +
			"Nexara gates it on manage:cluster.",
		Group:       "Clusters",
		Permissions: clusterCheck("manage", "cluster"),
		Parameters: clusterParams(apischema.Properties{
			// Not "id": the permission middleware resolves the cluster from a
			// parameter of that name, so the registry refuses it as a body key.
			"mapping_id": {
				Type:        apischema.String,
				Format:      "pve-configid",
				Typetext:    "<mapping id>",
				Description: "Name for the new mapping, which a VM's usbN names as mapping=<mapping_id>: 2-128 characters, starting with a letter.",
			},
			"node": requiredNode("Node the entry is for. Proxmox uses one entry per node when it starts a VM."),
			// Both patterns are $map_fmt in pve-guest-common
			// src/PVE/Mapping/USB.pm, transcribed; proxmox.CreateUSBMapping
			// applies the same two, because the values are joined into one
			// property string there and the client is the choke point.
			"device_id": {
				Type:        apischema.String,
				Pattern:     `^[0-9A-Fa-f]{4}:[0-9A-Fa-f]{4}$`,
				Typetext:    "<vendor:product>",
				Description: "The device's USB id as vendor:product, four hex digits each, as GET …/hardware/usb reports it.",
			},
			"path": {
				Type:     apischema.String,
				Optional: true,
				Pattern:  `^\d+-\d+(\.\d+)*$`,
				// Nexara's bound, not Proxmox's; proxmox.usbMappingPathMax
				// says why no real port comes near it.
				MaxLength: apischema.Ptr(64),
				Typetext:  "<bus>-<port>[.<port>…]",
				Description: "USB port that ties the mapping to the device on it, as <busnum>-<usbpath> from " +
					"GET …/hardware/usb, e.g. 1-2 or 1-2.3; at most 64 characters.",
			},
			"description": optString(4096, "<string>",
				"Description Proxmox shows for the mapping. A single line: Proxmox refuses a line break."),
		}),
		Handler: h.CreateUSBMapping,
	})

	// ── The cluster's Resource Mappings tab ──────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/usb-mappings",
		Description: "List the cluster's USB resource mappings, each checked on every node one of its entries " +
			"names: node_checks holds what Proxmox reported running the check on that node — an empty list " +
			"is a clean check, a warning or an error says why the mapping would not work there — and " +
			"unchecked says why a node was not checked (offline, not answering, not a node of this cluster, " +
			"the mappings changed during the check, the checks' 20 seconds ran out, or the cluster's node list " +
			"could not be read — then no node is checked). Every node an entry names is in exactly one of the " +
			"two. digest is the whole usb.cfg's, from the same read as the entries: an update sends it back, " +
			"so it conflicts when any USB mapping changed since. Proxmox lists only the mappings the " +
			"cluster's token holds a Mapping privilege on.",
		Group:       "Clusters",
		Permissions: clusterCheck("view", "cluster"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListClusterUSBMappings,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   clusterScope + "/usb-mappings/:mapping_id",
		// Gated like the create, on manage:cluster, for the reason given
		// there.
		Description: "Replace a USB mapping's node entries, and optionally its description. map is the WHOLE " +
			"list — Proxmox replaces the mapping's entries with it — with at most one entry per node: " +
			"Proxmox's API would store two, but qemu-server refuses to start a VM using the mapping on that " +
			"node. Each entry is node=<node>,id=<vendor:product>[,path=<bus-port>][,description=<text>] and " +
			"is stored with its id in lowercase, which is what Proxmox compares against the device when a VM " +
			"starts. To remove the last entry, delete the mapping. digest is the listing's; the update is " +
			"refused with 409 when any USB mapping changed since — this one being deleted included — and " +
			"with 404 only when the digest is current and no mapping has that id. Needs the token to hold " +
			"Mapping.Modify; Nexara gates it on manage:cluster.",
		Group:       "Clusters",
		Permissions: clusterCheck("manage", "cluster"),
		Parameters: clusterParams(apischema.Properties{
			"mapping_id": usbMappingIDParam,
			// Not refused as empty by Proxmox, but a mapping with no entries
			// corrupts usb.cfg for every later write; proxmox.CanonicalUSBMap
			// refuses it too, and validates every entry, since the list is
			// joined into property strings there and the client is the
			// choke point.
			"map": {
				Type:      apischema.Array,
				MinLength: apischema.Ptr(1),
				Typetext:  "<entry>[]",
				Items: &apischema.Property{
					Type:        apischema.String,
					Typetext:    "node=<node>,id=<vendor:product>[,path=<bus-port>][,description=<text>]",
					Description: "One node's entry, as the listing returns it; its keys may come in any order.",
				},
				Description: "Every node entry the mapping is to have, at least one and at most one per node.",
			},
			"description": optString(4096, "<string>",
				"The mapping's new description. An empty string removes it; omitted, it is left as it is. A "+
					"single line: Proxmox refuses a line break."),
			// pve-config-digest in Proxmox: optional there, with maxLength 64
			// and no pattern. Required here, and not empty, because this
			// route exists for the SPA's compare-and-swap: an update without
			// one would overwrite a change nobody was shown.
			"digest": {
				Type:        apischema.String,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(64),
				Typetext:    "<digest>",
				Description: "digest of the listing this update is based on, exactly as the listing returns it.",
			},
		}),
		Handler: h.UpdateUSBMapping,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   clusterScope + "/usb-mappings/:mapping_id",
		Description: "Delete a USB mapping. Like Proxmox's own delete it succeeds whether or not the mapping " +
			"exists, and it does not check the VMs that use it: such a VM will not start until a mapping of " +
			"that name exists again, so read …/usage first. With digest — the listing's — it is refused with " +
			"409 when any USB mapping changed since; Proxmox's delete takes no digest, so Nexara compares it " +
			"against its own read just before the delete. The audit entry keeps the mapping's entries. Needs " +
			"the token to hold Mapping.Modify; Nexara gates it on manage:cluster.",
		Group:       "Clusters",
		Permissions: clusterCheck("manage", "cluster"),
		Parameters: clusterParams(apischema.Properties{
			"mapping_id": usbMappingIDParam,
			// Optional, unlike the update's: Proxmox's delete takes none, so
			// a delete without one is the delete Proxmox itself makes. The
			// SPA always sends it. The 64 is pve-config-digest's, as on the
			// update; the comparison is Nexara's own string compare, so "0"
			// has no special meaning here.
			// MinLength too: sent, it is compared — an empty one would read as
			// "not sent" and let a caller who asked for the guard go blind.
			"digest": {
				Type:      apischema.String,
				Optional:  true,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(64),
				Typetext:  "<digest>",
				Description: "digest of the listing the delete is based on. Sent, the delete is refused with 409 when " +
					"any USB mapping changed since, instead of deleting a mapping that changed meanwhile.",
			},
		}),
		Handler: h.DeleteUSBMapping,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/usb-mappings/:mapping_id/usage",
		// view:vm, not view:cluster like the listing: what this answers
		// with is guests — their ids, names and nodes — which every other
		// route serves under view:vm, and a role that may see the cluster's
		// configuration but not its guests must not learn them here.
		Description: "Which VMs pass this USB mapping through — a usbN of mapping=<mapping_id> — read from " +
			"each VM's current configuration (pending changes included; snapshots are not read). A VM whose " +
			"configuration could not be read — its node is not online, the read failed, or the scan ran out " +
			"of its 30 seconds — is listed under unchecked with the reason, and may use the mapping too. " +
			"Only the VMs the cluster's API token can see are read. Containers are not read: they cannot " +
			"use a USB mapping. Each call reads every VM, so it is limited to 10 a minute per user and two " +
			"running at once per cluster — another gets 429 — and one running at once per user: a user's " +
			"newer check replaces the older one, which answers 409.",
		Group:       "Clusters",
		Permissions: clusterCheck("view", "vm"),
		Parameters:  clusterParams(apischema.Properties{"mapping_id": usbMappingIDParam}),
		RateLimiter: usageLimiter,
		Handler:     h.GetUSBMappingUsage,
	})

	// ── PCI mappings, for the Add PCI Device dialog ──────────────────────
	//
	// The same reason as USB's: qemu-server's check_hostpci_perm lets only
	// root@pam set a hostpciN that names a host device ("only root can set
	// 'hostpciN' config for non-mapped devices"), and checks Mapping.Use on
	// /mapping/pci/<id> for a hostpciN of mapping=<id>.
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/nodes/:node_name/pci-mappings",
		Description: "List the cluster's PCI resource mappings, each checked against this node: a mapping's " +
			"checks say why it would not work here — a warning when it has no entry for the node, an error " +
			"when an entry names a device the node does not have or does not match exactly. A node may have " +
			"several entries; a VM starting there takes the first device not already in use. Proxmox lists " +
			"only the mappings the cluster's token holds a Mapping privilege on.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.ListNodePCIMappings,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/pci-mappings",
		// Gated like the USB create, on manage:cluster, for the reason given
		// there.
		Description: "Create a PCI resource mapping with one node entry, so a VM can pass that node's device " +
			"through as mapping=<mapping_id>. Only the node and the device's address are taken from the " +
			"request: the entry's device and subsystem ids, its IOMMU group and the mapping's mediated-device " +
			"flag are copied from the node's own report of the device, because Proxmox refuses to start a VM " +
			"whose device does not match its mapping exactly. An address without the function maps the whole " +
			"device, every function passed through as one. The mapping may name any device the node " +
			"reports, its own disk and network controllers included: a VM starting with it takes the " +
			"device, and every other device in its IOMMU group but PCI-to-PCI bridges, away from the node, and anyone " +
			"with manage:vm can attach the mapping to a VM. Needs the token to hold Mapping.Modify, which " +
			"Proxmox's Administrator role has and PVEAdmin does not; Nexara gates it on manage:cluster.",
		Group:       "Clusters",
		Permissions: clusterCheck("manage", "cluster"),
		Parameters: clusterParams(apischema.Properties{
			// "mapping_id", not "id", for the reason on the USB create.
			"mapping_id": {
				Type:        apischema.String,
				Format:      "pve-configid",
				Typetext:    "<mapping id>",
				Description: "Name for the new mapping, which a VM's hostpciN names as mapping=<mapping_id>: 2-128 characters, starting with a letter.",
			},
			"node": requiredNode("Node the device is on."),
			// One $PCI_RE of pve-guest-common src/PVE/Mapping/PCI.pm, not the
			// ";"-joined list its schema admits: proxmox.PCIMapEntryForDevice
			// says why, and applies the same pattern, being the choke point.
			// The 64 is Nexara's, as on the USB port (proxmox.pciMappingPathMax).
			"path": {
				Type:      apischema.String,
				Pattern:   `^[a-f0-9]{4,}:[a-f0-9]{2}:[a-f0-9]{2}(\.[a-f0-9])?$`,
				MaxLength: apischema.Ptr(64),
				Typetext:  "<domain>:<bus>:<slot>[.<function>]",
				Description: "The device's address as GET …/hardware/pci reports it, e.g. 0000:01:00.0, or without the " +
					"function, e.g. 0000:01:00, to map the whole device; lowercase hex, at most 64 characters.",
			},
			"description": optString(4096, "<string>",
				"Description Proxmox shows for the mapping. A single line: Proxmox refuses a line break."),
		}),
		Handler: h.CreatePCIMapping,
	})
}
