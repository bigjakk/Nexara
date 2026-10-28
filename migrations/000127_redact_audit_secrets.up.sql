-- Automatic in-place upgrade — no manual steps, no operator action required.
-- A data back-fill (trigger #7), resolved here: it rewrites only values that
-- must not be kept, in place, and needs nothing from the operator.
--
-- Until this release the guest config writes (PUT .../vms/:vm_id/config and
-- .../containers/:ct_id/config) recorded each value they sent Proxmox in their
-- audit row's details.fields, a cloud-init password included, in a row every
-- Viewer can read, and a URL sent with credentials in it (a download-url source)
-- kept them in its row and in its task's description. The rows written from this
-- release on keep neither (handlers.redactAuditSecrets); this does the same to
-- the rows already kept:
--
--   1. In every audit row whose details.fields is an object, the value of each
--      parameter that carries a secret becomes "(set)", or "(cleared)" where it
--      was sent empty. The key stays. The names are pvefield.Secret's, as at
--      this migration.
--   2. In every audit row's details and every task_history description, the
--      userinfo of a URL (from its "://" to the last "@" before a "/", "?" or
--      "#") becomes "(set)" — the pattern of handlers.urlCredentials, written
--      over the JSON text, where a string ends at an unescaped quote.
--
-- Nothing else in a row changes and no row is deleted. Every statement matches
-- only a value not yet redacted, so a second run changes nothing; interrupted,
-- the migration is one transaction and leaves nothing behind. What it removes is
-- kept nowhere, so the down migration cannot restore it and does nothing.
--
-- No trigger, rule or row-level security policy guards audit_log or task_history
-- against UPDATE (checked in pg_trigger, pg_rules and pg_class.relrowsecurity
-- at 000126), so there is nothing to lift for the rewrite. Copies outside the
-- database are beyond it: a syslog destination that received the rows, and
-- database backups taken before the upgrade, still hold the values.
--
-- Cost: one sequential read of each table and a rewrite of the matching rows.
-- Over 1,000,000 audit rows and 1,000,000 tasks, 18,000 of them to rewrite, it
-- took 3.7 s; with nothing left to rewrite, 2.6 s.

UPDATE audit_log
SET details = jsonb_set(details, '{fields}', (
        SELECT jsonb_object_agg(f.key, CASE
                   WHEN f.key <> ALL (ARRAY['password', 'cipassword', 'confirmation-password', 'token', 'secret', 'key',
                                            'keyring', 'encryption-key', 'master-pubkey', 'eab-hmac-key', 'data']) THEN f.value
                   WHEN f.value IN ('""', 'null') THEN '"(cleared)"'
                   ELSE '"(set)"' END)
        FROM jsonb_each(details->'fields') AS f))
WHERE jsonb_typeof(details->'fields') = 'object'
  AND EXISTS (SELECT 1 FROM jsonb_each(details->'fields') AS f
              WHERE f.key = ANY (ARRAY['password', 'cipassword', 'confirmation-password', 'token', 'secret', 'key',
                                       'keyring', 'encryption-key', 'master-pubkey', 'eab-hmac-key', 'data'])
                AND f.value NOT IN ('"(set)"', '"(cleared)"'));

UPDATE audit_log
SET details = regexp_replace(details::text, '([A-Za-z][A-Za-z0-9+.-]*://)(?:[^/?#"\\]|\\.)*@', '\1(set)@', 'g')::jsonb
WHERE details::text LIKE '%://%@%'
  AND details::text ~ '://(?!\(set\)@)(?:[^/?#"\\]|\\.)*@';

UPDATE task_history
SET description = regexp_replace(description, '([A-Za-z][A-Za-z0-9+.-]*://)[^/?#]*@', '\1(set)@', 'g')
WHERE description LIKE '%://%@%'
  AND description ~ '://(?!\(set\)@)[^/?#]*@';
