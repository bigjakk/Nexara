-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Adds a column with a DEFAULT to two tables and sets it on the rows that exist
-- when this runs (review trigger 7, a backfill), in the same transaction.
--
-- v1.17.2 let a notification channel's target and an OIDC issuer resolve to
-- carrier-grade NAT space (100.64.0.0/10, the range Tailscale uses). v1.18.0
-- counts that range as private, which needs the operator's confirmation
-- (allow_private_address, 000142 and 000146). Without this, every channel and
-- OIDC configuration made before the upgrade at such an address would stop
-- sending or signing in until someone saved it again with the confirmation.
--
-- cgnat_grandfathered keeps them working and reaches nothing v1.17.2 did not: a
-- row that has it may resolve to 100.64.0.0/10 without the confirmation, and to
-- no other private address, at every check (notifications.Allowance). v1.17.2
-- refused the other private ranges at every send and dial (an OIDC issuer at save
-- as well), and so does this.
-- It is set on every row that exists now, which on an upgrade from v1.17.2 is
-- every row made under that rule, and on none made later. The handlers drop it
-- the first time a channel's settings or type change, or a configuration's
-- issuer does; from then on the confirmation decides, as for a new one.
--
-- Interrupted, the transaction rolls back, but golang-migrate has already marked
-- 157 dirty and every start refuses to boot until that is cleared:
-- `docker compose run --rm nexara migrate force 156`, then start again
-- (docs/installation.md, "Recovering from a failed upgrade").
ALTER TABLE notification_channels
    ADD COLUMN IF NOT EXISTS cgnat_grandfathered BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE oidc_configs
    ADD COLUMN IF NOT EXISTS cgnat_grandfathered BOOLEAN NOT NULL DEFAULT false;
UPDATE notification_channels SET cgnat_grandfathered = true;
UPDATE oidc_configs SET cgnat_grandfathered = true;
