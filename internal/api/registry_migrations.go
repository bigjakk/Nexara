package api

import (
	"slices"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, withParams, optFlag, optString, imageFormatParam and the
// emptyOr* sentinels — lives in registry_vms.go, where the first migrated
// domain defined it.

// migrationScope is the global collection every migration job hangs off.
// The per-cluster listing is the one route in this domain that lives under
// /clusters/:cluster_id, because it is the only one whose subject is a
// cluster rather than a job.
const migrationScope = pathPrefix + "migrations"

// migrationJobIDParam is a migration job's Nexara row id as a PATH
// parameter.
//
// It is spelled :id rather than :job_id because that is what the four
// routes already register and a path rename breaks every caller. The name
// is one of the two clusterIDFromParam reads (see gateParamNames in
// registry.go), which is safe here only because it resolves to the PATH:
// checkPathParams refuses the same name declared as a body or query
// parameter, and namesACluster refuses a cluster-scoped Check on a path
// whose first placeholder is not :cluster_id — so the fallback cannot turn
// this job id into a cluster the gate would authorize.
var migrationJobIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Typetext:    "<uuid>",
	Description: "Nexara migration job identifier.",
}

// migrationListParams are the paging parameters both listings take.
//
// Both were read with fiber.Query[int] and CLAMPED rather than refused:
// ?limit=5000 answered with 50 rows and ?offset=-1 answered from the top.
// The schema bounds them instead, so an out-of-range value is now a 400
// that names the field — the same trade the CVE scan listing made in
// Phase 6b, and for the same reason: a silently substituted page size is
// indistinguishable from the caller's own, so a paging bug looks like
// missing data.
func migrationListParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"limit": {
			Type:     apischema.Integer,
			Optional: true,
			// The value the handler substituted for a missing ?limit=.
			Default:     50,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(500.0),
			Typetext:    "<integer>",
			Description: "Maximum jobs to return.",
		},
		"offset": {
			Type:        apischema.Integer,
			Optional:    true,
			Default:     0,
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(1000000.0),
			Typetext:    "<integer>",
			Description: "Jobs to skip before the first one returned.",
		},
	}, extra)
}

// migrationJobReason is the Deferred justification the five job routes
// share.
//
// A migration straddles TWO clusters, and which two is a property of the
// job row rather than of the request: /migrations/:id names a job, not a
// cluster, so there is nothing in the path for clusterIDFromParam to
// resolve and no middleware could run the check. The handlers load the row
// and then authorize against the clusters it names — both of them for a
// write, either one for a read — which is a decision only the handler can
// make.
const migrationJobReason = "the clusters to authorize are stored ON the job row, not named in the path: " +
	"the handler loads it with GetMigrationJob and then requires manage:migration on both the source " +
	"and the target cluster (view:migration on either one for the read)"

// registerMigrationEndpoints declares the 7 migration routes served by
// MigrationHandler.
//
// Only ONE of them is a plain Check: the per-cluster listing, whose
// subject is the cluster in its own path. The other six are the reason
// this domain is worth calling out — five are Deferred because the
// clusters they authorize come out of the job row (see
// migrationJobReason), and the global listing is Advisory because it does
// not gate at all.
//
// POST /migrations is Deferred for the same reason read from the other
// side: the two clusters are in the BODY, and a body-resolved permission
// is exactly what middleware cannot see. Declaring either of them as
// "cluster_id" would additionally be refused by checkPathParams, which is
// the guard that stops a gate and a handler acting on different clusters.
func registerMigrationEndpoints(reg *Registry, h *handlers.MigrationHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   migrationScope,
		Description: "List migration jobs across every cluster the caller can see. Filtered rather than " +
			"gated: a job is visible when the caller holds view:migration on its source OR its target cluster.",
		Group: "Migrations",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check: Check{Action: "view", Resource: "migration", Scope: ScopeCluster},
			Reason: "accessibleClusters(\"view\", \"migration\") narrows the SQL scope and the handler " +
				"re-checks each row with access.PermitsCluster on either end; there is no single cluster " +
				"a gate could resolve",
		}},
		Parameters: migrationListParams(nil),
		Handler:    h.List,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   migrationScope,
		// Deferred renders as the bare word "deferred", so the permission an
		// operator building a role needs is spelled out here — the same
		// reason convert-to-template carries its requirement in prose.
		Description: "Create a migration job. The job is only recorded; nothing moves until it is executed. " +
			"Requires manage:migration on the source cluster, and on the target cluster as well when the two differ.",
		Group:       "Migrations",
		Permissions: Permissions{Deferred: migrationBodyReason},
		Parameters:  createMigrationParams(),
		Handler:     h.Create,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   migrationScope + "/:id",
		Description: "Read one migration job, including its pre-flight report and progress. " +
			"Requires view:migration on the job's source or target cluster.",
		Group:       "Migrations",
		Permissions: Permissions{Deferred: migrationJobReason},
		Parameters:  apischema.Properties{"id": migrationJobIDParam},
		Handler:     h.Get,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   migrationScope + "/:id/check",
		Description: "Run a migration job's pre-flight checks and store the report on the job. " +
			"Requires manage:migration on the job's source cluster, and on its target cluster as well when the two differ.",
		Group:       "Migrations",
		Permissions: Permissions{Deferred: migrationJobReason},
		Parameters:  apischema.Properties{"id": migrationJobIDParam},
		Handler:     h.RunCheck,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   migrationScope + "/:id/execute",
		Description: "Start a migration job. It runs detached, so the response says only that it started; " +
			"poll the job for progress. Requires manage:migration on the job's source cluster, and on its " +
			"target cluster as well when the two differ.",
		Group:       "Migrations",
		Permissions: Permissions{Deferred: migrationJobReason},
		Parameters:  apischema.Properties{"id": migrationJobIDParam},
		Handler:     h.Execute,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   migrationScope + "/:id/cancel",
		Description: "Cancel a migration job. Requires manage:migration on the job's source cluster, and " +
			"on its target cluster as well when the two differ.",
		Group:       "Migrations",
		Permissions: Permissions{Deferred: migrationJobReason},
		Parameters:  apischema.Properties{"id": migrationJobIDParam},
		Handler:     h.Cancel,
	})

	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/migrations",
		Description: "List the migration jobs this cluster is the SOURCE of, newest first.",
		Group:       "Migrations",
		Permissions: clusterCheck("view", "migration"),
		Parameters:  clusterParams(migrationListParams(nil)),
		Handler:     h.ListByCluster,
	})
}

// migrationBodyReason is the Deferred justification for the create route.
const migrationBodyReason = "the clusters to authorize are named in the BODY (source_cluster_id and " +
	"target_cluster_id), which no middleware can read: the handler requires manage:migration on the " +
	"source cluster and, when the target differs, on the target as well"

// createMigrationParams is the body of POST /api/v1/migrations.
//
// Every parameter the handler refused an empty or zero value for is
// required here, and nothing else is — the frontend sends all sixteen keys
// on every create, so a parameter that quietly became required would be
// invisible to a fixture-driven test. See
// TestMigrationCreateRequiresOnlyWhatTheHandlerDid.
func createMigrationParams() apischema.Properties {
	return apischema.Properties{
		"source_cluster_id": {
			Type:        apischema.String,
			Format:      "uuid",
			Typetext:    "<uuid>",
			Description: "Cluster the guest is migrating out of.",
		},
		"target_cluster_id": {
			Type:     apischema.String,
			Format:   "uuid",
			Typetext: "<uuid>",
			Description: "Cluster the guest is migrating into. Must equal source_cluster_id for an " +
				"intra-cluster migration.",
		},
		"source_node": requiredNode("Node the guest currently runs on."),
		"target_node": {
			Type:     apischema.String,
			Optional: true,
			// The dialogs send target_node unconditionally and leave it EMPTY
			// for a storage-only move, so the node-name format would 400 a
			// request that has always worked. The handler substitutes the
			// source node in that case and otherwise refuses an empty one for
			// an intra-cluster live migration, which is a cross-field rule no
			// per-parameter format can express.
			Pattern:   emptyOrNodeName,
			MaxLength: apischema.Ptr(63),
			Typetext:  "<name>",
			Description: "Node to migrate onto. Empty is allowed for a storage-only move, which keeps " +
				"the guest where it is.",
		},
		"vmid": {
			Type:        apischema.Integer,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(999999999.0),
			Typetext:    "<integer>",
			Description: "Proxmox VMID of the guest to migrate.",
		},
		"vm_type": {
			Type: apischema.String,
			// Cloned rather than aliased, for the reason the VM status
			// route's enum gives: Property.Enum is only deep-copied on the
			// StdOption path, so sharing the package-level slice would give
			// every Server's schema the same backing array.
			Enum:        slices.Clone(handlers.MigrationVMTypes),
			Typetext:    "<qemu|lxc>",
			Description: "Guest kind. qemu is a VM, lxc a container.",
		},
		"migration_type": {
			Type:     apischema.String,
			Enum:     slices.Clone(handlers.MigrationTypes),
			Typetext: "<intra-cluster|cross-cluster>",
			Description: "Whether the guest stays inside its cluster or moves to another one. " +
				"Cross-cluster is a copy over the wire, not a live move.",
		},
		"migration_mode": {
			Type:     apischema.String,
			Enum:     slices.Clone(handlers.MigrationModes),
			Optional: true,
			// The value the handler substituted for a missing mode. Stating it
			// as the Default is what lets the docs answer "what happens if I
			// leave this out" — and it drops one spelling the handler used to
			// accept: an explicit "" also meant live, and an Enum rejects it,
			// because apischema treats the empty string as a value the caller
			// supplied rather than an absent one. Omitting the key is how a
			// caller asks for the default, and that still works.
			Default:  handlers.MigrationModeDefault,
			Typetext: "<live|storage|both>",
			Description: "What to move. live relocates the guest, storage moves its disks and leaves it " +
				"where it is, both does each in turn. Only live is supported cross-cluster.",
		},
		"storage_map": {
			Type:     apischema.Object,
			Optional: true,
			Typetext: "<object>",
			Description: "Per-disk destination storage, as a flat object of string values. Either this or " +
				"target_storage is required for the storage and both modes.",
		},
		"network_map": {
			Type:        apischema.Object,
			Optional:    true,
			Typetext:    "<object>",
			Description: "Source bridge to target bridge, as a flat object of string values. Cross-cluster only.",
		},
		"online":        optFlag("Migrate without stopping the guest."),
		"delete_source": optFlag("Remove the guest from the source cluster once the copy completes. Cross-cluster only."),
		"bwlimit_kib": {
			Type:     apischema.Integer,
			Optional: true,
			Default:  0,
			Minimum:  apischema.Ptr(0.0),
			// The column is an int32, so this is its ceiling rather than a
			// policy: a larger value used to fail JSON binding and come back
			// as "Invalid request body", naming no field.
			Maximum:     apischema.Ptr(2147483647.0),
			Typetext:    "<integer> (KiB/s, 0 for unlimited)",
			Description: "Cap the transfer's bandwidth in KiB/s. 0 means no limit.",
		},
		"target_vmid": {
			Type:     apischema.Integer,
			Optional: true,
			// 0 is the "let the target cluster keep the source VMID" spelling
			// the dialogs send, so the minimum is 0 rather than 1.
			Default:     0,
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(999999999.0),
			Typetext:    "<integer>",
			Description: "VMID to give the guest on the target cluster. 0 keeps the source VMID.",
		},
		"target_storage": {
			Type:     apischema.String,
			Optional: true,
			// Empty is the sentinel both dialogs send when the mode does not
			// need a single destination storage, so this cannot borrow the
			// storage-id format — every registered format rejects "".
			Pattern:   emptyOrStorageID,
			MaxLength: apischema.Ptr(100),
			Typetext:  "<storage>",
			Description: "Single destination storage for every disk. Either this or storage_map is " +
				"required for the storage and both modes.",
		},
		"disk_format": imageFormatParam("Convert the disks on the way over. Empty lets the storage decide, " +
			"which is the only valid choice for a container or for block storage."),
	}
}
