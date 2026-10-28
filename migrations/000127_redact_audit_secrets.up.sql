-- Automatic in-place upgrade — no manual steps, no operator action required.
-- A data back-fill (trigger #7), resolved here: it rewrites only values that
-- must not be kept, in place, and needs nothing from the operator.
--
-- Until this release the guest config writes (PUT .../vms/:vm_id/config and
-- .../containers/:ct_id/config) recorded each value they sent Proxmox in their
-- audit row's details.fields, a cloud-init password included, in a row every
-- Viewer can read; a URL sent with a secret in it (a download-url source, with
-- a user name and password, or a pre-signed or token query) kept it in its row
-- and in its task's description, and from the description in the alerts naming
-- a failed task, the dead-letter copies of their notifications and the reports
-- listing it; and a notification that failed kept its channel's URL — a chat
-- webhook's path, a bot's token — in the dead-letter row's last_error. The rows
-- written from this release on keep none of it (handlers.redactAuditSecrets,
-- handlers.TrackTask, the notification senders); this does the same to the rows
-- already kept:
--
--   1. In every audit row whose details.fields is an object, the value of each
--      parameter that carries a secret becomes "(set)", or "(cleared)" where it
--      was sent empty. The key stays. The names are pvefield.Secret's, as at
--      this migration.
--   2. In every string of every audit row's details, task description, alert's
--      resource name and message, dead-letter payload and report run (its data,
--      HTML and CSV), each URL's userinfo, query and fragment become "(set)":
--      redact.URLs's three patterns, in its order (redact_urls below). JSON is
--      walked value by value (redact_urls_json), so a string is rewritten
--      exactly as the Go side rewrites it and an object key never is.
--   3. In a dead-letter row's last_error, each URL keeps its scheme and host
--      alone, the rest "/(set)" and its userinfo "(set)@": redact.TargetURL,
--      over the text of the error (redact_target_urls).
--
-- Nothing else in a row changes and no row is deleted. Each statement rewrites
-- only a row its rewrite changes, and a rewritten value rewrites to itself, so
-- a second run changes nothing; interrupted, the migration is one transaction
-- and leaves nothing behind. The helper functions are made in pg_temp and
-- dropped at the end, so not even the session keeps them. What it removes is
-- kept nowhere, so the down migration cannot restore it and does nothing.
--
-- No rule or row-level security policy guards any of these tables, and only
-- task_history has a trigger, which moves updated_at on the rows rewritten
-- (checked in pg_trigger, pg_rules and pg_class.relrowsecurity at 000126).
-- Copies outside the database are beyond it: a syslog destination or channel
-- that received them, an e-mailed report, the application's own logs, the
-- nodes' task logs (/var/log/pve/tasks) and database backups taken before the
-- upgrade still hold the values.
--
-- Cost: one sequential read of each table and a rewrite of the matching rows.
-- Over 1,000,000 audit rows, 1,000,000 tasks, 100,000 alerts, 10,000 dead
-- letters and 1,000 report runs, 30,100 of them to rewrite, it took 7.3 s; with
-- nothing left to rewrite, 5.1 s.

CREATE OR REPLACE FUNCTION pg_temp.redact_urls(t text) RETURNS text LANGUAGE sql IMMUTABLE AS $f$
  SELECT regexp_replace(regexp_replace(regexp_replace(t,
           $re$([A-Za-z][A-Za-z0-9+.-]*://)[^/?#]*@$re$, '\1(set)@', 'g'),
           $re$([A-Za-z][A-Za-z0-9+.-]*://[^\t\n\f\r ?#"'<>`]*)\?[^\t\n\f\r #"'<>`]+$re$, '\1?(set)', 'g'),
           $re$([A-Za-z][A-Za-z0-9+.-]*://[^\t\n\f\r #"'<>`]*)#[^\t\n\f\r "'<>`]+$re$, '\1#(set)', 'g')
$f$;

-- A JSON value with redact_urls applied to each string in it that holds a
-- "://", and nothing else rebuilt: an object or array keeps every member that
-- does not change, and an object's keys are never rewritten.
CREATE OR REPLACE FUNCTION pg_temp.redact_urls_json(j jsonb) RETURNS jsonb LANGUAGE plpgsql IMMUTABLE AS $f$
DECLARE
  k text;
  v jsonb;
  r jsonb;
  o jsonb := j;
  i int := 0;
BEGIN
  CASE jsonb_typeof(j)
    WHEN 'string' THEN
      IF position('://' IN j #>> '{}') > 0 THEN
        o := to_jsonb(pg_temp.redact_urls(j #>> '{}'));
      END IF;
    WHEN 'object' THEN
      FOR k, v IN SELECT e.key, e.value FROM jsonb_each(j) AS e LOOP
        r := pg_temp.redact_urls_json(v);
        IF r <> v THEN
          o := jsonb_set(o, ARRAY[k], r);
        END IF;
      END LOOP;
    WHEN 'array' THEN
      FOR v IN SELECT e.value FROM jsonb_array_elements(j) AS e LOOP
        r := pg_temp.redact_urls_json(v);
        IF r <> v THEN
          o := jsonb_set(o, ARRAY[i::text], r);
        END IF;
        i := i + 1;
      END LOOP;
    ELSE
  END CASE;
  RETURN o;
END
$f$;

-- An error's text with each URL in it as redact.TargetURL keeps a URL: the
-- error quotes it (Go's %q, a quote inside escaped), so it ends at the closing
-- quote.
CREATE OR REPLACE FUNCTION pg_temp.redact_target_urls(t text) RETURNS text LANGUAGE sql IMMUTABLE AS $f$
  SELECT regexp_replace(regexp_replace(t,
           $re$([A-Za-z][A-Za-z0-9+.-]*://)(?:[^/?#"\\]|\\.)*@$re$, '\1(set)@', 'g'),
           $re$([A-Za-z][A-Za-z0-9+.-]*://(?:[^/?#"\\]|\\.)*)(?:/(?:[^"\\]|\\.)+|[?#](?:[^"\\]|\\.)*)$re$, '\1/(set)', 'g')
$f$;

UPDATE audit_log
SET details = jsonb_set(details, '{fields}', (
        SELECT jsonb_object_agg(f.key, CASE
                   WHEN f.key <> ALL (ARRAY['password', 'cipassword', 'confirmation-password', 'token', 'secret', 'key',
                                            'keyring', 'encryption-key', 'master-pubkey', 'eab-hmac-key', 'data',
                                            'client-key', 'tfa', 'totp', 'env']) THEN f.value
                   WHEN f.value IN ('""', 'null') THEN '"(cleared)"'
                   ELSE '"(set)"' END)
        FROM jsonb_each(details->'fields') AS f))
WHERE jsonb_typeof(details->'fields') = 'object'
  AND EXISTS (SELECT 1 FROM jsonb_each(details->'fields') AS f
              WHERE f.key = ANY (ARRAY['password', 'cipassword', 'confirmation-password', 'token', 'secret', 'key',
                                       'keyring', 'encryption-key', 'master-pubkey', 'eab-hmac-key', 'data',
                                       'client-key', 'tfa', 'totp', 'env'])
                AND f.value NOT IN ('"(set)"', '"(cleared)"'));

-- The LIKE is the cheap pass over every row, the regular expression the pass
-- over those holding a "://": a URL whose part could carry a secret has an "@",
-- "?" or "#" after its "://", in its own JSON string (an escape kept whole) for
-- a column of JSON. The rewrite runs only on what both pass, and is kept only
-- where it changes something.
UPDATE audit_log AS a
SET details = r.details
FROM (SELECT id, pg_temp.redact_urls_json(details) AS details FROM audit_log
      WHERE details::text LIKE '%://%' AND details::text ~ $re$://(?:[^"\\]|\\.)*[@?#]$re$
      OFFSET 0) AS r -- once a row: the fence keeps the planner from inlining it into each use
WHERE a.id = r.id AND a.details <> r.details;

UPDATE task_history
SET description = pg_temp.redact_urls(description)
WHERE description LIKE '%://%' AND description ~ '://.*[@?#]' AND pg_temp.redact_urls(description) <> description;

UPDATE alert_history
SET resource_name = pg_temp.redact_urls(resource_name), message = pg_temp.redact_urls(message)
WHERE (resource_name LIKE '%://%' OR message LIKE '%://%')
  AND (resource_name ~ '://.*[@?#]' OR message ~ '://.*[@?#]')
  AND (pg_temp.redact_urls(resource_name) <> resource_name OR pg_temp.redact_urls(message) <> message);

UPDATE notification_dlq
SET payload = pg_temp.redact_urls_json(payload), last_error = pg_temp.redact_target_urls(last_error)
WHERE (payload::text LIKE '%://%' OR last_error LIKE '%://%')
  AND (pg_temp.redact_urls_json(payload) <> payload OR pg_temp.redact_target_urls(last_error) <> last_error);

UPDATE report_runs
SET report_data = pg_temp.redact_urls_json(report_data), report_html = pg_temp.redact_urls(report_html),
    report_csv = pg_temp.redact_urls(report_csv)
WHERE (report_data::text LIKE '%://%' OR report_html LIKE '%://%' OR report_csv LIKE '%://%')
  AND (pg_temp.redact_urls_json(report_data) IS DISTINCT FROM report_data
       OR pg_temp.redact_urls(report_html) IS DISTINCT FROM report_html
       OR pg_temp.redact_urls(report_csv) IS DISTINCT FROM report_csv);

DROP FUNCTION pg_temp.redact_urls_json(jsonb), pg_temp.redact_target_urls(text), pg_temp.redact_urls(text);
