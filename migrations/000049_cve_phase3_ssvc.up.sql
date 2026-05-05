-- 000049_cve_phase3_ssvc.up.sql
-- Phase 3 SSVC action labels per vulnerability.
--
-- SSVC is CISA's decision-tree classification of what to *do* about a
-- vulnerability: Act (drop everything and patch), Attend (this week),
-- Track* (next routine cycle), Track (monthly batch). It complements the
-- scalar posture score with action-level guidance.
--
-- Reference: https://www.cisa.gov/sites/default/files/publications/cisa-ssvc-guide%20508c.pdf

ALTER TABLE cve_scan_vulns
    ADD COLUMN ssvc_label TEXT NOT NULL DEFAULT 'track';

CREATE INDEX idx_cve_scan_vulns_ssvc ON cve_scan_vulns(ssvc_label);

ALTER TABLE cve_scans
    ADD COLUMN act_count        INT NOT NULL DEFAULT 0,
    ADD COLUMN attend_count     INT NOT NULL DEFAULT 0,
    ADD COLUMN track_star_count INT NOT NULL DEFAULT 0,
    ADD COLUMN track_count      INT NOT NULL DEFAULT 0;
