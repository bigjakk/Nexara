package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// UserHandler handles user management endpoints.
type UserHandler struct {
	queries  *db.Queries
	rbac     *auth.RBACEngine
	eventPub *events.Publisher
	sessions *auth.SessionManager
}

// NewUserHandler creates a new user handler.
func NewUserHandler(queries *db.Queries, rbac *auth.RBACEngine, eventPub *events.Publisher, sessions *auth.SessionManager) *UserHandler {
	return &UserHandler{
		queries:  queries,
		rbac:     rbac,
		eventPub: eventPub,
		sessions: sessions,
	}
}

type userListResponse struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	IsActive    bool      `json:"is_active"`
	AuthSource  string    `json:"auth_source"`
	TotpEnabled bool      `json:"totp_enabled"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
}

type updateUserRequest struct {
	DisplayName *string `json:"display_name"`
	IsActive    *bool   `json:"is_active"`
	Role        *string `json:"role"`
}

// List handles GET /api/v1/users.
func (h *UserHandler) List(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "user"); err != nil {
		return err
	}

	users, err := h.queries.ListUsersWithRoles(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list users")
	}

	resp := make([]userListResponse, len(users))
	for i, u := range users {
		resp[i] = userListResponse{
			ID:          u.ID,
			Email:       u.Email,
			DisplayName: u.DisplayName,
			Role:        u.Role,
			IsActive:    u.IsActive,
			AuthSource:  u.AuthSource,
			TotpEnabled: u.TotpEnabled,
			CreatedAt:   u.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			UpdatedAt:   u.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}

	return c.JSON(resp)
}

// Get handles GET /api/v1/users/:id.
func (h *UserHandler) Get(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid user ID")
	}

	user, err := h.queries.GetUserByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "User not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get user")
	}

	return c.JSON(userListResponse{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		IsActive:    user.IsActive,
		AuthSource:  user.AuthSource,
		TotpEnabled: user.TotpSecret.Valid,
		CreatedAt:   user.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   user.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// Update handles PUT /api/v1/users/:id.
func (h *UserHandler) Update(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid user ID")
	}

	existing, err := h.queries.GetUserByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "User not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get user")
	}

	var req updateUserRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	displayName := existing.DisplayName
	isActive := existing.IsActive
	role := existing.Role

	callerID, _ := c.Locals("user_id").(uuid.UUID)

	if req.DisplayName != nil {
		displayName = *req.DisplayName
	}
	if req.IsActive != nil {
		if callerID == id {
			return fiber.NewError(fiber.StatusForbidden, "Cannot change your own active status")
		}
		isActive = *req.IsActive
	}
	if req.Role != nil {
		if callerID == id {
			return fiber.NewError(fiber.StatusForbidden, "Cannot change your own role")
		}
		if err := requirePerm(c, "manage", "role"); err != nil {
			return fiber.NewError(fiber.StatusForbidden, "Only role managers can change user roles")
		}
		if *req.Role != "admin" && *req.Role != "user" {
			return fiber.NewError(fiber.StatusBadRequest, "Role must be 'admin' or 'user'")
		}
		role = *req.Role
	}

	user, err := h.queries.UpdateUserProfile(c.Context(), db.UpdateUserProfileParams{
		ID:          id,
		DisplayName: displayName,
		IsActive:    isActive,
		Role:        role,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update user")
	}

	h.rbac.InvalidateUser(c.Context(), id)

	// If the user was deactivated, immediately revoke all their sessions
	// so the change takes effect without waiting for token expiry.
	if req.IsActive != nil && !*req.IsActive && h.sessions != nil {
		if err := h.sessions.RevokeAllUserSessions(c.Context(), id); err != nil {
			slog.Error("failed to revoke sessions for deactivated user", "user_id", id, "error", err)
		}
	}

	details, _ := json.Marshal(map[string]string{"email": user.Email})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "user", id.String(), "user_updated", details)

	return c.JSON(userListResponse{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		IsActive:    user.IsActive,
		AuthSource:  user.AuthSource,
		TotpEnabled: user.TotpSecret.Valid,
		CreatedAt:   user.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   user.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// Delete handles DELETE /api/v1/users/:id.
func (h *UserHandler) Delete(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid user ID")
	}

	// Prevent self-deletion
	callerID, _ := c.Locals("user_id").(uuid.UUID)
	if callerID == id {
		return fiber.NewError(fiber.StatusForbidden, "Cannot delete your own account")
	}

	user, err := h.queries.GetUserByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "User not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get user")
	}

	if err := h.queries.DeleteUser(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete user")
	}

	details, _ := json.Marshal(map[string]string{"email": user.Email})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "user", id.String(), "user_deleted", details)

	return c.SendStatus(fiber.StatusNoContent)
}
