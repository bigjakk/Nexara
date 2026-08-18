-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Adds the target image format a storage migration should convert disks to.
-- Additive column with a DEFAULT, so PostgreSQL fills existing rows itself;
-- '' preserves today's behaviour of letting the target storage decide.
ALTER TABLE migration_jobs
    ADD COLUMN IF NOT EXISTS disk_format TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN migration_jobs.disk_format IS
    'Target image format for storage moves (raw|qcow2|vmdk). Empty means the target storage decides. QEMU only — LXC volumes have no format choice.';
