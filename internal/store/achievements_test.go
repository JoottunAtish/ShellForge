package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openWithProfile returns an open store and its single profile id, which is
// what every test below needs before it can write a row keyed on one.
func openWithProfile(t *testing.T) (*Store, int64, context.Context) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "progress.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	p, err := s.EnsureProfile(ctx, "learner")
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	return s, p.ID, ctx
}

func TestAchievementsIsEmptyAndNotNilOnAFreshProfile(t *testing.T) {
	s, profileID, ctx := openWithProfile(t)

	got, err := s.Achievements(ctx, profileID)
	if err != nil {
		t.Fatalf("Achievements: %v", err)
	}
	if got == nil {
		t.Fatal("Achievements returned nil; a fresh profile must read back as an empty map")
	}
	if len(got) != 0 {
		t.Errorf("Achievements = %v, want empty", got)
	}
}

func TestSaveProgressCreatesThenUpdatesWithoutUnlocking(t *testing.T) {
	s, profileID, ctx := openWithProfile(t)

	if err := s.SaveProgress(ctx, profileID, "tab_master", 3); err != nil {
		t.Fatalf("SaveProgress (create): %v", err)
	}
	if err := s.SaveProgress(ctx, profileID, "tab_master", 24); err != nil {
		t.Fatalf("SaveProgress (update): %v", err)
	}

	got, err := s.Achievements(ctx, profileID)
	if err != nil {
		t.Fatalf("Achievements: %v", err)
	}
	a := got["tab_master"]
	if a.Progress != 24 {
		t.Errorf("Progress = %v, want 24", a.Progress)
	}
	if a.Unlocked() {
		t.Error("SaveProgress must never unlock an achievement")
	}
}

func TestUnlockReportsTrueExactlyOnceAndKeepsTheFirstTime(t *testing.T) {
	s, profileID, ctx := openWithProfile(t)

	first := time.Unix(1_700_000_000, 0).UTC()
	second := first.Add(24 * time.Hour)

	unlocked, err := s.Unlock(ctx, profileID, "first_blood", first)
	if err != nil {
		t.Fatalf("Unlock (first): %v", err)
	}
	if !unlocked {
		t.Fatal("the first Unlock must report true")
	}

	unlocked, err = s.Unlock(ctx, profileID, "first_blood", second)
	if err != nil {
		t.Fatalf("Unlock (second): %v", err)
	}
	if unlocked {
		t.Error("a second Unlock must report false, so no badge is announced twice")
	}

	got, err := s.Achievements(ctx, profileID)
	if err != nil {
		t.Fatalf("Achievements: %v", err)
	}
	if !got["first_blood"].UnlockedAt.Equal(first) {
		t.Errorf("UnlockedAt = %v, want the first unlock time %v", got["first_blood"].UnlockedAt, first)
	}
}

// The counter survives the unlock, because a rule that keeps counting after
// it has been earned must not have its progress thrown away.
func TestSaveProgressAfterUnlockDoesNotClearTheUnlock(t *testing.T) {
	s, profileID, ctx := openWithProfile(t)

	if _, err := s.Unlock(ctx, profileID, "tab_master", time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := s.SaveProgress(ctx, profileID, "tab_master", 99); err != nil {
		t.Fatalf("SaveProgress: %v", err)
	}

	got, err := s.Achievements(ctx, profileID)
	if err != nil {
		t.Fatalf("Achievements: %v", err)
	}
	if !got["tab_master"].Unlocked() {
		t.Error("SaveProgress cleared unlocked_at; an earned achievement must stay earned")
	}
	if got["tab_master"].Progress != 99 {
		t.Errorf("Progress = %v, want 99", got["tab_master"].Progress)
	}
}

// A never-unlocked achievement reads back as the zero time.Time, never as
// the unix epoch. Same rule as LevelState.FirstPassedAt.
func TestAnUnearnedAchievementReadsBackAsTheZeroTime(t *testing.T) {
	s, profileID, ctx := openWithProfile(t)

	if err := s.SaveProgress(ctx, profileID, "one_liner", 2); err != nil {
		t.Fatalf("SaveProgress: %v", err)
	}

	got, err := s.Achievements(ctx, profileID)
	if err != nil {
		t.Fatalf("Achievements: %v", err)
	}
	if !got["one_liner"].UnlockedAt.IsZero() {
		t.Errorf("UnlockedAt = %v, want the zero time", got["one_liner"].UnlockedAt)
	}
}

func TestAddHintUsedCreatesTheRowThenIncrementsIt(t *testing.T) {
	s, profileID, ctx := openWithProfile(t)

	for i := 0; i < 3; i++ {
		if err := s.AddHintUsed(ctx, profileID, "core", "nav-01", 1); err != nil {
			t.Fatalf("AddHintUsed %d: %v", i, err)
		}
	}

	st, ok, err := s.LevelState(ctx, profileID, "nav-01", 1)
	if err != nil {
		t.Fatalf("LevelState: %v", err)
	}
	if !ok {
		t.Fatal("AddHintUsed did not create a level_state row")
	}
	if st.HintsUsed != 3 {
		t.Errorf("HintsUsed = %d, want 3", st.HintsUsed)
	}
}

// The double counting guard AddHintUsed's doc comment describes: a caller
// that records hints as they are taken must pass zero to FinishAttempt, and
// this test is what proves the two would otherwise collide.
func TestAddHintUsedAndFinishAttemptDoNotDoubleCount(t *testing.T) {
	s, profileID, ctx := openWithProfile(t)

	attemptID, err := s.StartAttempt(ctx, profileID, "core", "nav-01", 1, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatalf("StartAttempt: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := s.AddHintUsed(ctx, profileID, "core", "nav-01", 1); err != nil {
			t.Fatalf("AddHintUsed: %v", err)
		}
	}

	// Zero, exactly as the orchestrator passes it.
	if err := s.FinishAttempt(ctx, attemptID, Attempt{
		Outcome: OutcomePassed,
		EndedAt: time.Unix(1_700_000_060, 0),
	}); err != nil {
		t.Fatalf("FinishAttempt: %v", err)
	}

	st, _, err := s.LevelState(ctx, profileID, "nav-01", 1)
	if err != nil {
		t.Fatalf("LevelState: %v", err)
	}
	if st.HintsUsed != 2 {
		t.Errorf("HintsUsed = %d, want 2: two hints taken must be recorded exactly twice", st.HintsUsed)
	}
}
