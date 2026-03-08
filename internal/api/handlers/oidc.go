package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/proxdash/proxdash/internal/auth"
	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
)

// OIDCHandler handles OIDC/SSO configuration and auth flow endpoints.
type OIDCHandler struct {
	queries       *db.Queries
	encryptionKey string
	rbac          *auth.RBACEngine
	eventPub      *events.Publisher
	rdb           *redis.Client
}

// NewOIDCHandler creates a new OIDC handler.
func NewOIDCHandler(queries *db.Queries, encryptionKey string, rbac *auth.RBACEngine, eventPub *events.Publisher, rdb *redis.Client) *OIDCHandler {
	return &OIDCHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		rbac:          rbac,
		eventPub:      eventPub,
		rdb:           rdb,
	}
}

// --- Request/Response types ---

type oidcConfigRequest struct {
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled"`
	IssuerURL        string            `json:"issuer_url"`
	ClientID         string            `json:"client_id"`
	ClientSecret     string            `json:"client_secret"`
	RedirectURI      string            `json:"redirect_uri"`
	Scopes           []string          `json:"scopes"`
	EmailClaim       string            `json:"email_claim"`
	DisplayNameClaim string            `json:"display_name_claim"`
	GroupsClaim      string            `json:"groups_claim"`
	GroupRoleMapping  map[string]string `json:"group_role_mapping"`
	DefaultRoleID    *string           `json:"default_role_id"`
	AutoProvision    bool              `json:"auto_provision"`
	AllowedDomains   []string          `json:"allowed_domains"`
}

type oidcConfigResponse struct {
	ID               uuid.UUID         `json:"id"`
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled"`
	IssuerURL        string            `json:"issuer_url"`
	ClientID         string            `json:"client_id"`
	ClientSecretSet  bool              `json:"client_secret_set"`
	RedirectURI      string            `json:"redirect_uri"`
	Scopes           []string          `json:"scopes"`
	EmailClaim       string            `json:"email_claim"`
	DisplayNameClaim string            `json:"display_name_claim"`
	GroupsClaim      string            `json:"groups_claim"`
	GroupRoleMapping  map[string]string `json:"group_role_mapping"`
	DefaultRoleID    *string           `json:"default_role_id"`
	AutoProvision    bool              `json:"auto_provision"`
	AllowedDomains   []string          `json:"allowed_domains"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`
}

func toOIDCConfigResponse(cfg db.OidcConfig) oidcConfigResponse {
	resp := oidcConfigResponse{
		ID:               cfg.ID,
		Name:             cfg.Name,
		Enabled:          cfg.Enabled,
		IssuerURL:        cfg.IssuerUrl,
		ClientID:         cfg.ClientID,
		ClientSecretSet:  cfg.ClientSecretEncrypted != "",
		RedirectURI:      cfg.RedirectUri,
		Scopes:           cfg.Scopes,
		EmailClaim:       cfg.EmailClaim,
		DisplayNameClaim: cfg.DisplayNameClaim,
		GroupsClaim:      cfg.GroupsClaim,
		AutoProvision:    cfg.AutoProvision,
		AllowedDomains:   cfg.AllowedDomains,
		CreatedAt:        cfg.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:        cfg.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	mapping := make(map[string]string)
	if len(cfg.GroupRoleMapping) > 0 {
		_ = json.Unmarshal(cfg.GroupRoleMapping, &mapping)
	}
	resp.GroupRoleMapping = mapping

	if cfg.DefaultRoleID.Valid {
		id, _ := uuid.FromBytes(cfg.DefaultRoleID.Bytes[:])
		s := id.String()
		resp.DefaultRoleID = &s
	}

	// Ensure non-nil slices for JSON
	if resp.Scopes == nil {
		resp.Scopes = []string{}
	}
	if resp.AllowedDomains == nil {
		resp.AllowedDomains = []string{}
	}

	return resp
}

// --- Admin CRUD endpoints ---

// List handles GET /api/v1/oidc/configs.
func (h *OIDCHandler) List(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	configs, err := h.queries.ListOIDCConfigs(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list OIDC configs")
	}

	resp := make([]oidcConfigResponse, len(configs))
	for i, cfg := range configs {
		resp[i] = toOIDCConfigResponse(cfg)
	}

	return c.JSON(resp)
}

// Get handles GET /api/v1/oidc/configs/:id.
func (h *OIDCHandler) Get(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid config ID")
	}

	cfg, err := h.queries.GetOIDCConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "OIDC config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get OIDC config")
	}

	return c.JSON(toOIDCConfigResponse(cfg))
}

// Create handles POST /api/v1/oidc/configs.
func (h *OIDCHandler) Create(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	var req oidcConfigRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.IssuerURL == "" || req.ClientID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "issuer_url and client_id are required")
	}

	if err := validateOIDCIssuerURL(req.IssuerURL); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	encSecret := ""
	if req.ClientSecret != "" {
		var err error
		encSecret, err = crypto.Encrypt(req.ClientSecret, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt client secret")
		}
	}

	mappingJSON, _ := json.Marshal(req.GroupRoleMapping)

	var defaultRoleID pgtype.UUID
	if req.DefaultRoleID != nil && *req.DefaultRoleID != "" {
		parsed, err := uuid.Parse(*req.DefaultRoleID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid default_role_id")
		}
		defaultRoleID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}

	allowedDomains := req.AllowedDomains
	if allowedDomains == nil {
		allowedDomains = []string{}
	}

	cfg, err := h.queries.CreateOIDCConfig(c.Context(), db.CreateOIDCConfigParams{
		Name:                  withDefault(req.Name, "Default"),
		Enabled:               req.Enabled,
		IssuerUrl:             req.IssuerURL,
		ClientID:              req.ClientID,
		ClientSecretEncrypted: encSecret,
		RedirectUri:           req.RedirectURI,
		Scopes:                scopes,
		EmailClaim:            withDefault(req.EmailClaim, "email"),
		DisplayNameClaim:      withDefault(req.DisplayNameClaim, "name"),
		GroupsClaim:           withDefault(req.GroupsClaim, "groups"),
		GroupRoleMapping:      mappingJSON,
		DefaultRoleID:         defaultRoleID,
		AutoProvision:         req.AutoProvision,
		AllowedDomains:        allowedDomains,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create OIDC config")
	}

	details, _ := json.Marshal(map[string]string{"name": cfg.Name})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "oidc", cfg.ID.String(), "oidc_config_created", details)

	return c.Status(fiber.StatusCreated).JSON(toOIDCConfigResponse(cfg))
}

// Update handles PUT /api/v1/oidc/configs/:id.
func (h *OIDCHandler) Update(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid config ID")
	}

	existing, err := h.queries.GetOIDCConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "OIDC config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get OIDC config")
	}

	var req oidcConfigRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.IssuerURL == "" || req.ClientID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "issuer_url and client_id are required")
	}

	if err := validateOIDCIssuerURL(req.IssuerURL); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// If secret is provided, encrypt it; otherwise keep existing
	encSecret := existing.ClientSecretEncrypted
	if req.ClientSecret != "" {
		encSecret, err = crypto.Encrypt(req.ClientSecret, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt client secret")
		}
	}

	mappingJSON, _ := json.Marshal(req.GroupRoleMapping)

	var defaultRoleID pgtype.UUID
	if req.DefaultRoleID != nil && *req.DefaultRoleID != "" {
		parsed, err := uuid.Parse(*req.DefaultRoleID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid default_role_id")
		}
		defaultRoleID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}

	allowedDomains := req.AllowedDomains
	if allowedDomains == nil {
		allowedDomains = []string{}
	}

	cfg, err := h.queries.UpdateOIDCConfig(c.Context(), db.UpdateOIDCConfigParams{
		ID:                    id,
		Name:                  withDefault(req.Name, "Default"),
		Enabled:               req.Enabled,
		IssuerUrl:             req.IssuerURL,
		ClientID:              req.ClientID,
		ClientSecretEncrypted: encSecret,
		RedirectUri:           req.RedirectURI,
		Scopes:                scopes,
		EmailClaim:            withDefault(req.EmailClaim, "email"),
		DisplayNameClaim:      withDefault(req.DisplayNameClaim, "name"),
		GroupsClaim:           withDefault(req.GroupsClaim, "groups"),
		GroupRoleMapping:      mappingJSON,
		DefaultRoleID:         defaultRoleID,
		AutoProvision:         req.AutoProvision,
		AllowedDomains:        allowedDomains,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update OIDC config")
	}

	details, _ := json.Marshal(map[string]string{"name": cfg.Name})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "oidc", cfg.ID.String(), "oidc_config_updated", details)

	return c.JSON(toOIDCConfigResponse(cfg))
}

// Delete handles DELETE /api/v1/oidc/configs/:id.
func (h *OIDCHandler) Delete(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid config ID")
	}

	if err := h.queries.DeleteOIDCConfig(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete OIDC config")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "oidc", id.String(), "oidc_config_deleted", nil)

	return c.SendStatus(fiber.StatusNoContent)
}

// TestConnection handles POST /api/v1/oidc/configs/:id/test.
func (h *OIDCHandler) TestConnection(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid config ID")
	}

	cfg, err := h.queries.GetOIDCConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "OIDC config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get OIDC config")
	}

	// Re-validate issuer URL before making the request
	if err := validateOIDCIssuerURL(cfg.IssuerUrl); err != nil {
		return c.JSON(testConnectionResponse{
			Success: false,
			Message: "Issuer URL validation failed: " + err.Error(),
		})
	}

	// Try to fetch the OIDC discovery document (no redirects)
	discoveryURL := strings.TrimRight(cfg.IssuerUrl, "/") + "/.well-known/openid-configuration"
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(discoveryURL) //nolint:gosec // URL comes from admin-configured issuer
	if err != nil {
		slog.Error("OIDC test connection failed", "config_id", id, "error", err)
		return c.JSON(testConnectionResponse{
			Success: false,
			Message: "Failed to reach OIDC discovery endpoint. Check the issuer URL and network connectivity.",
		})
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return c.JSON(testConnectionResponse{
			Success: false,
			Message: fmt.Sprintf("OIDC discovery endpoint returned HTTP %d", resp.StatusCode),
		})
	}

	return c.JSON(testConnectionResponse{
		Success: true,
		Message: "OIDC discovery endpoint is reachable and returned a valid response",
	})
}

// --- Auth flow endpoints (public, no auth required) ---

// Authorize handles GET /api/v1/auth/oidc/authorize.
// Returns the IdP redirect URL with state, nonce, PKCE.
func (h *OIDCHandler) Authorize(c *fiber.Ctx) error {
	cfg, err := h.queries.GetEnabledOIDCConfig(c.Context())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "No enabled OIDC configuration")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to load OIDC config")
	}

	oidcCfg, err := h.buildOIDCConfig(cfg)
	if err != nil {
		slog.Error("Failed to build OIDC config", "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to initialize OIDC configuration")
	}

	provider, err := auth.NewOIDCProvider(c.Context(), oidcCfg, h.rdb)
	if err != nil {
		slog.Error("OIDC provider init failed", "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to initialize OIDC provider")
	}

	authURL, err := provider.GenerateAuthURL(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate auth URL")
	}

	return c.JSON(fiber.Map{"redirect_url": authURL})
}

// Callback handles GET /api/v1/auth/oidc/callback.
// Called by the IdP after authentication; exchanges code, provisions user,
// stores short-lived exchange code in Redis, and redirects to frontend.
func (h *OIDCHandler) Callback(c *fiber.Ctx) error {
	code := c.Query("code")
	state := c.Query("state")
	if code == "" || state == "" {
		errMsg := c.Query("error_description", c.Query("error", "unknown error"))
		return c.Redirect("/login?error=" + url.QueryEscape("SSO failed: "+errMsg))
	}

	cfg, err := h.queries.GetEnabledOIDCConfig(c.Context())
	if err != nil {
		return c.Redirect("/login?error=" + url.QueryEscape("OIDC not configured"))
	}

	oidcCfg, err := h.buildOIDCConfig(cfg)
	if err != nil {
		return c.Redirect("/login?error=" + url.QueryEscape("OIDC config error"))
	}

	provider, err := auth.NewOIDCProvider(c.Context(), oidcCfg, h.rdb)
	if err != nil {
		slog.Error("OIDC provider init failed in callback", "error", err)
		return c.Redirect("/login?error=" + url.QueryEscape("OIDC provider error"))
	}

	userInfo, err := provider.ExchangeAndVerify(c.Context(), code, state)
	if err != nil {
		slog.Error("OIDC exchange failed", "error", err)
		errMsg := "Authentication failed"
		if errors.Is(err, auth.ErrOIDCDomainNotAllowed) {
			errMsg = "Your email domain is not allowed"
		}
		return c.Redirect("/login?error=" + url.QueryEscape(errMsg))
	}

	// JIT provision or update the user
	user, err := h.provisionUser(c, cfg, userInfo)
	if err != nil {
		slog.Error("OIDC user provisioning failed", "email", userInfo.Email, "error", err)
		return c.Redirect("/login?error=" + url.QueryEscape("User provisioning failed"))
	}

	// Sync group-to-role mapping
	mapping := make(map[string]string)
	if len(cfg.GroupRoleMapping) > 0 {
		_ = json.Unmarshal(cfg.GroupRoleMapping, &mapping)
	}
	h.syncUserRoles(c, user.ID, userInfo.Groups, mapping, cfg.DefaultRoleID)

	// Store short-lived exchange code in Redis (5 second TTL)
	exchangeCode, err := auth.GenerateRandomString(32)
	if err != nil {
		return c.Redirect("/login?error=" + url.QueryEscape("Internal error"))
	}

	exchangeData, _ := json.Marshal(map[string]string{
		"user_id": user.ID.String(),
	})
	if err := h.rdb.Set(c.Context(), "oidc:exchange:"+exchangeCode, string(exchangeData), 5*time.Second).Err(); err != nil {
		return c.Redirect("/login?error=" + url.QueryEscape("Internal error"))
	}

	details, _ := json.Marshal(map[string]string{"email": userInfo.Email, "ip": c.IP()})
	_ = h.queries.InsertAuditLog(c.Context(), db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{},
		UserID:       user.ID,
		ResourceType: "auth",
		ResourceID:   user.ID.String(),
		Action:       "oidc_callback",
		Details:      details,
	})

	return c.Redirect("/oidc-callback?oidc_token=" + exchangeCode)
}

// --- User provisioning ---

func (h *OIDCHandler) provisionUser(c *fiber.Ctx, cfg db.OidcConfig, userInfo *auth.OIDCUserInfo) (db.User, error) {
	// Look up existing user
	user, err := h.queries.GetUserByEmailAndSource(c.Context(), db.GetUserByEmailAndSourceParams{
		Email:      userInfo.Email,
		AuthSource: "oidc",
	})
	if err == nil {
		// Update display name if changed
		if userInfo.DisplayName != "" && userInfo.DisplayName != user.DisplayName {
			if updated, updateErr := h.queries.UpdateOIDCUserProfile(c.Context(), db.UpdateOIDCUserProfileParams{
				ID:          user.ID,
				DisplayName: userInfo.DisplayName,
			}); updateErr == nil {
				user = updated
			}
		}
		return user, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return db.User{}, fmt.Errorf("lookup user: %w", err)
	}

	// User not found — JIT provision if enabled
	if !cfg.AutoProvision {
		return db.User{}, fmt.Errorf("auto-provisioning disabled and user not found")
	}

	displayName := userInfo.DisplayName
	if displayName == "" {
		displayName = userInfo.Email
	}

	user, err = h.queries.CreateOIDCUser(c.Context(), db.CreateOIDCUserParams{
		Email:       userInfo.Email,
		DisplayName: displayName,
	})
	if err != nil {
		// Handle race condition
		if isDuplicateKeyError(err) {
			user, err = h.queries.GetUserByEmailAndSource(c.Context(), db.GetUserByEmailAndSourceParams{
				Email:      userInfo.Email,
				AuthSource: "oidc",
			})
			if err != nil {
				return db.User{}, fmt.Errorf("lookup user after race: %w", err)
			}
			return user, nil
		}
		return db.User{}, fmt.Errorf("create user: %w", err)
	}

	// Role assignment is handled by syncUserRoles called after provisionUser
	return user, nil
}

// syncUserRoles maps OIDC group names to RBAC role UUIDs and assigns them.
func (h *OIDCHandler) syncUserRoles(c *fiber.Ctx, userID uuid.UUID, groups []string, mapping map[string]string, defaultRoleID pgtype.UUID) {
	_ = h.queries.RevokeAllUserRoles(c.Context(), userID)

	assigned := false
	for _, group := range groups {
		roleIDStr, ok := mapping[group]
		if !ok {
			continue
		}
		roleID, err := uuid.Parse(roleIDStr)
		if err != nil {
			continue
		}
		_, _ = h.queries.AssignUserRole(c.Context(), db.AssignUserRoleParams{
			UserID:    userID,
			RoleID:    roleID,
			ScopeType: "global",
		})
		assigned = true
	}

	if !assigned && defaultRoleID.Valid {
		defID, _ := uuid.FromBytes(defaultRoleID.Bytes[:])
		_, _ = h.queries.AssignUserRole(c.Context(), db.AssignUserRoleParams{
			UserID:    userID,
			RoleID:    defID,
			ScopeType: "global",
		})
	}

	if h.rbac != nil {
		h.rbac.InvalidateUser(c.Context(), userID)
	}
}

// --- Helpers ---

func (h *OIDCHandler) buildOIDCConfig(cfg db.OidcConfig) (auth.OIDCConfig, error) {
	clientSecret := ""
	if cfg.ClientSecretEncrypted != "" {
		var err error
		clientSecret, err = crypto.Decrypt(cfg.ClientSecretEncrypted, h.encryptionKey)
		if err != nil {
			return auth.OIDCConfig{}, fmt.Errorf("decrypt client secret: %w", err)
		}
	}

	return auth.OIDCConfig{
		IssuerURL:        cfg.IssuerUrl,
		ClientID:         cfg.ClientID,
		ClientSecret:     clientSecret,
		RedirectURI:      cfg.RedirectUri,
		Scopes:           cfg.Scopes,
		EmailClaim:       cfg.EmailClaim,
		DisplayNameClaim: cfg.DisplayNameClaim,
		GroupsClaim:      cfg.GroupsClaim,
		AllowedDomains:   cfg.AllowedDomains,
	}, nil
}

func validateOIDCIssuerURL(rawURL string) error {
	if !strings.HasPrefix(rawURL, "https://") {
		return fmt.Errorf("issuer_url must use https:// scheme")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid issuer URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("issuer_url must include a hostname")
	}
	// Check literal IP
	ip := net.ParseIP(host)
	if ip != nil {
		if isPrivateOrReserved(ip) {
			return fmt.Errorf("issuer_url must not point to loopback or private addresses")
		}
		return nil
	}
	// Resolve DNS and check all returned IPs
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve issuer hostname: %w", err)
	}
	for _, resolved := range ips {
		if isPrivateOrReserved(resolved) {
			return fmt.Errorf("issuer_url hostname resolves to a private/loopback address")
		}
	}
	return nil
}

func isPrivateOrReserved(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
