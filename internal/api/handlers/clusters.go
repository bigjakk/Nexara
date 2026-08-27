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
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/netguard"
	"github.com/bigjakk/nexara/internal/proxmox"
)

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

type createClusterRequest struct {
	Name                string `json:"name"`
	APIURL              string `json:"api_url"`
	TokenID             string `json:"token_id"`
	TokenSecret         string `json:"token_secret"`
	TLSFingerprint      string `json:"tls_fingerprint"`
	SyncIntervalSeconds *int32 `json:"sync_interval_seconds"`
	// AllowPrivateAddress, when true, lets the URL resolve to a private/
	// loopback/link-local IP (typical homelab setup). Cloud metadata,
	// unspecified, and multicast addresses are still rejected.
	AllowPrivateAddress bool `json:"allow_private_address,omitempty"`
	// Bootstrap asks Nexara to mint the cluster's credential itself instead of
	// being handed one. Mutually exclusive with token_id/token_secret.
	Bootstrap *bootstrapRequest `json:"bootstrap,omitempty"`
}

type updateClusterRequest struct {
	Name                *string `json:"name"`
	APIURL              *string `json:"api_url"`
	TokenID             *string `json:"token_id"`
	TokenSecret         *string `json:"token_secret"`
	TLSFingerprint      *string `json:"tls_fingerprint"`
	SyncIntervalSeconds *int32  `json:"sync_interval_seconds"`
	IsActive            *bool   `json:"is_active"`
	AllowPrivateAddress bool    `json:"allow_private_address,omitempty"`
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
func (h *ClusterHandler) Create(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cluster"); err != nil {
		return err
	}

	var req createClusterRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Name == "" || req.APIURL == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name and api_url are required")
	}

	if len(req.Name) > 255 {
		return fiber.NewError(fiber.StatusBadRequest, "name must be 255 characters or fewer")
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
		return fiber.NewError(fiber.StatusBadRequest, "name, api_url, token_id, and token_secret are required")
	}

	if err := validateURLFormat(req.APIURL); err != nil {
		return err
	}
	if err := enforceURLAddressPolicy(c.Context(), req.APIURL, req.AllowPrivateAddress); err != nil {
		return renderAddressPolicyError(c, err)
	}

	syncInterval := int32(30)
	if req.SyncIntervalSeconds != nil {
		if *req.SyncIntervalSeconds < 10 || *req.SyncIntervalSeconds > 86400 {
			return fiber.NewError(fiber.StatusBadRequest, "sync_interval_seconds must be between 10 and 86400")
		}
		syncInterval = *req.SyncIntervalSeconds
	}

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
func (h *ClusterHandler) List(c fiber.Ctx) error {
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
func (h *ClusterHandler) Get(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	if err := requireClusterPerm(c, "view", "cluster", id); err != nil {
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
func (h *ClusterHandler) Update(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	if err := requireClusterPerm(c, "manage", "cluster", id); err != nil {
		return err
	}

	var req updateClusterRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

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
		if len(*req.Name) > 255 {
			return fiber.NewError(fiber.StatusBadRequest, "name must be 255 characters or fewer")
		}
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
		if *req.SyncIntervalSeconds < 10 || *req.SyncIntervalSeconds > 86400 {
			return fiber.NewError(fiber.StatusBadRequest, "sync_interval_seconds must be between 10 and 86400")
		}
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
func (h *ClusterHandler) Delete(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}
	if err := requireClusterPerm(c, "delete", "cluster", id); err != nil {
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
	revoke := wantsCredentialRevocation(c)
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

type fetchFingerprintRequest struct {
	APIURL              string `json:"api_url"`
	AllowPrivateAddress bool   `json:"allow_private_address,omitempty"`
}

type fetchFingerprintResponse struct {
	Fingerprint string `json:"fingerprint"`
	SelfSigned  bool   `json:"self_signed"`
}

// FetchFingerprint handles POST /api/v1/clusters/fetch-fingerprint.
// It connects to the Proxmox host, retrieves the TLS certificate, and returns the SHA-256 fingerprint.
func (h *ClusterHandler) FetchFingerprint(c fiber.Ctx) error {
	// Any global permission that lets the caller register a remote server
	// qualifies, because every one of those add-flows starts here: the
	// operator has to see and accept a certificate before a credential is
	// stored against it.
	//
	// Gating on manage:cluster alone made a backup-only role unusable — a
	// role holding manage:pbs or manage:veeam but not manage:cluster was
	// refused at step 1 and could never reach the create endpoint it *was*
	// granted. Widening costs nothing: each of these permissions can already
	// drive an outbound connection to an arbitrary operator-supplied URL
	// through its own create endpoint, and this one returns a certificate,
	// not a secret.
	if err := requireAnyGlobalManage(c, "cluster", "pbs", "veeam"); err != nil {
		return err
	}

	var req fetchFingerprintRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.APIURL == "" {
		return fiber.NewError(fiber.StatusBadRequest, "api_url is required")
	}
	if err := validateURLFormat(req.APIURL); err != nil {
		return err
	}
	if err := enforceURLAddressPolicy(c.Context(), req.APIURL, req.AllowPrivateAddress); err != nil {
		return renderAddressPolicyError(c, err)
	}

	u, _ := url.Parse(req.APIURL)
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	// Connect with InsecureSkipVerify to get the certificate regardless of CA trust.
	// The Control hook re-checks the resolved IP against the always-block set
	// so a hostile DNS authority can't redirect us to cloud metadata between
	// validation and dial.
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: netguard.DialControlSSRFGuard,
	}
	conn, err := tls.DialWithDialer(dialer, "tcp", host, &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Intentional: we're fetching the fingerprint for user verification
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		slog.Warn("fingerprint fetch: TLS dial failed", "host", u.Host, "error", err)
		return fiber.NewError(fiber.StatusBadGateway, fmt.Sprintf("Failed to connect to %s (connection or TLS handshake failed)", u.Host))
	}
	defer func() { _ = conn.Close() }()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return fiber.NewError(fiber.StatusBadGateway, "Server presented no TLS certificates")
	}

	// SHA-256 fingerprint of the leaf certificate in colon-separated hex format.
	sum := sha256.Sum256(certs[0].Raw)
	hexStr := hex.EncodeToString(sum[:])
	var parts []string
	for i := 0; i < len(hexStr); i += 2 {
		parts = append(parts, strings.ToUpper(hexStr[i:i+2]))
	}
	fingerprint := strings.Join(parts, ":")

	// Check if the cert is trusted by the system CA pool.
	// Proxmox typically uses an internal CA (not self-signed leaf, but still untrusted).
	systemPool, _ := x509.SystemCertPool()
	untrusted := true
	if systemPool != nil {
		_, verifyErr := certs[0].Verify(x509.VerifyOptions{
			Roots: systemPool,
		})
		untrusted = verifyErr != nil
	}

	return c.JSON(fetchFingerprintResponse{
		Fingerprint: fingerprint,
		SelfSigned:  untrusted,
	})
}
