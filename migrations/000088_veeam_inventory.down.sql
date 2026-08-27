-- Reverse 000088_veeam_inventory.
--
-- Drops the collected Veeam inventory. The registered servers themselves
-- (000087) survive, so re-applying repopulates everything from Veeam on the
-- next sync — nothing here is original data.
--
-- Order matters: the continuous aggregate before its hypertable, and
-- veeam_restore_points before veeam_backup_objects (it CASCADEs from it, but
-- dropping explicitly keeps this readable as the up migration in reverse).
-- Timescale removes a hypertable's retention policy with the table itself.

DROP MATERIALIZED VIEW IF EXISTS veeam_repository_metrics_5m;

DROP TABLE IF EXISTS veeam_repository_metrics;

DROP TABLE IF EXISTS veeam_restore_points;

DROP TABLE IF EXISTS veeam_backup_objects;

DROP TABLE IF EXISTS veeam_sessions;

DROP TABLE IF EXISTS veeam_jobs;

DROP TABLE IF EXISTS veeam_repositories;
