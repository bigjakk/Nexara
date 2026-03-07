-- 000010_drs.up.sql
-- Distributed Resource Scheduler (DRS) tables for VM/CT workload balancing.

-- Per-cluster DRS configuration. DRS is DISABLED by default.
CREATE TABLE IF NOT EXISTS drs_configs (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id             UUID NOT NULL UNIQUE REFERENCES clusters(id) ON DELETE CASCADE,
    mode                   TEXT NOT NULL DEFAULT 'disabled',
    enabled                BOOLEAN NOT NULL DEFAULT false,
    weights                JSONB NOT NULL DEFAULT '{"cpu":0.4,"memory":0.4,"network":0.2}',
    imbalance_threshold    FLOAT NOT NULL DEFAULT 0.25,
    eval_interval_seconds  INT NOT NULL DEFAULT 300,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_drs_configs_cluster ON drs_configs(cluster_id);

CREATE TRIGGER trg_drs_configs_updated_at
    BEFORE UPDATE ON drs_configs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Affinity / anti-affinity / pin rules.
CREATE TABLE IF NOT EXISTS drs_rules (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    rule_type   TEXT NOT NULL,
    vm_ids      JSONB NOT NULL DEFAULT '[]',
    node_names  JSONB NOT NULL DEFAULT '[]',
    enabled     BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_drs_rules_cluster ON drs_rules(cluster_id);

CREATE TRIGGER trg_drs_rules_updated_at
    BEFORE UPDATE ON drs_rules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Migration decision history log.
CREATE TABLE IF NOT EXISTS drs_history (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id    UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    source_node   TEXT NOT NULL,
    target_node   TEXT NOT NULL,
    vm_id         INT NOT NULL,
    vm_type       TEXT NOT NULL DEFAULT 'qemu',
    reason        TEXT NOT NULL,
    score_before  FLOAT NOT NULL,
    score_after   FLOAT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    executed_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_drs_history_cluster ON drs_history(cluster_id);
CREATE INDEX IF NOT EXISTS idx_drs_history_created ON drs_history(created_at DESC);
