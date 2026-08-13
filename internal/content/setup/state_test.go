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

// TestSentinelPathRefusesAnUnsafeStateDir pins the #83 review suggestion:
// the state directory guard moved from resolveLevelRoot into sentinelPath,
// the one place all three public entry points go through, so a broken
// WithStateDir value fails closed here regardless of which of Setup,
// Teardown, or IsSetUp called it.
func TestSentinelPathRefusesAnUnsafeStateDir(t *testing.T) {
	for _, dir := range []string{"", "relative/state", "/etc/shellforge-state"} {
		if _, err := sentinelPath(dir, "nav-01"); err == nil {
			t.Errorf("sentinelPath(%q, ...): want an error, got nil", dir)
		}
	}
}

// TestIsSetUpRefusesAnUnsafeStateDir pins the same guard specifically at
// IsSetUp, which builds an argv from r.stateDir directly and, before the
// #83 review, never validated it: NewRunner(sess, packFS,
// WithStateDir("/etc")) followed by IsSetUp used to issue
// `test -f /etc/levels/nav-01/SETUP_OK` inside the sandbox instead of
// refusing. Asserting zero recorded test calls is what makes this
// mutation-testable: removing sentinelPath's validateLevelRoot call leaves
// IsSetUp still returning an error only if the fake happens to answer
// non-zero, but this fake does not script test at all, so the call would go
// through and this assertion would catch it.
func TestIsSetUpRefusesAnUnsafeStateDir(t *testing.T) {
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest"}}
	for _, dir := range []string{"", "relative/state", "/etc/shellforge-state"} {
		f := &fakeSession{}
		r := NewRunner(f, nil, WithStateDir(dir))
		if _, err := r.IsSetUp(context.Background(), lvl); err == nil {
			t.Errorf("WithStateDir(%q): IsSetUp: want an error, got nil", dir)
		}
		if n := f.countArgvPrefix("test"); n != 0 {
			t.Errorf("WithStateDir(%q): IsSetUp recorded %d test call(s), want 0: it must refuse before issuing one", dir, n)
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
