-- name: MarkNodeOffline :exec
UPDATE nodes SET status = 'offline' WHERE id = $1 AND status != 'offline';

-- name: MarkNodeOnline :exec
UPDATE nodes SET status = 'online' WHERE id = $1 AND status = 'offline';
