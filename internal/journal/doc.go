// Package journal records every command the learner ran, with exit code,
// working directory, and timing, plus the environment and cwd snapshots.
//
// Layer L2. May import internal/store and below.
//
// The journal is written twice on purpose: to SQLite for the game, and to a TSV
// inside the sandbox so check scripts can read it and so it survives a crash.
//
// Entry.String and Entry.GoString redact Raw everywhere fmt can reach one of
// them: a bare Entry, a *Entry, a slice or map of either, a struct that
// embeds one in an EXPORTED field, and inside fmt.Errorf. There is one gap
// no Go code in this package can close: an Entry reached only through an
// UNEXPORTED field of some other type prints Raw in full under %v, %+v, and
// %#v, because reflect marks a value read through an unexported field as
// read-only and fmt will not call String or GoString on a read-only value.
// The rule this leaves for every caller, inside this package and out: never
// embed a journal.Entry or *journal.Entry as an unexported field of
// anything you might format with fmt. See Entry's own doc comment for the
// same rule stated where an author defining a new struct is most likely to
// read it.
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
// Entry has flat, typed fields and no attempt_id and no kind, and the
// events table this package writes to (internal/store/schema/001_init.sql)
// is flat and typed too, one column per Entry field. This conforms to
// docs/design/ARCHITECTURE.md section 4.7, "Command Journal & Snapshotter",
// which already specifies exactly this: "Stored in SQLite (`events`
// table)" holding a flat journal record with seq, ts, level, cwd, raw,
// exit, duration_ms, used_tab, used_history. Two column names differ from
// 4.7's wording (level_id for `level`, exit_code for `exit`, both needed
// because `exit` collides with a SQL-adjacent word and `level` alone reads
// ambiguously next to a future foreign key), and three of 4.7's fields,
// output_sha256, output_head, and output_bytes, are absent from Entry,
// deferred along with output capture rather than implemented against an
// encoding issue #51 never specified.
//
// What is genuinely drift is inside docs/design/ARCHITECTURE.md itself:
// section 4.11, "Progression, Scoring, Persistence", separately describes a
// singular event table with attempt_id, kind, and an opaque payload_json,
// for a later-stage persistence layer this ticket does not build. An opaque
// JSON payload could not satisfy "an appended Entry reads back with every
// field intact" without inventing that encoding, so it is not what ships
// here, and it should not be read as this package failing to match the
// design record: it matches the section the ticket names. Reconciling 4.7
// against 4.11 is tracked in issue #87, as a change to the design record,
// not to this package.
//
// # scope.Level, SetLevel, and the level boundary
//
// scope.Level answers only for the level and attempt SetLevel most recently
// named, bounded by the events.id SetLevel was given: Commands never infers
// a level from the newest row in the table, because the newest row can
// belong to a different level than the one currently under verification.
// See journal.go's SetLevel doc comment for the exact contract.
//
// scope.LastN and scope.Last deliberately keep their global, level-agnostic
// semantics: the most recent N commands, or the single most recent command,
// regardless of which level recorded them. This is a considered decision,
// not an oversight left over from fixing scope.Level. Their exposure to the
// same wrong-level window scope.Level had is far smaller and self-correcting:
// a learner who runs `check` before typing anything in a new level sees at
// most one stale command from the level they just left, and it ages out of
// scope the moment they type their own first command, rather than
// persisting for the rest of the attempt the way scope.Level's bug did.
// Scoping them to the level boundary as well is a reasonable future change,
// but it is not this ticket's fix and would need its own test pass over
// every level that uses "last" or "last_n:N" scope today.
//
// This package never opens its own connection to the database and never
// imports modernc.org/sqlite directly: internal/archtest/dependencies_test.go
// confines that driver to internal/store. Every query here goes through the
// *sql.DB returned by (*store.Store).DB(), which is standard library
// database/sql, so reaching SQLite this way is legal from layer 2. ts is
// stored as REAL unix seconds with microsecond precision; see Append's own
// doc comment for the exact round trip.
//
// # Scope and verify.JournalReader
//
// Commands takes scope.Scope, from internal/scope at layer 0. internal/verify
// declares verify.Scope and verify.ScopeKind as type ALIASES for that
// package's, so verify.Scope and scope.Scope are one type, not two that
// happen to match. *Journal therefore satisfies verify.JournalReader
// outright: no adapter, no field by field translation, and nothing that can
// drift.
//
// The vocabulary type lives below both packages because neither may import
// the other. internal/journal is layer 2 and internal/verify is layer 3, so
// naming a verify type in this package's production code would be the upward
// edge internal/archtest forbids, and internal/verify declines to import this
// package in the other direction by its own contract (see
// internal/verify/doc.go). A type both of them need has to sit underneath
// both, which is what internal/scope is for and all it is for.
//
// The compile-time proof of the interface being satisfied lives in
// verifycontract_test.go, and it has to be a _test.go file: writing
// verify.JournalReader anywhere requires importing internal/verify from the
// file that writes it, which a non-test file here may not do for the reason
// above. archtest's collectImports skips _test.go files, so the external
// journal_test package can name both sides and assert they meet. That file
// also asserts the aliases really are aliases and pins the three scope kind
// strings, which are the level YAML surface.
package journal
