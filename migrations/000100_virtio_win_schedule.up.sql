-- 000100_virtio_win_schedule.up.sql
-- A per-cluster check schedule for the virtio-win auto-download.
--
-- 000096 ran the check on a fixed 6-hourly tick counted from process start,
-- which is fine for "is there a new release" but wrong for the part that
-- matters: the check is what dispatches an ~840 MiB fetch, and an operator
-- wants that aimed at a maintenance window rather than at whenever the
-- container last restarted. next_check_at moves the decision out of the
-- process and into a row, so the schedule survives a restart and the UI can
-- say when the next one lands.
--
-- Additive (new columns with defaults) — safe for in-place upgrade, no
-- operator action required.

-- Cron expression (5 fields, no seconds — the same shape report_schedules
-- uses, parsed by internal/scheduler's robfig/cron parser). Empty preserves
-- the pre-000100 behaviour: check every six hours, counted from the last
-- check rather than from boot.
ALTER TABLE virtio_win_configs
    ADD COLUMN IF NOT EXISTS check_schedule TEXT NOT NULL DEFAULT '';

-- IANA zone the cron expression is evaluated in. Empty means the server's own
-- zone, which in a container is almost always UTC — the field exists because
-- "check at 03:00" is otherwise silently 03:00 UTC, i.e. the middle of the
-- working day for a good part of the world.
ALTER TABLE virtio_win_configs
    ADD COLUMN IF NOT EXISTS check_timezone TEXT NOT NULL DEFAULT '';

-- When the next check is due. NULL means "due now", which is what an existing
-- install and a freshly-enabled cluster both want: one check immediately, and
-- a schedule from then on.
ALTER TABLE virtio_win_configs
    ADD COLUMN IF NOT EXISTS next_check_at TIMESTAMPTZ;

COMMENT ON COLUMN virtio_win_configs.check_schedule IS 'Cron expression (min hour dom month dow); empty means every 6 hours from the last check';
COMMENT ON COLUMN virtio_win_configs.check_timezone IS 'IANA zone the cron expression is evaluated in; empty means server time';
COMMENT ON COLUMN virtio_win_configs.next_check_at IS 'When the next check is due; NULL means due now';

-- The scheduler now polls for due clusters every minute instead of running
-- every enabled one every six hours, so that predicate needs an index rather
-- than a sequential scan of the table on each tick.
CREATE INDEX IF NOT EXISTS idx_virtio_win_configs_due
    ON virtio_win_configs (next_check_at)
    WHERE enabled;
