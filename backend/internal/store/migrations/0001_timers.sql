-- Timers, alarms and reminders that ring on this speaker (skills/clock).
-- at is Unix milliseconds; duration is milliseconds (0 for a clock time).
CREATE TABLE timers (
    id       INTEGER PRIMARY KEY,
    kind     TEXT    NOT NULL,
    at       INTEGER NOT NULL,
    duration INTEGER NOT NULL,
    message  TEXT    NOT NULL
) STRICT;
