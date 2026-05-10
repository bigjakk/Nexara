DELETE FROM role_permissions
WHERE permission_id IN (SELECT id FROM permissions WHERE resource = 'notification_dlq');

DELETE FROM permissions WHERE resource = 'notification_dlq';

DROP TABLE IF EXISTS notification_dlq;
