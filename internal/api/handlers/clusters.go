package handlers

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/netguard"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// All seven routes are declared in internal/api/registry_clusters.go, which
// states their permission, their parameters and their rate limiters; nothing
// below re-checks any of the three.
//
// What stays here is what a declaration cannot see: the token-or-bootstrap
// exclusivity, the URL address policy and its structured confirmation, the
// credential-redirect refusal, the SSH trust reset, the rolling-update conflict
// checks, and the two-source corroboration behind verify-certificate.

// ClusterHandler handles cluster CRUD endpoints.
type ClusterHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewClusterHandler creates a new cluster handler.
func NewClusterHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *ClusterHandler {
	return &ClusterHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
	}
}

// createClusterRequest and updateClusterRequest are the validated bodies, read
// out of the declared parameters rather than bound from JSON.
//
// The POINTERS on the update are the whole point of the type: every field is
// optional and omitting one must leave the stored value alone, which a zero
// value cannot say. On the create, SyncIntervalSeconds carries the declared
// default instead, because there is no stored value to preserve.
type createClusterRequest struct {
	Name                string
	APIURL              string
	TokenID             string
	TokenSecret         string
	TLSFingerprint      string
	SyncIntervalSeconds int32
	// AllowPrivateAddress, when true, lets the URL resolve to a private/
	// loopback/link-local IP (typical homelab setup). Cloud metadata,
	// unspecified, and multicast addresses are still rejected.
	AllowPrivateAddress bool
	// Bootstrap asks Nexara to mint the cluster's credential itself instead of
	// being handed one. Mutually exclusive with token_id/token_secret.
	Bootstrap *bootstrapRequest
}

type updateClusterRequest struct {
	Name                *string
	APIURL              *string
	TokenID             *string
	TokenSecret         *string
	TLSFingerprint      *string
	SyncIntervalSeconds *int32
	IsActive            *bool
	AllowPrivateAddress bool
	// AcknowledgeSSHTrustReset confirms that moving api_url may clear this
	// cluster's SSH credential and every pinned host key.
	AcknowledgeSSHTrustReset bool
}

// bootstrapFromParams rebuilds the optional onboarding block out of the opaque
// object the schema validated.
//
// The re-marshal is what c.Bind().Body did before, restricted to the one key
// that carries a nested object: apischema has no nested-object schema, so the
// block's own rules stay with bootstrapRequest.validate. An explicit null reads
// as absent, which is how the handler already read a nil *bootstrapRequest.
func bootstrapFromParams(p *apischema.Params) (*bootstrapRequest, error) {
	if !p.Has("bootstrap") {
		return nil, nil
	}
	raw, err := json.Marshal(p.Object("bootstrap"))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "Invalid bootstrap block")
	}
	var req bootstrapRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "Invalid bootstrap block")
	}
	return &req, nil
}

// createClusterRequestFromParams reads the create body out of the validated
// parameters.
func createClusterRequestFromParams(p *apischema.Params) (createClusterRequest, error) {
	boot, err := bootstrapFromParams(p)
	if err != nil {
		return createClusterRequest{}, err
	}
	return createClusterRequest{
		Name:                p.String("name"),
		APIURL:              p.String("api_url"),
		TokenID:             p.String("token_id"),
		TokenSecret:         p.String("token_secret"),
		TLSFingerprint:      p.String("tls_fingerprint"),
		SyncIntervalSeconds: safeconv.Int32(int(p.Int("sync_interval_seconds"))),
		AllowPrivateAddress: p.Bool("allow_private_address"),
		Bootstrap:           boot,
	}, nil
}

// updateClusterRequestFromParams reads the edit body out of the validated
// parameters, keeping "the caller did not mention this field" distinct from
// "the caller sent the zero value" for every one of them.
func updateClusterRequestFromParams(p *apischema.Params) updateClusterRequest {
	req := updateClusterRequest{
		Name:                     optStringPtr(p.OptString("name")),
		APIURL:                   optStringPtr(p.OptString("api_url")),
		TokenID:                  optStringPtr(p.OptString("token_id")),
		TokenSecret:              optStringPtr(p.OptString("token_secret")),
		TLSFingerprint:           optStringPtr(p.OptString("tls_fingerprint")),
		IsActive:                 optBoolPtr(p.OptBool("is_active")),
		AllowPrivateAddress:      p.Bool("allow_private_address"),
		AcknowledgeSSHTrustReset: p.Bool("acknowledge_ssh_trust_reset"),
	}
	if v, supplied := p.OptInt("sync_interval_seconds"); supplied {
		n := safeconv.Int32(int(v))
		req.SyncIntervalSeconds = &n
	}
	return req
}

type clusterResponse struct {
	ID                  uuid.UUID `json:"id"`
	Name                string    `json:"name"`
	APIURL              string    `json:"api_url"`
	TokenID             string    `json:"token_id"`
	TLSFingerprint      string    `json:"tls_fingerprint"`
	SyncIntervalSeconds int32     `json:"sync_interval_seconds"`
	IsActive            bool      `json:"is_active"`
	Status              string    `json:"status"`
	PVEVersion          string    `json:"pve_version"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	// CredentialSource is "manual" or "bootstrap". The UI reads it to decide
	// whether deleting the cluster can offer to revoke the Proxmox-side
	// credential — an offer that is only ever valid for one Nexara created.
	CredentialSource string `json:"credential_source"`
	// Issues is the cluster's current infrastructure-health problems (Ceph, HA,
	// disks, storage, failed tasks, …), computed server-side so the UI can
	// surface them app-wide. Empty/omitted when the cluster is healthy.
	Issues []healthIssueResponse `json:"issues,omitempty"`
}

type connectivityResult struct {
	Reachable bool   `json:"reachable"`
	Message   string `json:"message"`
}

type createClusterResponse struct {
	Cluster      clusterResponse    `json:"cluster"`
	Connectivity connectivityResult `json:"connectivity"`
	// Bootstrap reports what onboarding created on the Proxmox side. Present
	// only when the cluster was onboarded with a bootstrap block; it carries
	// object names, never the minted secret.
	Bootstrap *bootstrapSummary `json:"bootstrap,omitempty"`
}

type updateClusterResponse struct {
	Cluster      clusterResponse    `json:"cluster"`
	Connectivity connectivityResult `json:"connectivity"`
}

// nodeStatusInfo holds per-cluster node counts for computing cluster status.
type nodeStatusInfo struct {
	Total  int64
	Online int64
}

func computeClusterStatus(c db.Cluster, nsi nodeStatusInfo) string {
	if !c.IsActive {
		return "inactive"
	}
	if nsi.Total == 0 {
		return "unknown"
	}
	if nsi.Online == 0 {
		return "offline"
	}
	if nsi.Online < nsi.Total {
		return "degraded"
	}
	return "online"
}

func toClusterResponse(c db.Cluster, nsi nodeStatusInfo) clusterResponse {
	return clusterResponse{
		ID:                  c.ID,
		Name:                c.Name,
		APIURL:              c.ApiUrl,
		TokenID:             c.TokenID,
		TLSFingerprint:      c.TlsFingerprint,
		SyncIntervalSeconds: c.SyncIntervalSeconds,
		IsActive:            c.IsActive,
		Status:              computeClusterStatus(c, nsi),
		PVEVersion:          c.PveVersion,
		CreatedAt:           c.CreatedAt,
		UpdatedAt:           c.UpdatedAt,
		CredentialSource:    c.CredentialSource,
	}
}

// Create handles POST /api/v1/clusters.
func (h *ClusterHandler) Create(c fiber.Ctx, p *apischema.Params) error {
	req, err := createClusterRequestFromParams(p)
	if err != nil {
		return err
	}

	// Either the operator hands us a token, or they hand us a password and we
	// mint one. Accepting both would leave it ambiguous which credential the
	// cluster actually ends up authenticating with.
	switch {
	case req.Bootstrap != nil && (req.TokenID != "" || req.TokenSecret != ""):
		return fiber.NewError(fiber.StatusBadRequest,
			"Supply either token_id and token_secret, or a bootstrap block — not both")
	case req.Bootstrap != nil:
		if err := req.Bootstrap.validate(); err != nil {
			return err
		}
	case req.TokenID == "" || req.TokenSecret == "":
		// name and api_url are required by the declaration and answered by
		// field name before this runs, so this branch is only ever about the
		// credential — and it names the alternative, because supplying a
		// bootstrap block instead is the other way to satisfy it.
		return fiber.NewError(fiber.StatusBadRequest,
			"token_id and token_secret are required, unless a bootstrap block is sent instead")
	}

	if err := validateURLFormat(req.APIURL); err != nil {
		return err
	}
	if err := enforceURLAddressPolicy(c.Context(), req.APIURL, req.AllowPrivateAddress); err != nil {
		return renderAddressPolicyError(c, err)
	}

	// The declaration carries both the 10..86400 bound and the default of 30,
	// so this is the value the caller asked for or the one they inherited.
	syncInterval := req.SyncIntervalSeconds

	// Resolve the credential BEFORE writing anything. If the mint fails there
	// is no half-configured cluster row to explain or clean up, and the
	// operator sees the Proxmox-side reason instead of a cluster that exists
	// but cannot talk to anything.
	cred := &clusterCredential{
		TokenID: req.TokenID,
		Secret:  req.TokenSecret,
		Source:  credentialSourceManual,
	}
	var bootClient *proxmox.BootstrapClient
	if req.Bootstrap != nil {
		minted, client, mintErr := runClusterBootstrap(c.Context(), req.APIURL, req.TLSFingerprint, req.Bootstrap)
		if client != nil {
			// Held open past the mint so the insert below can revoke the token
			// it just created without a second login.
			defer client.Close()
		}
		if mintErr != nil {
			// Recorded before answering: a failed attempt can leave objects on
			// the hypervisor that no cluster row will ever account for.
			h.auditBootstrapFailure(c, req.APIURL, req.Bootstrap, mintErr)
			return renderBootstrapError(c, mintErr, req.Bootstrap)
		}
		cred, bootClient = minted, client
	}

	encrypted, err := crypto.Encrypt(cred.Secret, h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt token secret")
	}

	cluster, err := h.queries.CreateCluster(c.Context(), db.CreateClusterParams{
		Name:                 req.Name,
		ApiUrl:               req.APIURL,
		TokenID:              cred.TokenID,
		TokenSecretEncrypted: encrypted,
		TlsFingerprint:       req.TLSFingerprint,
		SyncIntervalSeconds:  syncInterval,
		IsActive:             true,
		CredentialSource:     cred.Source,
		BootstrapUserID:      cred.UserID,
		BootstrapTokenName:   cred.TokenName,
		BootstrapCreatedUser: cred.CreatedUser,
		BootstrapCreatedAcl:  cred.CreatedACL,
		BootstrapCreatedAt:   cred.mintedAtColumn(),
	})
	if err != nil {
		// A minted token with no cluster row to hold its secret is an orphaned
		// privsep=0 Administrator credential nobody has — the same artefact
		// post-mint verification exists to prevent, so it gets the same
		// treatment. RollbackMint removes only what THIS request created, so an
		// adopted pre-existing user or grant is left alone.
		if bootClient != nil {
			rb := bootClient.RollbackMint(c.Context(), proxmox.MintParams{
				UserID:    cred.UserID,
				TokenName: cred.TokenName,
			}, cred.CreatedUser, cred.CreatedACL)

			h.auditBootstrapFailure(c, req.APIURL, req.Bootstrap, &proxmox.MintError{
				Err:         errors.New("cluster row could not be written after the credential was minted"),
				UserID:      cred.UserID,
				CreatedUser: rb.RemainingUser,
				CreatedACL:  rb.RemainingACL,
				Steps:       rb.Steps,
			})

			if rb.RemainingUser || rb.RemainingACL {
				slog.Error("cluster insert failed after minting a credential, and rolling it back left objects behind",
					"token_id", cred.TokenID, "user_id", cred.UserID,
					"remaining_user", rb.RemainingUser, "remaining_acl", rb.RemainingACL)
				return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf(
					"Failed to create the cluster, and the Proxmox objects created for it could not all be removed. Check %s in Proxmox before retrying.",
					cred.UserID))
			}
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create cluster")
	}

	auditDetails := map[string]any{
		"name":              cluster.Name,
		"credential_source": cred.Source,
	}
	if cred.Source == credentialSourceBootstrap {
		// Names only. The secret exists in exactly one place — the encrypted
		// column — and view:audit is held by every built-in Viewer.
		auditDetails["bootstrap_token_id"] = cred.TokenID
		auditDetails["bootstrap_created_user"] = cred.CreatedUser
		auditDetails["bootstrap_created_acl"] = cred.CreatedACL
	}
	details, _ := json.Marshal(auditDetails)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(cluster.ID), "cluster", cluster.ID.String(), "cluster_created", details)

	testResult := testClusterConnectivity(req.APIURL, cred.TokenID, cred.Secret, req.TLSFingerprint)

	// Pre-populate node entries with addresses from corosync discovery.
	if testResult.Result.Reachable {
		for _, entry := range testResult.Nodes {
			if entry.Type != "node" || entry.Name == "" {
				continue
			}
			_, nodeErr := h.queries.UpsertNode(c.Context(), db.UpsertNodeParams{
				ClusterID:      cluster.ID,
				Name:           entry.Name,
				Status:         "unknown",
				CpuCount:       0,
				MemTotal:       0,
				DiskTotal:      0,
				PveVersion:     "",
				SslFingerprint: "",
				Uptime:         0,
			})
			if nodeErr == nil && entry.IP != "" {
				_ = h.queries.UpdateNodeAddress(c.Context(), db.UpdateNodeAddressParams{
					ClusterID: cluster.ID,
					Name:      entry.Name,
					Address:   entry.IP,
				})
			}
		}
	}

	resp := createClusterResponse{
		Cluster:      toClusterResponse(cluster, nodeStatusInfo{}),
		Connectivity: testResult.Result,
	}
	if cred.Source == credentialSourceBootstrap {
		resp.Bootstrap = &bootstrapSummary{TokenID: cred.TokenID, Steps: cred.Steps}
	}
	return c.Status(fiber.StatusCreated).JSON(resp)
}

// List handles GET /api/v1/clusters.
func (h *ClusterHandler) List(c fiber.Ctx, _ *apischema.Params) error {
	access, err := accessibleClusters(c, "view", "cluster")
	if err != nil {
		return err
	}

	clusters, err := h.queries.ListClusters(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list clusters")
	}

	// Build a map of cluster_id → node status counts for computing cluster status.
	nsiMap := make(map[uuid.UUID]nodeStatusInfo)
	if rows, nsErr := h.queries.CountNodeStatusesByCluster(c.Context()); nsErr == nil {
		for _, r := range rows {
			nsiMap[r.ClusterID] = nodeStatusInfo{Total: r.Total, Online: r.Online}
		}
	}

	// Attach current health issues per cluster (Ceph, HA, disks, storage, failed
	// tasks, …) so they surface app-wide (header, dashboard, sidebar) without
	// per-cluster live calls.
	issuesMap := buildAllClusterIssues(c.Context(), h.queries)

	resp := make([]clusterResponse, 0, len(clusters))
	for _, cl := range clusters {
		if !access.PermitsCluster(cl.ID) {
			continue
		}
		cr := toClusterResponse(cl, nsiMap[cl.ID])
		cr.Issues = issuesMap[cl.ID]
		resp = append(resp, cr)
	}

	return RespondItems(c, resp)
}

// Get handles GET /api/v1/clusters/:id.
func (h *ClusterHandler) Get(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	cluster, err := h.queries.GetCluster(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	var nsi nodeStatusInfo
	if rows, nsErr := h.queries.CountNodeStatusesByCluster(c.Context()); nsErr == nil {
		for _, r := range rows {
			if r.ClusterID == id {
				nsi = nodeStatusInfo{Total: r.Total, Online: r.Online}
				break
			}
		}
	}

	cr := toClusterResponse(cluster, nsi)
	cr.Issues = buildAllClusterIssues(c.Context(), h.queries)[id]
	return c.JSON(cr)
}

// Update handles PUT /api/v1/clusters/:id.
func (h *ClusterHandler) Update(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	req := updateClusterRequestFromParams(p)

	// An explicit "" is not a way to say "keep the current secret" — omitting
	// the field is. Encrypting it would swap a working credential for the
	// ciphertext of an empty string (crypto.Encrypt always emits nonce+tag, so
	// the column stays non-empty and nothing downstream notices), which is a
	// one-request way to break a cluster's connectivity. It also reads to the
	// redirect check below as "no secret supplied", so the caller would be
	// told to re-enter a secret they did in fact send.
	if req.TokenSecret != nil && *req.TokenSecret == "" {
		return fiber.NewError(fiber.StatusBadRequest,
			"token_secret must not be empty — omit the field to keep the stored secret")
	}

	existing, err := h.queries.GetCluster(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	// Merge changed fields.
	params := db.UpdateClusterParams{
		ID:                   id,
		Name:                 existing.Name,
		ApiUrl:               existing.ApiUrl,
		TokenID:              existing.TokenID,
		TokenSecretEncrypted: existing.TokenSecretEncrypted,
		TlsFingerprint:       existing.TlsFingerprint,
		SyncIntervalSeconds:  existing.SyncIntervalSeconds,
		IsActive:             existing.IsActive,
	}

	if req.Name != nil {
		params.Name = *req.Name
	}
	if req.APIURL != nil {
		if err := validateURLFormat(*req.APIURL); err != nil {
			return err
		}
		if err := enforceURLAddressPolicy(c.Context(), *req.APIURL, req.AllowPrivateAddress); err != nil {
			return renderAddressPolicyError(c, err)
		}
		params.ApiUrl = *req.APIURL
	}
	if req.TokenID != nil {
		params.TokenID = *req.TokenID
	}
	if req.TokenSecret != nil {
		encrypted, err := crypto.Encrypt(*req.TokenSecret, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt token secret")
		}
		params.TokenSecretEncrypted = encrypted
	}
	if req.TLSFingerprint != nil {
		params.TlsFingerprint = *req.TLSFingerprint
	}
	if req.SyncIntervalSeconds != nil {
		params.SyncIntervalSeconds = *req.SyncIntervalSeconds
	}
	if req.IsActive != nil {
		params.IsActive = *req.IsActive
	}

	// Refuse to re-point a stored token at an address the operator never
	// entrusted it to. See credential_redirect.go for the shape; for a cluster
	// the delivery is immediate rather than deferred — testClusterConnectivity
	// at the end of this handler dials params.ApiUrl with the decrypted stored
	// secret before the response is written. Checked here, ahead of the write,
	// so a refused attempt leaves the row untouched.
	suppliedSecret := ""
	if req.TokenSecret != nil {
		suppliedSecret = *req.TokenSecret
	}
	if credentialRedirected(params.ApiUrl, existing.ApiUrl, existing.TokenSecretEncrypted, suppliedSecret) {
		// Audited, because this is the one request that is unambiguously an
		// attempt to point a stored credential somewhere new. Refusing it
		// silently would make an enumeration of this path invisible.
		redirectDetails, _ := json.Marshal(map[string]any{
			// Truncated: nothing bounds the length of a URL a caller can
			// submit, and audit_log.details is readable by every Viewer.
			"attempted_api_url": auditSafe(params.ApiUrl),
			"previous_api_url":  auditSafe(existing.ApiUrl),
		})
		AuditLog(c, h.queries, h.eventPub, ClusterUUID(id), "cluster", id.String(),
			"cluster_credential_redirect_refused", redirectDetails)
		return errCredentialRedirect("cluster API URL", "API token secret")
	}

	// Moving the address also re-homes the SSH credential, which the guard
	// above cannot see: cluster_ssh_credentials stores a password or private
	// key against the CLUSTER, while the host it is delivered to lives in
	// nodes.address — filled by the collector from whatever the API at
	// params.ApiUrl reports. So a caller who supplies their own token secret
	// (satisfying the redirect check, since the PVE token they are re-pointing
	// is one they chose) can stand up a host that advertises a node IP they
	// control, pin its key, and have every later rolling update or node command
	// hand it the cluster's real SSH credential.
	//
	// Confirmation cannot be the control here: the same caller holds
	// manage:ssh_credentials, so anything they can approve, they can approve
	// for themselves. Only re-supplying the secret proves possession, which is
	// the bargain credential_redirect.go strikes everywhere else — so drop the
	// trust anchor and make the operator re-enter it.
	//
	// The pins go too. They are keyed on (cluster_id, host, port) and describe
	// machines from the deployment being left behind; kept, a recycled address
	// in the new deployment would be silently authorized by an old pin.
	//
	// Deliberately BEFORE the write: if this fails, the address must not move,
	// because "new address + old SSH credential" is exactly the state being
	// prevented. The cost of the reverse ordering is a lost credential on a
	// write that then failed — recoverable by re-entering it, unlike the leak.
	//
	// token_id is not part of the predicate. It changes which identity Nexara
	// presents, not which host answers, so it cannot re-home a node address.
	if params.ApiUrl != existing.ApiUrl {
		// An in-flight rolling update reads these credentials on every tick
		// (internal/rolling/orchestrator.go advanceUpgrading). Pulling them
		// mid-run fails the node it is on while the cluster still holds a
		// drained node, disabled HA rules and a paused rebalance. Delete
		// already refuses for the same reason; fail closed if the check
		// itself errors, since we cannot rule an active job out.
		busy, busyErr := h.queries.HasRunningJobForCluster(c.Context(), id)
		if busyErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to check for active rolling update jobs")
		}
		if busy {
			return fiber.NewError(fiber.StatusConflict,
				"Cluster has an active rolling update job, and moving the API address re-homes the "+
					"node addresses it is working through. Cancel or finish the job first.")
		}

		// A terminal-but-uncleaned job also holds cluster-side state — a paused
		// CRS, disabled HA rules — and releaseJobState reaches it through a
		// client built from THIS row. Once the address moves, that sweep would
		// aim the old cluster's restore values at the new one. Refuse rather
		// than let the two clusters cross: unlike Delete, where the cluster is
		// usually gone for good and the state could never be released, here the
		// old address still works and the sweep can finish against it.
		if pending, pendErr := h.queries.ListCleanupPendingJobsForCluster(c.Context(), id); pendErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to check for pending rolling-update cleanup")
		} else if len(pending) > 0 {
			return fiber.NewError(fiber.StatusConflict,
				"Cluster has rolling-update state that has not been released yet (CRS pause / HA-rule "+
					"disables). Moving the API address would aim that cleanup at the new address. "+
					"Wait for the cleanup sweep to finish, then retry.")
		}

		// The token secret costs a re-type; this costs a password or private
		// key that is gone for good, plus every host-key pin. That asymmetry
		// is worth a confirmation on its own — a cosmetic edit (adding a
		// trailing slash, switching a hostname to its CNAME) reaches the same
		// destination and should not silently destroy the credential. The UI
		// warns before saving; this is the gate for every other caller.
		if err := h.requireSSHTrustResetAck(c.Context(), id, req.AcknowledgeSSHTrustReset); err != nil {
			return renderConfirmRequired(c, err)
		}

		if resetErr := h.resetClusterSSHTrust(c, id, existing.ApiUrl, params.ApiUrl); resetErr != nil {
			return resetErr
		}
	}

	cluster, err := h.queries.UpdateCluster(c.Context(), params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update cluster")
	}

	// Re-pointing a cluster at a different endpoint or a different token
	// invalidates its credential provenance: bootstrap_user_id and
	// bootstrap_token_name name objects on the target it USED to have. Left
	// stale, a later delete-with-revoke would issue DELETE /access/users
	// against a host where Nexara created nothing.
	//
	// A changed secret alone is not re-pointing — that is a token regenerate,
	// same user, same token name — so it deliberately does not clear.
	if params.ApiUrl != existing.ApiUrl || params.TokenID != existing.TokenID {
		if existing.CredentialSource == credentialSourceBootstrap {
			if clearErr := h.queries.ClearClusterCredentialProvenance(c.Context(), id); clearErr != nil {
				slog.Error("failed to clear stale cluster credential provenance",
					"cluster_id", id, "error", clearErr)
				return fiber.NewError(fiber.StatusInternalServerError, "Failed to update cluster credential provenance")
			}

			// Forgetting the provenance also forgets the objects Nexara created
			// on the OLD host — after this, deleting the cluster can never offer
			// to revoke them. Name them here or they are lost for good.
			forgotten, _ := json.Marshal(map[string]any{
				"reason":            "cluster re-pointed at a different endpoint or token; credential provenance cleared",
				"previous_api_url":  existing.ApiUrl,
				"previous_token_id": existing.TokenID,
				"orphaned_user_id":  existing.BootstrapUserID,
				"orphaned_token":    existing.BootstrapUserID + "!" + existing.BootstrapTokenName,
				"created_user":      existing.BootstrapCreatedUser,
				"created_acl":       existing.BootstrapCreatedAcl,
				"warning":           "these were created by Nexara on the previous host and must now be removed there by hand",
			})
			AuditLog(c, h.queries, h.eventPub, ClusterUUID(id), "cluster", id.String(), "cluster_credential_provenance_cleared", forgotten)

			cluster.CredentialSource = credentialSourceManual
			cluster.BootstrapUserID = ""
			cluster.BootstrapTokenName = ""
			cluster.BootstrapCreatedUser = false
			cluster.BootstrapCreatedAcl = false
		}
	}

	// Drop any cached *proxmox.Client built from the prior credentials so
	// the next API call rebuilds against the new BaseURL/TokenID/TokenSecret/
	// fingerprint. Same channel fans out to peer replicas so a multi-replica
	// deployment converges without waiting for cacheTTL.
	if cache := proxmoxCacheFromCtx(c); cache != nil {
		cache.PublishInvalidation(c.Context(), proxmox.CacheKindPVE, cluster.ID)
	}

	updateDetails, _ := json.Marshal(map[string]string{"name": cluster.Name})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(cluster.ID), "cluster", cluster.ID.String(), "cluster_updated", updateDetails)

	// Determine the token secret for connectivity test.
	var tokenSecret string
	if req.TokenSecret != nil {
		tokenSecret = *req.TokenSecret
	} else {
		tokenSecret, _ = crypto.Decrypt(existing.TokenSecretEncrypted, h.encryptionKey)
	}

	connectivity := testClusterConnectivity(params.ApiUrl, params.TokenID, tokenSecret, params.TlsFingerprint)

	var updateNsi nodeStatusInfo
	if rows, nsErr := h.queries.CountNodeStatusesByCluster(c.Context()); nsErr == nil {
		for _, r := range rows {
			if r.ClusterID == cluster.ID {
				updateNsi = nodeStatusInfo{Total: r.Total, Online: r.Online}
				break
			}
		}
	}

	return c.JSON(updateClusterResponse{
		Cluster:      toClusterResponse(cluster, updateNsi),
		Connectivity: connectivity.Result,
	})
}

// Delete handles DELETE /api/v1/clusters/:id.
func (h *ClusterHandler) Delete(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	cluster, err := h.queries.GetCluster(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	// An active rolling update holds cluster-side state (paused CRS
	// auto-rebalance, disabled HA rules, drained guests), and deleting the
	// cluster would CASCADE away the records needed to restore it. Fail
	// closed: if the check itself errors we can't rule out an active job.
	busy, busyErr := h.queries.HasRunningJobForCluster(c.Context(), id)
	if busyErr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check for active rolling update jobs")
	}
	if busy {
		return fiber.NewError(fiber.StatusConflict,
			"Cluster has an active rolling update job. Cancel it before deleting the cluster.")
	}

	// Terminal jobs whose cleanup is still pending also hold cluster-side
	// state, but they must not block deletion — a cluster being deleted is
	// often one that's gone for good, and its cleanup could never succeed.
	// Record what leaks so the audit trail explains the cluster-side residue
	// (paused CRS, disabled HA rules) if the cluster is ever re-added.
	auditFields := map[string]any{
		// Carried in the body because the row itself cannot name the cluster —
		// see the AuditLog call at the end of this function.
		"cluster_id": id.String(),
		"name":       cluster.Name,
		"api_url":    cluster.ApiUrl,
	}
	if pending, pendErr := h.queries.ListCleanupPendingJobsForCluster(c.Context(), id); pendErr == nil && len(pending) > 0 {
		ids := make([]string, len(pending))
		for i, pj := range pending {
			ids[i] = pj.ID.String()
		}
		auditFields["warning"] = "deleted with unreleased rolling-update state; CRS pause / HA-rule disables may persist on the Proxmox cluster"
		auditFields["cleanup_pending_job_ids"] = ids
	}

	// Opt-in Proxmox-side cleanup. Off by default: removing a cluster from
	// Nexara is a local act, and silently deleting users and tokens on a live
	// hypervisor is not something to infer from it.
	//
	// The permission is checked here, before anything is destroyed, but the
	// revocation itself runs after the row is gone. Revoking first would mean a
	// failed DeleteCluster leaves the operator with a cluster whose credential
	// has already been deleted on the hypervisor — present in Nexara, unable to
	// authenticate, and unfixable except by hand. `cluster` is a value copy, so
	// the credential is still readable after the row is deleted.
	revoke := wantsCredentialRevocation(p.String("revoke_pve_credentials"))
	if revoke {
		// Deleting the cluster needs delete:cluster, which can be granted
		// scoped to a single cluster. Mutating Proxmox's own access control is
		// a different act, and minting the credential in the first place
		// required GLOBAL manage:cluster — so removing it asks for the same
		// thing. Without this, a role holding only a cluster-scoped
		// delete:cluster could drive DELETE /access/users against a live
		// hypervisor.
		if err := requirePerm(c, "manage", "cluster"); err != nil {
			return err
		}
	}

	if err := h.queries.DeleteCluster(c.Context(), id); err != nil {
		// The old ordering audited before deleting, so a failed delete was the
		// one case that DID leave a row (nothing cascaded it away). Auditing
		// only on success would quietly lose that.
		auditFields["result"] = "failed"
		auditFields["error"] = auditSafe(err.Error())
		failDetails, _ := json.Marshal(auditFields)
		AuditLog(c, h.queries, h.eventPub, ClusterUUID(id), "cluster", id.String(), "cluster_deleted", failDetails)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete cluster")
	}

	if revoke {
		auditFields["revoke_pve_credentials"] = revocationOutcome(c.Context(), h.queries, cluster, h.encryptionKey)
	}

	// Audited after the delete succeeds, so the row records something that
	// actually happened — and so it can carry the revocation outcome.
	//
	// The row deliberately carries a NULL cluster_id, with the cluster's
	// identity in the details body instead. audit_log.cluster_id is
	// `REFERENCES clusters(id) ON DELETE CASCADE`, which makes a
	// cluster_deleted row that names its own cluster impossible to keep: write
	// it before the delete and the cascade destroys it; write it after and the
	// insert has nothing to point at. Either way the row vanished — this action
	// had never once been recorded in any install before this change.
	//
	// That matters more now than it did: this row carries the report of what
	// revocation left behind on the Proxmox side, which is the only durable
	// record an operator has to reconcile against.
	deleteDetails, _ := json.Marshal(auditFields)
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "cluster", id.String(), "cluster_deleted", deleteDetails)

	if cache := proxmoxCacheFromCtx(c); cache != nil {
		cache.PublishInvalidation(c.Context(), proxmox.CacheKindPVE, id)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// confirmSSHTrustReset is the code the cluster edit dialog keys on to turn the
// refusal into a prompt. Changing this string without changing
// EditClusterDialog's SSH_TRUST_RESET makes the cluster API URL unchangeable
// from the UI — the 422 stops matching, no panel renders, and the
// acknowledgement is never sent. TestSSHTrustResetGateWireCode pins the value.
const confirmSSHTrustReset = "ssh_trust_reset_confirm_required"

// requireSSHTrustResetAck asks before destroying an SSH credential that moving
// the address will invalidate. Confirm-and-proceed, in the same structured-422
// shape as the private-address and LDAP-transport gates, so the admin UI can
// turn it into a prompt rather than an unexplained failure.
//
// Silent only when there is genuinely nothing to lose — no stored credential
// AND no pinned host keys. Those two can exist independently: deleting the
// credential leaves the pins behind.
func (h *ClusterHandler) requireSSHTrustResetAck(ctx context.Context, id uuid.UUID, acknowledged bool) error {
	if acknowledged {
		return nil
	}

	hasCreds, err := h.queries.HasClusterSSHCredentials(ctx, id)
	if err != nil {
		slog.Error("failed to check for cluster SSH credentials before re-home",
			"cluster_id", id, "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check for SSH credentials")
	}

	// #8: the pins are destroyed too, and they outlive the credential — a
	// DELETE of the SSH credential leaves them behind. Prompting only on the
	// credential would silently discard trust the operator established.
	pins, pinErr := h.queries.ListSSHKnownHosts(ctx, id)
	if pinErr != nil {
		slog.Error("failed to list pinned SSH host keys before re-home",
			"cluster_id", id, "error", pinErr)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read pinned SSH host keys")
	}

	if !hasCreds && len(pins) == 0 {
		return nil
	}

	return &confirmRequiredError{
		Code: confirmSSHTrustReset,
		Message: "Node addresses are learned from this API, so the stored SSH credential and " +
			"every pinned host key were entrusted to machines this cluster is leaving. Saving " +
			"clears both, and the credential cannot be recovered.",
		AcknowledgeField: "acknowledge_ssh_trust_reset",
		Fields: map[string]any{
			"clears_ssh_credential": hasCreds,
			"clears_pinned_hosts":   len(pins),
		},
	}
}

// resetClusterSSHTrust drops the cluster's SSH credential and every pinned
// host key, because the address they were entrusted to has moved. Called only
// when clusters.api_url actually changes.
//
// A no-op when nothing is stored, so re-homing a cluster that never had SSH
// configured stays a plain address change.
func (h *ClusterHandler) resetClusterSSHTrust(c fiber.Ctx, id uuid.UUID, previousURL, newURL string) error {
	creds, credErr := h.queries.GetClusterSSHCredentials(c.Context(), id)
	hadCreds := credErr == nil
	if credErr != nil && !errors.Is(credErr, pgx.ErrNoRows) {
		slog.Error("failed to read cluster SSH credentials before re-home",
			"cluster_id", id, "error", credErr)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read cluster SSH credentials")
	}

	pins, pinErr := h.queries.ListSSHKnownHosts(c.Context(), id)
	if pinErr != nil {
		slog.Error("failed to list pinned SSH host keys before re-home",
			"cluster_id", id, "error", pinErr)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to read pinned SSH host keys")
	}

	if !hadCreds && len(pins) == 0 {
		return nil
	}

	// Both deletes are attempted and the outcome is recorded either way. An
	// early return between them would destroy the credential and leave no trace
	// of it — and this row is the only explanation the operator gets for their
	// rolling updates suddenly reporting SSH as unconfigured.
	var failure error
	credsCleared := false
	if hadCreds {
		if err := h.queries.DeleteClusterSSHCredentials(c.Context(), id); err != nil {
			slog.Error("failed to clear cluster SSH credentials on re-home",
				"cluster_id", id, "error", err)
			failure = fiber.NewError(fiber.StatusInternalServerError, "Failed to clear cluster SSH credentials")
		} else {
			credsCleared = true
		}
	}
	pinsCleared := false
	if len(pins) > 0 && failure == nil {
		if err := h.queries.DeleteSSHKnownHostsForCluster(c.Context(), id); err != nil {
			slog.Error("failed to clear pinned SSH host keys on re-home",
				"cluster_id", id, "error", err)
			failure = fiber.NewError(fiber.StatusInternalServerError, "Failed to clear pinned SSH host keys")
		} else {
			pinsCleared = true
		}
	}

	// Name what was dropped: after this the operator has to re-enter the
	// credential and re-pin, and this row is the only record of why their
	// rolling updates suddenly report SSH as unconfigured.
	//
	// Usernames and host addresses only — never the credential itself, and
	// note audit_log.details is readable by every Viewer.
	// Truncated and bounded like every other value in this row: ssh_known_hosts
	// .host is fed from nodes.address, which the collector fills from whatever
	// the cluster API reported — the same remote-controlled source the rest of
	// this blob is defended against — and audit_log.details is readable by
	// every Viewer.
	const maxAuditedPins = 64
	pinnedHosts := make([]string, 0, len(pins))
	for i, p := range pins {
		if i >= maxAuditedPins {
			pinnedHosts = append(pinnedHosts, "… and "+strconv.Itoa(len(pins)-maxAuditedPins)+" more")
			break
		}
		pinnedHosts = append(pinnedHosts, auditSafe(p.Host))
	}
	fields := map[string]any{
		"reason":                 "cluster re-pointed at a different API address; SSH trust anchor reset",
		"previous_api_url":       auditSafe(previousURL),
		"new_api_url":            auditSafe(newURL),
		"ssh_credential_cleared": credsCleared,
		"warning":                "re-enter the SSH credential and re-pin each node host key before the next rolling update",
	}
	if pinsCleared {
		fields["pinned_hosts_cleared"] = pinnedHosts
	}
	if failure != nil {
		// The address does NOT move when this is set, so the row describes a
		// half-done reset the operator has to reconcile by hand.
		fields["partial"] = true
		fields["warning"] = "SSH trust reset did not complete; the API address was NOT changed. " +
			"Re-check the cluster's SSH credential and pinned host keys."
	}
	if hadCreds {
		// The username names WHICH login has to be re-entered, which is the one
		// thing the operator cannot recover from the rest of this row. It is
		// otherwise reachable only with manage:ssh_credentials, and view:audit
		// is held by every Viewer, so this does widen who knows the privileged
		// login name — accepted because the row is useless without it, and
		// truncated like every other value here.
		fields["ssh_username"] = auditSafe(creds.Username)
	}
	details, _ := json.Marshal(fields)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(id), "cluster", id.String(),
		"cluster_ssh_trust_reset", details)

	return failure
}

type connectivityTestResult struct {
	Result connectivityResult
	Nodes  []proxmox.ClusterStatusEntry
}

func testClusterConnectivity(apiURL, tokenID, tokenSecret, tlsFingerprint string) connectivityTestResult {
	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        apiURL,
		TokenID:        tokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: tlsFingerprint,
		Timeout:        10 * time.Second,
	})
	if err != nil {
		return connectivityTestResult{Result: connectivityResult{Reachable: false, Message: "Failed to create client"}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	entries, err := client.GetClusterStatus(ctx)
	if err != nil {
		msg := "Connection failed"
		if errors.Is(err, proxmox.ErrForbidden) {
			msg = "Authentication failed: check token credentials"
		} else if errors.Is(err, proxmox.ErrConnectionFailed) {
			errStr := err.Error()
			if strings.Contains(errStr, "fingerprint mismatch") {
				msg = "TLS certificate has changed. The stored fingerprint no longer matches the server certificate. Please re-fetch the fingerprint."
			} else {
				msg = "Host unreachable or connection refused"
			}
		}
		return connectivityTestResult{Result: connectivityResult{Reachable: false, Message: msg}}
	}

	return connectivityTestResult{
		Result: connectivityResult{Reachable: true, Message: "Successfully connected to cluster"},
		Nodes:  entries,
	}
}

type fetchFingerprintResponse struct {
	Fingerprint string `json:"fingerprint"`
	SelfSigned  bool   `json:"self_signed"`
}

// FetchFingerprint handles POST /api/v1/clusters/fetch-fingerprint.
// It connects to the Proxmox host, retrieves the TLS certificate, and returns the SHA-256 fingerprint.
func (h *ClusterHandler) FetchFingerprint(c fiber.Ctx, p *apischema.Params) error {
	apiURL := p.String("api_url")
	if err := validateURLFormat(apiURL); err != nil {
		return err
	}
	if err := enforceURLAddressPolicy(c.Context(), apiURL, p.Bool("allow_private_address")); err != nil {
		return renderAddressPolicyError(c, err)
	}

	fingerprint, untrusted, err := dialLeafFingerprint(apiURL)
	if err != nil {
		return err
	}

	return c.JSON(fetchFingerprintResponse{
		Fingerprint: fingerprint,
		SelfSigned:  untrusted,
	})
}

// dialLeafFingerprint opens a TLS connection to apiURL and returns the leaf
// certificate's SHA-256 fingerprint in Proxmox's uppercase colon-separated
// form, plus whether the chain is untrusted by the system CA pool.
//
// The caller is responsible for having already run the URL through
// validateURLFormat and enforceURLAddressPolicy — this dials whatever it is
// given. Errors come back as fiber errors ready to return.
func dialLeafFingerprint(apiURL string) (fingerprint string, untrusted bool, err error) {
	u, _ := url.Parse(apiURL)
	// url.Port(), not a colon scan: a bracketed IPv6 literal such as
	// "[2001:db8::1]" is full of colons but carries no port, so the naive
	// check never appended one and the dial failed. VerifyCertificate reaches
	// this with a stored address rather than one a human just typed, so the
	// case is no longer hypothetical.
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}

	// Connect with InsecureSkipVerify to get the certificate regardless of CA trust.
	// The Control hook re-checks the resolved IP against the always-block set
	// so a hostile DNS authority can't redirect us to cloud metadata between
	// validation and dial.
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: netguard.DialControlSSRFGuard,
	}
	conn, dialErr := tls.DialWithDialer(dialer, "tcp", host, &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Intentional: we're fetching the fingerprint for user verification
		MinVersion:         tls.VersionTLS12,
	})
	if dialErr != nil {
		slog.Warn("fingerprint fetch: TLS dial failed", "host", u.Host, "error", dialErr)
		return "", false, fiber.NewError(fiber.StatusBadGateway, fmt.Sprintf("Failed to connect to %s (connection or TLS handshake failed)", u.Host))
	}
	defer func() { _ = conn.Close() }()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", false, fiber.NewError(fiber.StatusBadGateway, "Server presented no TLS certificates")
	}

	// SHA-256 fingerprint of the leaf certificate in colon-separated hex format.
	sum := sha256.Sum256(certs[0].Raw)
	hexStr := hex.EncodeToString(sum[:])
	var parts []string
	for i := 0; i < len(hexStr); i += 2 {
		parts = append(parts, strings.ToUpper(hexStr[i:i+2]))
	}
	fingerprint = strings.Join(parts, ":")

	// Check if the cert is trusted by the system CA pool.
	// Proxmox typically uses an internal CA (not self-signed leaf, but still untrusted).
	systemPool, _ := x509.SystemCertPool()
	untrusted = true
	if systemPool != nil {
		_, verifyErr := certs[0].Verify(x509.VerifyOptions{
			Roots: systemPool,
		})
		untrusted = verifyErr != nil
	}

	return fingerprint, untrusted, nil
}

type verifyCertificateResponse struct {
	Fingerprint string `json:"fingerprint"`
	Updated     bool   `json:"updated"`
	Message     string `json:"message"`
}

// VerifyCertificate handles POST /api/v1/clusters/:id/verify-certificate.
//
// Re-pins the cluster to the certificate its configured endpoint is currently
// serving, after corroborating it two independent ways. This exists because a
// Proxmox upgrade or an ACME renewal rotates node certificates, which breaks
// every live Proxmox call for the cluster until someone re-pins — a chore the
// operator previously had to do by hand through the edit dialog.
//
// What makes one click acceptable here is that neither source is trusted
// alone:
//
//   - A live TLS handshake to the configured api_url says what that endpoint
//     serves right now. On its own it proves nothing: an attacker in the path
//     would present their certificate to this dial exactly as they would to
//     any other.
//   - nodes.ssl_fingerprint is what the cluster itself reports for that node,
//     read by the collector over a connection pinned to a fingerprint we
//     already trusted. An attacker who cannot break that pin cannot forge it.
//
// Requiring both to agree means accepting only a certificate the existing
// trust chain vouches for. If they disagree the request is refused rather than
// resolved — that combination is the signature of an interception, and the
// operator needs to look rather than click again.
//
// Deliberately NOT automatic. Copying nodes.ssl_fingerprint into the cluster
// row on a timer would make the pin follow whatever the cluster reports, which
// is the same as not pinning at all.
func (h *ClusterHandler) VerifyCertificate(c fiber.Ctx, p *apischema.Params) error {
	// Re-pinning changes what the app will trust, so the declaration gates it
	// on manage:cluster — the same permission as editing the credentials.
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	cluster, err := h.queries.GetCluster(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	// Re-check before dialling. allowPrivate is true because the address is
	// already on file and was accepted once — re-prompting for a confirmation
	// on a one-click repair would be hostile — so this does NOT re-apply the
	// operator's private-address decision. What it still catches is the
	// always-blocked classes (cloud metadata, multicast, broadcast, Class E)
	// behind a hostname that has since been re-pointed; netguard's dial
	// control closes the rebinding window between here and the handshake.
	if err := validateURLFormat(cluster.ApiUrl); err != nil {
		return err
	}
	if err := enforceURLAddressPolicy(c.Context(), cluster.ApiUrl, true); err != nil {
		return renderAddressPolicyError(c, err)
	}

	live, _, err := dialLeafFingerprint(cluster.ApiUrl)
	if err != nil {
		return err
	}

	attested, err := h.attestedFingerprint(c, cluster)
	if err != nil {
		return err
	}

	switch decideCertificateVerify(cluster.TlsFingerprint, live, attested) {
	case certVerifyRefuseUnattested:
		return fiber.NewError(fiber.StatusConflict,
			"Nexara has no independently observed certificate for this endpoint to check against, "+
				"so it cannot verify one automatically. Set the fingerprint through Edit Cluster after "+
				"confirming it on the node itself.")
	case certVerifyRefuseMismatch:
		return fiber.NewError(fiber.StatusConflict,
			"The certificate served by this endpoint does not match what the cluster reports for that node. "+
				"Nexara will not pin it. Check the endpoint before retrying.")
	case certVerifyUnchanged:
		return c.JSON(verifyCertificateResponse{
			Fingerprint: live,
			Updated:     false,
			Message:     "The pinned certificate already matches; nothing to change.",
		})
	case certVerifyAccept:
		// fall through to the update below
	}

	previous := cluster.TlsFingerprint
	// Conditional on the api_url we dialled: if another admin re-pointed the
	// cluster while this request was in flight, pinning would attach the old
	// endpoint's certificate to the new address and break every call.
	rows, err := h.queries.UpdateClusterTLSFingerprint(c.Context(), db.UpdateClusterTLSFingerprintParams{
		ID:             id,
		TlsFingerprint: live,
		ApiUrl:         cluster.ApiUrl,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update cluster fingerprint")
	}
	if rows == 0 {
		return fiber.NewError(fiber.StatusConflict,
			"The cluster's address changed while its certificate was being verified. Try again.")
	}

	details, _ := json.Marshal(map[string]string{
		"cluster_name":         cluster.Name,
		"api_url":              cluster.ApiUrl,
		"previous_fingerprint": previous,
		"new_fingerprint":      live,
	})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{Bytes: id, Valid: true},
		"cluster", id.String(), "tls_fingerprint_verified", details)

	// Drop the cached client so the very next request uses the new pin rather
	// than waiting out the cache TTL.
	if cache := proxmoxCacheFromCtx(c); cache != nil {
		cache.PublishInvalidation(c.Context(), proxmox.CacheKindPVE, id)
	}

	return c.JSON(verifyCertificateResponse{
		Fingerprint: live,
		Updated:     true,
		Message:     "Certificate verified against the cluster and re-pinned.",
	})
}

// attestedFingerprint returns what the cluster itself reports for the node the
// api_url points at, or "" when nothing has been observed for it. Whether an
// empty result is fatal is decideCertificateVerify's call, not this lookup's.
func (h *ClusterHandler) attestedFingerprint(c fiber.Ctx, cluster db.Cluster) (string, error) {
	endpoints, err := h.queries.ListNodeEndpoints(c.Context(), cluster.ID)
	if err != nil {
		return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to list cluster nodes")
	}

	// nodes.ssl_fingerprint describes pveproxy's certificate only, so it can
	// corroborate this endpoint only when the endpoint IS pveproxy. Without
	// this, a reverse proxy terminating TLS on the node's own address matches
	// by address and its perfectly valid certificate gets reported as a
	// mismatch — telling a correctly configured operator they may be under
	// attack. Returning "" instead routes them to the manual path, which is
	// the honest answer: we have nothing to check against.
	if !proxmox.APIURLIsDirectToPVEProxy(cluster.ApiUrl) {
		return "", nil
	}

	host := proxmox.APIURLHost(cluster.ApiUrl)
	for _, ep := range endpoints {
		if strings.EqualFold(ep.Address, host) && ep.SslFingerprint != "" {
			return ep.SslFingerprint, nil
		}
	}
	return "", nil
}

// certVerifyOutcome is what VerifyCertificate should do with a candidate
// certificate. Split out from the handler so the security decision — the only
// part of this flow that can go wrong quietly — is a pure function with a
// table test, rather than three conditionals wrapped around a database.
type certVerifyOutcome int

const (
	// certVerifyAccept: corroborated by both sources and different from the
	// current pin, so re-pin.
	certVerifyAccept certVerifyOutcome = iota
	// certVerifyUnchanged: already pinned to this certificate. Not an error —
	// the operator may simply have clicked twice, or another admin got there
	// first.
	certVerifyUnchanged
	// certVerifyRefuseUnattested: nothing observed for this endpoint, so there
	// is no second source. Accepting on the live handshake alone would trust
	// whatever answers the address, which is what pinning exists to prevent.
	certVerifyRefuseUnattested
	// certVerifyRefuseMismatch: the live handshake and the cluster's own
	// report disagree. That is the shape of an interception, so refuse and
	// make a human look.
	certVerifyRefuseMismatch
)

// decideCertificateVerify judges a candidate certificate against the two
// independent sources.
//
// Order matters: the absence of corroboration is checked before agreement, so
// an unattested endpoint can never be waved through by comparing a value
// against itself.
//
// The explicit `pinned != ""` is load-bearing for the same reason.
// EndpointCertificateChanged answers "changed" as false when either side is
// unknown — correct for its own callers, where absence of evidence must not
// raise an alarm, but it means an unpinned cluster would otherwise report
// "already matches" and never get pinned. An unpinned cluster reaching here
// has had an operator explicitly ask to verify and pin, and both sources
// corroborate the certificate, so pinning it is the right answer and strictly
// stronger than leaving it on system-CA verification.
func decideCertificateVerify(pinned, live, attested string) certVerifyOutcome {
	if attested == "" || live == "" {
		return certVerifyRefuseUnattested
	}
	if proxmox.EndpointCertificateChanged(attested, live) {
		return certVerifyRefuseMismatch
	}
	if pinned != "" && !proxmox.EndpointCertificateChanged(pinned, live) {
		return certVerifyUnchanged
	}
	return certVerifyAccept
}
