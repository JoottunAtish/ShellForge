// Package verify is the check registry and the verification engine.
//
// Layer L3. May import internal/journal, internal/runtime, and below.
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
// journal's account of history. See internal/journal for the full statement. The
// engine is expected to drop its journal import entirely once it lands, and a
// test will hold that line.
package verify
