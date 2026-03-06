-- 000013_system_user.up.sql
-- Create a system user for DRS and scheduler-initiated actions that need audit/task logging.

INSERT INTO users (id, email, password_hash, display_name, is_active, role)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'system@proxdash.local',
    '', -- no password, cannot log in
    'DRS Scheduler',
    false, -- cannot log in
    'admin'
)
ON CONFLICT (id) DO NOTHING;
