package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// insertSentinelEvent writes one well formed row into events, so a test can
// prove a database survived a reopen or a second Migrate without going
// through internal/journal, which this package must never import.
func insertSentinelEvent(db *sql.DB, levelID string) error {
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO events (seq, ts, level_id, cwd, raw, exit_code, duration_ms, used_tab, used_history)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		1, 1700000000.0, levelID, "/home/learner", "echo hi", 0, 10, false, false)
	return err
}

func TestOpenOnAFreshPathAppliesEveryMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")

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

	for _, table := range []string{"schema_version", "events"} {
		var name string
		row := s.DB().QueryRowContext(context.Background(),
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table)
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %q missing from sqlite_master: %v", table, err)
		}
	}
	var idxName string
	row := s.DB().QueryRowContext(context.Background(),
		"SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?", "idx_events_level_seq")
	if err := row.Scan(&idxName); err != nil {
		t.Errorf("index idx_events_level_seq missing from sqlite_master: %v", err)
	}

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("database file does not exist on disk: %v", err)
	}
}

func TestOpenCreatesMissingParentDirectories(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "a", "b", "progress.db")

	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	if runtime.GOOS == "windows" {
		t.Skip("Unix mode bits are not meaningful on Windows")
	}

	for _, dir := range []string{filepath.Dir(filepath.Dir(dbPath)), filepath.Dir(dbPath)} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("%s mode = %o, want 0700", dir, perm)
		}
	}

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat %s: %v", dbPath, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("database file mode = %o, want 0600", perm)
	}
}

func TestOpenRejectsAnUnwritableParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce POSIX mode bits the same way")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory mode bits")
	}

	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o700) })

	dbPath := filepath.Join(parent, "sub", "progress.db")
	_, err := Open(context.Background(), dbPath)
	if err == nil {
		t.Fatal("Open succeeded against an unwritable parent directory")
	}

	var uerr *ux.Error
	if !errors.As(err, &uerr) {
		t.Fatalf("error is not a *ux.Error: %v", err)
	}
	if uerr.Op == "" {
		t.Error("Op is empty")
	}
	if uerr.Remediation == "" {
		t.Error("Remediation is empty")
	}
	if uerr.DocAnchor != anchorUnwritable {
		t.Errorf("DocAnchor = %q, want %q", uerr.DocAnchor, anchorUnwritable)
	}
}

func TestOpenSetsWALAndBusyTimeoutAndSynchronous(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	var mode string
	if err := s.DB().QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}

	var busy int
	if err := s.DB().QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}

	var synchronous int
	if err := s.DB().QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("query synchronous: %v", err)
	}
	if synchronous != 1 {
		t.Errorf("synchronous = %d, want 1 (NORMAL)", synchronous)
	}
}

func TestOpenOnACurrentDatabaseIsANoOp(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")

	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := insertSentinelEvent(s.DB(), "sentinel-open"); err != nil {
		t.Fatalf("insert sentinel: %v", err)
	}
	firstVersion, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { s2.Close() })

	secondVersion, err := s2.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion after reopen: %v", err)
	}
	if secondVersion != firstVersion {
		t.Errorf("SchemaVersion changed on reopen: %d -> %d", firstVersion, secondVersion)
	}

	var levelID string
	row := s2.DB().QueryRowContext(context.Background(), "SELECT level_id FROM events WHERE level_id = ?", "sentinel-open")
	if err := row.Scan(&levelID); err != nil {
		t.Fatalf("sentinel row missing after reopen: %v", err)
	}
}

func TestOpenOnACorruptFileFails(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	garbage := garbageBytes()
	if err := os.WriteFile(dbPath, garbage, 0o600); err != nil {
		t.Fatalf("write garbage file: %v", err)
	}

	_, err := Open(context.Background(), dbPath)
	if err == nil {
		t.Fatal("Open succeeded against a corrupt file")
	}

	var uerr *ux.Error
	if !errors.As(err, &uerr) {
		t.Fatalf("error is not a *ux.Error: %v", err)
	}
	if uerr.DocAnchor != anchorCorrupt {
		t.Errorf("DocAnchor = %q, want %q", uerr.DocAnchor, anchorCorrupt)
	}
	if uerr.Remediation == "" {
		t.Error("Remediation is empty")
	}
	if !strings.Contains(uerr.Remediation, dbPath) {
		t.Errorf("Remediation does not mention the path: %q", uerr.Remediation)
	}
}

func TestOpenOnACorruptFileLeavesItUntouched(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	garbage := garbageBytes()
	if err := os.WriteFile(dbPath, garbage, 0o600); err != nil {
		t.Fatalf("write garbage file: %v", err)
	}

	dir := filepath.Dir(dbPath)
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir before: %v", err)
	}

	if _, err := Open(context.Background(), dbPath); err == nil {
		t.Fatal("Open succeeded against a corrupt file")
	}

	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read file after: %v", err)
	}
	if string(after) != string(garbage) {
		t.Error("the corrupt file's bytes changed after a failed Open")
	}

	afterEntries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir after: %v", err)
	}
	if len(afterEntries) < len(before) {
		t.Fatalf("directory lost entries: before %d, after %d", len(before), len(afterEntries))
	}
	for _, e := range afterEntries {
		if e.Name() == filepath.Base(dbPath) {
			continue
		}
		if strings.HasSuffix(e.Name(), "-wal") || strings.HasSuffix(e.Name(), "-shm") || strings.HasSuffix(e.Name(), "-journal") {
			continue
		}
		t.Errorf("unexpected sibling file created: %s", e.Name())
	}
}

// TestOpenOnACorruptFileLeaksNoDescriptor pins the fix for a leak that only
// Windows CI could see. classifyOpenError used to discard the *os.File its
// permission probe opened, so every failed Open that classified as corrupt
// leaked a descriptor. Unix tolerates that (an unlinked file with an open
// handle still goes away), so the whole suite passed on Linux while
// windows-latest failed t.TempDir cleanup with "The process cannot access the
// file because it is being used by another process".
//
// Counting /proc/self/fd is Linux specific and this test skips elsewhere,
// which is the point: without it the guard lives only on a Windows runner,
// and a leak nobody can reproduce locally is a leak that comes back. The
// Windows behaviour is still covered, by the two corrupt-file tests above
// failing their own cleanup.
func TestOpenOnACorruptFileLeaksNoDescriptor(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("counting open descriptors through /proc/self/fd is Linux specific")
	}

	countFDs := func() int {
		t.Helper()
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatalf("read /proc/self/fd: %v", err)
		}
		return len(entries)
	}

	dbPath := filepath.Join(t.TempDir(), "progress.db")
	if err := os.WriteFile(dbPath, garbageBytes(), 0o600); err != nil {
		t.Fatalf("write garbage file: %v", err)
	}

	// One failed Open first, so that any one-off allocation the driver makes
	// on its own initialisation is not counted as this call's leak.
	if _, err := Open(context.Background(), dbPath); err == nil {
		t.Fatal("Open succeeded against a corrupt file")
	}

	before := countFDs()
	const rounds = 20
	for i := 0; i < rounds; i++ {
		if _, err := Open(context.Background(), dbPath); err == nil {
			t.Fatal("Open succeeded against a corrupt file")
		}
	}
	after := countFDs()

	// A leak of one descriptor per call would show as +20 here. Allow a small
	// slack for unrelated runtime activity rather than demanding equality,
	// which would make this flaky for no gain: the defect this guards against
	// grows linearly with the loop and clears any slack immediately.
	if after-before >= rounds/2 {
		t.Errorf("open descriptors grew from %d to %d across %d failed Opens: classifyOpenError is leaking its probe handle", before, after, rounds)
	}
}

func TestOpenClassifiesAnUnreadableFileAsUnwritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce POSIX mode bits the same way")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores file mode bits")
	}

	dbPath := filepath.Join(t.TempDir(), "progress.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := os.Chmod(dbPath, 0o000); err != nil {
		t.Fatalf("chmod 0000: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dbPath, 0o600) })

	_, err = Open(context.Background(), dbPath)
	if err == nil {
		t.Fatal("Open succeeded against a 0000 mode file")
	}

	var uerr *ux.Error
	if !errors.As(err, &uerr) {
		t.Fatalf("error is not a *ux.Error: %v", err)
	}
	if uerr.DocAnchor != anchorUnwritable {
		t.Errorf("DocAnchor = %q, want %q (not %q)", uerr.DocAnchor, anchorUnwritable, anchorCorrupt)
	}
}

// TestConcurrentStoresOnOneFileDoNotDeadlock covers the steady state: both
// Open calls below happen after the database is already in WAL mode, since
// s1's Open finishes the delete-to-WAL conversion before s2's Open ever
// begins, so neither one contends for the exclusive lock that conversion
// needs. It does not exercise that contended moment; see
// TestOpenOnAContendedConversionReportsInUseAndRetries for the test that
// creates the database in delete mode first and holds a write transaction
// on it, so a second Open genuinely contends for the conversion.
func TestConcurrentStoresOnOneFileDoNotDeadlock(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")

	s1, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open s1: %v", err)
	}
	t.Cleanup(func() { s1.Close() })

	s2, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open s2: %v", err)
	}
	t.Cleanup(func() { s2.Close() })

	const ops = 50
	errs := make(chan error, ops*2)
	var wg sync.WaitGroup

	worker := func(s *Store, tag string) {
		defer wg.Done()
		for i := 0; i < ops; i++ {
			if err := insertSentinelEvent(s.DB(), tag); err != nil {
				errs <- err
				return
			}
			var count int
			if err := s.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events").Scan(&count); err != nil {
				errs <- err
				return
			}
		}
	}

	wg.Add(2)
	go worker(s1, "concurrent-1")
	go worker(s2, "concurrent-2")

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent stores on one file deadlocked or ran too slowly")
	}
	close(errs)
	for err := range errs {
		t.Errorf("concurrent operation failed: %v", err)
	}
}

// TestOpenOnAContendedConversionReportsInUseAndRetries reproduces the exact
// contention classifyOpenError misclassified before this fix: a second
// connection holds a write transaction on a plain, non-WAL database, which
// makes the first connection's journal_mode=WAL pragma fail with
// SQLITE_BUSY. Before this fix that landed on the corrupt anchor, whose
// remediation tells the learner to rename their progress file and lose
// every level they finished, even though nothing is actually wrong with the
// file. Here it must land on the in-use anchor instead, whose remediation
// must never mention renaming or deleting anything, and then, once the
// blocking transaction is released, a second Open must succeed because
// execPragmaWithRetry retries rather than failing once and giving up.
func TestOpenOnAContendedConversionReportsInUseAndRetries(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")

	blocker, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open blocker: %v", err)
	}
	t.Cleanup(func() { blocker.Close() })
	blocker.SetMaxOpenConns(1)

	if _, err := blocker.ExecContext(context.Background(), "CREATE TABLE t (x INTEGER)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	tx, err := blocker.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin blocking transaction: %v", err)
	}
	if _, err := tx.ExecContext(context.Background(), "INSERT INTO t (x) VALUES (1)"); err != nil {
		t.Fatalf("insert inside blocking transaction: %v", err)
	}
	// tx is left open and uncommitted on purpose: this is what holds the
	// exclusive lock the delete-to-WAL conversion needs, for as long as
	// this first Open call is exercised below.

	if _, err := Open(context.Background(), dbPath); err == nil {
		t.Fatal("Open succeeded while a write transaction held the file")
	} else {
		var uerr *ux.Error
		if !errors.As(err, &uerr) {
			t.Fatalf("error is not a *ux.Error: %v", err)
		}
		if uerr.DocAnchor != anchorInUse {
			t.Errorf("DocAnchor = %q, want %q (not %q)", uerr.DocAnchor, anchorInUse, anchorCorrupt)
		}
		lower := strings.ToLower(uerr.Remediation)
		if strings.Contains(lower, "rename") || strings.Contains(lower, "delete") {
			t.Errorf("Remediation tells the learner to rename or delete a file that is not wrong: %q", uerr.Remediation)
		}
	}

	// Release the blocking transaction from another goroutine after a
	// delay comfortably inside the retry budget (pragmaRetries *
	// pragmaRetryDelay), then prove the retry loop is what makes the
	// simultaneous-start race disappear: Open must now succeed.
	go func() {
		time.Sleep(3 * pragmaRetryDelay)
		_ = tx.Rollback()
	}()

	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open after the blocking transaction released: %v", err)
	}
	t.Cleanup(func() { s.Close() })
}

func TestDatabasePathIsNotUnderCacheDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dbPath, err := platform.DatabasePath()
	if err != nil {
		t.Fatalf("DatabasePath: %v", err)
	}
	cacheDir, err := platform.CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}

	cleanDB := filepath.Clean(dbPath)
	cleanCache := filepath.Clean(cacheDir)
	if runtime.GOOS == "windows" {
		cleanDB = strings.ToLower(cleanDB)
		cleanCache = strings.ToLower(cleanCache)
	}
	if strings.HasPrefix(cleanDB, cleanCache+string(filepath.Separator)) {
		t.Errorf("DatabasePath %q sits under CacheDir %q", dbPath, cacheDir)
	}
}

func garbageBytes() []byte {
	b := make([]byte, 512)
	for i := range b {
		b[i] = byte(i%251 + 1)
	}
	return b
}
