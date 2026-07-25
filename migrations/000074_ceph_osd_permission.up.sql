-- Broaden the manage:ceph permission description to cover OSD lifecycle.
--
-- manage:ceph was introduced (000016) when the only mutating Ceph endpoints
-- were pool create/delete, so its description reads "Manage Ceph pools" — the
-- string the role editor shows operators when they grant it. OSD in/out and
-- daemon start/stop/restart now sit behind the same permission, which makes
-- that description an understatement of what granting it allows.
--
-- The permission itself is unchanged: no new grants, and every role that has
-- manage:ceph keeps exactly the access it had.

UPDATE permissions
SET description = 'Manage Ceph pools and OSD lifecycle'
WHERE action = 'manage' AND resource = 'ceph';
