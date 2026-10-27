-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Re-files existing audit rows under the key rows written from this release on
-- use, so an object of the cluster page (an SDN zone, a firewall rule set, a
-- Ceph daemon, a certificate ...) has ONE (resource_type, resource_id) in the
-- log and its History reads it from one place.
--
-- This matches trigger #7 (a data back-fill) and resolves it itself: it
-- rewrites resource_type and, where an old spelling of the key differs,
-- resource_id and (for tasks the collector ingested) action. Nothing is
-- deleted, details are untouched, and every statement matches only the OLD
-- shape, so a second run changes nothing. It is one transaction: if it is
-- interrupted it leaves nothing behind. User-visible: the Events page's type
-- filter now lists the SDN kinds and the firewall rule and options apart, and
-- old rows appear under them (release note, not a breaking change).
--
-- What it does NOT touch, because the old row does not say enough: the create
-- row of an SDN subnet (filed under the CIDR the create took; the id Proxmox
-- derived from it also needs the zone), and a stale HA create for a guest that
-- no longer exists (a bare VMID that vms cannot resolve to vm: or ct:).

-- SDN: the six kinds all filed under "sdn", the kind only in the action
-- (sdn_zone_created ...). A zone and a VNet may share an id, so the History of
-- one must not read the other's rows. The apply stays "sdn", id "cluster".
UPDATE audit_log
SET resource_type = regexp_replace(action, '^(sdn_[a-z]+)_(created|updated|deleted)$', '\1')
WHERE resource_type = 'sdn'
  AND action ~ '^sdn_(zone|vnet|subnet|controller|ipam|dns)_(created|updated|deleted)$';

-- Firewall: rules and the cluster options filed under "network", beside the
-- interface events. A rule is addressed by the set it belongs to (its position
-- moves with every insert), so cluster/rule/3 and vm/100/rule/3 become the
-- sets cluster and vm/100; the position is in the details.
UPDATE audit_log
SET resource_type = 'firewall_rule',
    resource_id = regexp_replace(resource_id, '/rule/[0-9]+$', '')
WHERE resource_type = 'network'
  AND action IN ('firewall_rule_created', 'firewall_rule_updated', 'firewall_rule_deleted');

UPDATE audit_log
SET resource_type = 'firewall_options', resource_id = 'cluster'
WHERE resource_type = 'network' AND action = 'firewall_options_updated';

-- Firewall aliases: Proxmox folds the name to lower case on every route, so
-- "MyNet" and "mynet" are one alias.
UPDATE audit_log
SET resource_id = lower(resource_id)
WHERE resource_type = 'firewall_alias' AND resource_id <> lower(resource_id);

-- ACL paths: Proxmox collapses repeated slashes and drops a trailing one
-- (PVE::AccessControl::normalize_path).
UPDATE audit_log
SET resource_id = regexp_replace(regexp_replace(resource_id, '/{2,}', '/', 'g'), '(.)/$', '\1')
WHERE resource_type = 'pve_acl'
  AND resource_id <> regexp_replace(regexp_replace(resource_id, '/{2,}', '/', 'g'), '(.)/$', '\1');

-- HA resources created, changed or removed by a bare VMID (the shortcut
-- Proxmox resolves to vm:N or ct:N by the guest's type): the sid they are
-- listed under, for the guests that still exist.
UPDATE audit_log a
SET resource_id = CASE v.type WHEN 'lxc' THEN 'ct:' ELSE 'vm:' END || a.resource_id
FROM vms v
WHERE a.resource_type = 'ha_resource'
  AND a.resource_id ~ '^[0-9]{1,9}$'
  AND v.cluster_id = a.cluster_id
  AND v.vmid = CASE WHEN a.resource_id ~ '^[0-9]{1,9}$' THEN a.resource_id::int END;

-- Tasks the collector ingested from Proxmox, now filed under the object they
-- changed: the identity Nexara's own handlers record for the same work. A Ceph
-- daemon is keyed by its number or name rather than "osd.12"; a pool by its
-- name; a certificate by its node; the SDN apply by the cluster.
UPDATE audit_log a
SET resource_type = m.resource_type,
    action = m.action,
    resource_id = substr(a.resource_id, length(m.strip) + 1)
FROM (VALUES
    ('cephcreateosd',   'ceph_osd',  'osd_create',  ''),
    ('cephdestroyosd',  'ceph_osd',  'osd_destroy', 'osd.'),
    ('cephcreatemon',   'ceph_mon',  'mon_create',  'mon.'),
    ('cephdestroymon',  'ceph_mon',  'mon_destroy', 'mon.'),
    ('cephcreatemgr',   'ceph_mgr',  'mgr_create',  'mgr.'),
    ('cephdestroymgr',  'ceph_mgr',  'mgr_destroy', 'mgr.'),
    ('cephcreatemds',   'ceph_mds',  'mds_create',  'mds.'),
    ('cephdestroymds',  'ceph_mds',  'mds_destroy', 'mds.'),
    ('cephfscreate',    'ceph_fs',   'fs_create',   ''),
    ('cephdestroyfs',   'ceph_fs',   'fs_destroy',  ''),
    ('cephcreatepool',  'ceph_pool', 'create',      ''),
    ('cephdestroypool', 'ceph_pool', 'delete',      ''),
    ('cephsetpool',     'ceph_pool', 'update',      '')
) AS m(task_type, resource_type, action, strip)
WHERE a.resource_type = 'proxmox_task'
  AND a.source = 'proxmox'
  AND a.action = m.task_type
  AND a.resource_id LIKE m.strip || '%'
  AND length(a.resource_id) > length(m.strip);

-- A Ceph daemon started, stopped or restarted in the Proxmox GUI (srvstart,
-- srvstop, srvrestart on "osd.3", "mon.pve-01" ...).
UPDATE audit_log
SET resource_type = 'ceph_' || split_part(resource_id, '.', 1),
    action = split_part(resource_id, '.', 1) || '_' || substr(action, 4),
    resource_id = substr(resource_id, strpos(resource_id, '.') + 1)
WHERE resource_type = 'proxmox_task'
  AND source = 'proxmox'
  AND action IN ('srvstart', 'srvstop', 'srvrestart')
  AND resource_id ~ '^(osd|mon|mgr|mds)\..+$';

UPDATE audit_log
SET resource_type = 'certificate',
    action = CASE details->>'task_type'
        WHEN 'acmenewcert' THEN 'ordered'
        WHEN 'acmerenew'   THEN 'renewed'
        ELSE 'revoked'
    END
WHERE source = 'proxmox'
  AND resource_type IN ('node', 'proxmox_task')
  AND details->>'task_type' IN ('acmenewcert', 'acmerenew', 'acmerevoke');

UPDATE audit_log
SET resource_type = 'sdn', resource_id = 'cluster', action = 'sdn_applied'
WHERE source = 'proxmox'
  AND resource_type = 'network'
  AND details->>'task_type' = 'reloadnetworkall';

UPDATE audit_log
SET resource_type = 'network', resource_id = details->>'node', action = 'network_applied'
WHERE source = 'proxmox'
  AND resource_type = 'node'
  AND details->>'task_type' = 'srvreload'
  AND resource_id = 'networking'
  AND COALESCE(details->>'node', '') <> '';
