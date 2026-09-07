package game

import (
	"errors"
	"fmt"

	"github.com/JoottunAtish/ShellForge/internal/content"
)

// The hint ladder: one level's authored hints, plus how far up them this
// learner has already paid to climb.
//
// A Ladder is pure. It reads no database and publishes nothing: the
// Orchestrator seeds it from level_state.hints_used at Start, spends
// through it, and records the spend. Keeping the arithmetic here and the
// persistence there is what lets every ordering rule below be tested with a
// literal and no sqlite file.

// Ladder sentinel errors. Compare with errors.Is.
var (
	// ErrLadderExhausted means every tier has already been taken.
	ErrLadderExhausted = errors.New("game: every hint on this level has been taken")

	// ErrNoRevealTier means the level authored no hint with
	// reveal_solution, so there is no solution to reveal.
	//
	// This is a deliberate divergence from issue #126's interface sketch,
	// which says Reveal falls back to "the final tier" when no tier is
	// marked. Its own acceptance criteria say --reveal must be "refused
	// with a clear message on a level that authored no reveal tier", and
	// that is the behaviour implemented: printing the last ordinary hint
	// under a banner that promised the solution would charge a learner the
	// reveal price for something that is not the solution.
	ErrNoRevealTier = errors.New("game: this level has no solution tier")

	// ErrTierOutOfOrder means a caller asked for a tier that is neither the
	// next one nor the reveal tier.
	ErrTierOutOfOrder = errors.New("game: hints are taken in order")
)

// Tier is one rung of the ladder, described without being spent.
type Tier struct {
	// Index is 1-based, matching the "Hint 1 of 3" a learner reads.
	Index int

	// Cost is the XP this tier costs to take now.
	//
	// For an ordinary tier that is the authored cost. For the reveal tier
	// reached out of order it is the sum of every tier from the next one up
	// to and including the reveal tier: see Reveal for why.
	Cost int

	// Total is how many tiers this level has.
	Total int

	// Reveals is true when taking this tier prints the level's solution.
	Reveals bool

	// Text is the authored hint text.
	Text string

	// Solution is the level's authored solution, and is populated only on
	// the reveal tier. It is the same string the golden test runs, verbatim
	// rather than paraphrased, so a learner who pays to reveal sees exactly
	// what the level considers correct.
	Solution string
}

// Ladder is one level's hints plus what this learner has already taken.
type Ladder struct {
	hints    []content.Hint
	solution string
	taken    int
}

// NewLadder returns the ladder for level with taken tiers already spent.
//
// taken is clamped into range rather than refused: it comes from
// level_state.hints_used, which an author can outdate by removing a hint
// from a level that a learner had already climbed past. A clamp keeps that
// learner playing; refusing would make the level unplayable for exactly the
// people who had engaged with it most.
func NewLadder(level *content.Level, taken int) *Ladder {
	l := &Ladder{}
	if level == nil {
		return l
	}
	l.hints = level.Hints
	l.solution = level.Solution
	if taken < 0 {
		taken = 0
	}
	if taken > len(l.hints) {
		taken = len(l.hints)
	}
	l.taken = taken
	return l
}

// Taken reports how many tiers have been spent.
func (l *Ladder) Taken() int { return l.taken }

// Total reports how many tiers this level has.
func (l *Ladder) Total() int { return len(l.hints) }

// CostsTaken returns the authored cost of every tier spent so far, in tier
// order, which is what the scorer subtracts.
//
// It is derived from the count rather than from a record of individual
// takes, and that is what makes a single hints_used integer enough to
// persist the ladder: tiers are always spent contiguously from the bottom,
// including when the reveal tier is reached out of order. See Reveal.
func (l *Ladder) CostsTaken() []int {
	if l.taken == 0 {
		return nil
	}
	costs := make([]int, 0, l.taken)
	for _, h := range l.hints[:l.taken] {
		costs = append(costs, h.Cost)
	}
	return costs
}

// Next describes the tier a learner would get, without spending anything.
// available is false when every tier has been taken, or the level authored
// none.
func (l *Ladder) Next() (tier Tier, available bool) {
	if l.taken >= len(l.hints) {
		return Tier{}, false
	}
	return l.tierAt(l.taken+1, l.hints[l.taken].Cost), true
}

// Reveal describes the solution tier, without spending anything.
// available is false when the level authored no tier with reveal_solution,
// or when the reveal tier has already been taken.
//
// The cost is the sum of every tier from the next one up to and including
// the reveal tier, not the reveal tier's own authored cost alone. Skipping
// tiers 2 and 3 to read the answer is not cheaper than working through
// them, and charging it that way is also what keeps a single hints_used
// integer sufficient: tiers are always spent contiguously from the bottom,
// so the ladder can be rebuilt from a count after a restart.
func (l *Ladder) Reveal() (tier Tier, available bool) {
	idx := l.revealIndex()
	if idx == 0 || l.taken >= idx {
		return Tier{}, false
	}
	cost := 0
	for _, h := range l.hints[l.taken:idx] {
		cost += h.Cost
	}
	return l.tierAt(idx, cost), true
}

// Take spends a tier and returns what the learner gets.
//
// index must be either the next tier or the reveal tier: a caller cannot
// skip the ladder by asking for tier 4 first. Taking the reveal tier spends
// every tier below it too, which is what its cost already said it would.
func (l *Ladder) Take(index int) (Tier, error) {
	if len(l.hints) == 0 {
		return Tier{}, fmt.Errorf("take hint %d: %w", index, ErrLadderExhausted)
	}
	if l.taken >= len(l.hints) {
		return Tier{}, fmt.Errorf("take hint %d: %w", index, ErrLadderExhausted)
	}

	var (
		tier Tier
		ok   bool
	)
	switch index {
	case l.taken + 1:
		tier, ok = l.Next()
	case l.revealIndex():
		tier, ok = l.Reveal()
	default:
		return Tier{}, fmt.Errorf("take hint %d when hint %d is next: %w", index, l.taken+1, ErrTierOutOfOrder)
	}
	if !ok {
		return Tier{}, fmt.Errorf("take hint %d: %w", index, ErrLadderExhausted)
	}

	l.taken = tier.Index
	return tier, nil
}

// revealIndex is the 1-based index of the first tier marked
// reveal_solution, or zero when the level authored none.
func (l *Ladder) revealIndex() int {
	for i, h := range l.hints {
		if h.RevealSolution {
			return i + 1
		}
	}
	return 0
}

// tierAt builds the Tier value for a 1-based index at a computed cost.
func (l *Ladder) tierAt(index, cost int) Tier {
	h := l.hints[index-1]
	tier := Tier{
		Index:   index,
		Cost:    cost,
		Total:   len(l.hints),
		Reveals: h.RevealSolution,
		Text:    h.Text,
	}
	if h.RevealSolution {
		tier.Solution = l.solution
	}
	return tier
}

// HasReveal reports whether the level authored a tier marked
// reveal_solution.
//
// It exists so a caller can tell "this level has no solution tier" from
// "you have already revealed it", which are different messages to a learner
// and would otherwise both arrive as Reveal returning false.
func (l *Ladder) HasReveal() bool { return l.revealIndex() != 0 }
