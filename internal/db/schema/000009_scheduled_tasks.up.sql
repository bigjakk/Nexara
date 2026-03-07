-- 000009_scheduled_tasks.up.sql
-- Scheduled tasks for automated snapshot, backup, and reboot operations.

CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id    UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL,
    resource_id   TEXT NOT NULL,
    node          TEXT NOT NULL,
    action        TEXT NOT NULL,
    schedule      TEXT NOT NULL,
    params        JSONB NOT NULL DEFAULT '{}',
    enabled       BOOLEAN NOT NULL DEFAULT true,
    last_run_at   TIMESTAMPTZ,
    next_run_at   TIMESTAMPTZ,
    last_status   TEXT,
    last_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_cluster ON scheduled_tasks(cluster_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_next_run ON scheduled_tasks(next_run_at) WHERE enabled = true;

CREATE TRIGGER trg_scheduled_tasks_updated_at
    BEFORE UPDATE ON scheduled_tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
