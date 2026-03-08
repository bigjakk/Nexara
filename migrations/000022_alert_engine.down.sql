DROP TABLE IF EXISTS alert_history;
DROP TABLE IF EXISTS alert_rules;
DROP TABLE IF EXISTS maintenance_windows;
DROP TABLE IF EXISTS notification_channels;

DELETE FROM role_permissions WHERE permission_id IN (
    SELECT id FROM permissions WHERE resource IN ('alert', 'notification_channel', 'maintenance_window')
);
DELETE FROM permissions WHERE resource IN ('alert', 'notification_channel', 'maintenance_window');
