-- 000016_rbac.down.sql
-- Reverse RBAC tables

DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS permissions;
DROP TRIGGER IF EXISTS trg_roles_updated_at ON roles;
DROP TABLE IF EXISTS roles;
