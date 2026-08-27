// Package game is the session orchestrator, curriculum DAG, scoring, hint
// ladder, achievements, and event bus.
//
// Layer L4. May import internal/content, internal/verify, internal/runtime, and
// below.
//
// This package must never import a runtime implementation. It knows the Runtime
// interface and nothing about Docker or WSL. If you need backend information
// here, add it to Runtime.Capabilities(). The layer test in internal/archtest
// enforces this and it is the rule that keeps "drop WSL" cheap.
//
// Game mechanics subscribe to the event bus rather than being wired into the
// orchestrator. That is what makes achievements and scoring pluggable.
//
// What exists today is Session (load a level, set its world up, hand out the
// briefing and the objective checklist, answer `check`, tear the world down),
// the Orchestrator wrapped around one Session, and JournalSink. There is no
// scoring, no XP, no hint ladder, no prerequisite gating and no unlock state.
// Those are Day 4, and the paragraph above describes where they will attach
// rather than what is here now.
//
// JournalSink (journalsink.go) drains the in-sandbox command journal into the
// events table and publishes one CommandExecuted per command, which is what
// gives the efficiency bonus and the command-counting achievements real
// evidence to read. Layer 4 is the lowest place that can hold it: it needs a
// journal.Collector, which needs a runtime.Session, and it needs the bus.
// Nothing wires it up yet, so no Session or Orchestrator calls Drain; that is
// a later Day 4 task.
//
// Two seams keep this package testable and honestly layered, and both follow
// the pattern its neighbours already use. Verifier is declared here rather than
// imported as *verify.Engine, the same way internal/content declares
// TypeChecker and internal/verify declares JournalReader: the consumer owns the
// interface, so a Session can be exercised with a two-method fake and no
// registry, sandbox or real level. And a Session is handed an open
// runtime.Session that it borrows and never closes, because one sandbox session
// outlives one level, and because resolving or provisioning a backend would
// mean importing a runtime implementation, which the rule above forbids.
//
// This package also holds the only conversion between internal/content's
// CheckSpec and internal/verify's Spec. Those two are peers at layer 3 and
// neither imports the other, which is what keeps the check registry independent
// of the YAML shape; something has to sit above both and join them, and layer 4
// is the lowest place that can.
package game
