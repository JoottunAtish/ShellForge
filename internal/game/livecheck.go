package game

import (
	"context"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// DefaultLiveDebounce is the live checker's default coalescing window.
//
// One verification pass over the shipped pack measures 90ms to 1.5s, with
// files-03 and boss-final at the top of that range, so a pass per command
// would be felt at a real prompt. 750ms is short enough that an objective
// ticks while the learner is still looking at the command that earned it,
// and long enough that holding Enter cannot spawn a docker exec per
// keystroke.
const DefaultLiveDebounce = 750 * time.Millisecond

// LiveChecker re-verifies a level's cheap checks whenever the learner runs a
// command, throttled so a burst of commands inside one debounce window
// produces exactly two passes: one on the leading edge and one coalescing
// everything else. It never decides pass or fail on its own: it reports
// which cheap objectives changed status, and `check` remains the only
// answer a learner reads.
//
// Concurrency: Notify and Attach are safe from any goroutine. Exactly one
// goroutine may call Run, and that single-owner rule is what makes a pass
// single-flight against ITSELF: there is no lock over one live pass and the
// next, because there is nothing to contend. The transition map is touched
// only by Run's own goroutine.
//
// A live pass is not single-flight against the rest of the level, though,
// and that is what CheckCheapSession buys rather than this type: Run calls
// l.session.CheckCheap, never Session.checkCheap directly, so the real
// serialization against a check the learner typed and against a reset lives
// in whatever implements that interface. See CheckCheapSession.
type LiveChecker struct {
	session  CheckCheapSession
	debounce time.Duration

	// notify carries one pending wakeup from Notify to Run. Capacity 1 with
	// a non-blocking send: a notify arriving while one is already pending,
	// or while a pass is running, is coalesced into it rather than queued,
	// which is what collapses a burst of commands into one follow-up pass.
	notify chan struct{}

	// transitions carries one slice of changed objectives per pass that
	// found any. Capacity 1 with a non-blocking send: a printer that fell
	// behind must not stall the pass loop, which in turn must not stall the
	// journal drain. A dropped batch is rolled back out of last (see
	// rollback) rather than left recorded as delivered, so the objectives
	// it named are reported again on their next real transition instead of
	// staying silent for the rest of the level; `check` is always the
	// authoritative answer regardless.
	transitions chan []verify.ObjectiveResult

	// now and after are unexported seams for the fake-clock test. Production
	// code never sets them: NewLiveChecker defaults both to the real clock.
	now   func() time.Time
	after func(time.Duration) <-chan time.Time

	// last holds each cheap objective's most recently observed status.
	// Touched only by Run's own goroutine, which is what the single-owner
	// contract on Run buys: no lock is needed here.
	last map[string]verify.Status
}

// CheckCheapSession is what a live pass needs from the level in play,
// declared here in the consumer the same way Session declares Verifier and
// Orchestrator declares Progress.
//
// *Orchestrator satisfies it, and is what production code passes to
// NewLiveChecker rather than a *Session directly. That indirection is the
// fix for a real overlap: a live pass that called Session.checkCheap
// straight would share no lock at all with Orchestrator.Check, which takes
// resetMu around Session.Check precisely so a check cannot run through the
// middle of a Reset, and precisely so two verification passes over the same
// sandbox session cannot run at once. Routing the live pass through
// Orchestrator.CheckCheap, which takes that same resetMu, makes both true
// of a live pass as well: see Orchestrator.CheckCheap's own doc comment.
type CheckCheapSession interface {
	// CheckCheap runs the level's cheap checks and returns their objective
	// results. It returns nil, doing nothing further, when a pass would not
	// be meaningful right now (the attempt is closed, or not in the one
	// state a check can run against a whole world): a live pass is a
	// nicety on a background timer, so skipping this one and waiting for
	// the next notify is the right response, not an error nobody would see.
	CheckCheap(ctx context.Context) []verify.ObjectiveResult
}

// NewLiveChecker returns a checker for s. A debounce of zero or less means
// DefaultLiveDebounce.
//
// s may be nil for a checker used only to exercise transition directly, as
// this package's own tests do; Run returns immediately without ever calling
// CheckCheap on a nil s, rather than panicking on the first notify.
func NewLiveChecker(s CheckCheapSession, debounce time.Duration) *LiveChecker {
	if debounce <= 0 {
		debounce = DefaultLiveDebounce
	}
	return &LiveChecker{
		session:     s,
		debounce:    debounce,
		notify:      make(chan struct{}, 1),
		transitions: make(chan []verify.ObjectiveResult, 1),
		now:         time.Now,
		after:       time.After,
		last:        make(map[string]verify.Status),
	}
}

// Attach subscribes the checker to b's CommandExecuted events and returns a
// function that unsubscribes. The handler does nothing but a non-blocking
// notify: bus.Publish runs subscribers synchronously on the publishing
// goroutine, which is the goroutine that called JournalSink.Drain, so a
// handler that verified anything would stall the journal drain behind a
// sandbox round trip.
func (l *LiveChecker) Attach(b *bus.Bus) (detach func()) {
	return b.Subscribe("game.livecheck", func(_ context.Context, ev bus.Event) {
		if _, ok := ev.(bus.CommandExecuted); !ok {
			return
		}
		l.Notify()
	})
}

// Notify tells the checker that a command ran. It never blocks and never
// runs a pass on the caller's goroutine. Safe for concurrent use.
func (l *LiveChecker) Notify() {
	select {
	case l.notify <- struct{}{}:
	default:
	}
}

// Run owns the pass loop until ctx is done, then closes the Transitions
// channel. Exactly one goroutine may call Run; calling it twice is a
// programming error, not something this method defends against, because the
// single-owner rule is what buys single-flight with no lock.
//
// Throttle semantics, decided once here: leading edge plus one coalesced
// trailing edge. On a notify, if now minus the start of the last pass is at
// least the debounce window, a pass runs immediately; otherwise Run waits
// out the remainder of the window, drops one pending notify (the burst that
// arrived is covered by the pass about to run), then passes. The last-pass
// timestamp is stamped at the START of a pass, not the end, so a pass's own
// cost never adds to the interval. That is what guarantees N notifies inside
// one window produce exactly two passes, and that a learner running one
// command every 700ms is never starved, which a pure trailing-edge debounce
// would do forever.
func (l *LiveChecker) Run(ctx context.Context) {
	defer close(l.transitions)

	if l.session == nil {
		return
	}

	var lastPass time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.notify:
		}

		if since := l.now().Sub(lastPass); since < l.debounce {
			select {
			case <-ctx.Done():
				return
			case <-l.after(l.debounce - since):
			}
			select {
			case <-l.notify:
			default:
			}
		}

		lastPass = l.now()
		objs := l.session.CheckCheap(ctx)
		if ctx.Err() != nil {
			return
		}

		if transitions, prevs := l.transition(objs); len(transitions) > 0 {
			select {
			case l.transitions <- transitions:
			default:
				// Nobody was ready to receive this batch. Recording those
				// objectives as up to date anyway would mean they never
				// transition again for the rest of the level, so undo the
				// write transition just made and let a later pass find them
				// changed once more.
				l.rollback(prevs)
			}
		}
	}
}

// Transitions delivers the objectives that changed status, one slice per
// pass that found any, and is closed when Run returns. A receive is the
// only way a caller learns about a transition; the checker never writes to
// a terminal itself.
func (l *LiveChecker) Transitions() <-chan []verify.ObjectiveResult {
	return l.transitions
}

// liveMemory is one reported objective's status in l.last from before a
// transition call overwrote it, plus whether it was known at all. It exists
// only so Run can undo that write with rollback if the batch carrying it is
// ever dropped; nothing else needs it, and it is never kept past one pass.
type liveMemory struct {
	id     string
	status verify.Status
	known  bool
}

// transition compares objs against the checker's own memory of each cheap
// objective's last status, returns the ones worth reporting, and updates
// the memory to match. The second return is that same memory from BEFORE
// this call updated it, one entry per reported objective and in the same
// order, for rollback to undo the update with if this pass's batch is ever
// dropped.
//
// Reported: unknown to pass (the tick the feature exists for), fail to pass,
// and pass to fail (a regression the learner needs to know about).
// Suppressed: unknown to fail (which would print the whole checklist on the
// first command), pass to pass, fail to fail, and pass, fail, error or
// timeout to error or timeout (a transient sandbox hiccup is not news, and
// is never reported on either side of the transition).
func (l *LiveChecker) transition(objs []verify.ObjectiveResult) ([]verify.ObjectiveResult, []liveMemory) {
	var out []verify.ObjectiveResult
	var prevs []liveMemory
	for _, obj := range objs {
		prev, known := l.last[obj.ID]
		l.last[obj.ID] = obj.Status

		reported := false
		switch {
		case !known:
			reported = obj.Status == verify.StatusPass
		case obj.Status == verify.StatusPass && prev != verify.StatusPass:
			reported = true
		case prev == verify.StatusPass && obj.Status == verify.StatusFail:
			reported = true
		}

		if reported {
			out = append(out, obj)
			prevs = append(prevs, liveMemory{id: obj.ID, status: prev, known: known})
		}
	}
	return out, prevs
}

// rollback restores l.last to what it was before the transition call that
// produced prevs, for exactly the objectives named in it. It undoes the
// write transition already made when the batch describing that change is
// dropped rather than delivered, so those objectives are compared against
// their real previous status again on the next pass instead of being
// remembered as already reported for the rest of the level.
//
// An objective transition saw for the first time is restored to unknown,
// by deleting its entry, rather than to any status: it had none before, and
// giving it one here would make its later real pass look like a no-op
// repeat of the same status instead of the unknown-to-pass tick it is.
func (l *LiveChecker) rollback(prevs []liveMemory) {
	for _, p := range prevs {
		if !p.known {
			delete(l.last, p.id)
			continue
		}
		l.last[p.id] = p.status
	}
}
