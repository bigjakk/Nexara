-- 000011_migrations.up.sql
-- Migration jobs table for intra-cluster and cross-cluster VM/CT migrations.

CREATE TABLE IF NOT EXISTS migration_jobs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    target_cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    source_node       TEXT NOT NULL,
    target_node       TEXT NOT NULL DEFAULT '',
    vmid              INT NOT NULL,
    vm_type           TEXT NOT NULL DEFAULT 'qemu',
    migration_type    TEXT NOT NULL DEFAULT 'intra-cluster',

    -- Mappings (cross-cluster only).
    storage_map       JSONB NOT NULL DEFAULT '{}',
    network_map       JSONB NOT NULL DEFAULT '{}',

    -- Options.
    online            BOOLEAN NOT NULL DEFAULT false,
    bwlimit_kib       INT NOT NULL DEFAULT 0,
    delete_source     BOOLEAN NOT NULL DEFAULT false,
    target_vmid       INT NOT NULL DEFAULT 0,

    -- State machine: pending -> checking -> migrating -> completed/failed/cancelled.
    status            TEXT NOT NULL DEFAULT 'pending',
    upid              TEXT NOT NULL DEFAULT '',
    progress          FLOAT NOT NULL DEFAULT 0.0,
    check_results     JSONB,
    error_message     TEXT NOT NULL DEFAULT '',

    -- Tracking.
    created_by        UUID REFERENCES users(id) ON DELETE SET NULL,
    started_at        TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_migration_jobs_source_cluster ON migration_jobs(source_cluster_id);
CREATE INDEX IF NOT EXISTS idx_migration_jobs_target_cluster ON migration_jobs(target_cluster_id);
CREATE INDEX IF NOT EXISTS idx_migration_jobs_status ON migration_jobs(status);
CREATE INDEX IF NOT EXISTS idx_migration_jobs_created ON migration_jobs(created_at DESC);

CREATE TRIGGER trg_migration_jobs_updated_at
    BEFORE UPDATE ON migration_jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
