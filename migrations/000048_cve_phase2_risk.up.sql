-- 000048_cve_phase2_risk.up.sql
-- Phase 2 CVE risk scoring: enrich vulns with EPSS likelihood and CISA KEV
-- "actively exploited" flag, then re-bucket each vuln by computed risk
-- score instead of Debian's static urgency label.
--
-- Tables:
--   kev_cache  — CISA Known Exploited Vulnerabilities catalog snapshot.
--                Refreshed hourly by the scheduler.
--   epss_cache — FIRST EPSS scores (probability of exploitation in next
--                30 days). Lazily populated per scan from the FIRST API.

CREATE TABLE kev_cache (
    cve_id          TEXT PRIMARY KEY,
    date_added      DATE NOT NULL,
    vendor_project  TEXT NOT NULL DEFAULT '',
    product         TEXT NOT NULL DEFAULT '',
    vulnerability_name TEXT NOT NULL DEFAULT '',
    short_description  TEXT NOT NULL DEFAULT '',
    required_action    TEXT NOT NULL DEFAULT '',
    due_date           DATE,
    ransomware_use     BOOLEAN NOT NULL DEFAULT FALSE,
    fetched_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_kev_cache_date ON kev_cache(date_added DESC);

CREATE TABLE epss_cache (
    cve_id     TEXT PRIMARY KEY,
    score      REAL NOT NULL,
    percentile REAL NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_epss_cache_score ON epss_cache(score DESC);

-- Per-vuln risk enrichment columns. severity stays as Debian's view; the
-- new risk_score (0–10) drives bucketing for posture scoring, and the raw
-- inputs (epss, kev) are surfaced to users for transparency.
ALTER TABLE cve_scan_vulns
    ADD COLUMN risk_score REAL NOT NULL DEFAULT 0,
    ADD COLUMN epss       REAL,
    ADD COLUMN epss_percentile REAL,
    ADD COLUMN kev        BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN risk_severity TEXT NOT NULL DEFAULT 'unknown';

CREATE INDEX idx_cve_scan_vulns_risk_severity ON cve_scan_vulns(risk_severity);
CREATE INDEX idx_cve_scan_vulns_kev ON cve_scan_vulns(kev) WHERE kev = TRUE;

-- Scan-level KEV count for fast dashboard summaries.
ALTER TABLE cve_scans
    ADD COLUMN kev_count INT NOT NULL DEFAULT 0,
    ADD COLUMN unknown_count INT NOT NULL DEFAULT 0;
