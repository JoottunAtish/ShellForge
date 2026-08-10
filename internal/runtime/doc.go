// Package runtime defines the Runtime and Session interfaces that abstract over
// the backend hosting the sandbox.
//
// Layer L1. May import internal/platform and internal/platform/ux.
//
// This package holds interfaces and value types only. Implementations live in
// subpackages (internal/runtime/docker, internal/runtime/wsl), and those
// subpackages may only be imported by cmd/shellforge and internal/sandbox. The
// layer test in internal/archtest enforces that.
//
// The point of the rule: dropping WSL support should be a one-day decision, not
// a rewrite. See the Day 3 go/no-go gate in docs/design/SEVEN-DAY-PLAN.md.
//
// Session.Exec takes an argv slice rather than a command string. That is a
// security boundary, not a style preference. Do not add a string overload.
package runtime
