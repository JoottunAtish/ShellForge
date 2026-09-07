package game

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/store"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// passingResult is what fakeVerifier returns for a level that passed, with
// n bonus objectives satisfied.
func passingResult(bonuses int) verify.LevelResult {
	res := verify.LevelResult{
		LevelID:    "nav-01",
		Passed:     true,
		Objectives: []verify.ObjectiveResult{{ID: "location", Status: verify.StatusPass}},
	}
	for i := 0; i < bonuses; i++ {
		res.Objectives = append(res.Objectives, verify.ObjectiveResult{
			ID: "bonus", Status: verify.StatusPass, Optional: true,
		})
	}
	return res
}

// scoredLevel is testLevel with the fields the formula reads actually set.
func scoredLevel() *content.Level {
	l := hintedLevel()
	l.XP = 40
	l.Difficulty = 3
	l.ParCommands = 6
	return l
}

// startedOrchestrator builds an Orchestrator over scoredLevel, starts it,
// and returns everything a test needs to drive it.
func startedOrchestrator(t *testing.T, result verify.LevelResult) (*Orchestrator, *bus.Bus, *recordingBus, *fakeProgress) {
	t.Helper()

	s, verifier, _ := newTestSession(t, Config{Level: scoredLevel()})
	verifier.result = result
	progress := newFakeProgress()
	o, b, rec := newTestOrchestrator(t, s, progress)

	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return o, b, rec, progress
}

func lastLevelPassed(t *testing.T, rec *recordingBus) bus.LevelPassed {
	t.Helper()
	for _, ev := range rec.snapshot() {
		if p, ok := ev.(bus.LevelPassed); ok {
			return p
		}
	}
	t.Fatal("no LevelPassed event was published")
	return bus.LevelPassed{}
}

// --- scoring ---

func TestPassingAwardsTheScoreAndPutsItOnTheEvent(t *testing.T) {
	o, _, rec, _ := startedOrchestrator(t, passingResult(0))

	if _, err := o.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	// 40 base at difficulty 3 is 52, plus 4 for a first try pass. No
	// efficiency bonus: no commands were counted, but par is 6 and zero
	// commands is under it, so 10 more. 52 plus 4 plus 10 is 66.
	want := 66
	if got := o.Score().Total; got != want {
		t.Errorf("Score().Total = %d, want %d (breakdown %+v)", got, want, o.Score().Breakdown)
	}

	passed := lastLevelPassed(t, rec)
	if passed.Score != want {
		t.Errorf("LevelPassed.Score = %d, want %d: the event must carry the same number the store gets", passed.Score, want)
	}
	if !passed.FirstTry {
		t.Error("LevelPassed.FirstTry = false on a level passed by the first check of the first attempt")
	}
}

func TestBonusObjectivesAddToTheScore(t *testing.T) {
	o, _, _, _ := startedOrchestrator(t, passingResult(2))

	if _, err := o.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	// 66 as above, plus 10 per bonus objective.
	if got := o.Score().Total; got != 86 {
		t.Errorf("Score().Total = %d, want 86 (breakdown %+v)", got, o.Score().Breakdown)
	}
}

func TestASecondCheckAfterPassingAwardsNothingFurther(t *testing.T) {
	o, _, rec, _ := startedOrchestrator(t, passingResult(0))

	for i := 0; i < 3; i++ {
		if _, err := o.Check(context.Background()); err != nil {
			t.Fatalf("Check %d: %v", i, err)
		}
	}

	if got := o.Score().Total; got != 66 {
		t.Errorf("Score().Total = %d after three checks, want 66", got)
	}

	passes := 0
	for _, ev := range rec.snapshot() {
		if _, ok := ev.(bus.LevelPassed); ok {
			passes++
		}
	}
	if passes != 1 {
		t.Errorf("%d LevelPassed events, want exactly 1", passes)
	}
}

func TestCloseRecordsTheScoreAndTheCommandCountButNotTheHints(t *testing.T) {
	o, b, _, progress := startedOrchestrator(t, passingResult(0))

	// Two commands, published the way JournalSink publishes them.
	for seq := int64(1); seq <= 2; seq++ {
		b.Publish(context.Background(), bus.CommandExecuted{
			LevelID: "nav-01", AttemptID: 1, Seq: seq, At: time.Now(),
		})
	}
	if _, err := o.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := o.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := progress.finishAttemptArg
	if got.Score != o.Score().Total {
		t.Errorf("FinishAttempt got score %d, want the %d the learner was shown", got.Score, o.Score().Total)
	}
	if got.CommandsUsed != 2 {
		t.Errorf("FinishAttempt got commands %d, want 2", got.CommandsUsed)
	}
	if got.HintsUsed != 0 {
		t.Errorf("FinishAttempt got hints %d, want 0: hints are recorded by AddHintUsed as they are taken, and this column is added to the same one", got.HintsUsed)
	}
}

func TestCommandsFromAnotherAttemptAreNotCounted(t *testing.T) {
	o, b, _, _ := startedOrchestrator(t, passingResult(0))

	b.Publish(context.Background(), bus.CommandExecuted{LevelID: "nav-01", AttemptID: 1, At: time.Now()})
	b.Publish(context.Background(), bus.CommandExecuted{LevelID: "nav-01", AttemptID: 99, At: time.Now()})
	b.Publish(context.Background(), bus.CommandExecuted{LevelID: "files-01", AttemptID: 7, At: time.Now()})

	if got := o.CommandsUsed(); got != 1 {
		t.Errorf("CommandsUsed() = %d, want 1: only this attempt's commands count", got)
	}
}

func TestCommandsPublishedAfterCloseAreNotCounted(t *testing.T) {
	o, b, _, _ := startedOrchestrator(t, passingResult(0))

	if err := o.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	b.Publish(context.Background(), bus.CommandExecuted{LevelID: "nav-01", AttemptID: 1, At: time.Now()})

	if got := o.CommandsUsed(); got != 0 {
		t.Errorf("CommandsUsed() = %d after Close, want 0", got)
	}
}

// The efficiency bonus is the one place a learner-influenced count reaches
// the score, so the direction it can move the number matters: over par must
// award nothing rather than subtract.
func TestGoingOverParAwardsNothingRatherThanSubtracting(t *testing.T) {
	o, b, _, _ := startedOrchestrator(t, passingResult(0))

	for seq := int64(1); seq <= 9; seq++ {
		b.Publish(context.Background(), bus.CommandExecuted{
			LevelID: "nav-01", AttemptID: 1, Seq: seq, At: time.Now(),
		})
	}
	if _, err := o.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}

	// 52 plus the 4 first try bonus, and nothing for par.
	if got := o.Score().Total; got != 56 {
		t.Errorf("Score().Total = %d over par, want 56 (breakdown %+v)", got, o.Score().Breakdown)
	}
}

// The end to end assertion #126 asks for: the cost shown before confirming
// is the number later subtracted.
func TestHintsTakenAreSubtractedFromTheScore(t *testing.T) {
	o, _, _, _ := startedOrchestrator(t, passingResult(0))
	ctx := context.Background()

	shown, ok := o.PeekHint(false)
	if !ok {
		t.Fatal("PeekHint reported nothing available")
	}
	if _, err := o.TakeHint(ctx, false); err != nil {
		t.Fatalf("TakeHint: %v", err)
	}
	second, _ := o.PeekHint(false)
	if _, err := o.TakeHint(ctx, false); err != nil {
		t.Fatalf("TakeHint (second): %v", err)
	}

	if shown.Cost+second.Cost != 15 {
		t.Fatalf("the two hints shown cost %d, want 15", shown.Cost+second.Cost)
	}

	if _, err := o.Check(ctx); err != nil {
		t.Fatalf("Check: %v", err)
	}

	// 66 as in the plain case, minus exactly the 15 that was shown.
	if got := o.Score().Total; got != 51 {
		t.Errorf("Score().Total = %d, want 51 (breakdown %+v)", got, o.Score().Breakdown)
	}
}

// --- best score and status persistence ---

// Replaying a level already passed and then walking away must not take the
// pass back. store.FinishAttempt writes in_progress for an abandoned
// outcome; Close puts the status back.
func TestAbandoningAReplayDoesNotDowngradeAPassedLevel(t *testing.T) {
	s, verifier, _ := newTestSession(t, Config{Level: scoredLevel()})
	verifier.result = passingResult(0)
	progress := newFakeProgress()
	progress.levelState.Status = store.StatusPassed
	o, _, _ := newTestOrchestrator(t, s, progress)

	ctx := context.Background()
	if err := o.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// No check: the learner replayed and walked away.
	if err := o.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if progress.finishAttemptArg.Outcome != store.OutcomeAbandoned {
		t.Fatalf("Outcome = %q, want %q", progress.finishAttemptArg.Outcome, store.OutcomeAbandoned)
	}
	if len(progress.setLevelStatusArgs) != 1 || progress.setLevelStatusArgs[0] != store.StatusPassed {
		t.Errorf("SetLevelStatus calls = %v, want exactly one restoring %q", progress.setLevelStatusArgs, store.StatusPassed)
	}
}

// The same restore must NOT fire for a level that was never passed: writing
// passed there would award a pass nobody earned.
func TestAbandoningAFreshLevelDoesNotMarkItPassed(t *testing.T) {
	s, verifier, _ := newTestSession(t, Config{Level: scoredLevel()})
	verifier.result = passingResult(0)
	progress := newFakeProgress()
	o, _, _ := newTestOrchestrator(t, s, progress)

	ctx := context.Background()
	if err := o.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := o.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(progress.setLevelStatusArgs) != 0 {
		t.Errorf("SetLevelStatus was called %v on a level never passed, want not at all", progress.setLevelStatusArgs)
	}
}

// --- the hint ladder, through the Orchestrator ---

func TestPeekHintSpendsNothing(t *testing.T) {
	o, _, rec, progress := startedOrchestrator(t, passingResult(0))

	for i := 0; i < 3; i++ {
		if _, ok := o.PeekHint(false); !ok {
			t.Fatal("PeekHint reported nothing available")
		}
	}

	if progress.addHintUsedCalls != 0 {
		t.Errorf("AddHintUsed was called %d times by PeekHint, want 0", progress.addHintUsedCalls)
	}
	if o.HintsTaken() != 0 {
		t.Errorf("HintsTaken() = %d after peeking, want 0", o.HintsTaken())
	}
	for _, ev := range rec.snapshot() {
		if _, ok := ev.(bus.HintTaken); ok {
			t.Error("PeekHint published a HintTaken")
		}
	}
}

func TestTakeHintSpendsOneTierAndPublishesOnce(t *testing.T) {
	o, _, rec, progress := startedOrchestrator(t, passingResult(0))

	tier, err := o.TakeHint(context.Background(), false)
	if err != nil {
		t.Fatalf("TakeHint: %v", err)
	}
	if tier.Index != 1 || tier.Cost != 5 {
		t.Errorf("tier = %+v, want tier 1 at 5 XP", tier)
	}
	if progress.addHintUsedCalls != 1 {
		t.Errorf("AddHintUsed was called %d times, want 1", progress.addHintUsedCalls)
	}
	if o.State() != StateActive {
		t.Errorf("State() = %q after taking a hint, want %q", o.State(), StateActive)
	}

	taken := 0
	for _, ev := range rec.snapshot() {
		if h, ok := ev.(bus.HintTaken); ok {
			taken++
			if h.Tier != 1 || h.Cost != 5 || h.Revealed {
				t.Errorf("HintTaken = %+v, want tier 1 at 5 XP without revealing", h)
			}
		}
	}
	if taken != 1 {
		t.Errorf("%d HintTaken events, want 1", taken)
	}
}

func TestTakeHintRevealsTheSolutionTier(t *testing.T) {
	o, _, _, _ := startedOrchestrator(t, passingResult(0))

	tier, err := o.TakeHint(context.Background(), true)
	if err != nil {
		t.Fatalf("TakeHint(reveal): %v", err)
	}
	if !tier.Reveals || tier.Solution != scoredLevel().Solution {
		t.Errorf("tier = %+v, want the reveal tier carrying the authored solution", tier)
	}
	if o.HintsTaken() != 3 {
		t.Errorf("HintsTaken() = %d after revealing, want 3: revealing ends the ladder", o.HintsTaken())
	}
}

func TestTakeHintRefusesARevealOnALevelWithoutOne(t *testing.T) {
	lvl := scoredLevel()
	lvl.Hints[2].RevealSolution = false

	s, verifier, _ := newTestSession(t, Config{Level: lvl})
	verifier.result = passingResult(0)
	o, _, _ := newTestOrchestrator(t, s, newFakeProgress())
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := o.TakeHint(context.Background(), true); !errors.Is(err, ErrNoRevealTier) {
		t.Errorf("TakeHint(reveal) = %v, want ErrNoRevealTier", err)
	}
	if o.HintsTaken() != 0 {
		t.Error("a refused reveal spent a tier")
	}
}

func TestTakeHintPastTheLastTierSpendsNothing(t *testing.T) {
	o, _, _, progress := startedOrchestrator(t, passingResult(0))
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := o.TakeHint(ctx, false); err != nil {
			t.Fatalf("TakeHint %d: %v", i, err)
		}
	}
	before := progress.addHintUsedCalls

	if _, err := o.TakeHint(ctx, false); !errors.Is(err, ErrLadderExhausted) {
		t.Errorf("TakeHint past the end = %v, want ErrLadderExhausted", err)
	}
	if progress.addHintUsedCalls != before {
		t.Error("a refused take still recorded a hint")
	}
}

// Hints survive a restart, which is the property that makes them cost
// anything at all.
func TestHintsTakenSurviveARestart(t *testing.T) {
	lvl := scoredLevel()
	progress := newFakeProgress()
	ctx := context.Background()

	s1, v1, _ := newTestSession(t, Config{Level: lvl})
	v1.result = passingResult(0)
	first, _, _ := newTestOrchestrator(t, s1, progress)
	if err := first.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := first.TakeHint(ctx, false); err != nil {
			t.Fatalf("TakeHint %d: %v", i, err)
		}
	}
	if err := first.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A new Orchestrator over the same recorded progress.
	s2, v2, _ := newTestSession(t, Config{Level: lvl})
	v2.result = passingResult(0)
	second, _, _ := newTestOrchestrator(t, s2, progress)
	if err := second.Start(ctx); err != nil {
		t.Fatalf("Start (second session): %v", err)
	}

	tier, ok := second.PeekHint(false)
	if !ok {
		t.Fatal("PeekHint reported nothing available after a restart")
	}
	if tier.Index != 3 {
		t.Errorf("the next tier after a restart is %d, want 3: two were already paid for", tier.Index)
	}
}

func TestTakeHintIsRefusedOutsideActive(t *testing.T) {
	s, verifier, _ := newTestSession(t, Config{Level: scoredLevel()})
	verifier.result = passingResult(0)
	o, _, _ := newTestOrchestrator(t, s, newFakeProgress())

	// Idle: Start has not run.
	var te *TransitionError
	if _, err := o.TakeHint(context.Background(), false); !errors.As(err, &te) {
		t.Errorf("TakeHint before Start = %v, want a *TransitionError", err)
	}

	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := o.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := o.TakeHint(context.Background(), false); !errors.Is(err, ErrOrchestratorClosed) {
		t.Errorf("TakeHint after Close = %v, want ErrOrchestratorClosed", err)
	}
}

// A store that will not accept the hint must leave the ladder untouched, so
// the learner is not charged for something they did not receive.
func TestTakeHintSpendsNothingWhenTheStoreRefusesIt(t *testing.T) {
	o, _, rec, progress := startedOrchestrator(t, passingResult(0))
	progress.addHintUsedErr = errors.New("database is locked")

	if _, err := o.TakeHint(context.Background(), false); err == nil {
		t.Fatal("TakeHint succeeded despite the store refusing to record it")
	}
	if o.HintsTaken() != 0 {
		t.Errorf("HintsTaken() = %d, want 0", o.HintsTaken())
	}
	if o.State() != StateActive {
		t.Errorf("State() = %q, want %q: a failed hint is retryable", o.State(), StateActive)
	}
	for _, ev := range rec.snapshot() {
		if _, ok := ev.(bus.HintTaken); ok {
			t.Error("a failed hint published HintTaken")
		}
	}
}
