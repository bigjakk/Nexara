package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterCheck,
// globalCheck, optFlag and optString — lives in registry_vms.go, where the
// first migrated domain defined it.

// clustersScope is the collection, and clusterByID is the ONE route shape in
// this API where the path parameter :id is a cluster id.
//
// namesACluster (permissions.go) accepts it as a special case, anchored as a
// whole segment, precisely because these five routes exist; every other
// cluster-scoped route spells it :cluster_id. Renaming it here would be an API
// break for every caller that has a cluster URL saved.
const (
	clustersScope = pathPrefix + "clusters"
	clusterByID   = clustersScope + "/:id"
)

// clusterIDPathParam is :id on the five per-cluster routes.
var clusterIDPathParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Source:      apischema.SourcePath,
	Typetext:    "<uuid>",
	Description: "Nexara cluster identifier.",
}

// clusterAPIURLParam is the Proxmox API base URL, on the create and the edit.
//
// It carries no url FORMAT, and that is deliberate rather than an omission:
// validateURLFormat and enforceURLAddressPolicy own this value, and they do
// more than a format could — scheme, embedded credentials, and an SSRF address
// policy that resolves the host and answers with a structured 422 the dialog
// turns into a "this is a private address, confirm?" prompt. A format here
// would reject some of those inputs with a flat 400 and lose the prompt.
func clusterAPIURLParam(optional bool, description string) apischema.Property {
	p := apischema.Property{
		Type:        apischema.String,
		Optional:    optional,
		MaxLength:   apischema.Ptr(2048),
		Typetext:    "<url>",
		Description: description,
	}
	if !optional {
		p.MinLength = apischema.Ptr(1)
	}
	return p
}

// clusterTLSFingerprintParam is the pinned certificate fingerprint.
//
// No format, for the reason registry_pbs.go gives for the same field: the add
// dialog sends it EMPTY when the operator skipped the fetch step, and every
// registered format rejects the empty string (apischema treats "" as a value
// the caller supplied, not as an absent one).
var clusterTLSFingerprintParam = optString(128, "<SHA256 fingerprint>",
	"Expected SHA-256 certificate fingerprint. Empty or omitted trusts the system roots; the add "+
		"dialog sends it empty when the fetch step was skipped.")

// clusterBootstrapParam is the optional onboarding block.
//
// Declared as an opaque Object because apischema has no nested-object schema,
// and left that way on purpose: bootstrapRequest.validate owns its rules,
// including the one that matters — a user_id in the @pam realm is refused,
// because that realm maps to real shell accounts on every node and minting a
// cluster-wide Administrator token against one is not something a schema should
// be the only thing standing in front of.
//
// It carries a PASSWORD. Nothing in the create path puts it in an audit row:
// the success row records the token id and the object names, and
// auditBootstrapFailure records the username, the ids and the reason — never
// Params.Raw(), which would publish the operator's Proxmox password to every
// Viewer, since view:audit is a default Viewer grant.
var clusterBootstrapParam = apischema.Property{
	Type:     apischema.Object,
	Optional: true,
	Typetext: "<object>",
	Description: "Ask Nexara to mint the cluster's credential itself instead of being handed one: " +
		"{username, password, otp?, user_id?, token_name?}. Mutually exclusive with token_id and " +
		"token_secret. The password is used once, for the login that mints the token, and is never stored " +
		"or recorded.",
}

// registerClusterEndpoints declares ClusterHandler's seven routes.
//
// Five of them are the ordinary shape — a cluster-scoped Check resolved from
// :id, or a global one — and two are not:
//
//   - GET /api/v1/clusters is Advisory. It lists every cluster and drops the
//     ones the caller cannot see, so there is no single cluster a gate could
//     resolve and a global view:cluster Check would refuse exactly the
//     cluster-scoped operators the filtering exists to serve.
//   - POST /api/v1/clusters/fetch-fingerprint is Alternatives over THREE global
//     manage permissions. That width is TRANSCRIBED, not introduced here: the
//     handler already called requireAnyGlobalManage(c, "cluster", "pbs",
//     "veeam"). It is load-bearing because every add-flow that stores a
//     credential against a remote server starts here, so gating it on
//     manage:cluster alone made a backup-only role unusable — refused at step 1
//     of a flow it was granted. It returns a certificate, not a secret.
//
// THREE rate limiters are passed IN rather than built here, and the fact that
// there are three rather than two is a transcription rather than a choice.
//
// fingerprintFetchLimiter() returns a NEW limiter on every call (middleware.go),
// and the legacy block called it TWICE — once for fetch-fingerprint, once for
// verify-certificate — so despite sharing a key string those two routes each had
// their own 30/min store. Passing one instance to both would halve the budget
// for a caller who uses both, which is a live rate-limit change and not this
// migration's to make; if the two should share a bucket, that is a separate
// decision. registerVeeamEndpoints shares ONE instance across its group for the
// opposite reason: its legacy block built one.
//
// All three mount between authentication and the permission check, exactly where
// the legacy block put them — so an unauthorized flood spends the caller's own
// bucket rather than collecting free 403s.
//
// What stays in the handler is everything the declaration cannot see: the
// token-or-bootstrap exclusivity, the credential-redirect refusal, the SSH
// trust reset and its confirmation gate, the rolling-update conflict checks,
// and the two-source corroboration behind verify-certificate.
func registerClusterEndpoints(reg *Registry, h *handlers.ClusterHandler,
	createLimiter, fetchFingerprintLimiter, verifyCertificateLimiter fiber.Handler,
) {
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clustersScope,
		Description: "Add a Proxmox cluster, with a pasted API token or a bootstrap block Nexara mints one " +
			"from. The credential is resolved BEFORE the row is written, so a failed mint leaves no " +
			"half-configured cluster, and connectivity is tested before the response.",
		Group:       "Clusters",
		Permissions: globalCheck("manage", "cluster"),
		RateLimiter: createLimiter,
		Parameters: apischema.Properties{
			"name": {
				Type:        apischema.String,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(255),
				Typetext:    "<string>",
				Description: "Display name for the cluster.",
			},
			"api_url": clusterAPIURLParam(false,
				"Base URL of the Proxmox API, e.g. https://pve-01.example.com:8006. Must be https and "+
					"must not carry credentials; a private or loopback address needs allow_private_address."),
			// Both optional, because the alternative to supplying them is the
			// bootstrap block — a cross-field rule the handler owns, and which
			// it answers with a message naming BOTH ways in.
			"token_id":     optString(255, "<user@realm!tokenid>", "Proxmox API token id. Required unless a bootstrap block is sent."),
			"token_secret": optString(1024, "<secret>", "Proxmox API token secret. Write-only: never returned, and never recorded in an audit row."),

			"tls_fingerprint": clusterTLSFingerprintParam,
			"sync_interval_seconds": {
				Type:        apischema.Integer,
				Optional:    true,
				Default:     30,
				Minimum:     apischema.Ptr(10.0),
				Maximum:     apischema.Ptr(86400.0),
				Typetext:    "<integer>",
				Description: "How often the collector polls this cluster, in seconds.",
			},
			"allow_private_address": optFlag(
				"Confirm an api_url that resolves to a private, loopback or link-local address, which a " +
					"homelab cluster always does. Cloud-metadata, unspecified and multicast addresses are " +
					"refused regardless."),
			"bootstrap": clusterBootstrapParam,
		},
		Handler: h.Create,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clustersScope + "/fetch-fingerprint",
		Description: "Open a TLS connection to a host and return its leaf certificate's SHA-256 " +
			"fingerprint, plus whether the chain is trusted by the system roots. Step 1 of the cluster, " +
			"PBS and Veeam add-flows, so manage on ANY of those three opens it; it returns a certificate, " +
			"never a secret.",
		Group: "Clusters",
		Permissions: Permissions{Alternatives: []Check{
			{Action: "manage", Resource: "cluster", Scope: ScopeGlobal},
			{Action: "manage", Resource: "pbs", Scope: ScopeGlobal},
			{Action: "manage", Resource: "veeam", Scope: ScopeGlobal},
		}},
		RateLimiter: fetchFingerprintLimiter,
		Parameters: apischema.Properties{
			"api_url": clusterAPIURLParam(false, "Base URL of the host to fetch the certificate from."),
			"allow_private_address": optFlag(
				"Confirm an api_url that resolves to a private, loopback or link-local address."),
		},
		Handler: h.FetchFingerprint,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   clustersScope,
		Description: "List the clusters the caller can see, each with its computed status, PVE version and " +
			"current infrastructure-health issues, so the shell can surface them app-wide without a live " +
			"call per cluster.",
		Group: "Clusters",
		Permissions: Permissions{Advisory: &AdvisoryCheck{
			Check: Check{Action: "view", Resource: "cluster", Scope: ScopeCluster},
			Reason: "accessibleClusters(\"view\", \"cluster\") builds the scope and access.PermitsCluster " +
				"drops every row outside it; the listing spans every cluster, so there is none for a gate " +
				"to resolve",
		}},
		Parameters: apischema.Properties{},
		Handler:    h.List,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterByID,
		Description: "Get one cluster, with its computed status and current infrastructure-health issues.",
		Group:       "Clusters",
		Permissions: clusterCheck("view", "cluster"),
		Parameters:  apischema.Properties{"id": clusterIDPathParam},
		Handler:     h.Get,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   clusterByID,
		Description: "Update a cluster. Every field is optional and omitting one leaves it alone. Moving " +
			"api_url refuses to re-point the STORED token at a new address unless the secret is re-supplied, " +
			"and clears the cluster's SSH credential and every pinned host key — which needs " +
			"acknowledge_ssh_trust_reset, and is refused outright while a rolling update is running or its " +
			"cleanup is still pending.",
		Group:       "Clusters",
		Permissions: clusterCheck("manage", "cluster"),
		Parameters: apischema.Properties{
			"id": clusterIDPathParam,
			// Every field below is OPTIONAL WITH NO DEFAULT, because the
			// handler reads each as a pointer and merges only what was sent. A
			// Default would make every save look like an explicit choice and
			// overwrite fields the editor never touched.
			"name":    optString(255, "<string>", "New display name."),
			"api_url": clusterAPIURLParam(true, "New base URL for the Proxmox API. See the endpoint description for what moving it costs."),
			// NOT MinLength-bounded, on purpose: the handler answers an explicit
			// "" with a message telling the caller to omit the field instead,
			// and a bare "must have at least 1 character" would lose that.
			"token_id":     optString(255, "<user@realm!tokenid>", "New Proxmox API token id."),
			"token_secret": optString(1024, "<secret>", "New Proxmox API token secret. Omit it to keep the stored one — an explicit empty value is refused, because encrypting it would silently break the cluster."),

			"tls_fingerprint": clusterTLSFingerprintParam,
			"sync_interval_seconds": {
				Type:        apischema.Integer,
				Optional:    true,
				Minimum:     apischema.Ptr(10.0),
				Maximum:     apischema.Ptr(86400.0),
				Typetext:    "<integer>",
				Description: "New collector poll interval in seconds. Omitted, the stored one is kept.",
			},
			"is_active": {
				Type:     apischema.Boolean,
				Optional: true,
				Typetext: "<boolean>",
				// No Default: false means "pause this cluster" and absent means
				// "leave it as it is", and collapsing the two would pause every
				// cluster whose editor only changed its name.
				Description: "Whether the collector polls this cluster. Omitted, the stored value is kept.",
			},
			"allow_private_address": optFlag(
				"Confirm a new api_url that resolves to a private, loopback or link-local address."),
			"acknowledge_ssh_trust_reset": optFlag(
				"Confirm that moving api_url may clear this cluster's SSH credential — which cannot be " +
					"recovered — and every pinned host key."),
		},
		Handler: h.Update,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   clusterByID + "/verify-certificate",
		Description: "Re-pin the cluster to the certificate its configured endpoint serves now, after " +
			"corroborating it against what the cluster itself reports for that node. The two must agree: a " +
			"mismatch is the signature of an interception and is refused rather than resolved.",
		Group:       "Clusters",
		Permissions: clusterCheck("manage", "cluster"),
		RateLimiter: verifyCertificateLimiter,
		Parameters:  apischema.Properties{"id": clusterIDPathParam},
		Handler:     h.VerifyCertificate,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   clusterByID,
		Description: "Remove a cluster from Nexara, optionally revoking the Proxmox-side credential Nexara " +
			"minted for it. Revoking additionally requires GLOBAL manage:cluster — minting it needed that, " +
			"so removing it asks for the same — and is refused while a rolling update is running.",
		Group:       "Clusters",
		Permissions: clusterCheck("delete", "cluster"),
		Parameters: apischema.Properties{
			"id": clusterIDPathParam,
			// A STRING rather than a boolean, and that is a deliberate
			// non-change: wantsCredentialRevocation reads exactly "1", "true"
			// and "yes" as opt-in and everything else as no. Declaring it a
			// boolean would ALSO accept "on" — apischema's toBool does — and
			// the handler would then read that as no while the schema had
			// accepted it as yes. On an endpoint that deletes users and tokens
			// from a live hypervisor, a declaration that disagrees with the
			// code about what "on" means is not a tidy-up worth making here.
			"revoke_pve_credentials": optString(16, "<1|true|yes>",
				"Send 1, true or yes to also delete the Proxmox user, token and ACL Nexara created for "+
					"this cluster. Any other value, or omitting it, leaves them alone."),
		},
		Handler: h.Delete,
	})
}
