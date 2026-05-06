package handlers

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
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
	Name           string  `json:"name"`
	APIURL         string  `json:"api_url"`
	TokenID        string  `json:"token_id"`
	TokenSecret    string  `json:"token_secret"`
	TLSFingerprint string  `json:"tls_fingerprint"`
	ClusterID      *string `json:"cluster_id"`
}

type updatePBSRequest struct {
	Name           *string `json:"name"`
	APIURL         *string `json:"api_url"`
	TokenID        *string `json:"token_id"`
	TokenSecret    *string `json:"token_secret"`
	TLSFingerprint *string `json:"tls_fingerprint"`
	ClusterID      *string `json:"cluster_id"`
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

// auditLog writes an audit log entry. Failures are logged but don't fail the request.
func (h *PBSHandler) auditLog(c *fiber.Ctx, resourceType, resourceID, action string, details json.RawMessage, clusterID pgtype.UUID) {
	AuditLog(c, h.queries, h.eventPub, clusterID, resourceType, resourceID, action, details)
}

// Create handles POST /api/v1/pbs-servers.
func (h *PBSHandler) Create(c *fiber.Ctx) error {
	var req createPBSRequest
	if err := c.BodyParser(&req); err != nil {
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

	if err := validateURL(req.APIURL); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
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

	h.auditLog(c, "pbs_server", pbs.ID.String(), "pbs_created",
		json.RawMessage(`{"name":"`+pbs.Name+`"}`), pbs.ClusterID)

	return c.Status(fiber.StatusCreated).JSON(toPBSResponse(pbs))
}

// List handles GET /api/v1/pbs-servers.
func (h *PBSHandler) List(c *fiber.Ctx) error {
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

	return c.JSON(resp)
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/pbs-servers.
func (h *PBSHandler) ListByCluster(c *fiber.Ctx) error {
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

	return c.JSON(resp)
}

// Get handles GET /api/v1/pbs-servers/:id.
func (h *PBSHandler) Get(c *fiber.Ctx) error {
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
func (h *PBSHandler) Update(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid PBS server ID")
	}

	var req updatePBSRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
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
		if err := validateURL(*req.APIURL); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
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

	pbs, err := h.queries.UpdatePBSServer(c.Context(), params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update PBS server")
	}

	h.auditLog(c, "pbs_server", pbs.ID.String(), "pbs_updated",
		json.RawMessage(`{"name":"`+pbs.Name+`"}`), pbs.ClusterID)

	return c.JSON(toPBSResponse(pbs))
}

// Delete handles DELETE /api/v1/pbs-servers/:id.
func (h *PBSHandler) Delete(c *fiber.Ctx) error {
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

	h.auditLog(c, "pbs_server", id.String(), "pbs_deleted",
		json.RawMessage(`{"name":"`+existing.Name+`"}`), existing.ClusterID)

	if err := h.queries.DeletePBSServer(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete PBS server")
	}

	return c.SendStatus(fiber.StatusNoContent)
}
