-- 000051_audit_log_user_set_null.down.sql
-- Reverts the FK back to ON DELETE CASCADE and re-enforces NOT NULL.
-- Note: down requires that no audit rows currently have user_id IS NULL
-- (the NOT NULL re-add will fail otherwise). This is a one-way change
-- in practice once orphaned rows accumulate.

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_user_id_fkey;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE audit_log ALTER COLUMN user_id SET NOT NULL;
