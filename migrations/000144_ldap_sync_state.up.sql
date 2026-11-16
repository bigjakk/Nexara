-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Two additive changes for the LDAP sync, both with nothing to backfill:
--
-- users.deactivated_by_directory says an account is inactive because an LDAP sync did
-- not find it in the directory, which is the one case a later sync may undo. Every
-- account inactive before this migration reads false, "deactivated by hand", the safe
-- reading: a sync never re-enables it. An administrator's change of is_active sets it
-- back to false (UpdateUserProfile).
--
-- users.ldap_login_name is the name a directory sign-in last found the account's entry
-- with (a uid, a sAMAccountName, an address), so that the sync searches the user
-- filter with what the person signs in with and not only with the stored address. It
-- starts empty, and an account with none is searched by its address, as before.
--
-- ldap_configs.sync_lease_token / sync_lease_expires_at keep two syncs of one config
-- apart, the scheduled one and Sync Now on any replica alike: a sync claims the lease,
-- and releases it when done. A lease left by a process that died runs out at its
-- expiry. Both start NULL: no sync is running.
ALTER TABLE users ADD COLUMN IF NOT EXISTS deactivated_by_directory BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS ldap_login_name TEXT NOT NULL DEFAULT '';
ALTER TABLE ldap_configs ADD COLUMN IF NOT EXISTS sync_lease_token UUID;
ALTER TABLE ldap_configs ADD COLUMN IF NOT EXISTS sync_lease_expires_at TIMESTAMPTZ;
