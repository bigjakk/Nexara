package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/proxdash/proxdash/internal/auth"
	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
)

// LDAPHandler handles LDAP configuration endpoints.
type LDAPHandler struct {
	queries       *db.Queries
	encryptionKey string
	rbac          *auth.RBACEngine
	eventPub      *events.Publisher
}

// NewLDAPHandler creates a new LDAP handler.
func NewLDAPHandler(queries *db.Queries, encryptionKey string, rbac *auth.RBACEngine, eventPub *events.Publisher) *LDAPHandler {
	return &LDAPHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		rbac:          rbac,
		eventPub:      eventPub,
	}
}

type ldapConfigRequest struct {
	Name                 string            `json:"name"`
	Enabled              bool              `json:"enabled"`
	ServerURL            string            `json:"server_url"`
	StartTLS             bool              `json:"start_tls"`
	SkipTLSVerify        bool              `json:"skip_tls_verify"`
	BindDN               string            `json:"bind_dn"`
	BindPassword         string            `json:"bind_password"`
	SearchBaseDN         string            `json:"search_base_dn"`
	UserFilter           string            `json:"user_filter"`
	UsernameAttribute    string            `json:"username_attribute"`
	EmailAttribute       string            `json:"email_attribute"`
	DisplayNameAttribute string            `json:"display_name_attribute"`
	GroupSearchBaseDN    string            `json:"group_search_base_dn"`
	GroupFilter          string            `json:"group_filter"`
	GroupAttribute       string            `json:"group_attribute"`
	GroupRoleMapping      map[string]string `json:"group_role_mapping"`
	DefaultRoleID        *string           `json:"default_role_id"`
	SyncIntervalMinutes  int32             `json:"sync_interval_minutes"`
}

type ldapConfigResponse struct {
	ID                   uuid.UUID         `json:"id"`
	Name                 string            `json:"name"`
	Enabled              bool              `json:"enabled"`
	ServerURL            string            `json:"server_url"`
	StartTLS             bool              `json:"start_tls"`
	SkipTLSVerify        bool              `json:"skip_tls_verify"`
	BindDN               string            `json:"bind_dn"`
	BindPasswordSet      bool              `json:"bind_password_set"`
	SearchBaseDN         string            `json:"search_base_dn"`
	UserFilter           string            `json:"user_filter"`
	UsernameAttribute    string            `json:"username_attribute"`
	EmailAttribute       string            `json:"email_attribute"`
	DisplayNameAttribute string            `json:"display_name_attribute"`
	GroupSearchBaseDN    string            `json:"group_search_base_dn"`
	GroupFilter          string            `json:"group_filter"`
	GroupAttribute       string            `json:"group_attribute"`
	GroupRoleMapping      map[string]string `json:"group_role_mapping"`
	DefaultRoleID        *string           `json:"default_role_id"`
	SyncIntervalMinutes  int32             `json:"sync_interval_minutes"`
	LastSyncAt           *string           `json:"last_sync_at"`
	CreatedAt            string            `json:"created_at"`
	UpdatedAt            string            `json:"updated_at"`
}

func toLDAPConfigResponse(cfg db.LdapConfig) ldapConfigResponse {
	resp := ldapConfigResponse{
		ID:                   cfg.ID,
		Name:                 cfg.Name,
		Enabled:              cfg.Enabled,
		ServerURL:            cfg.ServerUrl,
		StartTLS:             cfg.StartTls,
		SkipTLSVerify:        cfg.SkipTlsVerify,
		BindDN:               cfg.BindDn,
		BindPasswordSet:      cfg.BindPasswordEncrypted != "",
		SearchBaseDN:         cfg.SearchBaseDn,
		UserFilter:           cfg.UserFilter,
		UsernameAttribute:    cfg.UsernameAttribute,
		EmailAttribute:       cfg.EmailAttribute,
		DisplayNameAttribute: cfg.DisplayNameAttribute,
		GroupSearchBaseDN:    cfg.GroupSearchBaseDn,
		GroupFilter:          cfg.GroupFilter,
		GroupAttribute:       cfg.GroupAttribute,
		SyncIntervalMinutes:  cfg.SyncIntervalMinutes,
		CreatedAt:            cfg.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:            cfg.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	// Parse group_role_mapping from JSONB
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

	if cfg.LastSyncAt.Valid {
		s := cfg.LastSyncAt.Time.Format("2006-01-02T15:04:05Z07:00")
		resp.LastSyncAt = &s
	}

	return resp
}

// List handles GET /api/v1/ldap/configs.
func (h *LDAPHandler) List(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	configs, err := h.queries.ListLDAPConfigs(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list LDAP configs")
	}

	resp := make([]ldapConfigResponse, len(configs))
	for i, cfg := range configs {
		resp[i] = toLDAPConfigResponse(cfg)
	}

	return c.JSON(resp)
}

// Get handles GET /api/v1/ldap/configs/:id.
func (h *LDAPHandler) Get(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid config ID")
	}

	cfg, err := h.queries.GetLDAPConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "LDAP config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get LDAP config")
	}

	return c.JSON(toLDAPConfigResponse(cfg))
}

// Create handles POST /api/v1/ldap/configs.
func (h *LDAPHandler) Create(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	var req ldapConfigRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.ServerURL == "" || req.SearchBaseDN == "" {
		return fiber.NewError(fiber.StatusBadRequest, "server_url and search_base_dn are required")
	}

	if err := validateLDAPServerURL(req.ServerURL); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if err := validateLDAPFilters(req.UserFilter, req.GroupFilter); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	encPassword := ""
	if req.BindPassword != "" {
		var err error
		encPassword, err = crypto.Encrypt(req.BindPassword, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt bind password")
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

	syncInterval := req.SyncIntervalMinutes
	if syncInterval <= 0 {
		syncInterval = 60
	}

	cfg, err := h.queries.CreateLDAPConfig(c.Context(), db.CreateLDAPConfigParams{
		Name:                  req.Name,
		Enabled:               req.Enabled,
		ServerUrl:             req.ServerURL,
		StartTls:              req.StartTLS,
		SkipTlsVerify:         req.SkipTLSVerify,
		BindDn:                req.BindDN,
		BindPasswordEncrypted: encPassword,
		SearchBaseDn:          req.SearchBaseDN,
		UserFilter:            withDefault(req.UserFilter, "(|(uid={{username}})(mail={{username}}))"),
		UsernameAttribute:     withDefault(req.UsernameAttribute, "uid"),
		EmailAttribute:        withDefault(req.EmailAttribute, "mail"),
		DisplayNameAttribute:  withDefault(req.DisplayNameAttribute, "cn"),
		GroupSearchBaseDn:     req.GroupSearchBaseDN,
		GroupFilter:           withDefault(req.GroupFilter, "(member={{userDN}})"),
		GroupAttribute:        withDefault(req.GroupAttribute, "cn"),
		GroupRoleMapping:      mappingJSON,
		DefaultRoleID:         defaultRoleID,
		SyncIntervalMinutes:   syncInterval,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create LDAP config")
	}

	details, _ := json.Marshal(map[string]string{"name": cfg.Name})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", cfg.ID.String(), "ldap_config_created", details)

	return c.Status(fiber.StatusCreated).JSON(toLDAPConfigResponse(cfg))
}

// Update handles PUT /api/v1/ldap/configs/:id.
func (h *LDAPHandler) Update(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid config ID")
	}

	existing, err := h.queries.GetLDAPConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "LDAP config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get LDAP config")
	}

	var req ldapConfigRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.ServerURL == "" || req.SearchBaseDN == "" {
		return fiber.NewError(fiber.StatusBadRequest, "server_url and search_base_dn are required")
	}

	if err := validateLDAPServerURL(req.ServerURL); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if err := validateLDAPFilters(req.UserFilter, req.GroupFilter); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// If password is provided, encrypt it; otherwise keep existing
	encPassword := existing.BindPasswordEncrypted
	if req.BindPassword != "" {
		encPassword, err = crypto.Encrypt(req.BindPassword, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt bind password")
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

	syncInterval := req.SyncIntervalMinutes
	if syncInterval <= 0 {
		syncInterval = 60
	}

	cfg, err := h.queries.UpdateLDAPConfig(c.Context(), db.UpdateLDAPConfigParams{
		ID:                    id,
		Name:                  req.Name,
		Enabled:               req.Enabled,
		ServerUrl:             req.ServerURL,
		StartTls:              req.StartTLS,
		SkipTlsVerify:         req.SkipTLSVerify,
		BindDn:                req.BindDN,
		BindPasswordEncrypted: encPassword,
		SearchBaseDn:          req.SearchBaseDN,
		UserFilter:            withDefault(req.UserFilter, "(|(uid={{username}})(mail={{username}}))"),
		UsernameAttribute:     withDefault(req.UsernameAttribute, "uid"),
		EmailAttribute:        withDefault(req.EmailAttribute, "mail"),
		DisplayNameAttribute:  withDefault(req.DisplayNameAttribute, "cn"),
		GroupSearchBaseDn:     req.GroupSearchBaseDN,
		GroupFilter:           withDefault(req.GroupFilter, "(member={{userDN}})"),
		GroupAttribute:        withDefault(req.GroupAttribute, "cn"),
		GroupRoleMapping:      mappingJSON,
		DefaultRoleID:         defaultRoleID,
		SyncIntervalMinutes:   syncInterval,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update LDAP config")
	}

	details, _ := json.Marshal(map[string]string{"name": cfg.Name})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", cfg.ID.String(), "ldap_config_updated", details)

	return c.JSON(toLDAPConfigResponse(cfg))
}

// Delete handles DELETE /api/v1/ldap/configs/:id.
func (h *LDAPHandler) Delete(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid config ID")
	}

	if err := h.queries.DeleteLDAPConfig(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete LDAP config")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", id.String(), "ldap_config_deleted", nil)

	return c.SendStatus(fiber.StatusNoContent)
}

type testConnectionRequest struct {
	TestUsername string `json:"test_username"`
}

type testConnectionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// TestConnection handles POST /api/v1/ldap/configs/:id/test.
func (h *LDAPHandler) TestConnection(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid config ID")
	}

	cfg, err := h.queries.GetLDAPConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "LDAP config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get LDAP config")
	}

	ldapCfg, err := h.buildLDAPConfig(cfg)
	if err != nil {
		return c.JSON(testConnectionResponse{Success: false, Message: err.Error()})
	}

	client := auth.NewLDAPClient(ldapCfg)

	// Test basic connectivity and bind
	if err := client.TestConnection(); err != nil {
		slog.Error("LDAP test connection failed", "config_id", id, "error", err)
		return c.JSON(testConnectionResponse{
			Success: false,
			Message: classifyLDAPError(err),
		})
	}

	msg := "Connection and bind successful"

	// If a test username was provided, try to search for it
	var req testConnectionRequest
	if err := c.BodyParser(&req); err == nil && req.TestUsername != "" {
		user, err := client.SearchUser(req.TestUsername)
		if err != nil {
			slog.Error("LDAP test user search failed", "config_id", id, "username", req.TestUsername, "error", err)
			return c.JSON(testConnectionResponse{
				Success: false,
				Message: classifyLDAPError(err),
			})
		}
		msg = fmt.Sprintf("User found: %s (%s), %d groups", user.DisplayName, user.Email, len(user.Groups))
	}

	return c.JSON(testConnectionResponse{
		Success: true,
		Message: msg,
	})
}

// Sync handles POST /api/v1/ldap/configs/:id/sync.
func (h *LDAPHandler) Sync(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid config ID")
	}

	cfg, err := h.queries.GetLDAPConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "LDAP config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get LDAP config")
	}

	ldapCfg, err := h.buildLDAPConfig(cfg)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to build LDAP config: "+err.Error())
	}

	client := auth.NewLDAPClient(ldapCfg)

	// Get all LDAP users from DB
	ldapUsers, err := h.queries.ListLDAPUsers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list LDAP users")
	}

	// Parse group-role mapping
	mapping := make(map[string]string)
	if len(cfg.GroupRoleMapping) > 0 {
		_ = json.Unmarshal(cfg.GroupRoleMapping, &mapping)
	}

	synced := 0
	disabled := 0
	reEnabled := 0
	for _, dbUser := range ldapUsers {
		// Look up current user info in LDAP
		ldapUser, err := client.SearchUser(dbUser.Email)
		if err != nil {
			// User no longer in directory — disable if still active
			if dbUser.IsActive {
				_ = h.queries.SetLDAPUserActive(c.Context(), db.SetLDAPUserActiveParams{
					ID:       dbUser.ID,
					IsActive: false,
				})
				disabled++
				slog.Info("LDAP sync: disabled user removed from directory", "email", dbUser.Email)
			}
			continue
		}

		// User exists in directory — re-enable if previously disabled
		if !dbUser.IsActive {
			_ = h.queries.SetLDAPUserActive(c.Context(), db.SetLDAPUserActiveParams{
				ID:       dbUser.ID,
				IsActive: true,
			})
			reEnabled++
			slog.Info("LDAP sync: re-enabled user found in directory", "email", dbUser.Email)
		}

		// Update display name if changed
		if ldapUser.DisplayName != "" && ldapUser.DisplayName != dbUser.DisplayName {
			_, _ = h.queries.UpdateLDAPUserProfile(c.Context(), db.UpdateLDAPUserProfileParams{
				ID:          dbUser.ID,
				DisplayName: ldapUser.DisplayName,
			})
		}

		// Sync group-to-role mapping
		h.syncUserRoles(c, dbUser.ID, ldapUser.Groups, mapping, cfg.DefaultRoleID)
		synced++
	}

	_ = h.queries.UpdateLDAPConfigLastSync(c.Context(), id)

	details, _ := json.Marshal(map[string]interface{}{
		"synced":     synced,
		"disabled":   disabled,
		"re_enabled": reEnabled,
	})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", id.String(), "ldap_sync", details)

	return c.JSON(fiber.Map{
		"message":      "Sync complete",
		"users_synced": synced,
		"users_disabled": disabled,
		"users_re_enabled": reEnabled,
	})
}

func (h *LDAPHandler) buildLDAPConfig(cfg db.LdapConfig) (auth.LDAPConfig, error) {
	bindPassword := ""
	if cfg.BindPasswordEncrypted != "" {
		var err error
		bindPassword, err = crypto.Decrypt(cfg.BindPasswordEncrypted, h.encryptionKey)
		if err != nil {
			return auth.LDAPConfig{}, fmt.Errorf("decrypt bind password: %w", err)
		}
	}

	return auth.LDAPConfig{
		ServerURL:            cfg.ServerUrl,
		StartTLS:             cfg.StartTls,
		SkipTLSVerify:        cfg.SkipTlsVerify,
		BindDN:               cfg.BindDn,
		BindPassword:         bindPassword,
		SearchBaseDN:         cfg.SearchBaseDn,
		UserFilter:           cfg.UserFilter,
		UsernameAttribute:    cfg.UsernameAttribute,
		EmailAttribute:       cfg.EmailAttribute,
		DisplayNameAttribute: cfg.DisplayNameAttribute,
		GroupSearchBaseDN:    cfg.GroupSearchBaseDn,
		GroupFilter:          cfg.GroupFilter,
		GroupAttribute:       cfg.GroupAttribute,
	}, nil
}

// syncUserRoles maps LDAP group DNs to RBAC role UUIDs and assigns them.
func (h *LDAPHandler) syncUserRoles(c *fiber.Ctx, userID uuid.UUID, groups []string, mapping map[string]string, defaultRoleID pgtype.UUID) {
	// Clear existing RBAC roles
	_ = h.queries.RevokeAllUserRoles(c.Context(), userID)

	assigned := false
	for _, groupDN := range groups {
		roleIDStr, ok := mapping[groupDN]
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

	// If no group matched, assign default role
	if !assigned && defaultRoleID.Valid {
		defID, _ := uuid.FromBytes(defaultRoleID.Bytes[:])
		_, _ = h.queries.AssignUserRole(c.Context(), db.AssignUserRoleParams{
			UserID:    userID,
			RoleID:    defID,
			ScopeType: "global",
		})
	}

	// Invalidate RBAC cache
	if h.rbac != nil {
		h.rbac.InvalidateUser(c.Context(), userID)
	}
}

// BuildLDAPConfigFromDB creates an auth.LDAPConfig from a DB model. Exported for use by auth handler.
func (h *LDAPHandler) BuildLDAPConfigFromDB(cfg db.LdapConfig) (auth.LDAPConfig, error) {
	return h.buildLDAPConfig(cfg)
}

// SyncUserRoles is exported for use by the auth handler during login.
func (h *LDAPHandler) SyncUserRoles(c *fiber.Ctx, userID uuid.UUID, groups []string, mapping map[string]string, defaultRoleID pgtype.UUID) {
	h.syncUserRoles(c, userID, groups, mapping, defaultRoleID)
}

func withDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

func classifyLDAPError(err error) string {
	switch {
	case errors.Is(err, auth.ErrLDAPConnection):
		return "Could not connect to LDAP server. Verify the server URL and that the server is reachable."
	case errors.Is(err, auth.ErrLDAPBindCredentials):
		return "Bind failed: invalid bind DN or password. Verify your service account credentials."
	case errors.Is(err, auth.ErrLDAPUserNotFound):
		return "User not found. Check your search base DN, user filter, and username attribute. For Active Directory, use sAMAccountName instead of uid."
	case errors.Is(err, auth.ErrLDAPSearchFailed):
		return "Search failed. Verify the search base DN exists and the bind account has read permissions."
	case errors.Is(err, auth.ErrLDAPUserBindFailed):
		return "User found but password is incorrect."
	default:
		return "LDAP operation failed. Check server logs for details."
	}
}

func validateLDAPFilters(userFilter, groupFilter string) error {
	if userFilter != "" && !strings.Contains(userFilter, "{{username}}") {
		return fmt.Errorf("user_filter must contain the {{username}} placeholder")
	}
	if groupFilter != "" && !strings.Contains(groupFilter, "{{userDN}}") {
		return fmt.Errorf("group_filter must contain the {{userDN}} placeholder")
	}
	return nil
}

func validateLDAPServerURL(rawURL string) error {
	if !strings.HasPrefix(rawURL, "ldap://") && !strings.HasPrefix(rawURL, "ldaps://") {
		return fmt.Errorf("server_url must use ldap:// or ldaps:// scheme")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("server_url must include a hostname")
	}
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return fmt.Errorf("server_url must not point to loopback or link-local addresses")
	}
	return nil
}
