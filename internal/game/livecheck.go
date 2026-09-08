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
// single-flight: there is no lock over a pass, because there is nothing to
// contend. The transition map is touched only by Run's own goroutine.
type LiveChecker struct {
	session  *Session
	debounce time.Duration

	// notify carries one pending wakeup from Notify to Run. Capacity 1 with
	// a non-blocking send: a notify arriving while one is already pending,
	// or while a pass is running, is coalesced into it rather than queued,
	// which is what collapses a burst of commands into one follow-up pass.
	notify chan struct{}

	// transitions carries one slice of changed objectives per pass that
	// found any. Capacity 1 with a non-blocking send: a printer that fell
	// behind must not stall the pass loop, which in turn must not stall the
	// journal drain. A dropped batch is a cosmetic loss on a frame the
	// learner is not watching; the next pass re-derives status from real
	// state, and `check` is always the authoritative answer.
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

// NewLiveChecker returns a checker for s. A debounce of zero or less means
// DefaultLiveDebounce.
func NewLiveChecker(s *Session, debounce time.Duration) *LiveChecker {
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
		objs := l.session.checkCheap(ctx)
		if ctx.Err() != nil {
			return
		}

		if transitions := l.transition(objs); len(transitions) > 0 {
			select {
			case l.transitions <- transitions:
			default:
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

// transition compares objs against the checker's own memory of each cheap
// objective's last status, returns the ones worth reporting, and updates
// the memory to match.
//
// Reported: unknown to pass (the tick the feature exists for), fail to pass,
// and pass to fail (a regression the learner needs to know about).
// Suppressed: unknown to fail (which would print the whole checklist on the
// first command), pass to pass, fail to fail, and fail to error or timeout
// (a transient sandbox hiccup is not news).
func (l *LiveChecker) transition(objs []verify.ObjectiveResult) []verify.ObjectiveResult {
	var out []verify.ObjectiveResult
	for _, obj := range objs {
		prev, known := l.last[obj.ID]
		l.last[obj.ID] = obj.Status

		switch {
		case !known:
			if obj.Status == verify.StatusPass {
				out = append(out, obj)
			}
		case prev != obj.Status && (prev == verify.StatusPass || obj.Status == verify.StatusPass):
			out = append(out, obj)
		}
	}
	return out
}
