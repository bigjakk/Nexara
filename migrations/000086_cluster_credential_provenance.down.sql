-- Drop the credential-provenance columns.
--
-- Purely additive in the up direction, so this is a clean revert. What it does
-- lose is the record of which PVE users and ACL grants Nexara created: after
-- this runs, a re-applied 000086 sees every cluster as 'manual' and cluster
-- deletion will no longer offer to revoke anything. The credentials themselves
-- are untouched on the Proxmox side.

ALTER TABLE clusters
    DROP COLUMN IF EXISTS credential_source,
    DROP COLUMN IF EXISTS bootstrap_user_id,
    DROP COLUMN IF EXISTS bootstrap_token_name,
    DROP COLUMN IF EXISTS bootstrap_created_user,
    DROP COLUMN IF EXISTS bootstrap_created_acl,
    DROP COLUMN IF EXISTS bootstrap_created_at;
