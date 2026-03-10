package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// SettingsHandler handles application settings endpoints.
type SettingsHandler struct {
	queries  *db.Queries
	dataDir  string // directory to store uploaded files (logos, favicons)
}

// NewSettingsHandler creates a new settings handler.
func NewSettingsHandler(queries *db.Queries, dataDir string) *SettingsHandler {
	return &SettingsHandler{queries: queries, dataDir: dataDir}
}

type settingResponse struct {
	ID        uuid.UUID       `json:"id"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Scope     string          `json:"scope"`
	ScopeID   *string         `json:"scope_id,omitempty"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

func toSettingResponse(s db.Setting) settingResponse {
	resp := settingResponse{
		ID:        s.ID,
		Key:       s.Key,
		Value:     s.Value,
		Scope:     s.Scope,
		CreatedAt: s.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: s.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if s.ScopeID.Valid {
		id := s.ScopeID.Bytes
		str := uuid.UUID(id).String()
		resp.ScopeID = &str
	}
	return resp
}

// ListSettings returns settings filtered by scope.
// GET /api/v1/settings?scope=global|user
func (h *SettingsHandler) ListSettings(c *fiber.Ctx) error {
	scope := c.Query("scope", "global")
	if scope != "global" && scope != "user" && scope != "cluster" {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid scope: must be global, user, or cluster")
	}

	var scopeID pgtype.UUID
	switch scope {
	case "user":
		userID, _ := c.Locals("user_id").(uuid.UUID)
		if userID == uuid.Nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Not authenticated")
		}
		scopeID = pgtype.UUID{Bytes: userID, Valid: true}
	case "global":
		// All authenticated users can read global settings
	}

	settings, err := h.queries.ListSettingsByScope(c.Context(), db.ListSettingsByScopeParams{
		Scope:   scope,
		ScopeID: scopeID,
	})
	if err != nil {
		return fmt.Errorf("list settings: %w", err)
	}

	result := make([]settingResponse, len(settings))
	for i, s := range settings {
		result[i] = toSettingResponse(s)
	}
	return c.JSON(result)
}

// GetSetting returns a single setting by key.
// GET /api/v1/settings/:key?scope=global|user
func (h *SettingsHandler) GetSetting(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Key is required")
	}

	scope := c.Query("scope", "user")
	var scopeID pgtype.UUID

	if scope == "user" {
		userID, _ := c.Locals("user_id").(uuid.UUID)
		if userID == uuid.Nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Not authenticated")
		}
		scopeID = pgtype.UUID{Bytes: userID, Valid: true}
	}

	setting, err := h.queries.GetSetting(c.Context(), db.GetSettingParams{
		Key:     key,
		Scope:   scope,
		ScopeID: scopeID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Setting not found")
	}

	return c.JSON(toSettingResponse(setting))
}

type upsertSettingRequest struct {
	Value json.RawMessage `json:"value"`
	Scope string          `json:"scope"`
}

// UpsertSetting creates or updates a setting.
// PUT /api/v1/settings/:key
func (h *SettingsHandler) UpsertSetting(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Key is required")
	}

	// Validate key length
	if len(key) > 128 {
		return fiber.NewError(fiber.StatusBadRequest, "Key too long (max 128 characters)")
	}

	var req upsertSettingRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if len(req.Value) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "Value is required")
	}

	// Validate JSON
	if !json.Valid(req.Value) {
		return fiber.NewError(fiber.StatusBadRequest, "Value must be valid JSON")
	}

	// Limit value size
	if len(req.Value) > 65536 {
		return fiber.NewError(fiber.StatusBadRequest, "Value too large (max 64KB)")
	}

	scope := req.Scope
	if scope == "" {
		scope = "user"
	}
	if scope != "global" && scope != "user" && scope != "cluster" {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid scope")
	}

	// Global settings require admin permission
	if scope == "global" {
		if err := requirePerm(c, "manage", "settings"); err != nil {
			return err
		}
	}

	var scopeID pgtype.UUID
	if scope == "user" {
		userID, _ := c.Locals("user_id").(uuid.UUID)
		if userID == uuid.Nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Not authenticated")
		}
		scopeID = pgtype.UUID{Bytes: userID, Valid: true}
	}

	setting, err := h.queries.UpsertSetting(c.Context(), db.UpsertSettingParams{
		Key:     key,
		Value:   req.Value,
		Scope:   scope,
		ScopeID: scopeID,
	})
	if err != nil {
		return fmt.Errorf("upsert setting: %w", err)
	}

	return c.JSON(toSettingResponse(setting))
}

// DeleteSetting deletes a setting by key.
// DELETE /api/v1/settings/:key?scope=global|user
func (h *SettingsHandler) DeleteSetting(c *fiber.Ctx) error {
	key := c.Params("key")
	if key == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Key is required")
	}

	scope := c.Query("scope", "user")

	// Global settings require admin permission
	if scope == "global" {
		if err := requirePerm(c, "manage", "settings"); err != nil {
			return err
		}
	}

	var scopeID pgtype.UUID
	if scope == "user" {
		userID, _ := c.Locals("user_id").(uuid.UUID)
		if userID == uuid.Nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Not authenticated")
		}
		scopeID = pgtype.UUID{Bytes: userID, Valid: true}
	}

	if err := h.queries.DeleteSetting(c.Context(), db.DeleteSettingParams{
		Key:     key,
		Scope:   scope,
		ScopeID: scopeID,
	}); err != nil {
		return fmt.Errorf("delete setting: %w", err)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// UploadLogo handles logo file upload for branding.
// POST /api/v1/settings/branding/logo
func (h *SettingsHandler) UploadLogo(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "settings"); err != nil {
		return err
	}

	file, err := c.FormFile("logo")
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Logo file is required")
	}

	// Validate file size (max 2MB)
	if file.Size > 2*1024*1024 {
		return fiber.NewError(fiber.StatusBadRequest, "Logo file too large (max 2MB)")
	}

	// Validate content type
	ct := file.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		return fiber.NewError(fiber.StatusBadRequest, "File must be an image")
	}

	// Validate extension
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".svg" && ext != ".webp" && ext != ".ico" {
		return fiber.NewError(fiber.StatusBadRequest, "Unsupported image format")
	}

	// Read file content to validate it's actually an image
	src, err := file.Open()
	if err != nil {
		return fmt.Errorf("open uploaded file: %w", err)
	}
	defer func() { _ = src.Close() }()

	content, err := io.ReadAll(io.LimitReader(src, 2*1024*1024+1))
	if err != nil {
		return fmt.Errorf("read uploaded file: %w", err)
	}

	// Ensure data directory exists
	brandingDir := filepath.Join(h.dataDir, "branding")
	if err := os.MkdirAll(brandingDir, 0o750); err != nil {
		return fmt.Errorf("create branding directory: %w", err)
	}

	// Save file with a fixed name so it's easy to serve
	filename := "logo" + ext
	destPath := filepath.Join(brandingDir, filename)

	// Clean up old logos with different extensions
	entries, _ := os.ReadDir(brandingDir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "logo.") {
			_ = os.Remove(filepath.Join(brandingDir, entry.Name()))
		}
	}

	if err := os.WriteFile(destPath, content, 0o600); err != nil {
		return fmt.Errorf("write logo file: %w", err)
	}

	// Store the logo path in global settings
	logoURL := "/api/v1/settings/branding/logo-file"
	valueJSON, _ := json.Marshal(logoURL)
	if _, err := h.queries.UpsertSetting(c.Context(), db.UpsertSettingParams{
		Key:   "branding.logo_url",
		Value: valueJSON,
		Scope: "global",
	}); err != nil {
		return fmt.Errorf("save logo setting: %w", err)
	}

	return c.JSON(fiber.Map{"logo_url": logoURL, "filename": filename})
}

// ServeLogo serves the uploaded logo file.
// GET /api/v1/settings/branding/logo-file
func (h *SettingsHandler) ServeLogo(c *fiber.Ctx) error {
	brandingDir := filepath.Join(h.dataDir, "branding")
	entries, err := os.ReadDir(brandingDir)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "No logo uploaded")
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "logo.") {
			return c.SendFile(filepath.Join(brandingDir, entry.Name()))
		}
	}

	return fiber.NewError(fiber.StatusNotFound, "No logo uploaded")
}

// UploadFavicon handles favicon file upload for branding.
// POST /api/v1/settings/branding/favicon
func (h *SettingsHandler) UploadFavicon(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "settings"); err != nil {
		return err
	}

	file, err := c.FormFile("favicon")
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Favicon file is required")
	}

	// Validate file size (max 512KB)
	if file.Size > 512*1024 {
		return fiber.NewError(fiber.StatusBadRequest, "Favicon file too large (max 512KB)")
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext != ".ico" && ext != ".png" && ext != ".svg" {
		return fiber.NewError(fiber.StatusBadRequest, "Favicon must be .ico, .png, or .svg")
	}

	src, err := file.Open()
	if err != nil {
		return fmt.Errorf("open uploaded file: %w", err)
	}
	defer func() { _ = src.Close() }()

	content, err := io.ReadAll(io.LimitReader(src, 512*1024+1))
	if err != nil {
		return fmt.Errorf("read uploaded file: %w", err)
	}

	brandingDir := filepath.Join(h.dataDir, "branding")
	if err := os.MkdirAll(brandingDir, 0o750); err != nil {
		return fmt.Errorf("create branding directory: %w", err)
	}

	filename := "favicon" + ext
	destPath := filepath.Join(brandingDir, filename)

	// Clean up old favicons
	entries, _ := os.ReadDir(brandingDir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "favicon.") {
			_ = os.Remove(filepath.Join(brandingDir, entry.Name()))
		}
	}

	if err := os.WriteFile(destPath, content, 0o600); err != nil {
		return fmt.Errorf("write favicon file: %w", err)
	}

	faviconURL := "/api/v1/settings/branding/favicon-file"
	valueJSON, _ := json.Marshal(faviconURL)
	if _, err := h.queries.UpsertSetting(c.Context(), db.UpsertSettingParams{
		Key:   "branding.favicon_url",
		Value: valueJSON,
		Scope: "global",
	}); err != nil {
		return fmt.Errorf("save favicon setting: %w", err)
	}

	return c.JSON(fiber.Map{"favicon_url": faviconURL, "filename": filename})
}

// ServeFavicon serves the uploaded favicon file.
// GET /api/v1/settings/branding/favicon-file
func (h *SettingsHandler) ServeFavicon(c *fiber.Ctx) error {
	brandingDir := filepath.Join(h.dataDir, "branding")
	entries, err := os.ReadDir(brandingDir)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "No favicon uploaded")
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "favicon.") {
			return c.SendFile(filepath.Join(brandingDir, entry.Name()))
		}
	}

	return fiber.NewError(fiber.StatusNotFound, "No favicon uploaded")
}

// GetBranding returns all global branding settings (public, no auth for logo/favicon serving).
// GET /api/v1/settings/branding
func (h *SettingsHandler) GetBranding(c *fiber.Ctx) error {
	settings, err := h.queries.ListGlobalSettings(c.Context())
	if err != nil {
		return fmt.Errorf("list global settings: %w", err)
	}

	branding := make(map[string]json.RawMessage)
	for _, s := range settings {
		if strings.HasPrefix(s.Key, "branding.") {
			branding[s.Key] = s.Value
		}
	}

	return c.JSON(branding)
}
