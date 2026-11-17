-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Adds columns with defaults only, so every existing row is filled by
-- PostgreSQL itself and nothing is rewritten by hand.
--
-- What a rolling update job does about Ceph (see internal/rolling/ceph.go):
--   ceph_wait            wait for every PG to be active+clean, and the OSDs
--                        up to the baseline, before a node is taken down and
--                        before a node is called healthy. Default on.
--   ceph_noout           hold Ceph's noout flag while the job runs. Default on.
--   ceph_present         whether the cluster answered its Ceph status; NULL
--                        until the job first looks, then remembered for the
--                        job's life. A cluster without Ceph is looked at once.
--   ceph_osds_baseline   OSDs up when the job last started a node (0: none).
--   ceph_noout_state     '' not looked yet, 'held' set by this job and to be
--                        released, 'already_set' set before the job looked
--                        (the operator's, never cleared), 'not_held' could
--                        not be set, 'released' cleared at the job's end.
--   pause_reason         why the engine paused the job itself; '' when an
--                        operator paused it or it is not paused.
-- Per node, ceph_wait_reason and ceph_wait_started_at say what the node is
-- waiting for and since when, for the 30-minute limit that pauses the job.
--
-- A job that is pending or running across this upgrade gets the defaults and
-- starts waiting for Ceph on its next tick, which is intended: a node in its
-- health check is not called healthy, and the next node is not started, until
-- Ceph is clean, and the next node it starts sets noout. Its first wait has no
-- OSD baseline, so it waits for the PGs alone.
ALTER TABLE rolling_update_jobs
    ADD COLUMN IF NOT EXISTS ceph_wait BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS ceph_noout BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS ceph_present BOOLEAN,
    ADD COLUMN IF NOT EXISTS ceph_osds_baseline INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS ceph_noout_state TEXT NOT NULL DEFAULT ''
        CHECK (ceph_noout_state IN ('', 'held', 'already_set', 'not_held', 'released')),
    ADD COLUMN IF NOT EXISTS pause_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE rolling_update_nodes
    ADD COLUMN IF NOT EXISTS ceph_wait_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ceph_wait_started_at TIMESTAMPTZ;
