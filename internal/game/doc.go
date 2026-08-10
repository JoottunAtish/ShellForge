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
package game
