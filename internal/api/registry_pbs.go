package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary — clusterScope, clusterCheck,
// clusterParams, withParams, optFlag and optString — lives in
// registry_vms.go, where the first migrated domain defined it.

// pbsScope is the global collection every PBS server hangs off. The
// per-cluster listing is the one route in this domain whose subject is a
// cluster rather than a server.
const pbsScope = pathPrefix + "pbs-servers"

// emptyOrUUID is the canonical UUID form, or nothing.
//
// It arrived for ONE parameter — the cluster a PBS server is attached to,
// on the CREATE body — where the empty string has always meant "this
// server is standalone". apischema treats "" as a value the caller
// supplied and the uuid format rejects it, so the create rule has to be a
// pattern. See pbsAttachedClusterParam.
//
// FIVE other files now reach for it — alerts, ldap, oidc, reports and
// rolling_update — each for its own "" sentinel, which is why the rule
// itself lives in the catalogue and is DERIVED there from the uuid format
// rather than transcribed beside it.
var emptyOrUUID = apischema.Rule("uuid-or-empty")

// pbsServerIDParam is a PBS server's Nexara row id as a PATH parameter.
//
// It is spelled :id because that is what the three routes already
// register. The name is one of the two clusterIDFromParam reads (see
// gateParamNames in registry.go), which is safe only because it resolves
// to the PATH — checkPathParams refuses the same name declared as a body
// or query parameter, and namesACluster refuses a cluster-scoped Check on
// a path whose first placeholder is not :cluster_id, so the fallback
// cannot turn a PBS server id into a cluster the gate would authorize.
var pbsServerIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Typetext:    "<uuid>",
	Description: "Nexara PBS server identifier.",
}

// pbsAttachedClusterParam is the cluster a PBS server belongs to, in the
// create and update BODIES.
//
// It is declared as attached_cluster_id with "cluster_id" as an ALIAS, and
// that is not cosmetic. checkPathParams refuses a parameter NAMED
// cluster_id that resolves to anything but the path, because
// clusterIDFromParam reads that name to decide which cluster the
// permission gate authorizes — so a body parameter of that name is a name
// the gate also reads. On these two paths no gate runs at all (both are
// Deferred), but the refusal is deliberately about the NAME rather than
// about whether today's path happens to make it safe, and routing around a
// fail-closed guard is how the hole it protects gets reopened by the next
// path edit. The alias is what keeps every existing caller working: both
// dialogs send {"cluster_id": …}, and Alias exists for exactly this — a
// second spelling of one parameter, read from the same source.
//
// The two routes differ in ONE way, and it is pre-existing rather than
// introduced here: on CREATE an empty value means "standalone" (the
// handler branches on `*req.ClusterID != ""` and gates globally), while on
// UPDATE an empty value has always been refused with "Invalid cluster_id
// format". The declarations state each route's actual rule rather than
// papering over the difference — see pbsAttachedClusterUpdateParam.
func pbsAttachedClusterParam() apischema.Property {
	return apischema.Property{
		Type:      apischema.String,
		Alias:     "cluster_id",
		Optional:  true,
		Pattern:   emptyOrUUID,
		MaxLength: apischema.Ptr(36),
		Typetext:  "<uuid>",
		Description: "Cluster this PBS server belongs to, which is what scopes its permission and its " +
			"backup coverage. Empty or omitted makes it standalone, which requires the instance-wide " +
			"grant instead. Also accepted as \"cluster_id\", which is what Nexara's own dialogs send.",
	}
}

// pbsAttachedClusterUpdateParam is pbsAttachedClusterParam for the update
// body, where the empty string is NOT accepted.
//
// UpdatePBSServer has always answered 400 for one: it parses the value
// with uuid.Parse and refuses anything that does not. Declaring the uuid
// format states that rule one layer earlier and names the field, instead
// of the handler's generic "Invalid cluster_id format".
//
// That also means there is no way to DETACH a PBS server from its cluster
// through this endpoint, and the edit dialog's "None" option sends exactly
// the empty string this refuses. That is a pre-existing bug, not one this
// declaration introduces, and fixing it is a behaviour change rather than
// a migration — it is flagged for separate scoping rather than folded in
// here.
func pbsAttachedClusterUpdateParam() apischema.Property {
	p := pbsAttachedClusterParam()
	p.Pattern = ""
	p.Format = "uuid"
	p.Description = "Move this PBS server to another cluster, which additionally requires manage:pbs on " +
		"the target. Omitted leaves it where it is. An empty value is refused: this endpoint cannot " +
		"detach a server from its cluster. Also accepted as \"cluster_id\"."
	return p
}

// pbsDeferredReason is the Deferred justification the four per-server
// routes share.
//
// requirePBSPerm (handlers/backup.go) makes the same decision for the 19
// backup routes nested under /pbs-servers/:pbs_id, and PBSHandler makes it
// inline for these four: load the server, then gate CLUSTER-SCOPED when
// server.ClusterID is valid and GLOBALLY when it is not. A permission
// decided by a DB lookup cannot be hoisted into middleware, which runs
// before any query.
const pbsDeferredReason = "the scope depends on a DB lookup: the handler loads the PBS server row and " +
	"gates on the cluster it is attached to when there is one, and on the instance-wide grant when the " +
	"server is standalone — the same split requirePBSPerm makes for the nested backup routes"

// registerPBSEndpoints declares the 6 PBS server routes served by
// PBSHandler.
//
// This is the domain where almost nothing hoists. Exactly ONE of the six
// is a plain Check — the per-cluster listing, whose subject is the cluster
// in its own path. The global listing is Advisory, and the other four are
// Deferred because their permission is decided by a DB lookup (see
// pbsDeferredReason).
//
// These are NOT the 19 backup routes under /pbs-servers/:pbs_id, which
// reach the same decision through BackupHandler.requirePBSPerm and are
// declared — Deferred — by registerBackupEndpoints (registry_backup.go).
// Only the PBSHandler routes are declared here.
func registerPBSEndpoints(reg *Registry, h *handlers.PBSHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsScope,
		Description: "List the PBS servers the caller can see. Filtered rather than gated: a " +
			"cluster-bound server needs view:pbs on its cluster, and a standalone one needs it " +
			"instance-wide.",
		Group: "Backup",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check: Check{Action: "view", Resource: "pbs", Scope: ScopeCluster},
			Reason: "accessibleClusters(\"view\", \"pbs\") builds the filter and the handler applies it " +
				"per row — PermitsCluster for a cluster-bound server, HasGlobal for a standalone one; " +
				"there is no single cluster a gate could resolve",
		}},
		Parameters: nil,
		Handler:    h.List,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   pbsScope,
		// Deferred renders as the bare word "deferred", so the permission an
		// operator building a role needs is spelled out here.
		Description: "Add a PBS server. Requires manage:pbs on the cluster named in the body, or the " +
			"instance-wide manage:pbs when the server is standalone.",
		Permissions: Permissions{Deferred: pbsCreateReason},
		Group:       "Backup",
		Parameters:  createPBSParams(),
		Handler:     h.Create,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   pbsScope + "/:id",
		Description: "Get one PBS server's connection details. The token secret is never returned. " +
			"Requires view:pbs on the server's cluster, or the instance-wide view:pbs when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: pbsDeferredReason},
		Parameters:  apischema.Properties{"id": pbsServerIDParam},
		Handler:     h.Get,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   pbsScope + "/:id",
		Description: "Change a PBS server. Every parameter is optional; the ones omitted are left as they " +
			"are, and re-pointing the API URL without re-entering the token secret is refused outright. " +
			"Requires manage:pbs on the server's cluster, or the instance-wide manage:pbs when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: pbsDeferredReason},
		Parameters:  updatePBSParams(),
		Handler:     h.Update,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   pbsScope + "/:id",
		Description: "Remove a PBS server. Requires delete:pbs on the server's cluster, or the " +
			"instance-wide delete:pbs when it is standalone.",
		Group:       "Backup",
		Permissions: Permissions{Deferred: pbsDeferredReason},
		Parameters:  apischema.Properties{"id": pbsServerIDParam},
		Handler:     h.Delete,
	})

	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/pbs-servers",
		Description: "List the PBS servers attached to this cluster.",
		Group:       "Backup",
		Permissions: clusterCheck("view", "pbs"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListByCluster,
	})
}

// pbsCreateReason is the Deferred justification for the create route,
// which reaches the same split from the BODY rather than from a row.
const pbsCreateReason = "the scope depends on the body: the handler gates cluster-scoped on the " +
	"cluster_id it carries, and on the instance-wide manage:pbs when the server is standalone — " +
	"neither of which middleware can read"

// createPBSParams is the body of POST /api/v1/pbs-servers.
//
// Four parameters are required, matching exactly what the handler refused
// an empty value for: name, api_url, token_id and token_secret. The URL
// rules (https, a host, no credentials) stay in validateURLFormat and
// enforceURLAddressPolicy, which own them and answer with messages of
// their own — and the address policy is a DNS-resolving SSRF check that no
// parameter schema could make.
func createPBSParams() apischema.Properties {
	return apischema.Properties{
		"name": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(255),
			Typetext:    "<string>",
			Description: "Display name for the server.",
		},
		"api_url": {
			Type:      apischema.String,
			MinLength: apischema.Ptr(1),
			MaxLength: apischema.Ptr(2048),
			Typetext:  "<url>",
			Description: "Base URL of the PBS API, e.g. https://backup.example.com:8007. Must be https " +
				"and must not carry credentials.",
		},
		"token_id": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(255),
			Typetext:    "<user@realm!tokenid>",
			Description: "PBS API token id.",
		},
		// Declaring the secret at all is what makes checkMisplaced refuse it
		// as a QUERY parameter, so a caller cannot move a credential into a
		// URL that proxies and access logs record. It is never read back —
		// the response type carries no secret field — and must not reach an
		// audit row, which this project makes readable by every Viewer.
		"token_secret": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(1024),
			Typetext:    "<secret>",
			Description: "PBS API token secret. Stored encrypted and never returned.",
		},
		"tls_fingerprint": optString(128, "<SHA256 fingerprint>",
			"Expected SHA-256 certificate fingerprint, for a self-signed PBS. Empty or omitted trusts "+
				"the system roots. No format: the add dialog sends it empty when the fetch step was skipped."),
		"attached_cluster_id": pbsAttachedClusterParam(),
		"allow_private_address": optFlag(
			"Confirm an api_url that resolves to a private or loopback address, which a PBS on the same " +
				"network always does."),
	}
}

// updatePBSParams is the body of PUT /api/v1/pbs-servers/:id.
//
// Every parameter is optional and read as a POINTER, so that omitting one
// leaves the stored value alone. token_secret is the exception worth
// naming: an explicit empty string is refused by the handler with a
// message that says to omit the field instead, because encrypting "" would
// swap a working credential for the ciphertext of an empty string and
// nothing downstream would notice.
func updatePBSParams() apischema.Properties {
	return apischema.Properties{
		"id": pbsServerIDParam,
		"name": {
			Type:        apischema.String,
			Optional:    true,
			MaxLength:   apischema.Ptr(255),
			Typetext:    "<string>",
			Description: "New display name.",
		},
		"api_url": {
			Type:        apischema.String,
			Optional:    true,
			MaxLength:   apischema.Ptr(2048),
			Typetext:    "<url>",
			Description: "New base URL. Changing it without also sending token_secret is refused.",
		},
		"token_id": optString(255, "<user@realm!tokenid>", "New PBS API token id."),
		// NOT MinLength-bounded, on purpose: the handler answers an explicit
		// "" with a message that tells the caller to omit the field instead,
		// and a bare "must have at least 1 character" would lose that.
		"token_secret": optString(1024, "<secret>",
			"New PBS API token secret. Omit the field to keep the stored one; an empty value is refused."),
		"tls_fingerprint": optString(128, "<SHA256 fingerprint>",
			"New expected SHA-256 certificate fingerprint. An empty value clears the pin."),
		"attached_cluster_id": pbsAttachedClusterUpdateParam(),
		"allow_private_address": optFlag(
			"Confirm an api_url that resolves to a private or loopback address."),
	}
}
