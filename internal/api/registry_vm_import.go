package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, emptyOrNodeName, imageFormatParam, optFlag,
// optString, optCount, optTristateBool, requiredStorage and requiredNode —
// lives in registry_vms.go, where the first migrated domain defined it.

// importScope and importSourceScope are the two collections this domain
// hangs off. :cluster_id is the FIRST path parameter of both, which
// namesACluster (permissions.go) requires of a cluster-scoped Check.
const (
	importScope       = clusterScope + "/vm-imports"
	importSourceScope = clusterScope + "/vm-import-sources"
)

// importSourceStorageParam is the Proxmox storage id an import source is
// registered under, as a PATH parameter on the delete route.
//
// It takes the storage-id FORMAT rather than a bespoke pattern, and that is
// the anchor as well as the shape: proxmox.DeleteStorage and
// GetStorageConfig build "/storage/" + url.PathEscape(name), and PathEscape
// leaves "." and ".." alone — so an un-anchored name resolves onto the
// storage COLLECTION. The handler only ever checked the segment was
// non-empty, which ".." satisfies. formatStorageID requires a leading
// letter, which is also PVE's own rule for a storage id, so nothing that
// could have been created is refused.
var importSourceStorageParam = func() apischema.Property {
	p := apischema.StdOption("storage-id")
	p.Description = "Proxmox storage id of the import source to remove, as " +
		"GET /clusters/{cluster_id}/vm-import-sources reports it."
	return p
}()

// importJobIDParam is a Nexara vm_import_jobs row id.
//
// It is spelled :id because that is what the two routes already register.
// The name is one of the two clusterIDFromParam reads (see gateParamNames in
// registry.go), which is safe here because it resolves to the PATH and
// :cluster_id is the FIRST placeholder — so the gate authorizes the cluster
// and never falls back to this.
var importJobIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Typetext:    "<uuid>",
	Description: "Nexara import job identifier, as GET /clusters/{cluster_id}/vm-imports reports it.",
}

// importVolumeParam is the source volume id (a Proxmox volid such as
// "store01:import/appliance.ova").
//
// No format and no anchor: it reaches Proxmox as a QUERY parameter, never as
// a path segment (GetImportMetadata url-encodes it into ?volume=), and its
// colon-and-slash shape is exactly what every registered format refuses.
// What the schema owes it is a length bound and membership in a closed
// parameter set.
func importVolumeParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		MinLength:   apischema.Ptr(1),
		MaxLength:   apischema.Ptr(1024),
		Typetext:    "<storage>:<path>",
		Description: description,
	}
}

// registerVMImportEndpoints declares the 11 routes served by
// VMImportHandler.
//
// Every one of them is the uniform shape the legacy handlers had — resolve
// the cluster from the path, then one static requireClusterPerm — so all 11
// declare a plain Check and every hand-placed call is gone from the handler
// bodies. Nothing here is Deferred and nothing is Advisory: no listing in
// this domain filters through accessibleClusters, and no permission depends
// on a body value or a DB lookup.
//
// TWO of the 11 are gated on a STORAGE grant rather than on vm_import, and
// both are deliberate rather than an oversight — see their Descriptions.
func registerVMImportEndpoints(reg *Registry, h *handlers.VMImportHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/import-metadata",
		Description: "Parse an importable OVA, OVF or ESXi guest and return the guest definition the " +
			"wizard pre-fills from it. POST rather than GET because the source volume id carries slashes " +
			"and colons.",
		Group:       "VM Import",
		Permissions: clusterCheck("view", "vm_import"),
		Parameters: clusterParams(apischema.Properties{
			// Required by resolveNode, which this route reaches with the body's
			// own node and which answers "node is required" for an empty one.
			// The handler itself checked only storage and volume, so the
			// rejection for an absent node used to arrive from one layer down;
			// it now names the field.
			"node":    requiredNode("Node that can see the source volume, and that will do the parsing."),
			"storage": requiredStorage("Storage the source volume lives on."),
			"volume":  importVolumeParam("Source volume id, e.g. store01:import/appliance.ova."),
		}),
		Handler: h.GetImportMetadata,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clusterScope + "/query-url-metadata",
		// The permission is NOT manage:vm_import, and that is a decision
		// rather than an inconsistency. This makes a NODE issue an outbound
		// request to a caller-supplied URL — an SSRF-shaped primitive — so it
		// is held to the same bar as the download it precedes
		// (POST .../storage/{id}/download-url), and manage:vm_import must not
		// gain a node-side URL-fetch capability it otherwise lacks. What stays
		// available to a manage:vm_import holder is the browser-upload leg,
		// which makes no node-side fetch at all.
		Description: "Ask a node what a remote download's filename and size are, so the import wizard can " +
			"pre-fill them before staging an OVA. Requires manage:storage rather than manage:vm_import: " +
			"the node makes the outbound request, which is the same capability the download endpoint " +
			"needs and one manage:vm_import deliberately does not carry.",
		Group:       "VM Import",
		Permissions: clusterCheck("manage", "storage"),
		Parameters: clusterParams(apischema.Properties{
			"url": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(2048),
				Typetext:  "<url>",
				// No format: the http/https rule stays in isHTTPURL, which owns
				// it and answers with a message of its own, and no parameter
				// schema can make the address decision this endpoint's whole
				// permission choice is about.
				Description: "URL to probe. Must be http or https.",
			},
			"node": {
				Type:     apischema.String,
				Optional: true,
				// The EMPTY string has always meant "any online node" —
				// pickImportNode branches on `nodeName != ""` — and every
				// registered format rejects it, so the rule is a pattern.
				Pattern:     emptyOrNodeName,
				MaxLength:   apischema.Ptr(63),
				Typetext:    "<name>",
				Description: "Node to probe from. Empty or omitted picks the first online node in the cluster.",
			},
		}),
		Handler: h.QueryURLMetadata,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   importSourceScope,
		Description: "List the cluster's import-capable storages and ESXi sources, each with an online " +
			"node it can be browsed from. Read from Proxmox's live storage config rather than Nexara's " +
			"inventory, so a just-registered source appears immediately.",
		Group:       "VM Import",
		Permissions: clusterCheck("view", "vm_import"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListImportSources,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   importSourceScope + "/content",
		Description: "List the importable volumes and guests on one import source, as seen from one node. " +
			"The node is one the source listing already resolved to be online, which is what lets a " +
			"shared source be browsed while its inventory-owning node is down.",
		Group:       "VM Import",
		Permissions: clusterCheck("view", "vm_import"),
		Parameters: clusterParams(apischema.Properties{
			"storage": requiredStorage("Import source to list."),
			"node":    requiredNode("Node to list it from. Must belong to this cluster."),
		}),
		Handler: h.ListImportContent,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   importSourceScope + "/esxi",
		Description: "Register an ESXi or vCenter host as an import source, so its guests become " +
			"importable. Proxmox stores the credentials under /etc/pve/priv; Nexara never logs or " +
			"returns them.",
		Group:       "VM Import",
		Permissions: clusterCheck("manage", "vm_import"),
		Parameters:  clusterParams(esxiSourceParams()),
		Handler:     h.RegisterEsxiSource,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   importSourceScope + "/enable-content",
		// manage:storage, like query-url-metadata above and for a related
		// reason: changing what a storage may hold is a storage-management
		// act, not something manage:vm_import alone permits.
		Description: "Add the \"import\" content type to an existing storage, merging it into the " +
			"storage's current content list rather than replacing it. Requires manage:storage rather " +
			"than manage:vm_import: changing what a storage is used for is a storage-management action.",
		Group:       "VM Import",
		Permissions: clusterCheck("manage", "storage"),
		Parameters: clusterParams(apischema.Properties{
			"storage": requiredStorage("Storage to make import-capable."),
		}),
		Handler: h.EnableImportContent,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   importSourceScope + "/:storage",
		Description: "Remove an import source. Refused for a storage that is not one — the check runs " +
			"against Proxmox's live config, so a role holding only manage:vm_import cannot delete " +
			"production storage through this route.",
		Group:       "VM Import",
		Permissions: clusterCheck("manage", "vm_import"),
		Parameters:  clusterParams(apischema.Properties{"storage": importSourceStorageParam}),
		Handler:     h.DeleteImportSource,
	})

	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        importScope,
		Description: "List the cluster's import jobs, newest first.",
		Group:       "VM Import",
		Permissions: clusterCheck("view", "vm_import"),
		Parameters: clusterParams(apischema.Properties{
			"limit": {
				Type:     apischema.Integer,
				Optional: true,
				// The value the handler substituted for a missing, EMPTY,
				// unparseable or out-of-range ?limit=. The bounds REFUSE the
				// last three now rather than silently substituting this — the
				// trade the migration and CVE listings already made. (Empty is
				// in that list because apischema counts "" as a value the caller
				// supplied, while strconv.Atoi("") simply failed and fell to the
				// default.)
				Default:     100,
				Minimum:     apischema.Ptr(1.0),
				Maximum:     apischema.Ptr(500.0),
				Typetext:    "<integer>",
				Description: "Maximum jobs to return.",
			},
			"offset": {
				Type:     apischema.Integer,
				Optional: true,
				Default:  0,
				Minimum:  apischema.Ptr(0.0),
				// A NEW ceiling — the handler only asked for `v > 0` — matching
				// the one the migration and CVE listings already carry, so the
				// three pagers answer the same way.
				Maximum:     apischema.Ptr(1000000.0),
				Typetext:    "<integer>",
				Description: "Jobs to skip before the first one returned.",
			},
		}),
		Handler: h.ListVMImports,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   importScope,
		Description: "Start an import. Records the job, dispatches Proxmox's create-with-import-from " +
			"call against the target node and returns the job; the disk conversion runs as a background " +
			"task the scheduler reconciles.",
		Group:       "VM Import",
		Permissions: clusterCheck("manage", "vm_import"),
		Parameters:  clusterParams(startImportParams()),
		Handler:     h.StartVMImport,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        importScope + "/:id",
		Description: "Get one import job. A job belonging to another cluster answers 404.",
		Group:       "VM Import",
		Permissions: clusterCheck("view", "vm_import"),
		Parameters:  clusterParams(apischema.Properties{"id": importJobIDParam}),
		Handler:     h.GetVMImport,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   importScope + "/:id/cancel",
		Description: "Cancel a pending or running import. Best-effort: the running Proxmox task is asked " +
			"to stop, and delete_vm additionally destroys the partially-created guest.",
		Group:       "VM Import",
		Permissions: clusterCheck("manage", "vm_import"),
		Parameters: clusterParams(apischema.Properties{
			"id": importJobIDParam,
			"delete_vm": optFlag(
				"Destroy the partially-created guest as well. Omitted leaves it in place."),
		}),
		Handler: h.CancelVMImport,
	})
}

// esxiSourceParams is the body of POST .../vm-import-sources/esxi.
//
// Four parameters are required, matching exactly what the handler refused an
// empty value for: storage, server, username and password. `storage` takes
// the storage-id format because it is the id Proxmox will create the source
// under and PVE enforces the same shape.
func esxiSourceParams() apischema.Properties {
	return apischema.Properties{
		"storage": requiredStorage("Storage id to create the ESXi source under, e.g. esxi01."),
		"server": {
			Type:      apischema.String,
			MinLength: apischema.Ptr(1),
			MaxLength: apischema.Ptr(255),
			Typetext:  "<host>",
			// No format: Proxmox accepts a hostname or an address here, and it
			// is relayed as a form value rather than a path segment.
			Description: "ESXi or vCenter host Proxmox will connect to.",
		},
		"username": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(255),
			Typetext:    "<user>",
			Description: "Account Proxmox authenticates to the host with.",
		},
		// Declaring the password at all is what makes checkMisplaced refuse it
		// as a QUERY parameter, so a credential cannot be moved into a URL
		// that proxies and access logs record. It is never read back and never
		// reaches an audit row, which this project makes readable by every
		// Viewer.
		"password": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(1024),
			Typetext:    "<secret>",
			Description: "Password for that account. Stored by Proxmox under /etc/pve/priv and never returned.",
		},
		"skip_cert_verification": optFlag(
			"Accept the host's TLS certificate without verifying it, which a self-signed ESXi needs."),
		"nodes": optString(512, "<name>[,<name>...]",
			"Restrict the source to these nodes. Omitted or empty makes it visible from every node."),
	}
}

// startImportParams is the body of POST /clusters/:cluster_id/vm-imports.
//
// FOUR parameters are required, matching exactly the two combined checks the
// handler made: storage and volume, then target_node and target_storage.
// `node` is required as well although the handler never checked it —
// proxmox.GetImportMetadata does, through validateNodeName, which answers
// "node name cannot be empty", so every request without it has always
// failed; the rejection now names the field instead of arriving as an
// upstream sentence. That is the same call the download-url body's `url`
// makes.
//
// vmid is the one rule the schema cannot state in full. 0 means
// "auto-allocate from /cluster/nextid" and any explicit value must be
// 100–999999999, which is two disjoint ranges; a single Minimum/Maximum pair
// cannot express the 1–99 gap, so the bounds here cover 0 and the ceiling
// and the gap check stays in the handler with its original message.
func startImportParams() apischema.Properties {
	return apischema.Properties{
		// ── Source ────────────────────────────────────────────────────
		"node":    requiredNode("Node that can see the source volume."),
		"storage": requiredStorage("Storage the source volume lives on."),
		"volume":  importVolumeParam("Source volume id, e.g. store01:import/appliance.ova."),
		"source_format": optString(32, "<ova|ovf|vmdk|raw|esxi>",
			"What the source is, recorded on the job for the activity feed. Proxmox derives the real "+
				"format from the volume itself."),
		"source_acquisition": {
			Type:     apischema.String,
			Optional: true,
			// The empty string IS a member, because the handler's switch reads
			// "" and "staged" as the same thing and the wizard sends "" for a
			// file already on the storage. Declaring the default as "staged"
			// instead would reject nothing but would also stop the empty
			// spelling working, which is a request that has always succeeded.
			Enum:     []string{"", "staged", "url", "esxi", "upload"},
			Typetext: "<staged|url|esxi|upload>",
			Description: "How the source got onto the storage, recorded on the job. Empty or omitted " +
				"means staged.",
		},

		// ── Target ────────────────────────────────────────────────────
		"target_node":    requiredNode("Node to create the guest on. Must be the source node when the source storage is not shared."),
		"target_storage": requiredStorage("Storage to place the imported disks on."),
		"working_storage": optString(100, "<storage>",
			"Storage Proxmox stages the conversion on. Omitted lets it use the target."),
		"bridge": optString(64, "<bridge>",
			"Bridge to attach a synthesised net0 to. Omitted or empty adds no network device, and every "+
				"net_* option below is then ignored."),
		"vmid": {
			Type:     apischema.Integer,
			Optional: true,
			// NO Default: 0 already means auto-allocate, and a Default of 0
			// would read as if the endpoint had chosen it.
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(999999999.0),
			Typetext:    "<integer>",
			Description: "VMID for the new guest. 0 or omitted allocates the next free one; any other value must be at least 100.",
		},
		"name":        optString(255, "<string>", "Name for the new guest. Omitted keeps the one the source metadata carries."),
		"disk_format": imageFormatParam("Format to write the imported disks in. Omitted lets the target storage decide."),
		"start_after": optFlag("Start the guest once the import finishes."),
		"live_import": optFlag("Start the guest while its disks are still converting, which Proxmox supports for some sources."),

		// ── Guest-config overrides ────────────────────────────────────
		"cores":       optCount(1024, "Cores per socket. 0 or omitted keeps the source-derived value."),
		"sockets":     optCount(16, "CPU sockets. 0 or omitted keeps the source-derived value."),
		"memory":      optCount(4194304, "RAM in MiB. 0 or omitted keeps the source-derived value."),
		"cpu_type":    optString(256, "<cputype>", "CPU model, e.g. x86-64-v2-AES or host."),
		"os_type":     optString(64, "<ostype>", "Guest OS type, which sets Proxmox's device defaults."),
		"bios":        optString(64, "<seabios|ovmf>", "Firmware."),
		"machine":     optString(128, "<type>", "QEMU machine type."),
		"scsihw":      optString(128, "<model>", "SCSI controller model."),
		"pool":        optString(64, "<pool>", "Resource pool to place the guest in."),
		"tags":        optString(1024, "<tags>", "Semicolon-separated Proxmox tags."),
		"description": optString(8192, "<string>", "Free-text note stored on the guest."),
		// Tristates rather than flags: ImportCreateOptions carries *bool and
		// sends the key only when one is non-nil, so omitting one means "keep
		// what the source metadata derived" rather than "off". A Default would
		// collapse the two and start writing onboot=0 onto every import whose
		// requester never mentioned it.
		"onboot": optTristateBool("Start the guest when its node boots. Omitted keeps the source-derived value."),
		"agent":  optTristateBool("Enable the QEMU guest agent. Omitted keeps the source-derived value."),
		"numa":   optTristateBool("Expose a NUMA topology to the guest. Omitted keeps the source-derived value."),

		// ── Network options for the synthesised net0 ──────────────────
		"net_model": optString(64, "<virtio|e1000|rtl8139|vmxnet3>", "NIC model for net0. Applied only when bridge is set."),
		"vlan_tag": {
			Type:     apischema.Integer,
			Optional: true,
			// 0 is the sentinel the option builder reads as "do not send this",
			// exactly as interfaceTagParam documents for the network domain, so
			// the Minimum is 0 rather than 1.
			Default:     0,
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(float64(proxmox.MaxVLANTag)),
			Typetext:    "<integer>",
			Description: "802.1Q VLAN tag for net0. 0 or omitted adds none.",
		},
		"firewall":    optTristateBool("Enable Proxmox's firewall on net0. Omitted leaves Proxmox's default."),
		"mac_address": optString(64, "<MAC>", "MAC address for net0. Omitted lets Proxmox generate one."),
		"rate_limit":  optString(32, "<MB/s>", "Rate limit for net0 in MB/s, as Proxmox spells it. Empty or omitted is unlimited."),
		"mtu":         optCount(65520, "MTU for net0. 0 or omitted leaves Proxmox's default."),
		"multiqueue":  optCount(64, "Multiqueue depth for net0. 0 or omitted leaves it off."),
	}
}
