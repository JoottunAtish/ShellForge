package game

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/game/score"
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

	// AddHintUsed records one hint spent, at the moment the learner
	// confirms it rather than when the level ends.
	AddHintUsed(ctx context.Context, profileID int64, packID, levelID string, levelVersion int) error
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

	// ladder is this level's hints plus how far up them the learner has
	// already paid to climb. Seeded at Start from level_state.hints_used,
	// so a level re-entered after three hints offers the fourth.
	ladder *Ladder

	// attemptNo is which attempt at this level this is, 1-based. It decides
	// score.Inputs.FirstTry together with checkCount.
	attemptNo int

	// wasPassed records whether the learner had already passed this level
	// before this attempt opened.
	//
	// It exists to undo a downgrade rather than to grant anything.
	// store.FinishAttempt writes in_progress on an abandoned outcome, so
	// replaying a level already passed and then walking away would take the
	// pass back. PROGRESS.md recorded that as deferred to "scoring, which
	// this ticket puts out of scope"; this is that ticket, and Close is
	// where the status is restored. The fix lives here rather than in
	// FinishAttempt because preserving a best-known status across
	// re-attempts is orchestrator policy, which is exactly what the store
	// said it was declining to decide.
	wasPassed bool

	// commands counts the CommandExecuted events published for this
	// attempt. It only ever adds an efficiency bonus and can never make a
	// level unpassable, which is what makes it safe to derive from data the
	// learner can influence: see issue #23 and score's own package doc.
	commands int

	// scored is the award this attempt earned, computed once on the first
	// fully passing check and read back by Score.
	scored score.Result

	// unsubscribe detaches the CommandExecuted subscription taken out by
	// NewOrchestrator. Called once, by the first Close.
	unsubscribe func()

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

	// resetMu is the reset lock ARCHITECTURE 4.5 names among the
	// orchestrator's responsibilities, and it is deliberately a second
	// mutex rather than a wider use of o.mu.
	//
	// o.mu promises never to span a call that leaves this package, and that
	// promise is what lets a bus subscriber call back into an Orchestrator
	// from inside its handler. A reset needs the opposite: it has to hold a
	// lock across two sandbox calls, teardown then setup, so that a check
	// arriving mid-reset waits rather than running against a half-deleted
	// world. Giving that job to a lock of its own keeps o.mu's promise
	// intact.
	//
	// Held by Reset for its whole operation, taken by Check around
	// Session.Check, and taken by Close before it tears down, so a teardown
	// can never run while a reset is rebuilding.
	resetMu sync.Mutex
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

	o := &Orchestrator{
		session:   cfg.Session,
		bus:       cfg.Bus,
		progress:  cfg.Progress,
		profileID: cfg.ProfileID,
		packID:    cfg.PackID,
		now:       now,
		state:     StateIdle,
		ladder:    NewLadder(cfg.Session.Level(), 0),
	}

	// The Orchestrator subscribes as well as publishes, for exactly one
	// thing: counting the learner's commands so the efficiency bonus is
	// real rather than always zero.
	//
	// Reading them off the bus rather than being told is what keeps the
	// count honest whatever produces it. game.JournalSink publishes one
	// CommandExecuted per record it drains out of the sandbox journal, and
	// anything that later replaces or joins it publishes the same event, so
	// no caller has to remember to report a number as well as drain.
	// Events from another level or another attempt are ignored, so a
	// long-lived bus cannot leak one level's commands into another's score.
	o.unsubscribe = cfg.Bus.Subscribe("game.orchestrator", o.onEvent)

	return o, nil
}

// onEvent counts the commands published for this attempt.
//
// It takes o.mu and does nothing else: a bus handler runs in the
// publishing goroutine, so anything slower here would be slower for
// whoever published.
func (o *Orchestrator) onEvent(_ context.Context, ev bus.Event) {
	cmd, ok := ev.(bus.CommandExecuted)
	if !ok {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || cmd.AttemptID != o.attemptID || o.attemptID == 0 {
		return
	}
	o.commands++
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

	// Read BEFORE StartAttempt, and read again after it. Two reads of the
	// same row, for two different facts, and the order is what makes each
	// one answerable: StartAttempt writes in_progress and increments
	// attempts in one transaction, so the status and hint count from before
	// it are the only ones that describe what the learner arrived with,
	// and the attempt count from after it is the only one that describes
	// the attempt they just opened.
	prior, priorOK, err := o.progress.LevelState(ctx, o.profileID, lvl.ID, lvl.Version)
	if err != nil {
		o.failStart()
		return fmt.Errorf("read level state for %q before starting an attempt: %w", lvl.ID, err)
	}
	o.mu.Lock()
	o.wasPassed = priorOK && prior.Status == store.StatusPassed
	o.ladder = NewLadder(lvl, prior.HintsUsed)
	o.commands = 0
	o.scored = score.Result{}
	o.mu.Unlock()

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
	o.attemptNo = levelState.Attempts
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

	// The reset lock, taken for the duration of the check and nothing more.
	// A reset in flight is tearing the level's world down and building it
	// back; a check that ran through the middle of that would report a
	// failure the learner cannot explain and cannot reproduce. Waiting is
	// the whole point: this call blocks until the world is whole again and
	// then verifies the rebuilt one.
	o.resetMu.Lock()
	result, err := o.session.Check(ctx)
	o.resetMu.Unlock()

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
	var awarded score.Result
	var hintsTaken, commandsUsed int
	firstTry := false
	if markPassed {
		o.passed = true
		firstTry = checkCount == 1 && o.attemptNo <= 1
		hintsTaken = o.ladder.Taken()
		commandsUsed = o.commands
		awarded = score.Score(score.Inputs{
			BaseXP:       lvl.XP,
			Difficulty:   lvl.Difficulty,
			HintCosts:    o.ladder.CostsTaken(),
			ParCommands:  lvl.ParCommands,
			CommandsUsed: commandsUsed,
			FirstTry:     firstTry,
			OptionalHit:  countOptionalPassed(result.Objectives),
		})
		// Recorded here, under the same lock that decided the pass, so a
		// Close racing this check finishes the attempt with the score the
		// learner was just shown rather than with a zero.
		o.scored = awarded
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
				// see it; it did not reach the store, so take it back, along
				// with the award that went with it, and let a later check
				// try again.
				o.passed = false
				o.scored = score.Result{}
				o.transition(StateActive)
			}
			o.mu.Unlock()
			return result, fmt.Errorf("mark level %q passed: %w", lvl.ID, err)
		}
		o.bus.Publish(ctx, bus.LevelPassed{
			LevelID:      lvl.ID,
			AttemptID:    attemptID,
			Score:        awarded.Total,
			HintsUsed:    hintsTaken,
			CommandsUsed: commandsUsed,
			FirstTry:     firstTry,
			At:           now,
		})
	}

	o.mu.Lock()
	if !o.closed {
		o.transition(StateActive)
	}
	o.mu.Unlock()
	return result, nil
}

// countOptionalPassed counts the bonus objectives the learner satisfied,
// which is score.Inputs.OptionalHit.
func countOptionalPassed(objectives []verify.ObjectiveResult) int {
	n := 0
	for _, obj := range objectives {
		if obj.Optional && obj.Status == verify.StatusPass {
			n++
		}
	}
	return n
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

	// Stop counting before anything else. A CommandExecuted published
	// during teardown belongs to no attempt, and o.commands is about to be
	// read for the last time.
	if o.unsubscribe != nil {
		o.unsubscribe()
	}

	// Wait for any reset to finish rebuilding before tearing down, so a
	// teardown is never concurrent with a setup. Reset re-checks o.closed
	// after it takes this same lock, so a reset that had not started yet
	// finds the Orchestrator closed and does nothing.
	o.resetMu.Lock()
	defer o.resetMu.Unlock()

	o.mu.Lock()
	o.transition(StateTeardown)
	attemptID := o.attemptID
	passed := o.passed
	wasPassed := o.wasPassed
	awarded := o.scored.Total
	commandsUsed := o.commands
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
			Score:   awarded,
			// Zero, deliberately, and not an oversight. Hints are recorded
			// as they are taken, through Progress.AddHintUsed, so that a
			// learner who abandons a level has still spent them. That call
			// already incremented level_state.hints_used, and FinishAttempt
			// folds this field into the same column, so passing the count
			// here would charge every hint twice. See AddHintUsed's own doc
			// comment for the consequence this accepts.
			HintsUsed:    0,
			CommandsUsed: commandsUsed,
		})
	}

	// Undo the downgrade, if there is one to undo. FinishAttempt writes
	// in_progress for an abandoned outcome, which would take back a pass
	// the learner earned on an earlier attempt merely because they replayed
	// the level and walked away. best_score and first_passed_at survive on
	// their own; the status does not, so it is put back here.
	var restoreErr error
	if attemptID != 0 && !passed && wasPassed {
		if err := o.progress.SetLevelStatus(ctx, o.profileID, o.packID, lvl.ID, lvl.Version, store.StatusPassed); err != nil {
			restoreErr = fmt.Errorf("restore the recorded pass on level %q: %w", lvl.ID, err)
		}
	}

	o.mu.Lock()
	o.transition(StateIdle)
	o.mu.Unlock()

	if combined := errors.Join(teardownErr, finishErr, restoreErr); combined != nil {
		return fmt.Errorf("close level %q: %w", lvl.ID, combined)
	}
	return nil
}

// Score returns the award this attempt has earned, with the arithmetic that
// produced it. It is the zero Result until a check passes, and never
// changes afterward: a second check after passing awards nothing further.
//
// Legal from every state, and never mutates.
func (o *Orchestrator) Score() score.Result {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.scored
}

// CommandsUsed reports how many commands have been counted for this
// attempt. Legal from every state, and never mutates.
func (o *Orchestrator) CommandsUsed() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.commands
}

// HintsTaken reports how many hint tiers have been spent on this level,
// including any spent on an earlier attempt. Legal from every state, and
// never mutates.
func (o *Orchestrator) HintsTaken() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ladder.Taken()
}

// PeekHint returns what the next hint would cost, or what revealing the
// solution would cost when reveal is true.
//
// It changes nothing, spends nothing, publishes nothing and writes nothing.
// It is legal from every state, including a closed Orchestrator, because
// showing a price is not an action on the level: a caller that wants to
// know what a hint costs before deciding whether the state permits taking
// one should not have to handle a transition error to find out.
//
// available is false when there is no such tier: every hint taken, or, for
// reveal, a level that authored no solution tier. Use Ladder.HasReveal
// through this Orchestrator's own error from TakeHint to tell those apart.
func (o *Orchestrator) PeekHint(reveal bool) (Tier, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if reveal {
		return o.ladder.Reveal()
	}
	return o.ladder.Next()
}

// TakeHint spends a tier, records it, and publishes HintTaken.
//
// Legal only from StateActive, and refused with ErrOrchestratorClosed once
// Close has run. It transitions StateActive to StateHinting for the
// duration and back afterward, which are edges legalTransition has always
// allowed and which no exported method entered until now.
//
// The order is: record, then spend. Progress.AddHintUsed writes
// level_state.hints_used before this Orchestrator's own ladder advances, so
// the store is the source of truth for how far up the ladder a learner has
// paid to climb, and a failure to write it spends nothing at all. That is
// what makes the tier offered after a restart the one they have not yet
// paid for, however the previous session ended.
//
// It never subtracts anything itself. It records which tiers were taken and
// their authored costs; internal/game/score is the only thing that does
// arithmetic with them, and the floor at zero lives there.
func (o *Orchestrator) TakeHint(ctx context.Context, reveal bool) (Tier, error) {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return Tier{}, ErrOrchestratorClosed
	}
	if o.state != StateActive {
		state := o.state
		o.mu.Unlock()
		return Tier{}, &TransitionError{Op: "take a hint", State: state}
	}

	// Chosen under the lock, spent below once the store has accepted it.
	var (
		tier Tier
		ok   bool
	)
	if reveal {
		tier, ok = o.ladder.Reveal()
	} else {
		tier, ok = o.ladder.Next()
	}
	if !ok {
		hasReveal := o.ladder.HasReveal()
		o.mu.Unlock()
		if reveal && !hasReveal {
			return Tier{}, ErrNoRevealTier
		}
		return Tier{}, ErrLadderExhausted
	}

	o.transition(StateHinting)
	attemptID := o.attemptID
	o.mu.Unlock()

	lvl := o.level()

	if err := o.progress.AddHintUsed(ctx, o.profileID, o.packID, lvl.ID, lvl.Version); err != nil {
		o.mu.Lock()
		if !o.closed {
			o.transition(StateActive)
		}
		o.mu.Unlock()
		return Tier{}, fmt.Errorf("record a hint taken on level %q: %w", lvl.ID, err)
	}

	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return Tier{}, ErrOrchestratorClosed
	}
	spent, err := o.ladder.Take(tier.Index)
	if err != nil {
		o.transition(StateActive)
		o.mu.Unlock()
		return Tier{}, fmt.Errorf("take hint %d on level %q: %w", tier.Index, lvl.ID, err)
	}
	o.transition(StateActive)
	o.mu.Unlock()

	o.bus.Publish(ctx, bus.HintTaken{
		LevelID:   lvl.ID,
		AttemptID: attemptID,
		Tier:      spent.Index,
		Cost:      spent.Cost,
		Revealed:  spent.Reveals,
		At:        o.now(),
	})
	return spent, nil
}

// Reset rebuilds the current level's world from scratch.
//
// It is one call to Session.Setup and nothing else. setup.Runner.Setup
// already tears the level root down unconditionally before it rebuilds,
// through the same validated, refusing, inside-the-sandbox deletion path
// every other setup uses, and it rolls back on a failure halfway. So this
// method adds no deletion of its own, and that is a hard requirement rather
// than a convenience: a second removal path here, whether an os.RemoveAll,
// an inline rm, or anything host-side, would be wrong however well it
// tested. Every refusal setup.Runner makes (an empty root, a relative root,
// a .. segment, anything not strictly under the learner's home, the home
// directory itself, anything containing the state directory, and anything
// whose symlink resolution moved it) therefore holds when reached from
// here, and internal/game/reset_test.go asserts each of them again through
// this method rather than trusting that they do.
//
// Legal only from StateActive. A reset arriving while a check is running is
// refused, naming StateChecking, rather than deleting the world out from
// under a check that is reading it.
//
// It holds the reset lock for the whole rebuild, so a check that arrives
// mid-reset waits and then runs against the rebuilt world rather than
// against a half-deleted one. It does not enter a state of its own: the
// level stays StateActive throughout, which is what lets that waiting check
// proceed the moment the world is whole again instead of being refused.
//
// It does not reset progress. The attempt continues, hints already taken
// stay taken, the command count keeps counting, and the attempt number is
// unchanged. Rebuilding the files is not starting the level again.
func (o *Orchestrator) Reset(ctx context.Context) error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return ErrOrchestratorClosed
	}
	if o.state != StateActive {
		state := o.state
		o.mu.Unlock()
		return &TransitionError{Op: "reset", State: state}
	}
	attemptID := o.attemptID
	o.mu.Unlock()

	o.resetMu.Lock()

	// Re-checked after taking the lock, not only before. Close marks the
	// Orchestrator closed and then waits on this same lock before tearing
	// down, so a reset that had not started yet must find that out here and
	// build nothing: otherwise it would rebuild a world immediately after
	// the teardown that was meant to be the last thing to touch it.
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed {
		o.resetMu.Unlock()
		return ErrOrchestratorClosed
	}

	lvl := o.level()
	err := o.session.Setup(ctx)

	// Released before publishing. Publish runs every subscriber
	// synchronously in this goroutine, and a subscriber calling Check from
	// inside its handler would block on this lock forever.
	o.resetMu.Unlock()

	if err != nil {
		return fmt.Errorf("reset level %q: %w", lvl.ID, err)
	}

	o.bus.Publish(ctx, bus.LevelReset{
		LevelID:   lvl.ID,
		AttemptID: attemptID,
		At:        o.now(),
	})
	return nil
}
