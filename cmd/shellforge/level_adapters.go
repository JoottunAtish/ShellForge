package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/content/setup"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/sandbox"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// The two things `run` can play, behind one interface.
//
// gameLevel is a level loaded from the YAML pack, which is every real level.
// demoLevel is the Day 1 hardcoded one, kept reachable until the golden harness
// has replaced the safety coverage its tests carry. See demoLevelID in
// cmd_run.go for the full reason and the follow-up.
//
// Keeping both behind playable is what stops the FIFO plumbing, the teardown
// ordering and the raw-mode rules from existing twice. Those are exactly the
// parts that were hard to get right, and a second copy would drift.

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
		// Rendered with the same renderer as the pre-attach briefing, then put
		// through crlf, because this one IS printed into a raw-mode terminal.
		return crlf("\n" + renderMarkdown(r.level.Briefing, defaultBriefWidth, r.color) + "\n")

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

// --- the Day 1 demo level ---

// demoLevel adapts internal/sandbox's hardcoded level to playable.
//
// It closes over the session because DemoLevel's own Setup, Teardown and Check
// each take one, which is the shape that predates game.Session.
type demoLevel struct {
	level sandbox.DemoLevel
	sess  runtime.Session
}

func (d *demoLevel) LevelID() string  { return d.level.ID }
func (d *demoLevel) Root() string     { return d.level.Root }
func (d *demoLevel) StateDir() string { return d.level.StateDir() }

func (d *demoLevel) Setup(ctx context.Context) error    { return d.level.Setup(ctx, d.sess) }
func (d *demoLevel) Teardown(ctx context.Context) error { return d.level.Teardown(ctx, d.sess) }

// PrintBriefing keeps the demo's original plain presentation.
//
// Deliberately not routed through the markdown renderer. The demo exists to be
// deleted, and its briefing is a Go string literal rather than authored level
// content, so rendering it would only create a second thing to keep working.
func (d *demoLevel) PrintBriefing(w io.Writer, _ bool) {
	fmt.Fprintf(w, "\n%s\n%s\n%s\n\n", briefingRule, d.level.Briefing, briefingRule)
}

func (d *demoLevel) Responder(_ bool) controlResponder {
	return &demoResponder{level: d.level, sess: d.sess}
}

// demoResponder is the Day 1 control logic, unchanged.
type demoResponder struct {
	level sandbox.DemoLevel
	sess  runtime.Session
}

func (r *demoResponder) Reply(ctx context.Context, verb, _ string) string {
	switch verb {
	case "check":
		passed, message, err := r.level.Check(ctx, r.sess)
		if err != nil {
			return fmt.Sprintf("check could not run: %v\n", err)
		}
		if passed {
			return fmt.Sprintf("PASS: %s\n", message)
		}
		return fmt.Sprintf("FAIL: %s\n", message)
	case "brief":
		return r.level.Briefing + "\n"
	case "hint", "reset":
		return fmt.Sprintf("`%s` is not built yet. It lands later in the build plan; see PROGRESS.md.\n", verb)
	default:
		return fmt.Sprintf("unknown request %q\n", verb)
	}
}

// verifyLevelBudget is the engine's whole-run budget, which bounds one `check`.
//
// It reads the engine's own exported default rather than repeating 60 seconds
// here, so the two cannot drift.
func verifyLevelBudget() time.Duration { return verify.DefaultLevelTimeout }
