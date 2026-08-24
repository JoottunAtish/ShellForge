package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newV1DB opens a fresh database at schema version 1 only, by applying
// migrations()[0] directly through a plain sql.Open rather than this
// package's own Open. This is what lets TestMigration002... prove that
// Open, starting from a real version 1 database, both applies 002 and
// leaves 001's own events table alone.
func newV1DB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	all, err := migrations()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if len(all) == 0 || all[0].version != 1 {
		t.Fatalf("expected migrations()[0] to be version 1, got %+v", all)
	}
	if err := applyMigration(context.Background(), db, all[0]); err != nil {
		t.Fatalf("applyMigration v1: %v", err)
	}
	return db, dbPath
}

// tableExists reports whether name appears in sqlite_master as a table.
func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRowContext(context.Background(),
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", name,
	).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("query sqlite_master for %q: %v", name, err)
	}
	return true
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	// #nosec G201 -- table is always one of this test file's own literal
	// constants, never a value derived from input.
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count rows in %s: %v", table, err)
	}
	return n
}

func TestMigration002AppliesOverVersion1AndPreservesEvents(t *testing.T) {
	db, dbPath := newV1DB(t)
	if err := insertSentinelEvent(db, "sentinel-002"); err != nil {
		t.Fatalf("insert sentinel: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v1 handle: %v", err)
	}

	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	version, err := s.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != 2 {
		t.Errorf("SchemaVersion = %d, want 2", version)
	}

	for _, table := range []string{"profile", "pack", "level_state", "attempt", "concept_mastery", "achievement"} {
		if !tableExists(t, s.DB(), table) {
			t.Errorf("table %q missing from sqlite_master after migrating to 002", table)
		}
	}

	if n := countRows(t, s.DB(), "events"); n != 1 {
		t.Fatalf("events row count = %d, want 1", n)
	}
	var levelID, raw string
	row := s.DB().QueryRowContext(context.Background(),
		"SELECT level_id, raw FROM events WHERE level_id = ?", "sentinel-002")
	if err := row.Scan(&levelID, &raw); err != nil {
		t.Fatalf("sentinel event row missing or malformed: %v", err)
	}
	if levelID != "sentinel-002" || raw != "echo hi" {
		t.Errorf("sentinel event corrupted: level_id=%q raw=%q", levelID, raw)
	}
}

func TestEnsureProfileIsIdempotentAcrossHandles(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()

	s1, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open A: %v", err)
	}
	t.Cleanup(func() { s1.Close() })

	p1, err := s1.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile (first): %v", err)
	}
	if p1.ID == 0 {
		t.Fatal("EnsureProfile returned a zero id")
	}

	p1Again, err := s1.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile (second, same handle): %v", err)
	}
	if p1Again.ID != p1.ID {
		t.Errorf("second EnsureProfile on the same handle returned id %d, want %d", p1Again.ID, p1.ID)
	}

	s2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open B: %v", err)
	}
	t.Cleanup(func() { s2.Close() })

	p2, err := s2.EnsureProfile(ctx, "a different name")
	if err != nil {
		t.Fatalf("EnsureProfile (second handle): %v", err)
	}
	if p2.ID != p1.ID {
		t.Errorf("EnsureProfile on a second handle returned id %d, want %d", p2.ID, p1.ID)
	}
	if p2.Name != p1.Name {
		t.Errorf("EnsureProfile on a second handle returned name %q, want %q (name is ignored once a profile exists)", p2.Name, p1.Name)
	}

	if n := countRows(t, s1.DB(), "profile"); n != 1 {
		t.Errorf("profile row count = %d, want 1", n)
	}
}

func TestLevelStateMissingRowIsFoundFalse(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	profile, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	state, found, err := s.LevelState(ctx, profile.ID, "nav-01", 1)
	if err != nil {
		t.Fatalf("LevelState on an unrecorded level: %v", err)
	}
	if found {
		t.Fatalf("LevelState found = true on an unrecorded level, state = %+v", state)
	}
	if state != (LevelState{}) {
		t.Errorf("LevelState returned a non-zero value when not found: %+v", state)
	}

	if err := s.SetLevelStatus(ctx, profile.ID, "core-linux-basics", "nav-01", 1, StatusAvailable); err != nil {
		t.Fatalf("SetLevelStatus: %v", err)
	}

	state, found, err = s.LevelState(ctx, profile.ID, "nav-01", 1)
	if err != nil {
		t.Fatalf("LevelState after SetLevelStatus: %v", err)
	}
	if !found {
		t.Fatal("LevelState found = false right after SetLevelStatus created the row")
	}
	if state.Status != StatusAvailable {
		t.Errorf("Status = %q, want %q", state.Status, StatusAvailable)
	}
	if state.BestScore != 0 || state.Attempts != 0 || state.HintsUsed != 0 || state.CommandsUsed != 0 || state.TotalSeconds != 0 {
		t.Errorf("a freshly created row is not all-default: %+v", state)
	}
	if state.Stale {
		t.Error("Stale = true for a row created at the version just read")
	}
}

func TestStartThenFinishAttemptLeavesConsistentState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	profile, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	started := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	ended := started.Add(90 * time.Second)

	id, err := s.StartAttempt(ctx, profile.ID, "core-linux-basics", "nav-01", 1, started)
	if err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}
	if id <= 0 {
		t.Fatalf("StartAttempt returned id %d, want > 0", id)
	}

	err = s.FinishAttempt(ctx, id, Attempt{
		ProfileID:    profile.ID,
		LevelID:      "nav-01",
		EndedAt:      ended,
		Outcome:      OutcomePassed,
		Score:        75,
		HintsUsed:    2,
		CommandsUsed: 10,
	})
	if err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	var endedAt sql.NullInt64
	var outcome string
	row := s.DB().QueryRowContext(ctx, "SELECT ended_at, outcome FROM attempt WHERE id = ?", id)
	if err := row.Scan(&endedAt, &outcome); err != nil {
		t.Fatalf("read attempt row: %v", err)
	}
	if !endedAt.Valid {
		t.Error("attempt.ended_at is still null after FinishAttempt")
	}
	if outcome != string(OutcomePassed) {
		t.Errorf("attempt.outcome = %q, want %q", outcome, OutcomePassed)
	}

	state, found, err := s.LevelState(ctx, profile.ID, "nav-01", 1)
	if err != nil {
		t.Fatalf("LevelState: %v", err)
	}
	if !found {
		t.Fatal("LevelState found = false after StartAttempt and FinishAttempt")
	}
	if state.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", state.Attempts)
	}
	if state.BestScore != 75 {
		t.Errorf("BestScore = %d, want 75", state.BestScore)
	}
	if state.HintsUsed != 2 {
		t.Errorf("HintsUsed = %d, want 2", state.HintsUsed)
	}
	if state.CommandsUsed != 10 {
		t.Errorf("CommandsUsed = %d, want 10", state.CommandsUsed)
	}
	if state.Status != StatusPassed {
		t.Errorf("Status = %q, want %q", state.Status, StatusPassed)
	}
	if state.TotalSeconds != 90 {
		t.Errorf("TotalSeconds = %d, want 90", state.TotalSeconds)
	}
	if !state.FirstPassedAt.Equal(ended) {
		t.Errorf("FirstPassedAt = %v, want %v", state.FirstPassedAt, ended)
	}
}

func TestFinishAttemptUnknownIdErrorsAndWritesNothing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	const bogusID = int64(999999)
	err = s.FinishAttempt(ctx, bogusID, Attempt{Outcome: OutcomePassed})
	if !errors.Is(err, ErrNoSuchAttempt) {
		t.Fatalf("FinishAttempt on an unknown id: got %v, want ErrNoSuchAttempt", err)
	}
	if !strings.Contains(err.Error(), strconv.FormatInt(bogusID, 10)) {
		t.Errorf("error %q does not name the attempt id %d", err.Error(), bogusID)
	}

	if n := countRows(t, s.DB(), "attempt"); n != 0 {
		t.Errorf("attempt table has %d rows, want 0", n)
	}
	if n := countRows(t, s.DB(), "level_state"); n != 0 {
		t.Errorf("level_state table has %d rows, want 0", n)
	}
}

func TestFinishAttemptAlreadyClosedErrorsAndWritesNothing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	profile, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	started := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	ended := started.Add(30 * time.Second)
	id, err := s.StartAttempt(ctx, profile.ID, "core-linux-basics", "nav-01", 1, started)
	if err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}
	first := Attempt{ProfileID: profile.ID, LevelID: "nav-01", EndedAt: ended, Outcome: OutcomePassed, Score: 60, HintsUsed: 1, CommandsUsed: 5}
	if err := s.FinishAttempt(ctx, id, first); err != nil {
		t.Fatalf("first FinishAttempt: %v", err)
	}

	before, found, err := s.LevelState(ctx, profile.ID, "nav-01", 1)
	if err != nil || !found {
		t.Fatalf("LevelState after first finish: found=%v err=%v", found, err)
	}

	second := Attempt{ProfileID: profile.ID, LevelID: "nav-01", EndedAt: ended.Add(time.Minute), Outcome: OutcomePassed, Score: 99, HintsUsed: 9, CommandsUsed: 9}
	err = s.FinishAttempt(ctx, id, second)
	if !errors.Is(err, ErrAttemptClosed) {
		t.Fatalf("second FinishAttempt on a closed attempt: got %v, want ErrAttemptClosed", err)
	}

	after, found, err := s.LevelState(ctx, profile.ID, "nav-01", 1)
	if err != nil || !found {
		t.Fatalf("LevelState after rejected second finish: found=%v err=%v", found, err)
	}
	if after != before {
		t.Errorf("level_state changed after a rejected second finish: before=%+v after=%+v", before, after)
	}
}

func TestBestScoreNeverFallsAndFirstPassKept(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	profile, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	base := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	scores := []int{50, 20, 90}
	var firstEnded time.Time

	for i, score := range scores {
		started := base.Add(time.Duration(i) * time.Hour)
		ended := started.Add(time.Minute)
		if i == 0 {
			firstEnded = ended
		}
		id, err := s.StartAttempt(ctx, profile.ID, "core-linux-basics", "nav-01", 1, started)
		if err != nil {
			t.Fatalf("StartAttempt %d: %v", i, err)
		}
		if err := s.FinishAttempt(ctx, id, Attempt{ProfileID: profile.ID, LevelID: "nav-01", EndedAt: ended, Outcome: OutcomePassed, Score: score}); err != nil {
			t.Fatalf("FinishAttempt %d: %v", i, err)
		}
	}

	state, found, err := s.LevelState(ctx, profile.ID, "nav-01", 1)
	if err != nil || !found {
		t.Fatalf("LevelState: found=%v err=%v", found, err)
	}
	if state.BestScore != 90 {
		t.Errorf("BestScore = %d, want 90", state.BestScore)
	}
	if !state.FirstPassedAt.Equal(firstEnded) {
		t.Errorf("FirstPassedAt = %v, want %v (the first pass, not the highest)", state.FirstPassedAt, firstEnded)
	}
	if state.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", state.Attempts)
	}
}

func TestReaderReportsStaleAndZeroesBestScore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	profile, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	started := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	ended := started.Add(time.Minute)
	id, err := s.StartAttempt(ctx, profile.ID, "core-linux-basics", "nav-01", 1, started)
	if err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}
	if err := s.FinishAttempt(ctx, id, Attempt{ProfileID: profile.ID, LevelID: "nav-01", EndedAt: ended, Outcome: OutcomePassed, Score: 42, HintsUsed: 3, CommandsUsed: 4}); err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	stale, found, err := s.LevelState(ctx, profile.ID, "nav-01", 2)
	if err != nil {
		t.Fatalf("LevelState at version 2: %v", err)
	}
	if !found {
		t.Fatal("LevelState at version 2 found = false")
	}
	if !stale.Stale {
		t.Error("Stale = false when reading a level_version different from the one stored")
	}
	if stale.BestScore != 0 {
		t.Errorf("BestScore = %d, want 0 when Stale", stale.BestScore)
	}
	if stale.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1 (preserved even when Stale)", stale.Attempts)
	}
	if stale.HintsUsed != 3 {
		t.Errorf("HintsUsed = %d, want 3 (preserved even when Stale)", stale.HintsUsed)
	}

	current, found, err := s.LevelState(ctx, profile.ID, "nav-01", 1)
	if err != nil {
		t.Fatalf("LevelState at version 1: %v", err)
	}
	if !found {
		t.Fatal("LevelState at version 1 found = false")
	}
	if current.Stale {
		t.Error("Stale = true when reading the version that is actually stored")
	}
	if current.BestScore != 42 {
		t.Errorf("BestScore = %d, want 42 when reading the matching version", current.BestScore)
	}
}

func TestSetLevelStatusRejectsUnknownStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	profile, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	err = s.SetLevelStatus(ctx, profile.ID, "core-linux-basics", "nav-01", 1, LevelStatus("bogus"))
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("SetLevelStatus with an unknown status: got %v, want ErrInvalidStatus", err)
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error %q does not name the rejected value", err.Error())
	}

	if n := countRows(t, s.DB(), "level_state"); n != 0 {
		t.Errorf("level_state has %d rows after a rejected status, want 0", n)
	}
}

func TestConcurrentWritesFromTwoStoreHandles(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()

	s1, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open A: %v", err)
	}
	t.Cleanup(func() { s1.Close() })
	s2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open B: %v", err)
	}
	t.Cleanup(func() { s2.Close() })

	profile, err := s1.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	const iterations = 25
	levelIDs := []string{"nav-01", "nav-02"}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	record := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}

	run := func(s *Store, levelID string) {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if err := s.SetLevelStatus(ctx, profile.ID, "core-linux-basics", levelID, 1, StatusAvailable); err != nil {
				record(err)
				continue
			}
			started := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second)
			if _, err := s.StartAttempt(ctx, profile.ID, "core-linux-basics", levelID, 1, started); err != nil {
				record(err)
			}
		}
	}

	wg.Add(2)
	go run(s1, levelIDs[0])
	go run(s2, levelIDs[1])
	wg.Wait()

	for _, err := range errs {
		t.Errorf("concurrent write error: %v", err)
	}

	// Both handles wrote through to the same database: not just "no
	// errors", but the rows both goroutines' StartAttempt calls inserted
	// are actually all there, and each distinct level_id got its own
	// level_state row rather than colliding or being dropped.
	wantAttempts := len(levelIDs) * iterations
	if n := countRows(t, s1.DB(), "attempt"); n != wantAttempts {
		t.Errorf("attempt row count = %d, want %d (%d goroutines x %d StartAttempt calls each)", n, wantAttempts, len(levelIDs), iterations)
	}
	if n := countRows(t, s1.DB(), "level_state"); n != len(levelIDs) {
		t.Errorf("level_state row count = %d, want %d (one row per distinct profile_id, level_id pair written)", n, len(levelIDs))
	}
}

func TestNeverPassedReadsZeroTimeNotEpoch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	profile, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	started := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	ended := started.Add(15 * time.Second)
	id, err := s.StartAttempt(ctx, profile.ID, "core-linux-basics", "nav-01", 1, started)
	if err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}
	if err := s.FinishAttempt(ctx, id, Attempt{ProfileID: profile.ID, LevelID: "nav-01", EndedAt: ended, Outcome: OutcomeAbandoned}); err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	state, found, err := s.LevelState(ctx, profile.ID, "nav-01", 1)
	if err != nil || !found {
		t.Fatalf("LevelState: found=%v err=%v", found, err)
	}
	if !state.FirstPassedAt.IsZero() {
		t.Errorf("FirstPassedAt = %v, want the zero time.Time for a level never passed", state.FirstPassedAt)
	}
	if state.FirstPassedAt.Equal(time.Unix(0, 0)) {
		t.Error("FirstPassedAt reads as the unix epoch, not as time.Time's zero value")
	}
}

func TestTotalXPEmptyProfileIsZero(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	profile, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	xp, err := s.TotalXP(ctx, profile.ID, "core-linux-basics")
	if err != nil {
		t.Fatalf("TotalXP: %v", err)
	}
	if xp != 0 {
		t.Errorf("TotalXP = %d, want 0 for a profile with no recorded levels", xp)
	}
}

func TestTotalXPIgnoresOtherPacks(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	profile, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	started := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	pass := func(packID, levelID string, score int) {
		t.Helper()
		id, err := s.StartAttempt(ctx, profile.ID, packID, levelID, 1, started)
		if err != nil {
			t.Fatalf("StartAttempt %s/%s: %v", packID, levelID, err)
		}
		if err := s.FinishAttempt(ctx, id, Attempt{ProfileID: profile.ID, LevelID: levelID, EndedAt: started.Add(time.Minute), Outcome: OutcomePassed, Score: score}); err != nil {
			t.Fatalf("FinishAttempt %s/%s: %v", packID, levelID, err)
		}
	}

	pass("core-linux-basics", "nav-01", 30)
	pass("core-linux-basics", "nav-02", 40)
	pass("some-other-pack", "other-01", 1000)

	xp, err := s.TotalXP(ctx, profile.ID, "core-linux-basics")
	if err != nil {
		t.Fatalf("TotalXP: %v", err)
	}
	if xp != 70 {
		t.Errorf("TotalXP = %d, want 70 (30 + 40, excluding the other pack's 1000)", xp)
	}
}

func TestFinishAttemptRejectsUnknownOutcome(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	profile, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	started := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	id, err := s.StartAttempt(ctx, profile.ID, "core-linux-basics", "nav-01", 1, started)
	if err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}

	err = s.FinishAttempt(ctx, id, Attempt{ProfileID: profile.ID, LevelID: "nav-01", EndedAt: started.Add(time.Minute), Outcome: Outcome("nope")})
	if !errors.Is(err, ErrInvalidOutcome) {
		t.Fatalf("FinishAttempt with an unknown outcome: got %v, want ErrInvalidOutcome", err)
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error %q does not name the rejected value", err.Error())
	}

	var endedAt sql.NullInt64
	row := s.DB().QueryRowContext(ctx, "SELECT ended_at FROM attempt WHERE id = ?", id)
	if err := row.Scan(&endedAt); err != nil {
		t.Fatalf("read attempt row: %v", err)
	}
	if endedAt.Valid {
		t.Error("attempt.ended_at was written despite the rejected outcome")
	}

	state, found, err := s.LevelState(ctx, profile.ID, "nav-01", 1)
	if err != nil || !found {
		t.Fatalf("LevelState: found=%v err=%v", found, err)
	}
	if state.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1 (from StartAttempt only; the rejected finish must not touch it)", state.Attempts)
	}
	if state.BestScore != 0 {
		t.Errorf("BestScore = %d, want 0 (the rejected finish must not touch it)", state.BestScore)
	}
	if state.Status != StatusInProgress {
		t.Errorf("Status = %q, want %q (the rejected finish must not touch it)", state.Status, StatusInProgress)
	}
}
