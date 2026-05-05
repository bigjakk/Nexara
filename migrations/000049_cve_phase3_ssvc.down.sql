-- 000049_cve_phase3_ssvc.down.sql

ALTER TABLE cve_scans
    DROP COLUMN IF EXISTS act_count,
    DROP COLUMN IF EXISTS attend_count,
    DROP COLUMN IF EXISTS track_star_count,
    DROP COLUMN IF EXISTS track_count;

DROP INDEX IF EXISTS idx_cve_scan_vulns_ssvc;

ALTER TABLE cve_scan_vulns DROP COLUMN IF EXISTS ssvc_label;
