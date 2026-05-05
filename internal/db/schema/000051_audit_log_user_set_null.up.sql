-- 000051_audit_log_user_set_null.up.sql
-- See migrations/000051_audit_log_user_set_null.up.sql for full rationale.
-- Idempotent: DROP NOT NULL is a no-op if already nullable, the FK is
-- dropped+recreated each run.

ALTER TABLE audit_log ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_user_id_fkey;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;
