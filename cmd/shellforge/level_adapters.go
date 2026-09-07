package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/content/setup"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/game/achievements"
	"github.com/JoottunAtish/ShellForge/internal/store"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// What `run` and `play` can play, behind one interface.
//
// gameLevel is a level loaded from the YAML pack, which is every level
// there is. It was not always the only one: the Day 1 hardcoded demo sat
// beside it until #96 deleted it, and playable outlived the demo on
// purpose.
//
// Keeping the flow behind an interface is what stops the FIFO plumbing, the
// teardown ordering and the raw-mode rules from existing twice. Those are
// exactly the parts that were hard to get right, and a second copy would
// drift, so the next thing `run` has to play adapts to playable rather than
// forking the flow.

// setupStateDir returns SF_STATE for a run.
//
// It is a function rather than a constant so there is one place to change if a
// level ever needs its own, and it deliberately returns the same value the setup
// runner defaults to: the runner, the shell instrumentation and the control
// channel all have to agree on this path or `check` cannot find its FIFOs.
func setupStateDir() string { return setup.DefaultStateDir }

// --- a level from the pack ---

// gameLevel adapts a game.Orchestrator to playable.
//
// The Orchestrator, not the Session underneath it, and that is the whole
// Day 4 change here: Setup and Teardown are Start and Close, so an attempt
// is opened and closed around every level anybody plays, `check` scores,
// `hint` spends, `reset` rebuilds, and the achievements subscribed to the
// bus see it all.
//
// `run <level-id>` goes through this too, not only `play`. Issue #129
// sketches progress recording as something `play` adds around a `run` that
// does not do it; that turns out not to be separable, because a hint costs
// XP and a `run` that quietly discarded a hint the learner had paid for
// would be worse than one that records it. One path, no drift.
type gameLevel struct {
	orch    *game.Orchestrator
	session *game.Session
	level   *content.Level
	sink    *game.JournalSink
	pass    *passContext
}

func (g *gameLevel) LevelID() string  { return g.session.LevelID() }
func (g *gameLevel) Root() string     { return g.session.Root() }
func (g *gameLevel) StateDir() string { return g.session.StateDir() }

// Setup opens the attempt, which materializes the level's world on the way.
func (g *gameLevel) Setup(ctx context.Context) error { return g.orch.Start(ctx) }

// Teardown closes the attempt, which removes the level's world on the way.
// Safe to call more than once and from any state, which is what lets the
// run flow defer it before Setup has run.
func (g *gameLevel) Teardown(ctx context.Context) error { return g.orch.Close(ctx) }

func (g *gameLevel) PrintBriefing(w io.Writer, color bool) {
	printBriefing(w, g.level, terminalWidth(w), color)
}

func (g *gameLevel) Responder(color bool) controlResponder {
	return &gameResponder{
		checker: g.orch,
		hints:   g.orch,
		resets:  g.orch,
		level:   g.level,
		sink:    g.sink,
		orch:    g.orch,
		pass:    g.pass,
		color:   color,
	}
}

// levelChecker is the one method the check reply needs, declared here so
// the responder can be driven in a test by a game.Session directly, with no
// orchestrator, no progress database and no bus.
type levelChecker interface {
	Check(ctx context.Context) (verify.LevelResult, error)
}

// gameResponder answers control requests for a pack level.
type gameResponder struct {
	checker levelChecker
	hints   hinter
	resets  resetter
	level   *content.Level
	color   bool

	// sink drains the in-sandbox command journal. Nil in tests that do not
	// care about the command count.
	sink *game.JournalSink

	// orch is the attempt, for the id the journal records are stamped with
	// and for the score the banner shows. Nil in tests.
	orch *game.Orchestrator

	// pass is what the pass banner needs beyond the level itself. Nil when
	// there is no progress database, in which case a pass renders as an
	// ordinary check reply.
	pass *passContext
}

// Reply answers one request. The returned text is already CRLF terminated,
// because the shim prints it verbatim into a terminal held in raw mode.
func (r *gameResponder) Reply(ctx context.Context, verb, args string) string {
	switch verb {
	case "check":
		return r.check(ctx)

	case "brief":
		// Rendered with the same renderer AND the same checklist as the
		// pre-attach briefing, then put through crlf, because this one IS
		// printed into a raw-mode terminal. printObjectiveChecklist is the
		// only place report.txt-style filenames and paths named nowhere else
		// appear, and it is also what the hint verb below points a learner
		// back to; dropping it here is the one place both promises break at
		// once, worst on a boss level with no numbered steps to fall back on.
		var b strings.Builder
		b.WriteString("\n" + renderMarkdown(r.level.Briefing, defaultBriefWidth, r.color) + "\n")
		printObjectiveChecklist(&b, r.level)
		return crlf(b.String())

	case "hint":
		if r.hints == nil {
			return crlf("\n`hint` is not available in this session.\nType `brief` to see the objectives again.\n")
		}
		return crlf(renderHintReply(ctx, r.hints, args, r.color))

	case "reset":
		if r.resets == nil {
			return crlf(fmt.Sprintf("\n`reset` is not available in this session. Type `exit`, then run `shellforge run %s` again.\n", r.level.ID))
		}
		return crlf(renderResetReply(ctx, r.resets, r.level.Setup.Root, args))

	default:
		return crlf(fmt.Sprintf("\nunknown request %q\n", verb))
	}
}

// check runs the level's checks and renders the result.
//
// The context is bounded rather than passed through untouched. The host cannot
// see the learner's Ctrl-C, so it cannot cancel a run in flight; what it can do
// is stop working on a reply nobody will read. The engine's own level budget does
// the real bounding, and checkSlack is the margin that keeps the two from racing.
func (r *gameResponder) check(ctx context.Context) string {
	checkCtx, cancel := context.WithTimeout(ctx, verifyLevelBudget()+checkSlack)
	defer cancel()

	// Drained first, so the efficiency bonus counts the commands that led
	// to this check rather than the ones before the last one. Drain reports
	// no error for a journal it cannot read, which is the right degradation:
	// a lost journal costs a bonus, never a pass.
	r.drainJournal(checkCtx)

	res, err := r.checker.Check(checkCtx)
	if err != nil {
		return renderCheckError(err, r.level.ID)
	}

	if res.Passed {
		if banner, ok := r.passBanner(checkCtx, res); ok {
			return crlf(truncateReply(banner))
		}
	}
	return renderCheckReply(res, r.color)
}

// drainJournal pulls the learner's commands out of the sandbox and onto the
// bus, where the score's command count and two of the achievements are
// waiting for them.
func (r *gameResponder) drainJournal(ctx context.Context) {
	if r.sink == nil || r.orch == nil {
		return
	}
	// The error is a failure to WRITE the progress database, which Drain
	// documents as the only one it reports. It is not a reason to refuse
	// the learner a check, and it must not be rendered: an Entry cannot
	// redact its own command text, so nothing derived from a journal read
	// reaches the terminal.
	_ = r.sink.Drain(ctx, r.level.ID, r.orch.AttemptID())
}

// passContext is what the pass banner needs beyond the level and the check
// result: the pack, for ranks and achievement descriptions, and the store,
// for the XP total either side of this award.
type passContext struct {
	pack      *content.Pack
	store     *store.Store
	profileID int64
	unlocks   *unlockCollector
}

// passBanner assembles the pass banner. ok is false when there is not
// enough context to build one, in which case the caller falls back to the
// ordinary check reply rather than showing half a banner.
func (r *gameResponder) passBanner(ctx context.Context, res verify.LevelResult) (string, bool) {
	if r.pass == nil || r.orch == nil {
		return "", false
	}

	awarded := r.orch.Score()

	xpBefore, err := r.pass.store.TotalXP(ctx, r.pass.profileID, r.pass.pack.ID)
	if err != nil {
		return "", false
	}

	// The award is not banked until the attempt closes, and best_score
	// never falls, so the new total is the old one plus whatever this
	// attempt improved on. Computing it rather than re-reading it is what
	// lets the banner show the right number while the level is still open.
	previousBest := 0
	if st, ok, err := r.pass.store.LevelState(ctx, r.pass.profileID, r.level.ID, r.level.Version); err == nil && ok {
		previousBest = st.BestScore
	}
	gained := awarded.Total - previousBest
	if gained < 0 {
		gained = 0
	}
	xpAfter := xpBefore + gained

	rankBefore, _, hasRanks := game.RankFor(r.pass.pack, xpBefore)
	rankAfter, nextRank, _ := game.RankFor(r.pass.pack, xpAfter)

	return renderPassBanner(passSummaryData{
		Level:      r.level,
		Result:     res,
		Score:      awarded,
		TotalXP:    xpAfter,
		RankBefore: rankBefore,
		RankAfter:  rankAfter,
		NextRank:   nextRank,
		HasRanks:   hasRanks,
		Unlocked:   unlockedRules(r.pass.pack, r.pass.unlocks.drain()),
	}, r.color), true
}

// unlockedRules turns the keys the bus announced into the rules that hold
// the words a learner reads.
func unlockedRules(pack *content.Pack, keys []string) []achievements.Rule {
	if len(keys) == 0 {
		return nil
	}
	byKey := make(map[string]achievements.Rule)
	for _, rule := range achievements.RulesFor(pack) {
		byKey[rule.Key] = rule
	}

	out := make([]achievements.Rule, 0, len(keys))
	for _, key := range keys {
		if rule, ok := byKey[key]; ok {
			out = append(out, rule)
		}
	}
	return out
}

// unlockCollector records the achievements announced on the bus so the next
// pass banner can name them.
//
// It exists because an achievement can unlock at any moment during a level,
// on a command as easily as on a check, and the banner is the next thing
// the learner reads. Draining rather than reading is what stops the same
// badge being announced by two consecutive banners.
type unlockCollector struct {
	mu   sync.Mutex
	keys []string
}

func (c *unlockCollector) record(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = append(c.keys, key)
}

func (c *unlockCollector) drain() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := c.keys
	c.keys = nil
	return keys
}

// verifyLevelBudget is the engine's whole-run budget, which bounds one `check`.
//
// It reads the engine's own exported default rather than repeating 60 seconds
// here, so the two cannot drift.
func verifyLevelBudget() time.Duration { return verify.DefaultLevelTimeout }
