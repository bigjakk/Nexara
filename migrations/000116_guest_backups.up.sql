-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Purely additive: two new tables and a partial index on task_history.
-- Nothing existing is rewritten or backfilled, so an interrupted run leaves
-- nothing behind — the migration is one transaction. The collector fills
-- both tables from its next pass on, starting with the backup jobs still in
-- task_history, so the job backups of the last TASK_HISTORY_RETENTION reach a
-- guest's History with no backfill here.
--
-- Both back the backup JOB entries of a guest's History
-- (queries/guest_history.sql). A vzdump that backs up more than one guest of
-- a node is one Proxmox task whose UPID names no guest: Proxmox sets the
-- worker id only when exactly one of the node's guests is in the job
-- (PVE/API2/VZDump.pm). Which guests it backed up, and how each went, is in
-- the task's log alone, which the collector reads once the task has finished
-- (internal/collector/backup_job_log.go).

-- guest_backups is one guest's part of a backup job: its outcome and times as
-- the job's log gives them, and which lines of that log are its part
-- (log_start..log_end, Proxmox's line numbers), for the History to show when
-- the entry is expanded. status is task_history's completed or failed;
-- exit_status is "OK", or the error Proxmox logged for this guest, capped and
-- without control characters.
--
-- Kept for good, like task_history_archive and for the same reason: once the
-- job's own task_history row is pruned it is the only record of which guests
-- the job backed up. So is the error. The archive keeps the text a failed
-- guest task died with for good too, a failed start's say; this is a line of
-- vzdump's log instead — what failed for this guest, which can name a backup
-- server's repository (user@realm!token@host:datastore: names, no secret) —
-- and nothing else keeps it past the task log. user_id is SET NULL rather than
-- CASCADE, as there. Keyed on the cluster as well as the UPID: a cluster
-- registered twice can ingest a task again under its other registration once
-- the first copy's task row is pruned, and neither copy's guests may take the
-- other's place.
CREATE TABLE IF NOT EXISTS guest_backups (
    cluster_id  UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    upid        TEXT NOT NULL,
    vmid        INTEGER NOT NULL,
    node        TEXT NOT NULL,
    source      TEXT NOT NULL,
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    status      TEXT NOT NULL,
    exit_status TEXT NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    log_start   INTEGER NOT NULL,
    log_end     INTEGER NOT NULL,
    PRIMARY KEY (cluster_id, upid, vmid)
);

-- A guest's History, and ClassifyGuestStatusChanges' search of one guest's
-- backups, which is bounded by finished_at as its archive search is.
CREATE INDEX IF NOT EXISTS idx_guest_backups_cluster_vmid
    ON guest_backups (cluster_id, vmid, finished_at);

-- backup_job_logs marks a job whose log the collector is done with, so each
-- is read once, and says how much of the log that was (log_read):
--
--   full     all Proxmox will write — to its closing TASK line, or to the end
--            of a task Proxmox says has stopped — so guest_backups holds every
--            guest the job backed up;
--   partial  some of it: longer than the collector reads, or still
--            unfinished, or failing or too slow to read, a day on;
--   none     nothing usable: Proxmox has no log for the task, or refused the
--            read; or none of it could be read in the day it is given — its
--            node not there to ask, or not answering — or its guests could
--            not be written.
--
-- ClassifyGuestStatusChanges decides by it: a job read in full explains its
-- own guests' status changes and no others; one read in part, every change
-- on its node while it ran, up to recorded_at; one not read at all, none. A
-- job with no mark yet holds back the changes on its node until it has one.
--
-- Deleted with the job's task_history row: the mark is read only beside it.
CREATE TABLE IF NOT EXISTS backup_job_logs (
    upid        TEXT PRIMARY KEY REFERENCES task_history (upid) ON DELETE CASCADE,
    log_read    TEXT NOT NULL CHECK (log_read IN ('full', 'partial', 'none')),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The backup jobs among a cluster's tasks — a few a day among a retention's
-- worth of every other task — which every slow pass looks for: the collector
-- for the jobs to read, ClassifyGuestStatusChanges for the ones it waits on.
-- The predicate is the one those queries test (queries/guest_history.sql).
CREATE INDEX IF NOT EXISTS idx_task_history_backup_jobs
    ON task_history (cluster_id, node, started_at)
    WHERE vmid IS NULL AND split_part(upid, ':', 6) = 'vzdump';
