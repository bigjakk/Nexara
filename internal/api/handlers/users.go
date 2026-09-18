package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// All 4 routes are declared in internal/api/registry_users.go, which states
// their parameters and, for three of them, their permission. The fourth —
// Update — is Deferred and keeps BOTH of its checks here, because the second
// one fires only when the body carries `role`; see userUpdateReason.
//
// What else stays here is what a declaration cannot see: the refusal to touch
// the system account, the refusal to change your own role or active status, the
// refusal to delete your own account, and the session revocation that has to
// follow a deactivation.

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
	Roles       []string  `json:"roles"`
	IsActive    bool      `json:"is_active"`
	AuthSource  string    `json:"auth_source"`
	TotpEnabled bool      `json:"totp_enabled"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
}

// List handles GET /api/v1/users.
func (h *UserHandler) List(c fiber.Ctx, _ *apischema.Params) error {
	users, err := h.queries.ListUsersWithRoles(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list users")
	}

	resp := make([]userListResponse, len(users))
	for i, u := range users {
		var roles []string
		if rbacStr, ok := u.RbacRoles.(string); ok && rbacStr != "" {
			roles = strings.Split(rbacStr, ", ")
		}
		if roles == nil {
			roles = []string{}
		}
		resp[i] = userListResponse{
			ID:          u.ID,
			Email:       u.Email,
			DisplayName: u.DisplayName,
			Role:        u.Role,
			Roles:       roles,
			IsActive:    u.IsActive,
			AuthSource:  u.AuthSource,
			TotpEnabled: u.TotpEnabled,
			CreatedAt:   u.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt:   u.UpdatedAt.Format(time.RFC3339Nano),
		}
	}

	return RespondItems(c, resp)
}

// Get handles GET /api/v1/users/:id.
func (h *UserHandler) Get(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
		CreatedAt:   user.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   user.UpdatedAt.Format(time.RFC3339Nano),
	})
}

// Update handles PUT /api/v1/users/:id.
func (h *UserHandler) Update(c fiber.Ctx, p *apischema.Params) error {
	// Deferred, so BOTH checks live here. This one is unconditional and runs
	// before anything is read or written; the manage:role one below fires only
	// when the body carries `role`. See userUpdateReason in
	// internal/api/registry_users.go for why the pair is not a static Check.
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	if id == auth.SystemUserID {
		return fiber.NewError(fiber.StatusForbidden, "Cannot modify the system account")
	}

	existing, err := h.queries.GetUserByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "User not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get user")
	}

	// Tristate reads throughout: every refusal below keys on the field having
	// been SUPPLIED, not on its value, so a request that never mentioned
	// is_active must not be read as "set it to false".
	displayName := existing.DisplayName
	isActive := existing.IsActive
	role := existing.Role

	callerID, _ := c.Locals("user_id").(uuid.UUID)

	if v, supplied := p.OptString("display_name"); supplied {
		displayName = v
	}
	deactivating := false
	if v, supplied := p.OptBool("is_active"); supplied {
		if callerID == id {
			return fiber.NewError(fiber.StatusForbidden, "Cannot change your own active status")
		}
		isActive = v
		deactivating = !v
	}
	if v, supplied := p.OptString("role"); supplied {
		if callerID == id {
			return fiber.NewError(fiber.StatusForbidden, "Cannot change your own role")
		}
		// The second, CONDITIONAL half of this route's authorization — the
		// reason it is declared Deferred rather than Check. The vocabulary
		// check that used to follow is now the schema's enum.
		if err := requirePerm(c, "manage", "role"); err != nil {
			return fiber.NewError(fiber.StatusForbidden, "Only role managers can change user roles")
		}
		role = v
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
	if deactivating && h.sessions != nil {
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
		CreatedAt:   user.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   user.UpdatedAt.Format(time.RFC3339Nano),
	})
}

// Delete handles DELETE /api/v1/users/:id.
func (h *UserHandler) Delete(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	if id == auth.SystemUserID {
		return fiber.NewError(fiber.StatusForbidden, "Cannot delete the system account")
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
