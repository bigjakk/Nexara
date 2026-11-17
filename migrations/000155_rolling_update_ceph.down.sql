ALTER TABLE rolling_update_nodes
    DROP COLUMN IF EXISTS ceph_wait_started_at,
    DROP COLUMN IF EXISTS ceph_wait_reason;

ALTER TABLE rolling_update_jobs
    DROP COLUMN IF EXISTS pause_reason,
    DROP COLUMN IF EXISTS ceph_noout_state,
    DROP COLUMN IF EXISTS ceph_osds_baseline,
    DROP COLUMN IF EXISTS ceph_present,
    DROP COLUMN IF EXISTS ceph_noout,
    DROP COLUMN IF EXISTS ceph_wait;
