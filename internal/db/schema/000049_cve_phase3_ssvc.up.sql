-- 000049_cve_phase3_ssvc.up.sql
-- Phase 3 SSVC action labels. See migrations/000049_cve_phase3_ssvc.up.sql
-- for full rationale. All statements idempotent.

ALTER TABLE cve_scan_vulns
    ADD COLUMN IF NOT EXISTS ssvc_label TEXT NOT NULL DEFAULT 'track';

CREATE INDEX IF NOT EXISTS idx_cve_scan_vulns_ssvc ON cve_scan_vulns(ssvc_label);

ALTER TABLE cve_scans
    ADD COLUMN IF NOT EXISTS act_count        INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS attend_count     INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS track_star_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS track_count      INT NOT NULL DEFAULT 0;
