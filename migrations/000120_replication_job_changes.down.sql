DROP TRIGGER IF EXISTS trg_replication_jobs_changed ON replication_jobs;
DROP TRIGGER IF EXISTS trg_replication_jobs_removed ON replication_jobs;
DROP TRIGGER IF EXISTS trg_replication_jobs_added ON replication_jobs;
DROP FUNCTION IF EXISTS record_replication_job_change();
ALTER TABLE replication_jobs
    DROP COLUMN IF EXISTS comment,
    DROP COLUMN IF EXISTS disable,
    DROP COLUMN IF EXISTS rate,
    DROP COLUMN IF EXISTS schedule;
