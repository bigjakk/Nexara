ALTER TABLE rolling_update_jobs
  ADD COLUMN ha_policy TEXT NOT NULL DEFAULT 'warn'
    CHECK (ha_policy IN ('strict', 'warn')),
  ADD COLUMN ha_warnings JSONB NOT NULL DEFAULT '[]';
