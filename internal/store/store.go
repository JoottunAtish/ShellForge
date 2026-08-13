package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"

	_ "modernc.org/sqlite"
)

// Doc anchors for every user-facing failure this package can produce. Each
// one has a matching heading in docs/05-troubleshooting.md, added in the
// same commit that introduced it; guards_test.go enforces that locally.
const (
	anchorUnwritable      = "progress-db-unwritable"
	anchorCorrupt         = "progress-db-corrupt"
	anchorTooNew          = "progress-db-too-new"
	anchorMigrationFailed = "progress-db-migration-failed"
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

	for _, pragma := range []string{pragmaJournalModeWAL, pragmaBusyTimeout, pragmaSynchronousNormal} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close() // best effort: the pragma failure is what the caller needs to see
			return nil, classifyOpenError(path, fmt.Errorf("apply %s: %w", pragma, err))
		}
	}

	// Defence in depth on top of the 0700 directory. This only ever
	// restricts, on a path this package derived itself, and it never
	// deletes, truncates, or renames anything.
	//
	// TODO(v0.2): progress.db-wal and progress.db-shm are created later by
	// SQLite with the umask default, not mode 0600 directly, and are
	// protected by the 0700 directory rather than by their own mode.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close() // best effort: the chmod failure is what the caller needs to see
		return nil, classifyOpenError(path, fmt.Errorf("restrict permissions on %q: %w", path, err))
	}

	s := &Store{db: db, path: path}
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close() // best effort: the migration error is what the caller needs to see
		return nil, err
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

// classifyOpenError decides between the unwritable and corrupt doc anchors
// without string matching a driver error message. It opens path itself
// with os.O_RDWR and no os.O_CREATE, so the classification attempt itself
// creates or modifies nothing.
func classifyOpenError(path string, cause error) *ux.Error {
	if _, err := os.OpenFile(path, os.O_RDWR, 0o600); err != nil && errors.Is(err, fs.ErrPermission) {
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
