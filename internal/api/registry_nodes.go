package api

import (
	"slices"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, nodeParams, withParams, optFlag, optString and the
// emptyOrNodeName sentinel — lives in registry_vms.go, where the first
// migrated domain defined it.

// nodeScope is the collection every node route hangs off. :cluster_id is
// the FIRST path parameter on every one of them, which namesACluster
// (permissions.go) requires of a cluster-scoped Check.
const nodeScope = clusterScope + "/nodes"

// nodeRowParams is the pair the three routes carry that take Nexara's own
// node ROW id rather than the Proxmox node name.
//
// The two identifiers are genuinely different and the paths spell them
// differently on purpose: :node_id is a uuid this collector assigned,
// :node_name is what Proxmox calls the host. Confusing them has bitten
// this codebase before (see the vm-id standard option), so the docs say
// which one a route takes rather than leaving a caller to try both.
func nodeRowParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"node_id": {
			Type:        apischema.String,
			Format:      "uuid",
			Typetext:    "<uuid>",
			Description: "Nexara node identifier, as GET /clusters/{cluster_id}/nodes returns it — not the Proxmox node name.",
		},
	}, extra)
}

// devicePathBody is the /dev/… body both the single-device pattern and the
// comma-separated list pattern are built from, so the two cannot drift.
//
// Anchored at /dev/ because that is the only shape PVE accepts: its own
// `disk` parameter carries the pattern ^/dev/[a-zA-Z0-9/]+$, so a bare
// "sda" has never been a working request. The character class is slightly
// wider than PVE's — dots, dashes, underscores and pluses, for the
// /dev/disk/by-id/… spellings a future PVE might take — and PVE still
// applies its own.
//
// Every SEGMENT must begin with an alphanumeric, not just the first one.
// That is what keeps a "." or ".." segment out of the middle of the path
// as well as off the front: a first-character-only anchor refuses
// /dev/../etc/passwd and accepts /dev/sda/../../etc/passwd, which is the
// shape the anchor is here to exclude. RE2 cannot express it as a negative
// lookahead, so it is expressed as a repeated segment instead.
const devicePathBody = `/dev/[A-Za-z0-9][A-Za-z0-9._+-]*(?:/[A-Za-z0-9][A-Za-z0-9._+-]*)*`

// devicePathParam is a block device as Proxmox names it.
//
// These values go out as FORM or QUERY parameters, never as a path
// segment, so nothing downstream resolves the ".." that devicePathBody
// refuses — the anchor is here so that a value the UI never sends cannot
// reach a node as a device name at all.
func devicePathParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Pattern:     `^` + devicePathBody + `$`,
		MaxLength:   apischema.Ptr(256),
		Typetext:    "</dev/...>",
		Description: description,
	}
}

// storageObjectNameParam is a ZFS pool, LVM volume group or LVM-thin pool
// name, in a PATH segment or in the body that creates one.
//
// The same Property serves both ends deliberately: proxmox.DeleteNodeZFSPool
// and its two siblings run validatePathSegment on the value, and a create
// rule looser than the delete rule would produce an object this API could
// not remove. Requiring a leading alphanumeric is what keeps "." and ".."
// out — the traversal segments validatePathSegment exists for — which RE2
// cannot express as a negative lookahead.
//
// The character class is the union of what ZFS and LVM each allow: ZFS
// wants a leading letter and accepts [A-Za-z0-9_.:-], LVM accepts
// [A-Za-z0-9+_.-] and refuses a leading hyphen. Anything narrower would
// make an existing object created outside Nexara undeletable through it.
func storageObjectNameParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Pattern:     `^[A-Za-z0-9][A-Za-z0-9._:+-]*$`,
		MaxLength:   apischema.Ptr(128),
		Typetext:    "<name>",
		Description: description,
	}
}

// cleanupParams are the two flags the three disk-destroying DELETEs share.
//
// Both were read with fiber.Query[bool], which answers false for anything
// it cannot parse — so ?cleanup-disks=yes silently left the disks alone.
// Declared as booleans they are coerced (true/1/yes/on all work) and a
// value that means neither is a 400 that names the field.
func cleanupParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"cleanup-disks": optFlag(
			"Also wipe the partition table of every disk the object occupied."),
		"cleanup-config": optFlag(
			"Also remove the Proxmox storage entry that was created alongside it."),
	}, extra)
}

// syslogTimeParam is one end of the syslog or journal window.
//
// It carries NO format and no pattern, and that is the whole point: three
// spellings are accepted — a Proxmox wall clock, a relative offset
// ("-1h", "30m ago") and a unix timestamp — and which one a value is
// decides how it is rendered against the node's own clock. That resolution
// lives in normalizeSyslogTime / parseJournalTime, which own the rule and
// answer with messages of their own; a per-parameter pattern here could
// only re-state one of the three.
//
// The EMPTY STRING also has to survive: an empty `since` means "use the
// default window", which every registered format would reject.
func syslogTimeParam(description string) apischema.Property {
	return optString(64, "<YYYY-MM-DD[ HH:MM[:SS]]|-1h|30m ago|unix seconds>", description)
}

// registerNodeEndpoints declares the 38 node routes served by NodeHandler.
//
// Every one of them is the uniform shape: resolve the cluster from the
// path, then one static requireClusterPerm. So all 38 declare a plain
// cluster-scoped Check and every hand-placed call is gone from the handler
// bodies — nothing here is Deferred, Advisory, Public or SelfService, and
// nothing is global: a node belongs to exactly one cluster, and that
// cluster is the first placeholder in every path.
//
// Two permission choices are worth naming because they are not the
// obvious one:
//
//   - the support bundle is manage:node, not view:node. It is a full
//     disclosure of the host — storage backing paths, network layout, the
//     package inventory and every guest's config — so it sits with the
//     operator actions rather than with the telemetry. See GetNodeReport.
//   - the five firewall routes are view/manage:NETWORK, not :node. The
//     node's ruleset is a firewall object, and every other firewall route
//     in the API — all 28 of them in registry_firewall.go — gates on
//     :network, as does the catalogue entry itself
//     ("View networks, firewall, SDN", migrations/000016_rbac.up.sql).
//     These five asked for :firewall instead, a resource the catalogue has
//     never contained, so from the day they shipped (c129078, 2026-03-23)
//     they answered 403 to every caller including Admin — HasPermission
//     can only match a row that exists. Repointing them widens nothing in
//     practice: a Viewer already holds view:network and an Operator
//     manage:network, and those already carry the cluster-wide and
//     per-guest firewall rules, which are strictly broader than one node's.
//     TestGuard_DeclaredPermissionsExistInTheCatalogue now fails on any
//     declaration naming a resource the migrations do not seed.
//
// The node hardware listings the VM dialogs use (/bridges, /hardware/*,
// /machine-types, /cpu-models, /cpu-flags, /isos) are NOT here: they are
// VMHandler methods and are declared in registry_vms.go.
func registerNodeEndpoints(reg *Registry, h *handlers.NodeHandler) {
	// ── Inventory ─────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope,
		Description: "List every node Nexara has collected for this cluster, with its sizing, versions and last-seen time.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListByCluster,
	})
	for _, r := range []struct {
		suffix      string
		description string
		handler     Handler
	}{
		{"/disks", "List the physical disks collected for one node, by its Nexara node id.", h.ListNodeDisks},
		{"/network-interfaces", "List the network interfaces collected for one node, by its Nexara node id.", h.ListNodeNetworkInterfaces},
		{"/pci-devices", "List the PCI devices collected for one node, by its Nexara node id.", h.ListNodePCIDevices},
	} {
		reg.Register(Endpoint{
			Method:      fiber.MethodGet,
			Path:        nodeScope + "/:node_id" + r.suffix,
			Description: r.description,
			Group:       "Nodes",
			Permissions: clusterCheck("view", "node"),
			Parameters:  nodeRowParams(nil),
			Handler:     r.handler,
		})
	}

	// ── DNS, time and power ───────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope + "/:node_name/dns",
		Description: "Read a node's live DNS resolver configuration.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.GetNodeDNS,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        nodeScope + "/:node_name/dns",
		Description: "Write a node's DNS resolver configuration. Every field is replaced, so send the servers you want kept.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(apischema.Properties{
			"search": {
				Type:        apischema.String,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(255),
				Typetext:    "<domain>",
				Description: "Search domain. Required: Proxmox refuses a DNS write without one.",
			},
			// Optional and format-free because the dialog sends all three
			// unconditionally and leaves the unused ones EMPTY, which is how
			// Proxmox is told to clear that resolver. Every registered format
			// rejects "", so borrowing the ip format would 400 a request that
			// has always worked.
			"dns1": optString(64, "<ip>", "First resolver. Empty clears it."),
			"dns2": optString(64, "<ip>", "Second resolver. Empty clears it."),
			"dns3": optString(64, "<ip>", "Third resolver. Empty clears it."),
		}),
		Handler: h.SetNodeDNS,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope + "/:node_name/time",
		Description: "Read a node's timezone and its clock, as both UTC and node-local epoch seconds.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.GetNodeTime,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        nodeScope + "/:node_name/time",
		Description: "Set a node's timezone.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(apischema.Properties{
			"timezone": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(64),
				Typetext:  "<Area/Location>",
				Description: "IANA timezone name, e.g. Etc/UTC. Proxmox owns the vocabulary and rejects " +
					"a name its tzdata does not carry.",
			},
		}),
		Handler: h.SetNodeTimezone,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        nodeScope + "/:node_name/shutdown",
		Description: "Power a node off. Guests on it are not migrated first.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.ShutdownNode,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        nodeScope + "/:node_name/reboot",
		Description: "Reboot a node. Guests on it are not migrated first.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.RebootNode,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   nodeScope + "/:node_name/maintenance",
		Description: "Enter or leave HA node maintenance. Proxmox exposes no REST API for this, so it runs " +
			"ha-manager over SSH with the cluster's stored credentials; a cluster without SSH configured " +
			"can use evacuate instead.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(apischema.Properties{
			"enable": {
				Type:     apischema.Boolean,
				Typetext: "<boolean>",
				Description: "true enters maintenance and drains the node's HA guests; false leaves it. " +
					"Required, because neither direction is a safe default to assume.",
			},
		}),
		Handler: h.SetNodeMaintenance,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   nodeScope + "/:node_name/evacuate",
		Description: "Migrate every running guest off a node. Nexara picks each target with the same " +
			"HA and DRS constraints the rolling orchestrator uses, unless target_node names one.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(apischema.Properties{
			"target_node": {
				Type:     apischema.String,
				Optional: true,
				// Empty is the "let Nexara choose" spelling the dialog sends, so
				// this cannot borrow the node-name format — every registered
				// format rejects "". The handler branches on exactly that value
				// and skips the constraint load when it is set.
				Pattern:   emptyOrNodeName,
				MaxLength: apischema.Ptr(63),
				Typetext:  "<name>",
				Description: "Send every guest to this one node. Empty or omitted lets Nexara score a " +
					"target per guest against the cluster's HA and DRS rules.",
			},
		}),
		Handler: h.EvacuateNode,
	})

	// ── Disks, ZFS, LVM and directories ───────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope + "/:node_name/disks/list",
		Description: "List a node's physical disks live from Proxmox, including what each one is currently used for.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.ListLiveDisks,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope + "/:node_name/disks/smart",
		Description: "Read one disk's SMART attributes.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters: nodeParams(apischema.Properties{
			"disk": devicePathParam("Block device to read, as the disk listing reports it, e.g. /dev/sda."),
		}),
		Handler: h.GetDiskSMART,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope + "/:node_name/disks/zfs",
		Description: "List a node's ZFS pools with their health and capacity.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.ListZFSPools,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        nodeScope + "/:node_name/disks/zfs",
		Description: "Create a ZFS pool from one or more of a node's unused disks. Proxmox does the work in a background task.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(apischema.Properties{
			"name": storageObjectNameParam("Name for the new pool."),
			"raidlevel": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(32),
				Typetext:  "<single|mirror|raid10|raidz|raidz2|raidz3|draid|draid2|draid3>",
				// No Enum: Proxmox owns this vocabulary and versions it — draid
				// arrived in PVE 8 — so a copy here would date and reject a
				// level the cluster in front of the operator accepts.
				Description: "ZFS layout for the pool. Proxmox rejects a level it does not know.",
			},
			"devices": {
				Type: apischema.String,
				// The list form, built from the same body as devicePathParam
				// so the two cannot disagree about what a device path is.
				Pattern:     `^` + devicePathBody + `(?:,` + devicePathBody + `)*$`,
				MaxLength:   apischema.Ptr(4096),
				Typetext:    "</dev/...>[,/dev/...]",
				Description: "Comma-separated block devices to build the pool from. Everything on them is destroyed.",
			},
			"compression": optString(32, "<on|off|lz4|zstd|gzip|lzjb|zle>",
				"Compression algorithm. Empty or omitted leaves Proxmox's default, which is on (LZ4)."),
			"ashift": {
				Type:     apischema.Integer,
				Optional: true,
				// 0 is the "not chosen" spelling the client already reads: it
				// only sends ashift when the value is positive, so 0 has always
				// meant "let ZFS decide". Stated as the Default so the docs
				// answer what omitting it does.
				Default: 0,
				Minimum: apischema.Ptr(0.0),
				// ZFS's own ceiling: ashift is a power-of-two exponent, and 16
				// is 64 KiB. Proxmox's own minimum is 9.
				Maximum:     apischema.Ptr(16.0),
				Typetext:    "<integer>",
				Description: "Sector-size exponent, 9 (512 B) to 16 (64 KiB). 0 or omitted lets ZFS choose.",
			},
		}),
		Handler: h.CreateZFSPool,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        nodeScope + "/:node_name/disks/zfs/:pool_name",
		Description: "Destroy a ZFS pool and everything stored in it. Irreversible.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(cleanupParams(apischema.Properties{
			"pool_name": storageObjectNameParam("ZFS pool to destroy."),
		})),
		Handler: h.DeleteZFSPool,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope + "/:node_name/disks/lvm",
		Description: "List a node's LVM volume groups with their size and free space.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.ListLVM,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        nodeScope + "/:node_name/disks/lvm",
		Description: "Create an LVM volume group on one of a node's unused disks.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(apischema.Properties{
			"name":        storageObjectNameParam("Name for the new volume group."),
			"device":      devicePathParam("Block device to build the volume group on. Everything on it is destroyed."),
			"add_storage": optFlag("Also add a Proxmox storage entry pointing at the new volume group."),
		}),
		Handler: h.CreateLVM,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        nodeScope + "/:node_name/disks/lvm/:vg_name",
		Description: "Destroy an LVM volume group and every volume in it. Irreversible.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(cleanupParams(apischema.Properties{
			"vg_name": storageObjectNameParam("Volume group to destroy."),
		})),
		Handler: h.DeleteLVM,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope + "/:node_name/disks/lvmthin",
		Description: "List a node's LVM-thin pools.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.ListLVMThin,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        nodeScope + "/:node_name/disks/lvmthin",
		Description: "Create an LVM-thin pool on one of a node's unused disks.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(apischema.Properties{
			"name":        storageObjectNameParam("Name for the new thin pool."),
			"device":      devicePathParam("Block device to build the thin pool on. Everything on it is destroyed."),
			"add_storage": optFlag("Also add a Proxmox storage entry pointing at the new thin pool."),
		}),
		Handler: h.CreateLVMThin,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        nodeScope + "/:node_name/disks/lvmthin/:pool_name",
		Description: "Destroy an LVM-thin pool and every volume in it. Irreversible.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(cleanupParams(apischema.Properties{
			"pool_name": storageObjectNameParam("Thin pool to destroy."),
			// Required, and it always was: the handler answered 400 for a
			// missing one, because a thin pool name only identifies a pool
			// within its volume group.
			"volume-group": storageObjectNameParam("Volume group the thin pool lives in."),
		})),
		Handler: h.DeleteLVMThin,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope + "/:node_name/disks/directory",
		Description: "List the directory-backed storages a node mounts, with the device and filesystem behind each.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.ListDirectories,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        nodeScope + "/:node_name/disks/directory",
		Description: "Format one of a node's unused disks, mount it under /mnt and optionally add it as a storage.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(apischema.Properties{
			"name":   storageObjectNameParam("Mount name for the new directory, which becomes /mnt/pve/<name>."),
			"device": devicePathParam("Block device to format. Everything on it is destroyed."),
			"filesystem": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(32),
				Typetext:  "<ext4|xfs>",
				// No Enum, for the reason raidlevel gives above: the filesystem
				// vocabulary is Proxmox's and it versions it.
				Description: "Filesystem to create on the device.",
			},
			"add_storage": optFlag("Also add a Proxmox storage entry pointing at the new directory."),
		}),
		Handler: h.CreateDirectory,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        nodeScope + "/:node_name/disks/initgpt",
		Description: "Write a fresh GPT partition table to one of a node's disks, which is what makes an unrecognised disk usable.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(apischema.Properties{
			"disk": devicePathParam("Block device to initialize."),
		}),
		Handler: h.InitializeGPT,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        nodeScope + "/:node_name/disks/wipe",
		Description: "Wipe a node's disk, destroying every partition and signature on it. Irreversible.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(apischema.Properties{
			"disk": devicePathParam("Block device to wipe."),
		}),
		Handler: h.WipeDisk,
	})

	// ── Services ──────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope + "/:node_name/services",
		Description: "List the Proxmox services on a node with each one's running state.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.ListNodeServices,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        nodeScope + "/:node_name/services/:service/:action",
		Description: "Start, stop, restart or reload one of a node's services.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters: nodeParams(apischema.Properties{
			// :service becomes a segment of the Proxmox request path, and
			// proxmox.ServiceAction does NOT run validatePathSegment on it the
			// way its siblings do for a pool name. Requiring a leading
			// alphanumeric is what keeps "." and ".." out of that segment —
			// the traversal that lands a request on the parent collection — and
			// the class excludes both separators.
			"service": {
				Type:        apischema.String,
				Pattern:     `^[A-Za-z0-9][A-Za-z0-9._@-]*$`,
				MaxLength:   apischema.Ptr(64),
				Typetext:    "<service>",
				Description: "Service name as the service listing reports it, e.g. pveproxy.",
			},
			"action": {
				Type: apischema.String,
				// Cloned rather than aliased, for the reason the VM status
				// route's enum gives: Property.Enum is only deep-copied on the
				// StdOption path, so sharing the package-level slice would give
				// every Server's schema the same backing array.
				Enum:        slices.Clone(handlers.NodeServiceActions),
				Typetext:    "<start|stop|restart|reload>",
				Description: "What to do to the service.",
			},
		}),
		Handler: h.ServiceAction,
	})

	// ── Logs ──────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   nodeScope + "/:node_name/syslog",
		Description: "Read a node's syslog over a time window. since and until accept a Proxmox wall clock, " +
			"a relative offset such as \"-1h\", or a unix timestamp, and the latter two are rendered against " +
			"the node's own clock.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters: nodeParams(apischema.Properties{
			"start": {
				Type:     apischema.Integer,
				Optional: true,
				// -1 is Proxmox's "give me the newest entries" and is what the
				// handler substituted for a missing ?start=. Stated as the
				// Default so the docs answer what omitting it does.
				Default: -1,
				Minimum: apischema.Ptr(-1.0),
				// No Maximum: paging depth was never bounded here, and inventing
				// a ceiling would 400 a caller who has been paging happily. The
				// lower bound is what closes the real hole — strconv.Atoi mapped
				// a non-numeric value to 0, silently answering from the top.
				Typetext:    "<integer>",
				Description: "Line offset into the matched entries. -1, the default, returns the newest page.",
			},
			"limit": {
				Type:     apischema.Integer,
				Optional: true,
				Default:  handlers.DefaultSyslogLimit,
				Minimum:  apischema.Ptr(1.0),
				// The ceiling the handler CLAMPED to, read from the handler's
				// own constant. Declared as a bound instead, so ?limit=50000 is
				// a 400 rather than a silently substituted page size a caller
				// cannot tell from their own.
				Maximum:     apischema.Ptr(float64(handlers.MaxSyslogEntries)),
				Typetext:    "<integer>",
				Description: "Maximum entries to return.",
			},
			"since": syslogTimeParam("Start of the window. Omitted or empty reads the last 24 hours. " +
				"Cannot reach further back than 90 days."),
			"until": syslogTimeParam("End of the window. Omitted or empty reads up to now."),
			"service": optString(128, "<unit>",
				"Return only entries from this systemd unit. Empty or omitted returns every unit's."),
		}),
		Handler: h.GetNodeSyslog,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   nodeScope + "/:node_name/journal",
		Description: "Read a node's systemd journal by line count or cursor. The counterpart to syslog for " +
			"\"just show me the last N lines\"; its since and until go out as unix timestamps, so unlike " +
			"syslog they do not depend on the node's timezone.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters: nodeParams(apischema.Properties{
			"since": syslogTimeParam("Start of the window. Cannot reach further back than 90 days."),
			"until": syslogTimeParam("End of the window."),
			"lastentries": {
				Type:     apischema.Integer,
				Optional: true,
				// NO Default, and that is load-bearing: the handler falls back to
				// 500 lines only when NOTHING else bounds the read — no window,
				// no cursor and no line count — which is a cross-field rule a
				// per-parameter default would collapse. A Default here would make
				// every cursor-paged request also carry a line count.
				Minimum: apischema.Ptr(1.0),
				// The ceiling the handler clamped to with min(), read from its
				// own constant; declared as a bound so an over-large ask is a
				// 400 rather than a quiet substitution.
				Maximum:     apischema.Ptr(float64(handlers.MaxJournalEntries)),
				Typetext:    "<integer>",
				Description: "Return the newest N lines. Omitted with no window and no cursor reads the last 500.",
			},
			"startcursor": optString(1024, "<cursor>",
				"Journald cursor to page forward from, as a previous page's first entry reports it."),
			"endcursor": optString(1024, "<cursor>",
				"Journald cursor to page backward from, as a previous page's last entry reports it."),
		}),
		Handler: h.GetNodeJournal,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   nodeScope + "/:node_name/report",
		Description: "Download a node's pvereport support bundle as plain text — the artefact to attach when " +
			"asking for help with a cluster. Requires manage:node rather than view:node, and is audited: " +
			"the bundle discloses storage paths, network layout, the package inventory and every guest's config.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.GetNodeReport,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   nodeScope + "/:node_name/sensors",
		Description: "Read a node's hardware temperatures from its hwmon tree over SSH, since Proxmox exposes " +
			"no sensor API. Answers available=false with a reason rather than failing when the node cannot be read.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "node"),
		Parameters:  nodeParams(nil),
		Handler:     h.GetNodeSensors,
	})

	// ── Firewall ──────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope + "/:node_name/firewall/rules",
		Description: "List a node's firewall rules in evaluation order. Requires view:network, not view:node — see the resource note above.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "network"),
		Parameters:  nodeParams(nil),
		Handler:     h.ListNodeFirewallRules,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        nodeScope + "/:node_name/firewall/rules",
		Description: "Add a firewall rule to a node, at the top of its list. Requires manage:network, not manage:node — see the resource note above.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  nodeParams(createNodeFirewallRuleParams()),
		Handler:     h.CreateNodeFirewallRule,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   nodeScope + "/:node_name/firewall/rules/:pos",
		Description: "Change one of a node's firewall rules. A field left out is not sent, so Proxmox keeps " +
			"its current value — except enable, which is always written and therefore resets to 0 when the " +
			"body omits it. Requires manage:network, not manage:node — see the resource note above.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  nodeParams(updateNodeFirewallRuleParams()),
		Handler:     h.UpdateNodeFirewallRule,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        nodeScope + "/:node_name/firewall/rules/:pos",
		Description: "Delete one of a node's firewall rules by position. Requires manage:network, not manage:node — see the resource note above.",
		Group:       "Nodes",
		Permissions: clusterCheck("manage", "network"),
		Parameters:  nodeParams(apischema.Properties{"pos": firewallRulePosParam}),
		Handler:     h.DeleteNodeFirewallRule,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        nodeScope + "/:node_name/firewall/log",
		Description: "Read a node's firewall log. Requires view:network, not view:node — see the resource note above.",
		Group:       "Nodes",
		Permissions: clusterCheck("view", "network"),
		Parameters: nodeParams(apischema.Properties{
			"limit": {
				Type:     apischema.Integer,
				Optional: true,
				Default:  500,
				// The floor is 0 rather than 1 because 0 was always reachable:
				// the hand-rolled default was substituted for an absent or EMPTY
				// value only. It does NOT mean "no limit" —
				// proxmox.GetNodeFirewallLog writes the key only when it is
				// positive, so 0 omits it and Proxmox applies its own default.
				// A NEGATIVE limit is a 400 now, deliberately, where it was a
				// 200 that meant nothing: strconv.Atoi passed one through and
				// the same positive-only check dropped it, so it read exactly
				// like 0. So is an EMPTY ?limit=, rather than the default —
				// see the cluster firewall log's limit in registry_firewall.go.
				Minimum: apischema.Ptr(0.0),
				// The ceiling the handler clamped to; see the syslog limit for
				// why a bound is better than a silent substitution.
				Maximum:     apischema.Ptr(5000.0),
				Typetext:    "<integer>",
				Description: "Maximum entries to return. Omitted, 500; 0 leaves Proxmox's own default.",
			},
			"start": {
				Type:     apischema.Integer,
				Optional: true,
				Default:  0,
				Minimum:  apischema.Ptr(0.0),
				// No Maximum, for the reason the syslog start gives.
				Typetext:    "<integer>",
				Description: "Line offset into the log.",
			},
		}),
		Handler: h.GetNodeFirewallLog,
	})
}

// createNodeFirewallRuleParams and updateNodeFirewallRuleParams are the two
// bodies of the node firewall rule routes. They differ in exactly two ways,
// and both differences are what the handlers already enforced rather than
// anything introduced here: the create route requires type and action, and
// the update route additionally carries :pos.
//
// The rule body itself is firewallRuleBody in registry_firewall.go — the
// same twelve fields the cluster, guest and security-group rule routes take,
// declared once so the five rule surfaces cannot drift apart.
func createNodeFirewallRuleParams() apischema.Properties {
	return firewallRuleBody(false)
}

func updateNodeFirewallRuleParams() apischema.Properties {
	p := firewallRuleBody(true)
	p["pos"] = firewallRulePosParam
	return p
}
