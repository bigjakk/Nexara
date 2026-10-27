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
-- details.rekeyed_from = {resource_type, resource_id, action}. A row two
-- statements touch keeps its FIRST original. The rest of the old shape also
-- survives in details (name, path, sid, position, task_type), and the down
-- migration restores the originals from rekeyed_from.
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
--     the new key it would show twice for ever. A copy with a Nexara row for the
--     same apply within two minutes, made by an API token, stays where it is.

-- SDN: the six kinds all filed under "sdn", the kind only in the action
-- (sdn_zone_created ...). A zone and a VNet may share an id, so the History of
-- one must not read the other's rows. The apply stays "sdn", id "cluster".
UPDATE audit_log
SET resource_type = regexp_replace(action, '^(sdn_[a-z]+)_(created|updated|deleted)$', '\1'),
    details = CASE WHEN details ? 'rekeyed_from' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type = 'sdn'
  AND action ~ '^sdn_(zone|vnet|subnet|controller|ipam|dns)_(created|updated|deleted)$';

-- An SDN subnet is addressed by the id Proxmox derives from its zone and CIDR
-- ("zone01-192.0.2.0-24"). The create was filed under the CIDR it took
-- ("vnet01/192.0.2.0/24"); where a later update or delete of the same subnet
-- carries the id, or the VNet's create row says its zone, the create row moves
-- to it.
WITH creates AS (
    SELECT c.id, c.cluster_id,
           split_part(c.resource_id, '/', 1) AS vnet,
           replace(substr(c.resource_id, strpos(c.resource_id, '/') + 1), '/', '-') AS dashed
    FROM audit_log c
    WHERE c.resource_type = 'sdn_subnet'
      AND c.action = 'sdn_subnet_created'
      AND c.resource_id ~ '^[^/]+/[^/]+/[0-9]+$'
), resolved AS (
    SELECT cr.id,
           COALESCE(
               (SELECT substr(u.resource_id, length(cr.vnet) + 2)
                FROM audit_log u
                WHERE u.cluster_id = cr.cluster_id
                  AND u.resource_type = 'sdn_subnet'
                  AND u.action IN ('sdn_subnet_updated', 'sdn_subnet_deleted')
                  AND starts_with(u.resource_id, cr.vnet || '/')
                  AND right(u.resource_id, length(cr.dashed) + 1) = '-' || cr.dashed
                ORDER BY u.created_at
                LIMIT 1),
               (SELECT z.details->>'zone' || '-' || cr.dashed
                FROM audit_log z
                WHERE z.cluster_id = cr.cluster_id
                  AND z.resource_type = 'sdn_vnet'
                  AND z.action = 'sdn_vnet_created'
                  AND z.resource_id = cr.vnet
                  AND COALESCE(z.details->>'zone', '') <> ''
                ORDER BY z.created_at DESC
                LIMIT 1)
           ) AS subnet_id
    FROM creates cr
)
UPDATE audit_log c
SET resource_id = split_part(c.resource_id, '/', 1) || '/' || r.subnet_id,
    details = CASE WHEN c.details ? 'rekeyed_from' THEN c.details
                   ELSE c.details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', c.resource_type, 'resource_id', c.resource_id, 'action', c.action)) END
FROM resolved r
WHERE c.id = r.id AND r.subnet_id IS NOT NULL;

-- Firewall: rules and the cluster options filed under "network" (and, in the
-- first releases, under "firewall"), beside the interface events. A rule is
-- addressed by the set it belongs to (its position moves with every insert), so
-- cluster/rule/3 and vm/100/rule/3 become the sets cluster and vm/100; the
-- position is in the details. Only the shapes the handlers wrote: any other id
-- under these actions is left as it is.
UPDATE audit_log
SET resource_type = 'firewall_rule',
    resource_id = regexp_replace(resource_id, '/rule/[0-9]+$', ''),
    details = CASE WHEN details ? 'rekeyed_from' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type IN ('network', 'firewall')
  AND action IN ('firewall_rule_created', 'firewall_rule_updated', 'firewall_rule_deleted')
  AND resource_id ~ '^(cluster|vm/[0-9]+)(/rule/[0-9]+)?$';

-- The cluster options only: a guest's options (vm/100/options) are not the
-- cluster's.
UPDATE audit_log
SET resource_type = 'firewall_options',
    resource_id = 'cluster',
    details = CASE WHEN details ? 'rekeyed_from' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type IN ('network', 'firewall')
  AND action = 'firewall_options_updated'
  AND resource_id = 'cluster/options';

-- Firewall aliases, IP sets and security groups: Proxmox folds the name to lower
-- case ("MyNet" and "mynet" are one alias; a [group ...] and [ipset ...] header
-- is lc()'d when the config is parsed).
UPDATE audit_log
SET resource_id = lower(resource_id),
    details = CASE WHEN details ? 'rekeyed_from' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type IN ('firewall_alias', 'firewall_ipset', 'firewall_security_group', 'firewall_security_group_rule')
  AND resource_id <> lower(resource_id);

-- An IP set entry is <set>/<entry>: only the set is a name.
UPDATE audit_log
SET resource_id = lower(split_part(resource_id, '/', 1)) || substr(resource_id, length(split_part(resource_id, '/', 1)) + 1),
    details = CASE WHEN details ? 'rekeyed_from' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type = 'firewall_ipset_entry'
  AND split_part(resource_id, '/', 1) <> lower(split_part(resource_id, '/', 1));

-- ACL paths: Proxmox collapses repeated slashes and drops a trailing one
-- (PVE::AccessControl::normalize_path).
UPDATE audit_log
SET resource_id = regexp_replace(regexp_replace(resource_id, '/{2,}', '/', 'g'), '(.)/$', '\1'),
    details = CASE WHEN details ? 'rekeyed_from' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
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
    details = CASE WHEN a.details ? 'rekeyed_from' THEN a.details
                   ELSE a.details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', a.resource_type, 'resource_id', a.resource_id, 'action', a.action)) END
FROM (VALUES
    ('cephcreateosd',   'ceph_osd',  'osd_create',  '',     '^[A-Za-z0-9/._+@-]{1,128}$'),
    ('cephdestroyosd',  'ceph_osd',  'osd_destroy', 'osd.', '^[0-9]{1,9}$'),
    ('cephcreatemon',   'ceph_mon',  'mon_create',  'mon.', '^[A-Za-z0-9]([A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$'),
    ('cephdestroymon',  'ceph_mon',  'mon_destroy', 'mon.', '^[A-Za-z0-9]([A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$'),
    ('cephcreatemgr',   'ceph_mgr',  'mgr_create',  'mgr.', '^[A-Za-z0-9]([A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$'),
    ('cephdestroymgr',  'ceph_mgr',  'mgr_destroy', 'mgr.', '^[A-Za-z0-9]([A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$'),
    ('cephcreatemds',   'ceph_mds',  'mds_create',  'mds.', '^[A-Za-z0-9]([A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$'),
    ('cephdestroymds',  'ceph_mds',  'mds_destroy', 'mds.', '^[A-Za-z0-9]([A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$'),
    ('cephfscreate',    'ceph_fs',   'fs_create',   '',     '^[^[:cntrl:]]+$'),
    ('cephdestroyfs',   'ceph_fs',   'fs_destroy',  '',     '^[^[:cntrl:]]+$'),
    ('cephcreatepool',  'ceph_pool', 'create',      '',     '^[^[:cntrl:]]+$'),
    ('cephdestroypool', 'ceph_pool', 'delete',      '',     '^[^[:cntrl:]]+$'),
    ('cephsetpool',     'ceph_pool', 'update',      '',     '^[^[:cntrl:]]+$')
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
    details = CASE WHEN details ? 'rekeyed_from' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE resource_type = 'proxmox_task'
  AND source = 'proxmox'
  AND action IN ('srvstart', 'srvstop', 'srvrestart')
  AND details->>'resource_id' = resource_id
  AND (resource_id ~ '^osd\.[0-9]{1,9}$'
       OR resource_id ~ '^(mon|mgr|mds)\.[A-Za-z0-9]([A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$')
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
    details = CASE WHEN details ? 'rekeyed_from' THEN details
                   ELSE details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', resource_type, 'resource_id', resource_id, 'action', action)) END
WHERE source = 'proxmox'
  AND action IN ('acme_new_cert', 'acmerenew', 'acmerevoke')
  AND resource_type IN ('node', 'proxmox_task')
  AND details->>'task_type' IN ('acmenewcert', 'acmerenew', 'acmerevoke');

-- The SDN apply (reloadnetworkall), ingested before the collector knew the
-- cluster as its object — unless Nexara made the apply itself, which has its own
-- row.
UPDATE audit_log c
SET resource_type = 'sdn',
    resource_id = 'cluster',
    action = 'sdn_applied',
    details = CASE WHEN c.details ? 'rekeyed_from' THEN c.details
                   ELSE c.details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', c.resource_type, 'resource_id', c.resource_id, 'action', c.action)) END
WHERE c.source = 'proxmox'
  AND c.resource_type = 'network'
  AND c.action = 'reload'
  AND c.details->>'task_type' = 'reloadnetworkall'
  AND NOT (COALESCE(c.details->>'proxmox_user', '') LIKE '%!%'
           AND EXISTS (SELECT 1 FROM audit_log n
                       WHERE n.cluster_id = c.cluster_id
                         AND n.source = 'nexara'
                         AND n.resource_type = 'sdn'
                         AND n.resource_id = 'cluster'
                         AND n.action = 'sdn_applied'
                         AND n.created_at BETWEEN c.created_at - interval '2 minutes' AND c.created_at + interval '2 minutes'));

-- An SDN apply reloads every node's network as a task of its own (srvreload,
-- service "networking"). Those that fall within an apply — an ingested
-- reloadnetworkall, from its start to its end in task_history (a quarter of an
-- hour when that is gone), with ten seconds' slack — are parts of it, not
-- network applies: the action says so and details.parent_upid names the apply.
WITH reloads AS (
    SELECT c.id,
           (SELECT p.details->>'upid'
            FROM audit_log p
            LEFT JOIN task_history th ON th.cluster_id = p.cluster_id AND th.upid = p.details->>'upid'
            WHERE p.cluster_id = c.cluster_id
              AND p.source = 'proxmox'
              AND p.details->>'task_type' = 'reloadnetworkall'
              AND p.details->>'upid' IS NOT NULL
              AND c.created_at BETWEEN p.created_at - interval '10 seconds'
                  AND COALESCE(th.finished_at, p.created_at + interval '15 minutes') + interval '10 seconds'
            ORDER BY p.created_at DESC
            LIMIT 1) AS parent_upid
    FROM audit_log c
    WHERE c.source = 'proxmox'
      AND c.resource_type = 'node'
      AND c.action = 'service_reload'
      AND c.resource_id = 'networking'
      AND c.details->>'task_type' = 'srvreload'
      AND COALESCE(c.details->>'node', '') <> ''
)
UPDATE audit_log c
SET resource_type = 'network',
    resource_id = c.details->>'node',
    action = 'network_reloaded_by_sdn',
    details = CASE WHEN c.details ? 'rekeyed_from' THEN c.details
                   ELSE c.details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', c.resource_type, 'resource_id', c.resource_id, 'action', c.action)) END
              || jsonb_build_object('parent_upid', r.parent_upid)
FROM reloads r
WHERE c.id = r.id AND r.parent_upid IS NOT NULL;

-- The rest are node network applies, ingested as a reload of the service
-- "networking" — unless Nexara made the apply itself, which has its own row.
UPDATE audit_log c
SET resource_type = 'network',
    resource_id = c.details->>'node',
    action = 'network_applied',
    details = CASE WHEN c.details ? 'rekeyed_from' THEN c.details
                   ELSE c.details || jsonb_build_object('rekeyed_from', jsonb_build_object('resource_type', c.resource_type, 'resource_id', c.resource_id, 'action', c.action)) END
WHERE c.source = 'proxmox'
  AND c.resource_type = 'node'
  AND c.action = 'service_reload'
  AND c.resource_id = 'networking'
  AND c.details->>'task_type' = 'srvreload'
  AND COALESCE(c.details->>'node', '') <> ''
  AND NOT (COALESCE(c.details->>'proxmox_user', '') LIKE '%!%'
           AND EXISTS (SELECT 1 FROM audit_log n
                       WHERE n.cluster_id = c.cluster_id
                         AND n.source = 'nexara'
                         AND n.resource_type = 'network'
                         AND n.resource_id = c.details->>'node'
                         AND n.action = 'network_applied'
                         AND n.created_at BETWEEN c.created_at - interval '2 minutes' AND c.created_at + interval '2 minutes'));
