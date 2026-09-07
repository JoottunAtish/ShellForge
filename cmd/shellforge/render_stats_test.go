package main

import (
	"strings"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// statsPack is two acts and three levels, with the shipped rank ladder, so
// every block of the report has something to show.
func statsPack() *content.Pack {
	return &content.Pack{
		ID:   "core-linux-basics",
		Name: "Linux Basics",
		Acts: []content.Act{
			{ID: "act1", Title: "Orientation", Levels: []string{"nav-01", "nav-02"}},
			{ID: "act2", Title: "Files", Levels: []string{"files-01"}},
		},
		Levels: []content.Level{
			{ID: "nav-01", Act: "act1", Title: "First Contact", XP: 40},
			{ID: "nav-02", Act: "act1", Title: "Getting Around", XP: 50, Prerequisites: []string{"nav-01"}},
			{ID: "files-01", Act: "act2", Title: "Making Things", XP: 60, Prerequisites: []string{"nav-02"}},
		},
		Ranks: []content.Rank{
			{ID: "novice", Title: "Novice", MinXP: 0},
			{ID: "apprentice", Title: "Apprentice", MinXP: 300},
		},
	}
}

func statsNodes(t *testing.T, states map[string]store.LevelState) []game.Node {
	t.Helper()
	nodes, err := game.Resolve(statsPack(), states)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return nodes
}

func TestStatsOnAnEmptyProfile(t *testing.T) {
	got := renderStats(statsPack(), statsNodes(t, nil), 0, nil, false)

	for _, want := range []string{"Total XP: 0", "Rank: Novice", "Next rank: Apprentice, 300 XP away", "Progress", "Achievements"} {
		if !strings.Contains(got, want) {
			t.Errorf("stats does not contain %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "0/2") {
		t.Errorf("stats does not show act 1 as untouched:\n%s", got)
	}
}

func TestStatsMidCampaign(t *testing.T) {
	states := map[string]store.LevelState{
		"nav-01": {LevelID: "nav-01", Status: store.StatusPassed, BestScore: 52},
		"nav-02": {LevelID: "nav-02", Status: store.StatusPassed, BestScore: 61},
	}
	unlocked := map[string]store.Achievement{
		"first_blood": {Key: "first_blood", UnlockedAt: fixedTime()},
	}

	got := renderStats(statsPack(), statsNodes(t, states), 113, unlocked, false)

	if !strings.Contains(got, "Total XP: 113") {
		t.Errorf("stats does not show the total:\n%s", got)
	}
	if !strings.Contains(got, "2/2") {
		t.Errorf("stats does not show act 1 as cleared:\n%s", got)
	}
	if !strings.Contains(got, "0/1") {
		t.Errorf("stats does not show act 2 as untouched:\n%s", got)
	}
	if !strings.Contains(got, "113 XP") {
		t.Errorf("stats does not bank act 1's XP:\n%s", got)
	}
}

// Locked achievements are shown rather than hidden: a locked list is a
// preview of what the game rewards.
func TestStatsShowsLockedAchievementsRatherThanHidingThem(t *testing.T) {
	got := renderStats(statsPack(), statsNodes(t, nil), 0, nil, false)

	if !strings.Contains(got, "First Blood") {
		t.Errorf("stats hides an achievement nobody has earned yet:\n%s", got)
	}
	if !strings.Contains(got, "Pass your first level.") {
		t.Errorf("stats hides a locked achievement's description:\n%s", got)
	}
	if !strings.Contains(got, mapMarkLocked) {
		t.Errorf("stats does not mark locked achievements as locked:\n%s", got)
	}
}

func TestStatsCountsEarnedAchievements(t *testing.T) {
	unlocked := map[string]store.Achievement{
		"first_blood": {Key: "first_blood", UnlockedAt: fixedTime()},
		"night_owl":   {Key: "night_owl", UnlockedAt: fixedTime()},
	}
	got := renderStats(statsPack(), statsNodes(t, nil), 0, unlocked, false)

	if !strings.Contains(got, "Achievements (2 of ") {
		t.Errorf("stats does not count what has been earned:\n%s", got)
	}
	if !strings.Contains(got, mapMarkPassed) {
		t.Errorf("stats does not mark earned achievements:\n%s", got)
	}
}

func TestStatsAtTheTopOfTheLadder(t *testing.T) {
	got := renderStats(statsPack(), statsNodes(t, nil), 5000, nil, false)
	if !strings.Contains(got, "top of the ladder") {
		t.Errorf("stats does not say the learner is at the top:\n%s", got)
	}
}

func TestStatsOnAPackWithNoRanks(t *testing.T) {
	pack := statsPack()
	pack.Ranks = nil

	got := renderStats(pack, statsNodes(t, nil), 42, nil, false)
	if !strings.Contains(got, "Total XP: 42") {
		t.Errorf("stats does not show XP on a pack with no ranks:\n%s", got)
	}
	if strings.Contains(got, "Rank:") {
		t.Errorf("stats invented a rank on a pack that declares none:\n%s", got)
	}
}

func TestStatsWithNoColourHasNoEscapeSequence(t *testing.T) {
	unlocked := map[string]store.Achievement{"first_blood": {Key: "first_blood", UnlockedAt: fixedTime()}}
	got := renderStats(statsPack(), statsNodes(t, nil), 113, unlocked, false)

	if strings.Contains(got, "\x1b[") {
		t.Errorf("stats contains an escape sequence with colour off:\n%q", got)
	}
	for _, r := range got {
		if r > 127 {
			t.Errorf("stats contains a non-ASCII rune %q with colour off", r)
		}
	}
}

func TestStatsWithColourWrapsTheSameText(t *testing.T) {
	plain := renderStats(statsPack(), statsNodes(t, nil), 113, nil, false)
	coloured := renderStats(statsPack(), statsNodes(t, nil), 113, nil, true)

	if !strings.Contains(coloured, "\x1b[") {
		t.Error("stats has no escape sequence with colour on")
	}
	if stripANSI(coloured) != plain {
		t.Error("turning colour on changed the text, not only its styling")
	}
}

// stats runs on the host, so its lines end in a plain newline. crlf must
// never be applied to it, which is the drift render_check.go documents.
func TestStatsIsNotCRLFTerminated(t *testing.T) {
	got := renderStats(statsPack(), statsNodes(t, nil), 0, nil, false)
	if strings.Contains(got, "\r") {
		t.Errorf("stats carries carriage returns it must not:\n%q", got)
	}
}

// A narrow terminal must not produce nonsense or a panic. The report is
// fixed-width columns, so this asserts no line is absurdly long rather than
// that it fits: wrapping is the terminal's job.
func TestStatsSurvivesANarrowTerminal(t *testing.T) {
	got := renderStats(statsPack(), statsNodes(t, nil), 113, nil, false)
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 78 {
			t.Errorf("line is %d columns wide, which wraps badly on a narrow terminal: %q", len(line), line)
		}
	}
}

// fixedTime is any non-zero time: a store.Achievement counts as unlocked
// when UnlockedAt is not the zero value, and which instant it is does not
// matter to the renderer.
func fixedTime() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }
