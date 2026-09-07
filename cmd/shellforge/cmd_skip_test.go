package main

import (
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

func TestSkipResolvesTheNamedLevel(t *testing.T) {
	node, level, err := levelToSkip(playPack(), playNodes(t, nil), "nav-02")
	if err != nil {
		t.Fatalf("levelToSkip: %v", err)
	}
	if level.ID != "nav-02" || node.LevelID != "nav-02" {
		t.Errorf("resolved %q, want nav-02", level.ID)
	}
}

// A bare `skip` skips the level `play` would have chosen, which is the one
// a learner staring at something they cannot finish is actually looking at.
func TestSkipWithNoIdSkipsTheLevelPlayWouldChoose(t *testing.T) {
	states := map[string]store.LevelState{
		"nav-01": {LevelID: "nav-01", Status: store.StatusPassed},
	}

	_, level, err := levelToSkip(playPack(), playNodes(t, states), "")
	if err != nil {
		t.Fatalf("levelToSkip: %v", err)
	}
	if level.ID != "nav-02" {
		t.Errorf("resolved %q, want the level play would have chosen", level.ID)
	}
}

func TestSkipRefusesAnUnknownId(t *testing.T) {
	_, _, err := levelToSkip(playPack(), playNodes(t, nil), "nav-99")
	if err == nil {
		t.Fatal("levelToSkip accepted an unknown level id")
	}
	assertUserFacing(t, err)
}

func TestSkipWithNothingLeftToPlaySaysSo(t *testing.T) {
	states := map[string]store.LevelState{
		"nav-01":   {LevelID: "nav-01", Status: store.StatusPassed},
		"nav-02":   {LevelID: "nav-02", Status: store.StatusPassed},
		"files-01": {LevelID: "files-01", Status: store.StatusPassed},
	}

	_, _, err := levelToSkip(playPack(), playNodes(t, states), "")
	if err == nil {
		t.Fatal("levelToSkip found something to skip on a complete campaign")
	}
	assertUserFacing(t, err)
	if !strings.Contains(remediationOf(t, err), "nothing to skip") {
		t.Errorf("the refusal does not explain itself: %s", remediationOf(t, err))
	}
}

// A skipped level unlocks its dependants, which is the only reason skipping
// exists. game.Resolve owns the rule; this pins that skip writes the status
// the rule reads.
func TestASkippedLevelUnlocksWhatCameAfterIt(t *testing.T) {
	before := playNodes(t, nil)
	if availabilityOf(before, "nav-02") != game.AvailableLocked {
		t.Fatalf("nav-02 is not locked to begin with, so this test proves nothing")
	}

	after := playNodes(t, map[string]store.LevelState{
		"nav-01": {LevelID: "nav-01", Status: store.StatusSkipped},
	})
	if got := availabilityOf(after, "nav-02"); got != game.AvailableNow {
		t.Errorf("nav-02 is %q after nav-01 was skipped, want %q", got, game.AvailableNow)
	}
}

func availabilityOf(nodes []game.Node, levelID string) game.Availability {
	for _, n := range nodes {
		if n.LevelID == levelID {
			return n.Availability
		}
	}
	return ""
}

// skip writes a status and deletes nothing. Asserted against the source
// rather than against behaviour, because the absence of a deletion is not
// something a behavioural test can observe.
func TestSkipDeletesNothing(t *testing.T) {
	src := readSource(t, "cmd_skip.go")
	for _, forbidden := range []string{"RemoveAll", "Remove(", "Teardown", "rm -rf"} {
		if strings.Contains(src, forbidden) {
			t.Errorf("cmd_skip.go mentions %q; skipping a level must write a status and nothing else", forbidden)
		}
	}
}
