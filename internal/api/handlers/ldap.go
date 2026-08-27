package handlers

import (
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

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
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
	GroupRoleMapping     map[string]string `json:"group_role_mapping"`
	DefaultRoleID        *string           `json:"default_role_id"`
	SyncIntervalMinutes  int32             `json:"sync_interval_minutes"`
	// AcknowledgeInsecureTLS is required to store a config that carries
	// passwords over a connection that is unencrypted, or encrypted but
	// unverified.
	AcknowledgeInsecureTLS bool `json:"acknowledge_insecure_tls,omitempty"`
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
	GroupRoleMapping     map[string]string `json:"group_role_mapping"`
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
		CreatedAt:            cfg.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:            cfg.UpdatedAt.Format(time.RFC3339Nano),
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
		s := cfg.LastSyncAt.Time.Format(time.RFC3339Nano)
		resp.LastSyncAt = &s
	}

	return resp
}

// List handles GET /api/v1/ldap/configs.
func (h *LDAPHandler) List(c fiber.Ctx) error {
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

	return RespondItems(c, resp)
}

// Get handles GET /api/v1/ldap/configs/:id.
func (h *LDAPHandler) Get(c fiber.Ctx) error {
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
func (h *LDAPHandler) Create(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	var req ldapConfigRequest
	if err := c.Bind().Body(&req); err != nil {
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

	// No previous state on create, so the gate reads the END state: pass a
	// protected, verifying "before" and let the requested config be compared
	// against it.
	if err := requireLDAPTransportAck(
		true, ldapTransportProtected(req.ServerURL, req.StartTLS),
		false, req.SkipTLSVerify,
		req.AcknowledgeInsecureTLS); err != nil {
		return renderConfirmRequired(c, err)
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

	// Same reasoning as Update: a directory created straight into cleartext is
	// as worth a trail as one moved there, and creating a new enabled config is
	// the easier of the two for a caller who wants passwords sent somewhere.
	createFields := map[string]any{"name": cfg.Name}
	if lostEnc, lostVer := ldapTransportWeakened(
		true, ldapTransportProtected(cfg.ServerUrl, cfg.StartTls),
		false, cfg.SkipTlsVerify); lostEnc || lostVer {
		createFields["insecure_transport_acknowledged"] = true
		createFields["new_transport"] = ldapTransportLabel(cfg.ServerUrl, cfg.StartTls, cfg.SkipTlsVerify)
	}
	details, _ := json.Marshal(createFields)
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", cfg.ID.String(), "ldap_config_created", details)

	return c.Status(fiber.StatusCreated).JSON(toLDAPConfigResponse(cfg))
}

// Update handles PUT /api/v1/ldap/configs/:id.
func (h *LDAPHandler) Update(c fiber.Ctx) error {
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
	if err := c.Bind().Body(&req); err != nil {
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

	// A downgrade of the transport is gated the same way an address move is,
	// and for the same reason: the caller need never have seen the bind
	// password, and this is the other way to get it out of Nexara.
	wasProtected := ldapTransportProtected(existing.ServerUrl, existing.StartTls)
	nowProtected := ldapTransportProtected(req.ServerURL, req.StartTLS)
	lostEncryption, lostVerification := ldapTransportWeakened(
		wasProtected, nowProtected, existing.SkipTlsVerify, req.SkipTLSVerify)
	if err := requireLDAPTransportAck(
		wasProtected, nowProtected,
		existing.SkipTlsVerify, req.SkipTLSVerify,
		req.AcknowledgeInsecureTLS); err != nil {
		return renderConfirmRequired(c, err)
	}

	// Refuse to re-point the stored bind password at a directory the operator
	// never entrusted it to. See credential_redirect.go. Delivery is deferred
	// here: the password is bound against server_url on the next TestConnection,
	// scheduled sync, or user login. A config that binds anonymously has no
	// stored password and is not blocked from moving.
	if credentialRedirected(req.ServerURL, existing.ServerUrl, existing.BindPasswordEncrypted, req.BindPassword) {
		// Audited, because this is the one request that is unambiguously an
		// attempt to point a stored credential somewhere new. Refusing it
		// silently would make an enumeration of this path invisible.
		redirectDetails, _ := json.Marshal(map[string]any{
			// Truncated: nothing bounds the length of a URL a caller can
			// submit, and audit_log.details is readable by every Viewer.
			"attempted_server_url": auditSafe(req.ServerURL),
			"previous_server_url":  auditSafe(existing.ServerUrl),
		})
		AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", id.String(),
			"ldap_credential_redirect_refused", redirectDetails)
		return errCredentialRedirect("server URL", "bind password")
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

	// An acknowledged downgrade is the one action in this file the project
	// gates behind a confirmation, so it has to leave a trail — otherwise the
	// only record that a directory was moved to cleartext is the absence of
	// one. The transport is recorded as a label rather than the raw server_url:
	// GET on this config needs manage:user while view:audit is held by every
	// Viewer, and the label carries the whole point without the address.
	fields := map[string]any{"name": cfg.Name}
	if lostEncryption || lostVerification {
		fields["insecure_transport_acknowledged"] = true
		fields["previous_transport"] = ldapTransportLabel(existing.ServerUrl, existing.StartTls, existing.SkipTlsVerify)
		fields["new_transport"] = ldapTransportLabel(cfg.ServerUrl, cfg.StartTls, cfg.SkipTlsVerify)
	}
	details, _ := json.Marshal(fields)
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", cfg.ID.String(), "ldap_config_updated", details)

	return c.JSON(toLDAPConfigResponse(cfg))
}

// Delete handles DELETE /api/v1/ldap/configs/:id.
func (h *LDAPHandler) Delete(c fiber.Ctx) error {
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
func (h *LDAPHandler) TestConnection(c fiber.Ctx) error {
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
	if err := c.Bind().Body(&req); err == nil && req.TestUsername != "" {
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
func (h *LDAPHandler) Sync(c fiber.Ctx) error {
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
		"message":          "Sync complete",
		"users_synced":     synced,
		"users_disabled":   disabled,
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
func (h *LDAPHandler) syncUserRoles(c fiber.Ctx, userID uuid.UUID, groups []string, mapping map[string]string, defaultRoleID pgtype.UUID) {
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
func (h *LDAPHandler) SyncUserRoles(c fiber.Ctx, userID uuid.UUID, groups []string, mapping map[string]string, defaultRoleID pgtype.UUID) {
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

// ldapTransportProtected reports whether a config encrypts its connection at
// all. ldaps:// is implicit TLS; on ldap:// only StartTLS upgrades it.
func ldapTransportProtected(serverURL string, startTLS bool) bool {
	// Lowercased because wasProtected is computed from a STORED server_url.
	// validateLDAPServerURL is case-sensitive and runs on every write path, so
	// "LDAPS://" cannot be stored today — but a row that predates it would
	// otherwise read as cleartext, making a real downgrade look like no change
	// and skipping the prompt.
	return startTLS || strings.HasPrefix(strings.ToLower(serverURL), "ldaps://")
}

// ldapTransportWeakened reports the two ways a config can move to a weaker
// connection: losing encryption entirely, and keeping it but dropping the
// certificate check. They are reported separately because the operator needs
// different words for each — cleartext is readable by anyone on the wire,
// unverified only by someone who can intercept.
//
// Split out from the gate so the rule is testable without a request context.
func ldapTransportWeakened(wasProtected, nowProtected, wasSkippingVerify, nowSkippingVerify bool) (lostEncryption, lostVerification bool) {
	return wasProtected && !nowProtected, !wasSkippingVerify && nowSkippingVerify
}

// ldapTransportLabel names a config's connection security in one token, for
// audit rows that must not carry the server address itself.
func ldapTransportLabel(serverURL string, startTLS, skipVerify bool) string {
	if !ldapTransportProtected(serverURL, startTLS) {
		return "cleartext"
	}
	base := "starttls"
	if strings.HasPrefix(strings.ToLower(serverURL), "ldaps://") {
		base = "ldaps"
	}
	if skipVerify {
		return base + " (unverified)"
	}
	return base
}

// requireLDAPTransportAck refuses a config that weakens LDAP transport unless
// the caller says so explicitly.
//
// Why this is NOT conditional on a stored bind password: LDAP authentication
// binds AS THE USER, so every interactive login puts that person's password on
// this connection. A config doing an anonymous service bind still carries end
// user passwords, and a directory on a segment someone can sniff needs no MITM
// at all for cleartext to be readable.
//
// Confirm-and-proceed rather than a refusal, matching requireInsecureTLSAck and
// the private-address gate: a lab directory with no CA is a real case. What it
// removes is doing it by accident — and note start_tls and skip_tls_verify are
// non-pointer bools on a full-body PUT, so a client that merely OMITS them
// turns StartTLS off. That is the accident this catches.
//
// The predicate is the CHANGE, not the end state, so an install already running
// cleartext is not made to re-acknowledge on every unrelated edit. Pass
// wasProtected=true / wasSkippingVerify=false on create, where there is no
// previous state and the end state is what matters.
func requireLDAPTransportAck(wasProtected, nowProtected, wasSkippingVerify, nowSkippingVerify, acknowledged bool) error {
	lostEncryption, lostVerification := ldapTransportWeakened(
		wasProtected, nowProtected, wasSkippingVerify, nowSkippingVerify)
	if (!lostEncryption && !lostVerification) || acknowledged {
		return nil
	}

	// Losing encryption outranks losing verification: a cleartext connection
	// has no certificate to talk about, and the operator needs the stronger of
	// the two warnings.
	if lostEncryption {
		return &confirmRequiredError{
			Code: confirmInsecureLDAPTransport,
			Message: "This would carry the bind password and every user's login password to " +
				"the directory in cleartext — ldap:// without StartTLS is not encrypted at all. " +
				"Confirm to proceed.",
			Fields: map[string]any{"transport_kind": "cleartext"},
		}
	}
	return &confirmRequiredError{
		Code: confirmInsecureLDAPTransport,
		Message: "This directory connection's certificate would never be verified, so the " +
			"passwords on it are exposed to anyone who can intercept the connection. " +
			"Confirm to proceed.",
		Fields: map[string]any{"transport_kind": "unverified"},
	}
}

// confirmInsecureLDAPTransport is the code the admin page keys on to turn the
// refusal into a prompt.
const confirmInsecureLDAPTransport = "insecure_ldap_transport_confirm_required"

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
