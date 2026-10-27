-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Re-files existing audit rows under the key rows written from this release on
-- use, so an object of the cluster page (an SDN zone, a firewall rule set, a
-- Ceph daemon, a certificate ...) has ONE (resource_type, resource_id) in the
-- log and its History reads it from one place.
--
-- This matches trigger #7 (a data back-fill) and resolves it itself. It
-- rewrites resource_type and, where an old spelling of the key differs,
-- resource_id and (for tasks the collector ingested) action. Nothing is deleted.
-- It is one transaction: if it is interrupted it leaves nothing behind.
-- User-visible: the Events page's type filter now lists the SDN kinds, the
-- firewall rule and options, certificates and the Ceph kinds apart, and old rows
-- appear under them (release note, not a breaking change).
--
-- What it leaves a trace of. audit_log is a security record, so every statement
-- that changes a row records the originals, in the same UPDATE, as
-- details.rekeyed_from = {by, resource_type, resource_id, action}, where `by` is
-- this migration's NAME (numbers change when branches merge). A row two of its
-- statements touch keeps its FIRST original. The rest of the old shape also
-- survives in details (name, path, sid, position, task_type), and the down
-- migration restores the originals of the rows tagged with this name, and only
-- those.
--
-- What it can run twice over. Each statement's WHERE matches only the OLD shape
-- of a row, so a second run changes nothing and the rows it leaves are the same
-- rows it left the first time.
--
-- What it does NOT touch, because the old row does not say enough or says
-- something else:
--   - a stale HA resource row written with a bare VMID ("109"). Proxmox resolves
--     that to vm:109 or ct:109 by the guest's type, but today's vms row is not
--     the guest the row was about — VMIDs are reused — so picking vm: or ct: from
--     it could land a row about an earlier guest in the current guest's History.
--     Bare rows stay as they are and still show in the HA tab's History;
--   - the create row of an SDN subnet it cannot resolve: it was filed under the
--     CIDR sent, and the id Proxmox derived also needs the zone. It is re-filed
--     where a later update or delete of the same subnet, or the VNet's own
--     create row, says what the id is;
--   - an IP set entry created with host bits set ("192.0.2.5/24", which Proxmox
--     stores as "192.0.2.0/24"): only the set's name is lower-cased;
--   - a Proxmox-sourced copy of an apply Nexara made itself. Until now the SDN
--     apply and the node network apply did not keep the task's UPID, so the
--     collector ingested each as a second row beside Nexara's own: filed under
--     the new key it would show twice for ever. A copy stays where it is when
--     Nexara has a row for the same apply within two minutes (same cluster, and
--     for a node apply the same node) and the copy was made by an API token of
--     the user the cluster is configured with: the user in the UPID, which is
--     where Proxmox keeps the token for every task (the task list moves it into a
--     field of its own once the task has finished), with the same user@realm as
--     the cluster's token_id, so that an earlier token of that user counts and
--     another tool's does not.
--
-- Time. A migration gets about three minutes at container start before the
-- orchestrator replaces the task and the transaction rolls back. No statement
-- reads audit_log whole: each is bounded by its indexes on resource_type or
-- action, and the one lookup per row (the parent of a reload) by a time range.
-- Measured on a synthetic audit_log of 1,001,000 rows, 94,589 of them to re-file
-- (far more than an install holds of these kinds): 9.5 s with the table cached,
-- 12.8 s from empty shared buffers, the commit 23 ms; the down takes 6.7 s. The
-- time is mostly the writing of the re-filed rows, about 0.1 ms each.

-- SDN: the six kinds all filed under "sdn", the kind only in the action
-- (sdn_zone_created ...). A zone and a VNet may share an id, so the History of
-- one must not read the other's rows. The apply stays "sdn", id "cluster".
UPDATE audit_log
SET resource_type = regexp_replace(action, '^(sdn_[a-z]+)_(created|updated|deleted)$', '\1'),
    details = CASE WHEN details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type = 'sdn'
  AND action ~ '^sdn_(zone|vnet|subnet|controller|ipam|dns)_(created|updated|deleted)$';

-- The kinds just written are new values of resource_type, which the table's
-- statistics do not know. Without them the next statement is planned as if its
-- three sets were a row each and compares every one with every other.
ANALYZE audit_log (resource_type);

-- An SDN subnet is addressed by the id Proxmox derives from its zone and CIDR
-- ("zone01-192.0.2.0-24"). The create was filed under the CIDR it took
-- ("vnet01/192.0.2.0/24"); where an update or delete of the same subnet carries
-- the id, or the VNet's create row (the latest before the subnet's) says its
-- zone, the create row moves to it. The candidates come from joins of three
-- sets, each read once, so the cost is not that of a lookup per create.
WITH creates AS MATERIALIZED (
    SELECT c.id, c.cluster_id, c.created_at,
           split_part(c.resource_id, '/', 1) AS vnet,
           replace(substr(c.resource_id, strpos(c.resource_id, '/') + 1), '/', '-') AS dashed
    FROM audit_log c
    WHERE c.resource_type = 'sdn_subnet'
      AND c.action = 'sdn_subnet_created'
      AND c.resource_id ~ '^[^/]+/[^/]+/[0-9]+$'
), edits AS MATERIALIZED (
    SELECT u.cluster_id, split_part(u.resource_id, '/', 1) AS vnet, u.resource_id, u.created_at
    FROM audit_log u
    WHERE u.resource_type = 'sdn_subnet'
      AND u.action IN ('sdn_subnet_updated', 'sdn_subnet_deleted')
), vnets AS MATERIALIZED (
    SELECT z.cluster_id, z.resource_id AS vnet, z.created_at, z.details->>'zone' AS zone
    FROM audit_log z
    WHERE z.resource_type = 'sdn_vnet'
      AND z.action = 'sdn_vnet_created'
      AND COALESCE(z.details->>'zone', '') <> ''
), resolved AS (
    -- An edit's id first (the earliest), else the zone of the latest VNet row before the create.
    SELECT DISTINCT ON (id) id, subnet_id
    FROM (
        SELECT cr.id, 0 AS pref, u.created_at AS earliest, NULL::timestamptz AS latest,
               substr(u.resource_id, length(cr.vnet) + 2) AS subnet_id
        FROM creates cr
        JOIN edits u ON u.cluster_id = cr.cluster_id
                    AND u.vnet = cr.vnet
                    AND starts_with(u.resource_id, cr.vnet || '/')
                    AND right(u.resource_id, length(cr.dashed) + 1) = '-' || cr.dashed
        UNION ALL
        SELECT cr.id, 1, NULL, z.created_at, z.zone || '-' || cr.dashed
        FROM creates cr
        JOIN vnets z ON z.cluster_id = cr.cluster_id
                    AND z.vnet = cr.vnet
                    AND z.created_at <= cr.created_at
    ) candidates
    ORDER BY id, pref, earliest, latest DESC
)
UPDATE audit_log c
SET resource_id = split_part(c.resource_id, '/', 1) || '/' || r.subnet_id,
    details = CASE WHEN c.details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN c.details
                   ELSE c.details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', c.resource_type, 'resource_id', c.resource_id, 'action', c.action)) END
FROM resolved r
WHERE c.id = r.id;

-- Firewall: rules and the cluster options filed under "network" (and, in the
-- first releases, under "firewall"), beside the interface events. A rule is
-- addressed by the set it belongs to (its position moves with every insert), so
-- cluster/rule/3 and vm/100/rule/3 become the sets cluster and vm/100; the
-- position is in the details. Only the shapes the handlers wrote: any other id
-- under these actions is left as it is.
UPDATE audit_log
SET resource_type = 'firewall_rule',
    resource_id = regexp_replace(resource_id, '/rule/[0-9]+$', ''),
    details = CASE WHEN details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type IN ('network', 'firewall')
  AND action IN ('firewall_rule_created', 'firewall_rule_updated', 'firewall_rule_deleted')
  AND resource_id ~ '^(cluster|vm/[0-9]+)(/rule/[0-9]+)?$';

-- The cluster options only: a guest's options (vm/100/options) are not the
-- cluster's.
UPDATE audit_log
SET resource_type = 'firewall_options',
    resource_id = 'cluster',
    details = CASE WHEN details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type IN ('network', 'firewall')
  AND action = 'firewall_options_updated'
  AND resource_id = 'cluster/options';

-- Firewall aliases, IP sets and security groups: Proxmox folds the name to lower
-- case ("MyNet" and "mynet" are one alias; a [group ...] and [ipset ...] header
-- is lc()'d when the config is parsed).
UPDATE audit_log
SET resource_id = lower(resource_id),
    details = CASE WHEN details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type IN ('firewall_alias', 'firewall_ipset', 'firewall_security_group', 'firewall_security_group_rule')
  AND resource_id <> lower(resource_id);

-- An IP set entry is <set>/<entry>: only the set is a name.
UPDATE audit_log
SET resource_id = lower(split_part(resource_id, '/', 1)) || substr(resource_id, length(split_part(resource_id, '/', 1)) + 1),
    details = CASE WHEN details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type = 'firewall_ipset_entry'
  AND split_part(resource_id, '/', 1) <> lower(split_part(resource_id, '/', 1));

-- ACL paths: Proxmox collapses repeated slashes and drops a trailing one
-- (PVE::AccessControl::normalize_path).
UPDATE audit_log
SET resource_id = regexp_replace(regexp_replace(resource_id, '/{2,}', '/', 'g'), '(.)/$', '\1'),
    details = CASE WHEN details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type = 'pve_acl'
  AND resource_id <> regexp_replace(regexp_replace(resource_id, '/{2,}', '/', 'g'), '(.)/$', '\1');

-- Tasks the collector ingested from Proxmox, now filed under the object they
-- changed: the identity Nexara's own handlers record for the same work. A Ceph
-- daemon is keyed by its number or name rather than "osd.12"; a pool by its
-- name. The id has to be one Proxmox could have minted for the kind (the shapes
-- the collector now checks, internal/collector/task_mapping.go), has to be the
-- id the task carried (an empty one was filed under its node, which is not a
-- pool), and the UPID, where it was kept, has to say the same task type.
UPDATE audit_log a
SET resource_type = m.resource_type,
    action = m.action,
    resource_id = substr(a.resource_id, length(m.strip) + 1),
    details = CASE WHEN a.details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN a.details
                   ELSE a.details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', a.resource_type, 'resource_id', a.resource_id, 'action', a.action)) END
FROM (VALUES
    ('cephcreateosd',   'ceph_osd',  'osd_create',  '',     '^[^[:cntrl:]\u00ad\u0600\u0601-\u0605\u061c\u06dd\u070f\u0890\u0891\u08e2\u180e\u200b\u200c-\u200f\u202a-\u202e\u2060-\u2064\u2066-\u206f\ufeff\ufff9\ufffa-\ufffb\U000110bd\U000110cd\U00013430-\U0001343f\U0001bca0-\U0001bca3\U0001d173-\U0001d17a\U000e0001\U000e0020\U000e0021-\U000e007f]+$'),
    ('cephdestroyosd',  'ceph_osd',  'osd_destroy', 'osd.', '^[0-9]{1,9}$'),
    ('cephcreatemon',   'ceph_mon',  'mon_create',  'mon.', '^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$'),
    ('cephdestroymon',  'ceph_mon',  'mon_destroy', 'mon.', '^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$'),
    ('cephcreatemgr',   'ceph_mgr',  'mgr_create',  'mgr.', '^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$'),
    ('cephdestroymgr',  'ceph_mgr',  'mgr_destroy', 'mgr.', '^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$'),
    ('cephcreatemds',   'ceph_mds',  'mds_create',  'mds.', '^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$'),
    ('cephdestroymds',  'ceph_mds',  'mds_destroy', 'mds.', '^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$'),
    ('cephfscreate',    'ceph_fs',   'fs_create',   '',     '^[^:/[:space:][:cntrl:]\u00ad\u0600\u0601-\u0605\u061c\u06dd\u070f\u0890\u0891\u08e2\u180e\u200b\u200c-\u200f\u202a-\u202e\u2060-\u2064\u2066-\u206f\ufeff\ufff9\ufffa-\ufffb\U000110bd\U000110cd\U00013430-\U0001343f\U0001bca0-\U0001bca3\U0001d173-\U0001d17a\U000e0001\U000e0020\U000e0021-\U000e007f]+$'),
    ('cephdestroyfs',   'ceph_fs',   'fs_destroy',  '',     '^[^:/[:space:][:cntrl:]\u00ad\u0600\u0601-\u0605\u061c\u06dd\u070f\u0890\u0891\u08e2\u180e\u200b\u200c-\u200f\u202a-\u202e\u2060-\u2064\u2066-\u206f\ufeff\ufff9\ufffa-\ufffb\U000110bd\U000110cd\U00013430-\U0001343f\U0001bca0-\U0001bca3\U0001d173-\U0001d17a\U000e0001\U000e0020\U000e0021-\U000e007f]+$'),
    ('cephcreatepool',  'ceph_pool', 'create',      '',     '^[^:/[:space:][:cntrl:]\u00ad\u0600\u0601-\u0605\u061c\u06dd\u070f\u0890\u0891\u08e2\u180e\u200b\u200c-\u200f\u202a-\u202e\u2060-\u2064\u2066-\u206f\ufeff\ufff9\ufffa-\ufffb\U000110bd\U000110cd\U00013430-\U0001343f\U0001bca0-\U0001bca3\U0001d173-\U0001d17a\U000e0001\U000e0020\U000e0021-\U000e007f]+$'),
    ('cephdestroypool', 'ceph_pool', 'delete',      '',     '^[^:/[:space:][:cntrl:]\u00ad\u0600\u0601-\u0605\u061c\u06dd\u070f\u0890\u0891\u08e2\u180e\u200b\u200c-\u200f\u202a-\u202e\u2060-\u2064\u2066-\u206f\ufeff\ufff9\ufffa-\ufffb\U000110bd\U000110cd\U00013430-\U0001343f\U0001bca0-\U0001bca3\U0001d173-\U0001d17a\U000e0001\U000e0020\U000e0021-\U000e007f]+$'),
    ('cephsetpool',     'ceph_pool', 'update',      '',     '^[^:/[:space:][:cntrl:]\u00ad\u0600\u0601-\u0605\u061c\u06dd\u070f\u0890\u0891\u08e2\u180e\u200b\u200c-\u200f\u202a-\u202e\u2060-\u2064\u2066-\u206f\ufeff\ufff9\ufffa-\ufffb\U000110bd\U000110cd\U00013430-\U0001343f\U0001bca0-\U0001bca3\U0001d173-\U0001d17a\U000e0001\U000e0020\U000e0021-\U000e007f]+$')
) AS m(task_type, resource_type, action, strip, id_re)
WHERE a.resource_type = 'proxmox_task'
  AND a.source = 'proxmox'
  AND a.action = m.task_type
  AND a.details->>'resource_id' = a.resource_id
  AND starts_with(a.resource_id, m.strip)
  AND length(a.resource_id) - length(m.strip) <= 256
  AND substr(a.resource_id, length(m.strip) + 1) ~ m.id_re
  AND (a.details->>'upid' IS NULL OR split_part(a.details->>'upid', ':', 6) = m.task_type);

-- A Ceph daemon started, stopped or restarted in the Proxmox GUI (srvstart,
-- srvstop, srvrestart on "osd.3", "mon.pve-01" ...).
UPDATE audit_log
SET resource_type = 'ceph_' || split_part(resource_id, '.', 1),
    action = split_part(resource_id, '.', 1) || '_' || substr(action, 4),
    resource_id = substr(resource_id, strpos(resource_id, '.') + 1),
    details = CASE WHEN details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type = 'proxmox_task'
  AND source = 'proxmox'
  AND action IN ('srvstart', 'srvstop', 'srvrestart')
  AND details->>'resource_id' = resource_id
  AND (resource_id ~ '^osd\.[0-9]{1,9}$'
       OR resource_id ~ '^(mon|mgr|mds)\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$')
  AND (details->>'upid' IS NULL OR split_part(details->>'upid', ':', 6) = action);

-- Certificates are the node's.
UPDATE audit_log
SET resource_type = 'certificate',
    resource_id = COALESCE(NULLIF(details->>'node', ''), resource_id),
    action = CASE details->>'task_type'
        WHEN 'acmenewcert' THEN 'ordered'
        WHEN 'acmerenew'   THEN 'renewed'
        ELSE 'revoked'
    END,
    details = CASE WHEN details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE source = 'proxmox'
  AND action IN ('acme_new_cert', 'acmerenew', 'acmerevoke')
  AND resource_type IN ('node', 'proxmox_task')
  AND details->>'task_type' IN ('acmenewcert', 'acmerenew', 'acmerevoke');

-- The SDN apply (reloadnetworkall), ingested before the collector knew the
-- cluster as its object — unless Nexara made the apply itself, which has its own
-- row (see the end of the header for what makes a copy).
UPDATE audit_log c
SET resource_type = 'sdn',
    resource_id = 'cluster',
    action = 'sdn_applied',
    details = CASE WHEN c.details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN c.details
                   ELSE c.details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', c.resource_type, 'resource_id', c.resource_id, 'action', c.action)) END
WHERE c.source = 'proxmox'
  AND c.resource_type = 'network'
  AND c.action = 'reload'
  AND c.details->>'task_type' = 'reloadnetworkall'
  AND NOT (EXISTS (SELECT 1 FROM clusters cl
                   WHERE cl.id = c.cluster_id
                     AND split_part(c.details->>'upid', ':', 8) LIKE '%!%'
                     AND split_part(split_part(c.details->>'upid', ':', 8), '!', 1) = split_part(cl.token_id, '!', 1))
           AND EXISTS (SELECT 1 FROM audit_log n
                       WHERE n.cluster_id = c.cluster_id
                         AND n.source = 'nexara'
                         AND n.resource_type = 'sdn'
                         AND n.resource_id = 'cluster'
                         AND n.action = 'sdn_applied'
                         AND n.created_at BETWEEN c.created_at - interval '2 minutes' AND c.created_at + interval '2 minutes'));

-- The node network applies, ingested as a reload of the service "networking" —
-- unless Nexara made the apply itself, which has its own row. A reload that ran as
-- root@pam during an SDN apply, from its start to its end, is a part of that apply
-- (pve-network walks the node list and runs `pvesh set /nodes/<node>/network` for
-- each, as root, one after another): the row is annotated with the apply's UPID in
-- details.parent_upid, as the collector does for new ones, and filed as any other
-- network apply. The apply is an ingested reloadnetworkall started in the hour
-- before the reload, over by then (the end in task_history; a quarter of an hour
-- when that is gone), with 10 seconds' slack. The lookup is bounded by action and
-- time, so it is a range probe on audit_log's action index per reload.
UPDATE audit_log c
SET resource_type = 'network',
    resource_id = c.details->>'node',
    action = 'network_applied',
    details = (CASE WHEN c.details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys' THEN c.details
                    ELSE c.details || jsonb_build_object('rekeyed_from', jsonb_build_object('by', 'cluster_object_audit_keys', 'resource_type', c.resource_type, 'resource_id', c.resource_id, 'action', c.action)) END)
              || COALESCE(
                  (SELECT jsonb_build_object('parent_upid', p.details->>'upid')
                   FROM audit_log p
                   LEFT JOIN task_history th ON th.cluster_id = p.cluster_id AND th.upid = p.details->>'upid'
                   WHERE split_part(c.details->>'upid', ':', 8) = 'root@pam'
                     AND p.action IN ('reload', 'sdn_applied')
                     AND p.created_at BETWEEN c.created_at - interval '1 hour' AND c.created_at
                     AND p.cluster_id = c.cluster_id
                     AND p.source = 'proxmox'
                     AND p.details->>'task_type' = 'reloadnetworkall'
                     AND p.details->>'upid' IS NOT NULL
                     AND c.created_at <= COALESCE(th.finished_at, p.created_at + interval '15 minutes') + interval '10 seconds'
                   ORDER BY p.created_at DESC
                   LIMIT 1),
                  '{}'::jsonb)
WHERE c.source = 'proxmox'
  AND c.resource_type = 'node'
  AND c.action = 'service_reload'
  AND c.resource_id = 'networking'
  AND c.details->>'task_type' = 'srvreload'
  AND COALESCE(c.details->>'node', '') <> ''
  AND NOT (EXISTS (SELECT 1 FROM clusters cl
                   WHERE cl.id = c.cluster_id
                     AND split_part(c.details->>'upid', ':', 8) LIKE '%!%'
                     AND split_part(split_part(c.details->>'upid', ':', 8), '!', 1) = split_part(cl.token_id, '!', 1))
           AND EXISTS (SELECT 1 FROM audit_log n
                       WHERE n.cluster_id = c.cluster_id
                         AND n.source = 'nexara'
                         AND n.resource_type = 'network'
                         AND n.resource_id = c.details->>'node'
                         AND n.action = 'network_applied'
                         AND n.created_at BETWEEN c.created_at - interval '2 minutes' AND c.created_at + interval '2 minutes'));
