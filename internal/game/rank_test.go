package game

import (
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
)

// rankedPack is the shipped ladder: five ranks from Novice at 0 to Wizard
// at 2600.
func rankedPack() *content.Pack {
	return &content.Pack{
		ID: "core-linux-basics",
		Ranks: []content.Rank{
			{ID: "novice", Title: "Novice", MinXP: 0},
			{ID: "apprentice", Title: "Apprentice", MinXP: 300},
			{ID: "operator", Title: "Operator", MinXP: 800},
			{ID: "sysadmin", Title: "Sysadmin", MinXP: 1600},
			{ID: "wizard", Title: "Wizard", MinXP: 2600},
		},
	}
}

func TestRankForResolvesEveryEdge(t *testing.T) {
	cases := []struct {
		xp                    int
		wantCurrent, wantNext string
	}{
		{xp: 0, wantCurrent: "Novice", wantNext: "Apprentice"},
		{xp: 299, wantCurrent: "Novice", wantNext: "Apprentice"},
		{xp: 300, wantCurrent: "Apprentice", wantNext: "Operator"},
		{xp: 301, wantCurrent: "Apprentice", wantNext: "Operator"},
		{xp: 1600, wantCurrent: "Sysadmin", wantNext: "Wizard"},
		{xp: 2600, wantCurrent: "Wizard", wantNext: ""},
		{xp: 99999, wantCurrent: "Wizard", wantNext: ""},
	}

	for _, tc := range cases {
		current, next, ok := RankFor(rankedPack(), tc.xp)
		if !ok {
			t.Fatalf("xp %d: RankFor reported no ranks on a pack that declares five", tc.xp)
		}
		if current.Title != tc.wantCurrent {
			t.Errorf("xp %d: current = %q, want %q", tc.xp, current.Title, tc.wantCurrent)
		}
		if next.Title != tc.wantNext {
			t.Errorf("xp %d: next = %q, want %q", tc.xp, next.Title, tc.wantNext)
		}
	}
}

func TestRankForOnAPackWithNoRanks(t *testing.T) {
	if _, _, ok := RankFor(&content.Pack{ID: "bare"}, 500); ok {
		t.Error("RankFor reported a rank on a pack that declares none")
	}
	if _, _, ok := RankFor(nil, 500); ok {
		t.Error("RankFor reported a rank for a nil pack")
	}
}

func TestRankForBelowEveryThreshold(t *testing.T) {
	pack := &content.Pack{Ranks: []content.Rank{{ID: "a", Title: "Apprentice", MinXP: 100}}}

	current, next, ok := RankFor(pack, 0)
	if !ok {
		t.Fatal("RankFor reported no ranks")
	}
	if current.Title != "" {
		t.Errorf("current = %q, want no rank yet", current.Title)
	}
	if next.Title != "Apprentice" {
		t.Errorf("next = %q, want the lowest declared rank", next.Title)
	}
}

func TestRankForDoesNotTrustTheDeclaredOrder(t *testing.T) {
	pack := &content.Pack{Ranks: []content.Rank{
		{ID: "wizard", Title: "Wizard", MinXP: 2600},
		{ID: "novice", Title: "Novice", MinXP: 0},
		{ID: "operator", Title: "Operator", MinXP: 800},
	}}

	current, next, _ := RankFor(pack, 900)
	if current.Title != "Operator" || next.Title != "Wizard" {
		t.Errorf("got %q then %q, want Operator then Wizard", current.Title, next.Title)
	}
	if pack.Ranks[0].Title != "Wizard" {
		t.Error("RankFor sorted the caller's own slice; it must sort a copy")
	}
}
