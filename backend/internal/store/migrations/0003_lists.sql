-- Lists the user keeps by voice (skills/lists): "create a list called
-- packing", "add milk to the shopping list". Names and items are stored
-- normalized (lowercase). created_at is Unix milliseconds.
CREATE TABLE lists (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE list_items (
    id         INTEGER PRIMARY KEY,
    list_id    INTEGER NOT NULL REFERENCES lists (id) ON DELETE CASCADE,
    text       TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (list_id, text)
) STRICT;
