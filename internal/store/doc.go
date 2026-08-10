// Package store is the SQLite persistence layer for progress, journal events,
// and achievements.
//
// Layer L0. May import internal/platform. Must not import anything above it.
//
// WAL mode. Migrations are embedded, forward-only, and applied on open. Never
// hand-edit a migration that has shipped. Use parameterized queries only; never
// build SQL with fmt.Sprintf, even for values that look internal.
package store
