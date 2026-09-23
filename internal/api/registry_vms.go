package api

import (
	"maps"
	"slices"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// buildRegistry declares every migrated endpoint against a fresh registry
// bound to THIS Server's handlers.
//
// It mirrors router.go's nil-gating: setupRoutes skips a legacy block
// whose handler is nil, and a registry endpoint whose Handler were nil
// would panic in Register instead. The stub server every guard test
// builds fills all of them, so the gate never hides a route from the
// guards — see requireAllHandlersStubbed.
func (s *Server) buildRegistry() *Registry {
	reg := NewRegistry()
	if s.vmHandler != nil {
		registerVMEndpoints(reg, s.vmHandler)
	}
	if s.containerHandler != nil {
		registerContainerEndpoints(reg, s.containerHandler)
	}
	if s.nodeHandler != nil {
		registerNodeEndpoints(reg, s.nodeHandler)
	}
	if s.storageHandler != nil {
		registerStorageEndpoints(reg, s.storageHandler)
	}
	if s.cephHandler != nil {
		registerCephEndpoints(reg, s.cephHandler)
	}
	if s.haHandler != nil {
		registerHAEndpoints(reg, s.haHandler)
	}
	if s.drsHandler != nil {
		registerDRSEndpoints(reg, s.drsHandler)
	}
	if s.cveHandler != nil {
		registerCVEEndpoints(reg, s.cveHandler)
	}
	if s.replicationHandler != nil {
		registerReplicationEndpoints(reg, s.replicationHandler)
	}
	if s.migrationHandler != nil {
		registerMigrationEndpoints(reg, s.migrationHandler)
	}
	if s.clusterOptionsHandler != nil {
		registerClusterOptionsEndpoints(reg, s.clusterOptionsHandler)
	}
	if s.guestToolsHandler != nil {
		registerGuestToolsEndpoints(reg, s.guestToolsHandler)
	}
	if s.virtioWinHandler != nil {
		registerVirtioWinEndpoints(reg, s.virtioWinHandler)
	}
	if s.pbsHandler != nil {
		registerPBSEndpoints(reg, s.pbsHandler)
	}
	// AFTER registerPBSEndpoints, so /pbs-servers and /pbs-servers/:id keep
	// the positions they had in router.go — Fiber matches in registration
	// order. It is gated on backupHandler alone, unlike the legacy block,
	// which nested these 19 routes inside the pbsHandler gate as well: they
	// are BackupHandler's, and a Server holding one handler but not the
	// other would have silently dropped them.
	if s.backupHandler != nil {
		registerBackupEndpoints(reg, s.backupHandler)
	}
	if s.vmImportHandler != nil {
		registerVMImportEndpoints(reg, s.vmImportHandler)
	}
	if s.reportHandler != nil {
		registerReportEndpoints(reg, s.reportHandler)
	}
	if s.veeamHandler != nil {
		// ONE limiter instance per group, shared across the routes that carry
		// it, exactly as the legacy block built them: separate instances hold
		// separate stores, which would silently multiply the budget each one
		// exists to cap. See veeamConnectLimiter in middleware.go.
		registerVeeamEndpoints(reg, s.veeamHandler, s.veeamConnectLimiter(), s.veeamControlLimiter())
	}
	if s.alertHandler != nil {
		registerAlertEndpoints(reg, s.alertHandler)
	}
	if s.accessHandler != nil {
		registerAccessEndpoints(reg, s.accessHandler)
	}
	if s.acmeHandler != nil {
		registerACMEEndpoints(reg, s.acmeHandler)
	}
	if s.rollingUpdateHandler != nil {
		registerRollingUpdateEndpoints(reg, s.rollingUpdateHandler)
	}
	if s.rbacHandler != nil {
		registerRBACEndpoints(reg, s.rbacHandler)
	}
	if s.userHandler != nil {
		registerUserEndpoints(reg, s.userHandler)
	}
	if s.apiKeyHandler != nil {
		registerAPIKeyEndpoints(reg, s.apiKeyHandler)
	}
	if s.ldapHandler != nil {
		registerLDAPEndpoints(reg, s.ldapHandler)
	}
	if s.oidcHandler != nil {
		registerOIDCEndpoints(reg, s.oidcHandler)
	}
	if s.totpHandler != nil {
		registerTOTPEndpoints(reg, s.totpHandler)
	}
	if s.authHandler != nil {
		registerAuthEndpoints(reg, s.authHandler)
	}
	if s.networkHandler != nil {
		// One handler, four declaration files: see the file comment in
		// registry_networks.go for the split.
		registerNetworkInterfaceEndpoints(reg, s.networkHandler)
		registerFirewallEndpoints(reg, s.networkHandler)
		registerSDNEndpoints(reg, s.networkHandler)
		registerFirewallTemplateEndpoints(reg, s.networkHandler)
	}
	if s.metricsHandler != nil {
		registerMetricsEndpoints(reg, s.metricsHandler)
	}
	if s.poolHandler != nil {
		registerPoolEndpoints(reg, s.poolHandler)
	}
	if s.aptRepositoryHandler != nil {
		registerAptRepositoryEndpoints(reg, s.aptRepositoryHandler)
	}
	if s.metricServerHandler != nil {
		registerMetricServerEndpoints(reg, s.metricServerHandler)
	}
	if s.scheduleHandler != nil {
		registerScheduleEndpoints(reg, s.scheduleHandler)
	}
	if s.searchHandler != nil {
		registerSearchEndpoints(reg, s.searchHandler)
	}
	if s.guestSnapshotHandler != nil {
		registerGuestSnapshotEndpoints(reg, s.guestSnapshotHandler)
	}
	if s.favoritesHandler != nil {
		registerFavoritesEndpoints(reg, s.favoritesHandler)
	}
	if s.vmFoldersHandler != nil {
		registerVMFolderEndpoints(reg, s.vmFoldersHandler)
	}
	if s.notificationDLQHandler != nil {
		registerNotificationDLQEndpoints(reg, s.notificationDLQHandler)
	}
	if s.taskHandler != nil {
		registerTaskEndpoints(reg, s.taskHandler)
	}
	if s.auditHandler != nil {
		registerAuditEndpoints(reg, s.auditHandler)
	}
	if s.settingsHandler != nil {
		registerSettingsEndpoints(reg, s.settingsHandler)
	}
	if s.clusterHandler != nil {
		// THREE limiter instances, exactly as the legacy block built them.
		// fingerprintFetchLimiter() returns a NEW limiter on every call and the
		// legacy block called it twice, so fetch-fingerprint and
		// verify-certificate each had their own 30/min store despite sharing a
		// key string. Passing one instance to both would halve the budget for a
		// caller who uses both — a live rate-limit change, not a migration. See
		// registerClusterEndpoints, and contrast registerVeeamEndpoints, whose
		// legacy block built ONE instance and therefore shares one.
		registerClusterEndpoints(reg, s.clusterHandler,
			s.clusterCreateLimiter(), s.fingerprintFetchLimiter(), s.fingerprintFetchLimiter())
	}
	if s.changelogHandler != nil {
		registerChangelogEndpoints(reg, s.changelogHandler)
	}
	// Not gated on a handler: the version probe is a method on the Server
	// itself. See registerVersionEndpoint.
	registerVersionEndpoint(reg, s)
	return reg
}

// clusterScope is the path prefix every cluster-scoped route hangs off.
// :cluster_id is the FIRST path parameter on purpose — see namesACluster
// in permissions.go, which refuses a cluster-scoped Check that cannot
// resolve the cluster the route acts on.
const clusterScope = pathPrefix + "clusters/:cluster_id"

// clusterCheck is the shorthand for the shape almost every route here
// has: one action on one resource, resolved against the cluster in the
// path.
func clusterCheck(action, resource string) Permissions {
	return Permissions{Check: &Check{Action: action, Resource: resource, Scope: ScopeCluster}}
}

// globalCheck is clusterCheck for a route whose subject is the INSTALL
// rather than one cluster — the virtio-win catalog and its download
// source, which every cluster shares.
//
// It is a separate helper rather than a scope argument on clusterCheck so
// that "this route needs an instance-wide grant" is a deliberate word at
// the call site: ScopeGlobal short-circuits the per-cluster grant lookup
// (internal/auth/rbac.go), so writing it by accident on a cluster-scoped
// route would refuse every operator who holds the permission on exactly
// the cluster they are acting on.
func globalCheck(action, resource string) Permissions {
	return Permissions{Check: &Check{Action: action, Resource: resource, Scope: ScopeGlobal}}
}

// withParams merges extra into base, so a route can state its own
// parameters without repeating the path parameters every route in its
// family carries. base is never mutated.
func withParams(base, extra apischema.Properties) apischema.Properties {
	out := make(apischema.Properties, len(base)+len(extra))
	maps.Copy(out, base)
	maps.Copy(out, extra)
	return out
}

// clusterParams is the path parameter a cluster-scoped route carries.
func clusterParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
	}, extra)
}

// vmParams is the two path parameters every per-VM route carries.
func vmParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"vm_id":      apischema.StdOption("vm-id"),
	}, extra)
}

// nodeParams is the pair a per-node route carries. node_name is the
// Proxmox node NAME, not Nexara's node row id — the two are different
// identifiers and the routes below are the ones that take the former.
func nodeParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"node_name":  apischema.StdOption("node-name"),
	}, extra)
}

// snapshotNameParam is the snapshot name as a PATH parameter, on the
// routes that act on a snapshot that already exists.
//
// It is deliberately looser than the pve-configid format the CREATE body
// uses, because its job is to ADDRESS whatever exists rather than to
// restate a create rule: Proxmox is the authority on whether that snapshot
// is there, and a second refusal here could only turn "no such snapshot"
// into a 400 the caller cannot act on, or make an object Nexara did not
// create undeletable through Nexara.
//
// "Looser" means the LENGTH and the two-character minimum, and nothing
// else. The Pattern below is the first of two layers that make this safe to
// interpolate into a Proxmox path: it admits letters, digits, '-' and '_'
// only, so no separator, dot or percent can reach the URL through it.
// Escaping is not a substitute for either layer: the four snapshot methods
// url.PathEscape the name, which does turn a "%" into "%25" — so
// "%2e%2e%2f" reaches Proxmox as a literal name — but leaves a bare "." or
// ".." alone, and those resolve upward once pveproxy normalises the path.
// The second layer is the client's own validatePathSegment (client.go),
// which the snapshot methods in client_guests.go call ("Addressing an
// existing snapshot"). Dropping the Pattern in the name of being permissive
// would leave that as the only refusal: still a refusal, but a 400 that no
// longer names snap_name, reached only after the handler has loaded the
// guest.
// TestSnapshotNameParamRefusesTraversal sends "%2e%2e%2f" and the rest at
// all four routes and fails the moment one of them reaches a handler, and
// TestContainerSnapshotNameRules fails if the Pattern goes missing at all.
//
// The one-character allowance is NOT because upstream permits one:
// $CONFIGID_RE is `[a-z][a-z0-9_-]+` — a letter then ONE OR MORE, so
// upstream's minimum is two as well. Nothing in Proxmox takes a
// single-character configid: pve-ha-group-id and pve-ha-rule-id
// (pve-ha-manager src/PVE/HA/Tools.pm) are the bare format with no
// override, so the HA rules that share this pattern have the same minimum
// the snapshot ones do. The catalogue's pve-configid-existing entry once
// justified the one-character allowance by saying PROXMOX accepts one —
// backwards, and contradicting its own UpstreamRule on the line above. That
// has been corrected: the entry now states that THIS rule accepts one where
// Proxmox requires two, which is the true direction and the same conclusion.
//
// The 128 is a path-segment bound and nothing more. It is NOT a claim that
// a longer snapshot is reachable: PVE's own delete and rollback take the
// pve-snapshot-name standard option too, so upstream caps them at 40 as
// well (see handlers.SnapshotMaxNameLen) and a 41-character snapshot could
// not be addressed through the Proxmox API whatever Nexara declares here.
// The cap stays generous because it is not this parameter's business to
// re-state whatever limit the Proxmox of the day enforced when the
// snapshot was taken — a second refusal here would only turn upstream's
// clear error into our vaguer one.
//
// The rule is the catalogue's pve-configid-existing, shared with
// haConfigIDParam (registry_ha.go) — looser than pve-configid by that one
// character, for the addressing reason above rather than the upstream one
// the paragraph corrects. It is a Pattern and not a Format on purpose —
// see the guard in the container snapshot tests.
var snapshotNameParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     apischema.Rule("pve-configid-existing"),
	MaxLength:   apischema.Ptr(128),
	Typetext:    "<name>",
	Description: "Snapshot name.",
}

// These endpoints have always read the EMPTY STRING as "leave it unset":
// the clone dialog sends storage:"" for a linked clone, and the pool
// selector sends pool:"" to remove a guest from its pool. apischema
// treats "" as a value the caller SUPPLIED rather than as an absent one
// (see present() in validate.go), and every registered format rejects it,
// so an optional parameter with that sentinel has to spell its rule as a
// pattern instead of borrowing a standard option's format. Tightening it
// to the format would turn a working request into a 400 — and would do it
// to every external consumer at once, not just to our own dialog.
//
// Each of the three is DERIVED in the catalogue from the very rule it is
// the sentinel twin of — node-name, storage-id and pve-poolid — rather than
// hand-written beside it. That is what keeps the twin honest: these were
// three transcriptions of rules defined elsewhere, and a transcription only
// has to be corrected once to be wrong. pve-poolid is the one that proves
// it: the segment charset really does allow a leading dot or dash, and
// pools really do nest up to three levels ("infra/prod/db"), so the
// tidier-looking `^[A-Za-z0-9][A-Za-z0-9._-]*$` a reader invents instead
// would 400 pool names Proxmox itself accepts and this API has always
// forwarded.
var (
	emptyOrNodeName  = apischema.Rule("node-name-or-empty")
	emptyOrStorageID = apischema.Rule("storage-id-or-empty")
	emptyOrPoolID    = apischema.Rule("pve-poolid-or-empty")
)

// optPoolID is the `pool` body parameter, wherever a request names a
// Proxmox resource pool.
//
// What the pattern is for is the ERROR, not safety. On SetVMPool the value
// becomes a path segment — "/pools/" + url.PathEscape(pool)
// (internal/proxmox/client_admin.go) — and pve-poolid admits "." and "..",
// which PathEscape leaves alone and pveproxy then resolves upward: "." is the
// /pools collection itself, ".." the API root. What keeps those out is the
// client's own validatePathSegment on that path (client_admin.go), not this
// rule; do not delete that guard on the belief that a body field cannot
// traverse. What the rule adds is the 400: without it junk was forwarded to
// Proxmox and came back a 502 quoting a URL the caller never wrote ("Method
// 'PUT /pools/has spaces!' not implemented") instead of an error naming the
// field.
//
// So the rule is deliberately PVE's own (emptyOrPoolID) rather than a
// stricter one of our invention: a schema that rejects ids Proxmox accepts
// turns a working request into a 400, which is a worse bug than the one
// being fixed.
//
// MaxLength matches poolIDParam's 100 for the same reason — Nexara can
// create a 100-character pool through POST /pools, and a 64-cap here would
// leave it unassignable.
//
// NOTE, not fixed here: a NESTED id ("infra/prod") is valid to PVE and now
// passes this schema, but SetVMPool still cannot apply one, because
// UpdateResourcePool uses the legacy "/pools/{poolid}" path form that
// cannot carry a slash. PVE's replacement is "PUT /pools?poolid=…". Of the
// six routes taking this parameter, SetVMPool is the only one affected —
// the other five pass `pool` as a form field and handle nesting fine.
//
// What that failure LOOKS like changed once UpdateResourcePool gained
// validatePathSegment: the separator is now refused at the client, so the
// caller gets a local 400 naming it instead of whatever Proxmox made of a
// truncated path. Better, and still not the fix.
func optPoolID(description string) apischema.Property {
	p := optString(100, "<pool>", description)
	p.Pattern = emptyOrPoolID
	return p
}

// cloneParams are the body parameters shared by clone and
// clone-to-template, which take the same Proxmox call.
func cloneParams() apischema.Properties {
	return apischema.Properties{
		"new_id": {
			Type:        apischema.Integer,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(999999999.0),
			Typetext:    "<integer>",
			Description: "VMID for the new guest.",
		},
		"name": {
			Type:        apischema.String,
			Optional:    true,
			MaxLength:   apischema.Ptr(255),
			Typetext:    "<string>",
			Description: "Name for the new guest. Proxmox names the clone after the source when omitted.",
		},
		"target": {
			Type:        apischema.String,
			Optional:    true,
			Pattern:     emptyOrNodeName,
			MaxLength:   apischema.Ptr(63),
			Typetext:    "<name>",
			Description: "Node to place the clone on. Empty or omitted keeps it on the source node.",
		},
		"full": optFlag("Make a full copy rather than a linked clone."),
		"storage": {
			Type:        apischema.String,
			Optional:    true,
			Pattern:     emptyOrStorageID,
			MaxLength:   apischema.Ptr(100),
			Typetext:    "<storage>",
			Description: "Storage for the clone's disks. Empty or omitted lets Proxmox choose.",
		},
	}
}

// imageFormatParam is the optional disk image format.
//
// It carries no Enum, and that is deliberate rather than an omission:
// three of the four callers send format:"" for "let the storage decide",
// which an Enum would reject, and proxmox.ValidImageFormat already
// enforces the vocabulary at the choke point every one of these routes
// passes through (DiskMoveSpec.Validate, DiskAttachParams.Validate). A
// second copy here would be one that drifts and one that has to grow a
// sentinel member to stay correct.
func imageFormatParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Optional:    true,
		MaxLength:   apischema.Ptr(16),
		Typetext:    "<qcow2|raw|vmdk>",
		Description: description,
	}
}

// optFlag is an optional boolean that defaults to false, which is how
// every flag on these endpoints is spelled: omitting it means "no".
//
// The Default is stated rather than left implicit even though false is
// also the zero value, because the declaration IS the documentation and
// "what happens if I leave this out" is the question it has to answer.
// Anything whose omission must stay distinguishable from an explicit
// false takes optTristateBool instead — see its doc comment.
func optFlag(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.Boolean,
		Optional:    true,
		Default:     false,
		Typetext:    "<boolean>",
		Description: description,
	}
}

// bothGuestKindsReason is the Deferred justification shared by the two
// routes that act on either guest kind through one /vms/ path.
//
// Both branch on the loaded row's Type and call the LXC client method for
// a container, so the RESOURCE half of the permission is not knowable
// until the guest has been read — which is after any middleware would
// have run. The handlers check manage:vm first and then, for a container,
// manage:container as well (requireGuestKindPerm in handlers/vms.go);
// registryEnforcementGaps holds them to reaching a real permission leaf.
const bothGuestKindsReason = "the resource depends on the guest's type, which is only known once the row is " +
	"loaded: a container converted through this route needs manage:container as well as manage:vm " +
	"(see requireGuestKindPerm)"

// registerVMEndpoints declares the VM, task, node-hardware and resource
// pool routes served by VMHandler.
//
// Every one of them is the uniform shape the legacy handlers had —
// resolve the cluster from the path, then one static
// requireClusterPerm — so all but two declare a plain Check and the
// hand-placed call is gone from the handler body.
//
// The two exceptions are convert-to-template and clone-to-template, which
// serve BOTH guest kinds through a /vms/ path and pick the client method
// off the loaded row's Type. Their resource is not knowable before the
// lookup, so they are Deferred and keep their checks in the handler — see
// bothGuestKindsReason. None is Advisory: none of these filters a listing
// through accessibleClusters instead of gating it.
func registerVMEndpoints(reg *Registry, h *handlers.VMHandler) {
	// ── Virtual machines ──────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/vms",
		Description: "List every VM Nexara has collected for this cluster.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("view", "vm"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListByCluster,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        clusterScope + "/vms",
		Description: "Create a VM on a named node.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "vm"),
		Parameters:  clusterParams(createVMParams()),
		Handler:     h.CreateVM,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/vms/:vm_id",
		Description: "Get one VM's collected inventory row.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("view", "vm"),
		Parameters:  vmParams(nil),
		Handler:     h.GetVM,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        clusterScope + "/vms/:vm_id",
		Description: "Destroy a VM and its disks. Irreversible.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("delete", "vm"),
		Parameters:  vmParams(nil),
		Handler:     h.DestroyVM,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        clusterScope + "/vms/:vm_id/status",
		Description: "Change a VM's power state.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("execute", "vm"),
		Parameters: vmParams(apischema.Properties{
			"action": {
				Type: apischema.String,
				// Cloned rather than aliased: Property.Enum is only
				// deep-copied on the StdOption path, so sharing the
				// package-level slice would give every Server's schema the
				// same backing array.
				Enum:        slices.Clone(handlers.VMStatusActions),
				Typetext:    "<start|stop|shutdown|reboot|reset|suspend|resume>",
				Description: "Power action to dispatch.",
			},
		}),
		Handler: h.PerformAction,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        clusterScope + "/vms/:vm_id/clone",
		Description: "Clone a VM, full or linked.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "vm"),
		Parameters:  vmParams(cloneParams()),
		Handler:     h.CloneVM,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/vms/:vm_id/convert-to-template",
		// The permission requirement is stated in the DESCRIPTION because
		// the Permissions field has no vocabulary for "and, if it is a
		// container, also" — a Deferred route renders as the bare word
		// "deferred", and an operator building a role would be told
		// nothing. The route's endpointMeta entry carried the same
		// sentence for the same reason; now that the declaration is what
		// the docs render, it has to carry it instead.
		Description: "Convert a stopped VM or container into a template. Irreversible in Proxmox. " +
			"Requires manage:vm, and manage:container as well when the guest is a container.",
		Group:       "Virtual Machines",
		Permissions: Permissions{Deferred: bothGuestKindsReason},
		Parameters:  vmParams(nil),
		Handler:     h.ConvertToTemplate,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/vms/:vm_id/clone-to-template",
		// See convert-to-template above for why the permission is spelled
		// out here rather than left to the Permissions field.
		Description: "Clone a guest and convert the clone into a template once it settles. " +
			"Requires manage:vm, and manage:container as well when the guest is a container.",
		Group:       "Virtual Machines",
		Permissions: Permissions{Deferred: bothGuestKindsReason},
		Parameters:  vmParams(cloneParams()),
		Handler:     h.CloneToTemplate,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        clusterScope + "/vms/:vm_id/migrate",
		Description: "Migrate a VM to another node in the same cluster.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("execute", "vm"),
		Parameters: vmParams(apischema.Properties{
			"target": requiredNode("Node to migrate onto."),
			"online": optFlag("Migrate without stopping the guest. Requires shared storage or a live-migratable disk set."),
		}),
		Handler: h.MigrateVM,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/vms/:vm_id/agent",
		Description: "Read the guest agent's reported OS and network interfaces. Answers running=false when no agent responds.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("view", "vm"),
		Parameters:  vmParams(nil),
		Handler:     h.GetGuestAgentInfo,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        clusterScope + "/vms/:vm_id/media",
		Description: "Mount an ISO on the VM's CD-ROM device, or eject it with volid=none.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("execute", "vm"),
		Parameters: vmParams(apischema.Properties{
			"volid": {
				Type:        apischema.String,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(512),
				Typetext:    "<volume id>|none",
				Description: `Volume id of the ISO ("store01:iso/debian.iso"), or "none" to eject.`,
			},
		}),
		Handler: h.ChangeMedia,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        clusterScope + "/vms/:vm_id/pool",
		Description: "Move a guest into a Proxmox resource pool, or out of its current one with an empty pool.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "pool"),
		Parameters: vmParams(apischema.Properties{
			// A pattern rather than a format: the EMPTY string is the
			// meaningful value that removes the guest from its pool, and
			// every format in the registry rejects it. This is also the
			// route whose value becomes a Proxmox path segment — see
			// optPoolID.
			"pool": optPoolID("Target pool id. An empty value removes the guest from its current pool."),
		}),
		Handler: h.SetVMPool,
	})

	// ── Configuration ─────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/vms/:vm_id/config",
		Description: "Read a VM's live Proxmox configuration.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("view", "vm"),
		Parameters:  vmParams(nil),
		Handler:     h.GetVMConfig,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPut,
		Path:        clusterScope + "/vms/:vm_id/config",
		Description: "Write Proxmox configuration keys on a VM.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "vm"),
		Parameters: vmParams(apischema.Properties{
			"fields": {
				Type:        apischema.Object,
				Typetext:    "<object>",
				Description: "Proxmox config keys to set, as a flat object of string values.",
			},
		}),
		Handler: h.SetVMConfig,
	})

	// ── Snapshots ─────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/vms/:vm_id/snapshot-capability",
		Description: "Report whether this VM's disks support snapshots, and which volumes block it if not.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("view", "vm"),
		Parameters:  vmParams(nil),
		Handler:     h.GetSnapshotCapability,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/vms/:vm_id/snapshots",
		Description: "List a VM's snapshots, excluding the synthetic \"current\" row.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("view", "vm"),
		Parameters:  vmParams(nil),
		Handler:     h.ListSnapshots,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        clusterScope + "/vms/:vm_id/snapshots",
		Description: "Take a snapshot of a VM, optionally including its RAM.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("execute", "vm"),
		Parameters: vmParams(apischema.Properties{
			// The 40 is NOT this format's bound and must not be "corrected"
			// to 128 to match it. pve-configid allows 2 to 128 (Proxmox's
			// own $CONFIGID_RE states no maximum at all); 40 is the
			// narrower cap Proxmox puts on SNAPSHOT names specifically, via
			// the pve-snapshot-name standard option that every upstream
			// snapname parameter uses. handlers.SnapshotMaxNameLen carries
			// the citation, and is referenced rather than restated so the
			// declaration and the handler cannot end up stating different
			// numbers.
			//
			// An earlier pass concluded no MaxLength should be declared
			// here at all, on the grounds that it would put an unverified
			// Nexara bound in a second place. Both halves of that turned
			// out wrong: the docs payload now publishes the rule TEXT, so
			// omitting the bound publishes "2 to 128 characters" for a
			// route that answers 400 at 41 — and the bound was never
			// Nexara's to begin with.
			//
			// The description spells "pending" out as case-insensitive
			// because it is: PVE compares it with lc(), so "Pending" is
			// refused too, and prose naming only the lowercase form would
			// leave a caller unable to predict that 400. "current" really is
			// exact-match upstream, so "Current" is accepted.
			"snap_name": {
				Type:   apischema.String,
				Format: "pve-configid",
				// This narrows the create side only. snapshotNameParam, the
				// ADDRESSING parameter on delete and rollback, stays at 128
				// because its job is to bound a path segment, not to
				// re-state Proxmox's rule; PVE caps its own delete and
				// rollback at 40 as well, so a longer snapshot is out of
				// reach there whatever Nexara declares.
				MaxLength:   apischema.Ptr(handlers.SnapshotMaxNameLen),
				Typetext:    "<name>",
				Description: `Snapshot name: 2-40 characters, starting with a letter. Proxmox reserves "current", and "pending" in any casing.`,
			},
			"description": {
				Type:        apischema.String,
				Optional:    true,
				MaxLength:   apischema.Ptr(4096),
				Typetext:    "<string>",
				Description: "Free-text note stored with the snapshot.",
			},
			"vmstate": optFlag("Include the running guest's RAM, so the rollback resumes rather than boots."),
		}),
		Handler: h.CreateSnapshot,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        clusterScope + "/vms/:vm_id/snapshots/:snap_name",
		Description: "Delete one of a VM's snapshots.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("delete", "vm"),
		Parameters:  vmParams(apischema.Properties{"snap_name": snapshotNameParam}),
		Handler:     h.DeleteSnapshot,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        clusterScope + "/vms/:vm_id/snapshots/:snap_name/rollback",
		Description: "Roll a VM back to one of its snapshots, discarding everything written since.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("execute", "vm"),
		Parameters:  vmParams(apischema.Properties{"snap_name": snapshotNameParam}),
		Handler:     h.RollbackSnapshot,
	})

	// ── Disks ─────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        clusterScope + "/vms/:vm_id/disks/resize",
		Description: "Grow one of a VM's disks. Proxmox cannot shrink a disk.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "vm"),
		Parameters: vmParams(apischema.Properties{
			"disk": diskKeyParam("Config key of the disk to resize, e.g. scsi0."),
			"size": {
				Type: apischema.String,
				// Proxmox's own resize rule, which is NOT the disk-size
				// format the attach endpoint takes: a leading "+" means
				// "grow by", and its absence means "grow to". Normalizing
				// it to bare GiB would silently turn a delta into an
				// absolute size — which is why this stays a Pattern.
				// Shared with the container route through the catalogue.
				Pattern:     apischema.Rule("disk-resize"),
				Typetext:    "<+size|size><K|M|G|T>",
				Description: `New size, or a "+" delta to grow by, e.g. "+8G" or "64G".`,
			},
		}),
		Handler: h.ResizeDisk,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        clusterScope + "/vms/:vm_id/disks/move",
		Description: "Move one of a VM's disks onto another storage, optionally converting its format.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "vm"),
		Parameters: vmParams(apischema.Properties{
			"disk":    diskKeyParam("Config key of the disk to move, e.g. scsi0."),
			"storage": requiredStorage("Storage to move the disk onto."),
			"format":  imageFormatParam("Convert the image on the way over. Only meaningful for file-backed targets; leave unset for block storage."),
			"delete":  optFlag("Delete the source volume once the copy completes."),
			"bwlimit_kib": {
				Type:        apischema.Integer,
				Optional:    true,
				Minimum:     apischema.Ptr(0.0),
				Typetext:    "<integer> (KiB/s, 0 for unlimited)",
				Description: "Cap the copy's bandwidth in KiB/s. 0 means no limit.",
			},
		}),
		Handler: h.MoveDisk,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/vms/:vm_id/disks/attach",
		Description: "Allocate a new disk on a storage and attach it to a free slot on the chosen bus. " +
			"Refuses to overwrite an occupied slot or the VM's boot disk, and refuses a size the target pool cannot hold.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "vm"),
		Parameters: vmParams(apischema.Properties{
			"bus": {
				Type:        apischema.String,
				Enum:        slices.Clone(proxmox.DiskBuses),
				Typetext:    "<scsi|sata|virtio|ide>",
				Description: "Controller to attach the disk to.",
			},
			"index": {
				Type:     apischema.Integer,
				Optional: true,
				// NO Default, and that is the whole point of this
				// endpoint's declaration. A default would make every
				// request that omitted the index look identical to one
				// that explicitly asked for slot 0 — and slot 0 on a VM
				// with a disk is its boot disk. Left defaultless,
				// p.OptInt("index") answers supplied=false and the handler
				// picks the lowest FREE slot instead.
				Minimum:  apischema.Ptr(0.0),
				Maximum:  apischema.Ptr(30.0),
				Typetext: "<integer>",
				Description: "Slot on the bus. Omit to take the lowest free one. " +
					"Per-bus ceilings: ide 3, sata 5, virtio 15, scsi 30.",
			},
			"storage": requiredStorage("Storage to allocate the new volume on."),
			"size": {
				Type: apischema.String,
				// The format both validates and NORMALIZES: 500, "500",
				// "500G" and "1T" all reach Proxmox as the bare GiB count
				// its "storage:N" allocation form requires. Free text here
				// is how "512000" once meant 500 TiB.
				Format:      "disk-size",
				Typetext:    "<number><K|M|G|T|P>",
				Description: `Size of the new disk. A bare number is GiB; "512M", "500G" and "1T" are all accepted.`,
			},
			"format": imageFormatParam("Image format. Leave unset to let the storage decide, which is the only valid choice for block storage."),
		}),
		Handler: h.AttachDisk,
	})
	// What a detach does to the volume is qemu-server's decision
	// (src/PVE/API2/Qemu.pm): update_vm's delete loop frees an unusedN key and
	// vmstate itself, through try_deallocate_drive, and only queues a drive
	// key — whose pending delete, once applied, goes through
	// vmconfig_register_unused_drive, which parks an owned volume but frees a
	// cloud-init drive. detachRemovesVolume (handlers/vm_disk_attach.go)
	// transcribes that for the audit row's removes_volume. An earlier
	// Description promised the reversible outcome for every key, on an
	// endpoint API clients reach with no Nexara dialog in front of them — so
	// it names both.
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterScope + "/vms/:vm_id/disks/detach",
		Description: "Detach a disk from a VM. For a drive key such as scsi1, Proxmox parks a volume this VM owns " +
			"in an unusedN slot rather than deleting it (an ISO or another guest's volume is only unreferenced). " +
			"Naming an unusedN key or vmstate (a hibernated VM's saved RAM), or a cloud-init drive on any key, " +
			"deletes the volume from storage instead (a volume this VM owns), and that cannot be undone.",
		Group:       "Virtual Machines",
		Permissions: clusterCheck("manage", "vm"),
		Parameters: vmParams(apischema.Properties{
			"disk": diskKeyParam("Config key of the disk to detach, e.g. scsi1. An unusedN or vmstate key deletes its " +
				"volume from storage, if this VM owns it, rather than parking it."),
		}),
		Handler: h.DetachDisk,
	})

	// ── Proxmox tasks ─────────────────────────────────────────────────
	//
	// Both of these are verbatim passthroughs of PVE's own words: GetTaskStatus
	// returns status.ExitStatus unmodified, GetTaskLog every log line's text.
	// Neither consults task_history, so neither is covered by the credential
	// scrub in internal/migration/orchestrator.go — and that scrub's own comment
	// says so, rather than claiming a reach it does not have.
	//
	// It matters for one task shape. A cross-cluster migration puts the target
	// cluster's decrypted API token in the `target-endpoint` property string it
	// hands PVE, so a rejection PVE chooses to echo can name it, and the worker's
	// last log line is `TASK ERROR: <that message>`. A view:task holder can read
	// it here for as long as Proxmox retains the task log. The orchestrator
	// scrubs the copy it PERSISTS, which outlives the task log and feeds the
	// audit join; it does not, and cannot, scrub these.
	//
	// Left unscrubbed on purpose. The value of a live task view is that it is
	// PVE's real output — the operator reading it is debugging the failure these
	// endpoints exist to explain, already holds view:task on the source cluster,
	// and can read the same text from the PVE UI. Filtering it would mean
	// guessing at the vendor's error shapes on every response for every task
	// type, and would leave the log silently lying about what PVE said. The
	// exposure is bounded by PVE's task-log retention; the persisted copy is not,
	// which is why only that one is scrubbed. The SPA's migration dialog fetches
	// the /log route for exactly this UPID
	// (frontend/src/features/vms/components/MigrateJobDialog.tsx).
	//
	// Revisit if the permission ever widens, or if PVE is observed echoing the
	// property string back — see the "STRUCTURAL exposure, not a demonstrated
	// leak" note on scrubEndpointSecret.
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/tasks/:upid",
		Description: "Get a Proxmox task's status, with progress parsed out of its log while it runs.",
		Group:       "Tasks",
		Permissions: clusterCheck("view", "task"),
		Parameters:  clusterParams(apischema.Properties{"upid": upidParam}),
		Handler:     h.GetTaskStatus,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/tasks/:upid/log",
		Description: "Read a Proxmox task's log lines.",
		Group:       "Tasks",
		Permissions: clusterCheck("view", "task"),
		Parameters:  clusterParams(apischema.Properties{"upid": upidParam}),
		Handler:     h.GetTaskLog,
	})

	// ── Node hardware and inventory, for the VM dialogs ───────────────
	for _, r := range []struct {
		suffix      string
		description string
		handler     Handler
	}{
		{"/bridges", "List a node's network bridges, for picking a VM's NIC.", h.ListBridges},
		{"/hardware/usb", "List a node's USB devices, for passthrough.", h.ListNodeUSBDevices},
		{"/hardware/pci", "List a node's PCI devices, for passthrough.", h.ListNodePCIDevices},
		{"/machine-types", "List the QEMU machine types a node offers.", h.ListMachineTypes},
		{"/cpu-models", "List the CPU models a node offers. Empty on Proxmox versions without the endpoint.", h.ListCPUModels},
		{"/cpu-flags", "List the CPU flags a node offers, with which nodes support each.", h.ListCPUFlags},
		{"/isos", "List every ISO on a node's ISO-capable storages.", h.ListNodeISOs},
	} {
		reg.Register(Endpoint{
			Method:      fiber.MethodGet,
			Path:        clusterScope + "/nodes/:node_name" + r.suffix,
			Description: r.description,
			Group:       "Nodes",
			Permissions: clusterCheck("view", "node"),
			Parameters:  nodeParams(nil),
			Handler:     r.handler,
		})
	}

	// ── Resource pools ────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/pools",
		Description: "List the cluster's Proxmox resource pools.",
		// "Virtual Machines" rather than a section of its own. This is now
		// the value GetDocs renders — the matching endpointMeta entry is
		// the shadow copy — so the section a reader sees is decided here.
		Group:       "Virtual Machines",
		Permissions: clusterCheck("view", "cluster"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListResourcePools,
	})
}

// upidParam is a Proxmox task id as it arrives in a URL.
//
// It carries no pattern on purpose: the frontend percent-encodes the
// UPID's colons, Fiber does not decode path parameters, and a pattern
// written against the decoded form would reject every real request while
// one written against the encoded form would depend on which client did
// the encoding. On the two /clusters/:cluster_id/tasks/:upid routes the
// handler unescapes it and requires extractNodeFromUPID to find a node in
// it, and the traversal guard is at the client: the decoded UPID goes
// through proxmox.validateTaskUPID (client_tasks.go) and its node through
// validateNodeName (client.go), each refusing a "/", a bare "." or "..",
// and control characters — a UPID that yields a node name can still carry
// a "../" segment, and that pair is what stops it. The third route that
// takes it, PUT /api/v1/tasks/:upid, only looks the value up in the
// database and never builds a Proxmox path from it.
var upidParam = apischema.Property{
	Type:        apischema.String,
	MinLength:   apischema.Ptr(1),
	MaxLength:   apischema.Ptr(512),
	Typetext:    "<UPID>",
	Description: "Proxmox task id (UPID), percent-encoded.",
}

// diskKeyParam is a guest config key naming a volume — "scsi0", "rootfs",
// "mp0".
func diskKeyParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Pattern:     `^[a-z]+[0-9]*$`,
		MaxLength:   apischema.Ptr(32),
		Typetext:    "<config key>",
		Description: description,
	}
}

func requiredStorage(description string) apischema.Property {
	p := apischema.StdOption("storage-id")
	p.Description = description
	return p
}

func requiredNode(description string) apischema.Property {
	p := apischema.StdOption("node-name")
	p.Description = description
	return p
}

// optString is a plain optional string parameter with a length cap.
//
// Most of the VM-create body is this shape, and deliberately so: the
// values below are Proxmox's own configuration vocabulary — machine
// types, SCSI controller models, VGA kinds, cloud-init fields — which
// Proxmox owns, versions and rejects with a message of its own. Copying
// those vocabularies into an Enum here would date on the next PVE release
// and would reject a value the cluster in front of the operator actually
// accepts. What the schema IS doing for this body is closing the
// parameter SET: an unknown key now comes back as "unknown parameter"
// rather than being silently dropped, which is how a typo'd field used to
// create a VM that quietly ignored half the request.
func optString(maxLen int, typetext, description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Optional:    true,
		MaxLength:   apischema.Ptr(maxLen),
		Typetext:    typetext,
		Description: description,
	}
}

// optCount is an optional non-negative integer.
func optCount(maxValue float64, description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.Integer,
		Optional:    true,
		Minimum:     apischema.Ptr(0.0),
		Maximum:     apischema.Ptr(maxValue),
		Typetext:    "<integer>",
		Description: description,
	}
}

// optTristateBool is an optional boolean with NO default, so that
// p.OptBool reports whether the caller chose at all.
//
// The three parameters that use it are the three the handler passes to
// Proxmox as *bool: leaving one out means "do not send this key", which
// is different from sending it as false. A Default here would collapse
// those two and start writing numa=0 onto every VM whose creator never
// mentioned NUMA.
func optTristateBool(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.Boolean,
		Optional:    true,
		Typetext:    "<boolean>",
		Description: description,
	}
}

// createVMParams is the body of POST /clusters/:cluster_id/vms.
func createVMParams() apischema.Properties {
	return apischema.Properties{
		"vmid": {
			Type:        apischema.Integer,
			Minimum:     apischema.Ptr(1.0),
			Maximum:     apischema.Ptr(999999999.0),
			Typetext:    "<integer>",
			Description: "VMID for the new VM.",
		},
		"node": requiredNode("Node to create the VM on."),
		"name": optString(255, "<string>", "VM name, as Proxmox records it."),

		// Hardware.
		"memory":  optCount(4194304, "RAM in MiB."),
		"cores":   optCount(1024, "Cores per socket."),
		"sockets": optCount(16, "CPU sockets."),
		"cpu":     optString(256, "<cputype>", "CPU model, e.g. x86-64-v2-AES or host."),
		"numa":    optTristateBool("Expose a NUMA topology to the guest."),
		"balloon": optCount(4194304, "Minimum RAM in MiB when ballooning; 0 disables the balloon device."),

		// Devices written as raw Proxmox device strings.
		"scsi0":     optString(512, "<volume>", "First SCSI disk, as a Proxmox device string."),
		"ide2":      optString(512, "<volume>", "IDE2 device, conventionally the installer CD-ROM."),
		"net0":      optString(512, "<model>=<mac>,bridge=<bridge>", "First network device."),
		"efidisk0":  optString(512, "<volume>", "EFI vars disk. Required alongside bios=ovmf."),
		"tpmstate0": optString(512, "<volume>", "TPM state volume."),
		"cdrom":     optString(512, "<volume>", "CD-ROM device string."),

		// System and boot.
		"ostype":  optString(64, "<ostype>", "Guest OS type, which sets Proxmox's device defaults."),
		"bios":    optString(64, "<seabios|ovmf>", "Firmware."),
		"machine": optString(128, "<type>", "QEMU machine type."),
		"scsihw":  optString(128, "<model>", "SCSI controller model."),
		"agent":   optString(128, "<agent spec>", "QEMU guest agent setting, e.g. \"1\" or \"enabled=1,fstrim_cloned_disks=1\"."),
		"boot":    optString(512, "<order=dev;dev>", "Boot order."),
		"vga":     optString(128, "<type>", "Display adapter."),
		"hotplug": optString(256, "<features>", "Hot-pluggable device classes, e.g. \"network,disk,usb\"."),
		"onboot":  optTristateBool("Start the VM when its node boots."),
		"tablet":  optTristateBool("Attach a USB tablet pointer."),
		"start":   optFlag("Start the VM once it is created."),

		// Cloud-init.
		"ciuser":       optString(255, "<user>", "Cloud-init user to create."),
		"cipassword":   optString(1024, "<password>", "Cloud-init password. Proxmox stores it hashed in the guest's config."),
		"sshkeys":      optString(32768, "<keys>", "Cloud-init SSH public keys, URL-encoded as Proxmox expects."),
		"ipconfig0":    optString(512, "<ipconfig>", "Cloud-init network config for net0."),
		"nameserver":   optString(512, "<addresses>", "Cloud-init DNS servers."),
		"searchdomain": optString(512, "<domains>", "Cloud-init DNS search domains."),

		// Metadata.
		"description": optString(8192, "<string>", "Free-text note stored on the VM."),
		"tags":        optString(1024, "<tags>", "Semicolon-separated Proxmox tags."),
		"pool":        optPoolID("Resource pool to place the VM in."),

		"extra": {
			Type:     apischema.Object,
			Optional: true,
			Typetext: "<object>",
			Description: "Additional Proxmox config keys as a flat object of string values — " +
				"further disks (scsi1, sata0, virtio0), CD-ROM slots and anything else this schema does not name.",
		},
	}
}
