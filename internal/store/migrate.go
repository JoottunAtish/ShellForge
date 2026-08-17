package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

//go:embed schema/*.sql
var schemaFS embed.FS

// ErrSchemaTooNew is returned, wrapped in a *ux.Error, when the progress
// database records a schema version newer than this build understands.
// Nothing is written or deleted when this happens: see Migrate.
var ErrSchemaTooNew = errors.New("progress database schema is newer than this build")

// ErrForeignDatabase reports that path holds a SQLite database that
// Shellforge did not create, so Open refused to migrate it.
var ErrForeignDatabase = errors.New("store: not a Shellforge progress database")

// migration is one embedded schema/NNN_name.sql file.
type migration struct {
	version int
	name    string
	sql     string
}

// migrations reads schemaFS, parses each NNN_name.sql filename's numeric
// prefix, and returns them sorted ascending by version. It errors on a
// duplicate version, a gap, a version below 1, an unparsable filename, or
// an empty file. A malformed embedded filename is a programming error
// caught here and surfaced as an error, never a panic: it can only happen
// if this repository's own schema/ directory is malformed, never from
// anything a learner did.
func migrations() ([]migration, error) {
	entries, err := schemaFS.ReadDir("schema")
	if err != nil {
		return nil, fmt.Errorf("read embedded schema directory: %w", err)
	}

	out := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		version, err := parseMigrationVersion(name)
		if err != nil {
			return nil, fmt.Errorf("embedded migration %q: %w", name, err)
		}
		content, err := schemaFS.ReadFile(path.Join("schema", name))
		if err != nil {
			return nil, fmt.Errorf("read embedded migration %q: %w", name, err)
		}
		if strings.TrimSpace(string(content)) == "" {
			return nil, fmt.Errorf("embedded migration %q is empty", name)
		}
		out = append(out, migration{version: version, name: name, sql: string(content)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })

	for i, m := range out {
		want := i + 1
		if m.version != want {
			return nil, fmt.Errorf("embedded migrations are not ordered and gap free: expected version %d, found %d (%s)", want, m.version, m.name)
		}
	}

	return out, nil
}

// parseMigrationVersion extracts the leading NNN from a filename shaped
// NNN_name.sql.
func parseMigrationVersion(name string) (int, error) {
	base := strings.TrimSuffix(name, ".sql")
	if base == name {
		return 0, fmt.Errorf("missing .sql extension")
	}
	idx := strings.IndexByte(base, '_')
	if idx <= 0 {
		return 0, fmt.Errorf("missing NNN_ prefix")
	}
	version, err := strconv.Atoi(base[:idx])
	if err != nil {
		return 0, fmt.Errorf("unparsable version prefix: %w", err)
	}
	if version < 1 {
		return 0, fmt.Errorf("version must be 1 or greater, got %d", version)
	}
	return version, nil
}

// SchemaVersion returns the highest recorded schema version, or 0 with no
// error on a fresh database that has no schema_version table yet.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return schemaVersion(ctx, s.db)
}

// schemaVersion is the shared implementation SchemaVersion and Migrate both
// use. It takes a *sql.DB directly, rather than a *Store, so that a test can
// probe an empty database that was opened with a plain sql.Open rather than
// this package's own Open.
//
// It tells apart the two different states that would otherwise both read as
// version 0: the schema_version table does not exist yet, a genuinely fresh
// database, versus the table exists but holds no rows, which this package's
// own writes never produce (the migration that creates it inserts its
// version row in the same transaction) but an external tool or a future
// migration touching the table could. The second state is reported as an
// error rather than silently treated like the first, because treating it
// like the first would make Migrate re-run 001_init.sql's CREATE TABLE
// schema_version against a database where that table already exists,
// failing with a "table already exists" error that names the wrong problem
// and leaves the database permanently unopenable.
func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var name string
	err := db.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'schema_version'",
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("check for schema_version table: %w", err)
	}

	var count int
	var version int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*), COALESCE(MAX(version), 0) FROM schema_version",
	).Scan(&count, &version); err != nil {
		return 0, fmt.Errorf("read schema_version: %w", err)
	}
	if count == 0 {
		return 0, errors.New("schema_version table exists but records no version")
	}
	return version, nil
}

// userObjectCount returns the number of objects in db that SQLite does not own
// itself, that is every entry in sqlite_master whose name does not begin with
// the reserved sqlite_ prefix.
//
// It counts objects rather than only tables. An earlier version filtered on
// type = 'table', which let a database whose only user objects are views be
// adopted: Shellforge wrote its own tables into it and narrowed its mode,
// which is exactly the harm the foreign gate exists to prevent. A view cannot
// exist in a file this package created while that file is not yet provably
// ours, so counting every object is both stricter and simpler than reasoning
// about which types are possible.
//
// sqlite_% excludes SQLite's own bookkeeping, sqlite_sequence and
// sqlite_stat1 among them, which is not evidence of another application:
// events declares INTEGER PRIMARY KEY AUTOINCREMENT, so sqlite_sequence
// appears in this package's own databases too. In SQL LIKE, _ is a single
// character wildcard, so this also excludes a table named sqlitezzz. That
// over-exclusion is deliberate: sqlite_ is reserved by SQLite's own rules so
// no real application uses it, and detecting a database whose objects were
// named to look like SQLite internals is not in this project's threat model.
func userObjectCount(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'",
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count objects in sqlite_master: %w", err)
	}
	return count, nil
}

// ourObjectNames are the objects 001_init.sql creates. Both must be present
// for a file to be provably a Shellforge progress database.
//
// events is the load bearing half. schema_version alone is not proof of
// provenance, because schema_version is a common name: older Flyway and a
// great deal of hand rolled migration code create a table called exactly
// that. A file holding someone else's schema_version and nothing of ours
// used to reach the version arithmetic below and be reported through
// anchorMigrationFailed or anchorTooNew, both of which tell the learner to
// rename the file. Telling a learner to rename another program's database is
// the one thing this gate exists to make impossible.
const (
	ourVersionTable = "schema_version"
	ourEventsTable  = "events"
)

// looksLikeOurs reports whether db holds both objects 001_init.sql creates.
//
// True means the file has been through the foreign gate before and is ours,
// so a learner may legitimately have added their own tables to it and the
// version arithmetic in Migrate can be trusted to describe it. False means
// provenance is not established, which is not the same as "foreign": a fresh
// or zero byte database is also not provably ours, and is legitimately
// adopted because it holds nothing at all.
func looksLikeOurs(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE name IN (?, ?)",
		ourVersionTable, ourEventsTable,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("look for this package's own tables in sqlite_master: %w", err)
	}
	return count == 2, nil
}

// refuseIfForeign returns the refusal for a database that is not provably a
// Shellforge progress file and already holds objects Shellforge did not
// create, and nil when the file is ours or is empty and so safe to adopt.
//
// It runs before any of Migrate's version arithmetic, not inside the
// current == 0 branch, because two of the arms downstream of that arithmetic
// (anchorMigrationFailed and anchorTooNew) tell the learner to rename the
// file. Provenance therefore has to be settled before any of them can
// report: a file holding another program's schema_version reached those arms
// in an earlier version of this gate and was told to be renamed.
//
// It fails closed twice over. A sqlite_master read that did not complete is
// not evidence that the file is ours, so an error from either query is
// refused with the same anchor as a confirmed foreign database rather than
// allowed through. The remediation is safe under either reading: it never
// tells the learner to delete, rename, move, or overwrite anything, which is
// the whole point when the file may belong to another program.
func (s *Store) refuseIfForeign(ctx context.Context) error {
	foreignFail := func(cause error) error {
		return ux.Fail(
			"open the file where Shellforge records your progress",
			cause,
			fmt.Sprintf("Shellforge did not create %s, so it has left that file alone. Shellforge cannot yet be pointed at a different progress file, so please run: shellforge doctor, and include its output if you report this", s.path),
			anchorForeignDatabase,
		)
	}

	ours, err := looksLikeOurs(ctx, s.db)
	if err != nil {
		return foreignFail(fmt.Errorf("%w: could not read the object list of %q to confirm it is ours: %w", ErrForeignDatabase, s.path, err))
	}
	if ours {
		// Provably ours, so extra objects are the learner's own and are
		// none of this gate's business.
		return nil
	}

	count, err := userObjectCount(ctx, s.db)
	if err != nil {
		return foreignFail(fmt.Errorf("%w: could not read the object list of %q to confirm it is ours: %w", ErrForeignDatabase, s.path, err))
	}
	if count > 0 {
		return foreignFail(fmt.Errorf("%w: %q already holds %d object(s) Shellforge did not create, and does not hold both of this package's own tables", ErrForeignDatabase, s.path, count))
	}
	return nil
}

// Migrate applies every embedded migration with a version greater than the
// database's current schema version, in ascending order, each inside its
// own transaction. Calling it again once the database is current is a
// no-op: no migration re-runs.
//
// If the database already records a version newer than this build
// understands, Migrate refuses and returns an error wrapping
// ErrSchemaTooNew. Nothing is written, no migration runs, and the file is
// left exactly as it was: there are no down migrations, and this package
// never deletes a learner's progress file to recover from this.
func (s *Store) Migrate(ctx context.Context) error {
	migrateFail := func(cause error) error {
		return ux.Fail(
			"update the layout of the file where Shellforge records your progress",
			cause,
			fmt.Sprintf("Run: shellforge doctor, and include its output if you report this. To carry on now, rename %s to progress.db.broken and Shellforge will start a new progress file.", s.path),
			anchorMigrationFailed,
		)
	}

	all, err := migrations()
	if err != nil {
		return migrateFail(err)
	}
	latest := 0
	if len(all) > 0 {
		latest = all[len(all)-1].version
	}

	// Provenance first, before the version arithmetic and before either arm
	// that would tell the learner to rename the file. refuseIfForeign is a
	// no-op for a database that is provably ours and for an empty one, so
	// running it unconditionally costs one catalogue read and removes the
	// need to reason about which version-shaped arm a foreign file might
	// reach.
	if err := s.refuseIfForeign(ctx); err != nil {
		return err
	}

	current, err := schemaVersion(ctx, s.db)
	if err != nil {
		return migrateFail(err)
	}

	if current > latest {
		return ux.Fail(
			"open the file where Shellforge records your progress",
			fmt.Errorf("%w: the file is at version %d and this build understands %d", ErrSchemaTooNew, current, latest),
			fmt.Sprintf("This copy of Shellforge is older than your progress file. Update Shellforge and start it again. To go back to an older version on purpose, rename %s first and a new progress file will be created.", s.path),
			anchorTooNew,
		)
	}

	for _, m := range all {
		if m.version <= current {
			continue
		}
		if err := applyMigration(ctx, s.db, m); err != nil {
			return migrateFail(fmt.Errorf("apply migration %s: %w", m.name, err))
		}
	}

	return nil
}

// applyMigration runs one migration file's SQL as a single ExecContext with
// no bound arguments, inside its own transaction, then rewrites
// schema_version to hold exactly one row: m.version. The modernc driver
// executes every statement in a multi-statement string only when no
// arguments are bound and rejects trailing text once arguments are bound,
// so the migration SQL and the version write never mix in one call:
// delete-then-insert keeps exactly one row in schema_version whatever state
// the table was in before.
func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback() // no-op once Commit has succeeded; matters only on an early return
	}()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("execute migration sql: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM schema_version"); err != nil {
		return fmt.Errorf("clear schema_version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (?)", m.version); err != nil {
		return fmt.Errorf("record schema_version: %w", err)
	}
	return tx.Commit()
}
