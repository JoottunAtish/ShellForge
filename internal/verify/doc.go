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
package verify
