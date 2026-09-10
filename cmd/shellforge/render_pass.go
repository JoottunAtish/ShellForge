package main

import (
	"fmt"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game/achievements"
	"github.com/JoottunAtish/ShellForge/internal/game/score"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// The pass banner: what a learner reads the moment a level is done.
//
// It extends render_check.go rather than starting a third style. The
// palette, the marks, plural and crlf all come from there, so a check reply
// and the banner that follows it do not look like they came from different
// programs.
//
// Two output contexts, one renderer. This banner appears in the learner's
// raw mode terminal, arriving back through the control channel, and on the
// host after `run` or `play` returns. The string is built with plain "\n"
// and crlf is applied ONLY on the sandbox path, which is the mistake
// render_check.go documents having already made once.
//
// It shows the arithmetic. score.Result carries a Breakdown precisely so
// the banner can say base 40, difficulty plus 12, hints minus 5, rather
// than asserting 61 and leaving the learner to trust it. Somebody who
// cannot see why they got what they got learns nothing from the number.

// scoreColumn is where the numbers line up in the breakdown block. Wide
// enough for the longest label ("bonus objectives") plus a gap.
const scoreColumn = 20

// passSummaryData is everything the banner shows, as one value rather than
// eight parameters.
type passSummaryData struct {
	// Level is the level just passed.
	Level *content.Level

	// Result is what the checks decided, for the objective checklist.
	Result verify.LevelResult

	// Score is the award and the arithmetic behind it.
	Score score.Result

	// TotalXP is the learner's new total across the pack.
	TotalXP int

	// RankBefore and RankAfter are the rank either side of this award. A
	// rank up is RankAfter differing from RankBefore.
	RankBefore content.Rank
	RankAfter  content.Rank

	// NextRank is the rank above RankAfter, or the zero value at the top of
	// the ladder.
	NextRank content.Rank

	// HasRanks is false for a pack that declares no rank ladder, in which
	// case the banner shows XP and no title rather than inventing one.
	HasRanks bool

	// Unlocked are the achievements earned during this level, as their
	// rules, which is where the title and description a learner reads live.
	//
	// Issue #128 names this []achievements.Awarded. No such type exists:
	// the registry announces a key and the rule holds the words, so the
	// rule is what the banner needs.
	Unlocked []achievements.Rule

	// Next is where the learner goes from here. See nextStep.
	Next nextStep
}

// nextStep is what a learner is told to do once the level is over.
//
// It exists because without it they are finished and stranded. `play` plays
// one level and returns to the host shell rather than provisioning the next
// one, which is a deliberate decision recorded in cmd_play.go, and the
// learner reading the banner is still sitting at a prompt inside the
// sandbox, where `shellforge` is not a command and never will be: it is a
// program on their own computer, and the sandbox deliberately cannot reach
// anything on the host. Both guesses a real learner made, `next` and
// `shellforge next`, ended in "command not found" with nothing to go on.
type nextStep struct {
	// LevelID and Title name the level that comes next. Both empty when the
	// lookup failed, in which case the banner still says how to carry on,
	// just without naming where to.
	LevelID string
	Title   string

	// Complete is true when there is no next level because the campaign is
	// finished, which is an ending rather than a gap.
	Complete bool

	// Offered is true when `play` is driving and will ask, once this shell
	// exits, whether to start the next level.
	//
	// It changes the wording and nothing else, but getting it wrong would be
	// worse than saying nothing at all: telling a learner to run `shellforge
	// play` when they are about to be asked instead, or telling them they
	// will be asked when nobody is going to, are both instructions that do
	// not match what their screen then does. False under `run`, under `play
	// <level-id>`, and whenever the host's own stdin is not a terminal. See
	// canAsk.
	Offered bool
}

// renderPassBanner builds the banner. The returned string uses plain "\n";
// a caller writing into the learner's raw mode terminal applies crlf.
func renderPassBanner(d passSummaryData, color bool) string {
	p := palette(color)
	var b strings.Builder

	b.WriteString("\n")
	for _, obj := range d.Result.Objectives {
		b.WriteString("  ")
		b.WriteString(p.mark(obj))
		b.WriteString(" ")
		b.WriteString(objectiveLine(obj))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(p.good(fmt.Sprintf("PASS: %s", passSummary(d.Result))))
	b.WriteString("\n\n")

	b.WriteString(renderScoreBreakdown(d.Score, p))

	for _, rule := range d.Unlocked {
		b.WriteString("\n")
		b.WriteString(p.good("Achievement unlocked: " + rule.Title))
		b.WriteString("\n")
		b.WriteString(indentParagraph(rule.Description))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(renderRankLine(d, p))
	b.WriteString("\n")

	b.WriteString("\n")
	b.WriteString(renderNextStep(d.Next))
	b.WriteString("\n")

	return b.String()
}

// renderNextStep says what to do now the level is over.
//
// Every wording here names `exit` before it names `shellforge`, and that
// order is the whole point rather than a stylistic choice. A learner reading
// this has just passed and is sitting at a prompt inside the sandbox;
// `shellforge` is a program on their own computer and the sandbox cannot
// reach it. Telling them to run it without telling them to leave first earns
// them a "command not found" and no explanation, which is exactly what
// happened to the first person to finish a level.
func renderNextStep(n nextStep) string {
	if n.Complete {
		return "That was the last level in the pack.\n" +
			"Type `exit` to leave the sandbox, then run `shellforge stats` to see the whole picture."
	}
	if n.LevelID == "" {
		if n.Offered {
			return "Type `exit` to leave the sandbox, and you will be asked whether to carry on."
		}
		return "Type `exit` to leave the sandbox, then run `shellforge play` to carry on."
	}
	if n.Offered {
		return fmt.Sprintf("Next up: %s, %s.\nType `exit` to leave the sandbox, and you will be asked whether to start it.",
			n.LevelID, n.Title)
	}
	return fmt.Sprintf("Next up: %s, %s.\nType `exit` to leave the sandbox, then run `shellforge play` to start it.",
		n.LevelID, n.Title)
}

// renderScoreBreakdown is the arithmetic block: one line per contribution,
// a rule, then the total.
func renderScoreBreakdown(res score.Result, p colours) string {
	var b strings.Builder
	for i, line := range res.Breakdown {
		b.WriteString("  ")
		b.WriteString(padRight(line.Label, scoreColumn))
		// The opening base line is a quantity, so it reads as a plain
		// number. Every line after it is a change to that number, so it
		// carries its sign.
		if i == 0 {
			b.WriteString(fmt.Sprintf("%5d", line.Delta))
		} else {
			b.WriteString(fmt.Sprintf("%+5d", line.Delta))
		}
		b.WriteString("\n")
	}
	b.WriteString("  ")
	b.WriteString(strings.Repeat("-", scoreColumn+5))
	b.WriteString("\n  ")
	b.WriteString(padRight("XP earned", scoreColumn))
	b.WriteString(p.good(fmt.Sprintf("%5d", res.Total)))
	b.WriteString("\n")
	return b.String()
}

// renderRankLine is the total, the rank, and either a rank up or how far
// the next one is.
//
// A rank up is the one moment worth saying loudly, and it is still one
// extra line rather than an animation: this is a plain CLI and the TUI is
// on the cut list.
func renderRankLine(d passSummaryData, p colours) string {
	if !d.HasRanks {
		return fmt.Sprintf("Total XP %d.", d.TotalXP)
	}

	if d.RankAfter.Title != d.RankBefore.Title && d.RankAfter.Title != "" {
		line := p.good(fmt.Sprintf("Rank up: %s.", rankTransition(d.RankBefore, d.RankAfter)))
		return fmt.Sprintf("Total XP %d. %s", d.TotalXP, line)
	}

	if d.NextRank.Title == "" {
		return fmt.Sprintf("Total XP %d. %s, the top of the ladder.", d.TotalXP, rankName(d.RankAfter))
	}
	return fmt.Sprintf("Total XP %d. %s, %d XP to %s.",
		d.TotalXP, rankName(d.RankAfter), d.NextRank.MinXP-d.TotalXP, d.NextRank.Title)
}

// rankTransition words a rank up, handling the learner who had no rank at
// all because the pack's lowest starts above zero.
func rankTransition(before, after content.Rank) string {
	if before.Title == "" {
		return "you are now " + after.Title
	}
	return before.Title + " becomes " + after.Title
}

// rankName is a rank's title, or a plain phrase for a learner who has not
// reached the lowest one.
func rankName(r content.Rank) string {
	if r.Title == "" {
		return "No rank yet"
	}
	return r.Title
}

// padRight pads s with spaces to width, and leaves an over-long label
// alone rather than truncating it: a label that does not fit is a wider
// column's problem, not a reason to hide what a number is for.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s + " "
	}
	return s + strings.Repeat(" ", width-len(s))
}
