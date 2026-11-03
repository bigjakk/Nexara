-- UPS command runs and variable sets, and the lists they are checked against (000134).

-- name: SetUpsDeviceControls :exec
-- The commands and writable variables the poller read for a UPS, and the
-- driver it read them under.
UPDATE ups_devices
SET commands = @commands, rw_vars = @rw_vars, commands_listed_at = @listed_at, commands_driver = @driver
WHERE id = @id;

-- name: InsertUpsCommand :one
INSERT INTO ups_commands (ups_id, user_id, kind, name, argument, previous)
VALUES (@ups_id, sqlc.narg(user_id), @kind, @name, @argument, sqlc.narg(previous))
RETURNING *;

-- name: UpdateUpsCommand :execrows
-- A run's progress. Only an unfinished run moves, so neither a late answer nor
-- the sweep below overwrites an outcome already recorded; an empty tracking id
-- keeps the one recorded.
UPDATE ups_commands
SET status = @status,
    tracking_id = COALESCE(NULLIF(@tracking_id::text, ''), tracking_id),
    result = @result,
    finished_at = CASE WHEN @status::text IN ('succeeded', 'failed', 'unconfirmed') THEN now() END
WHERE id = @id AND status IN ('sent', 'pending');

-- name: ListUpsCommands :many
-- One UPS's newest runs, with who ran each.
SELECT c.*,
       COALESCE(u.email, '')::text        AS user_email,
       COALESCE(u.display_name, '')::text AS user_display_name
FROM ups_commands c
LEFT JOIN users u ON u.id = c.user_id
WHERE c.ups_id = @ups_id
ORDER BY c.requested_at DESC, c.id DESC
LIMIT @row_limit;

-- name: MarkUnfinishedUpsCommandsUnconfirmed :execrows
-- Runs nothing watches any more: asked for before this process started, while
-- a previous one was watching them.
UPDATE ups_commands
SET status = 'unconfirmed',
    result = 'Nexara stopped before the driver answered',
    finished_at = now()
WHERE status IN ('sent', 'pending') AND requested_at < @before;

-- name: SetNutServerCommandsVerified :exec
-- upsd took a command from the stored account, or refused the account.
UPDATE nut_servers
SET commands_verified_at = CASE WHEN @verified::boolean THEN now() END
WHERE id = @id;
