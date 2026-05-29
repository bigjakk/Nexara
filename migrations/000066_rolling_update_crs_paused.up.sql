-- Track whether Nexara paused Proxmox's native CRS dynamic auto-rebalancer for
-- the duration of a rolling update (so the native balancer can't migrate guests
-- back onto a node being drained/rebooted), plus the original `crs` config
-- string so it can be restored verbatim when the job finishes or fails.
ALTER TABLE rolling_update_jobs
    ADD COLUMN IF NOT EXISTS native_crs_paused BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS saved_crs_config TEXT NOT NULL DEFAULT '';
