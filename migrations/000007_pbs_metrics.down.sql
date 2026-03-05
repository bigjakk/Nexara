-- 000007_pbs_metrics.down.sql

-- Drop continuous aggregate and retention policy first.
DROP MATERIALIZED VIEW IF EXISTS pbs_datastore_metrics_5m;

-- Drop tables.
DROP TABLE IF EXISTS pbs_verify_jobs;
DROP TABLE IF EXISTS pbs_sync_jobs;
DROP TABLE IF EXISTS pbs_snapshots;
DROP TABLE IF EXISTS pbs_datastore_metrics;

-- Remove tls_fingerprint from pbs_servers.
ALTER TABLE pbs_servers DROP COLUMN IF EXISTS tls_fingerprint;
