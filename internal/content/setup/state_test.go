package setup

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// TestStateDirIsWritableAndOutsideOptShellforge pins criterion 7: the
// default state directory is inside the learner's own writable home, not
// under the read-only /opt/shellforge mount.
func TestStateDirIsWritableAndOutsideOptShellforge(t *testing.T) {
	if !strings.HasPrefix(DefaultStateDir, LevelRootPrefix) {
		t.Errorf("DefaultStateDir = %q, want a prefix of %q", DefaultStateDir, LevelRootPrefix)
	}
	if strings.HasPrefix(DefaultStateDir, "/opt/shellforge") {
		t.Errorf("DefaultStateDir = %q, must not live under /opt/shellforge", DefaultStateDir)
	}
}

// TestInstrumentationDefaultsToTheWritableStateDir reads the two shell files
// from the module root and asserts each contains DefaultStateDir as its
// SF_STATE default, and that neither still contains the old
// /opt/shellforge/state value. This is what stops the Go constant and the
// shell drifting apart.
func TestInstrumentationDefaultsToTheWritableStateDir(t *testing.T) {
	root := moduleRootForTest(t)

	for _, rel := range []string{"images/rc/instrument.bash", "images/bin/_sf-request"} {
		data, err := os.ReadFile(root + "/" + rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(data)
		if !strings.Contains(text, DefaultStateDir) {
			t.Errorf("%s does not contain %q as its SF_STATE default", rel, DefaultStateDir)
		}
		if strings.Contains(text, "/opt/shellforge/state") {
			t.Errorf("%s still contains the old /opt/shellforge/state default", rel)
		}
	}
}

// TestSentinelPathIsUnderTheStateDirAndNotTheLevelRoot pins criterion 6's
// path derivation.
func TestSentinelPathIsUnderTheStateDirAndNotTheLevelRoot(t *testing.T) {
	p, err := sentinelPath(DefaultStateDir, "nav-01")
	if err != nil {
		t.Fatalf("sentinelPath: %v", err)
	}
	const want = "/home/learner/.shellforge/levels/nav-01/SETUP_OK"
	if p != want {
		t.Errorf("sentinelPath = %q, want %q", p, want)
	}
	if strings.HasPrefix(p, "/home/learner/quest") {
		t.Errorf("sentinelPath = %q, must not sit under a level root such as /home/learner/quest", p)
	}
}

// TestSentinelPathRefusesAnInvalidLevelID pins the platform.ValidIdentifier
// guard: the level id becomes a path element, so it is validated first.
func TestSentinelPathRefusesAnInvalidLevelID(t *testing.T) {
	for _, id := range []string{"", "-x", "../etc", "nav 01"} {
		if _, err := sentinelPath(DefaultStateDir, id); err == nil {
			t.Errorf("sentinelPath(%q): want an error, got nil", id)
		}
	}
}

// TestIsSetUpReadsTheSentinel pins criterion 6: IsSetUp reads the sentinel
// through test -f and reports true, false, or an error, and never mutates.
func TestIsSetUpReadsTheSentinel(t *testing.T) {
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest"}}

	t.Run("true when the sentinel exists", func(t *testing.T) {
		f := &fakeSession{}
		f.when(func(argv []string) bool { return len(argv) > 0 && argv[0] == "test" }, runtime.ExecResult{ExitCode: 0}, nil)
		r := NewRunner(f, nil)
		got, err := r.IsSetUp(context.Background(), lvl)
		if err != nil {
			t.Fatalf("IsSetUp: %v", err)
		}
		if !got {
			t.Errorf("IsSetUp = false, want true")
		}
	})

	t.Run("false when the sentinel is absent", func(t *testing.T) {
		f := &fakeSession{}
		f.when(func(argv []string) bool { return len(argv) > 0 && argv[0] == "test" }, runtime.ExecResult{ExitCode: 1}, nil)
		r := NewRunner(f, nil)
		got, err := r.IsSetUp(context.Background(), lvl)
		if err != nil {
			t.Fatalf("IsSetUp: %v", err)
		}
		if got {
			t.Errorf("IsSetUp = true, want false")
		}
	})

	t.Run("error when Exec itself fails", func(t *testing.T) {
		f := &fakeSession{}
		f.when(func(argv []string) bool { return len(argv) > 0 && argv[0] == "test" }, runtime.ExecResult{}, errExecTransport)
		r := NewRunner(f, nil)
		if _, err := r.IsSetUp(context.Background(), lvl); err == nil {
			t.Fatal("IsSetUp: want an error when Exec itself fails")
		}
	})

	t.Run("records no mutating argv", func(t *testing.T) {
		f := &fakeSession{}
		f.when(func(argv []string) bool { return len(argv) > 0 && argv[0] == "test" }, runtime.ExecResult{ExitCode: 0}, nil)
		r := NewRunner(f, nil)
		if _, err := r.IsSetUp(context.Background(), lvl); err != nil {
			t.Fatalf("IsSetUp: %v", err)
		}
		for _, mutator := range []string{"rm", "mkdir", "touch", "chown"} {
			if f.countArgvPrefix(mutator) != 0 {
				t.Errorf("IsSetUp recorded a %q call, want none", mutator)
			}
		}
	})
}
