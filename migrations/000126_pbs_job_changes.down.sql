DROP TRIGGER IF EXISTS trg_pbs_verify_jobs_changed ON pbs_verify_jobs;
DROP TRIGGER IF EXISTS trg_pbs_verify_jobs_removed ON pbs_verify_jobs;
DROP TRIGGER IF EXISTS trg_pbs_verify_jobs_added ON pbs_verify_jobs;
DROP TRIGGER IF EXISTS trg_pbs_sync_jobs_changed ON pbs_sync_jobs;
DROP TRIGGER IF EXISTS trg_pbs_sync_jobs_removed ON pbs_sync_jobs;
DROP TRIGGER IF EXISTS trg_pbs_sync_jobs_added ON pbs_sync_jobs;
DROP FUNCTION IF EXISTS record_pbs_job_change();
DROP TABLE IF EXISTS pbs_job_baselines;
