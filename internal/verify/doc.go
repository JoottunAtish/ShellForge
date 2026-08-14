// Package verify is the check registry and the verification engine.
//
// Layer L3. May import internal/runtime and below. It does not import
// internal/journal at all: it declares its own JournalReader interface
// instead, satisfied by internal/journal from outside this package. That
// indirection is what lets a check ask about command history without this
// package ever knowing internal/journal's concrete type, and a
// source-parsing test holds the line so the import cannot come back quietly.
//
// Three invariants:
//
// Checks are read-only. A check that mutates sandbox state is a bug, and the
// purity test in CI catches it by hashing the filesystem across a double run.
//
// Checks never short-circuit. Every check runs so the objective checklist is
// fully populated, even though only the first failing required check has its
// on_fail displayed.
//
// That invariant is about objectives, and a composition branch is not one. A
// branch inside an any_of, an all_of or a not has no id, never appears in
// LevelResult.Objectives, and has nothing reported about it either way, so a
// composite stops evaluating branches once its own answer is decided. The
// checklist is still complete: the composite is the objective, and it is always
// reported. Evaluating the remaining branches of a decided any_of would cost
// sandbox round trips and tell the learner nothing. compose.go states the same
// thing at the point of use and a test pins it, so the optimisation is not
// mistaken for a violation of the rule above and reverted.
//
// Checks run through Session.Exec in a fresh non-interactive shell, never in the
// learner's shell. Shell-local state such as environment variables and the
// working directory comes from the snapshot files written by PROMPT_COMMAND, not
// from the check's own process. Getting that wrong makes level env-01
// unsolvable.
//
// A fourth invariant: verification never trusts the journal. A learner can emit a
// byte-identical OSC 133 marker by hand, so the journal's recorded exit code and
// cwd are learner-influenced and prove nothing about what ran. Checks decide pass
// or fail from real state read through Session.Exec, never by opening the journal.
// The PROMPT_COMMAND snapshot files above are a different source and remain
// legitimate: they are shell-local state a check reads directly, not the
// journal's account of history. See internal/journal for the full statement.
// This package never imports internal/journal at all, for the reason given
// above, and a test holds that line.
package verify
