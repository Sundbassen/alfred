-- Timers (skills/clock, through clockstore).

-- name: AddTimer :one
INSERT INTO timers (kind, at, duration, message) VALUES (?, ?, ?, ?) RETURNING id;

-- name: DeleteTimer :exec
DELETE FROM timers WHERE id = ?;

-- name: PendingTimers :many
SELECT id, kind, at, duration, message FROM timers ORDER BY at, id;
