DROP INDEX IF EXISTS idx_maintenance_windows_detached_node;
DROP INDEX IF EXISTS idx_alert_rules_detached_node;
ALTER TABLE maintenance_windows DROP COLUMN IF EXISTS detached_node_name;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS detached_enabled;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS detached_node_name;
ALTER TABLE clusters DROP COLUMN IF EXISTS corosync_name_reported;
ALTER TABLE clusters DROP COLUMN IF EXISTS corosync_name;
