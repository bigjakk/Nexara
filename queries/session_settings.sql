-- ApplySessionTimeoutDefaults sets the app pool's backstops on a new connection
-- (internal/db/pool_backstop.go), each only where nothing else has set it: a
-- value from DATABASE_URL or PGOPTIONS (source 'client'), ALTER ROLE or ALTER
-- DATABASE, or postgresql.conf is left as it is.
-- name: ApplySessionTimeoutDefaults :exec
SELECT set_config(s.name,
       CASE s.name WHEN 'lock_timeout' THEN sqlc.arg('lock_timeout')::text
                   ELSE sqlc.arg('idle_in_transaction_session_timeout')::text END,
       false)
FROM pg_catalog.pg_settings s
WHERE s.name IN ('lock_timeout', 'idle_in_transaction_session_timeout')
  AND s.source = 'default';
