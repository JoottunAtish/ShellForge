package game

import (
	"sort"

	"github.com/JoottunAtish/ShellForge/internal/content"
)

// Rank resolution, which lives here rather than in the renderer because it
// is a question about the pack: pack.yaml declares the ladder, and turning
// an XP total into a title is the same kind of join Resolve already does
// for levels.

// RankFor returns the rank xp has earned and the one above it.
//
// current is the highest rank whose MinXP is at or below xp. next is the
// rank above that, and is the zero content.Rank when there is none, which
// is what being at the top of the ladder looks like. ok is false when the
// pack declares no ranks at all, in which case a caller should show XP
// without a title rather than invent one.
//
// A pack whose lowest rank starts above zero leaves a learner below every
// threshold. That is not an error: current is the zero content.Rank and
// next is the lowest rank declared, so the display reads as "no rank yet,
// this many XP to the first one" rather than claiming a title nobody
// earned.
//
// The declared order is not trusted. Ranks are sorted by MinXP on a copy,
// so a pack that lists them out of order still resolves correctly and the
// caller's own slice is left alone.
func RankFor(pack *content.Pack, xp int) (current, next content.Rank, ok bool) {
	if pack == nil || len(pack.Ranks) == 0 {
		return content.Rank{}, content.Rank{}, false
	}

	ranks := make([]content.Rank, len(pack.Ranks))
	copy(ranks, pack.Ranks)
	sort.SliceStable(ranks, func(i, j int) bool { return ranks[i].MinXP < ranks[j].MinXP })

	idx := -1
	for i, r := range ranks {
		if xp >= r.MinXP {
			idx = i
			continue
		}
		break
	}

	if idx < 0 {
		return content.Rank{}, ranks[0], true
	}
	if idx+1 < len(ranks) {
		return ranks[idx], ranks[idx+1], true
	}
	return ranks[idx], content.Rank{}, true
}
