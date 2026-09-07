package verify

import (
	"strings"
	"testing"
)

// TestEnvSnapshotPathIsAlwaysASandboxPath is a Windows regression test that
// has to be able to fail on Linux, which is why it asserts on the separator
// rather than on the host.
//
// The snapshot lives inside the container, and the container is always
// Linux. filepath.Join follows the separator of whatever host the binary was
// built for, so on Windows this produced a path with backslashes that no cat
// in the container could open. The read failed the only way Snapshots can
// fail, and env_var and cwd_is answered "the shell has not reported its
// state yet" on every Windows host regardless of what the shell had reported.
//
// CI runs the golden job on Linux, where filepath.Join is already correct, so
// nothing on the container side would ever have caught this.
func TestEnvSnapshotPathIsAlwaysASandboxPath(t *testing.T) {
	got := Snapshots{Dir: "/home/learner/.shellforge"}.envSnapshotPath()

	if want := "/home/learner/.shellforge/env.snapshot"; got != want {
		t.Errorf("envSnapshotPath() = %q, want %q", got, want)
	}
	if strings.ContainsRune(got, '\\') {
		t.Errorf("envSnapshotPath() = %q: a sandbox path must never carry the host's separator; use path.Join, not filepath.Join", got)
	}
}
