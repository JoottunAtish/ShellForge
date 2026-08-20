-- 002_progression.sql
-- Progression tables for issue #120: profile, pack, level_state, attempt,
-- concept_mastery, achievement. Mirrors ARCHITECTURE 4.11 with two
-- deliberate, documented deviations (see the commit message):
--   1. level_state.level_version is added.
--   2. profile.name is UNIQUE so EnsureProfile stays a single row.
-- The events table from 001_init.sql is the command journal and is left
-- untouched here. The singular "event" table named in 4.11 is deliberately
-- NOT created: events from 001 is the journal, and issue #87 owns that
-- contradiction.

CREATE TABLE profile (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    created_at INTEGER
);

CREATE TABLE pack (
    id           TEXT PRIMARY KEY,
    version      TEXT,
    source       TEXT,
    sha256       TEXT,
    installed_at INTEGER
);

CREATE TABLE level_state (
    profile_id      INTEGER NOT NULL,
    level_id        TEXT NOT NULL,
    pack_id         TEXT,
    level_version   INTEGER NOT NULL DEFAULT 0,
    status          TEXT CHECK(status IN ('locked','available','in_progress','passed','skipped')),
    best_score      INTEGER NOT NULL DEFAULT 0,
    attempts        INTEGER NOT NULL DEFAULT 0,
    hints_used      INTEGER NOT NULL DEFAULT 0,
    commands_used   INTEGER NOT NULL DEFAULT 0,
    first_passed_at INTEGER,
    last_attempt_at INTEGER,
    total_seconds   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (profile_id, level_id)
);

CREATE TABLE attempt (
    id            INTEGER PRIMARY KEY,
    profile_id    INTEGER NOT NULL,
    level_id      TEXT NOT NULL,
    started_at    INTEGER,
    ended_at      INTEGER,
    outcome       TEXT,
    score         INTEGER,
    hints_used    INTEGER,
    commands_used INTEGER
);

CREATE TABLE concept_mastery (
    profile_id    INTEGER NOT NULL,
    concept       TEXT NOT NULL,
    ease          REAL DEFAULT 2.5,
    interval_days REAL DEFAULT 0,
    reps          INTEGER DEFAULT 0,
    last_seen_at  INTEGER,
    due_at        INTEGER,
    PRIMARY KEY (profile_id, concept)
);

CREATE TABLE achievement (
    profile_id  INTEGER NOT NULL,
    key         TEXT NOT NULL,
    unlocked_at INTEGER,
    progress    REAL,
    PRIMARY KEY (profile_id, key)
);

CREATE INDEX idx_mastery_due ON concept_mastery(profile_id, due_at);
