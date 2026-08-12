package platform

import "testing"

// TestExactPattern pins IdentifierPattern to its exact string, so a future
// consolidation or refactor of ValidIdentifier cannot silently loosen or
// tighten the allowlist and have every caller pick up the change unnoticed.
// scripts/check-allowlist-regexp.sh guards the loose form from reappearing
// in prose; this test guards the one Go source of truth.
func TestExactPattern(t *testing.T) {
	const want = `^[a-zA-Z0-9][a-zA-Z0-9_-]*$`
	if IdentifierPattern != want {
		t.Fatalf("IdentifierPattern = %q, want %q", IdentifierPattern, want)
	}
}

// TestValidIdentifier is the refusal table for ValidIdentifier. It is the
// executable form of the ticket's acceptance criterion, so a future
// loosening goes red here rather than only in prose. The refusal cases are
// the ones the ticket names by name: empty, a short flag, a long flag, a
// bare hyphen, a leading underscore, an embedded space, a shell
// metacharacter, a path separator, a parent traversal, a trailing newline,
// and a non ASCII byte.
func TestValidIdentifier(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"plain name", "shellforge", true},
		{"sandbox user", "learner", true},
		{"single letter", "a", true},
		{"leading digit", "0abc", true},
		{"underscore and hyphen inside", "img_v2-1", true},
		{"pack id", "core-linux-basics", true},

		{"empty", "", false},
		{"short flag", "-f", false},
		{"long flag", "--force", false},
		{"bare hyphen", "-", false},
		{"leading underscore", "_tmp", false},
		{"embedded space", "a b", false},
		{"shell metacharacter", "a;b", false},
		{"path separator", "a/b", false},
		{"parent traversal", "..", false},
		{"trailing newline", "ok\n", false},
		{"embedded newline", "a\nb", false},
		{"non ascii", "café", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidIdentifier(tt.value); got != tt.want {
				t.Errorf("ValidIdentifier(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
