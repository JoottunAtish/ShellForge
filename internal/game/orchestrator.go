package game

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/store"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// The Session Orchestrator: three public verbs, Start, Check and Close,
// driving one level attempt through the ten declared states.
//
// Every real transition this package performs is checked against
// legalTransition, in state.go, so the shape of the machine lives in one
// table rather than being re-derived at each call site. Two states are never
// assigned to o.state this ticket: StateProvisioning, because provisioning a
// sandbox is a caller's job above this package, and StateBriefing, because
// rendering a briefing is a rendering decision four layers from here. A
// third, StateHinting, is legal in the table and reachable through
// legalTransition directly, exercised by this package's own tests, but no
// exported method enters or exits it: there is no hint ladder this ticket.
//
// Locking discipline: one sync.Mutex, o.mu, guards state, closed, passed,
// attemptID, checkCount and startDone. It is held to read and write those
// fields and for nothing else. In particular it never spans a call that
// leaves this package: not the three sandbox calls (Session.Setup in Start,
// Session.Check in Check, Session.Teardown in Close), not a Progress call,
// and not a bus.Publish. Publish runs every subscriber synchronously, in the
// publishing goroutine, so holding o.mu across it would deadlock this
// Orchestrator outright the first time a subscriber called back into it; a
// Progress call is sqlite, and holding a mutex across a disk write blocks
// every other caller for nothing. Each such call is therefore made with the
// lock released, and the lock is reacquired immediately afterward to record
// the outcome, which means every one of those reacquisitions has to re-read
// o.closed rather than assume it is what it was.
//
// Ordering, which a mutex alone cannot buy: Close must never tear the level's
// world down while a Start is still building it, or Setup finishes afterward
// and leaves behind a world nothing will ever remove. So a Start that reaches
// Session.Setup registers startDone before it goes, closes it once its last
// sandbox and store call has returned, and Close waits on it before tearing
// anything down. Close does not wait on an in-flight Check: a check is
// read-only in the sandbox (CLAUDE.md non-negotiable 4), so a Check that
// finishes after a teardown has nothing left behind to remove. See Start,
// Check and Close for what all of that buys when they race.

// ErrOrchestratorClosed is returned by Start and Check once Close has run.
// closed is set exactly once, by the first Close, and never cleared: there
// is no reset this ticket, so an Orchestrator past Close is done for good.
// Compare with errors.Is.
var ErrOrchestratorClosed = errors.New("game: orchestrator is closed")

// Progress is the subset of *store.Store the Orchestrator needs to open and
// close attempts and read back a level's recorded state. It is declared
// here, in the consumer, the same way Session declares Verifier: a two to
// four method fake is enough to test every transition without a real
// sqlite file.
type Progress interface {
	// StartAttempt records a new attempt and returns its id.
	StartAttempt(ctx context.Context, profileID int64, packID, levelID string, levelVersion int, startedAt time.Time) (int64, error)

	// FinishAttempt closes an attempt opened by StartAttempt.
	FinishAttempt(ctx context.Context, attemptID int64, a store.Attempt) error

	// SetLevelStatus records a level's status directly, used here only for
	// the first full pass: FinishAttempt, at Close, sets status from the
	// outcome on its own, and Start relies on StartAttempt already having
	// set in_progress.
	SetLevelStatus(ctx context.Context, profileID int64, packID, levelID string, levelVersion int, status store.LevelStatus) error

	// LevelState reads back a profile's recorded progress on a level.
	LevelState(ctx context.Context, profileID int64, levelID string, levelVersion int) (store.LevelState, bool, error)
}

// OrchestratorConfig is everything NewOrchestrator needs. Session, Bus and
// Progress are required; Now defaults to time.Now.
type OrchestratorConfig struct {
	// Session is the level in play. The Orchestrator drives it and owns
	// none of what it borrows: exactly the rule Session itself already
	// documents for the runtime.Session it wraps.
	Session *Session

	// Bus receives the domain events this attempt produces.
	Bus *bus.Bus

	// Progress opens and closes attempts and reads level state.
	Progress Progress

	// ProfileID is whose attempt this is. Not validated: a zero profile id
	// is a caller's problem to notice, not this package's to guess at.
	ProfileID int64

	// PackID is the pack the level belongs to. Not validated, for the same
	// reason as ProfileID.
	PackID string

	// Now returns the current time. Defaults to time.Now. A test overrides
	// it for a deterministic LevelStarted.At, CheckRun.At and
	// LevelPassed.At.
	Now func() time.Time
}

// Orchestrator drives one level attempt through Start, Check and Close.
//
// What it deliberately does not do, so the absence reads as a decision
// rather than an oversight: no scoring beyond the zero values LevelPassed
// already carries, no hint ladder, no reset, no next-level selection, no
// achievements beyond the bus carrying the type, and no CLI wiring. See
// PROGRESS.md for the full list this ticket left out on purpose.
type Orchestrator struct {
	session  *Session
	bus      *bus.Bus
	progress Progress

	profileID int64
	packID    string
	now       func() time.Time

	mu         sync.Mutex
	state      State
	closed     bool
	passed     bool
	attemptID  int64
	checkCount int

	// startDone is created by the Start call that reaches Session.Setup and
	// closed by that same call once its last sandbox and store operation has
	// returned. Close waits on it, so that a teardown is always the last
	// thing to touch the level's world.
	//
	// It is nil until such a Start begins and is left in place, closed,
	// afterward rather than being set back to nil: at most one Start ever
	// reaches Session.Setup, because Start is legal only from StateIdle and
	// leaves StateIdle under the lock, so a closed channel here simply reads
	// as "no Start is in flight any more" and every later Close receives
	// from it without waiting.
	startDone chan struct{}
}

// NewOrchestrator validates cfg and returns an Orchestrator ready for
// Start, sitting in StateIdle.
func NewOrchestrator(cfg OrchestratorConfig) (*Orchestrator, error) {
	switch {
	case cfg.Session == nil:
		return nil, errors.New("game: OrchestratorConfig.Session is nil")
	case cfg.Bus == nil:
		return nil, errors.New("game: OrchestratorConfig.Bus is nil")
	case cfg.Progress == nil:
		return nil, errors.New("game: OrchestratorConfig.Progress is nil")
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Orchestrator{
		session:   cfg.Session,
		bus:       cfg.Bus,
		progress:  cfg.Progress,
		profileID: cfg.ProfileID,
		packID:    cfg.PackID,
		now:       now,
		state:     StateIdle,
	}, nil
}

// level returns the level the wrapped Session is playing. A small accessor
// rather than a repeated o.session.Level() so every call site reads the
// same way. It needs no lock: o.session is set once, by NewOrchestrator, and
// never reassigned.
func (o *Orchestrator) level() *content.Level { return o.session.Level() }

// transition moves o.state to to, after checking legalTransition. Every
// call site below asks only for an edge the table in state.go already
// allows, so a false here means this package's own logic has drifted from
// its own table: a genuinely impossible state, and CLAUDE.md's own
// exception for panicking on one. Callers hold o.mu already; transition
// does not lock.
func (o *Orchestrator) transition(to State) {
	if !legalTransition(o.state, to) {
		panic(fmt.Sprintf("game: illegal orchestrator transition %s -> %s", o.state, to))
	}
	o.state = to
}

// State reports the Orchestrator's current state. Legal from every state,
// and never mutates.
func (o *Orchestrator) State() State {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.state
}

// Passed reports whether this attempt has ever fully passed. Legal from
// every state, and never mutates.
func (o *Orchestrator) Passed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.passed
}

// failStart moves o to StateFailed after a step of Start failed, unless a
// concurrent Close already won. Close owns both the state and the outcome of
// the attempt from the moment it sets closed, and it has already torn this
// attempt's world back down, so there is nothing left for Failed to describe.
func (o *Orchestrator) failStart() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.closed {
		o.transition(StateFailed)
	}
}

// Start opens a new attempt at o's level: it materializes the level's
// world, records the attempt, and publishes LevelStarted. Legal only from
// StateIdle; refused everywhere else with a *TransitionError, and refused
// with ErrOrchestratorClosed once Close has run, checked first.
//
// A Session.Setup failure never opens an attempt: the store is left
// untouched and the Orchestrator moves to StateFailed, with no retry this
// ticket. A failure after Progress.StartAttempt has already succeeded is
// different, and records the attempt id before it returns even though the
// call as a whole fails: StartAttempt has written a row and marked the level
// in_progress by then, and Close has to be able to close that row rather
// than orphan it with ended_at never set.
//
// A Close racing this call always wins, and waits for it rather than racing
// it: from the moment Session.Setup begins until the last store call
// returns, a concurrent Close blocks instead of tearing down a world that is
// still being built. This call then finds o closed and stops. LevelStarted
// is published after that window has closed, so a subscriber may call Close
// from inside its handler without deadlocking.
func (o *Orchestrator) Start(ctx context.Context) error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return ErrOrchestratorClosed
	}
	if o.state != StateIdle {
		state := o.state
		o.mu.Unlock()
		return &TransitionError{Op: "start", State: state}
	}
	o.transition(StateSetup)
	inFlight := make(chan struct{})
	o.startDone = inFlight
	o.mu.Unlock()

	// done releases a Close waiting for this call to stop touching the
	// sandbox and the store. It runs on every path out, failures included,
	// and is called explicitly before the final Publish so that a subscriber
	// is free to call Close from inside its handler.
	var once sync.Once
	done := func() { once.Do(func() { close(inFlight) }) }
	defer done()

	lvl := o.level()

	if err := o.session.Setup(ctx); err != nil {
		o.failStart()
		return fmt.Errorf("start level %q: %w", lvl.ID, err)
	}

	startedAt := o.now()

	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed {
		// Close raced in while Setup ran unlocked and is waiting on done to
		// tear back down the world Setup just built. Opening an attempt now
		// would open a store row for a level nobody is playing any more.
		return ErrOrchestratorClosed
	}

	attemptID, err := o.progress.StartAttempt(ctx, o.profileID, o.packID, lvl.ID, lvl.Version, startedAt)
	if err != nil {
		o.failStart()
		return fmt.Errorf("start attempt for level %q: %w", lvl.ID, err)
	}

	// Recorded here, before the read-back below rather than after it. The
	// row exists from this point on, so every path out from here has to
	// leave Close able to find its id and finish it.
	o.mu.Lock()
	o.attemptID = attemptID
	o.checkCount = 0
	o.mu.Unlock()

	levelState, ok, err := o.progress.LevelState(ctx, o.profileID, lvl.ID, lvl.Version)
	if err != nil {
		o.failStart()
		return fmt.Errorf("read level state for %q right after starting an attempt: %w", lvl.ID, err)
	}
	if !ok {
		o.failStart()
		return fmt.Errorf("start level %q: no level_state row right after StartAttempt reported one written", lvl.ID)
	}

	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return ErrOrchestratorClosed
	}
	o.transition(StateActive)
	o.mu.Unlock()

	done()
	o.bus.Publish(ctx, bus.LevelStarted{
		LevelID:   lvl.ID,
		AttemptID: attemptID,
		Attempt:   levelState.Attempts,
		At:        startedAt,
	})
	return nil
}

// Check runs the level's checks once and returns the result. Legal only
// from StateActive; refused everywhere else with a *TransitionError naming
// the state that refused it, including StateChecking itself, which is how
// a second concurrent Check finds the first still running. That is also
// what a subscriber calling Check from inside a handler for this call's own
// CheckRun gets: a refusal, promptly, rather than a deadlock.
//
// If Close closes the Orchestrator while this call's Session.Check is
// running, unlocked, Close always wins: this call returns
// ErrOrchestratorClosed and a zero LevelResult instead of publishing or
// recording anything further. Close never waits on it, because a check only
// reads the sandbox and so leaves nothing behind a teardown would have to
// remove.
func (o *Orchestrator) Check(ctx context.Context) (verify.LevelResult, error) {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return verify.LevelResult{}, ErrOrchestratorClosed
	}
	if o.state != StateActive {
		state := o.state
		o.mu.Unlock()
		return verify.LevelResult{}, &TransitionError{Op: "check", State: state}
	}
	o.transition(StateChecking)
	o.checkCount++
	checkCount := o.checkCount
	attemptID := o.attemptID
	lvl := o.level()
	o.mu.Unlock()

	result, err := o.session.Check(ctx)

	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return verify.LevelResult{}, ErrOrchestratorClosed
	}
	if err != nil {
		// Back to Active rather than to Failed: a check command that could
		// not run is retryable, and does not end the attempt.
		o.transition(StateActive)
		o.mu.Unlock()
		return result, fmt.Errorf("check level %q: %w", lvl.ID, err)
	}

	now := o.now()
	// Decided and recorded under the lock, acted on with it released. A
	// Close racing the two calls below has to see the pass this check just
	// made, whichever of them lands first, or it would finish the attempt as
	// abandoned while the learner was being told they had passed.
	markPassed := result.Passed && !o.passed
	if markPassed {
		o.passed = true
	}
	o.mu.Unlock()

	o.bus.Publish(ctx, bus.CheckRun{
		LevelID:          lvl.ID,
		AttemptID:        attemptID,
		Passed:           result.Passed,
		ObjectivesPassed: countObjectivesPassed(result.Objectives),
		ObjectivesTotal:  len(result.Objectives),
		At:               now,
	})

	if markPassed {
		if err := o.progress.SetLevelStatus(ctx, o.profileID, o.packID, lvl.ID, lvl.Version, store.StatusPassed); err != nil {
			o.mu.Lock()
			if !o.closed {
				// The pass was recorded above so that a racing Close could
				// see it; it did not reach the store, so take it back and
				// let a later check try again.
				o.passed = false
				o.transition(StateActive)
			}
			o.mu.Unlock()
			return result, fmt.Errorf("mark level %q passed: %w", lvl.ID, err)
		}
		o.bus.Publish(ctx, bus.LevelPassed{
			LevelID:   lvl.ID,
			AttemptID: attemptID,
			FirstTry:  checkCount == 1,
			At:        now,
		})
	}

	o.mu.Lock()
	if !o.closed {
		o.transition(StateActive)
	}
	o.mu.Unlock()
	return result, nil
}

// countObjectivesPassed counts the objectives whose Status is
// verify.StatusPass, for CheckRun.ObjectivesPassed.
func countObjectivesPassed(objectives []verify.ObjectiveResult) int {
	n := 0
	for _, obj := range objectives {
		if obj.Status == verify.StatusPass {
			n++
		}
	}
	return n
}

// Close ends the attempt, once and permanently: it tears the level's world
// down and, if an attempt was ever opened, closes it. It is always legal,
// from any state, and always safe to call more than once: the second and
// every later call is a no-op that returns nil immediately, without a second
// teardown and without a second FinishAttempt.
//
// Two callers racing is fire and forget for the loser, deliberately: the
// first caller through does the work, and every other one returns nil at
// once rather than waiting to report the winner's teardown and FinishAttempt
// errors as its own. A caller that needs the outcome of the teardown has to
// be the caller that performs it.
//
// A Start still building the level's world is waited for rather than raced.
// Close marks the Orchestrator closed first, which is what stops that Start
// from opening or advancing anything further, then blocks until its last
// sandbox and store call has returned, and only then tears down. That
// ordering is what makes a teardown the last thing to touch the level's
// world even when the learner interrupts the run mid-Setup. An in-flight
// Check is not waited for: it only reads the sandbox.
//
// Session.Teardown and Progress.FinishAttempt are both attempted
// unconditionally, regardless of the other's outcome, and their errors are
// combined with errors.Join. FinishAttempt is skipped entirely, never
// called with a zero attempt id, when Start never successfully opened one.
func (o *Orchestrator) Close(ctx context.Context) error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil
	}
	o.closed = true
	inFlight := o.startDone
	o.mu.Unlock()

	if inFlight != nil {
		<-inFlight
	}

	o.mu.Lock()
	o.transition(StateTeardown)
	attemptID := o.attemptID
	passed := o.passed
	o.mu.Unlock()

	lvl := o.level()
	teardownErr := o.session.Teardown(ctx)

	var finishErr error
	if attemptID != 0 {
		outcome := store.OutcomeAbandoned
		if passed {
			outcome = store.OutcomePassed
		}
		finishErr = o.progress.FinishAttempt(ctx, attemptID, store.Attempt{
			Outcome: outcome,
			EndedAt: o.now(),
		})
	}

	o.mu.Lock()
	o.transition(StateIdle)
	o.mu.Unlock()

	if combined := errors.Join(teardownErr, finishErr); combined != nil {
		return fmt.Errorf("close level %q: %w", lvl.ID, combined)
	}
	return nil
}
