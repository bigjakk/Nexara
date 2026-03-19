package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// APIKeyHandler handles API key management endpoints.
type APIKeyHandler struct {
	queries  *db.Queries
	eventPub *events.Publisher
}

// NewAPIKeyHandler creates a new API key handler.
func NewAPIKeyHandler(queries *db.Queries, eventPub *events.Publisher) *APIKeyHandler {
	return &APIKeyHandler{
		queries:  queries,
		eventPub: eventPub,
	}
}

// --- Request / Response types ---

type createAPIKeyRequest struct {
	Name      string `json:"name"`
	ExpiresIn *int64 `json:"expires_in"` // seconds, optional, minimum 3600
}

type apiKeyResponse struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	ExpiresAt  *string    `json:"expires_at"`
	LastUsedAt *string    `json:"last_used_at"`
	LastUsedIP *string    `json:"last_used_ip"`
	IsRevoked  bool       `json:"is_revoked"`
	CreatedAt  string     `json:"created_at"`
}

type createAPIKeyResponse struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	KeyPrefix string    `json:"key_prefix"`
	ExpiresAt *string   `json:"expires_at"`
	CreatedAt string    `json:"created_at"`
}

type adminAPIKeyResponse struct {
	ID              uuid.UUID `json:"id"`
	UserID          uuid.UUID `json:"user_id"`
	Name            string    `json:"name"`
	KeyPrefix       string    `json:"key_prefix"`
	ExpiresAt       *string   `json:"expires_at"`
	LastUsedAt      *string   `json:"last_used_at"`
	LastUsedIP      *string   `json:"last_used_ip"`
	IsRevoked       bool      `json:"is_revoked"`
	CreatedAt       string    `json:"created_at"`
	UserEmail       string    `json:"user_email"`
	UserDisplayName string    `json:"user_display_name"`
}

// --- Helpers ---

// tsPtr returns a pointer to an RFC3339 string for a nullable timestamp, or nil if null.
func tsPtr(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	s := ts.Time.Format(time.RFC3339)
	return &s
}

// textPtr returns a pointer to the string value, or nil if the text is null.
func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

// --- Handlers ---

// Create handles POST /api/v1/api-keys.
func (h *APIKeyHandler) Create(c *fiber.Ctx) error {
	// API keys cannot create new API keys — require an interactive JWT session.
	// This prevents key self-replication if a key is compromised.
	if authMethod, _ := c.Locals("auth_method").(string); authMethod == "api_key" {
		return fiber.NewError(fiber.StatusForbidden, "API keys cannot be created using API key authentication; use an interactive login session")
	}

	if err := requirePerm(c, "manage", "api_key"); err != nil {
		return err
	}

	var req createAPIKeyRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// Validate name.
	if req.Name == "" || len(req.Name) > 100 {
		return fiber.NewError(fiber.StatusBadRequest, "Name is required and must be 1-100 characters")
	}

	// Validate expires_in if provided.
	var expiresAt pgtype.Timestamptz
	if req.ExpiresIn != nil {
		if *req.ExpiresIn < 3600 {
			return fiber.NewError(fiber.StatusBadRequest, "Expiration must be at least 3600 seconds (1 hour)")
		}
		expiresAt = pgtype.Timestamptz{
			Time:  time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second),
			Valid: true,
		}
	}

	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	// Enforce per-user key limit.
	count, err := h.queries.CountActiveAPIKeysByUser(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check API key count")
	}
	if count >= 25 {
		return fiber.NewError(fiber.StatusConflict, "Maximum of 25 active API keys per user")
	}

	// Generate a cryptographically random key.
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate API key")
	}
	fullKey := "nxra_" + base64.RawURLEncoding.EncodeToString(randomBytes)

	// Store the first 12 characters as prefix for identification.
	keyPrefix := fullKey[:12]

	// Hash the full key for storage.
	keyHash := auth.HashToken(fullKey)

	apiKey, err := h.queries.CreateAPIKey(c.Context(), db.CreateAPIKeyParams{
		UserID:    userID,
		Name:      req.Name,
		KeyPrefix: keyPrefix,
		KeyHash:   keyHash,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create API key")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "api_key", apiKey.ID.String(), "api_key_created", nil)

	return c.Status(fiber.StatusCreated).JSON(createAPIKeyResponse{
		ID:        apiKey.ID,
		Name:      apiKey.Name,
		Key:       fullKey,
		KeyPrefix: apiKey.KeyPrefix,
		ExpiresAt: tsPtr(apiKey.ExpiresAt),
		CreatedAt: apiKey.CreatedAt.Format(time.RFC3339),
	})
}

// List handles GET /api/v1/api-keys.
func (h *APIKeyHandler) List(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "api_key"); err != nil {
		return err
	}

	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	keys, err := h.queries.ListAPIKeysByUser(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list API keys")
	}

	resp := make([]apiKeyResponse, len(keys))
	for i, k := range keys {
		resp[i] = apiKeyResponse{
			ID:         k.ID,
			Name:       k.Name,
			KeyPrefix:  k.KeyPrefix,
			ExpiresAt:  tsPtr(k.ExpiresAt),
			LastUsedAt: tsPtr(k.LastUsedAt),
			LastUsedIP: textPtr(k.LastUsedIp),
			IsRevoked:  k.IsRevoked,
			CreatedAt:  k.CreatedAt.Format(time.RFC3339),
		}
	}

	return c.JSON(resp)
}

// Revoke handles DELETE /api/v1/api-keys/:id.
func (h *APIKeyHandler) Revoke(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "api_key"); err != nil {
		return err
	}

	keyID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid API key ID")
	}

	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	// Fetch key and verify ownership.
	key, err := h.queries.GetAPIKeyByID(c.Context(), keyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "API key not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get API key")
	}
	if key.UserID != userID {
		return fiber.NewError(fiber.StatusForbidden, "Cannot revoke another user's API key")
	}

	if err := h.queries.RevokeAPIKey(c.Context(), keyID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revoke API key")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "api_key", keyID.String(), "api_key_revoked", nil)

	return c.JSON(fiber.Map{"message": "API key revoked"})
}

// RevokeAll handles DELETE /api/v1/api-keys.
func (h *APIKeyHandler) RevokeAll(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "api_key"); err != nil {
		return err
	}

	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	if err := h.queries.RevokeAllUserAPIKeys(c.Context(), userID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revoke API keys")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "api_key", userID.String(), "api_keys_revoked_all", nil)

	return c.JSON(fiber.Map{"message": "All API keys revoked"})
}

// AdminList handles GET /api/v1/admin/api-keys.
func (h *APIKeyHandler) AdminList(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	keys, err := h.queries.ListAllAPIKeys(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list API keys")
	}

	resp := make([]adminAPIKeyResponse, len(keys))
	for i, k := range keys {
		resp[i] = adminAPIKeyResponse{
			ID:              k.ID,
			UserID:          k.UserID,
			Name:            k.Name,
			KeyPrefix:       k.KeyPrefix,
			ExpiresAt:       tsPtr(k.ExpiresAt),
			LastUsedAt:      tsPtr(k.LastUsedAt),
			LastUsedIP:      textPtr(k.LastUsedIp),
			IsRevoked:       k.IsRevoked,
			CreatedAt:       k.CreatedAt.Format(time.RFC3339),
			UserEmail:       k.UserEmail,
			UserDisplayName: k.UserDisplayName,
		}
	}

	return c.JSON(resp)
}

// AdminRevoke handles DELETE /api/v1/admin/api-keys/:id.
func (h *APIKeyHandler) AdminRevoke(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	keyID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid API key ID")
	}

	// Verify key exists before revoking.
	key, err := h.queries.GetAPIKeyByID(c.Context(), keyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "API key not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get API key")
	}

	if err := h.queries.RevokeAPIKey(c.Context(), keyID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revoke API key")
	}

	details, _ := json.Marshal(map[string]string{
		"key_owner_id": key.UserID.String(),
		"key_name":     key.Name,
	})

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "api_key", keyID.String(), "api_key_revoked_by_admin", details)

	return c.JSON(fiber.Map{"message": "API key revoked by admin"})
}
