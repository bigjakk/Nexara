package handlers

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
)

// PBSHandler handles PBS server CRUD endpoints.
type PBSHandler struct {
	queries       *db.Queries
	encryptionKey string
}

// NewPBSHandler creates a new PBS server handler.
func NewPBSHandler(queries *db.Queries, encryptionKey string) *PBSHandler {
	return &PBSHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
	}
}

type createPBSRequest struct {
	Name        string  `json:"name"`
	APIURL      string  `json:"api_url"`
	TokenID     string  `json:"token_id"`
	TokenSecret string  `json:"token_secret"`
	ClusterID   *string `json:"cluster_id"`
}

type updatePBSRequest struct {
	Name        *string `json:"name"`
	APIURL      *string `json:"api_url"`
	TokenID     *string `json:"token_id"`
	TokenSecret *string `json:"token_secret"`
	ClusterID   *string `json:"cluster_id"`
}

type pbsResponse struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	APIURL    string     `json:"api_url"`
	TokenID   string     `json:"token_id"`
	ClusterID *uuid.UUID `json:"cluster_id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func toPBSResponse(p db.PbsServer) pbsResponse {
	resp := pbsResponse{
		ID:        p.ID,
		Name:      p.Name,
		APIURL:    p.ApiUrl,
		TokenID:   p.TokenID,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
	if p.ClusterID.Valid {
		id := p.ClusterID.Bytes
		uid := uuid.UUID(id)
		resp.ClusterID = &uid
	}
	return resp
}

// Create handles POST /api/v1/pbs-servers.
func (h *PBSHandler) Create(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	var req createPBSRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
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

	encrypted, err := crypto.Encrypt(req.TokenSecret, h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt token secret")
	}

	var clusterID pgtype.UUID
	if req.ClusterID != nil {
		parsed, err := uuid.Parse(*req.ClusterID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id format")
		}
		clusterID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	pbs, err := h.queries.CreatePBSServer(c.Context(), db.CreatePBSServerParams{
		Name:                 req.Name,
		ApiUrl:               req.APIURL,
		TokenID:              req.TokenID,
		TokenSecretEncrypted: encrypted,
		ClusterID:            clusterID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create PBS server")
	}

	return c.Status(fiber.StatusCreated).JSON(toPBSResponse(pbs))
}

// List handles GET /api/v1/pbs-servers.
func (h *PBSHandler) List(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	servers, err := h.queries.ListPBSServers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list PBS servers")
	}

	resp := make([]pbsResponse, len(servers))
	for i, s := range servers {
		resp[i] = toPBSResponse(s)
	}

	return c.JSON(resp)
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/pbs-servers.
func (h *PBSHandler) ListByCluster(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	clusterID, err := uuid.Parse(c.Params("cluster_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
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
	if err := requireAdmin(c); err != nil {
		return err
	}

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

	return c.JSON(toPBSResponse(pbs))
}

// Update handles PUT /api/v1/pbs-servers/:id.
func (h *PBSHandler) Update(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

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

	params := db.UpdatePBSServerParams{
		ID:                   id,
		Name:                 existing.Name,
		ApiUrl:               existing.ApiUrl,
		TokenID:              existing.TokenID,
		TokenSecretEncrypted: existing.TokenSecretEncrypted,
		ClusterID:            existing.ClusterID,
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

	pbs, err := h.queries.UpdatePBSServer(c.Context(), params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update PBS server")
	}

	return c.JSON(toPBSResponse(pbs))
}

// Delete handles DELETE /api/v1/pbs-servers/:id.
func (h *PBSHandler) Delete(c *fiber.Ctx) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid PBS server ID")
	}

	_, err = h.queries.GetPBSServer(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}

	if err := h.queries.DeletePBSServer(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete PBS server")
	}

	return c.SendStatus(fiber.StatusNoContent)
}
