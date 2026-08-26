package game

import (
	"context"
	"errors"
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

// --- legalTransition, exercised directly (Active <-> Hinting) ---

// TestLegalTransitionCoversHintingBothWays pins the two edges no exported
// method reaches yet, so the contract is provable before any caller (the
// hint ladder, a later ticket) depends on it.
func TestLegalTransitionCoversHintingBothWays(t *testing.T) {
	if !legalTransition(StateActive, StateHinting) {
		t.Error("Active -> Hinting is not legal, but the hint ladder needs it")
	}
	if !legalTransition(StateHinting, StateActive) {
		t.Error("Hinting -> Active is not legal, but returning from a hint needs it")
	}
	if legalTransition(StateHinting, StateChecking) {
		t.Error("Hinting -> Checking is legal; a learner must return to Active before checking again")
	}
}
