package main

import (
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game/achievements"
	"github.com/JoottunAtish/ShellForge/internal/game/score"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// bannerLevel and bannerResult are the fixture the banner cases are built
// from: one required objective passed, one bonus missed.
func bannerLevel() *content.Level {
	return &content.Level{ID: "pipe-05", Title: "Billing service", XP: 40, Difficulty: 3, ParCommands: 6}
}

func bannerResult() verify.LevelResult {
	return verify.LevelResult{
		LevelID: "pipe-05",
		Passed:  true,
		Objectives: []verify.ObjectiveResult{
			{ID: "count", Text: "report.txt holds the total ERROR count", Status: verify.StatusPass},
			{ID: "oneliner", Text: "you did it in one pipeline", Status: verify.StatusFail, Optional: true},
		},
	}
}

func novice() content.Rank { return content.Rank{ID: "novice", Title: "Novice", MinXP: 0} }
func apprentice() content.Rank {
	return content.Rank{ID: "apprentice", Title: "Apprentice", MinXP: 300}
}

func plainPassData() passSummaryData {
	return passSummaryData{
		Level:      bannerLevel(),
		Result:     bannerResult(),
		Score:      score.Score(score.Inputs{BaseXP: 40, Difficulty: 3}),
		TotalXP:    52,
		RankBefore: novice(),
		RankAfter:  novice(),
		NextRank:   apprentice(),
		HasRanks:   true,
	}
}

func TestPassBannerShowsTheArithmetic(t *testing.T) {
	got := renderPassBanner(plainPassData(), false)

	for _, want := range []string{"base", "difficulty", "XP earned", "PASS:"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner does not mention %q:\n%s", want, got)
		}
	}
	// 40 base at difficulty 3 is 52: both numbers have to be visible, or
	// the learner is being asked to trust the total.
	if !strings.Contains(got, "40") || !strings.Contains(got, "52") {
		t.Errorf("banner hides the arithmetic:\n%s", got)
	}
}

func TestPassBannerShowsEveryBreakdownLine(t *testing.T) {
	d := plainPassData()
	d.Score = score.Score(score.Inputs{
		BaseXP: 40, Difficulty: 3, HintCosts: []int{5},
		ParCommands: 6, CommandsUsed: 4, FirstTry: true, OptionalHit: 1,
	})

	got := renderPassBanner(d, false)
	for _, label := range []string{
		score.LabelBase, score.LabelDifficulty, score.LabelEfficiency,
		score.LabelFirstTry, score.LabelBonus, score.LabelHints,
	} {
		if !strings.Contains(got, label) {
			t.Errorf("banner omits the %q line:\n%s", label, got)
		}
	}
}

func TestPassBannerCountsBonusObjectives(t *testing.T) {
	got := renderPassBanner(plainPassData(), false)
	if !strings.Contains(got, "bonus objective") {
		t.Errorf("banner does not say how many bonus objectives were earned:\n%s", got)
	}
	if !strings.Contains(got, "(bonus)") {
		t.Errorf("banner does not mark the bonus objective in the checklist:\n%s", got)
	}
}

func TestPassBannerNamesEveryAchievementUnlocked(t *testing.T) {
	d := plainPassData()
	d.Unlocked = []achievements.Rule{
		{Key: "first_blood", Title: "First Blood", Description: "Pass your first level."},
		{Key: "night_owl", Title: "Night Owl", Description: "Pass a level between 02:00 and 05:00."},
	}

	got := renderPassBanner(d, false)
	for _, want := range []string{"First Blood", "Pass your first level.", "Night Owl", "between 02:00 and 05:00"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner omits %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "Achievement unlocked:"); n != 2 {
		t.Errorf("banner announced %d achievements, want 2:\n%s", n, got)
	}
}

func TestPassBannerAnnouncesARankUp(t *testing.T) {
	d := plainPassData()
	d.TotalXP = 320
	d.RankBefore = novice()
	d.RankAfter = apprentice()
	d.NextRank = content.Rank{ID: "operator", Title: "Operator", MinXP: 800}

	got := renderPassBanner(d, false)
	if !strings.Contains(got, "Rank up") {
		t.Errorf("banner does not announce the rank up:\n%s", got)
	}
	if !strings.Contains(got, "Novice") || !strings.Contains(got, "Apprentice") {
		t.Errorf("the rank up does not name both ranks:\n%s", got)
	}
}

func TestPassBannerShowsTheDistanceToTheNextRankWhenThereIsNoRankUp(t *testing.T) {
	got := renderPassBanner(plainPassData(), false)
	// 300 minus 52.
	if !strings.Contains(got, "248 XP to Apprentice") {
		t.Errorf("banner does not say how far the next rank is:\n%s", got)
	}
	if strings.Contains(got, "Rank up") {
		t.Errorf("banner announced a rank up that did not happen:\n%s", got)
	}
}

func TestPassBannerAtTheTopOfTheLadder(t *testing.T) {
	d := plainPassData()
	d.TotalXP = 3000
	d.RankBefore = content.Rank{ID: "wizard", Title: "Wizard", MinXP: 2600}
	d.RankAfter = d.RankBefore
	d.NextRank = content.Rank{}

	got := renderPassBanner(d, false)
	if !strings.Contains(got, "top of the ladder") {
		t.Errorf("banner does not say the learner is at the top:\n%s", got)
	}
}

func TestPassBannerOnAPackWithNoRanks(t *testing.T) {
	d := plainPassData()
	d.HasRanks = false
	d.RankBefore = content.Rank{}
	d.RankAfter = content.Rank{}
	d.NextRank = content.Rank{}

	got := renderPassBanner(d, false)
	if !strings.Contains(got, "Total XP 52.") {
		t.Errorf("banner does not show XP for a pack with no ranks:\n%s", got)
	}
	if strings.Contains(got, "Rank") {
		t.Errorf("banner invented a rank on a pack that declares none:\n%s", got)
	}
}

func TestPassBannerWithNoColourHasNoEscapeSequence(t *testing.T) {
	d := plainPassData()
	d.TotalXP = 320
	d.RankAfter = apprentice()
	d.Unlocked = []achievements.Rule{{Key: "k", Title: "T", Description: "D"}}

	got := renderPassBanner(d, false)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("banner contains an escape sequence with colour off:\n%q", got)
	}
	for _, r := range got {
		if r > 127 {
			t.Errorf("banner contains a non-ASCII rune %q with colour off:\n%q", r, got)
		}
	}
}

func TestPassBannerWithColourWrapsTheSameText(t *testing.T) {
	plain := renderPassBanner(plainPassData(), false)
	coloured := renderPassBanner(plainPassData(), true)

	if !strings.Contains(coloured, "\x1b[") {
		t.Error("banner has no escape sequence with colour on")
	}
	if stripANSI(coloured) != plain {
		t.Errorf("turning colour on changed the text, not only its styling:\n%q\nvs\n%q", stripANSI(coloured), plain)
	}
}

// The CRLF rule, asserted in one test so the two contexts cannot drift.
// The banner is built with plain newlines; the sandbox path applies crlf
// and the host path does not.
func TestTheBannerIsCRLFTerminatedOnlyOnTheSandboxPath(t *testing.T) {
	host := renderPassBanner(plainPassData(), false)
	sandbox := crlf(host)

	if strings.Contains(host, "\r") {
		t.Errorf("the host banner carries carriage returns it must not:\n%q", host)
	}
	for i, line := range strings.Split(strings.TrimSuffix(sandbox, "\n"), "\n") {
		if line != "" && !strings.HasSuffix(line, "\r") {
			t.Errorf("sandbox banner line %d does not end in a carriage return: %q", i, line)
		}
	}
}

// --------------------------------------------------------------------------
// What to do once the level is over
// --------------------------------------------------------------------------

// TestRenderNextStepAlwaysSaysExitBeforeShellforge is the regression test for
// the fourth defect a learner hit: the banner ended on their XP total and
// said nothing about how to reach the next level. They tried `next`, then
// `shellforge next`, and got "command not found" from both, because
// shellforge runs on their own computer and the sandbox cannot reach it.
//
// Whatever the wording, `exit` has to come before `shellforge` in it, or the
// advice earns them the same error again.
func TestRenderNextStepAlwaysSaysExitBeforeShellforge(t *testing.T) {
	cases := []struct {
		name string
		next nextStep
	}{
		{"a named next level", nextStep{LevelID: "nav-03", Title: "Hidden in Plain Sight"}},
		{"the lookup failed", nextStep{}},
		{"the campaign is finished", nextStep{Complete: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderNextStep(tc.next)

			if !strings.Contains(got, "exit") {
				t.Fatalf("the learner is not told to leave the sandbox: %q", got)
			}
			if !strings.Contains(got, "shellforge ") {
				t.Fatalf("the learner is not told what to run: %q", got)
			}
			if strings.Index(got, "exit") > strings.Index(got, "shellforge ") {
				t.Errorf("`shellforge` is named before `exit`, so following this earns a command-not-found: %q", got)
			}
		})
	}
}

func TestRenderNextStepNamesTheLevel(t *testing.T) {
	got := renderNextStep(nextStep{LevelID: "nav-03", Title: "Hidden in Plain Sight"})
	for _, want := range []string{"nav-03", "Hidden in Plain Sight", "shellforge play"} {
		if !strings.Contains(got, want) {
			t.Errorf("the next step does not mention %q: %q", want, got)
		}
	}
}

// TestRenderNextStepOnAFinishedCampaign pins the ending. There is no next
// level, and saying "run `shellforge play`" to somebody who has passed
// everything would send them to a message telling them the same thing again.
func TestRenderNextStepOnAFinishedCampaign(t *testing.T) {
	got := renderNextStep(nextStep{Complete: true})
	if strings.Contains(got, "shellforge play") {
		t.Errorf("a finished campaign is pointed back at `play`: %q", got)
	}
	if !strings.Contains(got, "shellforge stats") {
		t.Errorf("a finished campaign is not offered `stats`: %q", got)
	}
}

// TestPassBannerEndsWithTheNextStep pins that the block reaches the banner at
// all, and that it is last: it is the one thing the learner has to act on,
// and a line above the score arithmetic is a line they scroll past.
func TestPassBannerEndsWithTheNextStep(t *testing.T) {
	got := renderPassBanner(passSummaryData{
		Level:  &content.Level{ID: "nav-02", Title: "Taking Stock"},
		Result: verify.LevelResult{Passed: true},
		Score:  score.Result{Total: 55},
		Next:   nextStep{LevelID: "nav-03", Title: "Hidden in Plain Sight"},
	}, false)

	if !strings.Contains(got, "Next up: nav-03") {
		t.Fatalf("the banner does not say what comes next: %q", got)
	}
	if strings.Index(got, "Next up: nav-03") < strings.Index(got, "XP earned") {
		t.Errorf("the next step printed above the score arithmetic: %q", got)
	}
}

// TestRenderNextStepMatchesWhatActuallyHappensNext pins the wording split
// against the behaviour it describes. Under `play`, the learner is asked
// whether to carry on the moment the shell exits, so telling them to run
// `shellforge play` sends them to do by hand something that is about to
// happen anyway. Under `run` and `play <level-id>` nobody is going to ask,
// so promising a question leaves them waiting for one. Either way the
// instruction has to match what their screen then does.
func TestRenderNextStepMatchesWhatActuallyHappensNext(t *testing.T) {
	named := nextStep{LevelID: "nav-03", Title: "Hidden in Plain Sight"}

	t.Run("play will ask", func(t *testing.T) {
		offered := named
		offered.Offered = true
		got := renderNextStep(offered)

		if !strings.Contains(got, "asked") {
			t.Errorf("the learner is not told they will be asked: %q", got)
		}
		if strings.Contains(got, "shellforge play") {
			t.Errorf("the learner is told to run `shellforge play` when they are about to be asked instead: %q", got)
		}
		if !strings.Contains(got, "nav-03") {
			t.Errorf("the next level is not named: %q", got)
		}
	})

	t.Run("run will not ask", func(t *testing.T) {
		got := renderNextStep(named)

		if strings.Contains(got, "asked") {
			t.Errorf("the learner is promised a question nobody will ask: %q", got)
		}
		if !strings.Contains(got, "shellforge play") {
			t.Errorf("the learner is not told what to run: %q", got)
		}
	})

	t.Run("the wording still names exit first either way", func(t *testing.T) {
		for _, n := range []nextStep{named, {LevelID: "nav-03", Title: "x", Offered: true}, {Offered: true}, {}} {
			got := renderNextStep(n)
			if !strings.Contains(got, "exit") {
				t.Errorf("nextStep %+v does not tell the learner to leave the sandbox: %q", n, got)
			}
			if i := strings.Index(got, "shellforge "); i >= 0 && strings.Index(got, "exit") > i {
				t.Errorf("nextStep %+v names `shellforge` before `exit`: %q", n, got)
			}
		}
	})
}
