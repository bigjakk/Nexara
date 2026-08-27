package handlers

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// PBSHandler handles PBS server CRUD endpoints.
type PBSHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewPBSHandler creates a new PBS server handler.
func NewPBSHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *PBSHandler {
	return &PBSHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
	}
}

type createPBSRequest struct {
	Name                string  `json:"name"`
	APIURL              string  `json:"api_url"`
	TokenID             string  `json:"token_id"`
	TokenSecret         string  `json:"token_secret"`
	TLSFingerprint      string  `json:"tls_fingerprint"`
	ClusterID           *string `json:"cluster_id"`
	AllowPrivateAddress bool    `json:"allow_private_address,omitempty"`
}

type updatePBSRequest struct {
	Name                *string `json:"name"`
	APIURL              *string `json:"api_url"`
	TokenID             *string `json:"token_id"`
	TokenSecret         *string `json:"token_secret"`
	TLSFingerprint      *string `json:"tls_fingerprint"`
	ClusterID           *string `json:"cluster_id"`
	AllowPrivateAddress bool    `json:"allow_private_address,omitempty"`
}

type pbsResponse struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	APIURL         string     `json:"api_url"`
	TokenID        string     `json:"token_id"`
	TLSFingerprint string     `json:"tls_fingerprint"`
	ClusterID      *uuid.UUID `json:"cluster_id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func toPBSResponse(p db.PbsServer) pbsResponse {
	resp := pbsResponse{
		ID:             p.ID,
		Name:           p.Name,
		APIURL:         p.ApiUrl,
		TokenID:        p.TokenID,
		TLSFingerprint: p.TlsFingerprint,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
	}
	if p.ClusterID.Valid {
		id := p.ClusterID.Bytes
		uid := uuid.UUID(id)
		resp.ClusterID = &uid
	}
	return resp
}

// Create handles POST /api/v1/pbs-servers.
func (h *PBSHandler) Create(c fiber.Ctx) error {
	var req createPBSRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// Permission gate runs against the cluster_id in the body when present
	// (so an operator with manage:pbs only on cluster X can attach a PBS to
	// cluster X), or globally otherwise.
	if req.ClusterID != nil && *req.ClusterID != "" {
		parsed, err := uuid.Parse(*req.ClusterID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id format")
		}
		if err := requireClusterPerm(c, "manage", "pbs", parsed); err != nil {
			return err
		}
	} else {
		// Standalone PBS server — gate behind global manage:pbs.
		if err := requirePerm(c, "manage", "pbs"); err != nil {
			return err
		}
	}

	if req.Name == "" || req.APIURL == "" || req.TokenID == "" || req.TokenSecret == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name, api_url, token_id, and token_secret are required")
	}

	if len(req.Name) > 255 {
		return fiber.NewError(fiber.StatusBadRequest, "name must be 255 characters or fewer")
	}

	if err := validateURLFormat(req.APIURL); err != nil {
		return err
	}
	if err := enforceURLAddressPolicy(c.Context(), req.APIURL, req.AllowPrivateAddress); err != nil {
		return renderAddressPolicyError(c, err)
	}

	var clusterID pgtype.UUID
	if req.ClusterID != nil && *req.ClusterID != "" {
		parsed, _ := uuid.Parse(*req.ClusterID)
		clusterID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	encrypted, err := crypto.Encrypt(req.TokenSecret, h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt token secret")
	}

	pbs, err := h.queries.CreatePBSServer(c.Context(), db.CreatePBSServerParams{
		Name:                 req.Name,
		ApiUrl:               req.APIURL,
		TokenID:              req.TokenID,
		TokenSecretEncrypted: encrypted,
		ClusterID:            clusterID,
		TlsFingerprint:       req.TLSFingerprint,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create PBS server")
	}

	AuditLog(c, h.queries, h.eventPub,
		pbs.ClusterID, "pbs_server", pbs.ID.String(), "pbs_created",
		pbsAuditName(pbs.Name))

	return c.Status(fiber.StatusCreated).JSON(toPBSResponse(pbs))
}

// List handles GET /api/v1/pbs-servers.
func (h *PBSHandler) List(c fiber.Ctx) error {
	access, err := accessibleClusters(c, "view", "pbs")
	if err != nil {
		return err
	}

	servers, err := h.queries.ListPBSServers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list PBS servers")
	}

	resp := make([]pbsResponse, 0, len(servers))
	for _, s := range servers {
		// Standalone PBS (no cluster_id) requires global view:pbs; cluster-bound
		// PBS requires view:pbs on that cluster.
		if !s.ClusterID.Valid {
			if !access.HasGlobal {
				continue
			}
		} else if !access.PermitsCluster(uuid.UUID(s.ClusterID.Bytes)) {
			continue
		}
		resp = append(resp, toPBSResponse(s))
	}

	return RespondItems(c, resp)
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/pbs-servers.
func (h *PBSHandler) ListByCluster(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "pbs", clusterID); err != nil {
		return err
	}

	servers, err := h.queries.ListPBSServersByCluster(c.Context(), pgtype.UUID{Bytes: clusterID, Valid: true})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list PBS servers")
	}

	resp := make([]pbsResponse, len(servers))
	for i, s := range servers {
		resp[i] = toPBSResponse(s)
	}

	return RespondItems(c, resp)
}

// Get handles GET /api/v1/pbs-servers/:id.
func (h *PBSHandler) Get(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid PBS server ID")
	}

	pbs, err := h.queries.GetPBSServer(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}

	if pbs.ClusterID.Valid {
		if err := requireClusterPerm(c, "view", "pbs", uuid.UUID(pbs.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "view", "pbs"); err != nil {
		return err
	}

	return c.JSON(toPBSResponse(pbs))
}

// Update handles PUT /api/v1/pbs-servers/:id.
func (h *PBSHandler) Update(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid PBS server ID")
	}

	var req updatePBSRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// An explicit "" is not a way to say "keep the current secret" — omitting
	// the field is. Encrypting it would swap a working credential for the
	// ciphertext of an empty string (crypto.Encrypt always emits nonce+tag, so
	// the column stays non-empty and nothing downstream notices), which is a
	// one-request way to break this server's collection. It also reads to the
	// redirect check below as "no secret supplied", so the caller would be
	// told to re-enter a secret they did in fact send.
	if req.TokenSecret != nil && *req.TokenSecret == "" {
		return fiber.NewError(fiber.StatusBadRequest,
			"token_secret must not be empty — omit the field to keep the stored secret")
	}

	existing, err := h.queries.GetPBSServer(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}

	if existing.ClusterID.Valid {
		if err := requireClusterPerm(c, "manage", "pbs", uuid.UUID(existing.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "manage", "pbs"); err != nil {
		return err
	}
	// If the user is moving the PBS server to a different cluster, also require
	// manage:pbs on the target cluster.
	if req.ClusterID != nil {
		parsed, perr := uuid.Parse(*req.ClusterID)
		if perr == nil {
			if err := requireClusterPerm(c, "manage", "pbs", parsed); err != nil {
				return err
			}
		}
	}

	params := db.UpdatePBSServerParams{
		ID:                   id,
		Name:                 existing.Name,
		ApiUrl:               existing.ApiUrl,
		TokenID:              existing.TokenID,
		TokenSecretEncrypted: existing.TokenSecretEncrypted,
		ClusterID:            existing.ClusterID,
		TlsFingerprint:       existing.TlsFingerprint,
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
	if req.ClusterID != nil {
		parsed, err := uuid.Parse(*req.ClusterID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id format")
		}
		params.ClusterID = pgtype.UUID{Bytes: parsed, Valid: true}
	}
	if req.TLSFingerprint != nil {
		params.TlsFingerprint = *req.TLSFingerprint
	}

	// Refuse to re-point a stored token at an address the operator never
	// entrusted it to. See credential_redirect.go. Nothing connects in this
	// handler, so the delivery is deferred: the collector rebuilds a client
	// from this row on its next sync (internal/proxmox/cache.go), which is
	// what makes the row worth refusing rather than merely warning about.
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
		AuditLog(c, h.queries, h.eventPub, existing.ClusterID, "pbs_server", id.String(),
			"pbs_credential_redirect_refused", redirectDetails)
		return errCredentialRedirect("server API URL", "API token secret")
	}

	pbs, err := h.queries.UpdatePBSServer(c.Context(), params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update PBS server")
	}

	if cache := proxmoxCacheFromCtx(c); cache != nil {
		cache.PublishInvalidation(c.Context(), proxmox.CacheKindPBS, pbs.ID)
	}

	AuditLog(c, h.queries, h.eventPub,
		pbs.ClusterID, "pbs_server", pbs.ID.String(), "pbs_updated",
		pbsAuditName(pbs.Name))

	return c.JSON(toPBSResponse(pbs))
}

// Delete handles DELETE /api/v1/pbs-servers/:id.
func (h *PBSHandler) Delete(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid PBS server ID")
	}

	existing, err := h.queries.GetPBSServer(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}

	if existing.ClusterID.Valid {
		if err := requireClusterPerm(c, "delete", "pbs", uuid.UUID(existing.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "delete", "pbs"); err != nil {
		return err
	}

	AuditLog(c, h.queries, h.eventPub,
		existing.ClusterID, "pbs_server", id.String(), "pbs_deleted",
		pbsAuditName(existing.Name))

	if err := h.queries.DeletePBSServer(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete PBS server")
	}

	if cache := proxmoxCacheFromCtx(c); cache != nil {
		cache.PublishInvalidation(c.Context(), proxmox.CacheKindPBS, id)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// pbsAuditName builds the details blob for the plain name-only PBS audit rows.
// Marshalled rather than concatenated: audit_log.details is JSONB NOT NULL, so
// a server name containing a quote would produce a malformed blob, fail the
// insert, and leave the action with no audit row at all — AuditLog only logs
// that failure. A crafted name could otherwise inject its own keys into a row
// every Viewer can read.
func pbsAuditName(name string) json.RawMessage {
	details, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return details
}
