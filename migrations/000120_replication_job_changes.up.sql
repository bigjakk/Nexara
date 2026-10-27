-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Additive: four nullable columns and three triggers on replication_jobs. Existing
-- rows keep NULL in the new columns, which means "not read yet": the collector
-- fills them on its next pass, and the trigger below treats a NULL old value as
-- the first reading, never as a change. One transaction; an interrupted run
-- leaves nothing behind.
--
-- A replication job's configuration, as GET /cluster/replication reports it, is
-- stored so that an edit made in the Proxmox GUI can be told from a job that
-- simply keeps being listed. replication_jobs is refreshed every slow pass
-- (30 s) and a job missing for 15 minutes is deleted (DeleteStaleReplicationJobs),
-- so the three triggers record what that churn would otherwise hide:
--
--   added    a job appears, once the cluster's replication has been read before
--            (cluster_object_baselines); the first read of a cluster just added
--            lists every job it has and records none.
--   removed  a job is deleted, once the cluster's replication has been read. A
--            cluster's own deletion records nothing: its cascade removes the
--            baseline row before the deletes it queues fire this trigger.
--   changed  schedule, rate, disable or comment differs from a KNOWN old value.
--
-- It never fires on run state — fail_count, error, last_seen_at — which changes
-- on a healthy job every pass, nor on node or target: a guest's migration swaps
-- a job's source and target, and a rolling update or DRS moves guests all day.
-- The UPDATE trigger lists its columns and carries a WHEN, so those writes do
-- not even reach the function.
ALTER TABLE replication_jobs
    ADD COLUMN IF NOT EXISTS schedule TEXT,
    ADD COLUMN IF NOT EXISTS rate     TEXT,
    ADD COLUMN IF NOT EXISTS disable  INT,
    ADD COLUMN IF NOT EXISTS comment  TEXT;

-- record_replication_job_change runs AFTER the row is written, so a failure
-- here fails the inventory write that caused it. It is one INSERT with no
-- constraint its own row can violate: cluster_id comes from a row that already
-- satisfies the foreign key, and a cluster being deleted is never recorded (see
-- above).
CREATE OR REPLACE FUNCTION record_replication_job_change()
RETURNS TRIGGER AS $$
DECLARE
    detail    jsonb  := '{}'::jsonb;
    changed   text[] := '{}';
    prev_read timestamptz;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF OLD.schedule IS NOT NULL AND OLD.schedule IS DISTINCT FROM NEW.schedule THEN
            detail  := detail || jsonb_build_object('schedule', jsonb_build_object('from', OLD.schedule, 'to', NEW.schedule));
            changed := array_append(changed, 'schedule');
        END IF;
        IF OLD.rate IS NOT NULL AND OLD.rate IS DISTINCT FROM NEW.rate THEN
            detail  := detail || jsonb_build_object('rate', jsonb_build_object('from', OLD.rate, 'to', NEW.rate));
            changed := array_append(changed, 'rate');
        END IF;
        IF OLD.disable IS NOT NULL AND OLD.disable IS DISTINCT FROM NEW.disable THEN
            detail  := detail || jsonb_build_object('disable', jsonb_build_object('from', OLD.disable, 'to', NEW.disable));
            changed := array_append(changed, 'disable');
        END IF;
        IF OLD.comment IS NOT NULL AND OLD.comment IS DISTINCT FROM NEW.comment THEN
            detail  := detail || jsonb_build_object('comment',
                jsonb_build_object('from', left(OLD.comment, 200), 'to', left(NEW.comment, 200)));
            changed := array_append(changed, 'comment');
        END IF;
        IF cardinality(changed) > 0 THEN
            INSERT INTO cluster_object_changes (cluster_id, resource_type, resource_id, change, fields, detail, observed_by, last_seen_at)
            VALUES (NEW.cluster_id, 'replication', NEW.job_id, 'changed', changed, detail, 'trigger', OLD.last_seen_at);
        END IF;
        RETURN NULL;
    END IF;

    -- No baseline, no change. The baseline's last_read_at is the end of the last
    -- pass, which has not yet written this one's rows: when a job appeared, it was
    -- absent then.
    SELECT b.last_read_at INTO prev_read
    FROM cluster_object_baselines b
    WHERE b.cluster_id = COALESCE(NEW.cluster_id, OLD.cluster_id) AND b.resource_type = 'replication';
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    IF TG_OP = 'INSERT' THEN
        INSERT INTO cluster_object_changes (cluster_id, resource_type, resource_id, change, detail, observed_by, last_seen_at)
        VALUES (NEW.cluster_id, 'replication', NEW.job_id, 'added',
                jsonb_build_object('guest', NEW.guest, 'target', NEW.target, 'schedule', NEW.schedule,
                                   'rate', NEW.rate, 'disable', NEW.disable, 'comment', left(NEW.comment, 200)),
                'trigger', prev_read);
    ELSE
        INSERT INTO cluster_object_changes (cluster_id, resource_type, resource_id, change, detail, observed_by, last_seen_at)
        VALUES (OLD.cluster_id, 'replication', OLD.job_id, 'removed',
                jsonb_build_object('guest', OLD.guest, 'target', OLD.target, 'schedule', OLD.schedule,
                                   'rate', OLD.rate, 'disable', OLD.disable, 'comment', left(OLD.comment, 200)),
                'trigger', OLD.last_seen_at);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_replication_jobs_added ON replication_jobs;
CREATE TRIGGER trg_replication_jobs_added
    AFTER INSERT ON replication_jobs
    FOR EACH ROW
    EXECUTE FUNCTION record_replication_job_change();

DROP TRIGGER IF EXISTS trg_replication_jobs_removed ON replication_jobs;
CREATE TRIGGER trg_replication_jobs_removed
    AFTER DELETE ON replication_jobs
    FOR EACH ROW
    EXECUTE FUNCTION record_replication_job_change();

-- UPDATE OF fires for the ON CONFLICT DO UPDATE arm of UpsertReplicationJob too,
-- which lists these columns in its SET; the WHEN clause keeps an unchanged value
-- — every quiet pass — from reaching the function.
DROP TRIGGER IF EXISTS trg_replication_jobs_changed ON replication_jobs;
CREATE TRIGGER trg_replication_jobs_changed
    AFTER UPDATE OF schedule, rate, disable, comment ON replication_jobs
    FOR EACH ROW
    WHEN ((OLD.schedule IS NOT NULL AND OLD.schedule IS DISTINCT FROM NEW.schedule)
       OR (OLD.rate IS NOT NULL AND OLD.rate IS DISTINCT FROM NEW.rate)
       OR (OLD.disable IS NOT NULL AND OLD.disable IS DISTINCT FROM NEW.disable)
       OR (OLD.comment IS NOT NULL AND OLD.comment IS DISTINCT FROM NEW.comment))
    EXECUTE FUNCTION record_replication_job_change();
