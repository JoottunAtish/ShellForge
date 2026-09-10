package main

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/content/setup"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/game/achievements"
	"github.com/JoottunAtish/ShellForge/internal/game/bus"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
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

	// sess is the sandbox session the level was built on, kept so the `next`
	// verb can drop the sentinel that tells the learner's shell to end. It is
	// borrowed, never closed here: the run flow that opened it owns it.
	sess runtime.Session

	// advanceRequested records that the learner typed `next` and the host
	// accepted, so the run flow can carry straight on to the following level
	// instead of asking a question the learner has already answered. Atomic
	// because the responder sets it on the control loop's goroutine and
	// runLevel reads it on its own, after the shell is gone.
	advanceRequested atomic.Bool

	// eventBus is the same bus buildLevel wired the session, the
	// orchestrator and the journal sink into. StartLive attaches a
	// LiveChecker to it: the checker's Notify fires from the CommandExecuted
	// events sink.Drain already publishes there, so wiring the live checker
	// needs no second path from a finished command to a verification pass.
	eventBus *bus.Bus

	// commandRan carries one signal per finished command from CommandRan,
	// the single goroutine draining mux.Events(), to StartLive's own drain
	// goroutine. Capacity 1 with a non-blocking send: CommandRan must never
	// block, because pty.Mux.emit drops events once its consumer falls
	// behind, and a burst of commands collapses to one pending drain rather
	// than a queue of them, which is fine because Drain is itself
	// incremental.
	commandRan chan struct{}
}

// AdvanceRequested reports that the learner asked for the next level with
// `next`, rather than simply leaving. See gameResponder's "next" case.
func (g *gameLevel) AdvanceRequested() bool { return g.advanceRequested.Load() }

func (g *gameLevel) LevelID() string  { return g.session.LevelID() }
func (g *gameLevel) Root() string     { return g.session.Root() }
func (g *gameLevel) StateDir() string { return g.session.StateDir() }

// Setup opens the attempt, which materializes the level's world on the way.
func (g *gameLevel) Setup(ctx context.Context) error { return g.orch.Start(ctx) }

// Teardown drains the last of the learner's commands, then closes the
// attempt, which removes the level's world on the way.
//
// The drain has to happen here as well as before each check, and it has to
// happen BEFORE Close. JournalSink.Drain's own contract says "before a check
// and once at teardown", and without the teardown half every command after
// the learner's last check is lost: from the events table, from
// commands_used, and from the achievements that count commands. A learner
// who never types check at all would record nothing whatsoever.
//
// Before Close, because Close is what reads the command count for the last
// time and writes it to the attempt, and because the Orchestrator stops
// counting the moment it is closed.
//
// Safe to call more than once and from any state, which is what lets the run
// flow defer it before Setup has run: Drain reports no error for a journal
// it cannot read, and Close is a no-op after the first.
func (g *gameLevel) Teardown(ctx context.Context) error {
	g.drainJournal(ctx)
	return g.orch.Close(ctx)
}

// drainJournal pulls the learner's commands out of the sandbox and onto the
// bus. See gameResponder.drainJournal for why the error is swallowed.
func (g *gameLevel) drainJournal(ctx context.Context) {
	if g.sink == nil {
		return
	}
	_ = g.sink.Drain(ctx, g.level.ID, g.orch.AttemptID())
}

// CommandRan reports that the PTY saw a command finish. It never blocks: it
// is called from the single goroutine draining mux.Events(), and
// pty.Mux.emit drops events once that consumer falls behind, so a call here
// that blocked would make CommandRan itself the reason events start being
// dropped. A non-blocking send into a capacity-1 channel is what keeps that
// true: a notify arriving while one is already pending is coalesced into it,
// never queued, because the drain that is about to run picks up every
// record Collector.Since has seen so far regardless of how many commands
// asked for it.
func (g *gameLevel) CommandRan() {
	select {
	case g.commandRan <- struct{}{}:
	default:
	}
}

// StartLive starts the level's live verification, bounded by ctx, and
// returns the channel a live checker reports transitions on, plus a wait
// function that blocks until both goroutines below have returned.
//
// Two goroutines, each owning one hop of the path from a finished command to
// a printed transition, and neither ever touching the sandbox on the
// goroutine that would block something upstream of it:
//
//   - The drain goroutine owns commandRan and calls the same drainJournal a
//     `check` reply already used. Draining publishes a CommandExecuted per
//     journal record onto eventBus, which is the same bus the live checker
//     below is attached to, so a finished command reaches the checker with
//     no second path invented for it.
//   - live.Run owns the pass loop until ctx is done. Its own bus subscriber,
//     registered by Attach, does nothing but a non-blocking Notify: the
//     handler runs synchronously on the goroutine that called Drain, so if
//     it did anything slower it would stall the drain behind a sandbox round
//     trip.
//
// wait exists because ctx being done only asks these two to stop; it does
// not confirm they have. The drain goroutine can be in the middle of
// drainJournal, appending to a store the caller is about to close, at the
// exact moment ctx is cancelled, and cancellation does not wait for that
// call to return. play calls wait before its own teardown for exactly that
// reason: see the comment on liveStopped in cmd_run.go.
//
// Detaching the checker's subscription is tied to Run's own return, which
// only happens once ctx is done, so a level whose live checking was never
// started (opts.live off, or a playable that predates it) never reaches this
// method at all: see startLiveChecking in cmd_run.go.
func (g *gameLevel) StartLive(ctx context.Context) (transitions <-chan []verify.ObjectiveResult, wait func()) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-g.commandRan:
				g.drainJournal(ctx)
			}
		}
	}()

	live := game.NewLiveChecker(g.orch, game.DefaultLiveDebounce)
	detach := live.Attach(g.eventBus)
	go func() {
		defer wg.Done()
		live.Run(ctx)
		detach()
	}()

	return live.Transitions(), wg.Wait
}

func (g *gameLevel) PrintBriefing(w io.Writer, color bool) {
	printBriefing(w, g.level, terminalWidth(w), color)
}

func (g *gameLevel) Responder(color bool) controlResponder {
	return &gameResponder{
		checker:  g.orch,
		hints:    g.orch,
		resets:   g.orch,
		level:    g.level,
		sink:     g.sink,
		orch:     g.orch,
		pass:     g.pass,
		color:    color,
		sess:     g.sess,
		stateDir: g.StateDir(),
		advance:  &g.advanceRequested,
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

	// sess, stateDir and advance are what the `next` verb needs to end the
	// learner's shell and have the run flow pick up the following level. All
	// three are nil or empty in a test that drives the responder without a
	// sandbox, in which case `next` degrades to telling the learner what to
	// type rather than doing it for them.
	sess     runtime.Session
	stateDir string
	advance  *atomic.Bool
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

	case "next":
		return crlf(r.next(ctx))

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
// waiting for them. gameLevel.Teardown does the same once more on the way
// out, so the tail after the last check is not lost.
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

	// advance says `play` is driving and will ask whether to start the next
	// level once this shell exits, which is the only thing the banner's
	// closing instruction needs to know. See nextStep.Offered.
	advance bool
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
		Next:       r.nextStep(ctx),
	}, r.color), true
}

// advanceSentinel is the file the host drops in the level's state directory
// to tell the learner's shell to end, so the run flow can start the level
// after this one. The `next` function in images/rc/instrument.bash is what
// reads it. The name is a compile-time constant joined to a state directory
// this program chose: nothing from a level pack reaches this path.
const advanceSentinel = "advance"

// next answers `next`, typed at the prompt inside a level, and where it can,
// it does the thing rather than explaining how to do it.
//
// Two outcomes, and which one a learner gets is decided here rather than in
// the sandbox:
//
//   - The level is passed and `play` is driving, so there is somewhere to go
//     and something to take them there. Drop the sentinel, record that this
//     was asked for, and say which level is loading. The shell ends itself a
//     moment later.
//   - Anything else: `run` is driving and will not carry on, or the level is
//     not passed yet, or there is no sandbox behind this responder at all.
//     Say what to do instead. Nothing ends, because a learner in the middle
//     of an unsolved level who is thrown out of it has lost work they did
//     not agree to lose.
//
// The sentinel is written BEFORE this reply is, and the ordering is what
// makes it race free rather than merely usually right: the shell reads the
// file only after this reply has been printed, so a sentinel written first
// is always there to be found.
//
// A failed write is not reported as a failure. It degrades to the same
// instructions the second outcome gives, which are true whether or not the
// automatic path worked, and a learner who has just passed a level should
// not be handed a sandbox error because a `touch` did not land.
func (r *gameResponder) next(ctx context.Context) string {
	step := r.nextStep(ctx)

	if !r.canAdvance(step) {
		return "\n" + renderNextStep(step) + "\n"
	}
	if err := r.armAdvance(ctx); err != nil {
		return "\n" + renderNextStep(step) + "\n"
	}

	r.advance.Store(true)
	return fmt.Sprintf("\nLoading %s, %s.\n", step.LevelID, step.Title)
}

// canAdvance reports whether `next` can carry the learner there itself.
//
// Passed is read from the Orchestrator, so it is a fact about this session:
// a learner replaying a level they passed last week has to pass it again
// before `next` will move them on, which is the same rule offerNextLevel
// follows for the same reason.
func (r *gameResponder) canAdvance(step nextStep) bool {
	switch {
	case step.LevelID == "", !step.Offered:
		return false
	case r.sess == nil, r.stateDir == "", r.advance == nil:
		return false
	case r.orch == nil, !r.orch.Passed():
		return false
	}
	return true
}

// armAdvance drops the sentinel the learner's shell watches for.
//
// argv form, with the path built from this program's own constants and the
// state directory it chose, never from level data: see the security skill's
// rules on spawning a process, and prepareControlChannel, which builds its
// FIFO paths the same way and removes a stale sentinel at level start.
func (r *gameResponder) armAdvance(ctx context.Context) error {
	res, err := r.sess.Exec(ctx, []string{"touch", "--", path.Join(r.stateDir, advanceSentinel)}, runtime.ExecOpts{})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("touch exited %d", res.ExitCode)
	}
	return nil
}

// nextStep works out which level the learner should play once this one is
// over, by the same route `shellforge play` takes: read the recorded level
// states, resolve the curriculum, ask for the next unlocked node.
//
// It is called from the pass banner, which runs AFTER Orchestrator.Check has
// recorded this level as passed, so the level just finished is already out
// of the running and the answer is genuinely the next one.
//
// Every failure degrades to the zero value rather than being reported. This
// decorates a message; a learner who has just passed a level must not be
// handed an error instead of their score because a read went wrong, and the
// banner says how to carry on either way, just without naming where to. That
// matches xpOf in cmd_play.go, which makes the same call for the same
// reason.
func (r *gameResponder) nextStep(ctx context.Context) nextStep {
	if r.pass == nil {
		return nextStep{}
	}

	// Offered is set on every path out from here, the failures included: it
	// describes what the host is about to do once this shell exits, which
	// does not stop being true because a store read went wrong.
	degraded := nextStep{Offered: r.pass.advance}

	states, err := r.pass.store.LevelStates(ctx, r.pass.profileID, r.pass.pack.ID)
	if err != nil {
		return degraded
	}
	nodes, err := game.Resolve(r.pass.pack, states)
	if err != nil {
		return degraded
	}

	next, ok := game.Next(nodes)
	if !ok {
		return nextStep{Complete: true}
	}
	lvl, found := r.pass.pack.Level(next.LevelID)
	if !found {
		return degraded
	}
	return nextStep{LevelID: lvl.ID, Title: lvl.Title, Offered: r.pass.advance}
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
