-- 000003_sessions.down.sql
-- Reverse: drop sessions table and role column

DROP TABLE IF EXISTS sessions;
ALTER TABLE users DROP COLUMN IF EXISTS role;
