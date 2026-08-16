package game

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/content/setup"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// --- fakes ---

// fakeVerifier is a Verifier that records what it was asked and answers with a
// canned result. It is the whole reason Verifier is an interface: a Session can
// be exercised with no registry, no engine and no sandbox.
type fakeVerifier struct {
	buildErr error

	// builtSpecs records the specs Build was given, so a test can assert the
	// conversion reached the engine rather than only that it happened.
	builtSpecs []verify.Spec

	// buildCalls counts Build calls, which is how the build-once property is
	// asserted.
	buildCalls int

	// ranEnv records the Env of the last Run, so a test can assert what the
	// session threaded into it.
	ranEnv   verify.Env
	runCalls int

	result verify.LevelResult
}

func (f *fakeVerifier) Build(specs []verify.Spec) ([]verify.Check, error) {
	f.buildCalls++
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	f.builtSpecs = specs
	checks := make([]verify.Check, 0, len(specs))
	for range specs {
		checks = append(checks, nil)
	}
	return checks, nil
}

func (f *fakeVerifier) Run(_ context.Context, _ []verify.Check, env verify.Env) verify.LevelResult {
	f.runCalls++
	f.ranEnv = env
	return f.result
}

// fakeSession is a runtime.Session that answers every Exec with a fixed result
// and records the argv it was given.
//
// It does not panic on the other methods the way internal/verify's verifytest
// does. A Session here is handed to setup.Runner as well as to the verifier, and
// the runner legitimately calls PushFiles; a fake that panicked on it could not
// be used to test Setup at all.
type fakeSession struct {
	result runtime.ExecResult
	err    error

	calls   [][]string
	pushed  []runtime.FileManifest
	closed  bool
	pushErr error

	// execByCmd overrides the answer for a particular argv[0].
	execByCmd map[string]runtime.ExecResult

	// symlinkedRoot makes readlink report a different path than it was asked
	// about, which is how the runner detects a symlink on the level root.
	symlinkedRoot string
}

func (f *fakeSession) Exec(_ context.Context, argv []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
	f.calls = append(f.calls, argv)
	if f.err != nil {
		return runtime.ExecResult{}, f.err
	}
	if len(argv) > 0 && f.execByCmd != nil {
		if res, ok := f.execByCmd[argv[0]]; ok {
			return res, nil
		}
	}

	// setup.Runner resolves the level root with `readlink -m -- <path>` and
	// refuses to continue unless the answer equals what it asked about, which
	// is how it catches a symlink planted on the path. Echoing the operand back
	// is what a real readlink -m does for a path containing no symlinks, so the
	// fake has to do the same or every Setup fails for the wrong reason.
	if len(argv) > 0 && argv[0] == "readlink" {
		resolved := argv[len(argv)-1]
		if f.symlinkedRoot != "" {
			resolved = f.symlinkedRoot
		}
		return runtime.ExecResult{ExitCode: 0, Stdout: []byte(resolved + "\n")}, nil
	}

	return f.result, nil
}

func (f *fakeSession) Attach(context.Context, runtime.AttachOpts) (runtime.PTY, error) {
	return nil, errors.New("fakeSession: Attach is not implemented; a game.Session never attaches")
}

func (f *fakeSession) PushFiles(_ context.Context, m runtime.FileManifest) error {
	f.pushed = append(f.pushed, m)
	return f.pushErr
}

func (f *fakeSession) PullFile(context.Context, string) ([]byte, error) {
	return nil, errors.New("fakeSession: PullFile is not implemented")
}

func (f *fakeSession) Close() error {
	f.closed = true
	return nil
}

// testLevel is a minimal valid level: one required check, one objective.
func testLevel() *content.Level {
	return &content.Level{
		ID:       "nav-01",
		Title:    "First Contact",
		Briefing: "## 08:15\n\nFind out where you are standing.",
		Setup:    content.Setup{Root: "/home/learner/quest"},
		Objectives: []content.Objective{
			{ID: "location", Text: "quest/answer.txt holds the folder you are standing in"},
		},
		Checks: []content.CheckSpec{
			{ID: "location", Type: "file_content", OnFail: "answer.txt is missing",
				Params: map[string]any{"path": "/home/learner/quest/answer.txt", "match": "trimmed_equals", "value": "/home/learner"}},
		},
		Solution: "pwd > ~/quest/answer.txt",
	}
}

// newTestSession builds a Session over the fakes, failing the test on error.
func newTestSession(t *testing.T, cfg Config) (*Session, *fakeVerifier, *fakeSession) {
	t.Helper()

	if cfg.Level == nil {
		cfg.Level = testLevel()
	}
	sess, _ := cfg.Sess.(*fakeSession)
	if cfg.Sess == nil {
		sess = &fakeSession{result: runtime.ExecResult{ExitCode: 0}}
		cfg.Sess = sess
	}
	verifier, _ := cfg.Verifier.(*fakeVerifier)
	if cfg.Verifier == nil {
		verifier = &fakeVerifier{}
		cfg.Verifier = verifier
	}

	s, err := NewSession(cfg)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return s, verifier, sess
}

// --- NewSession ---

// TestNewSessionBuildsChecksOnce is the build-once property, from the caller's
// side. A learner types `check` repeatedly, and each of those must not re-resolve
// the level's parameters.
func TestNewSessionBuildsChecksOnce(t *testing.T) {
	s, verifier, _ := newTestSession(t, Config{})

	if verifier.buildCalls != 1 {
		t.Fatalf("Build was called %d times during NewSession, want exactly 1", verifier.buildCalls)
	}

	for i := 0; i < 3; i++ {
		if _, err := s.Check(context.Background()); err != nil {
			t.Fatalf("Check: %v", err)
		}
	}

	if verifier.buildCalls != 1 {
		t.Errorf("Build was called %d times after three Check calls, want 1: checks are built at load, not per check", verifier.buildCalls)
	}
	if verifier.runCalls != 3 {
		t.Errorf("Run was called %d times for three Check calls, want 3", verifier.runCalls)
	}
}

// TestNewSessionConvertsChecksBeforeBuilding asserts the conversion actually
// reaches the engine, rather than the engine being handed an empty slice.
func TestNewSessionConvertsChecksBeforeBuilding(t *testing.T) {
	_, verifier, _ := newTestSession(t, Config{})

	if len(verifier.builtSpecs) != 1 {
		t.Fatalf("Build received %d specs, want 1", len(verifier.builtSpecs))
	}
	spec := verifier.builtSpecs[0]
	if spec.ID != "location" {
		t.Errorf("spec.ID = %q, want %q", spec.ID, "location")
	}
	if spec.Text != "quest/answer.txt holds the folder you are standing in" {
		t.Errorf("spec.Text = %q, want the objective text joined in", spec.Text)
	}
	if spec.Type != "file_content" {
		t.Errorf("spec.Type = %q", spec.Type)
	}
}

// TestNewSessionWrapsABuildFailureForALearner is CLAUDE.md non-negotiable 6 at
// this boundary: a malformed level must not reach the terminal as a bare Go
// error.
func TestNewSessionWrapsABuildFailureForALearner(t *testing.T) {
	verifier := &fakeVerifier{buildErr: errors.New(`param "path" must not be empty`)}
	_, err := NewSession(Config{
		Level:    testLevel(),
		Sess:     &fakeSession{},
		Verifier: verifier,
	})
	if err == nil {
		t.Fatal("NewSession succeeded with a Verifier that could not build")
	}

	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("error is not a *ux.Error: %T", err)
	}
	if uxErr.DocAnchor != docAnchorLevelPackInvalid {
		t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, docAnchorLevelPackInvalid)
	}
	if uxErr.Remediation == "" {
		t.Error("no remediation")
	}
	if !strings.Contains(uxErr.Op, "nav-01") {
		t.Errorf("Op %q does not name the level", uxErr.Op)
	}
	if !strings.Contains(err.Error(), "path") {
		t.Errorf("the underlying cause was lost: %v", err)
	}
}

func TestNewSessionRejectsAnIncompleteConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "no level", cfg: Config{Sess: &fakeSession{}, Verifier: &fakeVerifier{}}},
		{name: "no session", cfg: Config{Level: testLevel(), Verifier: &fakeVerifier{}}},
		{name: "no verifier", cfg: Config{Level: testLevel(), Sess: &fakeSession{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewSession(tt.cfg); err == nil {
				t.Error("NewSession accepted an incomplete Config")
			}
		})
	}
}

// --- Check ---

// TestCheckThreadsTheRightEnvironment asserts what a check is allowed to see.
// Getting Snapshots.Dir wrong is the failure mode that makes env-01 unsolvable,
// which internal/verify's doc.go calls out by name.
func TestCheckThreadsTheRightEnvironment(t *testing.T) {
	s, verifier, sess := newTestSession(t, Config{StateDir: "/home/learner/.shellforge"})

	if _, err := s.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	env := verifier.ranEnv
	if env.Session != sess {
		t.Error("Env.Session is not the session the Config supplied")
	}
	if env.Root != "/home/learner/quest" {
		t.Errorf("Env.Root = %q, want the level's setup.root", env.Root)
	}
	if env.LevelID != "nav-01" {
		t.Errorf("Env.LevelID = %q, want %q", env.LevelID, "nav-01")
	}
	if env.Snapshots.Dir != "/home/learner/.shellforge" {
		t.Errorf("Env.Snapshots.Dir = %q, want the state directory", env.Snapshots.Dir)
	}
	if env.Journal == nil {
		t.Fatal("Env.Journal is nil; a journal check would panic inside a check and the learner would see a Go panic")
	}
}

// TestCheckDefaultsToANoOpJournal pins the folding decision from issue #52 and
// the reason it is safe.
func TestCheckDefaultsToANoOpJournal(t *testing.T) {
	s, verifier, _ := newTestSession(t, Config{})

	if _, err := s.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	journal := verifier.ranEnv.Journal
	if journal == nil {
		t.Fatal("Env.Journal is nil rather than a no-op reader")
	}
	for _, scope := range []verify.Scope{
		{Kind: verify.ScopeLevel},
		{Kind: verify.ScopeLast},
		{Kind: verify.ScopeLastN, N: 5},
	} {
		if got := journal.Commands(scope); len(got) != 0 {
			t.Errorf("the default journal reported %d commands for scope %v, want none", len(got), scope)
		}
	}
}

// TestCheckUsesASuppliedJournal proves the seam is real, so wiring a journal in
// later is a caller change and not a change here.
func TestCheckUsesASuppliedJournal(t *testing.T) {
	want := []string{"pwd", "ls -la"}
	s, verifier, _ := newTestSession(t, Config{Journal: fixedJournal{commands: want}})

	if _, err := s.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := verifier.ranEnv.Journal.Commands(verify.Scope{Kind: verify.ScopeLevel}); len(got) != 2 {
		t.Errorf("journal reported %v, want %v", got, want)
	}
}

type fixedJournal struct{ commands []string }

func (f fixedJournal) Commands(verify.Scope) []string { return f.commands }

// TestCheckReturnsTheEngineResultVerbatim is the layer boundary: this package
// orchestrates and does not interpret. A Session that decided which failure to
// show would be making a rendering decision four layers from the terminal.
func TestCheckReturnsTheEngineResultVerbatim(t *testing.T) {
	want := verify.LevelResult{
		LevelID: "nav-01",
		Passed:  false,
		Objectives: []verify.ObjectiveResult{
			{ID: "location", Text: "t", Status: verify.StatusFail, Message: "authored"},
		},
		PrimaryFailure: &verify.ObjectiveResult{ID: "location", Message: "authored"},
		Notes:          []string{"a note"},
		DurationMS:     42,
	}

	s, _, _ := newTestSession(t, Config{Verifier: &fakeVerifier{result: want}})

	got, err := s.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Passed != want.Passed || got.DurationMS != want.DurationMS {
		t.Errorf("result was altered: got %+v", got)
	}
	if len(got.Objectives) != 1 || got.Objectives[0].Message != "authored" {
		t.Errorf("objectives were altered: %+v", got.Objectives)
	}
	if got.PrimaryFailure == nil || got.PrimaryFailure.ID != "location" {
		t.Errorf("PrimaryFailure was altered: %+v", got.PrimaryFailure)
	}
	if len(got.Notes) != 1 {
		t.Errorf("notes were altered: %v", got.Notes)
	}
}

// --- accessors and the state directory ---

func TestStateDirDefaultsToTheRunnerDefault(t *testing.T) {
	s, _, _ := newTestSession(t, Config{})
	if got := s.StateDir(); got != setup.DefaultStateDir {
		t.Errorf("StateDir = %q, want %q", got, setup.DefaultStateDir)
	}
}

func TestStateDirHonoursTheOverride(t *testing.T) {
	s, _, _ := newTestSession(t, Config{StateDir: "/home/learner/.sf-test"})
	if got := s.StateDir(); got != "/home/learner/.sf-test" {
		t.Errorf("StateDir = %q, want the override", got)
	}
}

// TestAccessorsReportTheLevelAsAuthored keeps the briefing unrendered, which is
// the property that lets the CLI decide width and colour.
func TestAccessorsReportTheLevelAsAuthored(t *testing.T) {
	level := testLevel()
	s, _, _ := newTestSession(t, Config{Level: level})

	if s.LevelID() != "nav-01" {
		t.Errorf("LevelID = %q", s.LevelID())
	}
	if s.Level() != level {
		t.Error("Level did not return the level it was given")
	}
	if s.Root() != "/home/learner/quest" {
		t.Errorf("Root = %q", s.Root())
	}
	if s.Briefing() != level.Briefing {
		t.Error("Briefing was altered; it must be the authored markdown")
	}
	if !strings.Contains(s.Briefing(), "##") {
		t.Error("Briefing appears to have been rendered; this layer must hand out markdown")
	}
	if len(s.Objectives()) != 1 || s.Objectives()[0].ID != "location" {
		t.Errorf("Objectives = %+v", s.Objectives())
	}
}

// --- the pack filesystem seam ---

// TestSetupReadsAssetsFromThePackRoot is the seam the plan flagged as latent.
//
// setup.Runner reads a level's assets with fs.ReadFile(packFS, spec.Source) and
// joins no pack root of its own, so packFS has to be rooted AT the pack. The
// shipped packs.FS is rooted one level higher, with paths beginning
// "core-linux-basics/", so a caller passing it directly makes every `source:`
// fail to resolve. No shipped level used `source:` before pipe-05, so nothing
// caught this.
//
// This test asserts the contract from the inside: given a filesystem rooted at
// the pack, a level's `source: assets/app-1.log` resolves.
func TestSetupReadsAssetsFromThePackRoot(t *testing.T) {
	packFS := fstest.MapFS{
		"assets/app-1.log": &fstest.MapFile{Data: []byte("2026-03-14T02:00:05Z api ERROR boom\n")},
	}

	level := testLevel()
	level.Setup.Files = []content.FileSpec{
		{Path: "logs/app-1.log", Source: "assets/app-1.log"},
	}

	sess := &fakeSession{result: runtime.ExecResult{ExitCode: 0}}
	s, _, _ := newTestSession(t, Config{Level: level, Sess: sess, PackFS: packFS})

	if err := s.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// The asset content must have reached a PushFiles manifest, which is the
	// only way it gets into the sandbox.
	var found bool
	for _, manifest := range sess.pushed {
		for _, entry := range manifest.Files {
			if strings.HasSuffix(entry.Path, "logs/app-1.log") && strings.Contains(string(entry.Content), "ERROR boom") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("the level's asset never reached a PushFiles manifest; pushed %d manifests: %+v", len(sess.pushed), sess.pushed)
	}
}

// TestSetupFailsClearlyWhenThePackFSIsRootedWrong is the same seam from the
// other side: it pins what a caller sees when it passes packs.FS rather than a
// sub-filesystem, so the failure is recognisable rather than mysterious.
func TestSetupFailsClearlyWhenThePackFSIsRootedWrong(t *testing.T) {
	// Rooted one level too high, the way packs.FS is.
	packFS := fstest.MapFS{
		"core-linux-basics/assets/app-1.log": &fstest.MapFile{Data: []byte("x\n")},
	}

	level := testLevel()
	level.Setup.Files = []content.FileSpec{
		{Path: "logs/app-1.log", Source: "assets/app-1.log"},
	}

	s, _, _ := newTestSession(t, Config{
		Level: level, Sess: &fakeSession{result: runtime.ExecResult{ExitCode: 0}}, PackFS: packFS,
	})

	err := s.Setup(context.Background())
	if err == nil {
		t.Fatal("Setup succeeded against a pack filesystem rooted above the pack; the asset cannot have been read")
	}
	if !strings.Contains(err.Error(), "assets/app-1.log") {
		t.Errorf("the error does not name the asset that could not be read: %v", err)
	}
}

// TestSetupPassesRunnerErrorsThroughUntouched is the anchor-preservation rule.
//
// setup.Runner attaches specific doc anchors: unsafe-level-root,
// setup-script-failed, level-pack-invalid. Re-wrapping them with one generic
// anchor sends the learner to the wrong page, which is worse than no link.
func TestSetupPassesRunnerErrorsThroughUntouched(t *testing.T) {
	level := testLevel()
	// A root outside /home/learner/ is refused by the runner with
	// unsafe-level-root, before it touches the sandbox.
	level.Setup.Root = "/etc"

	s, _, _ := newTestSession(t, Config{Level: level})

	err := s.Setup(context.Background())
	if err == nil {
		t.Fatal("Setup accepted a level root outside /home/learner/")
	}

	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("error is not a *ux.Error: %T", err)
	}
	if uxErr.DocAnchor != "unsafe-level-root" {
		t.Errorf("DocAnchor = %q, want %q: this method must not replace the runner's own anchor with a vaguer one",
			uxErr.DocAnchor, "unsafe-level-root")
	}
	if !errors.Is(err, setup.ErrUnsafeLevelRoot) {
		t.Error("the runner's sentinel error was lost, so a caller cannot match on it")
	}
}

// TestSetupRefusesASymlinkedLevelRoot is the containment case that matters most,
// asserted through this layer because this layer is what a level's Setup reaches
// the sandbox through.
//
// Teardown ends in a recursive delete of the level root. If a learner replaced
// ~/quest with a symlink to somewhere else in their sandbox home, a resolver that
// followed it would delete the target instead. setup.Runner catches that by
// asking readlink what the path really is and refusing when the answer differs;
// this test proves the refusal survives being called from here, and that the
// specific anchor reaches the caller.
func TestSetupRefusesASymlinkedLevelRoot(t *testing.T) {
	sess := &fakeSession{
		result:        runtime.ExecResult{ExitCode: 0},
		symlinkedRoot: "/home/learner/somewhere-else",
	}
	s, _, _ := newTestSession(t, Config{Sess: sess})

	err := s.Setup(context.Background())
	if err == nil {
		t.Fatal("Setup accepted a level root that resolves somewhere else; a teardown would then delete the wrong tree")
	}
	if !errors.Is(err, setup.ErrUnsafeLevelRoot) {
		t.Errorf("error does not wrap ErrUnsafeLevelRoot: %v", err)
	}

	var uxErr *ux.Error
	if !errors.As(err, &uxErr) || uxErr.DocAnchor != "unsafe-level-root" {
		t.Errorf("anchor = %+v, want unsafe-level-root", uxErr)
	}

	// And nothing was staged into the sandbox before the refusal.
	if len(sess.pushed) != 0 {
		t.Errorf("Setup pushed %d manifests before refusing the root", len(sess.pushed))
	}
}

// TestTeardownIsSafeBeforeSetup is what makes the CLI's defer-before-Setup
// pattern correct: a Setup that failed halfway, or never ran, must still clean
// up without erroring on the absence.
func TestTeardownIsSafeBeforeSetup(t *testing.T) {
	s, _, _ := newTestSession(t, Config{})

	if err := s.Teardown(context.Background()); err != nil {
		t.Errorf("Teardown before Setup: %v", err)
	}
	if err := s.Teardown(context.Background()); err != nil {
		t.Errorf("Teardown a second time: %v", err)
	}
}

// TestSessionNeverClosesTheSessionItWasGiven pins the ownership rule. One
// sandbox session can outlive one level, so a Session that closed it would end
// the learner's whole run when a level finished.
func TestSessionNeverClosesTheSessionItWasGiven(t *testing.T) {
	sess := &fakeSession{result: runtime.ExecResult{ExitCode: 0}}
	s, _, _ := newTestSession(t, Config{Sess: sess})

	if err := s.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := s.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := s.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	if sess.closed {
		t.Error("the Session closed the runtime session it borrowed; the caller owns it")
	}
}

// TestSessionNeverAttaches keeps the interactive shell out of this layer. Attach
// puts the host terminal into raw mode and belongs to the CLI.
func TestSessionNeverAttaches(t *testing.T) {
	sess := &fakeSession{result: runtime.ExecResult{ExitCode: 0}}
	s, _, _ := newTestSession(t, Config{Sess: sess})

	// fakeSession.Attach returns an error rather than panicking, so this
	// asserts on the calls actually made instead of relying on a panic.
	if err := s.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := s.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	for _, argv := range sess.calls {
		if len(argv) > 0 && argv[0] == "bash" && len(argv) > 1 && argv[1] == "-i" {
			t.Errorf("this layer started an interactive shell: %q", argv)
		}
	}
}

// TestPackFSMayBeNilForALevelWithNoAssets keeps a nil pack filesystem from being
// an error for levels that never read one. Seven of the eight shipped levels use
// inline content or a generator.
func TestPackFSMayBeNilForALevelWithNoAssets(t *testing.T) {
	var nilFS fs.FS
	s, _, _ := newTestSession(t, Config{PackFS: nilFS})

	if err := s.Setup(context.Background()); err != nil {
		t.Errorf("Setup with a nil PackFS and a level with no source: assets: %v", err)
	}
}
