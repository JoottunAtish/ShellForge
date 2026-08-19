package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/content/setup"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// What `run` can play, behind one interface.
//
// gameLevel is a level loaded from the YAML pack, which is every level there
// is. It was not always the only one: the Day 1 hardcoded demo sat beside it
// until #96 deleted it, and playable outlived the demo on purpose.
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

// gameLevel adapts a game.Session to playable.
type gameLevel struct {
	session *game.Session
	level   *content.Level
}

func (g *gameLevel) LevelID() string  { return g.session.LevelID() }
func (g *gameLevel) Root() string     { return g.session.Root() }
func (g *gameLevel) StateDir() string { return g.session.StateDir() }

func (g *gameLevel) Setup(ctx context.Context) error    { return g.session.Setup(ctx) }
func (g *gameLevel) Teardown(ctx context.Context) error { return g.session.Teardown(ctx) }

func (g *gameLevel) PrintBriefing(w io.Writer, color bool) {
	printBriefing(w, g.level, terminalWidth(w), color)
}

func (g *gameLevel) Responder(color bool) controlResponder {
	return &gameResponder{session: g.session, level: g.level, color: color}
}

// gameResponder answers control requests for a pack level.
type gameResponder struct {
	session *game.Session
	level   *content.Level
	color   bool
}

// Reply answers one request. The returned text is already CRLF terminated,
// because the shim prints it verbatim into a terminal held in raw mode.
func (r *gameResponder) Reply(ctx context.Context, verb, _ string) string {
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
		// Hints are Day 4: the ladder, the XP cost and the record of what a
		// learner has already spent all live with scoring and the progress
		// database. Answering with a fake free hint now would teach the wrong
		// thing about what a hint costs.
		return crlf("\n`hint` is not built yet. It arrives with scoring, which is Day 4 of the build plan.\n" +
			"The level's objectives are above; type `brief` to see them again.\n")

	case "reset":
		// reset is a recursive delete of a path that comes from level YAML. It
		// needs the validated destructive helper and the full refusal suite the
		// destructive-safety skill mandates, and it does not get done as a side
		// effect of wiring up `run`. The route below actually works today.
		return crlf(fmt.Sprintf("\n`reset` is not built yet. Type `exit`, then run `shellforge run %s` again.\n"+
			"Setup removes the level world before rebuilding it, so nothing is left over from this attempt.\n", r.level.ID))

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

	res, err := r.session.Check(checkCtx)
	if err != nil {
		return renderCheckError(err, r.level.ID)
	}
	return renderCheckReply(res, r.color)
}

// verifyLevelBudget is the engine's whole-run budget, which bounds one `check`.
//
// It reads the engine's own exported default rather than repeating 60 seconds
// here, so the two cannot drift.
func verifyLevelBudget() time.Duration { return verify.DefaultLevelTimeout }
