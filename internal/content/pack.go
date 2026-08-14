package content

import (
	"fmt"
	"sort"
)

// Pack is one content pack: pack.yaml plus every level file under levels/.
//
// The engine refuses a pack whose ContentAPI it does not understand, which is
// the one compatibility lever the format has. Everything else about a pack is
// data.
type Pack struct {
	// ID is the pack's unique identifier.
	ID string `yaml:"id"`

	// Name is the pack's display name.
	Name string `yaml:"name"`

	// Version is the pack's own version string, independent of ContentAPI.
	Version string `yaml:"version"`

	// ContentAPI is the level schema generation this pack was written
	// against. The loader refuses anything it does not understand rather
	// than reading a future pack with today's rules.
	ContentAPI int `yaml:"content_api"`

	// Author, License and Homepage are attribution metadata.
	Author   string `yaml:"author"`
	License  string `yaml:"license"`
	Homepage string `yaml:"homepage"`

	// Description is the pack's summary.
	Description string `yaml:"description"`

	// Intro is the markdown shown once at campaign start.
	Intro string `yaml:"intro"`

	// Acts group levels for presentation and define campaign order.
	Acts []Act `yaml:"acts"`

	// Ranks are awarded by cumulative XP.
	Ranks []Rank `yaml:"ranks"`

	// Levels is filled in by the loader from the levels/ directory. It is
	// absent from pack.yaml, which lists level ids inside its acts rather
	// than the levels themselves.
	Levels []Level `yaml:"-"`
}

// ContentAPIVersion is the level schema generation this build understands. A
// pack declaring anything else is refused by the loader rather than read with
// rules it was not written against.
const ContentAPIVersion = 1

// Act is one chapter of a pack, listing its level ids in campaign order.
type Act struct {
	// ID is the act's identifier, referenced by each level's act field.
	ID string `yaml:"id"`

	// Title is the act's display name.
	Title string `yaml:"title"`

	// Subtitle is the one-line framing shown under the title.
	Subtitle string `yaml:"subtitle"`

	// Levels are the level ids in this act, in the order they are played.
	Levels []string `yaml:"levels"`
}

// Rank is a title awarded once cumulative XP reaches MinXP.
type Rank struct {
	// ID is the rank's identifier.
	ID string `yaml:"id"`

	// Title is the rank's display name.
	Title string `yaml:"title"`

	// MinXP is the cumulative experience at which the rank is awarded.
	MinXP int `yaml:"min_xp"`
}

// Level returns the level with the given id, and whether it was found.
func (p *Pack) Level(id string) (*Level, bool) {
	for i := range p.Levels {
		if p.Levels[i].ID == id {
			return &p.Levels[i], true
		}
	}
	return nil, false
}

// Order returns every loaded level id in the order it should be played: acts
// in pack.yaml order, and within an act, an order that respects the
// prerequisite DAG.
//
// A level listed in an act but with no file yet is skipped, because that is
// the pack's normal state while it is being filled in. A level with a file
// but named by no act is appended after the acts, sorted by id, so that a
// level can never become invisible by being left out of pack.yaml.
//
// Order returns an error when the prerequisites form a cycle, naming the ids
// that are stuck. It is the only failure a caller has to handle here.
func (p *Pack) Order() ([]string, error) {
	loaded := make(map[string]*Level, len(p.Levels))
	for i := range p.Levels {
		loaded[p.Levels[i].ID] = &p.Levels[i]
	}

	// Campaign position, so a tie inside the DAG breaks toward the order
	// the author wrote rather than toward map iteration order.
	position := map[string]int{}
	var listed []string
	for _, act := range p.Acts {
		for _, id := range act.Levels {
			if _, ok := loaded[id]; !ok {
				continue // no file yet: a warning at validation time, not an error here
			}
			if _, dup := position[id]; dup {
				continue
			}
			position[id] = len(listed)
			listed = append(listed, id)
		}
	}

	var orphans []string
	for id := range loaded {
		if _, ok := position[id]; !ok {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	for _, id := range orphans {
		position[id] = len(listed)
		listed = append(listed, id)
	}

	return topoSort(listed, position, loaded)
}

// topoSort orders ids so that every prerequisite that is itself loaded comes
// first, breaking ties toward the campaign position.
//
// It is an iterative Kahn's algorithm rather than a recursive walk, so that a
// pathological pack cannot overflow the stack: a pack is untrusted content,
// and "the parser blew the stack" is a worse answer than "your prerequisites
// form a cycle".
func topoSort(ids []string, position map[string]int, loaded map[string]*Level) ([]string, error) {
	inDegree := make(map[string]int, len(ids))
	dependents := make(map[string][]string, len(ids))
	for _, id := range ids {
		inDegree[id] = 0
	}
	for _, id := range ids {
		for _, prereq := range loaded[id].Prerequisites {
			if _, ok := inDegree[prereq]; !ok {
				continue // dangling: reported by Validate, not fatal to ordering
			}
			inDegree[id]++
			dependents[prereq] = append(dependents[prereq], id)
		}
	}

	var ready []string
	for _, id := range ids {
		if inDegree[id] == 0 {
			ready = append(ready, id)
		}
	}
	byPosition := func(s []string) {
		sort.SliceStable(s, func(i, j int) bool { return position[s[i]] < position[s[j]] })
	}
	byPosition(ready)

	out := make([]string, 0, len(ids))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		out = append(out, id)

		var freed []string
		for _, dep := range dependents[id] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				freed = append(freed, dep)
			}
		}
		if len(freed) > 0 {
			ready = append(ready, freed...)
			byPosition(ready)
		}
	}

	if len(out) != len(ids) {
		var stuck []string
		for _, id := range ids {
			if inDegree[id] > 0 {
				stuck = append(stuck, id)
			}
		}
		sort.Strings(stuck)
		return nil, fmt.Errorf("prerequisites form a cycle among %v", stuck)
	}
	return out, nil
}
