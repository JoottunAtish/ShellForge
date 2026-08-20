package bus

import (
	"context"
	"errors"
	"runtime/debug"
	"sync"
)

// maxDispatchDepth bounds the number of events delivered within a single
// top-level Publish call, counting the outer event plus every event queued
// during it. It exists to catch a subscriber that republishes an event
// forever: such a loop keeps the queue oscillating between length 0 and 1
// (pop one, it appends one), so a bound on queue length would never trip.
// Counting the total events dispatched catches it instead. The deepest
// legitimate cascade in this game is single digit (a LevelPassed unlocking a
// handful of achievements, none of which re-cascades), so 1000 is three
// orders of magnitude of headroom, and 1000 synchronous value-type dispatches
// complete in well under a millisecond, so tripping the bound aborts almost
// instantly rather than hanging.
const maxDispatchDepth = 1000

// overflowSubscriber is the synthetic subscriber name reported to the error
// handler when a dispatch trips maxDispatchDepth. It is not the name of any
// real subscriber.
const overflowSubscriber = "bus: dispatch depth exceeded"

// ErrMaxDispatchDepthExceeded is the sentinel passed to the error handler,
// as the recovered value cast to error, when a single top-level Publish
// dispatches more than maxDispatchDepth events. Callers can compare against
// it with errors.Is after a type assertion to error.
var ErrMaxDispatchDepthExceeded = errors.New("game/bus: max dispatch depth exceeded")

// Handler is a subscriber's callback. It receives the ctx passed to the
// Publish call it is running under (see Publish for how that context is
// chosen when the event was queued rather than published directly), and the
// Event itself. A Handler must not block indefinitely: Publish does not
// return until every subscriber for the current dispatch, including every
// event queued during it, has run.
type Handler func(context.Context, Event)

// Option configures a Bus at construction time. See New.
type Option func(*Bus)

// WithErrorHandler registers fn to be called whenever a subscriber panics or
// a dispatch trips maxDispatchDepth. fn receives the subscriber's name (or
// overflowSubscriber), the recovered panic value (or ErrMaxDispatchDepthExceeded
// as an error), and a stack trace. fn is never handed the Event itself:
// CommandExecuted.Raw is secret material, and the bus never folds an event
// payload into an error report. A Bus with no error handler still contains
// panics and overflows; it just does not report them.
func WithErrorHandler(fn func(subscriberName string, recovered any, stack []byte)) Option {
	return func(b *Bus) { b.onError = fn }
}

// subscriber pairs a Handler with the identity Subscribe hands back an
// unsubscribe closure for. id is used, not a slice index, because an index
// would go stale the moment an earlier subscriber is removed and every later
// one shifts position.
type subscriber struct {
	id   uint64
	name string
	fn   Handler
}

// Bus is a synchronous, typed, in-memory event dispatcher. The zero value
// returned by New is ready to use: Publish on a Bus with no subscribers
// returns immediately, allocates nothing, and starts no goroutine.
//
// At most one goroutine ever fans out an event at a time. mu is never held
// while a handler runs, so a handler may freely call Subscribe, unsubscribe,
// or Publish without deadlocking against its own bus. See Publish for the
// full concurrency and re-entrancy contract.
type Bus struct {
	mu      sync.Mutex
	subs    []subscriber // subscription order preserved
	nextID  uint64
	onError func(subscriberName string, recovered any, stack []byte)

	dispatching bool    // true while a top-level Publish is fanning out or draining
	queue       []Event // events published during an active dispatch, drained in order
}

// New constructs a Bus, applying every opts in order.
func New(opts ...Option) *Bus {
	b := &Bus{}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Subscribe registers fn under name and returns a function that removes it.
// name labels the subscriber for WithErrorHandler's reports; an empty name
// is never an error, since it is only ever used as that label. A nil fn is
// ignored: Subscribe adds nothing and returns a no-op unsubscribe, since
// Handler has no error return with which to reject it and this package never
// panics outside a genuinely impossible state.
//
// The returned unsubscribe is safe to call more than once (the second call
// is a no-op), and safe to call from inside a handler, including a handler
// unsubscribing itself or another subscriber. A subscriber added during an
// in-flight Publish takes effect starting with the next event, never the one
// currently being delivered, because Publish fans out over a copy of the
// subscriber list taken at the start of each dispatch round.
func (b *Bus) Subscribe(name string, fn Handler) (unsubscribe func()) {
	if fn == nil {
		return func() {}
	}

	b.mu.Lock()
	b.nextID++
	id := b.nextID
	b.subs = append(b.subs, subscriber{id: id, name: name, fn: fn})
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i := range b.subs {
			if b.subs[i].id == id {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				return
			}
		}
	}
}

// Publish delivers ev to every current subscriber, in subscription order,
// and returns only after all of them have run. This "returns after the last
// handler" guarantee applies to the dispatching call, the one that found no
// dispatch already in progress. A Publish called concurrently from another
// goroutine while a dispatch is in flight enqueues its event and returns
// before that event has been delivered (see below).
//
// If a subscriber, while running, calls Publish again (directly, or on
// another goroutine while this dispatch is still in flight), that call does
// not recurse: it enqueues its event and returns immediately. The dispatcher
// already fanning out, the one Publish call that found no dispatch already
// in progress, drains that queue itself, in enqueue order, once its current
// round of subscribers has all run, before it returns. So a nested or
// concurrent Publish's event is always delivered after the dispatch that was
// active when it was published, never interleaved with it, and never lost
// or delivered twice. The context used for a queued event's delivery is the
// outermost Publish's ctx, not the ctx of the call that enqueued it: the
// queue stores Event values only, never a context, and a nested publish is
// causally within the outer dispatch's cancellation scope.
//
// A dispatch that would deliver more than maxDispatchDepth events (the
// original event plus everything queued during it) stops early: the
// remaining queue is dropped, and the registered error handler, if any, is
// called once with overflowSubscriber and ErrMaxDispatchDepthExceeded.
// Publish still returns normally.
//
// A subscriber that panics does not stop the fan-out or propagate out of
// Publish: it is recovered, its remaining siblings still run, and the
// registered error handler, if any, is called with the subscriber's name and
// a stack trace, never the event itself.
//
// Publish with no subscribers returns immediately, without allocating and
// without starting a goroutine.
func (b *Bus) Publish(ctx context.Context, ev Event) {
	b.mu.Lock()
	if len(b.subs) == 0 {
		b.mu.Unlock()
		return
	}
	if b.dispatching {
		b.queue = append(b.queue, ev)
		b.mu.Unlock()
		return
	}
	b.dispatching = true

	dispatched := 0
	for {
		dispatched++
		if dispatched > maxDispatchDepth {
			onErr := b.onError
			b.queue = nil
			b.dispatching = false
			b.mu.Unlock()
			if onErr != nil {
				onErr(overflowSubscriber, ErrMaxDispatchDepthExceeded, debug.Stack())
			}
			return
		}

		subs := make([]subscriber, len(b.subs))
		copy(subs, b.subs)
		onErr := b.onError
		b.mu.Unlock()

		for _, s := range subs {
			b.deliver(ctx, s, ev, onErr)
		}

		b.mu.Lock()
		if len(b.queue) == 0 {
			b.dispatching = false
			b.mu.Unlock()
			return
		}
		ev = b.queue[0]
		b.queue[0] = nil      // drop the reference so a delivered event, which may
		b.queue = b.queue[1:] // hold CommandExecuted.Raw, is not retained by the backing array
	}
}

// deliver runs a single subscriber's handler, recovering and reporting a
// panic rather than letting it escape and take down the whole dispatch.
func (b *Bus) deliver(ctx context.Context, s subscriber, ev Event, onErr func(string, any, []byte)) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			if onErr != nil {
				onErr(s.name, r, stack)
			}
		}
	}()
	s.fn(ctx, ev)
}
