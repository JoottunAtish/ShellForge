package game

import (
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// diamondPack builds a small fixture pack with a diamond dependency: A has
// no prerequisites, B and C each require A, and D requires both B and C.
// One act holds all four, in that order. Tests mutate a copy of the states
// map returned alongside it, never the pack.
func diamondPack() *content.Pack {
	return &content.Pack{
		ID: "fixture-diamond",
		Acts: []content.Act{
			{ID: "act1", Title: "Act One", Subtitle: "The only act", Levels: []string{"a", "b", "c", "d"}},
		},
		Levels: []content.Level{
			{ID: "a", Version: 1, Title: "Level A", Act: "act1", Difficulty: 1, XP: 10},
			{ID: "b", Version: 1, Title: "Level B", Act: "act1", Difficulty: 1, XP: 10, Prerequisites: []string{"a"}},
			{ID: "c", Version: 1, Title: "Level C", Act: "act1", Difficulty: 1, XP: 10, Prerequisites: []string{"a"}},
			{ID: "d", Version: 1, Title: "Level D", Act: "act1", Difficulty: 2, XP: 20, Prerequisites: []string{"b", "c"}},
		},
	}
}

func passedState(levelID string, version int) store.LevelState {
	return store.LevelState{LevelID: levelID, Status: store.StatusPassed, LevelVersion: version, BestScore: 100}
}

func skippedState(levelID string) store.LevelState {
	return store.LevelState{LevelID: levelID, Status: store.StatusSkipped}
}

// nodesEqual compares two Nodes field by field, since Node holds a slice
// and so is not comparable with ==.
func nodesEqual(a, b Node) bool {
	if a.LevelID != b.LevelID || a.ActID != b.ActID || a.Title != b.Title ||
		a.XP != b.XP || a.Difficulty != b.Difficulty || a.Availability != b.Availability ||
		a.BestScore != b.BestScore || a.Attempts != b.Attempts || a.Stale != b.Stale {
		return false
	}
	return strings.Join(a.BlockedBy, ",") == strings.Join(b.BlockedBy, ",")
}

func nodeByID(t *testing.T, nodes []Node, id string) Node {
	t.Helper()
	for _, n := range nodes {
		if n.LevelID == id {
			return n
		}
	}
	t.Fatalf("no node for level %q in %v", id, nodes)
	return Node{}
}

// TestResolveFreshProfileOnlyPrerequisiteFreeLevelsAvailable is acceptance
// criterion 1: with no rows at all, every level with no prerequisites is
// available and every level with one is locked.
func TestResolveFreshProfileOnlyPrerequisiteFreeLevelsAvailable(t *testing.T) {
	pack := diamondPack()
	nodes, err := Resolve(pack, map[string]store.LevelState{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("got %d nodes, want 4", len(nodes))
	}

	if got := nodeByID(t, nodes, "a").Availability; got != AvailableNow {
		t.Errorf("level a (no prerequisites) = %q, want %q", got, AvailableNow)
	}
	for _, id := range []string{"b", "c", "d"} {
		if got := nodeByID(t, nodes, id).Availability; got != AvailableLocked {
			t.Errorf("level %s (has prerequisites) = %q, want %q", id, got, AvailableLocked)
		}
	}
}

// TestResolveDiamondUnlocksAsPrerequisitesArePassed is acceptance criterion
// 2: a level becomes available exactly when all of its prerequisites are
// passed or skipped, walked stage by stage over the diamond fixture.
func TestResolveDiamondUnlocksAsPrerequisitesArePassed(t *testing.T) {
	pack := diamondPack()

	// Stage 0: nothing done. b, c, d locked.
	nodes, err := Resolve(pack, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, id := range []string{"b", "c", "d"} {
		if got := nodeByID(t, nodes, id).Availability; got != AvailableLocked {
			t.Errorf("stage 0: level %s = %q, want locked", id, got)
		}
	}

	// Stage 1: a passed. b and c unlock, d stays locked (needs both).
	states := map[string]store.LevelState{"a": passedState("a", 1)}
	nodes, err = Resolve(pack, states)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := nodeByID(t, nodes, "a").Availability; got != AvailablePassed {
		t.Errorf("stage 1: level a = %q, want passed", got)
	}
	for _, id := range []string{"b", "c"} {
		if got := nodeByID(t, nodes, id).Availability; got != AvailableNow {
			t.Errorf("stage 1: level %s = %q, want available", id, got)
		}
	}
	if got := nodeByID(t, nodes, "d").Availability; got != AvailableLocked {
		t.Errorf("stage 1: level d = %q, want locked", got)
	}

	// Stage 2: a and b passed, c still open. d still locked: c is unmet.
	states["b"] = passedState("b", 1)
	nodes, err = Resolve(pack, states)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := nodeByID(t, nodes, "d").Availability; got != AvailableLocked {
		t.Errorf("stage 2: level d = %q, want locked", got)
	}

	// Stage 3: a, b, c all passed. d unlocks.
	states["c"] = passedState("c", 1)
	nodes, err = Resolve(pack, states)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := nodeByID(t, nodes, "d").Availability; got != AvailableNow {
		t.Errorf("stage 3: level d = %q, want available", got)
	}
}

// TestResolveSkippedSatisfiesAPrerequisite is acceptance criterion 2's
// "or skipped" half, and the ticket's own test plan item: mark b skipped,
// assert d unlocks once c is passed.
func TestResolveSkippedSatisfiesAPrerequisite(t *testing.T) {
	pack := diamondPack()
	states := map[string]store.LevelState{
		"a": passedState("a", 1),
		"b": skippedState("b"),
		"c": passedState("c", 1),
	}
	nodes, err := Resolve(pack, states)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := nodeByID(t, nodes, "b").Availability; got != AvailableSkipped {
		t.Errorf("level b = %q, want skipped", got)
	}
	if got := nodeByID(t, nodes, "d").Availability; got != AvailableNow {
		t.Errorf("level d = %q, want available once its skipped and passed prerequisites are both met", got)
	}
}

// TestResolveBlockedByNamesUnmetPrerequisitesInPackOrder is acceptance
// criterion 3.
func TestResolveBlockedByNamesUnmetPrerequisitesInPackOrder(t *testing.T) {
	pack := diamondPack()
	nodes, err := Resolve(pack, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	d := nodeByID(t, nodes, "d")
	want := []string{"b", "c"}
	if strings.Join(d.BlockedBy, ",") != strings.Join(want, ",") {
		t.Errorf("d.BlockedBy = %v, want %v", d.BlockedBy, want)
	}

	for _, id := range []string{"a", "b", "c"} {
		n := nodeByID(t, nodes, id)
		if n.Availability == AvailableLocked {
			continue
		}
		if len(n.BlockedBy) != 0 {
			t.Errorf("level %s (%s) has BlockedBy %v, want empty", id, n.Availability, n.BlockedBy)
		}
	}

	// a has no prerequisites and is available: BlockedBy must be empty too.
	if a := nodeByID(t, nodes, "a"); len(a.BlockedBy) != 0 {
		t.Errorf("level a has BlockedBy %v, want empty", a.BlockedBy)
	}

	// BlockedBy must also be empty for a passed level and for a skipped
	// level, not just for the available one already checked above. This
	// pins "empty for every other availability" explicitly, on its own
	// Resolve call, rather than only by construction: nodes above never
	// carries a passed or skipped node, since states was nil.
	passedOrSkipped := map[string]store.LevelState{
		"a": passedState("a", 1),
		"b": skippedState("b"),
	}
	nodes2, err := Resolve(pack, passedOrSkipped)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := nodeByID(t, nodes2, "a"); got.Availability != AvailablePassed {
		t.Fatalf("level a = %q, want passed (fixture check, not the assertion under test)", got.Availability)
	}
	if got := nodeByID(t, nodes2, "a"); len(got.BlockedBy) != 0 {
		t.Errorf("passed level a has BlockedBy %v, want empty", got.BlockedBy)
	}
	if got := nodeByID(t, nodes2, "b"); got.Availability != AvailableSkipped {
		t.Fatalf("level b = %q, want skipped (fixture check, not the assertion under test)", got.Availability)
	}
	if got := nodeByID(t, nodes2, "b"); len(got.BlockedBy) != 0 {
		t.Errorf("skipped level b has BlockedBy %v, want empty", got.BlockedBy)
	}
}

// TestResolveNilStatesBehavesLikeEmptyMap is contract decision 2: a nil
// states map must not panic or error, and must behave exactly like an empty
// non-nil map, which is what a fresh profile with no rows produces.
func TestResolveNilStatesBehavesLikeEmptyMap(t *testing.T) {
	pack := diamondPack()

	nilNodes, err := Resolve(pack, nil)
	if err != nil {
		t.Fatalf("Resolve with nil states: %v", err)
	}
	emptyNodes, err := Resolve(pack, map[string]store.LevelState{})
	if err != nil {
		t.Fatalf("Resolve with empty states: %v", err)
	}

	if len(nilNodes) != len(emptyNodes) {
		t.Fatalf("nil states produced %d nodes, empty map produced %d", len(nilNodes), len(emptyNodes))
	}
	for i := range nilNodes {
		if !nodesEqual(nilNodes[i], emptyNodes[i]) {
			t.Errorf("node %d differs between nil and empty states: %+v vs %+v", i, nilNodes[i], emptyNodes[i])
		}
	}
}

// TestNextReturnsFirstAvailableInCampaignOrder is acceptance criterion 4's
// first half.
func TestNextReturnsFirstAvailableInCampaignOrder(t *testing.T) {
	pack := diamondPack()
	states := map[string]store.LevelState{"a": passedState("a", 1)}
	nodes, err := Resolve(pack, states)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	next, ok := Next(nodes)
	if !ok {
		t.Fatal("Next reported false with two levels available")
	}
	// b comes before c in campaign order (pack.yaml lists b before c).
	if next.LevelID != "b" {
		t.Errorf("Next = %q, want %q (the first available level in campaign order)", next.LevelID, "b")
	}
}

// TestNextReportsFalseWhenCampaignComplete is acceptance criterion 4's
// second half: the bool is false, not a zero Node silently returned as if
// it meant something, when every level is passed.
func TestNextReportsFalseWhenCampaignComplete(t *testing.T) {
	pack := diamondPack()
	states := map[string]store.LevelState{
		"a": passedState("a", 1),
		"b": passedState("b", 1),
		"c": passedState("c", 1),
		"d": passedState("d", 1),
	}
	nodes, err := Resolve(pack, states)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	next, ok := Next(nodes)
	if ok {
		t.Fatalf("Next reported true with the campaign complete: %+v", next)
	}
	if !nodesEqual(next, Node{}) {
		t.Errorf("Next returned a non-zero Node on false: %+v", next)
	}
}

// TestResolveStaleScoreStillCountsAsPassedForUnlocking covers the test
// plan's stale case: a state recorded against level version 1, read at
// version 2, still counts as passed for unlocking, and Node.Stale is set so
// the renderer knows not to trust BestScore.
func TestResolveStaleScoreStillCountsAsPassedForUnlocking(t *testing.T) {
	pack := diamondPack()
	// Bump level a to version 2, as if an author fixed a typo in its check.
	lvl, ok := pack.Level("a")
	if !ok {
		t.Fatal("fixture is missing level a")
	}
	lvl.Version = 2

	states := map[string]store.LevelState{"a": passedState("a", 1)}
	nodes, err := Resolve(pack, states)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	a := nodeByID(t, nodes, "a")
	if a.Availability != AvailablePassed {
		t.Errorf("stale level a = %q, want passed: re-locking on a stale score would punish the learner for an author's fix", a.Availability)
	}
	if !a.Stale {
		t.Error("level a recorded at version 1, read at version 2, should be Stale")
	}
	if a.BestScore != 0 {
		t.Errorf("stale BestScore = %d, want 0 (unknown, not a number that might be wrong)", a.BestScore)
	}

	// Unlocking still follows from the stale pass: b and c must be available.
	for _, id := range []string{"b", "c"} {
		if got := nodeByID(t, nodes, id).Availability; got != AvailableNow {
			t.Errorf("level %s should unlock from a's stale pass, got %q", id, got)
		}
	}
}

// TestResolveCycleFailsNamingTheStuckIDs is acceptance criterion 8's data
// half: a cycle in the pack fails, and the error names the ids stuck on it.
func TestResolveCycleFailsNamingTheStuckIDs(t *testing.T) {
	pack := &content.Pack{
		ID: "fixture-cycle",
		Acts: []content.Act{
			{ID: "act1", Title: "Act One", Levels: []string{"x", "y"}},
		},
		Levels: []content.Level{
			{ID: "x", Version: 1, Title: "Level X", Act: "act1", Prerequisites: []string{"y"}},
			{ID: "y", Version: 1, Title: "Level Y", Act: "act1", Prerequisites: []string{"x"}},
		},
	}

	nodes, err := Resolve(pack, nil)
	if err == nil {
		t.Fatalf("Resolve did not fail on a cycle, got nodes: %v", nodes)
	}
	for _, id := range []string{"x", "y"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("cycle error %q does not name stuck id %q", err.Error(), id)
		}
	}
}
