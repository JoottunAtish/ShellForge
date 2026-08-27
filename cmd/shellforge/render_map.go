package main

import (
	"fmt"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
)

// Rendering `shellforge map`: the campaign as a text tree grouped by act.
//
// renderMap is a pure function over already-resolved data, the same shape as
// renderCheckReply in render_check.go: content and game decide what the
// campaign looks like, this file only decides what the learner reads. Unlike
// a check reply, this never crosses the sandbox's raw-mode PTY. `map` runs on
// the host, writing straight to the CLI's own stdout through a normal cooked
// terminal (or a redirected file), so lines end in a plain "\n" and crlf,
// imported from render_check.go, is never called here.
//
// Marker glyphs are fixed ASCII strings regardless of colour, so disabling
// colour (NO_COLOR, or --ascii) never changes which characters are printed,
// only whether they are wrapped in an ANSI code. That is what makes the
// colour and ASCII renderings byte identical: turning colour off removes
// every "\x1b[...m" and leaves the text untouched.
const (
	mapMarkPassed    = "[x]"
	mapMarkAvailable = "[ ]"
	mapMarkSkipped   = "[s]"
	mapMarkLocked    = "[-]"
)

// ansi codes. A fresh copy of render_check.go's three roles, not an import
// of them: four constants and four one-line methods do not earn a shared
// helper package, and render_check.go's crlf-oriented neighbours are not
// something this file wants a reason to touch.
const (
	mapAnsiReset = "\x1b[0m"
	mapAnsiGood  = "\x1b[1;32m"
	mapAnsiBad   = "\x1b[1;31m"
	mapAnsiDim   = "\x1b[2m"
)

// mapColours wraps text in ANSI codes, or does not. A same-shaped sibling of
// render_check.go's colours, kept separate rather than shared: see the
// comment on the ansi code constants above for why.
type mapColours struct {
	enabled bool
}

func mapPalette(enabled bool) mapColours { return mapColours{enabled: enabled} }

func (c mapColours) good(s string) string { return c.wrap(mapAnsiGood, s) }
func (c mapColours) bad(s string) string  { return c.wrap(mapAnsiBad, s) }
func (c mapColours) dim(s string) string  { return c.wrap(mapAnsiDim, s) }

func (c mapColours) wrap(code, s string) string {
	if !c.enabled || s == "" {
		return s
	}
	return code + s + mapAnsiReset
}

// renderMap renders pack's campaign, joined with nodes (already resolved by
// game.Resolve, in campaign order), as a text tree grouped by act.
//
// Layout, decided once here and held stable by render_map_test.go: each
// declared act prints its title, its subtitle if it has one, then one line
// per level carrying a bracketed ASCII marker, the title, and the XP award,
// then a per-act progress count. A locked level's line ends with which
// prerequisites are still unmet. A level whose ActID names no declared act
// (an orphan Pack.Order still surfaces, sorted by id after the acts) prints
// under a final "Unassigned" heading, only when at least one exists. The
// file closes with a total progress count across every node.
//
// A level counts toward "progress" once it is passed or skipped: both mean
// the learner is done with it, and lumping them is what lets the total at
// the bottom read as "how much of the campaign is behind you" rather than a
// number a learner has to mentally split into two.
func renderMap(pack *content.Pack, nodes []game.Node, color bool) string {
	p := mapPalette(color)
	byAct := groupByAct(nodes)

	var b strings.Builder
	totalDone, totalCount := 0, 0

	for _, act := range pack.Acts {
		actNodes := byAct[act.ID]
		delete(byAct, act.ID)
		writeAct(&b, p, act.Title, act.Subtitle, actNodes)
		done, count := progressCount(actNodes)
		totalDone += done
		totalCount += count
	}

	if orphans := remainingInOrder(nodes, byAct); len(orphans) > 0 {
		writeAct(&b, p, "Unassigned", "Levels not listed in any act", orphans)
		done, count := progressCount(orphans)
		totalDone += done
		totalCount += count
	}

	fmt.Fprintf(&b, "Total: %d/%d complete\n", totalDone, totalCount)
	return b.String()
}

// groupByAct buckets nodes by ActID, preserving the campaign order within
// each bucket, which is already the order Resolve produced them in.
func groupByAct(nodes []game.Node) map[string][]game.Node {
	byAct := make(map[string][]game.Node)
	for _, n := range nodes {
		byAct[n.ActID] = append(byAct[n.ActID], n)
	}
	return byAct
}

// remainingInOrder flattens whatever is left in byAct after every declared
// act has claimed its own nodes, in the original campaign order, so an
// orphan section reads the same way the declared acts do.
func remainingInOrder(nodes []game.Node, byAct map[string][]game.Node) []game.Node {
	var out []game.Node
	for _, n := range nodes {
		if bucket, ok := byAct[n.ActID]; ok && len(bucket) > 0 {
			out = append(out, n)
			// Consume one occurrence, in case two acts somehow share an id
			// (invalid content, but this function must not panic on it).
			byAct[n.ActID] = bucket[1:]
		}
	}
	return out
}

// writeAct writes one act's heading, its subtitle if it has one, one line
// per level, and its own progress count.
func writeAct(b *strings.Builder, p mapColours, title, subtitle string, nodes []game.Node) {
	b.WriteString(title)
	b.WriteString("\n")
	if subtitle != "" {
		b.WriteString(p.dim(subtitle))
		b.WriteString("\n")
	}
	for _, n := range nodes {
		b.WriteString(levelLine(p, n))
		b.WriteString("\n")
	}
	done, count := progressCount(nodes)
	fmt.Fprintf(b, "%d/%d complete\n\n", done, count)
}

// levelLine renders one level's row: its marker, title, XP, and, when
// locked, what it is waiting on.
func levelLine(p mapColours, n game.Node) string {
	line := fmt.Sprintf("  %s %s (%d xp)", mark(p, n.Availability), n.Title, n.XP)
	if n.Availability == game.AvailableLocked && len(n.BlockedBy) > 0 {
		line += p.dim(fmt.Sprintf(" (locked: needs %s)", strings.Join(n.BlockedBy, ", ")))
	}
	return line
}

// mark returns the coloured marker for one level's availability.
func mark(p mapColours, a game.Availability) string {
	switch a {
	case game.AvailablePassed:
		return p.good(mapMarkPassed)
	case game.AvailableSkipped:
		return p.dim(mapMarkSkipped)
	case game.AvailableLocked:
		return p.dim(mapMarkLocked)
	default: // game.AvailableNow
		return mapMarkAvailable
	}
}

// progressCount reports how many of nodes are done (passed or skipped) out
// of the total.
func progressCount(nodes []game.Node) (done, count int) {
	for _, n := range nodes {
		count++
		if n.Availability == game.AvailablePassed || n.Availability == game.AvailableSkipped {
			done++
		}
	}
	return done, count
}
