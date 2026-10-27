-- The observer's readings (migrations/000128) and the classification of what it
-- records (internal/collector/observer.go, observer_explain.go). It files edits
-- made directly in Proxmox into cluster_object_changes (000124) with
-- observed_by 'observer'; ClassifyClusterObjectChanges decides the other
-- writers' rows, ClassifyObservedChanges below decides these.

-- ListClusterObjectState is everything the observer last read of one cluster:
-- one row per object, its fields as tags, never values.
-- name: ListClusterObjectState :many
SELECT kind, object_id, fields, node, signature, last_seen_at
FROM cluster_object_state
WHERE cluster_id = $1;

-- ListClusterObjectBaselines is the signature each kind of a cluster was last
-- read whole under, and when (000124, 000128). The replication trigger's own
-- mark has neither.
-- name: ListClusterObjectBaselines :many
SELECT resource_type, signature, last_read_at
FROM cluster_object_baselines
WHERE cluster_id = $1;

-- RecordObservedObjects writes one reading of one kind of a cluster — or one
-- batch of its guests — in a single statement, so a change is never recorded
-- without the reading it came from replacing the stored one, nor a reading
-- stored without its change:
--
--   changes  the rows for cluster_object_changes, each with its window;
--   states   the objects new or changed, or taken as a baseline, to store;
--   removed  the objects gone, whose stored reading is dropped;
--   touched  the objects read unchanged: only their last_seen_at moves;
--   baseline the signature the kind was read whole under, at seen_at, or ''
--            for a reading that marks nothing (a guest batch, a node's objects).
--
-- The sets are disjoint by construction. cluster_id is never NULL here: only a
-- global kind's trigger writes a row with none (000129).
-- name: RecordObservedObjects :exec
WITH changes AS (
    INSERT INTO cluster_object_changes (cluster_id, resource_type, resource_id, vmid, change, fields, detail, node,
                                        observed_by, last_seen_at, observed_at)
    SELECT sqlc.arg('cluster_id')::uuid, c.resource_type, c.resource_id, c.vmid, c.change,
           COALESCE(c.fields, '{}'), COALESCE(c.detail, '{}'), COALESCE(c.node, ''), 'observer',
           c.last_seen_at, c.observed_at
    FROM jsonb_to_recordset(sqlc.arg('changes')::jsonb) AS c(
        resource_type text, resource_id text, vmid int, change text, fields text[], detail jsonb, node text,
        last_seen_at timestamptz, observed_at timestamptz)
), saved AS (
    INSERT INTO cluster_object_state (cluster_id, kind, object_id, fields, node, signature, last_seen_at)
    SELECT sqlc.arg('cluster_id')::uuid, sqlc.arg('kind')::text, s.object_id, s.fields, COALESCE(s.node, ''),
           s.signature, s.last_seen_at
    FROM jsonb_to_recordset(sqlc.arg('states')::jsonb) AS s(
        object_id text, fields jsonb, node text, signature text, last_seen_at timestamptz)
    ON CONFLICT (cluster_id, kind, object_id) DO UPDATE
        SET fields = EXCLUDED.fields, node = EXCLUDED.node, signature = EXCLUDED.signature,
            last_seen_at = EXCLUDED.last_seen_at
), dropped AS (
    DELETE FROM cluster_object_state
    WHERE cluster_id = sqlc.arg('cluster_id')::uuid
      AND kind = sqlc.arg('kind')::text
      AND object_id = ANY(sqlc.arg('removed')::text[])
), marked AS (
    INSERT INTO cluster_object_baselines (cluster_id, resource_type, signature, last_read_at)
    SELECT sqlc.arg('cluster_id')::uuid, sqlc.arg('kind')::text, sqlc.arg('baseline')::text,
           sqlc.arg('seen_at')::timestamptz
    WHERE sqlc.arg('baseline')::text <> ''
    ON CONFLICT (cluster_id, resource_type) DO UPDATE
        SET signature = EXCLUDED.signature, last_read_at = EXCLUDED.last_read_at
)
UPDATE cluster_object_state
SET last_seen_at = sqlc.arg('seen_at')::timestamptz
WHERE cluster_id = sqlc.arg('cluster_id')::uuid
  AND kind = sqlc.arg('kind')::text
  AND object_id = ANY(sqlc.arg('touched')::text[]);

-- DeleteClusterObjectState drops what the observer holds of a kind's objects —
-- a guest that left the inventory, the HA groups Proxmox VE 9 migrated to
-- rules. Nothing is recorded for them.
-- name: DeleteClusterObjectState :exec
DELETE FROM cluster_object_state
WHERE cluster_id = sqlc.arg('cluster_id')
  AND kind = sqlc.arg('kind')
  AND object_id = ANY(sqlc.arg('object_ids')::text[]);

-- ClassifyObservedChanges decides, once, whether something already on record
-- explains each settled change the observer filed for a cluster. The window is
-- ClassifyGuestStatusChanges' and ClassifyClusterObjectChanges': an audit row
-- counts when it was made (its task started) no later than the change was seen,
-- plus 30 seconds of clock skew, and not before the object was last seen as it
-- was, less 2 minutes — a task while it runs, until it finishes. Rows the
-- collector ingests from Proxmox tasks are audit rows too, so they count.
--
-- What counts is the caller's rules (observer_explain.go), one JSON object
-- each: the kind of change it applies to, the audit rows it reads — by
-- resource_type ('' any), and by match_key: 'id' the change's own resource_id,
-- 'node' its node, 'vmid' its guest, 'sid' the guest an HA sid names, 'cluster'
-- any row of the cluster, 'details:<k>' a row whose details-><k> is the
-- change's resource_id — and which of those rows: by action and UPID worker
-- type (anchored patterns, '' any), never an action matching except_re, only a
-- row with no task when untasked. field_re says which fields such a row can
-- explain ('' any): a disk move explains the disk it moved, not the memory
-- someone changed in the GUI meanwhile. A change is explained when every field
-- it names is covered by some rule's row; an added or removed object, when any
-- rule's row is found.
--
-- The window opens no earlier than lookback_seconds before the change was seen
-- (observedLookback says how long and why): after an outage a change's window
-- would otherwise be the whole gap, and one row from its start would explain
-- everything found at its end. A task's start is looked for no more than a day
-- before the window, so a change is never weighed against the cluster's whole
-- audit log.
--
-- A change waits for the pass whose ingest listed every node the cluster calls
-- online (all_nodes_listed), since the task that explains it may have run on
-- any of them — a migration on its source, a destroy on the guest's node — or
-- until give_up_before. Until then, and until it settles, it is not shown.
-- name: ClassifyObservedChanges :execrows
WITH rules AS MATERIALIZED (
    SELECT e.value->>'kind'                AS kind,
           e.value->>'change'              AS change,
           e.value->>'audit_type'          AS audit_type,
           e.value->>'match_key'           AS match_key,
           (e.value->>'untasked')::bool    AS untasked,
           e.value->>'action_re'           AS action_re,
           e.value->>'except_re'           AS except_re,
           e.value->>'worker_re'           AS worker_re,
           e.value->>'field_re'            AS field_re
    FROM jsonb_array_elements(sqlc.arg('rules')::jsonb) AS e
), pending AS MATERIALIZED (
    SELECT c.id, c.cluster_id, c.resource_type, c.resource_id, c.vmid, c.change, c.node,
           GREATEST(LEAST(c.last_seen_at, c.observed_at) - interval '2 minutes',
                    c.observed_at - make_interval(secs => sqlc.arg('lookback_seconds')::int)) AS win_from,
           c.observed_at + interval '30 seconds'                                           AS win_to
    FROM cluster_object_changes c
    WHERE c.cluster_id = sqlc.arg('cluster_id')
      AND c.observed_by = 'observer'
      AND c.explained IS NULL
      AND c.observed_at < sqlc.arg('settled_before')::timestamptz
      AND (sqlc.arg('all_nodes_listed')::bool OR c.observed_at < sqlc.arg('give_up_before')::timestamptz)
), candidates AS (
    -- One arm per match_key, so each reads audit_log through the index its key
    -- can use.
    SELECT p.id, p.cluster_id, p.win_from, p.win_to, r.untasked, r.action_re, r.except_re, r.worker_re, r.field_re,
           a.action, a.details->>'upid' AS upid, a.created_at
    FROM pending p
    JOIN rules r ON r.kind = p.resource_type AND r.change IN ('', p.change) AND r.match_key = 'id'
    JOIN audit_log a ON a.cluster_id = p.cluster_id AND a.resource_type = r.audit_type
                    AND a.resource_id = p.resource_id
                    AND a.created_at BETWEEN p.win_from - interval '1 day' AND p.win_to + interval '10 minutes'
    UNION ALL
    SELECT p.id, p.cluster_id, p.win_from, p.win_to, r.untasked, r.action_re, r.except_re, r.worker_re, r.field_re,
           a.action, a.details->>'upid', a.created_at
    FROM pending p
    JOIN rules r ON r.kind = p.resource_type AND r.change IN ('', p.change) AND r.match_key = 'node'
    JOIN audit_log a ON a.cluster_id = p.cluster_id AND a.resource_type = r.audit_type
                    AND p.node <> '' AND a.resource_id = p.node
                    AND a.created_at BETWEEN p.win_from - interval '1 day' AND p.win_to + interval '10 minutes'
    UNION ALL
    SELECT p.id, p.cluster_id, p.win_from, p.win_to, r.untasked, r.action_re, r.except_re, r.worker_re, r.field_re,
           a.action, a.details->>'upid', a.created_at
    FROM pending p
    JOIN rules r ON r.kind = p.resource_type AND r.change IN ('', p.change) AND r.match_key = 'vmid'
    JOIN audit_log a ON a.cluster_id = p.cluster_id AND a.vmid = p.vmid
                    AND (r.audit_type = '' OR a.resource_type = r.audit_type)
                    AND a.created_at BETWEEN p.win_from - interval '1 day' AND p.win_to + interval '10 minutes'
    UNION ALL
    SELECT p.id, p.cluster_id, p.win_from, p.win_to, r.untasked, r.action_re, r.except_re, r.worker_re, r.field_re,
           a.action, a.details->>'upid', a.created_at
    FROM pending p
    JOIN rules r ON r.kind = p.resource_type AND r.change IN ('', p.change) AND r.match_key = 'sid'
    JOIN audit_log a ON a.cluster_id = p.cluster_id
                    AND a.vmid = (substring(p.resource_id FROM '^(?:vm|ct):([0-9]{1,9})$'))::int
                    AND (r.audit_type = '' OR a.resource_type = r.audit_type)
                    AND a.created_at BETWEEN p.win_from - interval '1 day' AND p.win_to + interval '10 minutes'
    UNION ALL
    SELECT p.id, p.cluster_id, p.win_from, p.win_to, r.untasked, r.action_re, r.except_re, r.worker_re, r.field_re,
           a.action, a.details->>'upid', a.created_at
    FROM pending p
    JOIN rules r ON r.kind = p.resource_type AND r.change IN ('', p.change) AND r.match_key = 'cluster'
    JOIN audit_log a ON a.cluster_id = p.cluster_id
                    AND (r.audit_type = '' OR a.resource_type = r.audit_type)
                    AND a.created_at BETWEEN p.win_from - interval '1 day' AND p.win_to + interval '10 minutes'
    UNION ALL
    SELECT p.id, p.cluster_id, p.win_from, p.win_to, r.untasked, r.action_re, r.except_re, r.worker_re, r.field_re,
           a.action, a.details->>'upid', a.created_at
    FROM pending p
    JOIN rules r ON r.kind = p.resource_type AND r.change IN ('', p.change) AND r.match_key LIKE 'details:_%'
    JOIN audit_log a ON a.cluster_id = p.cluster_id AND a.resource_type = r.audit_type
                    AND a.details->>substr(r.match_key, 9) = p.resource_id
                    AND a.created_at BETWEEN p.win_from - interval '1 day' AND p.win_to + interval '10 minutes'
), hits AS (
    SELECT k.id, k.field_re
    FROM candidates k
    LEFT JOIN task_history th ON th.cluster_id = k.cluster_id AND th.upid = k.upid
    WHERE (NOT k.untasked OR k.upid IS NULL)
      AND (k.action_re = '' OR k.action ~ k.action_re)
      AND (k.except_re = '' OR k.action !~ k.except_re)
      AND (k.worker_re = '' OR COALESCE(split_part(k.upid, ':', 6) ~ k.worker_re, false))
      AND COALESCE(th.started_at, k.created_at) <= k.win_to
      AND CASE WHEN th.upid IS NULL THEN k.created_at ELSE COALESCE(th.finished_at, now()) END >= k.win_from
)
UPDATE cluster_object_changes c
SET explained = NOT EXISTS (
    SELECT 1
    FROM unnest(CASE WHEN c.change = 'changed' AND cardinality(c.fields) > 0 THEN c.fields ELSE ARRAY['']::text[] END)
         AS f(name)
    WHERE NOT EXISTS (
        SELECT 1 FROM hits h
        WHERE h.id = c.id
          AND (c.change <> 'changed' OR h.field_re = '' OR f.name ~ h.field_re)))
FROM pending p
WHERE c.id = p.id;

-- ClassifyGlobalObjectChanges is ClassifyObservedChanges for the rows of a
-- global kind — a PBS server's jobs (000130), whoever recorded them — which
-- belong to no cluster: their explaining rows are found by resource_type and
-- resource_id alone ('id' rules only), and there is no node list to wait for.
-- name: ClassifyGlobalObjectChanges :execrows
WITH rules AS MATERIALIZED (
    SELECT e.value->>'kind'                AS kind,
           e.value->>'change'              AS change,
           e.value->>'audit_type'          AS audit_type,
           e.value->>'match_key'           AS match_key,
           (e.value->>'untasked')::bool    AS untasked,
           e.value->>'action_re'           AS action_re,
           e.value->>'except_re'           AS except_re,
           e.value->>'worker_re'           AS worker_re,
           e.value->>'field_re'            AS field_re
    FROM jsonb_array_elements(sqlc.arg('rules')::jsonb) AS e
), pending AS MATERIALIZED (
    SELECT c.id, c.resource_type, c.resource_id, c.change,
           GREATEST(LEAST(c.last_seen_at, c.observed_at) - interval '2 minutes',
                    c.observed_at - make_interval(secs => sqlc.arg('lookback_seconds')::int)) AS win_from,
           c.observed_at + interval '30 seconds'                                           AS win_to
    FROM cluster_object_changes c
    WHERE c.cluster_id IS NULL
      AND c.explained IS NULL
      AND c.observed_at < sqlc.arg('settled_before')::timestamptz
), hits AS (
    SELECT p.id, r.field_re
    FROM pending p
    JOIN rules r ON r.kind = p.resource_type AND r.change IN ('', p.change) AND r.match_key = 'id'
    JOIN audit_log a ON a.resource_type = r.audit_type AND a.resource_id = p.resource_id
                    AND a.created_at BETWEEN p.win_from - interval '1 day' AND p.win_to + interval '10 minutes'
    LEFT JOIN task_history th ON th.upid = a.details->>'upid'
    WHERE (NOT r.untasked OR a.details->>'upid' IS NULL)
      AND (r.action_re = '' OR a.action ~ r.action_re)
      AND (r.except_re = '' OR a.action !~ r.except_re)
      AND (r.worker_re = '' OR COALESCE(split_part(a.details->>'upid', ':', 6) ~ r.worker_re, false))
      AND COALESCE(th.started_at, a.created_at) <= p.win_to
      AND CASE WHEN th.upid IS NULL THEN a.created_at ELSE COALESCE(th.finished_at, now()) END >= p.win_from
)
UPDATE cluster_object_changes c
SET explained = NOT EXISTS (
    SELECT 1
    FROM unnest(CASE WHEN c.change = 'changed' AND cardinality(c.fields) > 0 THEN c.fields ELSE ARRAY['']::text[] END)
         AS f(name)
    WHERE NOT EXISTS (
        SELECT 1 FROM hits h
        WHERE h.id = c.id
          AND (c.change <> 'changed' OR h.field_re = '' OR f.name ~ h.field_re)))
FROM pending p
WHERE c.id = p.id;
