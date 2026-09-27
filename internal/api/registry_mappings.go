package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

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
// They are declared from registerVMEndpoints, which VMHandler also serves, so
// they count toward vmRouteCount.
func registerResourceMappingEndpoints(reg *Registry, h *handlers.VMHandler) {
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
}
