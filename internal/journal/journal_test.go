package journal

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/store"
)

// newTestJournal opens a fresh store in a temp directory and wraps it in a
// Journal, so each test starts from a clean, empty events table.
func newTestJournal(t *testing.T) (*Journal, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	s, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s), s
}

func TestAppendThenLevelPreservesEveryField(t *testing.T) {
	j, _ := newTestJournal(t)

	want := Entry{
		Seq:         42,
		TS:          time.Date(2026, 8, 13, 12, 34, 56, 123456000, time.UTC),
		LevelID:     "pipes-03",
		Cwd:         "/home/learner/my quest",
		Raw:         "grep -ri 'error' logs/\t| wc -l\nsecond line\t\"quoted\"",
		Exit:        -1,
		DurationMS:  84,
		UsedTab:     true,
		UsedHistory: true,
	}

	if err := j.Append(context.Background(), want); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := j.Level(context.Background(), want.LevelID)
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Level returned %d entries, want 1", len(got))
	}

	e := got[0]
	if e.Seq != want.Seq {
		t.Errorf("Seq = %d, want %d", e.Seq, want.Seq)
	}
	if !e.TS.UTC().Equal(want.TS.UTC()) {
		t.Errorf("TS = %s, want %s", e.TS, want.TS)
	}
	if e.LevelID != want.LevelID {
		t.Errorf("LevelID = %q, want %q", e.LevelID, want.LevelID)
	}
	if e.Cwd != want.Cwd {
		t.Errorf("Cwd = %q, want %q", e.Cwd, want.Cwd)
	}
	if e.Raw != want.Raw {
		t.Errorf("Raw = %q, want %q", e.Raw, want.Raw)
	}
	if e.Exit != want.Exit {
		t.Errorf("Exit = %d, want %d", e.Exit, want.Exit)
	}
	if e.DurationMS != want.DurationMS {
		t.Errorf("DurationMS = %d, want %d", e.DurationMS, want.DurationMS)
	}
	if e.UsedTab != want.UsedTab {
		t.Errorf("UsedTab = %t, want %t", e.UsedTab, want.UsedTab)
	}
	if e.UsedHistory != want.UsedHistory {
		t.Errorf("UsedHistory = %t, want %t", e.UsedHistory, want.UsedHistory)
	}
}

func TestAppendTruncatesTimestampsToMicroseconds(t *testing.T) {
	j, _ := newTestJournal(t)

	original := time.Date(2026, 8, 13, 12, 34, 56, 123456789, time.UTC)
	truncated := time.UnixMicro(original.UnixMicro()).UTC()

	entry := Entry{Seq: 1, TS: original, LevelID: "precision-01", Cwd: "/", Raw: "echo hi"}
	if err := j.Append(context.Background(), entry); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := j.Level(context.Background(), "precision-01")
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Level returned %d entries, want 1", len(got))
	}

	if !got[0].TS.Equal(truncated) {
		t.Errorf("TS = %s, want %s (truncated to microseconds)", got[0].TS, truncated)
	}
	if got[0].TS.Equal(original) {
		t.Error("TS equals the original nanosecond-precision instant; truncation did not happen")
	}
}

func TestLevelReturnsOnlyThatLevelInSeqOrder(t *testing.T) {
	j, _ := newTestJournal(t)
	base := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)

	entries := []Entry{
		{Seq: 2, TS: base, LevelID: "a", Raw: "a2", Cwd: "/"},
		{Seq: 1, TS: base, LevelID: "b", Raw: "b1", Cwd: "/"},
		{Seq: 1, TS: base, LevelID: "a", Raw: "a1", Cwd: "/"},
		{Seq: 2, TS: base, LevelID: "b", Raw: "b2", Cwd: "/"},
	}
	for _, e := range entries {
		if err := j.Append(context.Background(), e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := j.Level(context.Background(), "a")
	if err != nil {
		t.Fatalf("Level: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Level(a) returned %d entries, want 2", len(got))
	}
	if got[0].Raw != "a1" || got[1].Raw != "a2" {
		t.Errorf("Level(a) order = %q, %q, want a1, a2", got[0].Raw, got[1].Raw)
	}
}

func TestCommandsForEveryScopeKind(t *testing.T) {
	j, _ := newTestJournal(t)
	base := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)

	fixture := []Entry{
		{Seq: 1, TS: base, LevelID: "nav-01", Raw: "cd quest", Cwd: "/"},
		{Seq: 2, TS: base.Add(time.Second), LevelID: "nav-01", Raw: "ls", Cwd: "/"},
		{Seq: 1, TS: base.Add(2 * time.Second), LevelID: "pipes-03", Raw: "grep foo", Cwd: "/"},
		{Seq: 2, TS: base.Add(3 * time.Second), LevelID: "pipes-03", Raw: "wc -l", Cwd: "/"},
		{Seq: 3, TS: base.Add(4 * time.Second), LevelID: "pipes-03", Raw: "cat out", Cwd: "/"},
	}
	for _, e := range fixture {
		if err := j.Append(context.Background(), e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	if got := j.Commands(Scope{Kind: ScopeLevel}); !equalStrings(got, []string{"grep foo", "wc -l", "cat out"}) {
		t.Errorf("ScopeLevel = %v", got)
	}
	if got := j.Commands(Scope{Kind: ScopeLastN, N: 2}); !equalStrings(got, []string{"wc -l", "cat out"}) {
		t.Errorf("ScopeLastN N=2 = %v", got)
	}
	if got := j.Commands(Scope{Kind: ScopeLastN, N: 0}); len(got) != 0 {
		t.Errorf("ScopeLastN N=0 = %v, want empty", got)
	}
	if got := j.Commands(Scope{Kind: ScopeLastN, N: -1}); len(got) != 0 {
		t.Errorf("ScopeLastN N=-1 = %v, want empty", got)
	}
	if got := j.Commands(Scope{Kind: ScopeLastN, N: 100}); !equalStrings(got, []string{"cd quest", "ls", "grep foo", "wc -l", "cat out"}) {
		t.Errorf("ScopeLastN N=100 = %v", got)
	}
	if got := j.Commands(Scope{Kind: ScopeLast}); !equalStrings(got, []string{"cat out"}) {
		t.Errorf("ScopeLast = %v", got)
	}
	if got := j.Commands(Scope{Kind: "bogus"}); len(got) != 0 {
		t.Errorf("unknown kind = %v, want empty", got)
	}
	if j.Err() == nil {
		t.Error("Err() is nil after an unknown scope kind")
	}
}

func TestCommandsOnAnEmptyTableReturnsEmptyForEveryScopeKind(t *testing.T) {
	j, _ := newTestJournal(t)
	for _, kind := range []ScopeKind{ScopeLevel, ScopeLastN, ScopeLast} {
		if got := j.Commands(Scope{Kind: kind, N: 5}); len(got) != 0 {
			t.Errorf("kind %s on an empty table = %v, want empty", kind, got)
		}
	}
}

func TestCommandsSurfacesAQueryFailureThroughErr(t *testing.T) {
	j, s := newTestJournal(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := j.Commands(Scope{Kind: ScopeLast})
	if len(got) != 0 {
		t.Errorf("Commands after Close = %v, want empty", got)
	}
	if j.Err() == nil {
		t.Error("Err() is nil after querying a closed store")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
