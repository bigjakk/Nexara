-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Additive: one new table and three triggers on each of pbs_sync_jobs and
-- pbs_verify_jobs. Nothing existing is read, rewritten or backfilled; the
-- migration is one transaction.
--
-- A PBS sync or verify job edited on the PBS itself — Nexara never edits one —
-- is recorded in cluster_object_changes (000124) the way 000125 records a
-- replication job edited in the Proxmox GUI, from the rows the collector keeps
-- refreshing (syncPBSServer, every PBS pass):
--
--   added    a job appears, once that server's jobs of its type have been read
--            whole before (pbs_job_baselines); the first read of a server lists
--            every job it has and records none;
--   removed  a job is pruned, unless the server itself is going (a cascade);
--   changed  a configuration column differs. The run state (last_run_state,
--            next_run, last_seen_at) changes on a healthy job every run and is
--            never compared.
--
-- The rows are global: no cluster_id (000129), resource_type pbs_server and
-- resource_id the server's id, as the audit rows for a PBS server are filed;
-- the job and its datastore are in detail, the changed names (PBS's own
-- spelling) in fields. Never a configuration value: detail holds the job's
-- identity — its id, its type, the datastore it is on, and, when it moved to
-- another, the one it was on (from_datastore) — which the PBS's History needs
-- to say which job changed, and which Nexara lists beside the job anyway.
--
-- The collector prunes a server's jobs only after a listing that succeeded and
-- was written whole, so a PBS that cannot be reached removes nothing.

-- pbs_job_baselines says a server's jobs of one type were listed and written
-- whole at least once, and when last: last_read_at is the start of an added
-- job's window, since the job was not in that listing.
CREATE TABLE IF NOT EXISTS pbs_job_baselines (
    pbs_server_id UUID NOT NULL REFERENCES pbs_servers(id) ON DELETE CASCADE,
    job_type      TEXT NOT NULL CHECK (job_type IN ('sync', 'verify')),
    last_read_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (pbs_server_id, job_type)
);

-- record_pbs_job_change runs AFTER the row is written, so a failure here fails
-- the inventory write that caused it; it is one INSERT, whose only constraint
-- the global-kinds CHECK of 000129 admits. TG_ARGV[0] is the job type and the
-- rest are the compared columns, each "column" or "column=name as PBS spells it".
CREATE OR REPLACE FUNCTION record_pbs_job_change()
RETURNS TRIGGER AS $$
DECLARE
    v_type     text := TG_ARGV[0];
    v_old      jsonb;
    v_new      jsonb;
    v_fields   text[];
    v_col      text;
    v_name     text;
    v_last     timestamptz;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        v_old := to_jsonb(OLD);
        v_new := to_jsonb(NEW);
        v_fields := '{}';
        FOR i IN 1 .. TG_NARGS - 1 LOOP
            v_col := split_part(TG_ARGV[i], '=', 1);
            v_name := COALESCE(NULLIF(split_part(TG_ARGV[i], '=', 2), ''), v_col);
            IF v_old->v_col IS DISTINCT FROM v_new->v_col THEN
                v_fields := v_fields || v_name;
            END IF;
        END LOOP;
        IF cardinality(v_fields) > 0 THEN
            INSERT INTO cluster_object_changes (cluster_id, resource_type, resource_id, change, fields, detail,
                                                observed_by, last_seen_at)
            VALUES (NULL, 'pbs_server', NEW.pbs_server_id::text, 'changed', v_fields,
                    jsonb_build_object('job_type', v_type, 'job', NEW.job_id, 'datastore', NEW.store)
                        || CASE WHEN OLD.store IS DISTINCT FROM NEW.store
                                THEN jsonb_build_object('from_datastore', OLD.store) ELSE '{}' END,
                    'trigger', OLD.last_seen_at);
        END IF;
        RETURN NULL;
    END IF;

    IF TG_OP = 'INSERT' THEN
        SELECT b.last_read_at INTO v_last
        FROM pbs_job_baselines b
        WHERE b.pbs_server_id = NEW.pbs_server_id AND b.job_type = v_type;
        IF FOUND THEN
            INSERT INTO cluster_object_changes (cluster_id, resource_type, resource_id, change, detail,
                                                observed_by, last_seen_at)
            VALUES (NULL, 'pbs_server', NEW.pbs_server_id::text, 'added',
                    jsonb_build_object('job_type', v_type, 'job', NEW.job_id, 'datastore', NEW.store),
                    'trigger', v_last);
        END IF;
        RETURN NULL;
    END IF;

    IF EXISTS (SELECT 1 FROM pbs_servers s WHERE s.id = OLD.pbs_server_id) THEN
        INSERT INTO cluster_object_changes (cluster_id, resource_type, resource_id, change, detail,
                                            observed_by, last_seen_at)
        VALUES (NULL, 'pbs_server', OLD.pbs_server_id::text, 'removed',
                jsonb_build_object('job_type', v_type, 'job', OLD.job_id, 'datastore', OLD.store),
                'trigger', OLD.last_seen_at);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_pbs_sync_jobs_added ON pbs_sync_jobs;
CREATE TRIGGER trg_pbs_sync_jobs_added
    AFTER INSERT ON pbs_sync_jobs
    FOR EACH ROW
    EXECUTE FUNCTION record_pbs_job_change('sync');

DROP TRIGGER IF EXISTS trg_pbs_sync_jobs_removed ON pbs_sync_jobs;
CREATE TRIGGER trg_pbs_sync_jobs_removed
    AFTER DELETE ON pbs_sync_jobs
    FOR EACH ROW
    EXECUTE FUNCTION record_pbs_job_change('sync');

-- UPDATE OF fires for UpsertPBSSyncJob's ON CONFLICT arm, which sets these
-- columns on every pass; the WHEN keeps an unchanged job from reaching the
-- function.
DROP TRIGGER IF EXISTS trg_pbs_sync_jobs_changed ON pbs_sync_jobs;
CREATE TRIGGER trg_pbs_sync_jobs_changed
    AFTER UPDATE OF store, remote, remote_store, schedule, comment ON pbs_sync_jobs
    FOR EACH ROW
    WHEN ((OLD.store, OLD.remote, OLD.remote_store, OLD.schedule, OLD.comment)
          IS DISTINCT FROM (NEW.store, NEW.remote, NEW.remote_store, NEW.schedule, NEW.comment))
    EXECUTE FUNCTION record_pbs_job_change('sync', 'store', 'remote', 'remote_store=remote-store',
                                           'schedule', 'comment');

DROP TRIGGER IF EXISTS trg_pbs_verify_jobs_added ON pbs_verify_jobs;
CREATE TRIGGER trg_pbs_verify_jobs_added
    AFTER INSERT ON pbs_verify_jobs
    FOR EACH ROW
    EXECUTE FUNCTION record_pbs_job_change('verify');

DROP TRIGGER IF EXISTS trg_pbs_verify_jobs_removed ON pbs_verify_jobs;
CREATE TRIGGER trg_pbs_verify_jobs_removed
    AFTER DELETE ON pbs_verify_jobs
    FOR EACH ROW
    EXECUTE FUNCTION record_pbs_job_change('verify');

DROP TRIGGER IF EXISTS trg_pbs_verify_jobs_changed ON pbs_verify_jobs;
CREATE TRIGGER trg_pbs_verify_jobs_changed
    AFTER UPDATE OF store, schedule, comment ON pbs_verify_jobs
    FOR EACH ROW
    WHEN ((OLD.store, OLD.schedule, OLD.comment) IS DISTINCT FROM (NEW.store, NEW.schedule, NEW.comment))
    EXECUTE FUNCTION record_pbs_job_change('verify', 'store', 'schedule', 'comment');
