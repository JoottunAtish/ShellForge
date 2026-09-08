// Package bugreport collects the diagnostic bundle behind `shellforge
// bug-report`.
//
// Layer L4. It aggregates across L0 to L3 and learns nothing about a
// runtime backend, so it sits above internal/content and internal/verify
// and beside internal/game. It may import internal/doctor, internal/journal,
// internal/store, internal/content, internal/platform,
// internal/platform/ux, and the standard library. It must never import
// internal/runtime, internal/runtime/docker, internal/runtime/wsl,
// internal/sandbox, internal/game, cmd/shellforge, or any network client:
// sandbox state arrives through the Prober interface declared in this
// package and satisfied by the caller at L5, the same seam
// doctor.SandboxProber uses and for the same reason.
//
// Collect never fails for a missing or failing source. Every one of those
// becomes a Note on the Report instead of an error, because the most likely
// reason someone runs bug-report is that something, often Docker, is not
// working, and a diagnostic tool that refuses to run at the one moment it
// matters is useless.
//
// Without --journal the bundle carries no command text at all. With
// --journal every command's text goes through journal.Redact first. The
// bundle never carries command output, the environment snapshot, or the
// progress database file itself, and every host home directory prefix is
// rewritten to "~" by scrubHome. See internal/journal's doc.go for the
// closed list Redact applies.
package bugreport
