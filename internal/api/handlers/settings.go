package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// THREE of these nine routes are declared in
// internal/api/registry_settings.go — the two branding uploads and the delete.
// The other six stay in router.go, and that file's long comment is the reason,
// per route. The short version: the two generic READS perform no permission
// check on any path, the three branding reads are instance-shared, and the PUT
// carries a `value` that is arbitrary JSON, which apischema's Type vocabulary
// cannot describe.
//
// settingScopeID below is the conditional the whole decision turns on, and it
// is the live instance rbac_route_guard_test.go's LIMITATION note names: it is
// REACHABLE from the reads, which is what the call-graph guard checks, and it
// never runs on them.

// SettingsHandler handles application settings endpoints.
type SettingsHandler struct {
	queries  *db.Queries
	eventPub *events.Publisher
	dataDir  string // directory to store uploaded files (logos, favicons)
}

// NewSettingsHandler creates a new settings handler.
func NewSettingsHandler(queries *db.Queries, eventPub *events.Publisher, dataDir string) *SettingsHandler {
	return &SettingsHandler{queries: queries, eventPub: eventPub, dataDir: dataDir}
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
		CreatedAt: s.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: s.UpdatedAt.Format(time.RFC3339Nano),
	}
	if s.ScopeID.Valid {
		id := s.ScopeID.Bytes
		str := uuid.UUID(id).String()
		resp.ScopeID = &str
	}
	return resp
}

// settingScope describes how one scope string is handled: which row the
// setting keys on, and what a write to it requires.
type settingScope struct {
	// perUser keys the row on the caller's user_id. Otherwise scope_id is
	// NULL and the row is shared by every user.
	perUser bool
	// adminOnly gates writes (create/update/delete) on manage:settings.
	// Reads stay open — the SPA fetches global settings (branding, etc.)
	// for every signed-in user.
	adminOnly bool
}

// settingScopes is the authoritative set of scopes these endpoints accept.
//
// The settings table's CHECK constraint also permits 'cluster', but nothing
// reads or writes a cluster-scoped setting, and these endpoints take no
// cluster ID — so such a row would land on scope_id = NULL, i.e. a second
// shared namespace keyed only by name rather than per-cluster storage. It
// used to fall through the write gate entirely, letting any authenticated
// user create, overwrite or delete entries every other user reads back.
// Implementing it properly means plumbing a cluster ID through and gating
// on requireClusterPerm; until then the scope is rejected outright.
var settingScopes = map[string]settingScope{
	"global": {adminOnly: true},
	"user":   {perUser: true},
}

// SettingScopeKeys returns the accepted ?scope= values, sorted. Exported for
// the guard in package api that compares them against the Enum declared on the
// delete: they are two copies of one list, and a schema that accepted a scope
// this map has no entry for would fall through to the zero settingScope —
// perUser false, adminOnly false — i.e. a shared row nobody's permission gates.
// Package handlers cannot import package api, so the comparison reads this from
// the other side.
func SettingScopeKeys() []string { return slices.Sorted(maps.Keys(settingScopes)) }

// settingOwner is the dedicated endpoint that owns a reserved shared-scope
// setting key, and the permission that endpoint gates on.
//
// The permission is recorded, not just the path, because two places need it:
// the refusal below points the caller at the endpoint, and the audit log
// redacts entries about the key for anyone who could not have read the setting
// through it (see auditDetailsFor in audit.go). One table so those two can't
// disagree about who is allowed to see a key's value.
type settingOwner struct {
	Endpoint string
	Action   string
	Resource string
}

// String renders the owner for a refusal message: the endpoint and the
// permission it wants.
func (o settingOwner) String() string {
	return fmt.Sprintf("%s (%s:%s)", o.Endpoint, o.Action, o.Resource)
}

// reservedGlobalSettings maps a shared-scope setting key to the endpoint that
// owns it. These endpoints refuse the listed keys outright — reads and writes
// alike, for every caller including a manage:settings holder — because a
// dedicated handler already gates them on a narrower permission.
//
// Without this, the generic endpoints are a way around the owner's gate in both
// directions. Shared-scope reads here are deliberately ungated (the SPA fetches
// branding for every signed-in user), so an owned key would be readable by any
// authenticated account, Viewer included, via GET /settings/:key or dumped
// wholesale by GET /settings?scope=global. Writes gate on manage:settings, so
// holding that alone would be enough to overwrite a key whose own handler
// demands something else — for syslog forwarding, enough to redirect the audit
// stream at an attacker-controlled collector or disable it outright.
//
// The reservation binds to the shared row, not to the name: a "user" scope row
// of the same key is keyed on the caller's own user_id and readable by nobody
// else, so it stays allowed.
//
// Keys are referenced through the const their owner declares, so renaming one
// there cannot silently unreserve it. TestGuard_GlobalSettingKeysClassified
// fails if a new global key appears in either map.
var reservedGlobalSettings = map[string]settingOwner{
	syslogSettingKey: {Endpoint: "/api/v1/audit-log/syslog-config", Action: "manage", Resource: "audit"},
	// Not a secret — nothing authenticates to the mirror — but its own endpoint
	// validates the URL (scheme, credentials, SSRF address policy, and the two
	// explicit confirmations for a private address and for plain http). Left
	// unreserved, a manage:settings holder could write an unvalidated value
	// through the generic PUT and every Proxmox node would fetch from it.
	virtioWinMirrorSettingKey: {Endpoint: "/api/v1/virtio-win/mirror", Action: "manage", Resource: "settings"},
}

// reservedSettingOwner returns the endpoint that owns key under scope; ok is
// false when the generic endpoints may handle it. Per-user scopes are never
// reserved: the row is keyed on the caller's own user_id, so nobody else can
// read it back.
//
// An unrecognised scope classifies as shared, not exempt — the zero settingScope
// has perUser false. Every caller validates the scope first, so that branch is
// unreachable today; it is written this way so the one function the reservation
// rests on fails closed if a caller ever forgets.
func reservedSettingOwner(scope, key string) (settingOwner, bool) {
	if settingScopes[scope].perUser {
		return settingOwner{}, false
	}
	owner, ok := reservedGlobalSettings[key]
	return owner, ok
}

// settingScopeID validates the requested scope, rejects keys owned by a
// dedicated endpoint, enforces the write gate when write is true, and returns
// the scope_id the row is keyed on. Every settings handler routes its scope
// handling through here so the read and write paths can't drift apart on which
// scopes exist, which keys are off-limits, or who may touch them.
//
// key is "" for the bulk listing, which has no single key — ListSettings drops
// reserved rows from its result instead.
func settingScopeID(c fiber.Ctx, key, scope string, write bool) (pgtype.UUID, error) {
	sc, ok := settingScopes[scope]
	if !ok {
		return pgtype.UUID{}, fiber.NewError(fiber.StatusBadRequest, "Invalid scope: must be global or user")
	}

	// Ahead of the write gate: manage:settings is not a way in either, so the
	// answer is the same for every caller and naming the owner is more useful
	// than a bare permission denial.
	if owner, reserved := reservedSettingOwner(scope, key); reserved {
		return pgtype.UUID{}, fiber.NewError(fiber.StatusForbidden,
			"Setting '"+key+"' is managed by "+owner.String()+" — use that endpoint")
	}

	if write && sc.adminOnly {
		if err := requirePerm(c, "manage", "settings"); err != nil {
			return pgtype.UUID{}, err
		}
	}

	if !sc.perUser {
		return pgtype.UUID{}, nil
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return pgtype.UUID{}, fiber.NewError(fiber.StatusUnauthorized, "Not authenticated")
	}
	return pgtype.UUID{Bytes: userID, Valid: true}, nil
}

// settingResourceType is the audit_log.resource_type these endpoints record
// under; resource_id is the setting key.
const settingResourceType = "setting"

// maxSettingKeyLen bounds the :key path parameter. settings.key is TEXT, so
// this cap is the application's alone — there is no database constraint to
// fall back on.
const maxSettingKeyLen = 128

// settingKeyFromPath reads and validates the :key path parameter, so every
// keyed endpoint applies one rule to it — the same reason scope and reserved
// keys route through settingScopeID.
//
// The bound used to live in UpsertSetting only. That made DeleteSetting answer
// 204 for a key no setting could ever hold, and now that deletes are audited it
// would also write that unbounded caller-supplied string into two columns of
// the audit row, on a request guaranteed to match nothing.
func settingKeyFromPath(c fiber.Ctx) (string, error) {
	key := c.Params("key")
	if key == "" {
		return "", fiber.NewError(fiber.StatusBadRequest, "Key is required")
	}
	if len(key) > maxSettingKeyLen {
		return "", fiber.NewError(fiber.StatusBadRequest,
			fmt.Sprintf("Key too long (max %d characters)", maxSettingKeyLen))
	}
	return key, nil
}

// settingAuditDetails is everything a settings mutation records.
//
// There is deliberately no value field. A setting value is up to 64KB and can
// hold sensitive configuration — syslog_forwarding carries the audit
// destination, and future keys may carry worse — so copying it into the audit
// log would duplicate the secret into a table with a wider read audience than
// the settings row itself. Key and scope identify which row moved; the current
// value is always readable from `settings`. A struct rather than a map so the
// value cannot be slipped in by a later caller.
type settingAuditDetails struct {
	Key   string `json:"key"`
	Scope string `json:"scope"`
	// Upload-only. Filename is the client-supplied name (bounded by
	// auditFilename), StoredAs the name it was written under on disk.
	Filename  string `json:"filename,omitempty"`
	StoredAs  string `json:"stored_as,omitempty"`
	SizeBytes int    `json:"size_bytes,omitempty"`
}

// auditSettingWrite records a settings mutation — but only for the shared
// scopes, and that exclusion is the deliberate part.
//
// A per-user setting is keyed on the caller's own user_id: nobody else reads
// it, nobody else can write it, and changing it alters nothing outside that
// one account's UI. Meanwhile it is by far the highest-volume write here — the
// dashboard persists its layout on every drag — so auditing it would bury the
// entries that matter under a stream that says nothing about who changed the
// application. Shared-scope writes are the opposite on both counts: one row
// every user reads, gated on manage:settings, and rare.
//
// The test is settingScopes[scope].perUser rather than scope == "global", so a
// second shared scope added to that map is audited the day it lands. It also
// fails closed — an unrecognised scope has the zero settingScope, perUser
// false, and so gets audited. Every caller validates the scope through
// settingScopeID first, so that branch is unreachable today.
func (h *SettingsHandler) auditSettingWrite(c fiber.Ctx, action string, d settingAuditDetails) {
	if settingScopes[d.Scope].perUser {
		return
	}
	details, _ := json.Marshal(d) // strings and an int — cannot fail
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, settingResourceType, d.Key, action, details)
}

// maxAuditFilenameLen bounds a client-supplied upload filename in an audit
// detail: longer than any filename a real upload carries, short enough that a
// padded one cannot bulk out the row.
const maxAuditFilenameLen = 128

// auditFilename bounds a client-supplied upload filename before it lands in an
// audit detail.
func auditFilename(name string) string {
	return auditTruncate(name, maxAuditFilenameLen)
}

// ListSettings returns settings filtered by scope.
// GET /api/v1/settings?scope=global|user
func (h *SettingsHandler) ListSettings(c fiber.Ctx) error {
	scope := c.Query("scope", "global")

	scopeID, err := settingScopeID(c, "", scope, false)
	if err != nil {
		return err
	}

	settings, err := h.queries.ListSettingsByScope(c.Context(), db.ListSettingsByScopeParams{
		Scope:   scope,
		ScopeID: scopeID,
	})
	if err != nil {
		return fmt.Errorf("list settings: %w", err)
	}

	result := make([]settingResponse, 0, len(settings))
	for _, s := range settings {
		// Fetching a reserved key by name is refused, so returning it in the
		// bulk dump would reopen the same hole. Filtered rather than refused
		// wholesale: one owned row shouldn't take the whole listing down.
		if _, reserved := reservedSettingOwner(scope, s.Key); reserved {
			continue
		}
		result = append(result, toSettingResponse(s))
	}
	return RespondItems(c, result)
}

// GetSetting returns a single setting by key.
// GET /api/v1/settings/:key?scope=global|user
func (h *SettingsHandler) GetSetting(c fiber.Ctx) error {
	key, err := settingKeyFromPath(c)
	if err != nil {
		return err
	}

	scope := c.Query("scope", "user")

	scopeID, err := settingScopeID(c, key, scope, false)
	if err != nil {
		return err
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
func (h *SettingsHandler) UpsertSetting(c fiber.Ctx) error {
	key, err := settingKeyFromPath(c)
	if err != nil {
		return err
	}

	var req upsertSettingRequest
	if err := c.Bind().Body(&req); err != nil {
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

	scopeID, err := settingScopeID(c, key, scope, true)
	if err != nil {
		return err
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

	h.auditSettingWrite(c, "setting_updated", settingAuditDetails{Key: key, Scope: scope})

	return c.JSON(toSettingResponse(setting))
}

// DeleteSetting deletes a setting by key.
// DELETE /api/v1/settings/:key?scope=global|user
func (h *SettingsHandler) DeleteSetting(c fiber.Ctx, p *apischema.Params) error {
	key := p.String("key")
	scope := p.String("scope")

	// Still routed through settingScopeID: the declaration says the permission
	// is Deferred, and this is where it is decided. It is also what refuses a
	// key owned by a dedicated endpoint, which no schema can express — the
	// reservation is a property of the KEY, not of its shape.
	scopeID, err := settingScopeID(c, key, scope, true)
	if err != nil {
		return err
	}

	if err := h.queries.DeleteSetting(c.Context(), db.DeleteSettingParams{
		Key:     key,
		Scope:   scope,
		ScopeID: scopeID,
	}); err != nil {
		return fmt.Errorf("delete setting: %w", err)
	}

	// Recorded whether or not a row existed: the DELETE is unconditional and
	// the endpoint answers 204 either way, so the request is all there is to
	// audit. An attempt on an absent key is worth seeing.
	h.auditSettingWrite(c, "setting_deleted", settingAuditDetails{Key: key, Scope: scope})

	return c.SendStatus(fiber.StatusNoContent)
}

// The global setting keys the upload endpoints write. Named consts because
// each one is used twice — once for the row itself, once for the audit detail
// — and only the settings write is covered by
// TestGuard_GlobalSettingKeysClassified, so a literal changed in one place and
// not the other would produce audit rows naming a key that does not exist.
const (
	brandingLogoKey    = "branding.logo_url"
	brandingFaviconKey = "branding.favicon_url"
)

// UploadLogo handles logo file upload for branding.
// POST /api/v1/settings/branding/logo
func (h *SettingsHandler) UploadLogo(c fiber.Ctx, _ *apischema.Params) error {
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
	if int64(len(content)) > 2*1024*1024 {
		return fiber.NewError(fiber.StatusBadRequest, "Logo file too large (max 2MB)")
	}

	if err := validateImageUpload(content, ext); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid image: "+err.Error())
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
		Key:   brandingLogoKey,
		Value: valueJSON,
		Scope: "global",
	}); err != nil {
		return fmt.Errorf("save logo setting: %w", err)
	}

	h.auditSettingWrite(c, "branding_logo_uploaded", settingAuditDetails{
		Key:       brandingLogoKey,
		Scope:     "global",
		Filename:  auditFilename(file.Filename),
		StoredAs:  filename,
		SizeBytes: len(content),
	})

	return c.JSON(fiber.Map{"logo_url": logoURL, "filename": filename})
}

// ServeLogo serves the uploaded logo file.
// GET /api/v1/settings/branding/logo-file
func (h *SettingsHandler) ServeLogo(c fiber.Ctx) error {
	return h.serveBrandingFile(c, "logo")
}

// UploadFavicon handles favicon file upload for branding.
// POST /api/v1/settings/branding/favicon
func (h *SettingsHandler) UploadFavicon(c fiber.Ctx, _ *apischema.Params) error {
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
	if int64(len(content)) > 512*1024 {
		return fiber.NewError(fiber.StatusBadRequest, "Favicon file too large (max 512KB)")
	}

	if err := validateImageUpload(content, ext); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid image: "+err.Error())
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
		Key:   brandingFaviconKey,
		Value: valueJSON,
		Scope: "global",
	}); err != nil {
		return fmt.Errorf("save favicon setting: %w", err)
	}

	h.auditSettingWrite(c, "branding_favicon_uploaded", settingAuditDetails{
		Key:       brandingFaviconKey,
		Scope:     "global",
		Filename:  auditFilename(file.Filename),
		StoredAs:  filename,
		SizeBytes: len(content),
	})

	return c.JSON(fiber.Map{"favicon_url": faviconURL, "filename": filename})
}

// ServeFavicon serves the uploaded favicon file.
// GET /api/v1/settings/branding/favicon-file
func (h *SettingsHandler) ServeFavicon(c fiber.Ctx) error {
	return h.serveBrandingFile(c, "favicon")
}

// serveBrandingFile serves an uploaded branding asset (logo or favicon) with
// hardened response headers: explicit Content-Type, X-Content-Type-Options
// nosniff, and a default-src 'none' CSP that neutralises any script that
// slipped past the upload validator. This is the second layer of defence —
// the upload validator is the first.
func (h *SettingsHandler) serveBrandingFile(c fiber.Ctx, prefix string) error {
	brandingDir := filepath.Join(h.dataDir, "branding")
	entries, err := os.ReadDir(brandingDir)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "No "+prefix+" uploaded")
	}

	var match os.DirEntry
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix+".") {
			match = entry
			break
		}
	}
	if match == nil {
		return fiber.NewError(fiber.StatusNotFound, "No "+prefix+" uploaded")
	}

	// Defence-in-depth: filepath.Base strips any path components ReadDir
	// shouldn't have produced anyway, and the post-Clean prefix check ensures
	// the resolved path stays inside brandingDir.
	safeName := filepath.Base(match.Name())
	joined := filepath.Join(brandingDir, safeName)
	cleanDir := filepath.Clean(brandingDir) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(joined), cleanDir) {
		return fiber.NewError(fiber.StatusInternalServerError, "Branding asset path escaped data directory")
	}

	ext := strings.ToLower(filepath.Ext(safeName))
	if ct, ok := brandingContentType(ext); ok {
		c.Set(fiber.HeaderContentType, ct)
	}
	c.Set("X-Content-Type-Options", "nosniff")
	// Block any script execution and external resource loading even if a
	// malicious SVG slipped past validation. style-src 'unsafe-inline' is
	// retained because legitimate SVGs carry inline <style> for presentation.
	// Bare `sandbox` is the most restrictive sandbox per CSP3 — blocks scripts,
	// forms, top-level navigation, plugins, and same-origin treatment when the
	// asset is loaded as a top-level document.
	c.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox")
	c.Set("Cross-Origin-Resource-Policy", "same-origin")
	// Force download for SVG when navigated directly. <img>, <link rel="icon">,
	// and CSS background-image ignore Content-Disposition, so the BrandingPage
	// preview, sidebar logo, and favicon link still render normally — but
	// pasting the URL in the address bar produces a download instead of an
	// inline render in the Nexara same-origin context.
	if ext == ".svg" {
		c.Set("Content-Disposition", `attachment; filename="`+prefix+`.svg"`)
	}
	return c.SendFile(joined)
}

func brandingContentType(ext string) (string, bool) {
	switch ext {
	case ".svg":
		return "image/svg+xml; charset=utf-8", true
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".webp":
		return "image/webp", true
	case ".ico":
		return "image/x-icon", true
	}
	return "", false
}

// GetBranding returns all global branding settings (public, no auth for logo/favicon serving).
// GET /api/v1/settings/branding
func (h *SettingsHandler) GetBranding(c fiber.Ctx) error {
	settings, err := h.queries.ListGlobalSettings(c.Context())
	if err != nil {
		return fmt.Errorf("list global settings: %w", err)
	}

	branding := make(map[string]json.RawMessage)
	for _, s := range settings {
		// The prefix filter is what keeps this ungated read to branding, but
		// it's a naming convention, not a gate — an owned key that ever adopted
		// the prefix would ride straight out. Reserved keys are dropped here
		// too so every path over the shared rows answers to one rule.
		if _, reserved := reservedSettingOwner("global", s.Key); reserved {
			continue
		}
		if strings.HasPrefix(s.Key, "branding.") {
			branding[s.Key] = s.Value
		}
	}

	return c.JSON(branding)
}
