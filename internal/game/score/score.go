package score

import "math"

// Inputs is everything the formula reads.
//
// Every field comes from the level as authored or from the attempt as
// recorded. Nothing here is read from a clock, a database, or a journal
// exit code inside this package: see the package doc comment for why the
// last of those matters.
type Inputs struct {
	// BaseXP is the level's authored award, content.Level.XP. A negative
	// value is treated as zero rather than refused.
	BaseXP int

	// Difficulty is content.Level.Difficulty, 1 to 5. Anything outside that
	// range is clamped, not refused: see clampDifficulty.
	Difficulty int

	// HintCosts is the authored cost of each hint actually taken, in tier
	// order. Nil means no hint was taken.
	HintCosts []int

	// ParCommands is content.Level.ParCommands. Zero disables the
	// efficiency bonus rather than demanding perfection, which is what the
	// level format already documents it to mean.
	ParCommands int

	// CommandsUsed is how many commands the learner ran during this
	// attempt.
	CommandsUsed int

	// FirstTry is true when this is attempt 1 and it passed.
	FirstTry bool

	// OptionalHit is how many bonus objectives were satisfied. A negative
	// value awards nothing.
	OptionalHit int
}

// Breakdown is one line of the arithmetic, so the pass banner can show the
// learner where the number came from instead of asserting it.
type Breakdown struct {
	// Label is one of the six labels declared below.
	Label string

	// Delta is the signed contribution to the total.
	Delta int
}

// Result is the award and the arithmetic that produced it. Breakdown always
// sums exactly to Total.
type Result struct {
	// Total is the award, never negative.
	Total int

	// Breakdown is the arithmetic, in the order it was applied. LabelBase
	// is always present, even at zero, because a breakdown that starts
	// somewhere other than the level's own XP reads as though a line went
	// missing. Every other line appears only when its delta is non-zero: a
	// "difficulty +0" line on a difficulty 1 level is noise, not
	// information.
	Breakdown []Breakdown
}

// The six breakdown labels. Exported so the renderer names them from here
// rather than repeating string literals that could drift.
const (
	LabelBase       = "base"
	LabelDifficulty = "difficulty"
	LabelEfficiency = "efficiency"
	LabelFirstTry   = "first try"
	LabelBonus      = "bonus objectives"
	LabelHints      = "hints"
)

// difficultyMultiplier is indexed by Inputs.Difficulty, 1 to 5. Index 0 is
// unused and unreachable: clampDifficulty never returns it.
var difficultyMultiplier = [...]float64{1: 1.0, 2: 1.15, 3: 1.3, 4: 1.5, 5: 1.75}

// The tunable constants, all in one block so a balance change is one edit.
const (
	// efficiencyBonusFraction is of base XP, awarded when commands used is
	// at or under par.
	efficiencyBonusFraction = 0.25

	// firstTryBonusFraction is of base XP.
	firstTryBonusFraction = 0.10

	// optionalObjectiveBonus is flat XP per bonus objective satisfied.
	optionalObjectiveBonus = 10

	// The range difficultyMultiplier is indexed over.
	minDifficulty = 1
	maxDifficulty = 5
)

// Score computes the award.
//
// It is pure and deterministic: the same Inputs return the same Result on
// every call, it reads no clock, and it touches nothing outside its own
// arguments. The total is never negative.
func Score(in Inputs) Result {
	base := in.BaseXP
	if base < 0 {
		base = 0
	}

	// The one multiplicative stage, rounded once at its end, before any
	// additive term touches the number.
	scaled := roundHalfAwayFromZero(float64(base) * difficultyMultiplier[clampDifficulty(in.Difficulty)])

	res := Result{
		Total:     base,
		Breakdown: make([]Breakdown, 0, 6),
	}
	res.Breakdown = append(res.Breakdown, Breakdown{Label: LabelBase, Delta: base})

	add := func(label string, delta int) {
		if delta == 0 {
			return
		}
		res.Breakdown = append(res.Breakdown, Breakdown{Label: label, Delta: delta})
		res.Total += delta
	}

	add(LabelDifficulty, scaled-base)

	// Par of zero disables the bonus. It is deliberately NOT read as "the
	// learner used more than par": a level that declines to set a par is
	// declining to measure efficiency at all, which is what the level
	// format says it means.
	if in.ParCommands > 0 && in.CommandsUsed <= in.ParCommands {
		add(LabelEfficiency, roundHalfAwayFromZero(float64(base)*efficiencyBonusFraction))
	}

	if in.FirstTry {
		add(LabelFirstTry, roundHalfAwayFromZero(float64(base)*firstTryBonusFraction))
	}

	if in.OptionalHit > 0 {
		add(LabelBonus, in.OptionalHit*optionalObjectiveBonus)
	}

	// Last, and clamped to what was earned above it, so the total floors at
	// zero without the breakdown having to lie about how it got there.
	add(LabelHints, -hintCost(in.HintCosts, res.Total))

	return res
}

// clampDifficulty brings difficulty into the range difficultyMultiplier is
// indexed over.
//
// Clamping rather than refusing is deliberate. A difficulty of 0 or 9
// reaching this function means the pack validator let it through, which is
// a bug to fix in the validator. Clamping keeps the learner playing;
// refusing would turn a content bug into an unplayable level.
func clampDifficulty(d int) int {
	if d < minDifficulty {
		return minDifficulty
	}
	if d > maxDifficulty {
		return maxDifficulty
	}
	return d
}

// hintCost totals the costs actually taken and clamps them to earned, which
// is everything the breakdown has awarded so far.
//
// A cost at or below zero is skipped rather than added. A level authoring a
// negative hint cost would otherwise turn taking a hint into an award, and
// the pack validator is the place to reject that, not the scorer.
func hintCost(costs []int, earned int) int {
	total := 0
	for _, c := range costs {
		if c <= 0 {
			continue
		}
		total += c
	}
	if total > earned {
		return earned
	}
	return total
}

// roundHalfAwayFromZero rounds to the nearest whole number, with a half
// rounding away from zero. That is what math.Round already does; this
// wrapper exists so the rule is named at every call site rather than
// assumed from the standard library's choice.
func roundHalfAwayFromZero(f float64) int { return int(math.Round(f)) }
