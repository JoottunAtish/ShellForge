package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenEnablesWALMode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}
}

func TestOpenRejectsAnUnwritableDirectory(t *testing.T) {
	_, err := Open(context.Background(), filepath.Join(t.TempDir(), "does-not-exist", "progress.db"))
	if err == nil {
		t.Fatal("Open accepted a path in a directory that does not exist")
	}
}
