// Package journal records every command the learner ran, with exit code,
// working directory, and timing, plus the environment and cwd snapshots.
//
// Layer L2. May import internal/store and below.
//
// The journal is written twice on purpose: to SQLite for the game, and to a TSV
// inside the sandbox so check scripts can read it and so it survives a crash.
//
// Treat journal contents as secret material. People type passwords into shells.
// It is never transmitted, and `bug-report` ships commands without output.
// Never parse ~/.bash_history for anything; the journal is the source of truth.
package journal
