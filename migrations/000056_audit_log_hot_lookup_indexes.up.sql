-- Indexes for two hot audit_log lookups identified by external review.
--
-- 1) Proxmox task ingestion (collector) calls ExistsAuditLogByUPID once per
--    task observed on the cluster. Without an index on (details->>'upid'),
--    PG narrows by `idx_audit_log_source` and then re-checks every row's
--    JSONB body — fine at thousands of rows, multi-millisecond at millions.
--    A partial functional index keyed on the UPID and filtered to
--    source='proxmox' keeps the index small (~70% of rows in active
--    deployments) and serves the lookup with a single index probe.
--
-- 2) The audit_log enrichment queries (ListAuditLogEnriched,
--    ListRecentAuditLogEnriched, ListAuditLogAdvanced) LEFT JOIN vms via
--    `v.id::text = a.resource_id`. With a small vms table the planner
--    materializes a seq scan; once vms grows past a few thousand rows the
--    cast on the join probe blocks any use of the UUID PK. A functional
--    index on the text-cast id gives the planner a hash-build target that
--    matches the join shape exactly.

CREATE INDEX IF NOT EXISTS idx_audit_log_proxmox_upid
    ON audit_log ((details->>'upid'))
    WHERE source = 'proxmox';

CREATE INDEX IF NOT EXISTS idx_vms_id_text
    ON vms ((id::text));
