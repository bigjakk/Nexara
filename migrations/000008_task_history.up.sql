CREATE TABLE IF NOT EXISTS task_history (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    upid        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'running',
    exit_status TEXT NOT NULL DEFAULT '',
    node        TEXT NOT NULL DEFAULT '',
    task_type   TEXT NOT NULL DEFAULT '',
    progress    DOUBLE PRECISION,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_task_history_cluster_id ON task_history (cluster_id);
CREATE INDEX IF NOT EXISTS idx_task_history_user_id ON task_history (user_id);
CREATE INDEX IF NOT EXISTS idx_task_history_started_at ON task_history (started_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_history_upid ON task_history (upid);

CREATE TRIGGER trg_task_history_updated_at
    BEFORE UPDATE ON task_history
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
