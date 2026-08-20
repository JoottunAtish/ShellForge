// Package store is the SQLite persistence layer for progress, journal events,
// and achievements.
//
// Layer L0. May import internal/platform, internal/platform/ux, and
// modernc.org/sqlite. Must not import anything else, and
// internal/archtest/dependencies_test.go confines modernc.org/sqlite to this
// package alone: nothing above it may open its own connection to the
// progress database.
//
// Open takes a context.Context, ahead of the ticket that first named this
// package's signature, because Open creates directories, creates a file,
// chmods it, and runs migrations, and every one of those touches the
// filesystem. Migrate already needed a context for the same reason; Open now
// matches it.
//
// WAL mode, a 5 second busy timeout, and NORMAL synchronous are set on every
// Open, on a connection pool capped at one connection. The single-connection
// cap is not an optimization: busy_timeout and synchronous are per
// connection pragmas, and database/sql pools connections, so without the cap
// a pragma set through db.ExecContext would apply to one pooled connection
// and silently not to the next one the pool opens. NORMAL synchronous is
// what keeps Append off fsync on every command; the accepted cost is that a
// power loss can lose the last few journal entries, which is fine because
// the journal is never evidence for scoring.
//
// Migrations are embedded under schema/, forward-only, named NNN_name.sql,
// and applied on Open. Never hand-edit a migration that has shipped; add a
// new NNN_name.sql instead. A database recording a schema version newer than
// this build understands is refused with ErrSchemaTooNew rather than
// migrated backward: there are no down migrations.
//
// DB() is the escape hatch for a layer 2 package that owns its own tables,
// such as internal/journal: parameterized queries only, never build SQL with
// fmt.Sprintf even for a value that looks internal, and never close the
// returned handle.
//
// This package deletes nothing belonging to a learner. The only DELETE
// anywhere in this package is DELETE FROM schema_version, inside a
// migration's own transaction, on a single-column table this package owns
// and immediately rewrites with a single row. Every user-facing failure's
// remediation tells the learner to rename their progress file, never to
// delete it, except progress-db-in-use: the file is not wrong in that case,
// so its remediation says to close the other process instead. It also
// never writes into a database it did not create: Open refuses, through
// ErrForeignDatabase, any file that records no schema version and already
// holds a table this package did not put there, before the WAL pragma
// touches its header and before Migrate applies a single statement to it.
// That ordering is issue #110's fix: journal_mode=WAL used to run first and
// rewrite the header of a database Open went on to refuse, which is a
// smaller wrong than writing to it but still not untouched. See the six doc
// anchors this package declares and their headings in
// docs/05-troubleshooting.md.
package store
