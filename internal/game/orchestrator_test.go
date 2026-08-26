package game

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/store"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// --- fakes and helpers local to the orchestrator ---

// fakeProgress is a Progress that records what it was asked and answers
// with canned, and internally consistent, results. StartAttempt increments
// levelState.Attempts itself, the way store.Store's real StartAttempt does
// in the same transaction it writes, so a test can seed a prior attempt
// count and see Start's read-back reflect the new one without hand
// simulating the store's own bookkeeping twice.
type fakeProgress struct {
	mu sync.Mutex

	startAttemptCalls int
	lastProfileID     int64
	lastPackID        string
	lastLevelID       string
	lastLevelVersion  int
	startAttemptErr   error
	nextAttemptID     int64

	finishAttemptCalls int
	finishAttemptID    int64
	finishAttemptArg   store.Attempt
	finishAttemptErr   error

	setLevelStatusCalls int
	setLevelStatusErr   error

	levelState    store.LevelState
	levelStateOK  bool
	levelStateErr error
}

// newFakeProgress returns a fakeProgress whose LevelState reads back ok, as
// the real store does once a row exists, so a test only has to override
// what it cares about.
func newFakeProgress() *fakeProgress {
	return &fakeProgress{levelStateOK: true}
}

func (p *fakeProgress) StartAttempt(_ context.Context, profileID int64, packID, levelID string, levelVersion int, _ time.Time) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.startAttemptCalls++
	p.lastProfileID = profileID
	p.lastPackID = packID
	p.lastLevelID = levelID
	p.lastLevelVersion = levelVersion
	if p.startAttemptErr != nil {
		return 0, p.startAttemptErr
	}
	p.nextAttemptID++
	p.levelState.Attempts++
	return p.nextAttemptID, nil
}

func (p *fakeProgress) FinishAttempt(_ context.Context, attemptID int64, a store.Attempt) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finishAttemptCalls++
	p.finishAttemptID = attemptID
	p.finishAttemptArg = a
	return p.finishAttemptErr
}

func (p *fakeProgress) SetLevelStatus(_ context.Context, _ int64, _, _ string, _ int, _ store.LevelStatus) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setLevelStatusCalls++
	return p.setLevelStatusErr
}

func (p *fakeProgress) LevelState(_ context.Context, _ int64, _ string, _ int) (store.LevelState, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.levelStateErr != nil {
		return store.LevelState{}, false, p.levelStateErr
	}
	return p.levelState, p.levelStateOK, nil
}

// recordingBus subscribes to a *bus.Bus and records every event delivered
// to it, in publish order, so a test can inspect what actually went out
// without writing a one-off Handler each time.
type recordingBus struct {
	mu     sync.Mutex
	events []bus.Event
}

// newRecordingBus returns a fresh *bus.Bus with a recordingBus already
// subscribed to it.
func newRecordingBus() (*bus.Bus, *recordingBus) {
	b := bus.New()
	rec := &recordingBus{}
	b.Subscribe("orchestrator_test", func(_ context.Context, ev bus.Event) {
		rec.mu.Lock()
		rec.events = append(rec.events, ev)
		rec.mu.Unlock()
	})
	return b, rec
}

func (r *recordingBus) snapshot() []bus.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]bus.Event, len(r.events))
	copy(out, r.events)
	return out
}

func (r *recordingBus) countKind(k bus.Kind) int {
	n := 0
	for _, ev := range r.snapshot() {
		if ev.Kind() == k {
			n++
		}
	}
	return n
}

// newTestOrchestrator builds an Orchestrator over a *Session already built
// from fakeSession and fakeVerifier (session_test.go's own helpers), plus a
// fakeProgress and a recordingBus this test can inspect.
func newTestOrchestrator(t *testing.T, s *Session, progress *fakeProgress) (*Orchestrator, *bus.Bus, *recordingBus) {
	t.Helper()

	b, rec := newRecordingBus()
	o, err := NewOrchestrator(OrchestratorConfig{
		Session:   s,
		Bus:       b,
		Progress:  progress,
		ProfileID: 1,
		PackID:    "core-linux-basics",
	})
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	return o, b, rec
}

// --- NewOrchestrator ---

func TestNewOrchestratorRejectsAnIncompleteConfig(t *testing.T) {
	s, _, _ := newTestSession(t, Config{})
	b, _ := newRecordingBus()
	progress := newFakeProgress()

	tests := []struct {
		name string
		cfg  OrchestratorConfig
	}{
		{name: "no session", cfg: OrchestratorConfig{Bus: b, Progress: progress}},
		{name: "no bus", cfg: OrchestratorConfig{Session: s, Progress: progress}},
		{name: "no progress", cfg: OrchestratorConfig{Session: s, Bus: b}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewOrchestrator(tt.cfg); err == nil {
				t.Error("NewOrchestrator accepted an incomplete config")
			}
		})
	}
}

// --- AC1 ---

// TestIllegalTransitionRefusesAndChangesNothing pins the refusal shape and
// proves a refused call is a true no-op: nothing opened, nothing published,
// nothing moved.
func TestIllegalTransitionRefusesAndChangesNothing(t *testing.T) {
	s, _, _ := newTestSession(t, Config{})
	progress := newFakeProgress()
	o, _, rec := newTestOrchestrator(t, s, progress)

	_, err := o.Check(context.Background())
	if err == nil {
		t.Fatal("Check succeeded while the orchestrator was Idle")
	}

	var transErr *TransitionError
	if !errors.As(err, &transErr) {
		t.Fatalf("error is not a *TransitionError: %v", err)
	}
	if transErr.Op != "check" || transErr.State != StateIdle {
		t.Errorf("TransitionError = %+v, want Op %q State %q", transErr, "check", StateIdle)
	}

	if progress.startAttemptCalls != 0 {
		t.Errorf("StartAttempt was called %d times by a refused Check", progress.startAttemptCalls)
	}
	if progress.finishAttemptCalls != 0 {
		t.Errorf("FinishAttempt was called %d times by a refused Check", progress.finishAttemptCalls)
	}
	if len(rec.snapshot()) != 0 {
		t.Errorf("a refused Check published %d events, want 0", len(rec.snapshot()))
	}
	if o.State() != StateIdle {
		t.Errorf("State() = %q after a refused Check, want %q", o.State(), StateIdle)
	}
}

// --- AC2 ---

// TestCloseTearsDownExactlyOnce is the refusal-path reuse test from section
// 5 of the plan as well as AC2: Close and Start reach the sandbox only
// through s.Session.Teardown and s.Session.Setup on the concrete *Session
// the Orchestrator holds, so there is no seam for an alternate destructive
// path, and this asserts the one path runs exactly once no matter how Close
// is provoked.
func TestCloseTearsDownExactlyOnce(t *testing.T) {
	t.Run("after success", func(t *testing.T) {
		s, _, sess := newTestSession(t, Config{})
		progress := newFakeProgress()
		o, _, _ := newTestOrchestrator(t, s, progress)

		if err := o.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		// Session.Setup tears down first, unconditionally, as its own
		// doc comment says: that is one teardown-shaped Exec call before
		// Close ever runs. Snapshot here so the assertion below is about
		// what Close itself did, not what Setup already had to do.
		sess.mu.Lock()
		before := sess.teardownCalls
		sess.mu.Unlock()

		if err := o.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		sess.mu.Lock()
		got := sess.teardownCalls - before
		sess.mu.Unlock()
		if got != 1 {
			t.Errorf("Close performed %d teardowns, want 1", got)
		}
	})

	t.Run("after failed start", func(t *testing.T) {
		s, _, sess := newTestSession(t, Config{})
		sess.execByCmd = map[string]runtime.ExecResult{
			"mkdir": {ExitCode: 1, Stderr: []byte("permission denied")},
		}
		progress := newFakeProgress()
		o, _, _ := newTestOrchestrator(t, s, progress)

		if err := o.Start(context.Background()); err == nil {
			t.Fatal("Start succeeded despite mkdir failing inside Setup")
		}
		sess.mu.Lock()
		before := sess.teardownCalls
		sess.mu.Unlock()

		if err := o.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		sess.mu.Lock()
		got := sess.teardownCalls - before
		sess.mu.Unlock()
		if got != 1 {
			t.Errorf("Close performed %d teardowns after a failed Start, want 1", got)
		}
		if progress.finishAttemptCalls != 0 {
			t.Errorf("FinishAttempt was called %d times for an attempt Start never opened", progress.finishAttemptCalls)
		}
	})

	t.Run("after cancelled context", func(t *testing.T) {
		s, _, sess := newTestSession(t, Config{})
		progress := newFakeProgress()
		o, _, _ := newTestOrchestrator(t, s, progress)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Close does not honour cancellation by skipping its own work: a
		// cancelled context is a signal to the commands it runs, not a
		// reason to leave the sandbox half torn down. The fake session
		// answers every Exec regardless of ctx, so this asserts Close still
		// completes and still tears down exactly once.
		if err := o.Close(ctx); err != nil {
			t.Fatalf("Close with a cancelled context: %v", err)
		}
		sess.mu.Lock()
		got := sess.teardownCalls
		sess.mu.Unlock()
		if got != 1 {
			t.Errorf("Close performed %d teardowns with a cancelled context, want 1", got)
		}
	})

	t.Run("after subscriber panic", func(t *testing.T) {
		s, _, sess := newTestSession(t, Config{})
		progress := newFakeProgress()
		b := bus.New(bus.WithErrorHandler(func(string, any, []byte) {}))
		b.Subscribe("panics", func(context.Context, bus.Event) {
			panic("a subscriber that panics must not stop Close")
		})
		o, err := NewOrchestrator(OrchestratorConfig{
			Session: s, Bus: b, Progress: progress, ProfileID: 1, PackID: "core-linux-basics",
		})
		if err != nil {
			t.Fatalf("NewOrchestrator: %v", err)
		}

		if err := o.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		sess.mu.Lock()
		before := sess.teardownCalls
		sess.mu.Unlock()

		if err := o.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		sess.mu.Lock()
		got := sess.teardownCalls - before
		sess.mu.Unlock()
		if got != 1 {
			t.Errorf("Close performed %d teardowns despite a panicking subscriber, want 1", got)
		}
	})

	t.Run("double close", func(t *testing.T) {
		s, _, sess := newTestSession(t, Config{})
		progress := newFakeProgress()
		o, _, _ := newTestOrchestrator(t, s, progress)

		if err := o.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		sess.mu.Lock()
		before := sess.teardownCalls
		sess.mu.Unlock()

		if err := o.Close(context.Background()); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := o.Close(context.Background()); err != nil {
			t.Errorf("second Close = %v, want nil", err)
		}

		sess.mu.Lock()
		got := sess.teardownCalls - before
		sess.mu.Unlock()
		if got != 1 {
			t.Errorf("two Close calls performed %d teardowns, want 1", got)
		}
		if progress.finishAttemptCalls != 1 {
			t.Errorf("two Close calls ran FinishAttempt %d times, want 1", progress.finishAttemptCalls)
		}
	})
}

// --- AC3 ---

func TestStartOpensOneAttemptAndMarksInProgress(t *testing.T) {
	s, _, _ := newTestSession(t, Config{})
	progress := newFakeProgress()
	o, _, _ := newTestOrchestrator(t, s, progress)

	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if progress.startAttemptCalls != 1 {
		t.Fatalf("StartAttempt was called %d times, want 1", progress.startAttemptCalls)
	}
	if progress.lastProfileID != 1 {
		t.Errorf("StartAttempt profileID = %d, want 1", progress.lastProfileID)
	}
	if progress.lastPackID != "core-linux-basics" {
		t.Errorf("StartAttempt packID = %q, want %q", progress.lastPackID, "core-linux-basics")
	}
	if progress.lastLevelID != "nav-01" {
		t.Errorf("StartAttempt levelID = %q, want %q", progress.lastLevelID, "nav-01")
	}
}

func TestCloseOnUnpassedAttemptFinishesAbandoned(t *testing.T) {
	s, _, _ := newTestSession(t, Config{})
	progress := newFakeProgress()
	o, _, _ := newTestOrchestrator(t, s, progress)

	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := o.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if progress.finishAttemptCalls != 1 {
		t.Fatalf("FinishAttempt was called %d times, want 1", progress.finishAttemptCalls)
	}
	if progress.finishAttemptArg.Outcome != store.OutcomeAbandoned {
		t.Errorf("Outcome = %q, want %q", progress.finishAttemptArg.Outcome, store.OutcomeAbandoned)
	}
}

// --- AC4 ---

// TestFirstCheckPassEmitsLevelPassedOnce checks a fully passing level three
// times running and asserts LevelPassed fires only for the first.
func TestFirstCheckPassEmitsLevelPassedOnce(t *testing.T) {
	passing := verify.LevelResult{
		LevelID: "nav-01",
		Passed:  true,
		Objectives: []verify.ObjectiveResult{
			{ID: "location", Status: verify.StatusPass},
		},
	}
	s, verifier, _ := newTestSession(t, Config{})
	verifier.result = passing
	progress := newFakeProgress()
	o, _, rec := newTestOrchestrator(t, s, progress)

	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := o.Check(context.Background()); err != nil {
			t.Fatalf("Check %d: %v", i, err)
		}
		if !o.Passed() {
			t.Errorf("Passed() = false after check %d, want true", i)
		}
	}

	if got := rec.countKind(bus.KindLevelPassed); got != 1 {
		t.Errorf("LevelPassed published %d times over three passing checks, want 1", got)
	}
	if got := rec.countKind(bus.KindCheckRun); got != 3 {
		t.Errorf("CheckRun published %d times over three checks, want 3", got)
	}
	if progress.setLevelStatusCalls != 1 {
		t.Errorf("SetLevelStatus was called %d times over three passing checks, want 1", progress.setLevelStatusCalls)
	}

	var passedEvent *bus.LevelPassed
	for _, ev := range rec.snapshot() {
		if lp, ok := ev.(bus.LevelPassed); ok {
			passedEvent = &lp
		}
	}
	if passedEvent == nil {
		t.Fatal("no LevelPassed was published")
	}
	if !passedEvent.FirstTry {
		t.Error("LevelPassed.FirstTry = false for a level passed on the first check")
	}
}

// TestLevelPassedIsNotFirstTryAfterAFailedCheck is the other half of
// FirstTry: it is computed from this attempt's check count, so a pass that
// arrives on the second check is not a first try.
func TestLevelPassedIsNotFirstTryAfterAFailedCheck(t *testing.T) {
	s, verifier, _ := newTestSession(t, Config{})
	verifier.result = verify.LevelResult{
		LevelID: "nav-01",
		Passed:  false,
		Objectives: []verify.ObjectiveResult{
			{ID: "location", Status: verify.StatusFail},
		},
	}
	progress := newFakeProgress()
	o, _, rec := newTestOrchestrator(t, s, progress)

	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := o.Check(context.Background()); err != nil {
		t.Fatalf("first Check: %v", err)
	}
	if o.Passed() {
		t.Fatal("Passed() = true after a failing check")
	}

	verifier.result = verify.LevelResult{
		LevelID: "nav-01",
		Passed:  true,
		Objectives: []verify.ObjectiveResult{
			{ID: "location", Status: verify.StatusPass},
		},
	}
	if _, err := o.Check(context.Background()); err != nil {
		t.Fatalf("second Check: %v", err)
	}

	var passedEvent *bus.LevelPassed
	for _, ev := range rec.snapshot() {
		if lp, ok := ev.(bus.LevelPassed); ok {
			passedEvent = &lp
		}
	}
	if passedEvent == nil {
		t.Fatal("no LevelPassed was published for a level passed on the second check")
	}
	if passedEvent.FirstTry {
		t.Error("LevelPassed.FirstTry = true for a level that failed its first check")
	}
	if progress.setLevelStatusCalls != 1 {
		t.Errorf("SetLevelStatus was called %d times, want 1", progress.setLevelStatusCalls)
	}
}

// --- AC5 ---

// TestLevelStartedReportsAttemptCountFromStore pins the read-back
// contract: LevelStarted.Attempt comes from Progress.LevelState after
// StartAttempt, not from a count this package keeps of its own.
func TestLevelStartedReportsAttemptCountFromStore(t *testing.T) {
	s, _, _ := newTestSession(t, Config{})
	progress := newFakeProgress()
	progress.levelState.Attempts = 2 // as if two attempts already happened

	o, _, rec := newTestOrchestrator(t, s, progress)

	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var found *bus.LevelStarted
	for _, ev := range rec.snapshot() {
		if ls, ok := ev.(bus.LevelStarted); ok {
			found = &ls
		}
	}
	if found == nil {
		t.Fatal("no LevelStarted was published")
	}
	if found.Attempt != 3 {
		t.Errorf("LevelStarted.Attempt = %d, want 3", found.Attempt)
	}
}

// --- AC6 ---

// TestIllegalOrderRefusesAcrossAllMethods is table-driven over every
// declared state and every exported mutating method, excluding the one
// legal (state, method) pair each method has (Idle for Start, Active for
// Check). Every other pair must refuse with a *TransitionError naming the
// state and the op, and must not open an attempt, close one, or publish
// anything.
func TestIllegalOrderRefusesAcrossAllMethods(t *testing.T) {
	states := []State{
		StateIdle, StateProvisioning, StateSetup, StateBriefing, StateActive,
		StateChecking, StateHinting, StatePassed, StateFailed, StateTeardown,
	}

	for _, state := range states {
		for _, method := range []string{"start", "check"} {
			if state == StateIdle && method == "start" {
				continue // the one legal pair for Start; covered elsewhere
			}
			if state == StateActive && method == "check" {
				continue // the one legal pair for Check; covered elsewhere
			}

			t.Run(string(state)+"/"+method, func(t *testing.T) {
				s, _, _ := newTestSession(t, Config{})
				progress := newFakeProgress()
				o, _, rec := newTestOrchestrator(t, s, progress)

				o.mu.Lock()
				o.state = state
				o.mu.Unlock()

				var err error
				switch method {
				case "start":
					err = o.Start(context.Background())
				case "check":
					_, err = o.Check(context.Background())
				}

				if err == nil {
					t.Fatalf("%s succeeded while the orchestrator was %s", method, state)
				}
				var transErr *TransitionError
				if !errors.As(err, &transErr) {
					t.Fatalf("error is not a *TransitionError: %v", err)
				}
				if transErr.Op != method || transErr.State != state {
					t.Errorf("TransitionError = %+v, want Op %q State %q", transErr, method, state)
				}
				if progress.startAttemptCalls != 0 || progress.finishAttemptCalls != 0 {
					t.Errorf("a refused %s touched Progress: start=%d finish=%d", method, progress.startAttemptCalls, progress.finishAttemptCalls)
				}
				if len(rec.snapshot()) != 0 {
					t.Errorf("a refused %s published %d events", method, len(rec.snapshot()))
				}
				if o.State() != state {
					t.Errorf("State() = %q after a refused %s, want unchanged %q", o.State(), method, state)
				}
			})
		}
	}
}

// --- AC7 ---

// blockingCheckVerifier is a Verifier whose Run calls Exec on the Env's
// Session before returning a canned result, so a test can hold a Check call
// open, unlocked, by blocking that Exec call. It exists only for
// TestCheckAndCloseRace.
type blockingCheckVerifier struct {
	result verify.LevelResult
}

func (v *blockingCheckVerifier) Build(specs []verify.Spec) ([]verify.Check, error) {
	return make([]verify.Check, len(specs)), nil
}

func (v *blockingCheckVerifier) Run(ctx context.Context, _ []verify.Check, env verify.Env) verify.LevelResult {
	_, _ = env.Session.Exec(ctx, []string{"check-command-marker"}, runtime.ExecOpts{})
	return v.result
}

// TestCheckAndCloseRace is AC7: a Check whose sandbox call is still running
// races a concurrent Close. Run under go test -race, it must never panic,
// Close must tear down exactly once, and Check must return either a real
// LevelResult or ErrOrchestratorClosed, never anything torn or
// inconsistent.
func TestCheckAndCloseRace(t *testing.T) {
	sess := &fakeSession{result: runtime.ExecResult{ExitCode: 0}}
	gate := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	sess.execHook = func(argv []string) {
		if len(argv) > 0 && argv[0] == "check-command-marker" {
			startOnce.Do(func() { close(started) })
			<-gate
		}
	}

	verifier := &blockingCheckVerifier{result: verify.LevelResult{
		LevelID: "nav-01",
		Passed:  true,
		Objectives: []verify.ObjectiveResult{
			{ID: "location", Status: verify.StatusPass},
		},
	}}

	s, err := NewSession(Config{Level: testLevel(), Sess: sess, Verifier: verifier})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	progress := newFakeProgress()
	o, _, _ := newTestOrchestrator(t, s, progress)

	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sess.mu.Lock()
	before := sess.teardownCalls
	sess.mu.Unlock()

	var checkErr error
	var checkResult verify.LevelResult
	checkDone := make(chan struct{})
	go func() {
		checkResult, checkErr = o.Check(context.Background())
		close(checkDone)
	}()

	<-started // Check is now blocked mid-flight, holding no lock.

	var closeErr error
	closeDone := make(chan struct{})
	go func() {
		closeErr = o.Close(context.Background())
		close(closeDone)
	}()
	close(gate) // let the blocked Check proceed; now it genuinely races Close.

	<-checkDone
	<-closeDone

	if closeErr != nil {
		t.Errorf("Close: %v", closeErr)
	}

	switch {
	case checkErr == nil:
		if !checkResult.Passed {
			t.Errorf("Check returned a non-passing result with no error: %+v", checkResult)
		}
	case errors.Is(checkErr, ErrOrchestratorClosed):
		if checkResult.LevelID != "" {
			t.Errorf("Check returned a non-zero result alongside ErrOrchestratorClosed: %+v", checkResult)
		}
	default:
		t.Errorf("Check returned an unexpected error: %v", checkErr)
	}

	sess.mu.Lock()
	got := sess.teardownCalls - before
	sess.mu.Unlock()
	if got != 1 {
		t.Errorf("Close performed %d teardowns racing a Check, want 1", got)
	}
	if o.State() != StateIdle {
		t.Errorf("State() = %q after the race settled, want %q", o.State(), StateIdle)
	}
}

// --- regressions found in review ---

// TestCloseFinishesAnAttemptStartCouldNotComplete covers the window between
// Progress.StartAttempt succeeding and the rest of Start succeeding. The row
// exists from the moment StartAttempt returns, and level_state is already
// marked in_progress, so a Start that fails after that point still has to
// leave Close able to close the row out. Without the attempt id recorded
// straight away, Close skips FinishAttempt, ended_at stays NULL forever and
// the level is stuck in_progress with no legal way back.
func TestCloseFinishesAnAttemptStartCouldNotComplete(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*fakeProgress)
	}{
		{
			name:    "the level state read fails",
			arrange: func(p *fakeProgress) { p.levelStateErr = errors.New("database is locked") },
		},
		{
			name:    "the level state row is missing",
			arrange: func(p *fakeProgress) { p.levelStateOK = false },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newTestSession(t, Config{})
			progress := newFakeProgress()
			tt.arrange(progress)
			o, _, _ := newTestOrchestrator(t, s, progress)

			if err := o.Start(context.Background()); err == nil {
				t.Fatal("Start succeeded despite the level state read failing right after StartAttempt")
			}
			if progress.startAttemptCalls != 1 {
				t.Fatalf("StartAttempt was called %d times, want 1: this test is about the window after it has already succeeded", progress.startAttemptCalls)
			}
			if o.State() != StateFailed {
				t.Errorf("State() = %q after a failed Start, want %q", o.State(), StateFailed)
			}

			if err := o.Close(context.Background()); err != nil {
				t.Fatalf("Close: %v", err)
			}

			if progress.finishAttemptCalls != 1 {
				t.Fatalf("FinishAttempt was called %d times, want 1: StartAttempt opened a row and nothing else will ever close it", progress.finishAttemptCalls)
			}
			if progress.finishAttemptID != 1 {
				t.Errorf("FinishAttempt closed attempt %d, want the one StartAttempt opened", progress.finishAttemptID)
			}
			if progress.finishAttemptArg.Outcome != store.OutcomeAbandoned {
				t.Errorf("Outcome = %q, want %q", progress.finishAttemptArg.Outcome, store.OutcomeAbandoned)
			}
		})
	}
}

// waitForClosed blocks until Close has marked o closed, so that a test
// racing Close against another call can be certain the two genuinely
// overlap rather than running one after the other. It polls, because closed
// is guarded by o.mu and nothing signals it; the deadline exists only so
// that a regression fails this test instead of hanging the suite.
func waitForClosed(t *testing.T, o *Orchestrator) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		o.mu.Lock()
		closed := o.closed
		o.mu.Unlock()
		if closed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Close did not mark the orchestrator closed within 5s")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestStartAndCloseRace is the Ctrl-C-during-Start case: a Session.Setup
// still materializing the level's world races a concurrent Close.
//
// Setup writes to the sandbox, unlike Check, which only reads it, so the
// order matters here in a way it does not for TestCheckAndCloseRace. If
// Close tears down first and Setup finishes afterward, the learner is left
// with a level world that nothing will ever remove: Close has already
// returned and told its caller the world is gone, and a second Close is a
// no-op. So the assertion is about ordering, not just about counts: the last
// thing the sandbox sees must be the teardown's recursive delete of the
// level root.
func TestStartAndCloseRace(t *testing.T) {
	sess := &fakeSession{result: runtime.ExecResult{ExitCode: 0}}
	gate := make(chan struct{})
	inSetup := make(chan struct{})
	var once sync.Once
	sess.execHook = func(argv []string) {
		// The level root's own mkdir, which Setup runs after its unconditional
		// teardown and before it writes anything. Blocking here holds Setup
		// open mid-flight, exactly where an interrupted run would sit.
		if len(argv) > 0 && argv[0] == "mkdir" {
			once.Do(func() {
				close(inSetup)
				<-gate
			})
		}
	}

	s, err := NewSession(Config{Level: testLevel(), Sess: sess, Verifier: &fakeVerifier{}})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	progress := newFakeProgress()
	o, _, _ := newTestOrchestrator(t, s, progress)

	var startErr error
	startDone := make(chan struct{})
	go func() {
		startErr = o.Start(context.Background())
		close(startDone)
	}()

	<-inSetup // Setup is now blocked mid-flight, holding no lock.

	var closeErr error
	closeDone := make(chan struct{})
	go func() {
		closeErr = o.Close(context.Background())
		close(closeDone)
	}()

	waitForClosed(t, o) // Close is committed and past the point of no return.
	close(gate)         // Only now may Setup finish, so the two genuinely overlap.

	<-startDone
	<-closeDone

	if closeErr != nil {
		t.Errorf("Close: %v", closeErr)
	}
	if startErr != nil && !errors.Is(startErr, ErrOrchestratorClosed) {
		t.Errorf("Start returned an unexpected error: %v", startErr)
	}

	sess.mu.Lock()
	calls := make([][]string, len(sess.calls))
	copy(calls, sess.calls)
	teardowns := sess.teardownCalls
	sess.mu.Unlock()

	if len(calls) == 0 {
		t.Fatal("the sandbox saw no calls at all")
	}
	last := calls[len(calls)-1]
	if len(last) != 4 || last[0] != "rm" || last[1] != "-rf" || last[3] != "/home/learner/quest" {
		t.Errorf("the last call the sandbox saw was %q, want the teardown's recursive delete of the level root: "+
			"a Setup that finishes after Close returned leaves a level world nothing will ever remove", last)
	}
	if teardowns != 2 {
		t.Errorf("the sandbox saw %d recursive deletes, want 2: the one Setup always does first, and Close's", teardowns)
	}
	if o.State() != StateIdle {
		t.Errorf("State() = %q after the race settled, want %q", o.State(), StateIdle)
	}
	if progress.startAttemptCalls != progress.finishAttemptCalls {
		t.Errorf("Start opened %d attempts and Close finished %d: an attempt row was left open",
			progress.startAttemptCalls, progress.finishAttemptCalls)
	}
}

// mustNotHang runs fn on its own goroutine and fails the test if it has not
// returned within d. A deadlock inside the Orchestrator has to fail a test,
// not hang the whole suite until the package timeout fires.
func mustNotHang(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %s: an Orchestrator call is deadlocked", what, d)
	}
}

// TestSubscriberMayCallBackIntoTheOrchestrator is the re-entrancy contract.
// bus.Bus.Publish runs every subscriber synchronously, in the publishing
// goroutine, so an Orchestrator that published while holding its own mutex
// would deadlock against itself the first time a subscriber asked it a
// question, and every later call on it would then hang too. The achievement,
// hint and scoring subscribers are all coming, so this is pinned now.
func TestSubscriberMayCallBackIntoTheOrchestrator(t *testing.T) {
	passing := verify.LevelResult{
		LevelID: "nav-01",
		Passed:  true,
		Objectives: []verify.ObjectiveResult{
			{ID: "location", Status: verify.StatusPass},
		},
	}

	t.Run("a handler reads and is refused a re-entrant check", func(t *testing.T) {
		s, verifier, _ := newTestSession(t, Config{})
		verifier.result = passing
		progress := newFakeProgress()

		var o *Orchestrator
		handlerErrs := make(chan error, 16)
		b := bus.New()
		b.Subscribe("calls-back", func(ctx context.Context, ev bus.Event) {
			if got := o.State(); got == "" {
				handlerErrs <- errors.New("State() reported an empty state from inside a handler")
			}
			o.Passed()
			if ev.Kind() != bus.KindCheckRun {
				return
			}
			// A mutating verb, from inside a handler, on the Orchestrator
			// that is publishing. It must be refused promptly rather than
			// block: the check that published this event is still running.
			if _, err := o.Check(ctx); err == nil {
				handlerErrs <- errors.New("a re-entrant Check was accepted while a check was already running")
			} else {
				var transErr *TransitionError
				if !errors.As(err, &transErr) {
					handlerErrs <- fmt.Errorf("a re-entrant Check returned %v, want a *TransitionError", err)
				}
			}
		})

		var err error
		o, err = NewOrchestrator(OrchestratorConfig{
			Session: s, Bus: b, Progress: progress, ProfileID: 1, PackID: "core-linux-basics",
		})
		if err != nil {
			t.Fatalf("NewOrchestrator: %v", err)
		}

		var startErr, checkErr, closeErr error
		mustNotHang(t, 5*time.Second, "Start", func() { startErr = o.Start(context.Background()) })
		mustNotHang(t, 5*time.Second, "Check", func() { _, checkErr = o.Check(context.Background()) })
		mustNotHang(t, 5*time.Second, "Close", func() { closeErr = o.Close(context.Background()) })

		close(handlerErrs)
		for err := range handlerErrs {
			t.Error(err)
		}
		if startErr != nil {
			t.Errorf("Start: %v", startErr)
		}
		if checkErr != nil {
			t.Errorf("Check: %v", checkErr)
		}
		if closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	})

	t.Run("a handler closes the orchestrator", func(t *testing.T) {
		s, verifier, sess := newTestSession(t, Config{})
		verifier.result = passing
		progress := newFakeProgress()

		var o *Orchestrator
		var closeOnce sync.Once
		var closeFromHandlerErr error
		b := bus.New()
		b.Subscribe("closes", func(ctx context.Context, ev bus.Event) {
			if ev.Kind() == bus.KindCheckRun {
				closeOnce.Do(func() { closeFromHandlerErr = o.Close(ctx) })
			}
		})

		var err error
		o, err = NewOrchestrator(OrchestratorConfig{
			Session: s, Bus: b, Progress: progress, ProfileID: 1, PackID: "core-linux-basics",
		})
		if err != nil {
			t.Fatalf("NewOrchestrator: %v", err)
		}

		var startErr, checkErr error
		mustNotHang(t, 5*time.Second, "Start", func() { startErr = o.Start(context.Background()) })
		if startErr != nil {
			t.Fatalf("Start: %v", startErr)
		}
		sess.mu.Lock()
		before := sess.teardownCalls
		sess.mu.Unlock()

		mustNotHang(t, 5*time.Second, "Check", func() { _, checkErr = o.Check(context.Background()) })

		if checkErr != nil {
			t.Errorf("Check: %v", checkErr)
		}
		if closeFromHandlerErr != nil {
			t.Errorf("Close from inside a handler: %v", closeFromHandlerErr)
		}
		sess.mu.Lock()
		got := sess.teardownCalls - before
		sess.mu.Unlock()
		if got != 1 {
			t.Errorf("a Close from inside a handler performed %d teardowns, want 1", got)
		}
		if o.State() != StateIdle {
			t.Errorf("State() = %q, want %q", o.State(), StateIdle)
		}
		// The pass was recorded before CheckRun went out, so the Close the
		// handler ran has to finish the attempt as passed, not abandoned.
		if progress.finishAttemptArg.Outcome != store.OutcomePassed {
			t.Errorf("Outcome = %q, want %q: a Close racing a passing check must not throw the pass away",
				progress.finishAttemptArg.Outcome, store.OutcomePassed)
		}
	})
}

// --- legalTransition, exercised directly ---

// TestLegalTransitionCoversTheEdgesNoMethodTakes pins the edges the table
// declares but no exported method reaches yet, so the contract is provable
// before any caller (the hint ladder, scoring, both later tickets) depends on
// it.
func TestLegalTransitionCoversTheEdgesNoMethodTakes(t *testing.T) {
	if !legalTransition(StateActive, StateHinting) {
		t.Error("Active -> Hinting is not legal, but the hint ladder needs it")
	}
	if !legalTransition(StateHinting, StateActive) {
		t.Error("Hinting -> Active is not legal, but returning from a hint needs it")
	}
	if legalTransition(StateHinting, StateChecking) {
		t.Error("Hinting -> Checking is legal; a learner must return to Active before checking again")
	}
	// Check itself never takes this edge: it returns to StateActive whether
	// the level passed or not. The edge is pinned here so that the state a
	// later scoring ticket will want is already agreed on.
	if !legalTransition(StateChecking, StatePassed) {
		t.Error("Checking -> Passed is not legal, but a passing check has nowhere else to come to rest")
	}
	if !legalTransition(StatePassed, StateActive) {
		t.Error("Passed -> Active is not legal, but a learner may keep working in a level they have passed")
	}
}
