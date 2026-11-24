-- Queries for clockstore; sqlc generates clockdb from these and the shared
-- schema in internal/store/migrations (make sqlc).

-- name: AddTimer :one
INSERT INTO timers (kind, at, duration, message) VALUES (?, ?, ?, ?) RETURNING id;

-- name: DeleteTimer :exec
DELETE FROM timers WHERE id = ?;

-- name: PendingTimers :many
SELECT id, kind, at, duration, message FROM timers ORDER BY at, id;
