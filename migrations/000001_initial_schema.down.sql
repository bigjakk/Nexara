-- 000001_initial_schema.down.sql
-- Drop in reverse order of creation

DROP TRIGGER IF EXISTS trg_settings_updated_at ON settings;
DROP TRIGGER IF EXISTS trg_pbs_servers_updated_at ON pbs_servers;
DROP TRIGGER IF EXISTS trg_clusters_updated_at ON clusters;
DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
DROP FUNCTION IF EXISTS set_updated_at();

DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS pbs_servers;
DROP TABLE IF EXISTS clusters;
DROP TABLE IF EXISTS users;
