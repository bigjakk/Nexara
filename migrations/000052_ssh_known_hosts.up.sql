-- Pinned SSH host keys for Proxmox node connections (TOFU model).
-- Each row binds (cluster_id, host, port) to a public key. Connections in
-- internal/ssh verify against these rows; an unpinned host fails closed.
CREATE TABLE IF NOT EXISTS ssh_known_hosts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id   UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    host         TEXT NOT NULL,
    port         INTEGER NOT NULL DEFAULT 22,
    public_key   TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,
    pinned_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    pinned_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cluster_id, host, port)
);

CREATE INDEX IF NOT EXISTS idx_ssh_known_hosts_cluster
    ON ssh_known_hosts (cluster_id);
