-- 000048_cve_phase2_risk.up.sql
-- Phase 2 CVE risk scoring. See migrations/000048_cve_phase2_risk.up.sql
-- for full rationale. All statements idempotent for embedded schema runner.

CREATE TABLE IF NOT EXISTS kev_cache (
    cve_id             TEXT PRIMARY KEY,
    date_added         DATE NOT NULL,
    vendor_project     TEXT NOT NULL DEFAULT '',
    product            TEXT NOT NULL DEFAULT '',
    vulnerability_name TEXT NOT NULL DEFAULT '',
    short_description  TEXT NOT NULL DEFAULT '',
    required_action    TEXT NOT NULL DEFAULT '',
    due_date           DATE,
    ransomware_use     BOOLEAN NOT NULL DEFAULT FALSE,
    fetched_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_kev_cache_date ON kev_cache(date_added DESC);

CREATE TABLE IF NOT EXISTS epss_cache (
    cve_id     TEXT PRIMARY KEY,
    score      REAL NOT NULL,
    percentile REAL NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_epss_cache_score ON epss_cache(score DESC);

ALTER TABLE cve_scan_vulns
    ADD COLUMN IF NOT EXISTS risk_score REAL NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS epss       REAL,
    ADD COLUMN IF NOT EXISTS epss_percentile REAL,
    ADD COLUMN IF NOT EXISTS kev        BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS risk_severity TEXT NOT NULL DEFAULT 'unknown';

CREATE INDEX IF NOT EXISTS idx_cve_scan_vulns_risk_severity ON cve_scan_vulns(risk_severity);
CREATE INDEX IF NOT EXISTS idx_cve_scan_vulns_kev ON cve_scan_vulns(kev) WHERE kev = TRUE;

ALTER TABLE cve_scans
    ADD COLUMN IF NOT EXISTS kev_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS unknown_count INT NOT NULL DEFAULT 0;
