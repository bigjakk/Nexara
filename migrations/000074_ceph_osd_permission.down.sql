UPDATE permissions
SET description = 'Manage Ceph pools'
WHERE action = 'manage' AND resource = 'ceph';
