-- Add a `source` column to task_history so external (PVE-native) tasks ingested
-- by the collector can be distinguished from Nexara-dispatched ones in the UI,
-- mirroring audit_log.source. Every existing row is Nexara-initiated (the only
-- writers before external ingest were TrackTask, the DRS executor, and the
-- migration orchestrator), so the 'nexara' default backfills them correctly.
-- Safe additive change: ADD COLUMN with a NOT NULL DEFAULT fills existing rows
-- automatically and is metadata-only on PostgreSQL 11+.
ALTER TABLE task_history ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'nexara';
