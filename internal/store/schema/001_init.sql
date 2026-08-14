-- 001_init.sql: the initial Shellforge schema.
-- Forward only. Never hand-edit this file once it has shipped; add 002_*.sql.

CREATE TABLE schema_version (
    version INTEGER NOT NULL
);

-- events is the command journal. One row per command the learner ran.
-- Every column here is learner-influenced and none of it is evidence for
-- scoring: see internal/journal/doc.go.
CREATE TABLE events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    seq          INTEGER NOT NULL,
    ts           REAL    NOT NULL,
    level_id     TEXT    NOT NULL,
    cwd          TEXT    NOT NULL,
    raw          TEXT    NOT NULL,
    exit_code    INTEGER NOT NULL,
    duration_ms  INTEGER NOT NULL,
    used_tab     INTEGER NOT NULL DEFAULT 0,
    used_history INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_events_level_seq ON events(level_id, seq);
