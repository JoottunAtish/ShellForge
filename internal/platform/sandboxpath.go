package platform

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// LearnerHomePrefix is the only prefix a level root, or the sandbox's state
// directory, may live under. The trailing slash is load-bearing: it is what
// makes /home/learner itself, without a trailing slash, fail the prefix test
// rather than pass it.
const LearnerHomePrefix = "/home/learner/"

// DefaultStateDir is where the shell instrumentation and the level engine
// share the journal, the environment snapshots, the control channel and the
// per-level SETUP_OK markers, unless an embedder overrides it.
//
// It lives here rather than in internal/content/setup, which owns the
// runner that uses it, for the same reason UnsafeLevelRoot does: the
// validator in internal/content has to know it too, and internal/content
// cannot import internal/content/setup because setup already imports
// content. L0 is the only layer both may read. See issue #116.
const DefaultStateDir = LearnerHomePrefix + ".shellforge"

// LevelRootCollidesWithStateDir reports why root may not be used as a level
// root alongside stateDir, or nil when the two are disjoint.
//
// A level root that equals, contains, or is contained by the state
// directory is lexically legal under the LearnerHomePrefix rule and still
// catastrophic: reset and teardown are rm -rf on the level root, so such a
// root would delete the journal, the command history, the control channel
// and every other level's progress marker.
//
// Containment is tested in both directions on purpose. The root sitting
// inside the state directory is the obvious case; the state directory
// sitting inside the root is the one that actually destroys progress, and
// it is the one a plausible typo produces (a root of /home/learner rather
// than /home/learner/quest).
//
// Lexical only, like UnsafeLevelRoot, and for the same reason: a symlink
// escape is a filesystem property no host-side string check can see. Callers
// that can resolve inside the sandbox re-check the resolved path through
// this same function.
func LevelRootCollidesWithStateDir(root, stateDir string) error {
	cleanRoot := path.Clean(root)
	cleanState := path.Clean(stateDir)

	switch {
	case cleanRoot == cleanState:
		return fmt.Errorf("it is the state directory %q itself", cleanState)
	case strings.HasPrefix(cleanRoot, cleanState+"/"):
		return fmt.Errorf("it is inside the state directory %q", cleanState)
	case strings.HasPrefix(cleanState, cleanRoot+"/"):
		return fmt.Errorf("it contains the state directory %q", cleanState)
	}
	return nil
}

// UnsafeLevelRoot reports why root must not be used as a level root, or as
// any other path bound for a recursive delete inside the sandbox, or nil
// when it is safe. It refuses in this order: empty, then a .. segment
// (checked BEFORE path.Clean, because cleaning is exactly what would hide
// it: path.Clean("/home/learner/../etc") returns "/home/etc", which passes a
// naive prefix check while pointing somewhere it must never point), then not
// absolute after cleaning, then "/" or ".", then anything outside the strict
// /home/learner/ prefix.
//
// It is lexical only. A symlink escaping the root is a filesystem property
// no host-side string check can see; a caller resolves the path inside the
// sandbox with readlink -m and re-checks the result through this same
// function, which is what internal/content/setup.resolveLevelRoot does.
//
// It uses path, not filepath, because every value it sees is a Linux path
// inside the sandbox, never a host path. filepath.Clean on Windows would
// turn a leading "/" into "\" and make a value such as "C:/Users/Admin" look
// absolute, which would silently disable the whole guard on a Windows build
// of this binary.
//
// It lives here, in internal/platform (layer L0), rather than in
// internal/content (layer L3, the validator's home) or internal/content/setup
// (layer L3, the runner's home), because internal/sandbox (layer L1) also
// needs it and internal/archtest's layer map only allows a dependency to
// point downward: L0 is the only layer all three callers may import. This is
// the same reasoning that put ValidIdentifier here rather than in
// internal/runtime: both are a lexical allowlist for a value bound for a
// destructive sandbox argv, one for an argv element and one for an rm -rf
// target.
func UnsafeLevelRoot(root string) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("it is empty")
	}
	for _, segment := range strings.Split(root, "/") {
		if segment == ".." {
			return errors.New("it contains a .. segment")
		}
	}
	clean := path.Clean(root)
	if !path.IsAbs(clean) {
		return errors.New("it is not absolute")
	}
	if clean == "/" || clean == "." {
		return fmt.Errorf("it resolves to %q", clean)
	}
	if !strings.HasPrefix(clean, LearnerHomePrefix) {
		return fmt.Errorf("level roots must live under %s", LearnerHomePrefix)
	}
	return nil
}
