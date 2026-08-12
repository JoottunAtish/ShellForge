package platform

import "regexp"

// IdentifierPattern is the strict allowlist regexp for any value that
// reaches a docker or wsl.exe argv as a container name, distribution name,
// session user, or file manifest path element: exactly what the security
// skill quotes. The first character is restricted to alphanumeric so a
// validated value cannot be flag-shaped: once it reaches an argv vector as a
// positional operand, a value that can start with a hyphen is read as an
// option instead of a value. See the security skill's "Validate before you
// execute" section for the full argument injection rationale, and the
// destructive-safety skill for the additional compile-time constant
// comparison a destroy target needs on top of this.
//
// This is the single source of truth for the pattern. Every doc comment,
// skill, and gate script that used to spell it out verbatim now points here
// instead, so a future tightening or loosening cannot silently diverge
// between copies. TestExactPattern in identifier_test.go pins the exact
// string so consolidation itself cannot be the loosening.
const IdentifierPattern = `^[a-zA-Z0-9][a-zA-Z0-9_-]*$`

var identifierRegexp = regexp.MustCompile(IdentifierPattern)

// ValidIdentifier reports whether s is safe to use as an argv element
// derived into a container name, distribution name, session user, or file
// manifest path element. It matches IdentifierPattern.
//
// ValidIdentifier rejects; it never sanitizes. A caller that receives false
// must refuse the operation and report the value, not strip or escape the
// offending characters and proceed.
//
// It lives in internal/platform, not internal/runtime, because
// internal/runtime declares interfaces and value types only: see that
// package's doc.go. internal/runtime's own doc comments name this function
// rather than restating the pattern, and internal/runtime is already
// permitted to import internal/platform.
func ValidIdentifier(s string) bool {
	return identifierRegexp.MatchString(s)
}
