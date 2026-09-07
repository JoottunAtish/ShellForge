package main

import (
	"fmt"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/game/achievements"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// Rendering `shellforge stats`: XP, rank, per-act progress, achievements.
//
// A pure function over already-resolved data, the same shape as renderMap
// in render_map.go. It runs on the host, writing to a normal cooked
// terminal or a redirected file, so lines end in a plain "\n" and crlf is
// never called here.
//
// Locked achievements are shown rather than hidden. A locked list is a
// preview of what the game rewards, and hiding it makes the feature
// invisible to anybody who has not stumbled into one.

// statsAchievementIndent is the left margin of the achievement list. Two
// spaces, matching the act list above it, and narrow enough that the whole
// report survives a 30 column terminal without wrapping into nonsense.
const statsAchievementIndent = "  "

// statsActColumn is how wide the act title column is. Wide enough for the
// longest title the shipped pack declares ("Permissions and Processes",
// 25 characters) plus a space, so the counts beside them line up. A longer
// title pushes its own row out rather than being truncated: a name a
// learner cannot read is worse than a row that does not line up.
const statsActColumn = 26

// renderStats renders the whole progress report.
//
// nodes come from game.Resolve, unlocked from store.Achievements, and xp
// from store.TotalXP. Nothing here reads a sandbox, a database or a clock.
func renderStats(pack *content.Pack, nodes []game.Node, xp int, unlocked map[string]store.Achievement, color bool) string {
	p := mapPalette(color)
	var b strings.Builder

	b.WriteString(renderStatsHeader(pack, xp, p))
	b.WriteString("\n")
	b.WriteString(renderStatsActs(pack, nodes, p))
	b.WriteString("\n")
	b.WriteString(renderStatsAchievements(pack, unlocked, p))

	return b.String()
}

// renderStatsHeader is the XP total, the rank, and the distance to the next
// one.
func renderStatsHeader(pack *content.Pack, xp int, p mapColours) string {
	var b strings.Builder
	b.WriteString(p.good(fmt.Sprintf("Total XP: %d", xp)))
	b.WriteString("\n")

	current, next, ok := game.RankFor(pack, xp)
	if !ok {
		// A pack with no rank ladder still has XP worth showing. Inventing
		// a title for it would be worse than saying nothing.
		return b.String()
	}

	b.WriteString(fmt.Sprintf("Rank: %s\n", rankName(current)))
	if next.Title == "" {
		b.WriteString("You are at the top of the ladder.\n")
		return b.String()
	}
	b.WriteString(fmt.Sprintf("Next rank: %s, %s away\n", next.Title, xpAmount(next.MinXP-xp)))
	return b.String()
}

// renderStatsActs is one block per act: the levels passed, out of how many,
// and the XP banked in that act.
func renderStatsActs(pack *content.Pack, nodes []game.Node, p mapColours) string {
	byAct := make(map[string][]game.Node)
	for _, n := range nodes {
		byAct[n.ActID] = append(byAct[n.ActID], n)
	}

	var b strings.Builder
	b.WriteString("Progress\n")

	for _, act := range pack.Acts {
		levels := byAct[act.ID]
		if len(levels) == 0 {
			// An act whose levels are not written yet. Named rather than
			// omitted, so the campaign's shape is visible from the start.
			b.WriteString(fmt.Sprintf("  %-*s %s\n", statsActColumn, act.Title, p.dim("not written yet")))
			continue
		}

		passed, actXP := 0, 0
		for _, n := range levels {
			if n.Availability == game.AvailablePassed {
				passed++
			}
			actXP += n.BestScore
		}

		count := fmt.Sprintf("%d/%d", passed, len(levels))
		line := fmt.Sprintf("  %-*s %-7s %s", statsActColumn, act.Title, count, xpAmount(actXP))
		if passed == len(levels) {
			line = p.good(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

// renderStatsAchievements lists every achievement, earned or not.
func renderStatsAchievements(pack *content.Pack, unlocked map[string]store.Achievement, p mapColours) string {
	rules := achievements.RulesFor(pack)

	var b strings.Builder
	earned := 0
	for _, rule := range rules {
		if unlocked[rule.Key].Unlocked() {
			earned++
		}
	}
	b.WriteString(fmt.Sprintf("Achievements (%d of %d)\n", earned, len(rules)))

	for _, rule := range rules {
		row := unlocked[rule.Key]
		mark, title := mapMarkLocked, p.dim(rule.Title)
		if row.Unlocked() {
			mark, title = mapMarkPassed, p.good(rule.Title)
		}
		b.WriteString(fmt.Sprintf("%s%s %s\n", statsAchievementIndent, mark, title))
		b.WriteString(fmt.Sprintf("%s     %s\n", statsAchievementIndent, p.dim(rule.Description)))
	}

	return b.String()
}
