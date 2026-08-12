package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens the SQLite database at path and enables WAL mode, as this
// package's own doc comment requires.
//
// Schema creation and migrations are #51's job; this is the connection
// primitive they build on.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %q: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("enable WAL mode on %q: %w (closing the connection also failed: %v)", path, err, closeErr)
		}
		return nil, fmt.Errorf("enable WAL mode on %q: %w", path, err)
	}
	return db, nil
}
