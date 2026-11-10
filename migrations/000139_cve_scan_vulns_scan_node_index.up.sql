-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Additive: an index on cve_scan_vulns.scan_node_id, the one foreign key between
-- the three CVE scan tables (000020) with none. Deleting a scan cascades to its
-- node rows, and each node row's cascade looks up its vulnerabilities by this
-- column, so without the index every delete of scans, by hand or by the
-- scheduler's CVE scan retention, read the whole table once per node row: on a
-- synthetic table of 900,000 rows, deleting 560 scans took 129 s, and 2 s with
-- it. Plain CREATE INDEX: it is built while the binary migrates, before anything
-- writes a scan.
CREATE INDEX IF NOT EXISTS idx_cve_scan_vulns_scan_node ON cve_scan_vulns (scan_node_id);
