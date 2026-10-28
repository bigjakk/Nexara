-- 000127_redact_audit_secrets.down.sql
-- No-op: the up migration replaced secrets with "(set)" or "(cleared)" and kept
-- the values nowhere, so there is nothing to restore.
SELECT 1;
