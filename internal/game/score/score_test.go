package score

import (
	"math/rand"
	"testing"
)

// Every expected total in this table is computed by hand, in the comment
// beside it, rather than by re-implementing the formula in the test. A test
// that re-derives the answer the same way the code does asserts only that
// the code is self-consistent, which it always is.
func TestScoreTable(t *testing.T) {
	cases := []struct {
		name string
		in   Inputs
		want int
	}{
		{
			// 40 base, difficulty 3 is 1.3, so 40 * 1.3 = 52.
			name: "plain pass",
			in:   Inputs{BaseXP: 40, Difficulty: 3},
			want: 52,
		},
		{
			// 52, minus a 5 XP and a 10 XP hint, is 37.
			name: "two hints taken",
			in:   Inputs{BaseXP: 40, Difficulty: 3, HintCosts: []int{5, 10}},
			want: 37,
		},
		{
			// 52, plus 25 percent of the 40 base for coming in under par.
			name: "under par",
			in:   Inputs{BaseXP: 40, Difficulty: 3, ParCommands: 6, CommandsUsed: 4},
			want: 62,
		},
		{
			// 52, and nothing for par, because 9 commands is over the 6 par.
			name: "over par",
			in:   Inputs{BaseXP: 40, Difficulty: 3, ParCommands: 6, CommandsUsed: 9},
			want: 52,
		},
		{
			// 52, plus 10 percent of the 40 base for passing first try.
			name: "first try",
			in:   Inputs{BaseXP: 40, Difficulty: 3, FirstTry: true},
			want: 56,
		},
		{
			// 52, plus 10 flat XP for each of two bonus objectives.
			name: "two bonus objectives",
			in:   Inputs{BaseXP: 40, Difficulty: 3, OptionalHit: 2},
			want: 72,
		},
		{
			// 40 base, plus 12 difficulty, plus 10 efficiency, plus 4 first
			// try, plus 20 bonus, is 86. Minus 15 of hints is 71.
			name: "everything at once",
			in: Inputs{
				BaseXP:       40,
				Difficulty:   3,
				HintCosts:    []int{5, 10},
				ParCommands:  6,
				CommandsUsed: 6,
				FirstTry:     true,
				OptionalHit:  2,
			},
			want: 71,
		},
		{
			// 45 * 1.15 is 51.75, which rounds to 52.
			name: "multiplicative stage rounds up",
			in:   Inputs{BaseXP: 45, Difficulty: 2},
			want: 52,
		},
		{
			// 10 * 1.15 is 11.5, which rounds away from zero to 12.
			name: "multiplicative stage rounds a half away from zero",
			in:   Inputs{BaseXP: 10, Difficulty: 2},
			want: 12,
		},
		{
			// 10 base, difficulty 1 adds nothing. 10 * 0.25 is 2.5, which
			// rounds away from zero to 3, so 13.
			name: "efficiency bonus rounds a half away from zero",
			in:   Inputs{BaseXP: 10, Difficulty: 1, ParCommands: 4, CommandsUsed: 4},
			want: 13,
		},
		{
			// 45 base, difficulty 1 adds nothing. 45 * 0.10 is 4.5, which
			// rounds away from zero to 5, so 50.
			name: "first try bonus rounds a half away from zero",
			in:   Inputs{BaseXP: 45, Difficulty: 1, FirstTry: true},
			want: 50,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Score(tc.in)
			if got.Total != tc.want {
				t.Errorf("Score(%+v).Total = %d, want %d\nbreakdown: %+v", tc.in, got.Total, tc.want, got.Breakdown)
			}
			assertBreakdownSums(t, got)
		})
	}
}

// The floor, which is the one property a learner would notice being wrong:
// a level must never end by taking XP away.
func TestScoreFloorsAtZeroAndTheBreakdownStillSums(t *testing.T) {
	// 10 base, difficulty 1, so 10 earned. A 50 XP hint costs more than the
	// whole award.
	got := Score(Inputs{BaseXP: 10, Difficulty: 1, HintCosts: []int{50}})

	if got.Total != 0 {
		t.Errorf("Total = %d, want 0: the award must never go negative", got.Total)
	}
	assertBreakdownSums(t, got)

	var hints int
	for _, b := range got.Breakdown {
		if b.Label == LabelHints {
			hints = b.Delta
		}
	}
	if hints != -10 {
		t.Errorf("hints delta = %d, want -10: the hint line is clamped to what was earned, not to the authored cost", hints)
	}
}

func TestScoreParEdges(t *testing.T) {
	cases := []struct {
		name          string
		par, used     int
		wantEfficient bool
	}{
		{name: "par zero disables the bonus even at zero commands", par: 0, used: 0, wantEfficient: false},
		{name: "exactly at par earns the bonus", par: 5, used: 5, wantEfficient: true},
		{name: "under par earns the bonus", par: 5, used: 1, wantEfficient: true},
		{name: "one over par does not", par: 5, used: 6, wantEfficient: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Score(Inputs{BaseXP: 40, Difficulty: 1, ParCommands: tc.par, CommandsUsed: tc.used})
			gotEfficient := hasLabel(got, LabelEfficiency)
			if gotEfficient != tc.wantEfficient {
				t.Errorf("efficiency bonus awarded = %t, want %t (breakdown %+v)", gotEfficient, tc.wantEfficient, got.Breakdown)
			}
		})
	}
}

func TestScoreClampsDifficultyRatherThanRefusingIt(t *testing.T) {
	cases := []struct {
		difficulty int
		want       int // 100 base, so the total is the multiplier times 100
	}{
		{difficulty: -3, want: 100}, // clamped up to 1
		{difficulty: 0, want: 100},  // clamped up to 1
		{difficulty: 1, want: 100},
		{difficulty: 5, want: 175},
		{difficulty: 9, want: 175}, // clamped down to 5
	}

	for _, tc := range cases {
		got := Score(Inputs{BaseXP: 100, Difficulty: tc.difficulty})
		if got.Total != tc.want {
			t.Errorf("difficulty %d: Total = %d, want %d", tc.difficulty, got.Total, tc.want)
		}
	}
}

func TestScoreTreatsANegativeBaseAsZero(t *testing.T) {
	got := Score(Inputs{BaseXP: -50, Difficulty: 5})
	if got.Total != 0 {
		t.Errorf("Total = %d, want 0", got.Total)
	}
	assertBreakdownSums(t, got)
}

// A level authoring a negative hint cost must not turn taking a hint into
// an award. Refusing it belongs in the pack validator; ignoring it belongs
// here.
func TestScoreIgnoresANegativeHintCost(t *testing.T) {
	got := Score(Inputs{BaseXP: 40, Difficulty: 1, HintCosts: []int{-100}})
	if got.Total != 40 {
		t.Errorf("Total = %d, want 40: a negative hint cost must award nothing", got.Total)
	}
}

func TestScoreIsDeterministic(t *testing.T) {
	in := Inputs{
		BaseXP:       40,
		Difficulty:   3,
		HintCosts:    []int{5, 10},
		ParCommands:  6,
		CommandsUsed: 6,
		FirstTry:     true,
		OptionalHit:  2,
	}

	first := Score(in)
	for i := 0; i < 1000; i++ {
		got := Score(in)
		if got.Total != first.Total || len(got.Breakdown) != len(first.Breakdown) {
			t.Fatalf("run %d: Score is not deterministic: got %+v, first %+v", i, got, first)
		}
		for j := range got.Breakdown {
			if got.Breakdown[j] != first.Breakdown[j] {
				t.Fatalf("run %d: breakdown line %d drifted: got %+v, first %+v", i, j, got.Breakdown[j], first.Breakdown[j])
			}
		}
	}
}

// The two invariants that hold for every input there is, asserted over
// random ones rather than only over the table above.
func TestScorePropertiesOverRandomInputs(t *testing.T) {
	r := rand.New(rand.NewSource(1))

	for i := 0; i < 20000; i++ {
		hints := make([]int, r.Intn(6))
		for j := range hints {
			hints[j] = r.Intn(60) - 10 // deliberately includes negatives
		}
		in := Inputs{
			BaseXP:       r.Intn(400) - 50,
			Difficulty:   r.Intn(9) - 2,
			HintCosts:    hints,
			ParCommands:  r.Intn(20),
			CommandsUsed: r.Intn(40),
			FirstTry:     r.Intn(2) == 0,
			OptionalHit:  r.Intn(6) - 2,
		}

		got := Score(in)
		if got.Total < 0 {
			t.Fatalf("Score(%+v).Total = %d, want it never to go below zero", in, got.Total)
		}
		assertBreakdownSums(t, got)
	}
}

// Purity, measured rather than asserted in prose: Score does no I/O and its
// allocation count does not grow with its input, so the same call always
// costs the same.
func TestScoreAllocatesAConstantAmount(t *testing.T) {
	short := Inputs{BaseXP: 40, Difficulty: 3, HintCosts: []int{5}}
	long := Inputs{BaseXP: 40, Difficulty: 3, HintCosts: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}}

	shortAllocs := testing.AllocsPerRun(100, func() { _ = Score(short) })
	longAllocs := testing.AllocsPerRun(100, func() { _ = Score(long) })

	if shortAllocs != longAllocs {
		t.Errorf("allocations grew with the input: %v for one hint, %v for ten", shortAllocs, longAllocs)
	}
	if shortAllocs > 2 {
		t.Errorf("allocations per call = %v, want at most 2 (the breakdown slice, and its growth)", shortAllocs)
	}
}

// The base line is always present, even when it is zero, because a
// breakdown that starts somewhere other than the level's own XP reads as
// though a line went missing.
func TestBreakdownAlwaysOpensWithBase(t *testing.T) {
	for _, in := range []Inputs{
		{},
		{BaseXP: 0, Difficulty: 5, FirstTry: true},
		{BaseXP: 40, Difficulty: 3},
	} {
		got := Score(in)
		if len(got.Breakdown) == 0 || got.Breakdown[0].Label != LabelBase {
			t.Errorf("Score(%+v).Breakdown = %+v, want it to open with %q", in, got.Breakdown, LabelBase)
		}
	}
}

// A zero delta earns no line: a "difficulty +0" row on a difficulty 1 level
// is noise, not information.
func TestBreakdownOmitsZeroDeltas(t *testing.T) {
	got := Score(Inputs{BaseXP: 40, Difficulty: 1})
	if len(got.Breakdown) != 1 {
		t.Errorf("Breakdown = %+v, want just the base line", got.Breakdown)
	}
}

func assertBreakdownSums(t *testing.T, res Result) {
	t.Helper()
	sum := 0
	for _, b := range res.Breakdown {
		sum += b.Delta
	}
	if sum != res.Total {
		t.Errorf("breakdown sums to %d but Total is %d: %+v", sum, res.Total, res.Breakdown)
	}
}

func hasLabel(res Result, label string) bool {
	for _, b := range res.Breakdown {
		if b.Label == label {
			return true
		}
	}
	return false
}
