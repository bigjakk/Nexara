package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	db "github.com/proxdash/proxdash/internal/db/generated"
)

const (
	rbacCachePrefix = "proxdash:rbac:"
	rbacCacheTTL    = 5 * time.Minute
)

// ScopedPermission is a permission with scope context.
type ScopedPermission struct {
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	ScopeType string `json:"scope_type"`
	ScopeID   string `json:"scope_id,omitempty"`
}

// UserPermissions holds the resolved permissions for a user.
type UserPermissions struct {
	Permissions []ScopedPermission `json:"permissions"`
}

// RBACEngine evaluates permissions for users against the RBAC tables.
type RBACEngine struct {
	queries *db.Queries
	redis   *redis.Client
}

// NewRBACEngine creates a new RBAC engine.
func NewRBACEngine(queries *db.Queries, rdb *redis.Client) *RBACEngine {
	return &RBACEngine{
		queries: queries,
		redis:   rdb,
	}
}

// HasPermission checks if a user has a specific permission at the given scope.
// For global checks, pass scopeType="global" and scopeID=uuid.Nil.
func (e *RBACEngine) HasPermission(ctx context.Context, userID uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error) {
	perms, err := e.LoadUserPermissions(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("loading user permissions: %w", err)
	}

	for _, p := range perms.Permissions {
		if p.Action != action || p.Resource != resource {
			continue
		}
		// Global scope covers everything
		if p.ScopeType == "global" {
			return true, nil
		}
		// Cluster scope matches if types and IDs match
		if p.ScopeType == scopeType && p.ScopeID == scopeID.String() {
			return true, nil
		}
	}

	return false, nil
}

// HasGlobalPermission is a convenience for checking a global-scoped permission.
func (e *RBACEngine) HasGlobalPermission(ctx context.Context, userID uuid.UUID, action, resource string) (bool, error) {
	return e.HasPermission(ctx, userID, action, resource, "global", uuid.Nil)
}

// LoadUserPermissions loads all permissions for a user, using Redis cache.
func (e *RBACEngine) LoadUserPermissions(ctx context.Context, userID uuid.UUID) (*UserPermissions, error) {
	cacheKey := rbacCachePrefix + userID.String()

	// Try cache first
	if e.redis != nil {
		data, err := e.redis.Get(ctx, cacheKey).Bytes()
		if err == nil {
			var perms UserPermissions
			if json.Unmarshal(data, &perms) == nil {
				return &perms, nil
			}
		}
	}

	// Load from DB
	rows, err := e.queries.GetUserScopedPermissions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("querying user scoped permissions: %w", err)
	}

	perms := &UserPermissions{
		Permissions: make([]ScopedPermission, 0, len(rows)),
	}
	for _, r := range rows {
		sp := ScopedPermission{
			Action:    r.Action,
			Resource:  r.Resource,
			ScopeType: r.ScopeType,
		}
		if r.ScopeID.Valid {
			sid, _ := uuid.FromBytes(r.ScopeID.Bytes[:])
			sp.ScopeID = sid.String()
		}
		perms.Permissions = append(perms.Permissions, sp)
	}

	// Cache in Redis
	if e.redis != nil {
		data, err := json.Marshal(perms)
		if err == nil {
			_ = e.redis.Set(ctx, cacheKey, data, rbacCacheTTL).Err()
		}
	}

	return perms, nil
}

// GetFlatPermissions returns a simple list of "action:resource" strings for the user (global only).
func (e *RBACEngine) GetFlatPermissions(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := e.queries.GetUserPermissions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("querying user permissions: %w", err)
	}
	result := make([]string, 0, len(rows))
	for _, r := range rows {
		result = append(result, r.Action+":"+r.Resource)
	}
	return result, nil
}

// InvalidateUser clears the cached permissions for a user.
func (e *RBACEngine) InvalidateUser(ctx context.Context, userID uuid.UUID) {
	if e.redis != nil {
		_ = e.redis.Del(ctx, rbacCachePrefix+userID.String()).Err()
	}
}

// CheckPermissionDB is a direct DB check (bypasses cache) for a specific scoped permission.
func (e *RBACEngine) CheckPermissionDB(ctx context.Context, userID uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error) {
	var pgScopeID pgtype.UUID
	if scopeID != uuid.Nil {
		pgScopeID = pgtype.UUID{Bytes: scopeID, Valid: true}
	}
	return e.queries.CheckUserPermission(ctx, db.CheckUserPermissionParams{
		UserID:    userID,
		Action:    action,
		Resource:  resource,
		ScopeType: scopeType,
		ScopeID:   pgScopeID,
	})
}
