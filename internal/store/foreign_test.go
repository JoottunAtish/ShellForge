package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// writeForeignDatabase builds a plain SQLite database at path by running
// each of statements in order through a plain sql.Open, unrelated to this
// package's own Open. It must not enable WAL: several tests in this file
// need the fixture to stay a single file in delete mode, so a byte
// comparison of just that file has no -wal or -shm sidecars to trip over.
func writeForeignDatabase(t *testing.T, path string, statements ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open %s: %v", path, err)
	}
	defer db.Close()
	for _, stmt := range statements {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
}

// catalogueNames returns the sqlite_master names at path, read through a
// plain sql.Open unrelated to this package's own Open. Used both to assert a
// fixture's own precondition before calling Open, and to assert what a
// refused Open left behind afterward.
func catalogueNames(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open %s: %v", path, err)
	}
	defer db.Close()
	rows, err := db.QueryContext(context.Background(), "SELECT name FROM sqlite_master")
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan sqlite_master.name: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite_master: %v", err)
	}
	return names
}

// assertRemediationSuggestsNothingDestructive fails when a remediation tells
// the learner to disturb a file that may belong to another program.
//
// The path is stripped out before the words are looked for, because t.TempDir
// embeds the calling test's own name in the path and a test named
// ...Removed... or ...Renamed... would otherwise trip a naive match on its own
// directory rather than on the message.
func assertRemediationSuggestsNothingDestructive(t *testing.T, remediation, path string) {
	t.Helper()
	if remediation == "" {
		t.Fatal("Remediation is empty")
	}
	if !strings.Contains(remediation, path) {
		t.Errorf("Remediation does not name the path: %q", remediation)
	}
	lower := strings.ToLower(strings.ReplaceAll(remediation, path, ""))
	for _, bad := range []string{"delete", "rename", "overwrite", "move", "back up"} {
		if strings.Contains(lower, bad) {
			t.Errorf("Remediation says %q, which is unsafe for a file that may belong to another program: %q", bad, remediation)
		}
	}
}

// hashRegion returns a sha256 hex digest of b, so a mismatch prints a short
// fixed-width string instead of dumping a raw byte slice.
func hashRegion(t *testing.T, b []byte) string {
	t.Helper()
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestOpenRefusesAForeignDatabase covers #90's core acceptance criterion: a
// SQLite database Shellforge did not create is refused, not adopted. The
// "only sqlite internals" case is the negative control: a database that
// merely mentions sqlite_sequence, which this package's own databases also
// carry (events declares INTEGER PRIMARY KEY AUTOINCREMENT), must still open
// as fresh rather than being refused.
func TestOpenRefusesAForeignDatabase(t *testing.T) {
	tests := []struct {
		name        string
		statements  []string
		wantRefused bool
	}{
		{
			name:        "one unrelated table",
			statements:  []string{"CREATE TABLE bookmarks (url TEXT)"},
			wantRefused: true,
		},
		{
			name: "several unrelated tables",
			statements: []string{
				"CREATE TABLE bookmarks (url TEXT)",
				"CREATE TABLE notes (body TEXT)",
			},
			wantRefused: true,
		},
		{
			name: "a table and an index",
			statements: []string{
				"CREATE TABLE bookmarks (url TEXT)",
				"CREATE INDEX idx_bookmarks_url ON bookmarks(url)",
			},
			wantRefused: true,
		},
		{
			name: "only sqlite internals",
			statements: []string{
				"CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT)",
				"INSERT INTO t DEFAULT VALUES",
				"DROP TABLE t",
			},
			wantRefused: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "progress.db")
			writeForeignDatabase(t, dbPath, tt.statements...)

			if !tt.wantRefused {
				// SQLite keeps sqlite_sequence once created, even after the
				// table that needed it is dropped. Assert that precondition
				// before calling Open: without it, this case would silently
				// degenerate into "an empty database is fresh", which is
				// TestOpenTreatsAZeroByteFileAsFresh's job, and it would
				// pass for the wrong reason.
				names := catalogueNames(t, dbPath)
				hasSequence := false
				for _, n := range names {
					if n == "sqlite_sequence" {
						hasSequence = true
						continue
					}
					if !strings.HasPrefix(n, "sqlite_") {
						t.Fatalf("fixture precondition failed: unexpected non sqlite_ entry %q in sqlite_master", n)
					}
				}
				if !hasSequence {
					t.Fatal("fixture precondition failed: sqlite_sequence was not created by the drop")
				}
			}

			s, err := Open(context.Background(), dbPath)

			if !tt.wantRefused {
				if err != nil {
					t.Fatalf("Open failed against a database holding only SQLite internals: %v", err)
				}
				t.Cleanup(func() { s.Close() })

				all, err := migrations()
				if err != nil {
					t.Fatalf("migrations: %v", err)
				}
				want := all[len(all)-1].version
				got, err := s.SchemaVersion(context.Background())
				if err != nil {
					t.Fatalf("SchemaVersion: %v", err)
				}
				if got != want {
					t.Errorf("SchemaVersion = %d, want %d (highest embedded)", got, want)
				}
				return
			}

			if err == nil {
				// Close before failing: a foreign database Open wrongly
				// accepted still holds an open connection, and leaking it
				// makes t.TempDir's own cleanup fail on Windows with a
				// second, unrelated-looking error on top of this one.
				s.Close()
				t.Fatal("Open succeeded against a foreign database")
			}
			if !errors.Is(err, ErrForeignDatabase) {
				t.Errorf("error does not wrap ErrForeignDatabase: %v", err)
			}
			var uerr *ux.Error
			if !errors.As(err, &uerr) {
				t.Fatalf("error is not a *ux.Error: %v", err)
			}
			if uerr.DocAnchor != anchorForeignDatabase {
				t.Errorf("DocAnchor = %q, want %q", uerr.DocAnchor, anchorForeignDatabase)
			}
			if uerr.Remediation == "" {
				t.Fatal("Remediation is empty")
			}
			if !strings.Contains(uerr.Remediation, dbPath) {
				t.Errorf("Remediation does not name the path: %q", uerr.Remediation)
			}

			// Strip the path out first: t.TempDir embeds this test's own
			// name in the path, and a future case name could otherwise trip
			// one of these words by accident.
			withoutPath := strings.ReplaceAll(uerr.Remediation, dbPath, "")
			lower := strings.ToLower(withoutPath)
			for _, bad := range []string{"delete", "rename", "overwrite", "move", "back up"} {
				if strings.Contains(lower, bad) {
					t.Errorf("Remediation says %q, which is unsafe for a file that may belong to another program: %q", bad, uerr.Remediation)
				}
			}
		})
	}
}

// TestOpenLeavesAForeignDatabaseUntouched is the measured version of the
// byte-identity criterion, per plan90.md item 6 step B. The measurement (run
// before any #90 code existed, using this package's own already-shipped
// execPragmaWithRetry directly) found: length identical at 8192 bytes before
// and after; differing bytes only at offsets 18, 19, 27, and 95; nothing at
// or above offset 100 differed. That matches SQLite's own 100 byte file
// header ending exactly at offset 100 and confirms Finding 2: Open's own
// PRAGMA journal_mode=WAL runs before Migrate ever sees the file, and
// converting a delete-mode database to WAL rewrites the file-format version
// bytes at offsets 18 and 19, the change counter at 24-27, and the
// version-valid-for number at 92-95. That conversion is done by the time
// this refusal runs and cannot be undone without leaving the database in
// delete mode, which needs the identity gate to run before the WAL pragma:
// a bigger change than either #90 or #92 makes, filed as a follow-up. So
// this test asserts precisely what is true rather than literal byte
// identity: the length is unchanged, everything from offset 100 onward is
// unchanged, and the foreign table's own schema text and rows are unchanged.
func TestOpenLeavesAForeignDatabaseUntouched(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	writeForeignDatabase(t, dbPath,
		"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)",
		"INSERT INTO notes (id, body) VALUES (1, 'left alone')",
	)

	readNotes := func() (schemaSQL string, rows [][2]any) {
		t.Helper()
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("sql.Open: %v", err)
		}
		defer db.Close()

		if err := db.QueryRowContext(context.Background(),
			"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'notes'",
		).Scan(&schemaSQL); err != nil {
			t.Fatalf("read notes schema: %v", err)
		}

		result, err := db.QueryContext(context.Background(), "SELECT id, body FROM notes ORDER BY id")
		if err != nil {
			t.Fatalf("query notes: %v", err)
		}
		defer result.Close()
		for result.Next() {
			var id int64
			var body string
			if err := result.Scan(&id, &body); err != nil {
				t.Fatalf("scan notes row: %v", err)
			}
			rows = append(rows, [2]any{id, body})
		}
		if err := result.Err(); err != nil {
			t.Fatalf("iterate notes: %v", err)
		}
		return schemaSQL, rows
	}

	beforeSchema, beforeRows := readNotes()
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read file before: %v", err)
	}

	s, err := Open(context.Background(), dbPath)
	if err == nil {
		// t.Errorf rather than t.Fatalf: even if this assertion regresses,
		// the byte comparison below is worth reporting from the same run.
		// Close immediately, before reading the file back, rather than only
		// on test cleanup: SQLite's WAL mode defers new pages into the main
		// file until a checkpoint, which an ordinary caller triggers by
		// closing, and this comparison needs to see what such a caller
		// would actually see on disk, not an artificially uncheckpointed
		// snapshot that looks unchanged only because nothing forced it.
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store after Open unexpectedly succeeded: %v", closeErr)
		}
		t.Errorf("Open succeeded against a foreign database")
	}

	afterSchema, afterRows := readNotes()
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read file after: %v", err)
	}

	if len(before) != len(after) {
		t.Errorf("file length changed: before %d, after %d", len(before), len(after))
	}
	if len(before) < 100 || len(after) < 100 {
		t.Fatalf("fixture is smaller than SQLite's 100 byte file header (before %d, after %d): cannot assert the region this test cares about", len(before), len(after))
	}
	if got, want := hashRegion(t, after[100:]), hashRegion(t, before[100:]); got != want {
		t.Errorf("bytes from offset 100 onward changed: before %s, after %s", want, got)
	}

	if afterSchema != beforeSchema {
		t.Errorf("notes table schema changed:\nbefore: %s\nafter:  %s", beforeSchema, afterSchema)
	}
	if len(afterRows) != len(beforeRows) {
		t.Fatalf("notes row count changed: before %d, after %d", len(beforeRows), len(afterRows))
	}
	for i := range beforeRows {
		if beforeRows[i] != afterRows[i] {
			t.Errorf("notes row %d changed: before %v, after %v", i, beforeRows[i], afterRows[i])
		}
	}
}

// TestOpenLeavesAForeignDatabaseModeUnchanged is a separate test from
// TestOpenLeavesAForeignDatabaseUntouched on purpose: it asserts a chmod did
// NOT happen, which root cannot hide (unlike the mode assertions elsewhere
// in this package, no root skip is needed here), but Windows chmod cannot
// express 0644 at all, so on Windows the before-equals-after check would
// hold trivially and prove nothing. Skipping beats a half-vacuous assertion.
func TestOpenLeavesAForeignDatabaseModeUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode bits are not meaningful on Windows")
	}

	dbPath := filepath.Join(t.TempDir(), "progress.db")
	writeForeignDatabase(t, dbPath, "CREATE TABLE t (x INTEGER)")
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatalf("chmod 0644: %v", err)
	}

	before, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	if s, err := Open(context.Background(), dbPath); err == nil {
		s.Close()
		t.Errorf("Open succeeded against a foreign database")
	}

	after, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if before.Mode().Perm() != after.Mode().Perm() {
		t.Errorf("file mode changed: before %o, after %o", before.Mode().Perm(), after.Mode().Perm())
	}
	if after.Mode().Perm() != 0o644 {
		t.Errorf("file mode = %o, want 0644 (unchanged)", after.Mode().Perm())
	}
}

// TestOpenWritesNoShellforgeTableIntoAForeignDatabase asserts the refusal is
// silent as well as total: not one of this package's own table names
// appears in the foreign file afterward, and the foreign table itself is
// still there.
func TestOpenWritesNoShellforgeTableIntoAForeignDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	writeForeignDatabase(t, dbPath, "CREATE TABLE t (x INTEGER)")

	if s, err := Open(context.Background(), dbPath); err == nil {
		// t.Errorf, not t.Fatalf: closing first and continuing lets the same
		// pre-fix run also report exactly which Shellforge tables appeared.
		s.Close()
		t.Errorf("Open succeeded against a foreign database")
	}

	names := catalogueNames(t, dbPath)
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	for _, shellforgeTable := range []string{"events", "schema_version", "sqlite_sequence"} {
		if nameSet[shellforgeTable] {
			t.Errorf("found %q in sqlite_master after a refused Open, which Shellforge must never have written", shellforgeTable)
		}
	}
	if !nameSet["t"] {
		t.Error("the foreign table t is missing from sqlite_master after a refused Open")
	}
}

// TestOpenOnAShellforgeDatabaseAtARecordedVersionStillOpens proves the
// current == 0 scoping: a database Shellforge already owns, even one that
// has since grown a table Shellforge did not create, must never be refused.
// The gate only ever looks at a database recording no schema version at
// all.
func TestOpenOnAShellforgeDatabaseAtARecordedVersionStillOpens(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	wantVersion, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	writeForeignDatabase(t, dbPath, "CREATE TABLE bookmarks (url TEXT)", "INSERT INTO bookmarks (url) VALUES ('https://example.invalid')")

	s2, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { s2.Close() })

	gotVersion, err := s2.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion after reopen: %v", err)
	}
	if gotVersion != wantVersion {
		t.Errorf("SchemaVersion changed: %d -> %d", wantVersion, gotVersion)
	}

	var url string
	row := s2.DB().QueryRowContext(context.Background(), "SELECT url FROM bookmarks LIMIT 1")
	if err := row.Scan(&url); err != nil {
		t.Errorf("bookmarks table missing after reopen: %v", err)
	}
}

// TestOpenTreatsAZeroByteFileAsFresh is a pinning test, by the ticket's own
// reasoning: a zero-byte file already opened as fresh before this ticket,
// and this asserts that nobody makes count > 0 the wrong direction for an
// empty database (see mutation M3).
func TestOpenTreatsAZeroByteFileAsFresh(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	if err := os.WriteFile(dbPath, nil, 0o600); err != nil {
		t.Fatalf("write zero byte file: %v", err)
	}

	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	all, err := migrations()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	want := all[len(all)-1].version

	got, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if got != want {
		t.Errorf("SchemaVersion = %d, want %d (highest embedded)", got, want)
	}

	var name string
	row := s.DB().QueryRowContext(context.Background(),
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'events'")
	if err := row.Scan(&name); err != nil {
		t.Errorf("events table missing from a zero byte database treated as fresh: %v", err)
	}
}

// TestRefuseIfForeignFailsClosedWhenItCannotRead covers rule 8 in the
// destructive-safety skill: a sqlite_master read that did not complete is
// not evidence the file is ours, so it is refused with the same anchor as a
// confirmed foreign database rather than allowed through.
func TestRefuseIfForeignFailsClosedWhenItCannotRead(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s := &Store{db: db, path: dbPath}
	err = s.refuseIfForeign(context.Background())
	if err == nil {
		t.Fatal("refuseIfForeign succeeded reading from a closed database")
	}
	if !errors.Is(err, ErrForeignDatabase) {
		t.Errorf("error does not wrap ErrForeignDatabase: %v", err)
	}
	var uerr *ux.Error
	if !errors.As(err, &uerr) {
		t.Fatalf("error is not a *ux.Error: %v", err)
	}
	if uerr.DocAnchor != anchorForeignDatabase {
		t.Errorf("DocAnchor = %q, want %q", uerr.DocAnchor, anchorForeignDatabase)
	}
	if uerr.Remediation == "" {
		t.Fatal("Remediation is empty")
	}
	withoutPath := strings.ReplaceAll(uerr.Remediation, dbPath, "")
	lower := strings.ToLower(withoutPath)
	for _, bad := range []string{"delete", "rename", "overwrite", "move", "back up"} {
		if strings.Contains(lower, bad) {
			t.Errorf("Remediation says %q, which is unsafe when the file's identity could not be confirmed: %q", bad, uerr.Remediation)
		}
	}
}

// TestUserObjectCountIgnoresSQLiteInternalObjects is the supporting unit for
// the sqlite_ exclusion, covered independently of the fresh-path case so
// mutation M4 has a witness that does not merely ride on M3's.
func TestUserObjectCountIgnoresSQLiteInternalObjects(t *testing.T) {
	t.Run("one user table plus sqlite_sequence", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "progress.db")
		writeForeignDatabase(t, dbPath,
			"CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT)",
			"INSERT INTO t DEFAULT VALUES",
		)

		names := catalogueNames(t, dbPath)
		hasSequence := false
		for _, n := range names {
			if n == "sqlite_sequence" {
				hasSequence = true
			}
		}
		if !hasSequence {
			t.Fatal("fixture precondition failed: sqlite_sequence was not created")
		}

		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("sql.Open: %v", err)
		}
		defer db.Close()

		count, err := userObjectCount(context.Background(), db)
		if err != nil {
			t.Fatalf("userObjectCount: %v", err)
		}
		if count != 1 {
			t.Errorf("count = %d, want 1 (sqlite_sequence must not be counted)", count)
		}
	})

	t.Run("only sqlite_sequence survives", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "progress.db")
		writeForeignDatabase(t, dbPath,
			"CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT)",
			"INSERT INTO t DEFAULT VALUES",
			"DROP TABLE t",
		)

		names := catalogueNames(t, dbPath)
		for _, n := range names {
			if n != "sqlite_sequence" {
				t.Fatalf("fixture precondition failed: unexpected entry %q survived the drop", n)
			}
		}

		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatalf("sql.Open: %v", err)
		}
		defer db.Close()

		count, err := userObjectCount(context.Background(), db)
		if err != nil {
			t.Fatalf("userObjectCount: %v", err)
		}
		if count != 0 {
			t.Errorf("count = %d, want 0", count)
		}
	})
}

// TestOpenRefusesAForeignDatabaseThatHasItsOwnSchemaVersionTable is the
// regression test for the gate's worst failure mode.
//
// schema_version is a common name: older Flyway and a great deal of hand
// rolled migration code create a table called exactly that. An earlier
// version of this gate ran only when the recorded version was zero, so a file
// holding someone else's schema_version reached Migrate's version arithmetic
// instead, and both arms down there tell the learner to rename the file.
// Telling a learner to rename another program's database is the one outcome
// this gate exists to make impossible, so all three shapes are covered: a
// version column this package cannot parse, one it can parse as a number
// newer than it understands, and a table with no rows at all.
func TestOpenRefusesAForeignDatabaseThatHasItsOwnSchemaVersionTable(t *testing.T) {
	for _, tt := range []struct {
		name       string
		statements []string
	}{
		{
			name: "a version this package cannot parse",
			statements: []string{
				"CREATE TABLE customers (id INTEGER, name TEXT)",
				"CREATE TABLE schema_version (version TEXT)",
				"INSERT INTO schema_version (version) VALUES ('1.2.3')",
			},
		},
		{
			name: "a numeric version newer than this build",
			statements: []string{
				"CREATE TABLE customers (id INTEGER, name TEXT)",
				"CREATE TABLE schema_version (version INTEGER)",
				"INSERT INTO schema_version (version) VALUES (999)",
			},
		},
		{
			name: "a version table with no rows",
			statements: []string{
				"CREATE TABLE customers (id INTEGER, name TEXT)",
				"CREATE TABLE schema_version (version INTEGER)",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "progress.db")
			writeForeignDatabase(t, dbPath, tt.statements...)

			before, err := os.ReadFile(dbPath)
			if err != nil {
				t.Fatalf("read the fixture: %v", err)
			}

			s, err := Open(context.Background(), dbPath)
			if err == nil {
				s.Close()
				t.Fatal("Open succeeded against a foreign database carrying its own schema_version table")
			}

			if !errors.Is(err, ErrForeignDatabase) {
				t.Errorf("errors.Is(err, ErrForeignDatabase) = false, want true: a foreign schema_version must not be mistaken for provenance. got %v", err)
			}

			var uerr *ux.Error
			if !errors.As(err, &uerr) {
				t.Fatalf("error is not a *ux.Error: %v", err)
			}
			if uerr.DocAnchor != anchorForeignDatabase {
				t.Errorf("DocAnchor = %q, want %q", uerr.DocAnchor, anchorForeignDatabase)
			}

			// The whole point: no arm reachable from this input may tell the
			// learner to disturb a file that is not ours.
			assertRemediationSuggestsNothingDestructive(t, uerr.Remediation, dbPath)

			// And the file itself is still theirs.
			for _, name := range []string{"events", "sqlite_sequence"} {
				for _, got := range catalogueNames(t, dbPath) {
					if got == name {
						t.Errorf("Shellforge wrote %q into a foreign database", name)
					}
				}
			}
			after, err := os.ReadFile(dbPath)
			if err != nil {
				t.Fatalf("read the fixture back: %v", err)
			}
			if len(before) != len(after) {
				t.Errorf("file length changed: before %d, after %d", len(before), len(after))
			}
		})
	}
}

// TestOpenRefusesADatabaseWhoseOnlyObjectsAreViews covers the other way a
// foreign file used to slip through.
//
// The catalogue query filtered on type = 'table', so a database whose only
// user objects were views counted as empty and was adopted: Shellforge wrote
// its own tables into it and narrowed its mode. internal/store/doc.go and
// docs/05-troubleshooting.md both promise that never happens, and a promise a
// learner reads has to be true for every shape, not only for tables.
func TestOpenRefusesADatabaseWhoseOnlyObjectsAreViews(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	writeForeignDatabase(t, dbPath,
		"CREATE VIEW saved AS SELECT 1 AS id, 'a value' AS payload",
	)

	// Precondition: the fixture really has no tables, so this test cannot
	// pass for the trivial reason that a table was present after all.
	for _, name := range catalogueNames(t, dbPath) {
		if name == "saved" {
			continue
		}
		t.Fatalf("fixture precondition failed: unexpected catalogue entry %q", name)
	}

	s, err := Open(context.Background(), dbPath)
	if err == nil {
		s.Close()
		t.Fatal("Open succeeded against a database whose only object is a view")
	}
	if !errors.Is(err, ErrForeignDatabase) {
		t.Errorf("errors.Is(err, ErrForeignDatabase) = false, want true: got %v", err)
	}

	for _, got := range catalogueNames(t, dbPath) {
		if got == "events" || got == "schema_version" {
			t.Errorf("Shellforge wrote %q into a database it did not create", got)
		}
	}
}

// TestAFreshlyMigratedDatabaseIsProvablyOurs guards the ourObjectNames pair
// against a future migration.
//
// looksLikeOurs decides provenance from two hardcoded object names. If a
// later migration renames either one, or 001_init.sql stops creating both in
// the same transaction, then every existing progress database silently stops
// being provably ours: the gate would start counting the learner's own tables
// as foreign and refuse to open their real progress file. This test fails the
// moment that happens, which is the only reason it is safe for looksLikeOurs
// to name the two tables literally.
func TestAFreshlyMigratedDatabaseIsProvablyOurs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open on a fresh path: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	ours, err := looksLikeOurs(context.Background(), s.db)
	if err != nil {
		t.Fatalf("looksLikeOurs: %v", err)
	}
	if !ours {
		t.Errorf("a database this package just created is not recognised as ours: looksLikeOurs = false. Both %q and %q must exist after the migrations, or the foreign gate will refuse real progress files",
			ourVersionTable, ourEventsTable)
	}
}
