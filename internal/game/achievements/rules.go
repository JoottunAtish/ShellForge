package achievements

import (
	"math"
	"path"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game/bus"
)

// The fourteen achievement keys, fixed by SESSION-PROMPTS Day 4 Session G
// item 7.
//
// Issue #127 is titled "ten achievements" and then lists these fourteen,
// because act_clear_1 through act_clear_6 is six keys rather than one. The
// table is authoritative and the count in the title is not.
const (
	KeyFirstBlood       = "first_blood"
	KeyTabMaster        = "tab_master"
	KeyManualLabour     = "manual_labour"
	KeyNoHints          = "no_hints"
	KeyOneLiner         = "one_liner"
	KeyRmSurvivor       = "rm_survivor"
	KeyNightOwl         = "night_owl"
	KeyCampaignComplete = "campaign_complete"

	// KeyActClearPrefix plus a 1-based act number, for act_clear_1 through
	// act_clear_6.
	KeyActClearPrefix = "act_clear_"
)

// The thresholds, in one block so a balance change is one edit.
const (
	tabMasterTabs     = 25
	manualLabourRuns  = 20
	oneLinerLevels    = 5
	nightOwlFromHour  = 2
	nightOwlUntilHour = 5

	// maxTrackedLevels is how many levels a set-shaped counter can hold.
	//
	// A set is stored as one bit per level inside the float64 that backs
	// the REAL progress column, and a float64 holds whole numbers exactly
	// only below 2^53. Fifty-two bits is comfortably inside that with a bit
	// to spare. A pack larger than this makes the affected rules refuse to
	// track anything rather than silently lose a level, because an
	// achievement that unlocks at the wrong moment is worse than one that
	// does not unlock.
	maxTrackedLevels = 52

	// The no_hints counter packs three things into one number: which act is
	// being tracked, whether a hint has spoiled it, and which of its levels
	// have been passed. See noHintsEncode.
	noHintsMaskBits = 24
	noHintsPoison   = 1 << noHintsMaskBits
	noHintsSlotUnit = noHintsPoison << 1
)

// packIndex is the pack, reduced to the lookups the rules need.
type packIndex struct {
	// levelSlot is a level id to its bit position in a whole-pack set.
	levelSlot map[string]int

	// levels is how many levels the pack has.
	levels int

	// par is a level id to its authored par, zero when it declares none.
	par map[string]int

	// actOfLevel is a level id to its act id.
	actOfLevel map[string]string

	// actSlotOfLevel is a level id to its bit position within its own act.
	actSlotOfLevel map[string]int

	// actLevels is an act id to how many levels it declares that the pack
	// actually loaded.
	actLevels map[string]int

	// actOrder is the act ids in declaration order, which is what turns
	// act_clear_1 into a particular act.
	actOrder []string
}

// indexPack reduces pack to the lookups the rules need. A nil pack yields
// an empty index, and every rule that consults one degrades to never
// unlocking rather than guessing.
func indexPack(pack *content.Pack) *packIndex {
	idx := &packIndex{
		levelSlot:      make(map[string]int),
		par:            make(map[string]int),
		actOfLevel:     make(map[string]string),
		actSlotOfLevel: make(map[string]int),
		actLevels:      make(map[string]int),
	}
	if pack == nil {
		return idx
	}

	// Order is the campaign order, which is also a stable slot assignment.
	// Its only error is a prerequisite cycle; on one, fall back to load
	// order, because a malformed pack must not cost the learner a badge.
	order, err := pack.Order()
	if err != nil {
		order = make([]string, 0, len(pack.Levels))
		for i := range pack.Levels {
			order = append(order, pack.Levels[i].ID)
		}
	}
	for i, id := range order {
		idx.levelSlot[id] = i
	}
	idx.levels = len(order)

	loaded := make(map[string]bool, len(order))
	for _, id := range order {
		loaded[id] = true
	}

	for i := range pack.Levels {
		lvl := &pack.Levels[i]
		idx.par[lvl.ID] = lvl.ParCommands
	}

	for _, act := range pack.Acts {
		idx.actOrder = append(idx.actOrder, act.ID)
		slot := 0
		for _, id := range act.Levels {
			// A level listed in an act with no file yet is skipped, the
			// same way Pack.Order skips it: an act cannot be "cleared" by
			// passing levels that do not exist, and it must not be
			// unclearable because of levels that do not exist either.
			if !loaded[id] {
				continue
			}
			idx.actOfLevel[id] = act.ID
			idx.actSlotOfLevel[id] = slot
			slot++
		}
		idx.actLevels[act.ID] = slot
	}

	return idx
}

// RulesFor returns every rule, bound to pack.
//
// The order is the order they are announced in, and the order Unlocked
// reports them: the level-shaped ones first, then the act clears, then the
// campaign.
func RulesFor(pack *content.Pack) []Rule {
	idx := indexPack(pack)

	rules := []Rule{
		{
			Key:         KeyFirstBlood,
			Title:       "First Blood",
			Description: "Pass your first level.",
			Progress: func(ev bus.Event, counter float64) (float64, bool) {
				if _, ok := ev.(bus.LevelPassed); !ok {
					return counter, false
				}
				return 1, true
			},
		},
		{
			Key:         KeyTabMaster,
			Title:       "Tab Master",
			Description: "Use tab completion 25 times.",
			Progress: func(ev bus.Event, counter float64) (float64, bool) {
				cmd, ok := ev.(bus.CommandExecuted)
				if !ok || !cmd.UsedTab {
					return counter, false
				}
				next := counter + 1
				return next, next >= tabMasterTabs
			},
		},
		{
			Key:         KeyManualLabour,
			Title:       "Manual Labour",
			Description: "Read the manual 20 times.",
			Progress: func(ev bus.Event, counter float64) (float64, bool) {
				cmd, ok := ev.(bus.CommandExecuted)
				if !ok || commandName(cmd.Raw) != "man" {
					return counter, false
				}
				next := counter + 1
				return next, next >= manualLabourRuns
			},
		},
		{
			Key:         KeyOneLiner,
			Title:       "One Liner",
			Description: "Pass five levels at or under par.",
			Progress: func(ev bus.Event, counter float64) (float64, bool) {
				passed, ok := ev.(bus.LevelPassed)
				if !ok || idx.levels == 0 || idx.levels > maxTrackedLevels {
					return counter, false
				}
				par := idx.par[passed.LevelID]
				if par <= 0 || passed.CommandsUsed > par {
					return counter, false
				}
				slot, known := idx.levelSlot[passed.LevelID]
				if !known {
					return counter, false
				}
				next := setBit(counter, slot)
				return next, popcount(next) >= oneLinerLevels
			},
		},
		{
			Key:         KeyRmSurvivor,
			Title:       "rm Survivor",
			Description: "Reset a level you had broken, then pass it.",
			Progress: func(ev bus.Event, counter float64) (float64, bool) {
				// The name tells a story about recovery, not about
				// destruction: you broke it, you rebuilt it, you finished
				// it. So it takes a reset AND the pass that follows, on the
				// same level.
				switch e := ev.(type) {
				case bus.LevelReset:
					slot, known := idx.levelSlot[e.LevelID]
					if !known {
						return counter, false
					}
					return float64(slot + 1), false
				case bus.LevelPassed:
					slot, known := idx.levelSlot[e.LevelID]
					if !known {
						return counter, false
					}
					return counter, counter == float64(slot+1)
				default:
					return counter, false
				}
			},
		},
		{
			Key:         KeyNightOwl,
			Title:       "Night Owl",
			Description: "Pass a level between 02:00 and 05:00.",
			Progress: func(ev bus.Event, counter float64) (float64, bool) {
				passed, ok := ev.(bus.LevelPassed)
				if !ok {
					return counter, false
				}
				// The window is half open: 02:00 counts, 05:00 does not.
				// The hour comes from the event, which the orchestrator
				// stamps from its own injectable clock, so a test sets the
				// time without touching the system's.
				hour := passed.At.Hour()
				return counter, hour >= nightOwlFromHour && hour < nightOwlUntilHour
			},
		},
		{
			Key:         KeyNoHints,
			Title:       "No Hints",
			Description: "Clear a whole act without taking a single hint.",
			Progress: func(ev bus.Event, counter float64) (float64, bool) {
				return noHintsProgress(idx, ev, counter)
			},
		},
	}

	for i, actID := range idx.actOrder {
		actID := actID
		rules = append(rules, Rule{
			Key:         KeyActClearPrefix + itoa(i+1),
			Title:       "Act " + itoa(i+1) + " Cleared",
			Description: "Pass every level in act " + itoa(i+1) + ".",
			Progress: func(ev bus.Event, counter float64) (float64, bool) {
				passed, ok := ev.(bus.LevelPassed)
				if !ok || idx.actOfLevel[passed.LevelID] != actID {
					return counter, false
				}
				total := idx.actLevels[actID]
				if total == 0 || total > maxTrackedLevels {
					return counter, false
				}
				next := setBit(counter, idx.actSlotOfLevel[passed.LevelID])
				return next, popcount(next) >= total
			},
		})
	}

	rules = append(rules, Rule{
		Key:         KeyCampaignComplete,
		Title:       "Campaign Complete",
		Description: "Pass every level in the pack.",
		Progress: func(ev bus.Event, counter float64) (float64, bool) {
			passed, ok := ev.(bus.LevelPassed)
			if !ok || idx.levels == 0 || idx.levels > maxTrackedLevels {
				return counter, false
			}
			slot, known := idx.levelSlot[passed.LevelID]
			if !known {
				return counter, false
			}
			next := setBit(counter, slot)
			return next, popcount(next) >= idx.levels
		},
	})

	return rules
}

// noHintsProgress is the no_hints rule, extracted because its counter
// encoding needs more explaining than a literal in a slice can carry.
//
// One act is tracked at a time: the act of the last level the learner
// passed or took a hint in, which for anybody playing in campaign order is
// the act they are in. The counter packs three things:
//
//	slot     which act, 1-based into the pack's declared acts
//	poison   whether a hint has been taken in that act
//	mask     one bit per level of that act already passed
//
// A hint poisons the act it was taken in. Moving to a different act starts
// that act fresh, which is what makes the achievement re-earnable in
// principle and what keeps the encoding to one number.
func noHintsProgress(idx *packIndex, ev bus.Event, counter float64) (float64, bool) {
	var levelID string
	hinted := false

	switch e := ev.(type) {
	case bus.LevelPassed:
		levelID = e.LevelID
	case bus.HintTaken:
		levelID = e.LevelID
		hinted = true
	default:
		return counter, false
	}

	actID, known := idx.actOfLevel[levelID]
	if !known {
		return counter, false
	}
	slot := actSlot(idx, actID)
	if slot == 0 {
		return counter, false
	}
	total := idx.actLevels[actID]
	if total == 0 || total > noHintsMaskBits {
		return counter, false
	}

	curSlot, curPoisoned, mask := noHintsDecode(counter)
	if curSlot != slot {
		// A different act than the one being tracked: start it fresh.
		curPoisoned, mask = false, 0
	}

	if hinted {
		return noHintsEncode(slot, true, 0), false
	}
	if curPoisoned {
		return noHintsEncode(slot, true, 0), false
	}

	mask = setBit(mask, idx.actSlotOfLevel[levelID])
	return noHintsEncode(slot, false, mask), popcount(mask) >= total
}

// actSlot is the 1-based position of actID in the pack's declared acts, or
// zero when the pack does not declare it.
func actSlot(idx *packIndex, actID string) int {
	for i, id := range idx.actOrder {
		if id == actID {
			return i + 1
		}
	}
	return 0
}

func noHintsEncode(slot int, poisoned bool, mask float64) float64 {
	v := float64(slot)*noHintsSlotUnit + mask
	if poisoned {
		v += noHintsPoison
	}
	return v
}

func noHintsDecode(counter float64) (slot int, poisoned bool, mask float64) {
	if counter <= 0 {
		return 0, false, 0
	}
	n := int64(counter)
	slot = int(n / noHintsSlotUnit)
	rest := n % noHintsSlotUnit
	if rest >= noHintsPoison {
		poisoned = true
		rest -= noHintsPoison
	}
	return slot, poisoned, float64(rest)
}

// setBit returns mask with bit n set. Bits above maxTrackedLevels are
// refused rather than wrapped: see maxTrackedLevels.
func setBit(mask float64, n int) float64 {
	if n < 0 || n >= maxTrackedLevels {
		return mask
	}
	return float64(int64(mask) | (int64(1) << uint(n)))
}

// popcount counts the set bits of a mask held in a float64.
func popcount(mask float64) int {
	if mask <= 0 || math.IsInf(mask, 0) || math.IsNaN(mask) {
		return 0
	}
	n := int64(mask)
	count := 0
	for n != 0 {
		n &= n - 1
		count++
	}
	return count
}

// commandName is the program a command line ran, or the empty string when
// there is not one.
//
// It exists so manual_labour counts `man` as a command the learner ran
// rather than as a substring: mandatory_backup.sh must not count, and
// `sudo man ls` must. Any leading sudo is stepped over, and a path is
// reduced to its last element so /usr/bin/man counts too.
//
// raw is secret material. It is read here, compared, and discarded. Nothing
// derived from it is returned to a caller that stores anything, and the
// caller stores only a count.
func commandName(raw string) string {
	fields := strings.Fields(raw)
	for len(fields) > 0 {
		first := fields[0]
		if first == "sudo" {
			fields = fields[1:]
			continue
		}
		// An environment assignment prefix, as in `LANG=C man ls`.
		if i := strings.IndexByte(first, '='); i > 0 && !strings.ContainsAny(first[:i], "/ ") {
			fields = fields[1:]
			continue
		}
		return path.Base(first)
	}
	return ""
}

// itoa is strconv.Itoa for the small positive numbers act keys are built
// from, kept local so rules.go does not import strconv for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
