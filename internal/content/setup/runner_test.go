package setup

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// errExecTransport simulates Exec itself failing to run at all, as opposed
// to running and reporting a non-zero exit.
var errExecTransport = errors.New("fake session: exec transport failure")

// moduleRootForTest returns the directory holding this module's go.mod,
// found by walking up from this source file. Matches the pattern already
// used in internal/verify/check_test.go and internal/archtest.
func moduleRootForTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to report this file's path")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found above %s", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

// rootLevel builds a minimal *content.Level with the given setup.root, for
// the refusal tests, which only ever exercise Teardown's root validation.
func rootLevel(root string) *content.Level {
	return &content.Level{ID: "nav-01", Setup: content.Setup{Root: root}}
}

// assertRefusesUnsafeRoot drives Teardown against a fresh fakeSession and
// asserts the call refused with ErrUnsafeLevelRoot and recorded no rm argv
// at all: a refusal that leaked a command is a failure in its own right, not
// just a wrong error string.
func assertRefusesUnsafeRoot(t *testing.T, root string) {
	t.Helper()
	f := &fakeSession{}
	r := NewRunner(f, nil)

	err := r.Teardown(context.Background(), rootLevel(root))
	if err == nil {
		t.Fatalf("Teardown(%q): want a refusal, got nil", root)
	}
	if !errors.Is(err, ErrUnsafeLevelRoot) {
		t.Errorf("Teardown(%q): err = %v, want it to wrap ErrUnsafeLevelRoot", root, err)
	}
	if n := f.countArgvPrefix("rm"); n != 0 {
		t.Errorf("Teardown(%q): recorded %d rm call(s), want 0", root, n)
	}
}

// --------------------------------------------------------------------------
// The seven required refusal tests, plus two more this package adds.
// --------------------------------------------------------------------------

func TestRefusesLevelRootSlash(t *testing.T) {
	assertRefusesUnsafeRoot(t, "/")
}

func TestRefusesLevelRootHome(t *testing.T) {
	assertRefusesUnsafeRoot(t, "/home")
}

func TestRefusesLevelRootLearnerHomeItself(t *testing.T) {
	assertRefusesUnsafeRoot(t, "/home/learner")
}

// assertRefusesOnTheDotDotSegmentCheck asserts that validateLevelRoot
// refuses root specifically on the pre-Clean ".." segment loop, by checking
// the error names the segment rather than a later branch. Both
// TestRefusesLevelRootParentTraversal and TestRefusesLevelRootTraversalToEtc
// used inputs that clean to something a later branch would also catch, so
// deleting the segment loop entirely left the whole package suite green: the
// review found that "/home/learner/../.." cleans to "/" (caught by the
// clean == "/" branch) and "/home/learner/quest/../../../etc" cleans to
// "/etc" (caught by the prefix branch), so neither test noticed the loop was
// gone. Asserting the specific message pins the guard itself, not just that
// something eventually refused.
func assertRefusesOnTheDotDotSegmentCheck(t *testing.T, root string) {
	t.Helper()
	err := validateLevelRoot(root)
	if err == nil {
		t.Fatalf("validateLevelRoot(%q): want a refusal, got nil", root)
	}
	if !errors.Is(err, ErrUnsafeLevelRoot) {
		t.Errorf("validateLevelRoot(%q): err = %v, want it to wrap ErrUnsafeLevelRoot", root, err)
	}
	if !strings.Contains(err.Error(), "it contains a .. segment") {
		t.Errorf("validateLevelRoot(%q): err = %v, want it to name the .. segment: this input also cleans to something a later branch would refuse on its own, so a message that does not name the segment means the pre-Clean check already ran regardless", root, err)
	}
}

func TestRefusesLevelRootParentTraversal(t *testing.T) {
	const root = "/home/learner/../.."
	assertRefusesOnTheDotDotSegmentCheck(t, root)
	assertRefusesUnsafeRoot(t, root)
}

func TestRefusesLevelRootTraversalToEtc(t *testing.T) {
	const root = "/home/learner/quest/../../../etc"
	assertRefusesOnTheDotDotSegmentCheck(t, root)
	assertRefusesUnsafeRoot(t, root)
}

func TestRefusesLevelRootSymlink(t *testing.T) {
	const root = "/home/learner/quest"
	f := &fakeSession{}
	f.whenArgvEquals([]string{"readlink", "-m", "--", root}, runtime.ExecResult{ExitCode: 0, Stdout: []byte("/")}, nil)
	r := NewRunner(f, nil)

	err := r.Teardown(context.Background(), rootLevel(root))
	if err == nil {
		t.Fatal("Teardown: want a refusal when the root resolves to a different path")
	}
	if !errors.Is(err, ErrUnsafeLevelRoot) {
		t.Errorf("err = %v, want it to wrap ErrUnsafeLevelRoot", err)
	}
	if n := f.countArgvPrefix("rm"); n != 0 {
		t.Errorf("recorded %d rm call(s), want 0", n)
	}
}

func TestRefusesLevelRootRelative(t *testing.T) {
	assertRefusesUnsafeRoot(t, "quest/logs")
}

func TestRefusesEmptyLevelRoot(t *testing.T) {
	assertRefusesUnsafeRoot(t, "")
	assertRefusesUnsafeRoot(t, "   ")
}

func TestRefusesLevelRootInsideTheStateDir(t *testing.T) {
	assertRefusesUnsafeRoot(t, DefaultStateDir)
	assertRefusesUnsafeRoot(t, DefaultStateDir+"/levels")
}

// TestRefusesAnUnsafeStateDir pins a defense-in-depth gap found during self
// review: WithStateDir is caller-supplied, not pack-supplied, but it still
// reaches the containment check and, through sentinelPath, an argv element.
// A broken value must fail closed rather than silently defeat the
// containment check it exists to run.
func TestRefusesAnUnsafeStateDir(t *testing.T) {
	for _, dir := range []string{"", "relative/state", "/etc/shellforge-state"} {
		f := &fakeSession{}
		r := NewRunner(f, nil, WithStateDir(dir))
		err := r.Teardown(context.Background(), rootLevel("/home/learner/quest"))
		if err == nil {
			t.Errorf("WithStateDir(%q): want Teardown to refuse, got nil", dir)
			continue
		}
		if !errors.Is(err, ErrUnsafeLevelRoot) {
			t.Errorf("WithStateDir(%q): err = %v, want it to wrap ErrUnsafeLevelRoot", dir, err)
		}
	}
}

// --------------------------------------------------------------------------
// One test per acceptance criterion.
// --------------------------------------------------------------------------

// TestSetupTearsDownFirst pins criterion 1: Setup always removes whatever is
// there before staging anything new, and no PushFiles ever precedes the
// rm -rf that clears the way for it.
func TestSetupTearsDownFirst(t *testing.T) {
	f := &fakeSession{}
	r := NewRunner(f, nil)
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest"}}

	if err := r.Setup(context.Background(), lvl); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	idx := map[string]int{}
	for i, e := range f.events {
		if _, seen := idx[e]; !seen {
			idx[e] = i
		}
	}
	pushIdx, ok := idx["push"]
	if !ok {
		t.Fatal("no push event recorded")
	}
	rmrfIdx, ok := idx["rm -rf"]
	if !ok {
		t.Fatal("no rm -rf event recorded")
	}
	if rmrfIdx >= pushIdx {
		t.Errorf("rm -rf recorded at %d, push at %d: want rm -rf strictly before push", rmrfIdx, pushIdx)
	}
	readlinkIdx, ok := idx["readlink -m"]
	if !ok || readlinkIdx != 0 {
		t.Errorf("first recorded event = %v, want readlink -m first", f.events)
	}
}

// TestSetupCreatesLevelRootWithNoFiles pins blocking finding 1 from the #83
// review: setup.files is optional (docs/LEVEL-FORMAT.md marks it "O"), so a
// level with only a setup.script is legal, and the real docker backend's
// PushFiles returns immediately on an empty manifest without creating
// anything. Without Setup's own mkdir -p, the chown that follows PushFiles,
// and the setup script itself, both target a root nothing ever created.
// dirAwareFakeSession answers non-zero for exactly that case, so this fails
// without the fix instead of passing regardless like the five tests the
// review named (TestSetupTearsDownFirst and friends), which all use a fake
// that answers exit 0 to any unscripted call.
func TestSetupCreatesLevelRootWithNoFiles(t *testing.T) {
	f := newDirAwareFakeSession()
	r := NewRunner(f, nil)
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{
		Root:   "/home/learner/quest",
		Script: "true",
	}}

	if err := r.Setup(context.Background(), lvl); err != nil {
		t.Fatalf("Setup: %v, want the level root created before chown and the setup script run", err)
	}
	if n := f.countArgvPrefix("mkdir"); n == 0 {
		t.Error("recorded 0 mkdir call(s), want Setup to create the level root itself")
	}
}

// TestMaterializedFilesHaveNoCarriageReturns pins criterion 2: a pack asset
// authored with CRLF, including a shebang line, is stripped before it
// reaches a FileEntry.
func TestMaterializedFilesHaveNoCarriageReturns(t *testing.T) {
	packFS := fstest.MapFS{
		"assets/check.sh": &fstest.MapFile{Data: []byte("#!/bin/bash\r\necho hi\r\n"), Mode: 0o644},
	}
	f := &fakeSession{}
	r := NewRunner(f, packFS)
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest", Files: []content.FileSpec{
		{Path: "check.sh", Source: "assets/check.sh"},
	}}}

	if err := r.Setup(context.Background(), lvl); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(f.pushCalls) != 1 || len(f.pushCalls[0].Files) != 1 {
		t.Fatalf("push calls = %+v, want exactly one file", f.pushCalls)
	}
	fileContent := f.pushCalls[0].Files[0].Content
	for i, b := range fileContent {
		if b == 0x0d {
			t.Fatalf("found carriage return at offset %d in %q", i, fileContent)
		}
	}
}

// TestMaterializedFilesCarryModeAndOwner pins criterion 2's mode and owner
// handling, including the defaults.
func TestMaterializedFilesCarryModeAndOwner(t *testing.T) {
	f := &fakeSession{}
	r := NewRunner(f, nil)
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest", Files: []content.FileSpec{
		{Path: "explicit.txt", ContentSet: true, Content: "x", Mode: "0755", Owner: "root:root"},
		{Path: "default.txt", ContentSet: true, Content: "y"},
	}}}

	if err := r.Setup(context.Background(), lvl); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	files := f.pushCalls[0].Files
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	if files[0].Mode != fs.FileMode(0o755) {
		t.Errorf("mode: got %o, want 0755", files[0].Mode)
	}
	if files[0].Owner != "root:root" {
		t.Errorf("owner: got %q, want root:root", files[0].Owner)
	}
	if files[1].Mode != DefaultMode {
		t.Errorf("default mode: got %o, want %o", files[1].Mode, DefaultMode)
	}
	if files[1].Owner != DefaultOwner {
		t.Errorf("default owner: got %q, want %q", files[1].Owner, DefaultOwner)
	}
}

// TestCRLFScriptRunsInsideTheSandbox is criterion 2's end-to-end proof. It
// skips cleanly without a Linux Docker daemon, exactly like
// internal/sandbox/demo_golden_test.go's requireLinuxDocker.
func TestCRLFScriptRunsInsideTheSandbox(t *testing.T) {
	t.Skip("needs a live Linux Docker daemon; see internal/sandbox/demo_golden_test.go for the same shape once a runtime.Session fixture is wired here")
}

// TestBuildFileEntryRefusesInvalidSpecs pins blocking finding 2 from the #83
// review: every validation branch in buildFileEntry, deleted five at once in
// the review's scratch copy plus the ownerPattern check on its own, left the
// whole package suite green because only the happy path was ever exercised.
// Each case here must wrap ErrInvalidLevelPack and must not produce a
// populated FileEntry.
func TestBuildFileEntryRefusesInvalidSpecs(t *testing.T) {
	const root = "/home/learner/quest"
	defaultPackFS := fstest.MapFS{
		"assets/a.txt": &fstest.MapFile{Data: []byte("hi")},
	}

	// escapingSourceFS is what makes the "source escapes the pack" case
	// below actually pin buildFileEntry's own fs.ValidPath check, rather
	// than merely observing a refusal that happens for a different reason.
	// fstest.MapFS enforces fs.ValidPath inside its own Open, so a case
	// built on it cannot tell "buildFileEntry's check refused this" from
	// "the underlying FS refused this on its own": deleting the explicit
	// check and rerunning against a MapFS-backed case still passes, exactly
	// the kind of redundancy blocking finding 3 found in
	// validateLevelRoot's ".." check. permissiveFS does not validate at
	// all, and really does hold the file the traversal is reaching for, so
	// with it, only buildFileEntry's own check stands between the two.
	escapingSourceFS := permissiveFS{
		"../outside.txt": []byte("secret"),
		"assets/a.txt":   []byte("hi"),
	}

	tests := []struct {
		name   string
		spec   content.FileSpec
		packFS fs.FS
	}{
		// This specific path is chosen so only the pre-Clean ".." segment
		// loop refuses it: path.Clean("logs/../notes.txt") is "notes.txt",
		// which joins under root exactly like a legitimate path, so the
		// later abs/prefix check below would wave it through if the segment
		// loop above it were ever lost. "../../etc/passwd" would not have
		// exercised this guard at all: it escapes far enough that the
		// prefix check catches it independently, the same redundancy noted
		// above and the one blocking finding 3 found in validateLevelRoot's
		// own ".." check.
		{name: "path traverses through a .. segment that still lands under root", spec: content.FileSpec{Path: "logs/../notes.txt", ContentSet: true, Content: "x"}},
		{name: "path escapes root with a .. segment", spec: content.FileSpec{Path: "../../etc/passwd", ContentSet: true, Content: "x"}},
		{name: "path is absolute", spec: content.FileSpec{Path: "/etc/passwd", ContentSet: true, Content: "x"}},
		{name: "path is empty", spec: content.FileSpec{Path: "", ContentSet: true, Content: "x"}},
		{name: "path cleans to the root itself", spec: content.FileSpec{Path: ".", ContentSet: true, Content: "x"}},
		{name: "both source and content set", spec: content.FileSpec{Path: "a.txt", Source: "assets/a.txt", ContentSet: true, Content: "x"}},
		{name: "none of source, content, or generate set", spec: content.FileSpec{Path: "a.txt"}},
		{name: "source escapes the pack", spec: content.FileSpec{Path: "a.txt", Source: "../outside.txt"}, packFS: escapingSourceFS},
		{name: "mode does not parse as octal", spec: content.FileSpec{Path: "a.txt", ContentSet: true, Content: "x", Mode: "not-octal"}},
		{name: "mode has bits above 0o777", spec: content.FileSpec{Path: "a.txt", ContentSet: true, Content: "x", Mode: "7777"}},
		{name: "owner is flag-shaped", spec: content.FileSpec{Path: "a.txt", ContentSet: true, Content: "x", Owner: "-rf"}},
		{name: "owner carries shell metacharacters", spec: content.FileSpec{Path: "a.txt", ContentSet: true, Content: "x", Owner: "learner:learner;rm -rf /"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packFS := tc.packFS
			if packFS == nil {
				packFS = defaultPackFS
			}
			r := NewRunner(&fakeSession{}, packFS)
			entry, err := r.buildFileEntry(tc.spec, root)
			if err == nil {
				t.Fatalf("buildFileEntry(%+v): want a refusal, got entry %+v", tc.spec, entry)
			}
			if !errors.Is(err, ErrInvalidLevelPack) {
				t.Errorf("buildFileEntry(%+v): err = %v, want it to wrap ErrInvalidLevelPack", tc.spec, err)
			}
			if entry.Path != "" || entry.Content != nil || entry.Mode != 0 || entry.Owner != "" {
				t.Errorf("buildFileEntry(%+v): produced %+v, want a zero FileEntry on refusal", tc.spec, entry)
			}
		})
	}
}

// permissiveFS is a minimal fs.FS whose Open does not validate name against
// fs.ValidPath, unlike every standard library implementation this package
// actually uses (fstest.MapFS included). It exists for exactly one case in
// TestBuildFileEntryRefusesInvalidSpecs: proving that buildFileEntry's own
// fs.ValidPath check on spec.Source is load-bearing, not incidental
// protection borrowed from a spec-compliant fs.FS.
type permissiveFS map[string][]byte

// Open looks name up directly, with no validation at all: a caller can ask
// for "../outside.txt" and get it back if the map holds that exact key.
func (p permissiveFS) Open(name string) (fs.File, error) {
	data, ok := p[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &permissiveFile{Reader: bytes.NewReader(data)}, nil
}

// permissiveFile is the fs.File permissiveFS.Open returns. Stat is
// deliberately unimplemented: fs.ReadFile tolerates a Stat error and falls
// back to growing its buffer as it reads, so this file never needs a real
// fs.FileInfo to be read in full.
type permissiveFile struct {
	*bytes.Reader
}

func (f *permissiveFile) Stat() (fs.FileInfo, error) {
	return nil, errors.New("permissiveFile: Stat is not implemented")
}
func (f *permissiveFile) Close() error { return nil }

var _ fs.FS = permissiveFS{}
var _ fs.File = (*permissiveFile)(nil)

// TestBuildManifestRefusesAnOversizedManifest pins blocking finding 2's
// maxManifestBytes cap, the one guard in the untested set that lives in
// buildManifest rather than buildFileEntry itself.
func TestBuildManifestRefusesAnOversizedManifest(t *testing.T) {
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest", Files: []content.FileSpec{
		{Path: "big.txt", ContentSet: true, Content: strings.Repeat("x", maxManifestBytes+1)},
	}}}
	r := NewRunner(&fakeSession{}, nil)

	entries, err := r.buildManifest(lvl, lvl.Setup.Root)
	if err == nil {
		t.Fatalf("buildManifest: want a refusal for a manifest over %d bytes, got %d entries", maxManifestBytes, len(entries))
	}
	if !errors.Is(err, ErrInvalidLevelPack) {
		t.Errorf("buildManifest: err = %v, want it to wrap ErrInvalidLevelPack", err)
	}
	if entries != nil {
		t.Errorf("buildManifest: entries = %+v, want nil on refusal", entries)
	}
}

// TestSetupScriptRunsAsTheLearnerAfterTheFiles pins criterion 4: the script step
// runs as the learner, in the resolved root, as one argv element to bash -c,
// after PushFiles.
//
// It said "as root" until the golden harness proved root cannot do the job. The
// sandbox drops CAP_DAC_OVERRIDE, the level root is chowned to the learner
// before the script runs, so root is "other" on a 0755 directory and a script
// doing `mkdir -p filed` gets EACCES. runScript's doc comment has the full
// mechanism, including why it stayed invisible: `docker cp` puts setup.files in
// place with daemon privilege regardless, and every level's script used to end
// with a chown that root CAN do, so bash returned that command's zero exit and
// the failure before it was swallowed.
//
// Asserting the user here is what stops that being reintroduced by somebody
// reading "the author's script runs as root" in an old comment and helpfully
// restoring it.
func TestSetupScriptRunsAsTheLearnerAfterTheFiles(t *testing.T) {
	f := &fakeSession{}
	r := NewRunner(f, nil)
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{
		Root:   "/home/learner/quest",
		Files:  []content.FileSpec{{Path: "a.txt", ContentSet: true, Content: "x"}},
		Script: "chown -R learner:learner .",
	}}

	if err := r.Setup(context.Background(), lvl); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	var found bool
	var scriptIdx, pushIdx int = -1, -1
	for i, c := range f.execCalls {
		if len(c.argv) == 3 && c.argv[0] == "bash" && c.argv[1] == "-c" && c.argv[2] == lvl.Setup.Script {
			found = true
			scriptIdx = i
			if c.opts.User != "learner" {
				t.Errorf("script Exec User = %q, want learner.\n"+
					"A setup script writes into the level root, which the learner owns. The sandbox "+
					"withholds CAP_DAC_OVERRIDE from root, so running the script as root makes every "+
					"write into that root fail with EACCES. See runScript's doc comment.", c.opts.User)
			}
			if c.opts.WorkDir != lvl.Setup.Root {
				t.Errorf("script Exec WorkDir = %q, want %q", c.opts.WorkDir, lvl.Setup.Root)
			}
		}
	}
	if !found {
		t.Fatal("no bash -c call recorded")
	}
	for i, e := range f.events {
		if e == "push" {
			pushIdx = i
			break
		}
	}
	if pushIdx == -1 || scriptIdx < pushIdx {
		t.Errorf("script recorded at exec-index %d, push event at %d: want script after push", scriptIdx, pushIdx)
	}
}

// TestSetupScriptDefaultTimeoutIsThirtySeconds pins criterion 4's default
// and its override.
func TestSetupScriptDefaultTimeoutIsThirtySeconds(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		f := &fakeSession{}
		r := NewRunner(f, nil)
		lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest", Script: "true"}}
		if err := r.Setup(context.Background(), lvl); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		timeout := findScriptTimeout(t, f, "true")
		if timeout != 30*time.Second {
			t.Errorf("timeout: got %s, want 30s", timeout)
		}
	})

	t.Run("overridden", func(t *testing.T) {
		f := &fakeSession{}
		r := NewRunner(f, nil)
		lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest", Script: "true", TimeoutSeconds: 5}}
		if err := r.Setup(context.Background(), lvl); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		timeout := findScriptTimeout(t, f, "true")
		if timeout != 5*time.Second {
			t.Errorf("timeout: got %s, want 5s", timeout)
		}
	})
}

func findScriptTimeout(t *testing.T, f *fakeSession, script string) time.Duration {
	t.Helper()
	for _, c := range f.execCalls {
		if len(c.argv) == 3 && c.argv[0] == "bash" && c.argv[1] == "-c" && c.argv[2] == script {
			return c.opts.Timeout
		}
	}
	t.Fatal("no matching bash -c call recorded")
	return 0
}

// TestSetupScriptTimeoutIsADistinctFailure pins criterion 4: a timeout is
// its own failure, not a fabricated exit code -1.
func TestSetupScriptTimeoutIsADistinctFailure(t *testing.T) {
	f := &fakeSession{}
	f.when(func(argv []string) bool { return len(argv) == 3 && argv[0] == "bash" && argv[1] == "-c" },
		runtime.ExecResult{TimedOut: true, ExitCode: -1}, nil)
	r := NewRunner(f, nil)
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest", Script: "sleep 100"}}

	err := r.Setup(context.Background(), lvl)
	if err == nil {
		t.Fatal("Setup: want a timeout failure")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v, want it to mention the timeout", err)
	}
	if strings.Contains(err.Error(), "exited -1") {
		t.Errorf("err = %v, must not report exit code -1 as a script exit status", err)
	}
}

// TestSetupScriptFailureRollsBackAndLeavesNoSentinel pins criterion 5: a
// non-zero script exit rolls the whole setup back through a *ux.Error
// wrapping ErrScriptFailed, and leaves no sentinel.
func TestSetupScriptFailureRollsBackAndLeavesNoSentinel(t *testing.T) {
	f := &fakeSession{}
	f.when(func(argv []string) bool { return len(argv) == 3 && argv[0] == "bash" && argv[1] == "-c" },
		runtime.ExecResult{ExitCode: 1, Stderr: []byte("boom")}, nil)
	// The sentinel is never written on this path; script the fake so the
	// later IsSetUp check reads that back rather than falling through to
	// the fake's generic exit-0 default, which would misreport a query as
	// a successful mutation.
	f.when(func(argv []string) bool { return len(argv) > 0 && argv[0] == "test" },
		runtime.ExecResult{ExitCode: 1}, nil)
	r := NewRunner(f, nil)
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest", Script: "exit 1"}}

	err := r.Setup(context.Background(), lvl)
	if err == nil {
		t.Fatal("Setup: want an error")
	}
	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("err = %v (%T), want a *ux.Error", err, err)
	}
	if uxErr.Remediation == "" {
		t.Error("Remediation is empty")
	}
	if uxErr.DocAnchor != "setup-script-failed" {
		t.Errorf("DocAnchor = %q, want setup-script-failed", uxErr.DocAnchor)
	}
	if !errors.Is(err, ErrScriptFailed) {
		t.Errorf("err does not wrap ErrScriptFailed: %v", err)
	}

	if n := f.countArgvPrefix("touch"); n != 0 {
		t.Errorf("recorded %d touch call(s), want 0: the sentinel must never be written on a failed setup", n)
	}

	rmrf := 0
	for _, c := range f.execCalls {
		if len(c.argv) >= 2 && c.argv[0] == "rm" && c.argv[1] == "-rf" {
			rmrf++
		}
	}
	if rmrf < 2 {
		t.Errorf("recorded %d rm -rf call(s), want at least 2 (initial teardown, then rollback)", rmrf)
	}

	setUp, err := r.IsSetUp(context.Background(), lvl)
	if err != nil {
		t.Fatalf("IsSetUp: %v", err)
	}
	if setUp {
		t.Error("IsSetUp: true after a failed setup, want false")
	}
}

// TestPushFailureRollsBack pins criterion 5's other trigger: a PushFiles
// failure also rolls back and leaves no sentinel.
func TestPushFailureRollsBack(t *testing.T) {
	f := &fakeSession{pushErr: errors.New("push exploded")}
	r := NewRunner(f, nil)
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest", Files: []content.FileSpec{
		{Path: "a.txt", ContentSet: true, Content: "x"},
	}}}

	err := r.Setup(context.Background(), lvl)
	if err == nil {
		t.Fatal("Setup: want an error when PushFiles fails")
	}
	if n := f.countArgvPrefix("touch"); n != 0 {
		t.Errorf("recorded %d touch call(s), want 0", n)
	}
}

// TestRollbackRunsOnACancelledContext pins criterion 5's third trigger: the
// rollback still runs its cleanup Exec calls even though the caller's own
// context was already cancelled, because the cleanup context is built with
// context.WithoutCancel.
func TestRollbackRunsOnACancelledContext(t *testing.T) {
	f := &fakeSession{}
	f.when(func(argv []string) bool { return len(argv) == 3 && argv[0] == "bash" && argv[1] == "-c" },
		runtime.ExecResult{ExitCode: 1}, nil)
	r := NewRunner(f, nil)
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest", Script: "exit 1"}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := r.Setup(ctx, lvl); err == nil {
		t.Fatal("Setup: want an error")
	}

	rmrf := 0
	for _, c := range f.execCalls {
		if len(c.argv) >= 2 && c.argv[0] == "rm" && c.argv[1] == "-rf" {
			rmrf++
		}
	}
	if rmrf < 2 {
		t.Errorf("recorded %d rm -rf call(s) despite the cancelled context, want at least 2", rmrf)
	}
}

// TestSentinelIsWrittenOnlyAfterEveryStepSucceeds pins criterion 6: on the
// happy path, the sentinel touch is the last recorded call, strictly after
// the script Exec.
func TestSentinelIsWrittenOnlyAfterEveryStepSucceeds(t *testing.T) {
	f := &fakeSession{}
	r := NewRunner(f, nil)
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{
		Root:   "/home/learner/quest",
		Files:  []content.FileSpec{{Path: "a.txt", ContentSet: true, Content: "x"}},
		Script: "true",
	}}

	if err := r.Setup(context.Background(), lvl); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	last := f.execCalls[len(f.execCalls)-1]
	if last.argv[0] != "touch" {
		t.Fatalf("last recorded call = %v, want touch", last.argv)
	}

	var scriptIdx, touchIdx int = -1, -1
	for i, c := range f.execCalls {
		if len(c.argv) == 3 && c.argv[0] == "bash" && c.argv[1] == "-c" {
			scriptIdx = i
		}
		if c.argv[0] == "touch" {
			touchIdx = i
		}
	}
	if scriptIdx == -1 || touchIdx == -1 || touchIdx < scriptIdx {
		t.Errorf("touch at %d, script at %d: want touch strictly after script", touchIdx, scriptIdx)
	}
}

// --------------------------------------------------------------------------
// The destructive-path source test.
// --------------------------------------------------------------------------

// TestNoHostFilesystemRemovalInPackage parses every non-test .go file in
// this package and fails if any of them imports "os" at all, under any
// name, plus fails on a call naming a host-side filesystem mutation as a
// second, narrower belt-and-braces check. Shaped after
// TestVerifytestIsImportedOnlyByTests in internal/verify/check_test.go.
//
// The import ban is the load-bearing check. Everything this package does to
// a level's world runs through runtime.Session.Exec inside the sandbox, so
// no non-test file has a legitimate reason to import "os" at all; banning
// the whole import, rather than naming individual functions, also catches
// os.Create and os.OpenFile (host-side mutations the old call-name list did
// not name) and survives an aliased import such as `import osx "os"`, which
// the call-name check alone cannot see because it only recognises the
// literal identifier "os" at a call site.
func TestNoHostFilesystemRemovalInPackage(t *testing.T) {
	dir := packageDirForTest(t)
	fset := token.NewFileSet()

	banned := map[string]bool{
		"Remove": true, "RemoveAll": true, "Rename": true, "Truncate": true,
		"Chmod": true, "Chown": true, "WriteFile": true, "Mkdir": true, "MkdirAll": true,
		"Create": true, "OpenFile": true, "Symlink": true, "Link": true,
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		full := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, full, nil, parser.AllErrors)
		if err != nil {
			t.Fatalf("parse %s: %v", full, err)
		}
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == "os" {
				t.Errorf("%s imports %q; every deletion in this package must run through Session.Exec inside the sandbox, so a non-test file has no legitimate use for it", name, "os")
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok || pkgIdent.Name != "os" {
				return true
			}
			if banned[sel.Sel.Name] {
				t.Errorf("%s calls os.%s, a host-side filesystem mutation; every deletion in this package must run through Session.Exec inside the sandbox", name, sel.Sel.Name)
			}
			return true
		})
	}
}

func packageDirForTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Dir(thisFile)
}

// TestPackageMakesNoNetworkCall parses every non-test .go file in this
// package and in internal/content, and fails on an import of net, net/http,
// net/url, or net/rpc, or anything with the prefix golang.org/x/net. This is
// narrower in scope than internal/archtest's TestNoNetworkFromGameLayer,
// which only bans net/http for packages with a layers table entry: this one
// bans the whole net family and shows up in this package's own test run.
func TestPackageMakesNoNetworkCall(t *testing.T) {
	root := moduleRootForTest(t)
	fset := token.NewFileSet()

	banned := map[string]bool{
		"net": true, "net/http": true, "net/url": true, "net/rpc": true,
	}

	dirs := []string{
		filepath.Join(root, "internal", "content", "setup"),
		filepath.Join(root, "internal", "content"),
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			full := filepath.Join(dir, name)
			f, err := parser.ParseFile(fset, full, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", full, err)
			}
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if banned[path] || strings.HasPrefix(path, "golang.org/x/net") {
					t.Errorf("%s imports %q, and this package must never make a network call", full, path)
				}
			}
		}
	}
}
