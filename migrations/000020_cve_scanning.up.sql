-- CVE cache from Debian security tracker
CREATE TABLE cve_cache (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cve_id        TEXT NOT NULL UNIQUE,
    severity      TEXT NOT NULL DEFAULT 'unknown',
    cvss_score    REAL,
    description   TEXT NOT NULL DEFAULT '',
    published_at  TIMESTAMPTZ,
    fetched_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_cve_cache_cve_id ON cve_cache(cve_id);

-- Scan runs
CREATE TABLE cve_scans (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id    UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'pending',
    total_nodes   INT NOT NULL DEFAULT 0,
    scanned_nodes INT NOT NULL DEFAULT 0,
    total_vulns   INT NOT NULL DEFAULT 0,
    critical_count INT NOT NULL DEFAULT 0,
    high_count    INT NOT NULL DEFAULT 0,
    medium_count  INT NOT NULL DEFAULT 0,
    low_count     INT NOT NULL DEFAULT 0,
    error_message TEXT,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_cve_scans_cluster ON cve_scans(cluster_id);

-- Per-node scan results
CREATE TABLE cve_scan_nodes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scan_id       UUID NOT NULL REFERENCES cve_scans(id) ON DELETE CASCADE,
    node_id       UUID NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    node_name     TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    packages_total INT NOT NULL DEFAULT 0,
    vulns_found   INT NOT NULL DEFAULT 0,
    posture_score REAL,
    error_message TEXT,
    scanned_at    TIMESTAMPTZ
);
CREATE INDEX idx_cve_scan_nodes_scan ON cve_scan_nodes(scan_id);

-- Individual vulnerabilities found
CREATE TABLE cve_scan_vulns (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scan_id       UUID NOT NULL REFERENCES cve_scans(id) ON DELETE CASCADE,
    scan_node_id  UUID NOT NULL REFERENCES cve_scan_nodes(id) ON DELETE CASCADE,
    cve_id        TEXT NOT NULL,
    package_name  TEXT NOT NULL,
    current_version TEXT NOT NULL,
    fixed_version TEXT,
    severity      TEXT NOT NULL DEFAULT 'unknown',
    cvss_score    REAL,
    description   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_cve_scan_vulns_scan ON cve_scan_vulns(scan_id);
CREATE INDEX idx_cve_scan_vulns_severity ON cve_scan_vulns(severity);

-- RBAC permissions for CVE scanning
INSERT INTO permissions (id, action, resource, description) VALUES
    (gen_random_uuid(), 'view', 'cve_scan', 'View CVE scan results'),
    (gen_random_uuid(), 'manage', 'cve_scan', 'Trigger and configure CVE scans')
ON CONFLICT (action, resource) DO NOTHING;

-- Grant to Admin role (both view and manage)
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000001'::uuid, id FROM permissions WHERE resource = 'cve_scan'
ON CONFLICT DO NOTHING;

-- Grant view to Operator role
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000002'::uuid, id FROM permissions WHERE resource = 'cve_scan' AND action = 'view'
ON CONFLICT DO NOTHING;

-- Grant view to Viewer role
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'a0000000-0000-0000-0000-000000000003'::uuid, id FROM permissions WHERE resource = 'cve_scan' AND action = 'view'
ON CONFLICT DO NOTHING;
