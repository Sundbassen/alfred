-- Lists (skills/lists, through listsstore).

-- name: CreateList :execrows
INSERT INTO lists (name, created_at) VALUES (?, ?) ON CONFLICT (name) DO NOTHING;

-- name: DeleteList :execrows
DELETE FROM lists WHERE name = ?;

-- name: ListID :one
SELECT id FROM lists WHERE name = ?;

-- name: Lists :many
SELECT l.name, count(i.id) AS items
FROM lists l LEFT JOIN list_items i ON i.list_id = l.id
GROUP BY l.id
ORDER BY l.created_at, l.id;

-- name: AddItem :execrows
INSERT INTO list_items (list_id, text, created_at) VALUES (?, ?, ?) ON CONFLICT (list_id, text) DO NOTHING;

-- name: RemoveItem :execrows
DELETE FROM list_items WHERE list_id = ? AND text = ?;

-- name: Items :many
SELECT text FROM list_items WHERE list_id = ? ORDER BY created_at, id;

-- name: ClearList :execrows
DELETE FROM list_items WHERE list_id = ?;
