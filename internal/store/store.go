package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"

	"modernc.org/sqlite"
)

// Doc anchors for every user-facing failure this package can produce. Each
// one has a matching heading in docs/05-troubleshooting.md, added in the
// same commit that introduced it; guards_test.go enforces that locally.
const (
	anchorUnwritable      = "progress-db-unwritable"
	anchorCorrupt         = "progress-db-corrupt"
	anchorTooNew          = "progress-db-too-new"
	anchorMigrationFailed = "progress-db-migration-failed"
	anchorInUse           = "progress-db-in-use"
	anchorForeignDatabase = "progress-db-not-ours"
)

// Pragmas are literal constant strings, never built with fmt.Sprintf: a
// PRAGMA statement cannot take a bound parameter, so baking the value into
// the constant is the only way to keep this parameterized-queries-only and
// still set a pragma.
const (
	pragmaJournalModeWAL    = "PRAGMA journal_mode=WAL"
	pragmaBusyTimeout       = "PRAGMA busy_timeout=5000"
	pragmaSynchronousNormal = "PRAGMA synchronous=NORMAL"
)

// sqliteResultCodeMask isolates SQLite's primary result code from a code
// that also carries extended result code detail in the higher bits (for
// example SQLITE_BUSY_SNAPSHOT, which is SQLITE_BUSY with extra detail
// packed above the low byte). The modernc driver turns extended result
// codes on for every connection it opens, so a code read from
// (*sqlite.Error).Code() must be masked before it is compared against a
// primary code. See https://www.sqlite.org/rescode.html.
//
// sqliteBusy and sqliteLocked are declared here as our own named constants,
// not imported from modernc.org/sqlite/lib, which is a machine generated
// package not meant as a stable public surface. SQLite's numeric result
// codes are effectively part of its C ABI and have never been renumbered.
const (
	sqliteResultCodeMask = 0xff
	sqliteBusy           = 5 // SQLITE_BUSY
	sqliteLocked         = 6 // SQLITE_LOCKED
)

// pragmaRetries bounds how many times execPragmaWithRetry retries a pragma
// that fails with SQLITE_BUSY or SQLITE_LOCKED before giving up. The one
// pragma this actually protects in practice is journal_mode=WAL: converting
// a fresh delete-mode database to WAL needs a brief exclusive lock that
// pragmaBusyTimeout's own busy handler does not cover, so two Shellforge
// processes launched together can contend for it. A short bounded retry
// loop makes that race disappear without risking an unbounded wait.
const pragmaRetries = 8

// pragmaRetryDelay is the pause between retries of a busy or locked pragma.
// pragmaRetries * pragmaRetryDelay bounds the total wait to well under a
// second, which is unnoticeable next to everything else Open already does.
const pragmaRetryDelay = 25 * time.Millisecond

// Store is a handle to the Shellforge progress database.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens the SQLite database at path, creating its parent directories,
// setting WAL mode plus a busy timeout and NORMAL synchronous, and applying
// every pending migration.
//
// On a path that does not exist yet, Open creates the database and its
// parent directories and applies every embedded migration in order. On a
// database already at the highest embedded version, Open is a no-op beyond
// opening the connection: no migration re-runs and nothing is recreated.
func Open(ctx context.Context, path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := platform.EnsureDir(dir); err != nil {
		return nil, ux.Fail(
			"open the file where Shellforge records your progress",
			fmt.Errorf("create directory %q: %w", dir, err),
			fmt.Sprintf("Check that you can create files in %s, then run: shellforge doctor", dir),
			anchorUnwritable,
		)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, classifyOpenError(path, fmt.Errorf("open sqlite database %q: %w", path, err))
	}

	// A single connection is load bearing, not an optimization. busy_timeout
	// and synchronous are per connection pragmas, and database/sql pools
	// connections behind the scenes, so a pragma executed through
	// db.ExecContext would otherwise apply to one pooled connection and not
	// to the next one the pool opens under concurrent load. Capping the pool
	// at one connection is what makes the pragmas stick, and it also
	// serializes this process's own writers against each other.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db, path: path}

	// busy_timeout and synchronous first, ahead of every read below. Neither
	// touches the file on disk: they are connection-level settings, not a
	// format conversion, so applying them early is free and irreversible to
	// nothing. Doing so before the reads below matters: without a busy
	// handler set, a plain SELECT that meets contention gets an immediate
	// SQLITE_BUSY with no wait at all, where the old ordering (all three
	// pragmas, then the identity check) had busy_timeout active by the time
	// any read ran. journal_mode=WAL is deliberately not in this loop; see
	// below for why it waits.
	for _, pragma := range []string{pragmaBusyTimeout, pragmaSynchronousNormal} {
		if err := execPragmaWithRetry(ctx, db, pragma); err != nil {
			_ = db.Close() // best effort: the pragma failure is what the caller needs to see
			return nil, classifyOpenError(path, fmt.Errorf("apply %s: %w", pragma, err))
		}
	}

	// Prove the file is at least a readable SQLite database before asking
	// whether it is ours. A read failure here is corruption, a permission
	// problem, or contention, all of which classifyOpenError already tells
	// apart, and none of which means "not ours": refuseIfForeign's own
	// fail-closed rule (any read failure is refused as a foreign database)
	// is correct for identity questions and wrong for "can this even be
	// read", so that question is settled first, with the same classifier
	// every other read failure in this function already goes through.
	// looksLikeOurs succeeds with a false, nil result on a fresh or empty
	// file, so this does not disturb the adopted-as-fresh path. It also
	// means every successful Open now runs the same sqlite_master query
	// three times over (here, then again inside refuseIfForeign below, then
	// again inside Migrate's own call to refuseIfForeign): a deliberate,
	// accepted cost, since all three reads are cheap and read-only, and
	// splitting the question into "is this readable" and "is this ours"
	// is what lets a corrupt file classify as corrupt instead of foreign.
	if _, err := looksLikeOurs(ctx, db); err != nil {
		_ = db.Close() // best effort: the classified error is what the caller needs to see
		return nil, classifyOpenError(path, err)
	}

	// Provenance before the one pragma that touches the file. journal_mode=WAL
	// rewrites SQLite's own 100 byte file header (the format read and write
	// version bytes at offset 18 and 19, the low byte of the 4 byte change
	// counter at offset 27, and the low byte of the 4 byte version-valid-for
	// number at offset 95) and, for a database not already in WAL mode,
	// creates -wal and -shm sidecars next to it. Both are irreversible side
	// effects on a file this process may not own, so refuseIfForeign has to
	// run before that pragma, not after it as part of Migrate: Migrate still
	// calls it again as its own first step, which is a second, cheap
	// sqlite_master read on the path this check already took (now against a
	// file already proven readable, above), and is what keeps Migrate a safe
	// entry point on its own for a caller that does not go through Open. See
	// issue #110 for the byte-level measurement this ordering fixes and #90
	// for the refusal itself.
	if err := s.refuseIfForeign(ctx); err != nil {
		_ = db.Close() // best effort: the refusal is what the caller needs to see
		return nil, err
	}

	// journal_mode=WAL runs alone and last among the pragmas, once both
	// provenance and readability are settled: it is the one pragma that
	// converts the file, and execPragmaWithRetry's own retry loop is its
	// backstop against the exclusive lock the conversion needs, which
	// busy_timeout's ordinary wait does not fully cover. See pragmaRetries.
	if err := execPragmaWithRetry(ctx, db, pragmaJournalModeWAL); err != nil {
		_ = db.Close() // best effort: the pragma failure is what the caller needs to see
		return nil, classifyOpenError(path, fmt.Errorf("apply %s: %w", pragmaJournalModeWAL, err))
	}

	if err := s.Migrate(ctx); err != nil {
		_ = db.Close() // best effort: the migration error is what the caller needs to see
		return nil, err
	}

	// Defence in depth on top of the 0700 directory. This only ever
	// restricts, on a path this package derived itself, and it never
	// deletes, truncates, or renames anything.
	//
	// Runs after Migrate rather than right after the file first comes into
	// existence, so that a database Migrate refuses for a reason other than
	// ErrForeignDatabase, such as ErrSchemaTooNew, has had nothing at all
	// done to it beyond what the provenance check and the pragma loop
	// above already do: moving the chmod here, rather than making it
	// conditional on the outcome, avoids a signature change to Migrate. The
	// cost is that a fresh database sits at the process umask default for
	// the duration of the pragmas and the migrations rather than only the
	// pragmas; it is inside the 0700 directory EnsureDir created
	// throughout that window, and 001_init.sql creates only empty tables,
	// so no learner data exists in the file while it is briefly widened.
	//
	// TODO(v0.2): progress.db-wal and progress.db-shm are created later by
	// SQLite with the umask default, not mode 0600 directly, and are
	// protected by the 0700 directory rather than by their own mode.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close() // best effort: the chmod failure is what the caller needs to see
		return nil, classifyOpenError(path, fmt.Errorf("restrict permissions on %q: %w", path, err))
	}

	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB, for a layer 2 package that owns its
// own tables, such as internal/journal.
//
// Callers must use parameterized queries only, never build SQL with
// fmt.Sprintf, and must never close the returned handle: Store owns its
// lifecycle.
func (s *Store) DB() *sql.DB {
	return s.db
}

// execPragmaWithRetry runs pragma through db, retrying up to pragmaRetries
// times, with a pragmaRetryDelay pause between attempts, whenever the
// failure is SQLITE_BUSY or SQLITE_LOCKED. Any other failure, or ctx being
// cancelled or timing out, returns immediately: this is a short bounded
// wait for a one-off transition another process is finishing, never an
// unbounded sleep.
//
// This is what makes two Shellforge processes starting at the same moment
// converge instead of one of them failing: the first process to reach
// journal_mode=WAL holds a brief exclusive lock while it rewrites the file
// header, and pragmaBusyTimeout's own busy handler does not cover that one
// statement. See classifyOpenError for what happens if every retry is
// exhausted.
func execPragmaWithRetry(ctx context.Context, db *sql.DB, pragma string) error {
	var err error
	for attempt := 0; attempt < pragmaRetries; attempt++ {
		if _, err = db.ExecContext(ctx, pragma); err == nil {
			return nil
		}
		if !isBusyOrLocked(err) {
			return err
		}
		if attempt == pragmaRetries-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pragmaRetryDelay):
		}
	}
	return err
}

// isBusyOrLocked reports whether err is a *sqlite.Error whose primary
// result code is SQLITE_BUSY or SQLITE_LOCKED. It reaches the driver's own
// error type with errors.As rather than matching any part of Error(), so a
// wording change in the driver can never silently break this check.
func isBusyOrLocked(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	switch se.Code() & sqliteResultCodeMask {
	case sqliteBusy, sqliteLocked:
		return true
	default:
		return false
	}
}

// classifyOpenError decides between the in-use, unwritable, and corrupt doc
// anchors without string matching a driver error message.
//
// In-use is decided first, straight from the driver's own result code: if
// cause unwraps to a *sqlite.Error reporting SQLITE_BUSY or SQLITE_LOCKED,
// another process still holds the file after every retry in
// execPragmaWithRetry, and that is not corruption. Nothing about the file
// is wrong, so the remediation never suggests renaming or deleting it.
//
// Otherwise this opens path itself with os.O_RDWR and no os.O_CREATE, so
// the classification attempt itself creates or modifies nothing, and tells
// an unwritable path apart from a genuinely corrupt file. A probe failure
// matching fs.ErrNotExist is classified the same as fs.ErrPermission: a path
// that does not exist yet, or whose parent directory does not exist yet, is
// not corrupt, and the fix is the same one already given for a permission
// problem.
//
// The probe closes its handle immediately, and that close is load bearing
// rather than tidiness. Discarding the *os.File leaked a descriptor on every
// path that classified as corrupt, which Unix tolerates: an unlinked file
// with an open handle still goes away. Windows does not, so t.TempDir's
// cleanup could not remove progress.db and TestOpenOnACorruptFileFails and
// TestOpenOnACorruptFileLeavesItUntouched both failed on windows-latest with
// "The process cannot access the file because it is being used by another
// process" while passing on Linux. The platform difference hid the leak; it
// did not cause it.
//
// #nosec G304 -- path is the progress database path this package was handed,
// derived from platform.DatabasePath rather than from learner input or pack
// content, and it has already been opened by the sqlite driver by the time
// this runs. The open carries no os.O_CREATE and no os.O_TRUNC, so it cannot
// create or destroy anything; it exists only to read the errno that tells a
// permission problem apart from a corrupt file.
func classifyOpenError(path string, cause error) *ux.Error {
	if isBusyOrLocked(cause) {
		return ux.Fail(
			"open the file where Shellforge records your progress",
			cause,
			fmt.Sprintf("Another copy of Shellforge, or something else, is using %s. Close it, then run: shellforge doctor", path),
			anchorInUse,
		)
	}

	probe, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err == nil {
		_ = probe.Close() // nothing was read from it; the errno was the answer
	}
	if err != nil && (errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist)) {
		return ux.Fail(
			"open the file where Shellforge records your progress",
			cause,
			fmt.Sprintf("Check that you can create files in %s, then run: shellforge doctor", filepath.Dir(path)),
			anchorUnwritable,
		)
	}
	return ux.Fail(
		"read the file where Shellforge records your progress",
		cause,
		fmt.Sprintf("Rename %s to progress.db.broken, then run: shellforge doctor. Shellforge will start a new progress file. The levels you have finished will be forgotten, and nothing else is lost.", path),
		anchorCorrupt,
	)
}
