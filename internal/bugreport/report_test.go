package bugreport

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/doctor"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// fakeProber is a scriptable Prober.
type fakeProber struct {
	sb  Sandbox
	err error
}

func (f fakeProber) Probe(ctx context.Context) (Sandbox, error) {
	return f.sb, f.err
}

// newTestStore opens a fresh store in a temp directory, so each test starts
// from a clean, empty database.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	s, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seedEvent inserts one events row directly, so tests can control seq, ts,
// level_id, cwd, raw, exit_code and duration_ms without going through the
// journal package, which this package does not depend on for reading.
func seedEvent(t *testing.T, s *store.Store, seq int64, levelID, cwd, raw string, exit int, durationMS int64) {
	t.Helper()
	ts := float64(time.Now().UnixMicro()) / 1e6
	_, err := s.DB().ExecContext(context.Background(),
		`INSERT INTO events (seq, ts, level_id, cwd, raw, exit_code, duration_ms, used_tab, used_history)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0)`,
		seq, ts, levelID, cwd, raw, exit, durationMS,
	)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

// seedPassedLevel marks one level passed, with at least one attempt, for
// profileID under packID.
func seedPassedLevel(t *testing.T, s *store.Store, profileID int64, packID, levelID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.StartAttempt(ctx, profileID, packID, levelID, 1, time.Now()); err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}
	if err := s.SetLevelStatus(ctx, profileID, packID, levelID, 1, store.StatusPassed); err != nil {
		t.Fatalf("SetLevelStatus: %v", err)
	}
}

func testPack() *content.Pack {
	return &content.Pack{
		ID:         "core-linux-basics",
		Version:    "1.2.3",
		ContentAPI: content.ContentAPIVersion,
		Levels:     make([]content.Level, 5),
	}
}

func notesContain(notes []string, substr string) bool {
	for _, n := range notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

// TestCollectWithEverySourcePresent asserts every field populates when every
// source is present, with Notes empty except the no-journal note: the
// default is --journal off, so that note always fires unless a caller opts
// in.
func TestCollectWithEverySourcePresent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	profile, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	seedPassedLevel(t, s, profile.ID, "core-linux-basics", "nav-01")

	build := Build{Version: "v0.1.0", Commit: "abc123", BuildDate: "2026-09-08", GoVersion: "go1.25.0", GOOS: "linux", GOARCH: "amd64"}
	dr := doctor.Report{Platform: "linux", Version: "v0.1.0"}
	prober := fakeProber{sb: Sandbox{Backend: "docker", Provisioned: true, Running: true, Detail: "ok"}}

	report, err := Collect(ctx, Sources{
		Build:     build,
		Doctor:    dr,
		Prober:    prober,
		Pack:      testPack(),
		Store:     s,
		ProfileID: profile.ID,
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if report.Build != build {
		t.Errorf("Build = %+v, want %+v", report.Build, build)
	}
	if report.Sandbox == nil {
		t.Fatalf("Sandbox = nil, want %+v", prober.sb)
	}
	got := *report.Sandbox
	if got.Backend != prober.sb.Backend || got.Detail != prober.sb.Detail ||
		got.Provisioned != prober.sb.Provisioned || got.Running != prober.sb.Running {
		t.Errorf("Sandbox = %+v, want %+v", got, prober.sb)
	}
	if report.Pack.ID != "core-linux-basics" || report.Pack.Levels != 5 {
		t.Errorf("Pack = %+v, unexpected", report.Pack)
	}
	if !report.Progress.DatabaseFound {
		t.Errorf("Progress.DatabaseFound = false, want true")
	}
	if report.Progress.Passed != 1 {
		t.Errorf("Progress.Passed = %d, want 1", report.Progress.Passed)
	}
	if report.JournalIncluded {
		t.Errorf("JournalIncluded = true, want false: --journal was not requested")
	}
	if len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "not included") {
		t.Errorf("Notes = %v, want exactly one note about the journal not being included", report.Notes)
	}
}

// TestCollectWithANilProberRecordsANote asserts a nil Prober degrades to a
// note rather than an error, and leaves Sandbox nil.
func TestCollectWithANilProberRecordsANote(t *testing.T) {
	report, err := Collect(context.Background(), Sources{Pack: testPack()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if report.Sandbox != nil {
		t.Errorf("Sandbox = %+v, want nil", report.Sandbox)
	}
	if !notesContain(report.Notes, "no runtime was resolved") {
		t.Errorf("Notes = %v, want one naming the missing runtime", report.Notes)
	}
}

// TestCollectWithAFailingProberRecordsANote asserts a Prober error degrades
// to a note naming the error, never to a Collect error.
func TestCollectWithAFailingProberRecordsANote(t *testing.T) {
	proberErr := errors.New("docker daemon is not running")
	report, err := Collect(context.Background(), Sources{
		Pack:   testPack(),
		Prober: fakeProber{err: proberErr},
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if report.Sandbox != nil {
		t.Errorf("Sandbox = %+v, want nil", report.Sandbox)
	}
	if !notesContain(report.Notes, proberErr.Error()) {
		t.Errorf("Notes = %v, want one naming %q", report.Notes, proberErr.Error())
	}
}

// TestCollectWithANilStoreRecordsANote asserts a nil Store degrades to a
// note, DatabaseFound stays false, and Collect still returns no error.
func TestCollectWithANilStoreRecordsANote(t *testing.T) {
	report, err := Collect(context.Background(), Sources{Pack: testPack()})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if report.Progress.DatabaseFound {
		t.Errorf("Progress.DatabaseFound = true, want false")
	}
	if !notesContain(report.Notes, "no progress database was found") {
		t.Errorf("Notes = %v, want one about the missing database", report.Notes)
	}
}

// TestCollectWithANilPackRecordsANote asserts a nil Pack degrades to a note
// and a zero Pack, with the Store present so the progress note does not
// also fire and confuse this assertion.
func TestCollectWithANilPackRecordsANote(t *testing.T) {
	s := newTestStore(t)
	report, err := Collect(context.Background(), Sources{Store: s})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if report.Pack != (Pack{}) {
		t.Errorf("Pack = %+v, want zero value", report.Pack)
	}
	if !notesContain(report.Notes, "no content pack was supplied") {
		t.Errorf("Notes = %v, want one about the missing pack", report.Notes)
	}
}

// TestCollectCapsTheJournalAtTheLimit seeds 700 entries and asserts exactly
// 500 (DefaultJournalLimit) come back, ordered oldest first so the newest
// entry is last.
func TestCollectCapsTheJournalAtTheLimit(t *testing.T) {
	s := newTestStore(t)
	const total = 700
	for i := int64(1); i <= total; i++ {
		seedEvent(t, s, i, "nav-01", "/home/learner", "echo hi", 0, 10)
	}

	report, err := Collect(context.Background(), Sources{
		Store:          s,
		IncludeJournal: true,
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(report.Journal) != DefaultJournalLimit {
		t.Fatalf("len(Journal) = %d, want %d", len(report.Journal), DefaultJournalLimit)
	}
	newest := report.Journal[len(report.Journal)-1]
	if newest.Seq != total {
		t.Errorf("newest entry Seq = %d, want %d (newest last)", newest.Seq, total)
	}
	oldest := report.Journal[0]
	wantOldestSeq := int64(total - DefaultJournalLimit + 1)
	if oldest.Seq != wantOldestSeq {
		t.Errorf("oldest entry Seq = %d, want %d", oldest.Seq, wantOldestSeq)
	}
}

// TestCollectCountsRedactions asserts JournalRedacted equals the number of
// entries Redact actually changed, not the total entry count.
func TestCollectCountsRedactions(t *testing.T) {
	s := newTestStore(t)
	seedEvent(t, s, 1, "nav-01", "/home/learner", "export PASSWORD=hunter2", 0, 5)
	seedEvent(t, s, 2, "nav-01", "/home/learner", "ls -la", 0, 5)
	seedEvent(t, s, 3, "nav-01", "/home/learner", "curl --token=abc123 https://example.com", 0, 5)

	report, err := Collect(context.Background(), Sources{
		Store:          s,
		IncludeJournal: true,
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if report.JournalRedacted != 2 {
		t.Errorf("JournalRedacted = %d, want 2", report.JournalRedacted)
	}
	for _, c := range report.Journal {
		if strings.Contains(c.Text, "hunter2") || strings.Contains(c.Text, "abc123") {
			t.Errorf("command text still carries a secret: %q", c.Text)
		}
	}
}
