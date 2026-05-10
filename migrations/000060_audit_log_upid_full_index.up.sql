-- Make the audit-log UPID lookup work across both sources, not just
-- source='proxmox'. The collector's task ingest path needs to skip a
-- Proxmox task whose UPID was already audited by a Nexara handler
-- (source='nexara') — otherwise the user sees two activity rows for one
-- physical action ("Disk Move" from the Nexara handler followed by
-- "Move Disk" from the proxmox-source ingestion). The original partial
-- index added in 000056 only covered source='proxmox' rows because the
-- query at the time filtered by source; the query is now widened.
--
-- The new index is functional on `(details->>'upid')` with no source
-- predicate. The previous partial index becomes redundant for the
-- current dedup query and is dropped to avoid maintaining two indexes
-- on the same column.
CREATE INDEX IF NOT EXISTS idx_audit_log_upid ON audit_log ((details->>'upid'));
DROP INDEX IF EXISTS idx_audit_log_proxmox_upid;
