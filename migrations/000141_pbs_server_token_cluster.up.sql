-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Adds a column with a DEFAULT and fills it from each server's attached cluster
-- in the same transaction (review trigger 7, a derived backfill).
--
-- token_cluster_api_url is the API URL a PBS server's attached cluster had when
-- the server's token secret was last entered. Creating a pbs storage with the
-- server's own token (POST /clusters/:id/storage with pbs_server_id) hands that
-- token to Proxmox through the cluster's API URL, and moving that URL takes only
-- a new secret for the cluster's own token, which can be made up. So the token
-- is handed only to the attached cluster, and only while its URL is still this.
-- '' means no such pairing: a standalone server, or one moved to another cluster
-- since its token was entered. Entering the token secret again renews it.
--
-- The backfill pairs every attached server with its cluster's URL as it stands
-- at the upgrade, so reuse works on existing installs without re-entering any
-- token. Interrupted, the transaction rolls back and the next start reapplies it.
ALTER TABLE pbs_servers ADD COLUMN IF NOT EXISTS token_cluster_api_url TEXT NOT NULL DEFAULT '';

UPDATE pbs_servers p
SET token_cluster_api_url = c.api_url
FROM clusters c
WHERE p.cluster_id = c.id
  AND p.token_cluster_api_url = '';
