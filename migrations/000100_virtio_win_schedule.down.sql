DROP INDEX IF EXISTS idx_virtio_win_configs_due;

ALTER TABLE virtio_win_configs DROP COLUMN IF EXISTS next_check_at;
ALTER TABLE virtio_win_configs DROP COLUMN IF EXISTS check_timezone;
ALTER TABLE virtio_win_configs DROP COLUMN IF EXISTS check_schedule;
