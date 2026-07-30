-- Automatic in-place upgrade — NOT a breaking change. No manual steps, no
-- operator action, nothing to back up or re-enter. This migration DELETES
-- duplicate shared-scope settings rows before tightening the uniqueness
-- constraint, and applies unattended on container startup like any other
-- migration. What is deleted, what is kept, and the recovery semantics are
-- documented inline below.
--
-- It matches CLAUDE.md's breaking-change pattern #5 ("adds a UNIQUE constraint
-- that may be violated by existing rows") on its face, but that pattern is only
-- unsafe when the operator has to resolve the violations. Here the migration
-- resolves them itself, deterministically, in the same transaction — so the
-- callout would be noise. Kept the procedure documented anyway; the risk that
-- warrants writing it down is real even when the fix is automatic.
--
-- ONE VISIBLE EFFECT ON UPGRADE ---------------------------------------------
-- On an install that accumulated duplicates, the effective value of an affected
-- setting changes: the app had been reading a stale row (see WHY below) and now
-- reads the newest one the admin saved. That is the point of the fix — a
-- corrected syslog host or logo URL finally takes effect — but it is a
-- behaviour change on upgrade and belongs in the release notes.
--
-- WHY ----------------------------------------------------------------------
-- 000001 declared the settings uniqueness as a plain `UNIQUE (key, scope,
-- scope_id)`. Postgres treats NULLs as distinct under a plain UNIQUE, so no
-- two rows with scope_id IS NULL ever conflict. Every shared-scope row is
-- keyed that way (settingScopeID in internal/api/handlers/settings.go leaves
-- scope_id NULL whenever the scope is not per-user), which means the
-- `ON CONFLICT (key, scope, scope_id) DO UPDATE` in queries/settings.sql could
-- never infer a match for them: each write APPENDED a new row instead of
-- updating the existing one. Verified against a live PG16 database — three
-- identical global-scope upserts of the same key produced three rows, while
-- the same test with a non-NULL scope_id correctly produced one.
--
-- The user-visible damage is that a stale value can win on read. GetSetting is
-- `LIMIT 1` with no deterministic tiebreak, so which duplicate it returns is
-- planner-dependent (typically the oldest heap tuple). An admin who corrected
-- a syslog destination could have loadSyslogConfig in internal/api/server.go
-- resurrect the old host on the next restart; GetBranding (ListGlobalSettings)
-- had the same nondeterminism for branding.logo_url / branding.favicon_url.
--
-- WHAT IS DELETED ----------------------------------------------------------
-- Only rows with scope_id IS NULL, and only where more than one row shares a
-- (key, scope) pair — i.e. exactly the duplicates the missing conflict target
-- allowed to accumulate. The newest row per (key, scope) is kept, which is the
-- value the administrator most recently saved. Rows with a non-NULL scope_id
-- (every per-user setting) were always covered by the constraint, can never
-- have duplicates, and are not touched.
--
-- Note the dedup partitions on (key, scope) rather than filtering to
-- scope = 'global'. Before the scope allow-list landed, a `cluster`-scoped
-- write also fell through to scope_id = NULL, so such rows can exist in older
-- installs and duplicated the same way.
--
-- BEHAVIORAL CHANGE --------------------------------------------------------
-- NULLS NOT DISTINCT (PG15+) makes scope_id IS NULL a single namespace per
-- (key, scope), so the existing ON CONFLICT inference starts matching and
-- shared-scope writes update in place. A second row for the same shared key
-- now raises a unique violation instead of being silently appended.
--
-- RECOVERY -----------------------------------------------------------------
-- The whole migration runs in one transaction (golang-migrate default). If it
-- fails or is interrupted it rolls back to the previous schema with the
-- duplicates intact, and re-running it re-attempts the dedup cleanly. The
-- delete is not reversible once committed: the down migration restores the old
-- constraint but cannot resurrect discarded duplicates (nor would they be
-- wanted — they are superseded values).

-- 1. Collapse each duplicated (key, scope) group down to its newest row.
--    Shared-scope rows are only ever inserted (the upsert's DO UPDATE branch
--    was unreachable for them), so updated_at/created_at track write order.
--    id is the final tiebreak purely to make the ordering total: two rows can
--    only share both timestamps if they were written in the same transaction,
--    where neither is meaningfully newer. GetSetting in queries/settings.sql
--    orders identically so a read resolves to the same row this keeps.
DELETE FROM settings
WHERE id IN (
    SELECT id
    FROM (
        SELECT id,
               row_number() OVER (
                   PARTITION BY key, scope
                   ORDER BY updated_at DESC, created_at DESC, id DESC
               ) AS rn
        FROM settings
        WHERE scope_id IS NULL
    ) ranked
    WHERE ranked.rn > 1
);

-- 2. Swap in the NULL-aware constraint now that no group can violate it. The
--    name is kept identical to the one 000001 generated implicitly, so a
--    freshly migrated database and an upgraded one end up schema-identical.
ALTER TABLE settings DROP CONSTRAINT IF EXISTS settings_key_scope_scope_id_key;
ALTER TABLE settings ADD  CONSTRAINT settings_key_scope_scope_id_key
    UNIQUE NULLS NOT DISTINCT (key, scope, scope_id);
