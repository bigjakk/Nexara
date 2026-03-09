package handlers

import (
	"encoding/json"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// RBACHandler handles role and permission management endpoints.
type RBACHandler struct {
	queries  *db.Queries
	rbac     *auth.RBACEngine
	eventPub *events.Publisher
}

// NewRBACHandler creates a new RBAC handler.
func NewRBACHandler(queries *db.Queries, rbac *auth.RBACEngine, eventPub *events.Publisher) *RBACHandler {
	return &RBACHandler{
		queries:  queries,
		rbac:     rbac,
		eventPub: eventPub,
	}
}

// invalidateRoleUsers clears the RBAC cache for all users holding a given role.
func (h *RBACHandler) invalidateRoleUsers(c *fiber.Ctx, roleID uuid.UUID) {
	userIDs, err := h.queries.ListUserIDsByRole(c.Context(), roleID)
	if err != nil {
		return
	}
	for _, uid := range userIDs {
		h.rbac.InvalidateUser(c.Context(), uid)
	}
}

// -- Role CRUD --

type roleResponse struct {
	ID          uuid.UUID            `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	IsBuiltin   bool                 `json:"is_builtin"`
	Permissions []permissionResponse `json:"permissions,omitempty"`
	CreatedAt   string               `json:"created_at"`
	UpdatedAt   string               `json:"updated_at"`
}

type permissionResponse struct {
	ID          uuid.UUID `json:"id"`
	Action      string    `json:"action"`
	Resource    string    `json:"resource"`
	Description string    `json:"description"`
}

type createRoleRequest struct {
	Name          string      `json:"name"`
	Description   string      `json:"description"`
	PermissionIDs []uuid.UUID `json:"permission_ids"`
}

type updateRoleRequest struct {
	Name          *string      `json:"name"`
	Description   *string      `json:"description"`
	PermissionIDs *[]uuid.UUID `json:"permission_ids"`
}

// ListRoles handles GET /api/v1/rbac/roles.
func (h *RBACHandler) ListRoles(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "role"); err != nil {
		return err
	}

	roles, err := h.queries.ListRoles(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list roles")
	}

	resp := make([]roleResponse, len(roles))
	for i, r := range roles {
		resp[i] = roleResponse{
			ID:          r.ID,
			Name:        r.Name,
			Description: r.Description,
			IsBuiltin:   r.IsBuiltin,
			CreatedAt:   r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			UpdatedAt:   r.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}

	return c.JSON(resp)
}

// GetRole handles GET /api/v1/rbac/roles/:id.
func (h *RBACHandler) GetRole(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "role"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid role ID")
	}

	role, err := h.queries.GetRole(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Role not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get role")
	}

	perms, err := h.queries.ListRolePermissions(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list role permissions")
	}

	permResp := make([]permissionResponse, len(perms))
	for i, p := range perms {
		permResp[i] = permissionResponse{
			ID:          p.ID,
			Action:      p.Action,
			Resource:    p.Resource,
			Description: p.Description,
		}
	}

	return c.JSON(roleResponse{
		ID:          role.ID,
		Name:        role.Name,
		Description: role.Description,
		IsBuiltin:   role.IsBuiltin,
		Permissions: permResp,
		CreatedAt:   role.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   role.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// CreateRole handles POST /api/v1/rbac/roles.
func (h *RBACHandler) CreateRole(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "role"); err != nil {
		return err
	}

	var req createRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Name is required")
	}

	role, err := h.queries.CreateRole(c.Context(), db.CreateRoleParams{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			return fiber.NewError(fiber.StatusConflict, "Role name already exists")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create role")
	}

	for _, pid := range req.PermissionIDs {
		if err := h.queries.AddRolePermission(c.Context(), db.AddRolePermissionParams{
			RoleID:       role.ID,
			PermissionID: pid,
		}); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid permission ID: "+pid.String())
		}
	}

	details, _ := json.Marshal(map[string]string{"role": role.Name})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "role", role.ID.String(), "role_created", details)

	return c.Status(fiber.StatusCreated).JSON(roleResponse{
		ID:          role.ID,
		Name:        role.Name,
		Description: role.Description,
		IsBuiltin:   role.IsBuiltin,
		CreatedAt:   role.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   role.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// UpdateRole handles PUT /api/v1/rbac/roles/:id.
func (h *RBACHandler) UpdateRole(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "role"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid role ID")
	}

	existing, err := h.queries.GetRole(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Role not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get role")
	}

	if existing.IsBuiltin {
		return fiber.NewError(fiber.StatusForbidden, "Cannot modify built-in roles")
	}

	var req updateRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	name := existing.Name
	description := existing.Description
	if req.Name != nil {
		name = *req.Name
	}
	if req.Description != nil {
		description = *req.Description
	}

	role, err := h.queries.UpdateRole(c.Context(), db.UpdateRoleParams{
		ID:          id,
		Name:        name,
		Description: description,
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			return fiber.NewError(fiber.StatusConflict, "Role name already exists")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update role")
	}

	if req.PermissionIDs != nil {
		if err := h.queries.SetRolePermissions(c.Context(), id); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to clear role permissions")
		}
		for _, pid := range *req.PermissionIDs {
			if err := h.queries.AddRolePermission(c.Context(), db.AddRolePermissionParams{
				RoleID:       id,
				PermissionID: pid,
			}); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "Invalid permission ID: "+pid.String())
			}
		}
	}

	// Invalidate cache for all users holding this role
	h.invalidateRoleUsers(c, id)

	details, _ := json.Marshal(map[string]string{"role": role.Name})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "role", role.ID.String(), "role_updated", details)

	return c.JSON(roleResponse{
		ID:          role.ID,
		Name:        role.Name,
		Description: role.Description,
		IsBuiltin:   role.IsBuiltin,
		CreatedAt:   role.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   role.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// DeleteRole handles DELETE /api/v1/rbac/roles/:id.
func (h *RBACHandler) DeleteRole(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "role"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid role ID")
	}

	role, err := h.queries.GetRole(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Role not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get role")
	}

	if role.IsBuiltin {
		return fiber.NewError(fiber.StatusForbidden, "Cannot delete built-in roles")
	}

	// Invalidate cache for all users holding this role before deletion
	h.invalidateRoleUsers(c, id)

	if err := h.queries.DeleteRole(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete role")
	}

	details, _ := json.Marshal(map[string]string{"role": role.Name})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "role", id.String(), "role_deleted", details)

	return c.SendStatus(fiber.StatusNoContent)
}

// -- Permissions --

// ListPermissions handles GET /api/v1/rbac/permissions.
func (h *RBACHandler) ListPermissions(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "role"); err != nil {
		return err
	}

	perms, err := h.queries.ListPermissions(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list permissions")
	}

	resp := make([]permissionResponse, len(perms))
	for i, p := range perms {
		resp[i] = permissionResponse{
			ID:          p.ID,
			Action:      p.Action,
			Resource:    p.Resource,
			Description: p.Description,
		}
	}

	return c.JSON(resp)
}

// -- User Role Assignments --

type userRoleResponse struct {
	ID              uuid.UUID `json:"id"`
	UserID          uuid.UUID `json:"user_id"`
	RoleID          uuid.UUID `json:"role_id"`
	RoleName        string    `json:"role_name"`
	RoleDescription string    `json:"role_description"`
	IsBuiltin       bool      `json:"is_builtin"`
	ScopeType       string    `json:"scope_type"`
	ScopeID         string    `json:"scope_id,omitempty"`
	CreatedAt       string    `json:"created_at"`
}

type assignRoleRequest struct {
	RoleID    uuid.UUID `json:"role_id"`
	ScopeType string    `json:"scope_type"`
	ScopeID   string    `json:"scope_id,omitempty"`
}

// ListUserRoles handles GET /api/v1/rbac/users/:user_id/roles.
func (h *RBACHandler) ListUserRoles(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "role"); err != nil {
		return err
	}

	userID, err := uuid.Parse(c.Params("user_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid user ID")
	}

	rows, err := h.queries.ListUserRoles(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list user roles")
	}

	resp := make([]userRoleResponse, len(rows))
	for i, r := range rows {
		resp[i] = userRoleResponse{
			ID:              r.ID,
			UserID:          r.UserID,
			RoleID:          r.RoleID,
			RoleName:        r.RoleName,
			RoleDescription: r.RoleDescription,
			IsBuiltin:       r.IsBuiltin,
			ScopeType:       r.ScopeType,
			CreatedAt:       r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if r.ScopeID.Valid {
			sid, _ := uuid.FromBytes(r.ScopeID.Bytes[:])
			resp[i].ScopeID = sid.String()
		}
	}

	return c.JSON(resp)
}

// AssignUserRole handles POST /api/v1/rbac/users/:user_id/roles.
func (h *RBACHandler) AssignUserRole(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "role"); err != nil {
		return err
	}

	userID, err := uuid.Parse(c.Params("user_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid user ID")
	}

	var req assignRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.RoleID == uuid.Nil {
		return fiber.NewError(fiber.StatusBadRequest, "role_id is required")
	}
	if req.ScopeType == "" {
		req.ScopeType = "global"
	}
	if req.ScopeType != "global" && req.ScopeType != "cluster" {
		return fiber.NewError(fiber.StatusBadRequest, "scope_type must be 'global' or 'cluster'")
	}

	var scopeID pgtype.UUID
	if req.ScopeType == "cluster" {
		sid, err := uuid.Parse(req.ScopeID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "scope_id is required for cluster scope")
		}
		scopeID = pgtype.UUID{Bytes: sid, Valid: true}
	}

	assignment, err := h.queries.AssignUserRole(c.Context(), db.AssignUserRoleParams{
		UserID:    userID,
		RoleID:    req.RoleID,
		ScopeType: req.ScopeType,
		ScopeID:   scopeID,
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			return fiber.NewError(fiber.StatusConflict, "Role already assigned with this scope")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to assign role")
	}

	h.rbac.InvalidateUser(c.Context(), userID)

	details, _ := json.Marshal(map[string]interface{}{
		"user_id":    userID.String(),
		"role_id":    req.RoleID.String(),
		"scope_type": req.ScopeType,
	})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "role", assignment.ID.String(), "role_assigned", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":         assignment.ID,
		"user_id":    assignment.UserID,
		"role_id":    assignment.RoleID,
		"scope_type": assignment.ScopeType,
		"created_at": assignment.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// RevokeUserRole handles DELETE /api/v1/rbac/users/:user_id/roles/:id.
func (h *RBACHandler) RevokeUserRole(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "role"); err != nil {
		return err
	}

	userID, err := uuid.Parse(c.Params("user_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid user ID")
	}

	assignmentID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid assignment ID")
	}

	if err := h.queries.RevokeUserRole(c.Context(), db.RevokeUserRoleParams{
		ID:     assignmentID,
		UserID: userID,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revoke role")
	}

	h.rbac.InvalidateUser(c.Context(), userID)

	details, _ := json.Marshal(map[string]string{
		"user_id":       userID.String(),
		"assignment_id": assignmentID.String(),
	})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "role", assignmentID.String(), "role_revoked", details)

	return c.SendStatus(fiber.StatusNoContent)
}

// -- Current User Permissions --

// MyPermissions handles GET /api/v1/rbac/me/permissions.
func (h *RBACHandler) MyPermissions(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	perms, err := h.rbac.GetFlatPermissions(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get permissions")
	}

	roles, err := h.queries.ListUserRoles(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list roles")
	}

	roleResp := make([]userRoleResponse, len(roles))
	for i, r := range roles {
		roleResp[i] = userRoleResponse{
			ID:              r.ID,
			UserID:          r.UserID,
			RoleID:          r.RoleID,
			RoleName:        r.RoleName,
			RoleDescription: r.RoleDescription,
			IsBuiltin:       r.IsBuiltin,
			ScopeType:       r.ScopeType,
			CreatedAt:       r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if r.ScopeID.Valid {
			sid, _ := uuid.FromBytes(r.ScopeID.Bytes[:])
			roleResp[i].ScopeID = sid.String()
		}
	}

	return c.JSON(fiber.Map{
		"permissions": perms,
		"roles":       roleResp,
	})
}
