-- Track whether DRS was enabled before the rolling update started,
-- so we can restore it when the job finishes.
ALTER TABLE rolling_update_jobs
    ADD COLUMN IF NOT EXISTS drs_was_enabled BOOLEAN NOT NULL DEFAULT false;
