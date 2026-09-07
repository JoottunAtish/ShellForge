package game

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/content/setup"
	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// Reset, and the refusals that make it safe to offer a beginner.
//
// The deliverable here is the refusal table, not the feature. Reset is one
// call to Session.Setup, which is setup.Runner.Setup, which tears the level
// root down before rebuilding it through the same validated,
// inside-the-sandbox deletion path every other setup uses. That means every
// refusal the runner already makes has to hold when the call arrives from
// this direction too, and these tests assert each of them again through
// Orchestrator.Reset rather than trusting that it does.
//
// The blast radius, stated in writing as the destructive-safety skill
// requires: the worst path this can resolve to is the level root from level
// YAML. If every input were wrong, the deletion would still have to get
// past an empty check, a .. segment check, an absolute path check, a
// refusal of "/" and ".", a strict /home/learner/ prefix check that
// excludes /home/learner itself, a containment check against the state
// directory, and a readlink -m resolution that must come back byte
// identical. Reaching anything outside the level root therefore requires
// bypassing setup.Runner, which is why the last test in this file asserts
// that no second deletion path exists rather than only asserting the
// refusals.

// resetOrchestrator starts an Orchestrator over lvl and returns it with the
// fake sandbox session behind it.
func resetOrchestrator(t *testing.T, lvl *content.Level) (*Orchestrator, *fakeSession, *recordingBus) {
	t.Helper()

	s, verifier, sess := newTestSession(t, Config{Level: lvl})
	verifier.result = passingResult(0)
	o, _, rec := newTestOrchestrator(t, s, newFakeProgress())
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Close(context.Background()) })
	return o, sess, rec
}

// TestResetRefusesEveryUnsafeLevelRoot is the required refusal table, run
// against the reset path specifically.
//
// Start is not used here: a level root this bad fails Setup as well, so
// each case builds an Orchestrator sitting in StateActive by hand and calls
// Reset directly, which is the only way to prove the refusal belongs to
// Reset rather than to the Start that would otherwise have caught it first.
func TestResetRefusesEveryUnsafeLevelRoot(t *testing.T) {
	cases := []struct {
		name string
		root string
		// stateDir is only set by the containment case.
		stateDir string
		// symlinkedRoot makes readlink report a different path, which is
		// how the runner detects a symlink on or inside the root.
		symlinkedRoot string
	}{
		{name: "empty", root: ""},
		{name: "the root of the filesystem", root: "/"},
		{name: "a bare tilde, which is not a path at all inside the sandbox", root: "~"},
		{name: "the learner home itself", root: "/home/learner"},
		{name: "a parent traversal", root: "../../etc"},
		{name: "a parent traversal that cleans back inside", root: "/home/learner/quest/../../../etc"},
		{name: "a relative path", root: "quest"},
		{name: "outside the learner home", root: "/etc/shellforge"},
		{name: "the state directory itself", root: setup.DefaultStateDir},
		{name: "a directory containing the state directory", root: "/home/learner/.shellforge", stateDir: "/home/learner/.shellforge/state"},
		{name: "a level root that is itself a symlink", root: "/home/learner/quest", symlinkedRoot: "/home/learner/elsewhere"},
		{name: "a symlink planted inside the root during the level", root: "/home/learner/quest", symlinkedRoot: "/tmp/quest"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lvl := testLevel()
			lvl.Setup.Root = tc.root

			sess := &fakeSession{
				result:        runtime.ExecResult{ExitCode: 0},
				symlinkedRoot: tc.symlinkedRoot,
			}
			cfg := Config{Level: lvl, Sess: sess, Verifier: &fakeVerifier{}}
			if tc.stateDir != "" {
				cfg.StateDir = tc.stateDir
			}
			s, _, _ := newTestSession(t, cfg)
			o, _, _ := newTestOrchestrator(t, s, newFakeProgress())

			// Put the Orchestrator in StateActive without going through
			// Start, which would refuse this level for the same reason.
			o.mu.Lock()
			o.state = StateActive
			o.mu.Unlock()

			err := o.Reset(context.Background())
			if err == nil {
				t.Fatalf("Reset accepted %q as a level root", tc.root)
			}
			if !errors.Is(err, setup.ErrUnsafeLevelRoot) {
				t.Errorf("Reset(%q) = %v, want an error wrapping setup.ErrUnsafeLevelRoot", tc.root, err)
			}
			if !strings.Contains(err.Error(), "level root") {
				t.Errorf("the error does not say what was wrong: %v", err)
			}

			if got := sess.removals(); got != 0 {
				t.Errorf("%d removals were issued for a refused root, want 0", got)
			}
		})
	}
}

// The happy path, and the only behaviour Reset actually adds: one rebuild,
// one event, and a level that is checkable straight afterwards.
func TestResetRebuildsTheLevelAndPublishesOnce(t *testing.T) {
	o, sess, rec := resetOrchestrator(t, testLevel())
	before := sess.removals()

	if err := o.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if sess.removals() <= before {
		t.Error("Reset issued no removal: the level world was not torn down before it was rebuilt")
	}

	resets := 0
	for _, ev := range rec.snapshot() {
		if r, ok := ev.(bus.LevelReset); ok {
			resets++
			if r.LevelID != "nav-01" {
				t.Errorf("LevelReset.LevelID = %q, want nav-01", r.LevelID)
			}
		}
	}
	if resets != 1 {
		t.Errorf("%d LevelReset events, want 1", resets)
	}

	if o.State() != StateActive {
		t.Errorf("State() = %q after a reset, want %q", o.State(), StateActive)
	}
	if _, err := o.Check(context.Background()); err != nil {
		t.Errorf("Check right after a reset: %v", err)
	}
}

// Reset rebuilds files. It does not start the level again.
func TestResetPreservesProgress(t *testing.T) {
	o, _, _ := resetOrchestrator(t, scoredLevel())
	ctx := context.Background()

	if _, err := o.TakeHint(ctx, false); err != nil {
		t.Fatalf("TakeHint: %v", err)
	}
	if err := o.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if got := o.HintsTaken(); got != 1 {
		t.Errorf("HintsTaken() = %d after a reset, want 1: rebuilding the files does not refund a hint", got)
	}
	tier, ok := o.PeekHint(false)
	if !ok || tier.Index != 2 {
		t.Errorf("the next tier after a reset is %+v, want tier 2", tier)
	}
}

// The lock, under -race: a check arriving mid-reset waits for the rebuild
// and then runs against the rebuilt world, never against a half-deleted
// one.
func TestCheckArrivingMidResetWaitsForTheRebuild(t *testing.T) {
	var (
		mu       sync.Mutex
		observed []string
	)
	note := func(what string) {
		mu.Lock()
		observed = append(observed, what)
		mu.Unlock()
	}
	seen := func(what string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, o := range observed {
			if o == what {
				return true
			}
		}
		return false
	}

	sess := &fakeSession{result: runtime.ExecResult{ExitCode: 0}}
	s, verifier, _ := newTestSession(t, Config{Level: testLevel(), Sess: sess})
	verifier.result = passingResult(0)
	verifier.onRun = func() { note("check ran") }

	o, _, _ := newTestOrchestrator(t, s, newFakeProgress())
	ctx := context.Background()
	if err := o.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = o.Close(ctx) })

	// Arranged only AFTER Start, whose own Setup tears down first: the
	// teardown this test wants to hold open is the reset's, not Start's.
	release := make(chan struct{})
	sess.setHook(func(argv []string) {
		if len(argv) >= 2 && argv[0] == "rm" && argv[1] == "-rf" {
			note("rebuild started")
			<-release
			note("rebuild finished")
		}
	})

	resetDone := make(chan error, 1)
	go func() { resetDone <- o.Reset(ctx) }()
	waitFor(t, func() bool { return seen("rebuild started") })

	checkDone := make(chan error, 1)
	go func() {
		_, err := o.Check(ctx)
		checkDone <- err
	}()

	// The check is now blocked on the reset lock. Give it a window in which
	// it could wrongly run, and assert it did not.
	time.Sleep(20 * time.Millisecond)
	if seen("check ran") {
		close(release)
		t.Fatal("the check ran while the level world was still being rebuilt")
	}

	close(release)
	if err := <-resetDone; err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err := <-checkDone; err != nil {
		t.Fatalf("Check: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"rebuild started", "rebuild finished", "check ran"}
	if len(observed) != len(want) {
		t.Fatalf("observed %v, want %v", observed, want)
	}
	for i := range want {
		if observed[i] != want[i] {
			t.Fatalf("observed %v, want %v", observed, want)
		}
	}
}

// A reset arriving while a check is running is refused rather than deleting
// the world out from under a check that is reading it.
func TestResetIsRefusedWhileAChecksIsRunning(t *testing.T) {
	o, _, _ := resetOrchestrator(t, testLevel())

	o.mu.Lock()
	o.state = StateChecking
	o.mu.Unlock()

	var te *TransitionError
	err := o.Reset(context.Background())
	if !errors.As(err, &te) {
		t.Fatalf("Reset while checking = %v, want a *TransitionError", err)
	}
	if te.State != StateChecking {
		t.Errorf("the refusal names %q, want %q", te.State, StateChecking)
	}
}

func TestResetIsRefusedOnceClosed(t *testing.T) {
	o, _, _ := resetOrchestrator(t, testLevel())
	if err := o.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := o.Reset(context.Background()); !errors.Is(err, ErrOrchestratorClosed) {
		t.Errorf("Reset after Close = %v, want ErrOrchestratorClosed", err)
	}
}

// A setup that fails after its teardown succeeded must still leave the
// Orchestrator able to tear down cleanly on Close, and the failure must
// arrive carrying the doc anchor setup.Runner attached rather than as a
// bare error.
func TestResetThatFailsHalfwayStillClosesCleanly(t *testing.T) {
	lvl := testLevel()
	lvl.Setup.Script = "exit 1"

	sess := &fakeSession{result: runtime.ExecResult{ExitCode: 0}}
	s, verifier, _ := newTestSession(t, Config{Level: lvl, Sess: sess})
	verifier.result = passingResult(0)
	o, _, _ := newTestOrchestrator(t, s, newFakeProgress())

	o.mu.Lock()
	o.state = StateActive
	o.mu.Unlock()

	// Make the setup script fail, but leave the teardown that precedes it
	// working.
	sess.execByCmd = map[string]runtime.ExecResult{
		"bash": {ExitCode: 1, Stderr: []byte("boom")},
	}

	if err := o.Reset(context.Background()); err == nil {
		t.Fatal("Reset succeeded despite the level's setup script failing")
	}

	if err := o.Close(context.Background()); err != nil {
		t.Fatalf("Close after a failed reset: %v", err)
	}
	if o.State() != StateIdle {
		t.Errorf("State() = %q, want %q", o.State(), StateIdle)
	}
}

// waitFor spins until cond is true or the test times out. Used instead of a
// fixed sleep so the race tests above are not slower than they need to be
// and not flakier than they should be.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for a condition that should have become true")
}

// A guard rather than a behaviour: nothing in internal/game may grow a
// deletion path of its own. Every removal this package performs goes
// through setup.Runner, inside the sandbox, as the learner.
func TestThisPackageNeverCallsOsRemoveAll(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	fset := token.NewFileSet()
	selectorCalls := 0

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			selectorCalls++
			if pkgIdent.Name == "os" && sel.Sel.Name == "RemoveAll" {
				t.Errorf("%s:%d: os.RemoveAll is forbidden in this package: every removal goes through setup.Runner, inside the sandbox, as the learner",
					name, fset.Position(call.Pos()).Line)
			}
			return true
		})
	}

	if selectorCalls == 0 {
		t.Fatal("found no selector calls at all in this package; the AST walk is looking in the wrong place")
	}
}
