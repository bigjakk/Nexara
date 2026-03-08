-- Per-cluster CVE scan schedule configuration
CREATE TABLE cve_scan_schedules (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id     UUID NOT NULL UNIQUE REFERENCES clusters(id) ON DELETE CASCADE,
    enabled        BOOLEAN NOT NULL DEFAULT true,
    interval_hours INT NOT NULL DEFAULT 24,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_cve_scan_schedules_cluster ON cve_scan_schedules(cluster_id);
