package game

import (
	"errors"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
)

// hintedLevel is testLevel with a three tier ladder, the last of which
// reveals the solution. Three tiers rather than two because the interesting
// cases (a reveal reached out of order, a partly climbed ladder) need a
// middle rung to skip.
func hintedLevel() *content.Level {
	l := testLevel()
	l.Hints = []content.Hint{
		{Cost: 5, Text: "Kofi kept the manifests somewhere under /srv."},
		{Cost: 10, Text: "Have a look at ls -la /srv before you start grepping."},
		{Cost: 20, Text: "Here is the whole thing.", RevealSolution: true},
	}
	return l
}

func TestLadderNextWalksTheTiersInOrder(t *testing.T) {
	l := NewLadder(hintedLevel(), 0)

	for want := 1; want <= 3; want++ {
		tier, ok := l.Next()
		if !ok {
			t.Fatalf("tier %d: Next reported nothing available", want)
		}
		if tier.Index != want {
			t.Fatalf("Next().Index = %d, want %d", tier.Index, want)
		}
		if tier.Total != 3 {
			t.Errorf("Next().Total = %d, want 3", tier.Total)
		}
		if _, err := l.Take(want); err != nil {
			t.Fatalf("Take(%d): %v", want, err)
		}
	}

	if _, ok := l.Next(); ok {
		t.Error("Next reported a fourth tier on a three tier level")
	}
}

func TestLadderNextCostsTheAuthoredCost(t *testing.T) {
	l := NewLadder(hintedLevel(), 0)
	tier, ok := l.Next()
	if !ok {
		t.Fatal("Next reported nothing available")
	}
	if tier.Cost != 5 {
		t.Errorf("Cost = %d, want the authored 5", tier.Cost)
	}
	if tier.Reveals {
		t.Error("tier 1 must not claim to reveal the solution")
	}
}

func TestLadderTakeRefusesASkippedTier(t *testing.T) {
	l := NewLadder(hintedLevel(), 0)

	// Tier 2 is neither the next tier nor the reveal tier.
	_, err := l.Take(2)
	if !errors.Is(err, ErrTierOutOfOrder) {
		t.Fatalf("Take(2) on a fresh ladder = %v, want ErrTierOutOfOrder", err)
	}
	if l.Taken() != 0 {
		t.Errorf("Taken() = %d after a refused take, want 0: a refusal must spend nothing", l.Taken())
	}
}

func TestLadderRevealJumpsAndChargesEveryTierItSkipped(t *testing.T) {
	l := NewLadder(hintedLevel(), 0)

	tier, ok := l.Reveal()
	if !ok {
		t.Fatal("Reveal reported nothing available on a level that authored a reveal tier")
	}
	if tier.Index != 3 {
		t.Errorf("Reveal().Index = %d, want 3", tier.Index)
	}
	// 5 plus 10 plus 20: reading the answer is not cheaper than working up
	// to it.
	if tier.Cost != 35 {
		t.Errorf("Reveal().Cost = %d, want 35 (every tier from the next up to the reveal)", tier.Cost)
	}
	if !tier.Reveals {
		t.Error("Reveal().Reveals = false")
	}
	if tier.Solution != hintedLevel().Solution {
		t.Errorf("Reveal().Solution = %q, want the level's authored solution verbatim", tier.Solution)
	}

	if _, err := l.Take(3); err != nil {
		t.Fatalf("Take(3): %v", err)
	}
	if l.Taken() != 3 {
		t.Errorf("Taken() = %d after revealing, want 3: revealing ends the ladder", l.Taken())
	}
}

func TestLadderRevealCostsLessOnceTiersHaveBeenPaidFor(t *testing.T) {
	l := NewLadder(hintedLevel(), 1) // tier 1 already paid for

	tier, ok := l.Reveal()
	if !ok {
		t.Fatal("Reveal reported nothing available")
	}
	if tier.Cost != 30 {
		t.Errorf("Reveal().Cost = %d, want 30 (tiers 2 and 3 only)", tier.Cost)
	}
}

func TestLadderRevealIsUnavailableWithoutARevealTier(t *testing.T) {
	l := hintedLevel()
	l.Hints[2].RevealSolution = false

	ladder := NewLadder(l, 0)
	if _, ok := ladder.Reveal(); ok {
		t.Error("Reveal reported available on a level that authored no reveal tier")
	}
	if ladder.HasReveal() {
		t.Error("HasReveal reported true on a level that authored no reveal tier")
	}
}

func TestLadderRevealIsUnavailableOnceTaken(t *testing.T) {
	l := NewLadder(hintedLevel(), 3)

	if _, ok := l.Reveal(); ok {
		t.Error("Reveal reported available after the whole ladder was taken")
	}
	if !l.HasReveal() {
		t.Error("HasReveal must still report true: the level authored one, it has just been spent")
	}
}

func TestLadderCostsTakenFollowsTheCount(t *testing.T) {
	cases := []struct {
		taken int
		want  []int
	}{
		{taken: 0, want: nil},
		{taken: 1, want: []int{5}},
		{taken: 2, want: []int{5, 10}},
		{taken: 3, want: []int{5, 10, 20}},
	}

	for _, tc := range cases {
		got := NewLadder(hintedLevel(), tc.taken).CostsTaken()
		if len(got) != len(tc.want) {
			t.Fatalf("taken %d: CostsTaken() = %v, want %v", tc.taken, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("taken %d: CostsTaken() = %v, want %v", tc.taken, got, tc.want)
			}
		}
	}
}

// A count out of range comes from level_state.hints_used on a level whose
// author has since removed a hint. Clamping keeps that learner playing.
func TestLadderClampsATakenCountOutOfRange(t *testing.T) {
	if got := NewLadder(hintedLevel(), 99).Taken(); got != 3 {
		t.Errorf("Taken() = %d for an over-large count, want it clamped to 3", got)
	}
	if got := NewLadder(hintedLevel(), -4).Taken(); got != 0 {
		t.Errorf("Taken() = %d for a negative count, want it clamped to 0", got)
	}
}

func TestLadderOnALevelWithNoHints(t *testing.T) {
	l := NewLadder(testLevel(), 0)

	if _, ok := l.Next(); ok {
		t.Error("Next reported a tier on a level with no hints")
	}
	if _, err := l.Take(1); !errors.Is(err, ErrLadderExhausted) {
		t.Errorf("Take(1) = %v, want ErrLadderExhausted", err)
	}
}

func TestNewLadderOnANilLevelIsEmptyRatherThanAPanic(t *testing.T) {
	l := NewLadder(nil, 3)
	if l.Total() != 0 || l.Taken() != 0 {
		t.Errorf("NewLadder(nil, 3) = %d taken of %d, want an empty ladder", l.Taken(), l.Total())
	}
}
