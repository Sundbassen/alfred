-- Timer ids are never reused (AUTOINCREMENT): a snoozed or extended timer
-- is saved again as a new row, and the old row's id may still be in use in
-- memory while it rings (skills/clock). SQLite can't add AUTOINCREMENT to a
-- table, so the table is rebuilt.
CREATE TABLE timers_new (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    kind     TEXT    NOT NULL,
    at       INTEGER NOT NULL,
    duration INTEGER NOT NULL,
    message  TEXT    NOT NULL
) STRICT;

INSERT INTO timers_new (id, kind, at, duration, message)
SELECT id, kind, at, duration, message FROM timers;

DROP TABLE timers;

ALTER TABLE timers_new RENAME TO timers;
