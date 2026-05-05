-- 000048_cve_phase2_risk.down.sql

ALTER TABLE cve_scans
    DROP COLUMN IF EXISTS kev_count,
    DROP COLUMN IF EXISTS unknown_count;

DROP INDEX IF EXISTS idx_cve_scan_vulns_kev;
DROP INDEX IF EXISTS idx_cve_scan_vulns_risk_severity;

ALTER TABLE cve_scan_vulns
    DROP COLUMN IF EXISTS risk_severity,
    DROP COLUMN IF EXISTS kev,
    DROP COLUMN IF EXISTS epss_percentile,
    DROP COLUMN IF EXISTS epss,
    DROP COLUMN IF EXISTS risk_score;

DROP INDEX IF EXISTS idx_epss_cache_score;
DROP TABLE IF EXISTS epss_cache;

DROP INDEX IF EXISTS idx_kev_cache_date;
DROP TABLE IF EXISTS kev_cache;
