package handlers

import (
	"encoding/json"
	"errors"
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

// All 10 routes are declared in internal/api/registry_rbac.go, which states
// their permission and their parameters; nothing below re-checks either. Nine
// are a global Check (view:role or manage:role) and the tenth, MyPermissions,
// is SelfService — it reads the caller's own id from the session.
//
// What stays here is what the declaration cannot see: the refusal to edit or
// delete a built-in role, the refusal to touch the system account, and the
// RBAC cache invalidation that has to follow every grant change.

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

// parseRolePermissionIDs reads the permission_ids array both role writes
// carry.
//
// The uuid FORMAT has already run on every element, so this cannot fail on a
// well-formed request — but Strings() hands back the string form and the
// queries take uuid.UUID, and silently dropping an element that failed to
// parse would create a role granting less than the caller asked for with no
// indication. It returns the caller-facing 400 instead.
//
// It does NOT decide whether the set was supplied: an empty array and an
// absent key both come back as an empty slice, and only p.Has can tell "strip
// every permission" from "leave them alone". See UpdateRole.
func parseRolePermissionIDs(p *apischema.Params) ([]uuid.UUID, error) {
	raw := p.Strings("permission_ids")
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fiber.NewError(fiber.StatusBadRequest, "Invalid permission ID: "+s)
		}
		out = append(out, id)
	}
	return out, nil
}

// invalidateRoleUsers clears the RBAC cache for all users holding a given role.
func (h *RBACHandler) invalidateRoleUsers(c fiber.Ctx, roleID uuid.UUID) {
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

// ListRoles handles GET /api/v1/rbac/roles.
func (h *RBACHandler) ListRoles(c fiber.Ctx, _ *apischema.Params) error {
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
			CreatedAt:   r.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt:   r.UpdatedAt.Format(time.RFC3339Nano),
		}
	}

	return RespondItems(c, resp)
}

// GetRole handles GET /api/v1/rbac/roles/:id.
func (h *RBACHandler) GetRole(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
	for i, perm := range perms {
		permResp[i] = permissionResponse{
			ID:          perm.ID,
			Action:      perm.Action,
			Resource:    perm.Resource,
			Description: perm.Description,
		}
	}

	return c.JSON(roleResponse{
		ID:          role.ID,
		Name:        role.Name,
		Description: role.Description,
		IsBuiltin:   role.IsBuiltin,
		Permissions: permResp,
		CreatedAt:   role.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   role.UpdatedAt.Format(time.RFC3339Nano),
	})
}

// CreateRole handles POST /api/v1/rbac/roles.
func (h *RBACHandler) CreateRole(c fiber.Ctx, p *apischema.Params) error {
	permissionIDs, err := parseRolePermissionIDs(p)
	if err != nil {
		return err
	}

	role, err := h.queries.CreateRole(c.Context(), db.CreateRoleParams{
		Name:        p.String("name"),
		Description: p.String("description"),
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			return fiber.NewError(fiber.StatusConflict, "Role name already exists")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create role")
	}

	for _, pid := range permissionIDs {
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
		CreatedAt:   role.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   role.UpdatedAt.Format(time.RFC3339Nano),
	})
}

// UpdateRole handles PUT /api/v1/rbac/roles/:id.
func (h *RBACHandler) UpdateRole(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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

	// Tristate reads, not zero-value tests: an omitted key leaves the stored
	// value alone and an EMPTY one is a deliberate clear. p.OptString is what
	// keeps those apart, the way the *string fields on the old request struct
	// did.
	name := existing.Name
	if v, supplied := p.OptString("name"); supplied {
		name = v
	}
	description := existing.Description
	if v, supplied := p.OptString("description"); supplied {
		description = v
	}
	permissionIDs, err := parseRolePermissionIDs(p)
	if err != nil {
		return err
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

	// p.Has, not len(permissionIDs): an EMPTY array is how a caller strips
	// every permission from a role, and an absent key is how they leave the
	// set alone. Testing the length would collapse the two and make the
	// deliberate clear unexpressible.
	if p.Has("permission_ids") {
		if err := h.queries.SetRolePermissions(c.Context(), id); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to clear role permissions")
		}
		for _, pid := range permissionIDs {
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
		CreatedAt:   role.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   role.UpdatedAt.Format(time.RFC3339Nano),
	})
}

// DeleteRole handles DELETE /api/v1/rbac/roles/:id.
func (h *RBACHandler) DeleteRole(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
func (h *RBACHandler) ListPermissions(c fiber.Ctx, _ *apischema.Params) error {
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

	return RespondItems(c, resp)
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

// ListUserRoles handles GET /api/v1/rbac/users/:user_id/roles.
func (h *RBACHandler) ListUserRoles(c fiber.Ctx, p *apischema.Params) error {
	userID, err := parseParamUUID(p.String("user_id"))
	if err != nil {
		return err
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
			CreatedAt:       r.CreatedAt.Format(time.RFC3339Nano),
		}
		if r.ScopeID.Valid {
			sid, _ := uuid.FromBytes(r.ScopeID.Bytes[:])
			resp[i].ScopeID = sid.String()
		}
	}

	return RespondItems(c, resp)
}

// AssignUserRole handles POST /api/v1/rbac/users/:user_id/roles.
func (h *RBACHandler) AssignUserRole(c fiber.Ctx, p *apischema.Params) error {
	userID, err := parseParamUUID(p.String("user_id"))
	if err != nil {
		return err
	}

	if userID == auth.SystemUserID {
		return fiber.NewError(fiber.StatusForbidden, "Cannot modify the system account")
	}

	roleID, err := uuid.Parse(p.String("role_id"))
	// The uuid format has already run, so the parse cannot fail — but the
	// all-zeros uuid satisfies it, and assigning "no role" is not something a
	// caller can have meant.
	if err != nil || roleID == uuid.Nil {
		return fiber.NewError(fiber.StatusBadRequest, "role_id is required")
	}

	// Cross-field, so it stays here: apischema's Requires names a companion a
	// parameter ALWAYS needs, not one it needs only for a particular value of
	// another parameter.
	scopeType := p.String("scope_type")
	var scopeID pgtype.UUID
	if scopeType == "cluster" {
		sid, parseErr := uuid.Parse(p.String("scope_id"))
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "scope_id is required for cluster scope")
		}
		scopeID = pgtype.UUID{Bytes: sid, Valid: true}
	}

	assignment, err := h.queries.AssignUserRole(c.Context(), db.AssignUserRoleParams{
		UserID:    userID,
		RoleID:    roleID,
		ScopeType: scopeType,
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
		"role_id":    roleID.String(),
		"scope_type": scopeType,
	})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "role", assignment.ID.String(), "role_assigned", details)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":         assignment.ID,
		"user_id":    assignment.UserID,
		"role_id":    assignment.RoleID,
		"scope_type": assignment.ScopeType,
		"created_at": assignment.CreatedAt.Format(time.RFC3339Nano),
	})
}

// RevokeUserRole handles DELETE /api/v1/rbac/users/:user_id/roles/:id.
func (h *RBACHandler) RevokeUserRole(c fiber.Ctx, p *apischema.Params) error {
	userID, err := parseParamUUID(p.String("user_id"))
	if err != nil {
		return err
	}

	if userID == auth.SystemUserID {
		return fiber.NewError(fiber.StatusForbidden, "Cannot modify the system account")
	}

	assignmentID, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
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
func (h *RBACHandler) MyPermissions(c fiber.Ctx, _ *apischema.Params) error {
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
			CreatedAt:       r.CreatedAt.Format(time.RFC3339Nano),
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
