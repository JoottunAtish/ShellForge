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
//
// Journal contents are learner-influenced and must never be evidence for
// scoring. Every field, the command, the exit code, the cwd, comes from a marker
// the learner's own shell emitted, and a learner can print a byte-identical
// marker by hand: printf '\e]133;D;0\a' forges a clean exit for a command that
// failed or never ran, and the parser cannot tell it from the real thing. So
// verification never reads the journal to decide pass or fail; a check confirms
// actual sandbox state through Session.Exec. The journal still drives the command
// log, the replay, and the efficiency bonus, and that bonus is deliberately
// gameable: par is a nicety, not a gate, and forging a lower command count cheats
// only the forger. The environment and cwd snapshot files that PROMPT_COMMAND
// writes are a separate source and stay legitimate check inputs; this rule is
// about trusting the journal's account of what happened, not about shell-local
// state a check reads directly.
package journal
