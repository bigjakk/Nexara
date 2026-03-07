-- name: ListRoles :many
SELECT * FROM roles ORDER BY is_builtin DESC, name;

-- name: GetRole :one
SELECT * FROM roles WHERE id = $1;

-- name: GetRoleByName :one
SELECT * FROM roles WHERE name = $1;

-- name: CreateRole :one
INSERT INTO roles (name, description, is_builtin)
VALUES ($1, $2, false)
RETURNING *;

-- name: UpdateRole :one
UPDATE roles
SET name = $2, description = $3
WHERE id = $1 AND is_builtin = false
RETURNING *;

-- name: DeleteRole :exec
DELETE FROM roles WHERE id = $1 AND is_builtin = false;

-- name: ListPermissions :many
SELECT * FROM permissions ORDER BY resource, action;

-- name: GetPermission :one
SELECT * FROM permissions WHERE id = $1;

-- name: GetPermissionByActionResource :one
SELECT * FROM permissions WHERE action = $1 AND resource = $2;

-- name: ListRolePermissions :many
SELECT p.* FROM permissions p
JOIN role_permissions rp ON rp.permission_id = p.id
WHERE rp.role_id = $1
ORDER BY p.resource, p.action;

-- name: AddRolePermission :exec
INSERT INTO role_permissions (role_id, permission_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RemoveRolePermission :exec
DELETE FROM role_permissions WHERE role_id = $1 AND permission_id = $2;

-- name: SetRolePermissions :exec
DELETE FROM role_permissions WHERE role_id = $1;

-- name: ListUserRoles :many
SELECT ur.id, ur.user_id, ur.role_id, ur.scope_type, ur.scope_id, ur.created_at,
       r.name AS role_name, r.description AS role_description, r.is_builtin
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
WHERE ur.user_id = $1
ORDER BY r.name;

-- name: AssignUserRole :one
INSERT INTO user_roles (user_id, role_id, scope_type, scope_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: RevokeUserRole :exec
DELETE FROM user_roles WHERE id = $1 AND user_id = $2;

-- name: RevokeAllUserRoles :exec
DELETE FROM user_roles WHERE user_id = $1;

-- name: GetUserPermissions :many
SELECT DISTINCT p.action, p.resource
FROM user_roles ur
JOIN role_permissions rp ON rp.role_id = ur.role_id
JOIN permissions p ON p.id = rp.permission_id
WHERE ur.user_id = $1
ORDER BY p.resource, p.action;

-- name: GetUserScopedPermissions :many
SELECT DISTINCT p.action, p.resource, ur.scope_type, ur.scope_id
FROM user_roles ur
JOIN role_permissions rp ON rp.role_id = ur.role_id
JOIN permissions p ON p.id = rp.permission_id
WHERE ur.user_id = $1
ORDER BY p.resource, p.action;

-- name: CheckUserPermission :one
SELECT EXISTS(
    SELECT 1
    FROM user_roles ur
    JOIN role_permissions rp ON rp.role_id = ur.role_id
    JOIN permissions p ON p.id = rp.permission_id
    WHERE ur.user_id = $1
      AND p.action = $2
      AND p.resource = $3
      AND (
          ur.scope_type = 'global'
          OR (ur.scope_type = $4 AND ur.scope_id = $5)
      )
) AS has_permission;

-- name: ListUserIDsByRole :many
SELECT DISTINCT user_id FROM user_roles WHERE role_id = $1;

-- name: ListUsersWithRoles :many
SELECT u.id, u.email, u.display_name, u.role, u.is_active, u.created_at, u.updated_at, u.auth_source
FROM users u
WHERE u.id != '00000000-0000-0000-0000-000000000000'
ORDER BY u.created_at DESC;

-- name: UpdateUserProfile :one
UPDATE users
SET display_name = $2, is_active = $3, role = $4
WHERE id = $1
RETURNING *;
