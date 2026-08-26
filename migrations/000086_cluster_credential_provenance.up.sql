-- Automatic in-place upgrade — no manual steps, no operator action required.
--
-- Records WHERE a cluster's API credential came from, so that deleting the
-- cluster can offer to revoke the credential on the Proxmox side — and can
-- refuse to touch anything Nexara did not create.
--
-- Every column is ADD COLUMN IF NOT EXISTS with a DEFAULT, so PostgreSQL fills
-- existing rows itself. Existing clusters land on 'manual', which is accurate:
-- their token was pasted in by an operator.
--
-- bootstrap_created_user / bootstrap_created_acl are the load-bearing pair.
-- Onboarding is forward-idempotent and will happily adopt a user or an ACL
-- grant that already existed, so "Nexara uses this user" and "Nexara created
-- this user" are different facts. Revocation may only act on the second.

ALTER TABLE clusters
    -- 'manual'    — operator pasted an existing API token (every pre-upgrade row)
    -- 'bootstrap' — Nexara minted the token itself during onboarding
    ADD COLUMN IF NOT EXISTS credential_source      TEXT        NOT NULL DEFAULT 'manual',

    -- The PVE user that owns the token, e.g. 'nexara@pve'. Empty for 'manual':
    -- token_id already carries the owner there, and copying it would invite
    -- revocation logic to read a field that was never verified.
    ADD COLUMN IF NOT EXISTS bootstrap_user_id      TEXT        NOT NULL DEFAULT '',

    -- The bare token name, e.g. 'nexara' in 'nexara@pve!nexara'.
    ADD COLUMN IF NOT EXISTS bootstrap_token_name   TEXT        NOT NULL DEFAULT '',

    -- true only when THIS onboarding run created the object. An adopted
    -- pre-existing user or ACL leaves these false and is never revoked.
    ADD COLUMN IF NOT EXISTS bootstrap_created_user BOOLEAN     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS bootstrap_created_acl  BOOLEAN     NOT NULL DEFAULT false,

    -- Nullable rather than NOT NULL DEFAULT now(): NULL is the honest value for
    -- a cluster that was never bootstrapped, and stamping every existing row
    -- with the upgrade time would invent a provenance that never happened.
    ADD COLUMN IF NOT EXISTS bootstrap_created_at   TIMESTAMPTZ;
