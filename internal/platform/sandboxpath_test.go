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

// TestLevelRootCollidesWithStateDirRefusals is a refusal table, in the shape
// destructive-safety requires for anything bound for an rm -rf. Every case
// here is a path that passes UnsafeLevelRoot cleanly and would still destroy
// the learner's progress if it were used.
func TestLevelRootCollidesWithStateDirRefusals(t *testing.T) {
	const state = DefaultStateDir

	cases := []struct {
		name string
		root string
	}{
		{"the state directory itself", DefaultStateDir},
		{"the state directory with a trailing slash", DefaultStateDir + "/"},
		{"an uncleaned spelling of the state directory", "/home/learner/./.shellforge"},
		{"a directory inside the state directory", DefaultStateDir + "/levels"},
		{"a level's own marker directory", DefaultStateDir + "/levels/nav-01"},
		// The direction that actually destroys progress, and the one a
		// plausible typo produces.
		{"the learner home, which contains the state directory", "/home/learner"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := LevelRootCollidesWithStateDir(tc.root, state); err == nil {
				t.Errorf("LevelRootCollidesWithStateDir(%q, %q) = nil, want a refusal: reset and teardown are rm -rf on this path", tc.root, state)
			}
		})
	}
}

func TestLevelRootCollidesWithStateDirAccepts(t *testing.T) {
	const state = DefaultStateDir

	cases := []string{
		"/home/learner/quest",
		"/home/learner/quest/",
		"/home/learner/atlas/logs",
		// A sibling whose name merely starts with the state directory's,
		// which a naive prefix test without the separator would refuse.
		"/home/learner/.shellforge-backup",
	}

	for _, root := range cases {
		t.Run(root, func(t *testing.T) {
			if err := LevelRootCollidesWithStateDir(root, state); err != nil {
				t.Errorf("LevelRootCollidesWithStateDir(%q, %q) = %v, want nil", root, state, err)
			}
		})
	}
}

// TestDefaultStateDirIsUnderTheLearnerHome pins the one property everything
// else here assumes. A state directory outside the learner home would make
// every collision check above vacuous and would put the journal somewhere
// the sandbox user may not be able to write.
func TestDefaultStateDirIsUnderTheLearnerHome(t *testing.T) {
	if err := UnsafeLevelRoot(DefaultStateDir); err != nil {
		t.Errorf("DefaultStateDir %q does not satisfy UnsafeLevelRoot: %v", DefaultStateDir, err)
	}
}
