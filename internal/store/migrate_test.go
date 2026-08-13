package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"

	_ "modernc.org/sqlite"
)

func TestMigrationsAreOrderedAndGapFree(t *testing.T) {
	all, err := migrations()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no embedded migrations found")
	}
	for i, m := range all {
		want := i + 1
		if m.version != want {
			t.Errorf("migration at index %d has version %d, want %d", i, m.version, want)
		}
		if m.sql == "" {
			t.Errorf("migration %s has empty SQL", m.name)
		}
	}
}

func TestMigrateTwiceIsANoOp(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if err := insertSentinelEvent(s.DB(), "migrate-twice"); err != nil {
		t.Fatalf("insert sentinel: %v", err)
	}
	before, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}

	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	after, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion after second Migrate: %v", err)
	}
	if after != before {
		t.Errorf("SchemaVersion changed: %d -> %d", before, after)
	}

	var count int
	if err := s.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM events WHERE level_id = ?", "migrate-twice").Scan(&count); err != nil {
		t.Fatalf("count sentinel rows: %v", err)
	}
	if count != 1 {
		t.Errorf("sentinel row count = %d, want 1", count)
	}

	var versionRows int
	if err := s.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_version").Scan(&versionRows); err != nil {
		t.Fatalf("count schema_version rows: %v", err)
	}
	if versionRows != 1 {
		t.Errorf("schema_version row count = %d, want 1", versionRows)
	}
}

func TestMigrateRecordsTheAppliedVersion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	var count int
	if err := s.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_version").Scan(&count); err != nil {
		t.Fatalf("count schema_version rows: %v", err)
	}
	if count != 1 {
		t.Errorf("schema_version row count = %d, want 1", count)
	}

	all, err := migrations()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	want := all[len(all)-1].version

	var version int
	if err := s.DB().QueryRowContext(context.Background(), "SELECT version FROM schema_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != want {
		t.Errorf("recorded version = %d, want %d", version, want)
	}
}

func TestSchemaVersionOnAnEmptyDatabaseIsZero(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "plain.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatalf("enable WAL: %v", err)
	}

	version, err := schemaVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}
	if version != 0 {
		t.Errorf("version = %d, want 0", version)
	}
}

func TestOpenRefusesANewerSchemaVersion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}

	all, err := migrations()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	tooNew := all[len(all)-1].version + 1

	if _, err := s.DB().ExecContext(context.Background(), "UPDATE schema_version SET version = ?", tooNew); err != nil {
		t.Fatalf("bump schema_version: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = Open(context.Background(), dbPath)
	if err == nil {
		t.Fatal("Open succeeded against a newer schema version")
	}
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("error does not wrap ErrSchemaTooNew: %v", err)
	}
	var uerr *ux.Error
	if !errors.As(err, &uerr) {
		t.Fatalf("error is not a *ux.Error: %v", err)
	}
	if uerr.DocAnchor != anchorTooNew {
		t.Errorf("DocAnchor = %q, want %q", uerr.DocAnchor, anchorTooNew)
	}
	if uerr.Remediation == "" {
		t.Error("Remediation is empty")
	}

	// Reopen with a plain sql.DB and confirm the file was refused, not
	// corrupted, not rolled back, and not deleted.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("plain sql.Open: %v", err)
	}
	defer db.Close()

	var name string
	if err := db.QueryRowContext(context.Background(),
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'events'").Scan(&name); err != nil {
		t.Errorf("events table missing after refused reopen: %v", err)
	}

	var version int
	if err := db.QueryRowContext(context.Background(), "SELECT version FROM schema_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != tooNew {
		t.Errorf("version = %d, want %d (unchanged)", version, tooNew)
	}
}
