// Package bus is the L4 in-memory event bus that the game orchestrator,
// scoring, and achievements subscribe to. It sits at layer L4, the same
// layer as its parent internal/game, and imports the standard library only:
// it imports nothing from internal/, so nothing above it can be pulled in
// through it and it introduces no upward or sideways layer edge.
//
// Publish is synchronous and delivers in subscription order, over a copy of
// the subscriber list taken at the start of each dispatch round: a handler
// may subscribe or unsubscribe during a fan-out without corrupting the
// iteration, and the change takes effect starting with the next event
// rather than the one already in flight.
//
// Re-entrancy is handled by a per-dispatch queue rather than a recursive
// call. A Publish that arrives while a dispatch is already in flight,
// whether nested from inside a handler on the same goroutine or issued
// concurrently from another goroutine, enqueues its event and returns; the
// one active dispatcher drains that queue itself, in enqueue order, once its
// current round of subscribers has all run, before it returns. A mutex is
// never held while a handler runs, which is what lets a handler call
// Subscribe, unsubscribe, or Publish without deadlocking against its own
// bus.
//
// A dispatch is bounded at 1000 events per top-level Publish call, counting
// the original event plus every event queued during it. A subscriber that
// republishes the same event forever would otherwise hang the bus, since
// such a loop keeps the queue oscillating between length zero and one rather
// than growing; counting total events dispatched catches it where a queue
// length bound would not. Tripping the bound drops the remaining queue,
// reports once through the registered error handler with the sentinel error
// ErrMaxDispatchDepthExceeded, and lets Publish return normally rather than
// hang.
//
// A subscriber that panics is recovered per handler: its siblings still run
// and Publish still returns normally. The registered error handler receives
// only the subscriber's name and a stack trace, never the Event that was
// being delivered. This is deliberate: CommandExecuted.Raw is the literal
// text of a command the learner typed and may contain a password or a
// token, so it is secret material, and no panic report may fold the event
// payload into what it hands back.
package bus
