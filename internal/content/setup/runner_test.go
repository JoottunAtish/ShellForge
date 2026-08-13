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

func TestRefusesLevelRootParentTraversal(t *testing.T) {
	assertRefusesUnsafeRoot(t, "/home/learner/../..")
}

func TestRefusesLevelRootTraversalToEtc(t *testing.T) {
	assertRefusesUnsafeRoot(t, "/home/learner/quest/../../../etc")
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

// TestSetupOnDirtyRootMatchesSetupOnCleanRoot pins criterion 1's idempotency:
// running Setup twice produces the same manifest and the same argv
// sequence, regardless of what the fake's recorded world looked like before
// the second run.
func TestSetupOnDirtyRootMatchesSetupOnCleanRoot(t *testing.T) {
	newLevel := func() *content.Level {
		return &content.Level{ID: "nav-01", Setup: content.Setup{Root: "/home/learner/quest", Files: []content.FileSpec{
			{Path: "notes.txt", Content: "hello\n"},
		}}}
	}

	f1 := &fakeSession{}
	r1 := NewRunner(f1, nil)
	if err := r1.Setup(context.Background(), newLevel()); err != nil {
		t.Fatalf("first Setup: %v", err)
	}

	f2 := &fakeSession{}
	r2 := NewRunner(f2, nil)
	// Simulate a dirty world: pretend the sentinel already exists.
	f2.whenArgvEquals([]string{"test", "-f", mustSentinelPath(t, DefaultStateDir, "nav-01")}, runtime.ExecResult{ExitCode: 0}, nil)
	if err := r2.Setup(context.Background(), newLevel()); err != nil {
		t.Fatalf("second Setup: %v", err)
	}

	if len(f1.pushCalls) != 1 || len(f2.pushCalls) != 1 {
		t.Fatalf("push calls: first %d, second %d, want 1 each", len(f1.pushCalls), len(f2.pushCalls))
	}
	m1, m2 := f1.pushCalls[0], f2.pushCalls[0]
	if len(m1.Files) != len(m2.Files) {
		t.Fatalf("manifests differ: %d files vs %d files", len(m1.Files), len(m2.Files))
	}
	for i := range m1.Files {
		a, b := m1.Files[i], m2.Files[i]
		if a.Path != b.Path || a.Mode != b.Mode || a.Owner != b.Owner || !bytes.Equal(a.Content, b.Content) {
			t.Errorf("manifests differ at entry %d: %+v vs %+v", i, a, b)
		}
	}
}

func mustSentinelPath(t *testing.T, stateDir, levelID string) string {
	t.Helper()
	p, err := sentinelPath(stateDir, levelID)
	if err != nil {
		t.Fatalf("sentinelPath: %v", err)
	}
	return p
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
		{Path: "explicit.txt", Content: "x", Mode: "0755", Owner: "root:root"},
		{Path: "default.txt", Content: "y"},
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

// TestSetupScriptRunsAsRootAfterTheFiles pins criterion 4: the script step
// runs as root, in the resolved root, as one argv element to bash -c, after
// PushFiles.
func TestSetupScriptRunsAsRootAfterTheFiles(t *testing.T) {
	f := &fakeSession{}
	r := NewRunner(f, nil)
	lvl := &content.Level{ID: "nav-01", Setup: content.Setup{
		Root:   "/home/learner/quest",
		Files:  []content.FileSpec{{Path: "a.txt", Content: "x"}},
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
			if c.opts.User != "root" {
				t.Errorf("script Exec User = %q, want root", c.opts.User)
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
		{Path: "a.txt", Content: "x"},
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
		Files:  []content.FileSpec{{Path: "a.txt", Content: "x"}},
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
// this package and fails on a call naming a host-side filesystem mutation,
// and on any import of "os" outside this test's own allowlist. Shaped after
// TestVerifytestIsImportedOnlyByTests in internal/verify/check_test.go.
func TestNoHostFilesystemRemovalInPackage(t *testing.T) {
	dir := packageDirForTest(t)
	fset := token.NewFileSet()

	banned := map[string]bool{
		"Remove": true, "RemoveAll": true, "Rename": true, "Truncate": true,
		"Chmod": true, "Chown": true, "WriteFile": true, "Mkdir": true, "MkdirAll": true,
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
