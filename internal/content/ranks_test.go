package content

import "testing"

// TestEmbeddedPackTopRankIsReachable is the gate the Day 5 content sprint did
// not have.
//
// The pack shipped with Wizard at 2600 XP over 25 levels awarding 2580, so a
// learner who passed every level on the first attempt and never took a hint
// finished 20 XP short of the last rank in the game. It was caught by someone
// adding the numbers up by hand, which is not a gate.
//
// The arithmetic moves whenever a level is added, removed, or retuned, and
// the failure is silent: nothing errors, the game simply ends one rank early
// for the best possible run. The validator warns about this for any pack; the
// shipped pack is held to it as a test, because a warning nobody reads is how
// it shipped the first time.
func TestEmbeddedPackTopRankIsReachable(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	if len(pack.Ranks) == 0 {
		t.Fatal("the embedded pack declares no ranks: the ladder is the only progression a learner sees before the score formula lands")
	}

	total := 0
	for i := range pack.Levels {
		total += pack.Levels[i].XP
	}

	top := pack.Ranks[len(pack.Ranks)-1]
	if top.MinXP > total {
		t.Errorf("the top rank %q needs %d XP and all %d levels together award %d: a flawless hint-free run falls %d short. Lower the rank or raise a level's xp, and do not tune it to exactly %d, because hint costs subtract.",
			top.ID, top.MinXP, len(pack.Levels), total, top.MinXP-total, total)
	}
}

// TestEmbeddedPackRankLadderAscendsFromZero pins the two structural
// properties the ladder needs to award anything at all: it starts where a new
// learner starts, and it only ever goes up. A ladder that repeats or falls
// awards the later rank never, or the earlier one twice.
func TestEmbeddedPackRankLadderAscendsFromZero(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	if len(pack.Ranks) == 0 {
		t.Fatal("the embedded pack declares no ranks")
	}

	if got := pack.Ranks[0].MinXP; got != 0 {
		t.Errorf("first rank %q starts at %d XP, want 0: a learner who has passed nothing still holds a rank", pack.Ranks[0].ID, got)
	}
	for i := 1; i < len(pack.Ranks); i++ {
		prev, cur := pack.Ranks[i-1], pack.Ranks[i]
		if cur.MinXP <= prev.MinXP {
			t.Errorf("rank %q needs %d XP, which is not more than %q at %d: the ladder must ascend", cur.ID, cur.MinXP, prev.ID, prev.MinXP)
		}
	}
}
