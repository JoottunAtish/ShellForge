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
//
// # The events table and the SQLite driver
//
// Entry has flat, typed fields and no attempt_id and no kind, so the events
// table this package writes to (internal/store/schema/001_init.sql) is flat
// and typed too, one column per Entry field, rather than the singular
// event table with an opaque payload_json that
// docs/design/ARCHITECTURE.md section 4.11 describes. An opaque JSON
// payload cannot satisfy "an appended Entry reads back with every field
// intact" without inventing an encoding issue #51 never specified, so the
// flat shape is what ships; the difference from 4.11 is recorded as drift,
// not silently reconciled.
//
// This package never opens its own connection to the database and never
// imports modernc.org/sqlite directly: internal/archtest/dependencies_test.go
// confines that driver to internal/store. Every query here goes through the
// *sql.DB returned by (*store.Store).DB(), which is standard library
// database/sql, so reaching SQLite this way is legal from layer 2. ts is
// stored as REAL unix seconds with microsecond precision; see Append's own
// doc comment for the exact round trip.
//
// # ScopeKind, Scope, and verify.JournalReader
//
// This package declares its own ScopeKind and Scope, mirroring
// verify.ScopeKind and verify.Scope field for field and value for value,
// rather than naming verify's types directly. internal/journal is layer 2
// and internal/verify is layer 3; a layer 2 package importing layer 3 in
// production code is exactly the upward edge internal/archtest's layer
// rule forbids, and CLAUDE.md's dependency rule outranks a ticket's fixed
// method signature when the two disagree.
//
// The honest statement: *Journal does not itself satisfy
// verify.JournalReader. A three line adapter in
// verifycontract_test.go does, because that file is in the external
// journal_test package and archtest never parses _test.go files at all, so
// an upward import there widens nothing. The acceptance criterion "Commands
// satisfies verify.JournalReader ... without internal/verify importing this
// package" is met in substance (the method set is pinned at compile time by
// the adapter, a reflect-based drift test in the same file fails loudly if
// verify.Scope's shape or values ever change without a matching update
// here, and internal/verify still imports nothing from this package) and
// not in letter (the parameter type Commands actually takes is
// journal.Scope, not verify.Scope). The durable fix, moving Scope and
// ScopeKind into a small layer 0 package that both internal/journal and
// internal/verify alias, needs its own ticket: it edits an exported type in
// internal/verify, which is outside this ticket's authorization.
package journal
