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
