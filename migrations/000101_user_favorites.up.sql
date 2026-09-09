-- 000101_user_favorites.up.sql
-- Per-user starred clusters, nodes and guests, surfaced at the top of the
-- sidebar so the handful of resources someone actually works with are one
-- click away instead of several levels down an expanding tree.
--
-- Additive (a new table) — safe for in-place upgrade, no operator action
-- required.
--
-- BEHAVIOURAL CONSEQUENCE of keying on the Proxmox identity rather than on a
-- surrogate, the same one 000068 called out for folder memberships: destroying
-- a starred guest and later creating one that reuses its VMID makes the star
-- reappear, now pointing at the new guest. The inventory "slot" keeps its
-- place. Intentional, and the price of surviving the collector's row churn —
-- but more surprising for a star than for a folder, because a star is a
-- shortcut its owner clicks without re-reading the name.

CREATE TABLE IF NOT EXISTS user_favorites (
    user_id       UUID NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    -- Every favorite belongs to exactly one cluster, including a cluster
    -- favorite (which points at itself). That gives the table a single column
    -- to hang its lifetime on and a single column to scope RBAC by.
    cluster_id    UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL,
    -- The stable Proxmox identity of the target, as text so one column can
    -- serve all three types: '' for a cluster (cluster_id already names it),
    -- the node name for a node, the VMID for a guest.
    --
    -- Deliberately NOT nodes.id or vms.id, and deliberately no FK to either.
    -- The collector prunes and re-inserts a vms row whenever Proxmox
    -- transiently stops listing a guest — most often during a live migration —
    -- and the re-insert draws a fresh gen_random_uuid(). A surrogate key with
    -- ON DELETE CASCADE would silently unstar guests as they migrate. This is
    -- the same trap 000068 had to unpick for folder memberships; see the
    -- ListFavorite* queries for the join back to the current row id.
    --
    -- Both refs are already unique per cluster in 000004:
    -- nodes UNIQUE (cluster_id, name), vms UNIQUE (cluster_id, vmid).
    resource_ref  TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, cluster_id, resource_type, resource_ref),

    CONSTRAINT user_favorites_type_check
        CHECK (resource_type IN ('cluster', 'node', 'vm')),

    -- A cluster favorite must carry an empty ref and nothing else may. Without
    -- this, 'cluster' + any junk ref would be a distinct primary key, so the
    -- same cluster could be starred an unbounded number of times and the
    -- unstar (which sends '') would clear none of them.
    CONSTRAINT user_favorites_cluster_ref_check
        CHECK ((resource_type = 'cluster') = (resource_ref = '')),

    -- A guest is referenced by VMID, so anything else is a bug upstream rather
    -- than a row worth keeping. Guards the join in ListFavoriteVMs, which casts
    -- this column to int.
    --
    -- Bounded to 9 digits, not just "digits": '99999999999' is all digits and
    -- overflows int. One such row would make ListFavoriteVMs raise "value out
    -- of range" for that user forever, and the row could not be removed through
    -- the API either, since RemoveFavorite validates the same way the writer
    -- does. Same bound, for the same reason, as queries/audit_log.sql's
    -- '^[0-9]{1,9}$' guard ahead of its own ::int.
    CONSTRAINT user_favorites_vm_ref_check
        CHECK (resource_type <> 'vm' OR resource_ref ~ '^[0-9]{1,9}$')
);

COMMENT ON TABLE  user_favorites               IS 'Per-user starred resources, surfaced above the sidebar tree';
COMMENT ON COLUMN user_favorites.resource_ref  IS 'Stable Proxmox identity: empty for a cluster, node name for a node, VMID for a guest';

-- The listing reads one user's whole set, which the primary key's leading
-- user_id column already serves; no further index is needed.
