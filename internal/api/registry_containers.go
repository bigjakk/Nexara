package api

import (
	"slices"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, withParams, cloneParams, optFlag, optString, optCount,
// diskKeyParam, requiredNode, requiredStorage, snapshotNameParam and the
// emptyOr* patterns — lives in registry_vms.go, where the first migrated
// domain defined it. It is reused here rather than copied: a second
// definition of "what a node name looks like" is the drift this registry
// exists to remove.

// containerScope is the path prefix every container route hangs off.
const containerScope = clusterScope + "/containers"

// ctParams is the two path parameters every per-container route carries.
//
// :cluster_id is FIRST, and that is load-bearing rather than cosmetic:
// namesACluster (permissions.go) refuses a cluster-scoped Check on a path
// whose first placeholder is not the cluster, because clusterIDFromParam
// would otherwise resolve the wrong object. Every route below was checked
// against the real paths in router.go rather than assumed.
//
// ct_id is Nexara's own row id for the guest, not the Proxmox VMID — the
// collector deletes and re-inserts guest rows, so the two are different
// identifiers.
func ctParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"ct_id":      apischema.StdOption("ct-id"),
	}, extra)
}

// registerContainerEndpoints declares the 18 LXC routes served by
// ContainerHandler.
//
// Every one of them is the uniform shape the legacy handlers had — resolve
// the cluster from the path, then one static requireClusterPerm — so all
// 18 declare a plain Check and the hand-placed call is gone from the
// handler body. None is Deferred, and that is a finding rather than an
// omission: the VM domain needed two Deferred routes because
// convert-to-template and clone-to-template reach BOTH guest kinds through
// a /vms/ path and pick the client method off the loaded row's Type. The
// converse cannot happen here. Every route below loads its guest through
// queries.GetContainer, whose SQL is `WHERE id = $1 AND type = 'lxc'`, so
// a qemu guest's uuid on a /containers/ path is a 404 and no container
// route can ever act on a VM. The permission is therefore knowable before
// the lookup, which is what makes a middleware gate correct.
//
// None is Advisory either: none of these filters a listing through
// accessibleClusters instead of gating it — ListByCluster is gated on the
// cluster in the path like the rest.
func registerContainerEndpoints(reg *Registry, h *handlers.ContainerHandler) {
	// ── Containers ────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        containerScope,
		Description: "List every LXC container Nexara has collected for this cluster.",
		Group:       "Containers",
		Permissions: clusterCheck("view", "container"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListByCluster,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        containerScope,
		Description: "Create an LXC container on a named node from an OS template.",
		Group:       "Containers",
		Permissions: clusterCheck("manage", "container"),
		Parameters:  clusterParams(createCTParams()),
		Handler:     h.CreateContainer,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        containerScope + "/:ct_id",
		Description: "Get one container's collected inventory row.",
		Group:       "Containers",
		Permissions: clusterCheck("view", "container"),
		Parameters:  ctParams(nil),
		Handler:     h.GetContainer,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        containerScope + "/:ct_id",
		Description: "Destroy a container and its volumes. Irreversible.",
		Group:       "Containers",
		Permissions: clusterCheck("delete", "container"),
		Parameters:  ctParams(nil),
		Handler:     h.DestroyContainer,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        containerScope + "/:ct_id/status",
		Description: "Change a container's power state.",
		Group:       "Containers",
		Permissions: clusterCheck("execute", "container"),
		Parameters: ctParams(apischema.Properties{
			"action": {
				Type: apischema.String,
				// Cloned rather than aliased, for the reason the VM status
				// route's enum gives: Property.Enum is only deep-copied on
				// the StdOption path, so sharing the package-level slice
				// would give every Server's schema the same backing array.
				Enum:     slices.Clone(handlers.ContainerStatusActions),
				Typetext: "<start|stop|shutdown|reboot|suspend|resume>",
				Description: "Power action to dispatch. There is no reset: LXC has no equivalent of a " +
					"hardware reset button, so the VM route's seventh action is absent here.",
			},
		}),
		Handler: h.PerformAction,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        containerScope + "/:ct_id/clone",
		Description: "Clone a container, full or linked.",
		Group:       "Containers",
		Permissions: clusterCheck("manage", "container"),
		Parameters:  ctParams(cloneParams()),
		Handler:     h.CloneContainer,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        containerScope + "/:ct_id/convert-to-template",
		Description: "Convert a stopped container into a template. Irreversible in Proxmox.",
		Group:       "Containers",
		Permissions: clusterCheck("manage", "container"),
		Parameters:  ctParams(nil),
		Handler:     h.ConvertToTemplate,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        containerScope + "/:ct_id/clone-to-template",
		Description: "Clone a container and convert the clone into a template once it settles.",
		Group:       "Containers",
		Permissions: clusterCheck("manage", "container"),
		Parameters:  ctParams(cloneParams()),
		Handler:     h.CloneToTemplate,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        containerScope + "/:ct_id/migrate",
		Description: "Migrate a container to another node in the same cluster.",
		Group:       "Containers",
		Permissions: clusterCheck("execute", "container"),
		Parameters: ctParams(apischema.Properties{
			"target": requiredNode("Node to migrate onto."),
			"online": optFlag("Migrate without stopping the container. Proxmox restarts an LXC to move it " +
				"unless the volumes are on shared storage."),
		}),
		Handler: h.MigrateContainer,
	})

	// ── Configuration ─────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        containerScope + "/:ct_id/config",
		Description: "Read a container's live Proxmox configuration.",
		Group:       "Containers",
		Permissions: clusterCheck("view", "container"),
		Parameters:  ctParams(nil),
		Handler:     h.GetContainerConfig,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        containerScope + "/:ct_id/config",
		Description: "Write Proxmox configuration keys on a container.",
		Group:       "Containers",
		Permissions: clusterCheck("manage", "container"),
		Parameters: ctParams(apischema.Properties{
			"fields": {
				Type:        apischema.Object,
				Typetext:    "<object>",
				Description: "Proxmox config keys to set, as a flat object of string values.",
			},
		}),
		Handler: h.SetContainerConfig,
	})

	// ── Snapshots ─────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        containerScope + "/:ct_id/snapshot-capability",
		Description: "Report whether this container's volumes support snapshots, and which ones block it if not.",
		Group:       "Containers",
		Permissions: clusterCheck("view", "container"),
		Parameters:  ctParams(nil),
		Handler:     h.GetSnapshotCapability,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        containerScope + "/:ct_id/snapshots",
		Description: "List a container's snapshots, excluding the synthetic \"current\" row.",
		Group:       "Containers",
		Permissions: clusterCheck("view", "container"),
		Parameters:  ctParams(nil),
		Handler:     h.ListSnapshots,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        containerScope + "/:ct_id/snapshots",
		Description: "Take a snapshot of a container.",
		Group:       "Containers",
		Permissions: clusterCheck("execute", "container"),
		Parameters: ctParams(apischema.Properties{
			"snap_name": {
				Type:        apischema.String,
				Format:      "pve-configid",
				Typetext:    "<name>",
				Description: `Snapshot name: 2-40 characters, starting with a letter. "current" is reserved by Proxmox.`,
			},
			"description": {
				Type:        apischema.String,
				Optional:    true,
				MaxLength:   apischema.Ptr(4096),
				Typetext:    "<string>",
				Description: "Free-text note stored with the snapshot.",
			},
			// No vmstate, deliberately. The VM route takes one because
			// QEMU can save the guest's RAM into the snapshot; LXC has no
			// equivalent and Proxmox's CT snapshot endpoint takes no such
			// parameter, so the legacy body struct carried a vmstate field
			// that was bound and then never read. Declaring it here would
			// document a parameter that does nothing; leaving it out means
			// a caller who sends it is told so instead of being silently
			// ignored.
		}),
		Handler: h.CreateSnapshot,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        containerScope + "/:ct_id/snapshots/:snap_name",
		Description: "Delete one of a container's snapshots.",
		Group:       "Containers",
		Permissions: clusterCheck("delete", "container"),
		Parameters:  ctParams(apischema.Properties{"snap_name": snapshotNameParam}),
		Handler:     h.DeleteSnapshot,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        containerScope + "/:ct_id/snapshots/:snap_name/rollback",
		Description: "Roll a container back to one of its snapshots, discarding everything written since.",
		Group:       "Containers",
		Permissions: clusterCheck("execute", "container"),
		Parameters:  ctParams(apischema.Properties{"snap_name": snapshotNameParam}),
		Handler:     h.RollbackSnapshot,
	})

	// ── Volumes ───────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        containerScope + "/:ct_id/disks/resize",
		Description: "Grow one of a container's volumes. Proxmox cannot shrink one.",
		Group:       "Containers",
		Permissions: clusterCheck("manage", "container"),
		Parameters: ctParams(apischema.Properties{
			"disk": diskKeyParam("Config key of the volume to resize, e.g. rootfs or mp0."),
			"size": {
				Type: apischema.String,
				// Proxmox's resize rule, the VM route's own — the two were
				// the same regex written out twice and now share the
				// catalogue's disk-resize. Deliberately NOT the disk-size
				// format: a leading "+" means "grow by", and its absence
				// means "grow to", so normalizing to a bare GiB count
				// would turn a delta into an absolute size.
				Pattern:     apischema.Rule("disk-resize"),
				Typetext:    "<+size|size><K|M|G|T>",
				Description: `New size, or a "+" delta to grow by, e.g. "+8G" or "64G".`,
			},
		}),
		Handler: h.ResizeDisk,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        containerScope + "/:ct_id/volumes/move",
		Description: "Move one of a container's volumes onto another storage.",
		Group:       "Containers",
		Permissions: clusterCheck("execute", "container"),
		Parameters: ctParams(apischema.Properties{
			// Named "volume" rather than "disk", which is the name the
			// endpoint has always taken and the one the frontend sends.
			"volume":  diskKeyParam("Config key of the volume to move, e.g. rootfs or mp0."),
			"storage": requiredStorage("Storage to move the volume onto."),
			// No format parameter: the shared DiskMoveSpec carries one for
			// the VM path, and CTParams drops it because LXC has no image
			// format to convert between.
			"delete": optFlag("Delete the source volume once the copy completes."),
			"bwlimit_kib": {
				Type:        apischema.Integer,
				Optional:    true,
				Minimum:     apischema.Ptr(0.0),
				Typetext:    "<integer> (KiB/s, 0 for unlimited)",
				Description: "Cap the copy's bandwidth in KiB/s. 0 means no limit.",
			},
		}),
		Handler: h.MoveVolume,
	})
}

// createCTParams is the body of POST /clusters/:cluster_id/containers.
//
// Only three parameters are required, matching what the handler and the
// Proxmox client have always enforced: vmid, node and ostemplate. Every
// other key is dropped by CreateCT when it is empty (see client_guests.go),
// so "" and "absent" mean the same thing to Proxmox — which is why the two
// values the create dialog can legitimately send empty, storage and the
// root filesystem spec, carry no format. Borrowing storage-id for the
// former would 400 a dialog that has always worked, the same trap the
// clone body's emptyOrStorageID documents.
//
// Most of the rest is a plain optional string for the reason createVMParams
// gives at length: these are Proxmox's own configuration vocabulary, which
// Proxmox owns, versions and rejects with a message of its own. What the
// schema IS doing here is closing the parameter SET, so a misspelled key
// comes back as "unknown parameter" rather than being silently dropped.
func createCTParams() apischema.Properties {
	return apischema.Properties{
		"vmid": {
			Type:        apischema.Integer,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(999999999.0),
			Typetext:    "<integer>",
			Description: "VMID for the new container.",
		},
		"node": requiredNode("Node to create the container on."),
		"ostemplate": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(512),
			Typetext:    "<volume id>",
			Description: `Volume id of the OS template, e.g. "store01:vztmpl/debian-12-standard_12.7-1_amd64.tar.zst".`,
		},

		"hostname": optString(255, "<string>", "Hostname for the container. Proxmox picks one when omitted."),
		"storage": {
			Type:      apischema.String,
			Optional:  true,
			Pattern:   emptyOrStorageID,
			MaxLength: apischema.Ptr(100),
			Typetext:  "<storage>",
			Description: "Storage to allocate the root filesystem on. Empty or omitted lets Proxmox " +
				"choose, and the create dialog sends it empty when no storage was picked.",
		},
		"rootfs": optString(512, "<volume>", `Root filesystem spec, e.g. "store01:8" for an 8 GiB volume.`),

		// Hardware. All three are forwarded only when positive (CreateCT in
		// internal/proxmox/client_guests.go), so 0 is the same as omitting
		// them and Proxmox's own default applies. Saying "0 disables swap"
		// here would be a false claim rendered straight into the published
		// API docs — clearing the field in the create dialog yields
		// Number("") === 0, so it is reachable rather than theoretical.
		"memory": optCount(4194304, "RAM in MiB. Omitted or 0 leaves Proxmox's default."),
		"swap":   optCount(4194304, "Swap in MiB. Omitted or 0 leaves Proxmox's default."),
		"cores":  optCount(1024, "CPU cores. Omitted or 0 leaves Proxmox's default."),
		"net0":   optString(512, "<name>=<value>[,<name>=<value>]", "First network device, as a Proxmox LXC net string."),

		// Credentials. Both are sent by the create dialog whether or not
		// they were filled in, so neither may carry a format.
		"password": optString(1024, "<password>", "Root password to set inside the container."),
		"ssh_keys": optString(32768, "<keys>", "SSH public keys to authorize for root, one per line."),

		"unprivileged": optFlag("Create an unprivileged container, which maps root inside the container to " +
			"an unprivileged uid on the host."),
		"start": optFlag("Start the container once it is created."),

		// Metadata.
		"description":  optString(8192, "<string>", "Free-text note stored on the container."),
		"tags":         optString(1024, "<tags>", "Semicolon-separated Proxmox tags."),
		"pool":         optPoolID("Resource pool to place the container in."),
		"nameserver":   optString(512, "<addresses>", "DNS servers for the container."),
		"searchdomain": optString(512, "<domains>", "DNS search domains for the container."),

		"extra": {
			Type:     apischema.Object,
			Optional: true,
			Typetext: "<object>",
			Description: "Additional Proxmox LXC config keys as a flat object of string values — " +
				"features, cpulimit, cpuunits, arch, onboot, protection, startup, cmode and anything " +
				"else this schema does not name.",
		},
	}
}
