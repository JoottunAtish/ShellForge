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

	var version int
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema_version: %w", err)
	}
	return version, nil
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
