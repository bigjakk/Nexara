-- Persist the legacy users.role value at session creation / rotation time.
--
-- Refresh now refuses to issue a new access token if the user's legacy role
-- (the users.role column — the "admin"/"user"/"viewer" string) has changed
-- since the session was issued. We compare the persisted user_role against
-- the user's current role inside the same transaction that rotates the
-- refresh token, so an admin can demote a logged-in user and force them
-- back to the login page on their next refresh.
--
-- Existing pre-upgrade sessions are backfilled in this migration with the
-- user's *current* role. The Refresh handler still treats an empty string
-- as "unknown — accept" as a defensive fallback for any row that somehow
-- survives without a value (e.g. a session created in the brief window
-- between this migration and a rolling Go-binary restart on a
-- multi-replica deploy). With the backfill in place that fallback should
-- never fire for sessions issued by either the pre- or post-migration
-- binary in normal operation.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS user_role TEXT NOT NULL DEFAULT '';

UPDATE sessions
   SET user_role = u.role
  FROM users u
 WHERE sessions.user_id = u.id
   AND sessions.user_role = '';
