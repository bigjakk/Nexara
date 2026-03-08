package handlers

import (
	"encoding/json"
	"errors"
	"net/mail"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/proxdash/proxdash/internal/auth"
	db "github.com/proxdash/proxdash/internal/db/generated"
)

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	queries        *db.Queries
	jwtService     *auth.JWTService
	sessionManager *auth.SessionManager
	rbac           *auth.RBACEngine
	ldapHandler    *LDAPHandler
	oidcHandler    *OIDCHandler
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(queries *db.Queries, jwtSvc *auth.JWTService, sessMgr *auth.SessionManager, rbac *auth.RBACEngine) *AuthHandler {
	return &AuthHandler{
		queries:        queries,
		jwtService:     jwtSvc,
		sessionManager: sessMgr,
		rbac:           rbac,
	}
}

// SetLDAPHandler sets the LDAP handler reference for LDAP-aware login.
func (h *AuthHandler) SetLDAPHandler(lh *LDAPHandler) {
	h.ldapHandler = lh
}

// SetOIDCHandler sets the OIDC handler reference for SSO-aware login and token exchange.
func (h *AuthHandler) SetOIDCHandler(oh *OIDCHandler) {
	h.oidcHandler = oh
}

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type authResponse struct {
	User         authUserResponse `json:"user"`
	AccessToken  string           `json:"access_token"`
	RefreshToken string           `json:"refresh_token"`
	ExpiresAt    int64            `json:"expires_at"`
	Permissions  []string         `json:"permissions"`
}

type authUserResponse struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
}

// authAuditLog writes an audit log entry for auth events. Uses the provided userID
// directly since auth events happen before/outside normal auth middleware.
func (h *AuthHandler) authAuditLog(c *fiber.Ctx, userID uuid.UUID, action string, details json.RawMessage) {
	if details == nil {
		details = json.RawMessage(`{}`)
	}
	_ = h.queries.InsertAuditLog(c.Context(), db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{},
		UserID:       userID,
		ResourceType: "auth",
		ResourceID:   userID.String(),
		Action:       action,
		Details:      details,
	})
}

// Register handles user registration.
// First user is auto-promoted to admin and requires no auth.
// Subsequent users require admin auth (checked via authOptional middleware + handler check).
func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req registerRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Email == "" || req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Email and password are required")
	}

	if _, err := mail.ParseAddress(req.Email); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid email address")
	}

	// Validate password strength before hitting the database.
	hashedPassword, err := auth.HashPassword(req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrPasswordTooShort) || errors.Is(err, auth.ErrPasswordTooLong) || errors.Is(err, auth.ErrPasswordWeak) {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to process password")
	}

	count, err := h.queries.CountUsers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check user count")
	}

	role := "user"
	if count == 0 {
		role = "admin"
	} else {
		callerRole, _ := c.Locals("role").(string)
		if callerRole != "admin" {
			return fiber.NewError(fiber.StatusForbidden, "Only admins can register new users")
		}
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Email
	}

	user, err := h.queries.CreateUser(c.Context(), db.CreateUserParams{
		Email:        req.Email,
		PasswordHash: hashedPassword,
		DisplayName:  displayName,
		IsActive:     true,
		Role:         role,
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			return fiber.NewError(fiber.StatusConflict, "Email already registered")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create user")
	}

	// Assign RBAC role: admin -> Admin, user -> Viewer
	rbacRoleID := "a0000000-0000-0000-0000-000000000003" // Viewer
	if role == "admin" {
		rbacRoleID = "a0000000-0000-0000-0000-000000000001" // Admin
	}
	if roleUUID, parseErr := uuid.Parse(rbacRoleID); parseErr == nil {
		_, _ = h.queries.AssignUserRole(c.Context(), db.AssignUserRoleParams{
			UserID:    user.ID,
			RoleID:    roleUUID,
			ScopeType: "global",
		})
	}

	accessToken, expiresAt, err := h.jwtService.GenerateAccessToken(user.ID, user.Email, user.Role)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate access token")
	}

	refreshToken, err := h.jwtService.GenerateRefreshToken()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate refresh token")
	}

	_, err = h.sessionManager.CreateSession(
		c.Context(), user.ID, refreshToken, user.Role,
		c.Get("User-Agent"), c.IP(),
		h.jwtService.RefreshTokenTTL(),
	)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create session")
	}

	details, _ := json.Marshal(map[string]string{"email": user.Email, "role": user.Role})
	h.authAuditLog(c, user.ID, "register", details)

	perms := h.loadPerms(c, user.ID)

	return c.Status(fiber.StatusCreated).JSON(authResponse{
		User: authUserResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt.Unix(),
		Permissions:  perms,
	})
}

// Login handles user authentication.
// If LDAP is enabled, tries LDAP authentication first, then falls back to local auth.
func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Email == "" || req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Email and password are required")
	}

	invalidCredentials := fiber.NewError(fiber.StatusUnauthorized, "Invalid email or password")

	// Try LDAP authentication first if enabled
	if user, ok := h.tryLDAPLogin(c, req.Email, req.Password); ok {
		return h.issueTokens(c, user, "ldap_login")
	}

	// Fall back to local authentication
	user, err := h.queries.GetUserByEmail(c.Context(), req.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidCredentials
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to look up user")
	}

	// LDAP/OIDC-sourced users cannot log in with local passwords
	if user.AuthSource == "ldap" || user.AuthSource == "oidc" {
		return invalidCredentials
	}

	if !user.IsActive {
		return invalidCredentials
	}

	if err := auth.CheckPassword(user.PasswordHash, req.Password); err != nil {
		return invalidCredentials
	}

	return h.issueTokens(c, user, "login")
}

// tryLDAPLogin attempts LDAP authentication. Returns the DB user and true on success.
func (h *AuthHandler) tryLDAPLogin(c *fiber.Ctx, email, password string) (db.User, bool) {
	if h.ldapHandler == nil {
		return db.User{}, false
	}

	ldapCfg, err := h.queries.GetEnabledLDAPConfig(c.Context())
	if err != nil {
		return db.User{}, false
	}

	authCfg, err := h.ldapHandler.BuildLDAPConfigFromDB(ldapCfg)
	if err != nil {
		return db.User{}, false
	}

	client := auth.NewLDAPClient(authCfg)
	ldapUser, err := client.Authenticate(email, password)
	if err != nil {
		return db.User{}, false
	}

	// Determine the email to use
	userEmail := ldapUser.Email
	if userEmail == "" {
		userEmail = email
	}

	// Look up or JIT-create the user
	user, err := h.queries.GetUserByEmailAndSource(c.Context(), db.GetUserByEmailAndSourceParams{
		Email:      userEmail,
		AuthSource: "ldap",
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return db.User{}, false
		}

		// JIT provision
		displayName := ldapUser.DisplayName
		if displayName == "" {
			displayName = userEmail
		}
		user, err = h.queries.CreateLDAPUser(c.Context(), db.CreateLDAPUserParams{
			Email:       userEmail,
			DisplayName: displayName,
		})
		if err != nil {
			// Handle race condition: another concurrent request may have created this user
			if isDuplicateKeyError(err) {
				user, err = h.queries.GetUserByEmailAndSource(c.Context(), db.GetUserByEmailAndSourceParams{
					Email:      userEmail,
					AuthSource: "ldap",
				})
				if err != nil {
					return db.User{}, false
				}
			} else {
				return db.User{}, false
			}
		}
	} else {
		// Update display name if changed
		if ldapUser.DisplayName != "" && ldapUser.DisplayName != user.DisplayName {
			if updated, err := h.queries.UpdateLDAPUserProfile(c.Context(), db.UpdateLDAPUserProfileParams{
				ID:          user.ID,
				DisplayName: ldapUser.DisplayName,
			}); err == nil {
				user = updated
			}
		}
	}

	if !user.IsActive {
		return db.User{}, false
	}

	// Sync RBAC roles from LDAP group mapping
	mapping := make(map[string]string)
	if len(ldapCfg.GroupRoleMapping) > 0 {
		_ = json.Unmarshal(ldapCfg.GroupRoleMapping, &mapping)
	}
	h.ldapHandler.SyncUserRoles(c, user.ID, ldapUser.Groups, mapping, ldapCfg.DefaultRoleID)

	return user, true
}

// issueTokens creates JWT + session for the given user and returns the auth response.
func (h *AuthHandler) issueTokens(c *fiber.Ctx, user db.User, auditAction string) error {
	accessToken, expiresAt, err := h.jwtService.GenerateAccessToken(user.ID, user.Email, user.Role)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate access token")
	}

	refreshToken, err := h.jwtService.GenerateRefreshToken()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate refresh token")
	}

	_, err = h.sessionManager.CreateSession(
		c.Context(), user.ID, refreshToken, user.Role,
		c.Get("User-Agent"), c.IP(),
		h.jwtService.RefreshTokenTTL(),
	)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create session")
	}

	details, _ := json.Marshal(map[string]string{"email": user.Email, "ip": c.IP()})
	h.authAuditLog(c, user.ID, auditAction, details)

	perms := h.loadPerms(c, user.ID)

	return c.JSON(authResponse{
		User: authUserResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt.Unix(),
		Permissions:  perms,
	})
}

// Refresh exchanges a valid refresh token for a new token pair.
func (h *AuthHandler) Refresh(c *fiber.Ctx) error {
	var req refreshRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.RefreshToken == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Refresh token is required")
	}

	session, err := h.sessionManager.ValidateRefreshToken(c.Context(), req.RefreshToken)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired refresh token")
	}

	user, err := h.queries.GetUserByID(c.Context(), session.UserID)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "User not found")
	}

	if !user.IsActive {
		_ = h.sessionManager.RevokeSession(c.Context(), session.ID)
		return fiber.NewError(fiber.StatusUnauthorized, "Account is disabled")
	}

	accessToken, expiresAt, err := h.jwtService.GenerateAccessToken(user.ID, user.Email, user.Role)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate access token")
	}

	newRefreshToken, err := h.jwtService.GenerateRefreshToken()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate refresh token")
	}

	if err := h.sessionManager.RotateRefreshToken(c.Context(), session.ID, newRefreshToken, user.Role, h.jwtService.RefreshTokenTTL()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to rotate refresh token")
	}

	perms := h.loadPerms(c, user.ID)

	return c.JSON(authResponse{
		User: authUserResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		ExpiresAt:    expiresAt.Unix(),
		Permissions:  perms,
	})
}

// Logout revokes the session identified by the refresh token in the request body.
func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	var req logoutRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.RefreshToken == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Refresh token is required")
	}

	session, err := h.sessionManager.ValidateRefreshToken(c.Context(), req.RefreshToken)
	if err != nil {
		// Token already invalid/revoked — treat as success
		return c.JSON(fiber.Map{"message": "Logged out successfully"})
	}

	// Verify the session belongs to the authenticated user
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if session.UserID != userID {
		return fiber.NewError(fiber.StatusForbidden, "Session does not belong to authenticated user")
	}

	if err := h.sessionManager.RevokeSession(c.Context(), session.ID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revoke session")
	}

	h.authAuditLog(c, userID, "logout", nil)

	return c.JSON(fiber.Map{"message": "Logged out successfully"})
}

// LogoutAll revokes all sessions for the current user.
func (h *AuthHandler) LogoutAll(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	if err := h.sessionManager.RevokeAllUserSessions(c.Context(), userID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revoke sessions")
	}

	h.authAuditLog(c, userID, "logout_all", nil)

	return c.JSON(fiber.Map{"message": "All sessions revoked"})
}

// SetupStatus returns whether initial admin setup has been completed.
func (h *AuthHandler) SetupStatus(c *fiber.Ctx) error {
	count, err := h.queries.CountUsers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check user count")
	}
	return c.JSON(fiber.Map{
		"needs_setup": count == 0,
	})
}

// loadPerms loads the flat permissions list for the given user via RBAC engine.
func (h *AuthHandler) loadPerms(c *fiber.Ctx, userID uuid.UUID) []string {
	if h.rbac == nil {
		return []string{}
	}
	perms, err := h.rbac.GetFlatPermissions(c.Context(), userID)
	if err != nil || perms == nil {
		return []string{}
	}
	return perms
}

// SSOStatus returns whether OIDC/SSO is enabled and the provider name.
func (h *AuthHandler) SSOStatus(c *fiber.Ctx) error {
	resp := fiber.Map{
		"oidc_enabled":       false,
		"oidc_provider_name": "",
	}

	if h.oidcHandler != nil {
		cfg, err := h.queries.GetEnabledOIDCConfig(c.Context())
		if err == nil {
			resp["oidc_enabled"] = true
			resp["oidc_provider_name"] = cfg.Name
		}
	}

	return c.JSON(resp)
}

// OIDCTokenExchange consumes the short-lived exchange code and issues standard JWT tokens.
func (h *AuthHandler) OIDCTokenExchange(c *fiber.Ctx) error {
	if h.oidcHandler == nil {
		return fiber.NewError(fiber.StatusNotFound, "OIDC not configured")
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Code == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Code is required")
	}

	// Atomic GetDel — single-use exchange code
	data, err := h.oidcHandler.rdb.GetDel(c.Context(), "oidc:exchange:"+req.Code).Result()
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired exchange code")
	}

	var exchangeData map[string]string
	if err := json.Unmarshal([]byte(data), &exchangeData); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Invalid exchange data")
	}

	userID, err := uuid.Parse(exchangeData["user_id"])
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Invalid user in exchange data")
	}

	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "User not found")
	}

	if !user.IsActive {
		return fiber.NewError(fiber.StatusForbidden, "Account is disabled")
	}

	return h.issueTokens(c, user, "oidc_login")
}

// isDuplicateKeyError checks if a pgx error is a unique constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
