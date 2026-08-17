package platform

import "testing"

// TestUnsafeLevelRootRefusals is the union of the refusal tables that used to
// live separately in internal/content/validate.go, internal/content/setup,
// and internal/sandbox: every row a level root or a sandbox delete target
// must never pass. Deleting any single branch of UnsafeLevelRoot must make
// at least one of these rows go green.
func TestUnsafeLevelRootRefusals(t *testing.T) {
	cases := []struct {
		name string
		root string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"root of the filesystem", "/"},
		{"current directory", "."},
		{"a tilde, which no shell expands here", "~"},
		{"a tilde path", "~/quest"},
		{"the learner home directory itself", "/home/learner"},
		{"the learner home with a trailing slash", "/home/learner/"},
		{"traversal out of the home directory", "/home/learner/../etc"},
		{"traversal from inside the level root", "/home/learner/quest/../../etc"},
		{"a bare traversal", ".."},
		{"another user home directory", "/home/atish/quest"},
		{"an absolute path outside the home", "/etc"},
		{"a system directory", "/usr/lib"},
		{"a relative path", "quest"},
		{"a windows host path", `C:\Users\Admin`},
		{"a windows host path with forward slashes", "C:/Users/Admin"},
		{"a path that only looks like the prefix", "/home/learner2/quest"},
		{"the learner home directory, no trailing slash, from validate.go's table", "/home"},
		{"a deeper traversal out of the home directory", "/home/learner/../../etc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := UnsafeLevelRoot(tc.root); err == nil {
				t.Fatalf("UnsafeLevelRoot(%q) accepted a path it must refuse", tc.root)
			}
		})
	}
}

// TestUnsafeLevelRootAccepts is the other half: a helper that refuses
// everything would pass the refusal table by accident. This is what catches
// that.
func TestUnsafeLevelRootAccepts(t *testing.T) {
	cases := []string{
		"/home/learner/quest",
		"/home/learner/quest/",
		"/home/learner/quest/nested",
		"/home/learner/quest/warehouse/bay-3",
	}
	for _, root := range cases {
		t.Run(root, func(t *testing.T) {
			if err := UnsafeLevelRoot(root); err != nil {
				t.Fatalf("UnsafeLevelRoot(%q) refused a valid level root: %v", root, err)
			}
		})
	}
}

// TestUnsafeLevelRootNamesTheDotDotSegmentBeforeCleaning guards the ordering,
// not just the verdict. Both inputs clean to something a later branch would
// refuse anyway (the first to "/", the second to "/etc"), so a verdict-only
// assertion cannot tell whether the pre-Clean ".." segment scan is still
// there. Only the reason text can.
func TestUnsafeLevelRootNamesTheDotDotSegmentBeforeCleaning(t *testing.T) {
	cases := []string{
		"/home/learner/../..",
		"/home/learner/quest/../../../etc",
	}
	for _, root := range cases {
		t.Run(root, func(t *testing.T) {
			err := UnsafeLevelRoot(root)
			if err == nil {
				t.Fatalf("UnsafeLevelRoot(%q) accepted a path it must refuse", root)
			}
			const want = "it contains a .. segment"
			if got := err.Error(); got != want {
				t.Fatalf("UnsafeLevelRoot(%q) = %q, want %q", root, got, want)
			}
		})
	}
}
