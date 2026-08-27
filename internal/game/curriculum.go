package game

import (
	"fmt"
	"sort"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// Availability is what the learner may do with a level right now.
type Availability string

// The four availabilities a Node can hold. AvailableNow and AvailableLocked
// are mutually exclusive with having a recorded state at all: a level with
// no store.LevelState row is either available or locked, never passed or
// skipped.
const (
	// AvailableNow means every prerequisite is passed or skipped and the
	// learner has not yet passed or skipped this level itself.
	AvailableNow Availability = "available"

	// AvailablePassed means the learner has already passed this level.
	AvailablePassed Availability = "passed"

	// AvailableSkipped means the learner marked this level skipped rather
	// than passing it. A skip satisfies a downstream prerequisite the same
	// way a pass does; see the package doc comment for why.
	AvailableSkipped Availability = "skipped"

	// AvailableLocked means at least one prerequisite is neither passed nor
	// skipped.
	AvailableLocked Availability = "locked"
)

// Node is one level, joined with what the learner has done to it.
type Node struct {
	// LevelID is the level's unique identifier.
	LevelID string

	// ActID is the id of the act this level belongs to.
	ActID string

	// Title is the level's display name.
	Title string

	// XP is the level's base experience award.
	XP int

	// Difficulty is 1 to 5.
	Difficulty int

	// Availability is what the learner may do with this level right now.
	Availability Availability

	// BestScore is the learner's best recorded score, or zero when there is
	// none, or when Stale is true and the recorded score no longer means
	// anything.
	BestScore int

	// Attempts is how many times the learner has started this level.
	Attempts int

	// Stale is true when the recorded state predates the current level
	// version: an author changed the level's checks since the learner's
	// best score was recorded. A stale level still counts as passed for
	// unlocking; only the score itself is untrustworthy.
	Stale bool

	// BlockedBy names the unmet prerequisites, in campaign order, for a
	// locked level. It is empty for every other availability.
	BlockedBy []string
}

// Resolve joins pack with recorded level state and returns one Node per
// loaded level, in campaign order. It never reads a sandbox and never
// writes.
//
// A level listed in an act with no file yet is omitted, matching
// Pack.Order, because a half-filled pack is the pack's normal state while
// it is being written. A cycle in the prerequisite graph is returned as an
// error naming the stuck ids, straight from Pack.Order, which already
// proves the graph acyclic and is not re-derived here.
//
// A nil states map behaves exactly like an empty one: a fresh profile with
// no rows in the progress database produces this, and Resolve must not
// panic or error on it, so every level with no prerequisites reports
// AvailableNow and every level with one reports AvailableLocked.
//
// The unlock rule, per ARCHITECTURE 4.11: a level is available when every
// one of its prerequisites is passed or skipped. ARCHITECTURE 4.11 also
// conditions the skip half on a soft_prereq flag that does not exist in
// docs/LEVEL-FORMAT.md or content.Level; this function does not gate on
// it, so a skip satisfies any prerequisite, soft or not. See PROGRESS.md
// for the ratification note.
func Resolve(pack *content.Pack, states map[string]store.LevelState) ([]Node, error) {
	order, err := pack.Order()
	if err != nil {
		return nil, fmt.Errorf("resolve campaign order: %w", err)
	}

	position := make(map[string]int, len(order))
	for i, id := range order {
		position[id] = i
	}

	// satisfied holds every level id whose recorded status counts toward a
	// downstream prerequisite: passed, or skipped.
	satisfied := make(map[string]bool, len(order))
	for id, st := range states {
		if st.Status == store.StatusPassed || st.Status == store.StatusSkipped {
			satisfied[id] = true
		}
	}

	nodes := make([]Node, 0, len(order))
	for _, id := range order {
		lvl, ok := pack.Level(id)
		if !ok {
			// Pack.Order only returns ids it already resolved to a loaded
			// Level, so this branch is unreachable in practice. Skipping
			// rather than panicking keeps this function's own promise:
			// it never panics on pack or state data it did not construct.
			continue
		}

		node := Node{
			LevelID:    lvl.ID,
			ActID:      lvl.Act,
			Title:      lvl.Title,
			XP:         lvl.XP,
			Difficulty: lvl.Difficulty,
		}

		st, hasState := states[id]
		switch {
		case hasState && st.Status == store.StatusPassed:
			node.Availability = AvailablePassed
			node.Attempts = st.Attempts
			node.Stale = st.LevelVersion != lvl.Version
			if !node.Stale {
				node.BestScore = st.BestScore
			}
		case hasState && st.Status == store.StatusSkipped:
			node.Availability = AvailableSkipped
			node.Attempts = st.Attempts
		default:
			if hasState {
				node.Attempts = st.Attempts
			}
			if blocked := blockedPrerequisites(lvl.Prerequisites, satisfied, position); len(blocked) > 0 {
				node.Availability = AvailableLocked
				node.BlockedBy = blocked
			} else {
				node.Availability = AvailableNow
			}
		}

		nodes = append(nodes, node)
	}

	return nodes, nil
}

// blockedPrerequisites returns the entries of prereqs not present in
// satisfied, ordered by their position in the campaign rather than the
// order they happen to be listed in the level's own YAML, so BlockedBy
// reads the same way the rest of the tree does.
func blockedPrerequisites(prereqs []string, satisfied map[string]bool, position map[string]int) []string {
	var blocked []string
	for _, p := range prereqs {
		if !satisfied[p] {
			blocked = append(blocked, p)
		}
	}
	sort.Slice(blocked, func(i, j int) bool {
		return position[blocked[i]] < position[blocked[j]]
	})
	return blocked
}

// Next returns the level the learner should play now: the first available
// node in campaign order. The bool is false, and the Node is the zero
// value, when every level is passed, skipped, or locked, which is what a
// complete campaign looks like.
func Next(nodes []Node) (Node, bool) {
	for _, n := range nodes {
		if n.Availability == AvailableNow {
			return n, true
		}
	}
	return Node{}, false
}
