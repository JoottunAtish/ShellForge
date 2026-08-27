package main

import (
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
)

// fixtureMapPack is a small, two-act pack with a locked level, used by every
// test in this file rather than the shipped pack, so these tests do not
// drift as more levels are authored. It deliberately does not match
// diamondPack in internal/game/curriculum_test.go: this file exercises
// rendering, not unlock resolution, and needs two acts to prove acts print
// in pack.yaml order.
func fixtureMapPack() *content.Pack {
	return &content.Pack{
		ID:   "fixture-map",
		Name: "Fixture Campaign",
		Acts: []content.Act{
			{ID: "act1", Title: "Act One: First Steps", Subtitle: "Finding your way around", Levels: []string{"nav-01", "nav-02"}},
			{ID: "act2", Title: "Act Two: Files", Subtitle: "", Levels: []string{"files-01"}},
		},
	}
}

// fixtureMapNodes is the resolved state to go with fixtureMapPack: nav-01
// passed, nav-02 available, files-01 locked behind nav-02.
func fixtureMapNodes() []game.Node {
	return []game.Node{
		{LevelID: "nav-01", ActID: "act1", Title: "Where Am I", XP: 10, Difficulty: 1, Availability: game.AvailablePassed, BestScore: 95, Attempts: 2},
		{LevelID: "nav-02", ActID: "act1", Title: "Getting Around", XP: 10, Difficulty: 1, Availability: game.AvailableNow},
		{LevelID: "files-01", ActID: "act2", Title: "Make a Home", XP: 15, Difficulty: 2, Availability: game.AvailableLocked, BlockedBy: []string{"nav-02"}},
	}
}

func TestRenderMapPrintsActsInPackOrderWithTitleAndSubtitle(t *testing.T) {
	out := renderMap(fixtureMapPack(), fixtureMapNodes(), false)

	act1 := strings.Index(out, "Act One: First Steps")
	act2 := strings.Index(out, "Act Two: Files")
	if act1 == -1 || act2 == -1 {
		t.Fatalf("both act titles must appear: %q", out)
	}
	if act1 > act2 {
		t.Errorf("Act One must print before Act Two, per pack.yaml order: %q", out)
	}
	if !strings.Contains(out, "Finding your way around") {
		t.Errorf("act1's subtitle is missing: %q", out)
	}
}

func TestRenderMapPrintsEachLevelMarkerTitleAndXP(t *testing.T) {
	out := renderMap(fixtureMapPack(), fixtureMapNodes(), false)

	cases := []struct {
		levelLine string
	}{
		{"[x] Where Am I (10 xp)"},
		{"[ ] Getting Around (10 xp)"},
		{"[-] Make a Home (15 xp)"},
	}
	for _, tc := range cases {
		if !strings.Contains(out, tc.levelLine) {
			t.Errorf("output missing level line %q in: %q", tc.levelLine, out)
		}
	}
}

func TestRenderMapLockedLevelShowsBlockedBy(t *testing.T) {
	out := renderMap(fixtureMapPack(), fixtureMapNodes(), false)
	if !strings.Contains(out, "locked: needs nav-02") {
		t.Errorf("locked level's line does not name what it needs: %q", out)
	}
}

func TestRenderMapPerActAndTotalProgressCounts(t *testing.T) {
	out := renderMap(fixtureMapPack(), fixtureMapNodes(), false)

	// act1: nav-01 passed, nav-02 available -> 1/2.
	if !strings.Contains(out, "1/2 complete") {
		t.Errorf("act1 progress count missing or wrong: %q", out)
	}
	// act2: files-01 locked -> 0/1.
	if !strings.Contains(out, "0/1 complete") {
		t.Errorf("act2 progress count missing or wrong: %q", out)
	}
	// total: 1/3.
	if !strings.Contains(out, "Total: 1/3 complete") {
		t.Errorf("total progress count missing or wrong: %q", out)
	}
}

func TestRenderMapSkippedCountsAsProgress(t *testing.T) {
	pack := fixtureMapPack()
	nodes := fixtureMapNodes()
	// nav-02 skipped instead of available; files-01 unlocks as a result.
	nodes[1].Availability = game.AvailableSkipped
	nodes[2].Availability = game.AvailableNow
	nodes[2].BlockedBy = nil

	out := renderMap(pack, nodes, false)
	if !strings.Contains(out, "[s] Getting Around") {
		t.Errorf("skipped marker missing: %q", out)
	}
	if !strings.Contains(out, "Total: 2/3 complete") {
		t.Errorf("skipped level should count toward progress: %q", out)
	}
}

// TestRenderMapColorOffIsAsciiOnlyWithNoEscapeSequence is acceptance
// criterion 6's rendering half. cmd_map.go is what maps NO_COLOR and
// --ascii onto renderMap's color bool, both to false; since both roads lead
// to the same argument, renderMap itself only has one thing left to prove:
// that color=false output carries no escape sequence and no byte outside
// ASCII, which is what makes the two flags produce byte-identical output by
// construction rather than by coincidence.
func TestRenderMapColorOffIsAsciiOnlyWithNoEscapeSequence(t *testing.T) {
	pack := fixtureMapPack()
	nodes := fixtureMapNodes()

	out := renderMap(pack, nodes, false)
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("color=false output contains an escape sequence: %q", out)
	}
	for i, r := range out {
		if r > 127 {
			t.Errorf("color=false output has a non-ASCII rune %q at byte %d: %q", r, i, out)
		}
	}
}

// TestRenderMapColorOnAddsEscapeSequencesButSameText asserts colour changes
// only the wrapping, never which characters print: strip every ANSI code
// from the coloured output and it must equal the plain one, and colour must
// actually add at least one escape sequence to be worth testing.
func TestRenderMapColorOnAddsEscapeSequencesButSameText(t *testing.T) {
	pack := fixtureMapPack()
	nodes := fixtureMapNodes()

	plain := renderMap(pack, nodes, false)
	colored := renderMap(pack, nodes, true)

	if !strings.ContainsRune(colored, '\x1b') {
		t.Fatal("colour-on output has no escape sequence; the test proves nothing")
	}
	if stripANSI(colored) != plain {
		t.Errorf("stripping ANSI from the coloured output does not match the plain output:\nplain:   %q\nstripped: %q", plain, stripANSI(colored))
	}
}

// stripANSI is render_check_test.go's helper; renderMap emits the same
// "\x1b[...m" SGR shape, so it is reused rather than duplicated here.

// TestRenderMapGoldenPlainOutput pins the exact ASCII rendering of
// fixtureMapPack and fixtureMapNodes as an inline literal, per the
// Definition of Done: a golden output for both colour and ASCII, held as a
// Go string literal in this file rather than a testdata/ fixture, since
// this repository has no testdata/golden-file convention for CLI text.
func TestRenderMapGoldenPlainOutput(t *testing.T) {
	const want = "Act One: First Steps\n" +
		"Finding your way around\n" +
		"  [x] Where Am I (10 xp)\n" +
		"  [ ] Getting Around (10 xp)\n" +
		"1/2 complete\n" +
		"\n" +
		"Act Two: Files\n" +
		"  [-] Make a Home (15 xp) (locked: needs nav-02)\n" +
		"0/1 complete\n" +
		"\n" +
		"Total: 1/3 complete\n"

	got := renderMap(fixtureMapPack(), fixtureMapNodes(), false)
	if got != want {
		t.Errorf("plain renderMap output changed:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestRenderMapGoldenColorOutput is TestRenderMapGoldenPlainOutput's
// colour-on twin.
func TestRenderMapGoldenColorOutput(t *testing.T) {
	const want = "Act One: First Steps\n" +
		"\x1b[2mFinding your way around\x1b[0m\n" +
		"  \x1b[1;32m[x]\x1b[0m Where Am I (10 xp)\n" +
		"  [ ] Getting Around (10 xp)\n" +
		"1/2 complete\n" +
		"\n" +
		"Act Two: Files\n" +
		"  \x1b[2m[-]\x1b[0m Make a Home (15 xp)\x1b[2m (locked: needs nav-02)\x1b[0m\n" +
		"0/1 complete\n" +
		"\n" +
		"Total: 1/3 complete\n"

	got := renderMap(fixtureMapPack(), fixtureMapNodes(), true)
	if got != want {
		t.Errorf("colour renderMap output changed:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestRenderMapOrphanLevelPrintsUnderUnassignedHeading covers a node whose
// ActID names no act present in the pack's Acts: content.Pack.Order can
// surface this for a level listed in no act's Levels list, and renderMap
// must still show it rather than silently dropping it, under a final
// "Unassigned" heading that also counts toward the total.
func TestRenderMapOrphanLevelPrintsUnderUnassignedHeading(t *testing.T) {
	pack := fixtureMapPack()
	nodes := fixtureMapNodes()
	nodes = append(nodes, game.Node{
		LevelID:      "orphan-01",
		ActID:        "no-such-act",
		Title:        "Adrift",
		XP:           5,
		Difficulty:   1,
		Availability: game.AvailableNow,
	})

	out := renderMap(pack, nodes, false)

	unassignedIdx := strings.Index(out, "Unassigned")
	orphanIdx := strings.Index(out, "[ ] Adrift (5 xp)")
	if unassignedIdx == -1 {
		t.Fatalf("output missing the Unassigned heading: %q", out)
	}
	if orphanIdx == -1 {
		t.Fatalf("output missing the orphan level's line: %q", out)
	}
	if orphanIdx < unassignedIdx {
		t.Errorf("orphan level line (index %d) does not appear under the Unassigned heading (index %d): %q", orphanIdx, unassignedIdx, out)
	}
	// act1: 1/2, act2: 0/1, Unassigned: 1/1 (orphan is available, not done)
	// counted, so the total gains one to its denominator: 1/4.
	if !strings.Contains(out, "Total: 1/4 complete") {
		t.Errorf("orphan level was not counted in the total progress line: %q", out)
	}
}

func TestRenderMapEmptyPackProducesTotalLine(t *testing.T) {
	pack := &content.Pack{ID: "empty"}
	out := renderMap(pack, nil, false)
	if !strings.Contains(out, "Total: 0/0 complete") {
		t.Errorf("empty pack should still print a total line: %q", out)
	}
}
