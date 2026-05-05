-- 000051_audit_log_user_set_null.up.sql
-- Preserve audit history when a user is deleted.
--
-- Previously audit_log.user_id was NOT NULL with ON DELETE CASCADE,
-- which meant deleting a user wiped every action they ever took from
-- the audit trail. That's the opposite of what an audit log is for.
--
-- This migration relaxes the column to NULL-able and switches the FK
-- to ON DELETE SET NULL so audit rows survive user deletion. The
-- corresponding user_id is set to NULL on the surviving rows; the
-- handler-level enrichment continues to record the user's email /
-- display name in the details JSON at the time of the action, so the
-- attribution is still visible in the row even after the user is gone.

ALTER TABLE audit_log ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_user_id_fkey;

ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;
