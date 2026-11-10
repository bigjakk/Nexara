-- When a paused rolling update job was last resumed. A node's step timeouts
-- (draining 1 hour, upgrading 60 minutes, rebooting 10 minutes, restoring 30
-- minutes) count from the later of the step's start and this, so the time a
-- job spends paused no longer fails a healthy node when it is resumed.
-- NULL for a job that was never paused. Nullable, no default: existing rows
-- read as never resumed, which is what the timeouts assumed until now.
ALTER TABLE rolling_update_jobs ADD COLUMN IF NOT EXISTS resumed_at TIMESTAMPTZ;
