-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- Adds the queue slot a manual DRS evaluation uses to reach the scheduler.
--
-- Before this, POST /clusters/:id/drs/evaluate built its own DRS engine and
-- executor and dispatched migrations directly from the API process. That runs
-- outside the scheduler's leader election and outside its per-cluster interval
-- bookkeeping, so a manual trigger and the 60s scheduler tick could both
-- dispatch a migration for the same guest — the second one failing against
-- Proxmox with "VM is locked (migrate)" and recording a spurious failure.
--
-- The handler now stamps eval_requested_at and the leader's next DRS tick
-- picks it up, bypassing the per-cluster interval for that one pass. One
-- dispatcher, no race.
--
-- Nullable ADD COLUMN IF NOT EXISTS: existing rows get NULL, which reads as
-- "no evaluation requested" — the same behaviour those clusters have today.

ALTER TABLE drs_configs
    ADD COLUMN IF NOT EXISTS eval_requested_at TIMESTAMPTZ;
