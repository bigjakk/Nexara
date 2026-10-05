-- 000106_user_auth_epoch.down.sql
--
-- Drops the generation counter. Nothing else depends on it in the schema: every
-- session keeps its hash and its revoked flag, so each live session stays signed
-- in. What goes is only the guarantee that a sign-in whose credential was replaced
-- while it ran cannot mint a session, and a release that still has the
-- unconditional session insert needs nothing from this column.
ALTER TABLE users DROP COLUMN IF EXISTS auth_epoch;
