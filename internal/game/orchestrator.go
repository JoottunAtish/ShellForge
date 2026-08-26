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
// attemptID and checkCount, and is held for every Progress and bus.Publish
// call. It is released only around the three calls that touch the sandbox:
// Session.Setup (in Start), Session.Check (in Check), Session.Teardown (in
// Close), and reacquired immediately afterward to record the outcome. See
// Check and Close for what that buys when the two race.

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
// same way.
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

// Start opens a new attempt at o's level: it materializes the level's
// world, records the attempt, and publishes LevelStarted. Legal only from
// StateIdle; refused everywhere else with a *TransitionError, and refused
// with ErrOrchestratorClosed once Close has run, checked first.
//
// A Session.Setup failure never opens an attempt: the store is left
// untouched and the Orchestrator moves to StateFailed, with no retry this
// ticket.
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
	o.mu.Unlock()

	if err := o.session.Setup(ctx); err != nil {
		o.mu.Lock()
		// Close may have raced in while Setup ran unlocked; it always wins
		// once it has set closed, and it already tore this attempt's world
		// back down, so there is nothing left here for Failed to describe.
		if !o.closed {
			o.transition(StateFailed)
		}
		o.mu.Unlock()
		return fmt.Errorf("start level %q: %w", o.level().ID, err)
	}

	startedAt := o.now()

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		// Same race, on the success side: Setup finished after Close had
		// already torn the world down. Opening an attempt now would open a
		// store row for a level nobody is playing any more.
		return ErrOrchestratorClosed
	}

	lvl := o.level()
	attemptID, err := o.progress.StartAttempt(ctx, o.profileID, o.packID, lvl.ID, lvl.Version, startedAt)
	if err != nil {
		o.transition(StateFailed)
		return fmt.Errorf("start attempt for level %q: %w", lvl.ID, err)
	}

	levelState, ok, err := o.progress.LevelState(ctx, o.profileID, lvl.ID, lvl.Version)
	if err != nil {
		o.transition(StateFailed)
		return fmt.Errorf("read level state for %q right after starting an attempt: %w", lvl.ID, err)
	}
	if !ok {
		o.transition(StateFailed)
		return fmt.Errorf("start level %q: no level_state row right after StartAttempt reported one written", lvl.ID)
	}

	o.attemptID = attemptID
	o.checkCount = 0
	o.transition(StateActive)
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
// a second concurrent Check finds the first still running.
//
// If Close closes the Orchestrator while this call's Session.Check is
// running, unlocked, Close always wins: this call returns
// ErrOrchestratorClosed and a zero LevelResult instead of publishing or
// recording anything further. Close never waits on it.
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
	defer o.mu.Unlock()
	if o.closed {
		return verify.LevelResult{}, ErrOrchestratorClosed
	}
	if err != nil {
		o.transition(StateActive)
		return result, fmt.Errorf("check level %q: %w", lvl.ID, err)
	}

	now := o.now()
	o.bus.Publish(ctx, bus.CheckRun{
		LevelID:          lvl.ID,
		AttemptID:        attemptID,
		Passed:           result.Passed,
		ObjectivesPassed: countObjectivesPassed(result.Objectives),
		ObjectivesTotal:  len(result.Objectives),
		At:               now,
	})

	if result.Passed && !o.passed {
		if err := o.progress.SetLevelStatus(ctx, o.profileID, o.packID, lvl.ID, lvl.Version, store.StatusPassed); err != nil {
			o.transition(StateActive)
			return result, fmt.Errorf("mark level %q passed: %w", lvl.ID, err)
		}
		o.passed = true
		o.bus.Publish(ctx, bus.LevelPassed{
			LevelID:   lvl.ID,
			AttemptID: attemptID,
			FirstTry:  checkCount == 1,
			At:        now,
		})
	}

	o.transition(StateActive)
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
// every later call is a no-op that returns nil immediately, without a
// second teardown and without a second FinishAttempt.
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
	o.transition(StateTeardown)
	attemptID := o.attemptID
	passed := o.passed
	lvl := o.level()
	o.mu.Unlock()

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
