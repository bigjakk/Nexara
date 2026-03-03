package handlers

import (
	"errors"
	"net/mail"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/proxdash/proxdash/internal/auth"
	db "github.com/proxdash/proxdash/internal/db/generated"
)

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	queries        *db.Queries
	jwtService     *auth.JWTService
	sessionManager *auth.SessionManager
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(queries *db.Queries, jwtSvc *auth.JWTService, sessMgr *auth.SessionManager) *AuthHandler {
	return &AuthHandler{
		queries:        queries,
		jwtService:     jwtSvc,
		sessionManager: sessMgr,
	}
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
	User         userResponse `json:"user"`
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresAt    int64        `json:"expires_at"`
}

type userResponse struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
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

	return c.Status(fiber.StatusCreated).JSON(authResponse{
		User: userResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt.Unix(),
	})
}

// Login handles user authentication.
func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Email == "" || req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Email and password are required")
	}

	invalidCredentials := fiber.NewError(fiber.StatusUnauthorized, "Invalid email or password")

	user, err := h.queries.GetUserByEmail(c.Context(), req.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidCredentials
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to look up user")
	}

	if !user.IsActive {
		return invalidCredentials
	}

	if err := auth.CheckPassword(user.PasswordHash, req.Password); err != nil {
		return invalidCredentials
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

	return c.JSON(authResponse{
		User: userResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt.Unix(),
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

	return c.JSON(authResponse{
		User: userResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		ExpiresAt:    expiresAt.Unix(),
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

	return c.JSON(fiber.Map{"message": "All sessions revoked"})
}

// isDuplicateKeyError checks if a pgx error is a unique constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
