package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// `play`: the command a learner actually types.
//
//	shellforge play                 resume at the next unlocked level
//	shellforge play <level-id>      replay a specific level, if it is unlocked
//	shellforge play --next          print which level is next and exit
//
// It goes last of the Day 4 work because it depends on almost everything
// else: the orchestrator for the state machine, the curriculum for Next,
// the store for what is passed, scoring for the award, and the renderers
// for the banner.
//
// It carries on to the next level once one is passed, but only after asking,
// and the asking is the whole design rather than a politeness.
//
// The reason this was deferred for so long was that silently provisioning
// the next level makes Ctrl-C ambiguous. Inside a level the host terminal is
// in raw mode, so Ctrl-C is byte 0x03 forwarded to the sandbox and belongs
// to bash: it interrupts the learner's own command and nothing else, which
// is what non-negotiable 1 requires. If the game then chained straight into
// provisioning the next level, there would be a window where the same
// keystroke means something else entirely, "stop the game", with nothing on
// screen marking where one meaning ended and the other began. A learner who
// mashed it would not be able to say what they had just cancelled, and nor
// could we.
//
// Asking removes the ambiguity rather than working around it. The question
// is printed on the host, after the shell has exited and the terminal is
// back in cooked mode, so at that prompt Ctrl-C is the ordinary interrupt
// every other command-line program gives it: it stops the game, cleanly,
// with no level open and nothing half provisioned. Before the prompt, Ctrl-C
// is the sandbox's. After the answer, everything that happens was explicitly
// asked for. There is no third state.
//
// Nothing about `exit` changes either, which is the other half of it. It
// still means what it means in every shell, "leave this shell", and it does
// not quietly become a game verb meaning "next level, please". The decision
// is made by answering a question, not by overloading a builtin.
//
// See offerNextLevel.

// playOptions is what `play` parsed out of its arguments.
type playOptions struct {
	// LevelID names a specific level to replay. Empty means resume.
	LevelID string

	// DryRun is --next: say which level is next and provision nothing.
	DryRun bool

	// Debug is --log-level=debug, threaded into the shared run flow.
	Debug bool

	// Live is --live-check, threaded into the shared run flow. Defaults to
	// on, matching `run`.
	Live bool

	// In is where the answer to "carry on to the next level?" is read from.
	// Nil means os.Stdin, which is what the command itself passes; a test
	// supplies its own.
	In io.Reader
}

// newPlayCommand returns `shellforge play`.
func newPlayCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "play [level-id] [--next] [--log-level=debug] [--live-check=off]",
		GroupID:            groupPlaying,
		Short:              "Start, or resume at the next level",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := parsePlayArgs(args)
			if err != nil {
				return err
			}
			opts.In = os.Stdin
			return runPlay(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}
}

// parsePlayArgs reads the level id and the two flags `play` understands.
//
// Hand rolled under cobra, the same way `run` is and for the same reason:
// the flags are few, the errors are the learner's to read, and cobra's own
// "unknown flag" wording is not the voice the rest of this program speaks
// in.
func parsePlayArgs(args []string) (playOptions, error) {
	opts := playOptions{Live: true}

	badFlag := func(a string) error {
		return ux.Fail(
			fmt.Sprintf("understand the option %q", a),
			nil,
			"Run `shellforge help play` for the usage. The options are --next, --log-level=debug and --live-check=off.",
			"",
		)
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--next":
			opts.DryRun = true
		case a == "--log-level":
			if i+1 >= len(args) {
				return opts, badFlag(a)
			}
			i++
			opts.Debug = args[i] == "debug"
		case strings.HasPrefix(a, "--log-level="):
			opts.Debug = strings.TrimPrefix(a, "--log-level=") == "debug"
		case a == "--live-check":
			if i+1 >= len(args) {
				return opts, badFlag(a)
			}
			i++
			live, ok := parseLiveCheckValue(args[i])
			if !ok {
				return opts, badFlag(a + " " + args[i])
			}
			opts.Live = live
		case strings.HasPrefix(a, "--live-check="):
			live, ok := parseLiveCheckValue(strings.TrimPrefix(a, "--live-check="))
			if !ok {
				return opts, badFlag(a)
			}
			opts.Live = live
		case strings.HasPrefix(a, "-"):
			return opts, badFlag(a)
		case opts.LevelID != "":
			return opts, ux.Fail(
				"work out which level to play",
				nil,
				fmt.Sprintf("You named more than one level (%q and %q). Name one of them, or run `shellforge play` on its own to carry on where you left off.", opts.LevelID, a),
				"",
			)
		default:
			opts.LevelID = a
		}
	}
	return opts, nil
}

// runPlay resolves the level to play, plays it, and offers the one after it.
//
// The order matters and is pinned by a test: the pack, the database and the
// choice of level all happen BEFORE anything slow, so a learner who
// expected a different level can read the reason and press Ctrl-C rather
// than wait several minutes for a container they did not want. That holds on
// every pass round the loop, not only the first, which is the point of
// re-resolving from the store each time rather than walking a list decided
// up front: the learner's progress changed while they were playing, and the
// campaign is a DAG, so what comes next is a question to be asked again, not
// an index to increment.
func runPlay(ctx context.Context, out io.Writer, opts playOptions) error {
	pack, err := content.Embedded()
	if err != nil {
		return err // Embedded already wraps its own failure as a *ux.Error
	}

	st, profile, err := openProgress(ctx)
	if err != nil {
		return err
	}
	defer st.Close()

	// Consumed by the first pass only. `play <level-id>` names one level;
	// what the loop would go on to after it is the resume order, which the
	// learner did not ask for, so the loop stops there instead.
	levelID := opts.LevelID

	for {
		states, err := st.LevelStates(ctx, profile.ID, pack.ID)
		if err != nil {
			return ux.Fail("read your recorded progress", err, remediationRunDoctor, "")
		}

		nodes, err := game.Resolve(pack, states)
		if err != nil {
			return ux.Fail("resolve the campaign map", err, remediationPackCycle, docAnchorPackInvalid)
		}

		choice, reason, err := chooseLevel(pack, nodes, levelID)
		if err != nil {
			return err
		}

		if choice == nil {
			// A complete campaign is an ending, not an error.
			fmt.Fprint(out, renderCampaignComplete(pack, nodes, xpOf(ctx, st, profile.ID, pack.ID)))
			return nil
		}

		fmt.Fprintf(out, "Next: %s, %s.\n", choice.ID, choice.Title)
		if reason != "" {
			// Empty for a level that is in the pack but listed in no act, which
			// a half-written pack produces. Printing the blank line anyway would
			// look like something failed to render.
			fmt.Fprintln(out, reason)
		}
		if opts.DryRun {
			return nil
		}

		if err := checkInteractiveShellSupported(choice.ID); err != nil {
			return err
		}

		// Two reasons not to carry on, and they are one condition rather
		// than two because they mean the same thing here. `play <level-id>`
		// names one level and stops after it, so nothing offers to carry on
		// from a level the learner chose by hand. And a stdin that is not a
		// terminal has nobody behind it to answer, so the question must not
		// be asked, which also means it must not be read from: readLine
		// would take a line of somebody else's input.
		advance := levelID == "" && canAsk(opts.In)
		outcome, err := runLevel(ctx, runOptions{
			levelID: choice.ID,
			debug:   opts.Debug,
			live:    opts.Live,
			advance: advance,
		}, pack, choice)
		if err != nil {
			return err
		}

		if !outcome.Passed || !advance {
			return nil
		}

		// A learner who typed `next` said what they wanted while the shell
		// was still up, so asking again here would be asking them to say it
		// twice. Everything else goes through the question, including the
		// learner who simply typed `exit`.
		if !outcome.Advanced && !offerNextLevel(opts.In, out) {
			return nil
		}
		levelID = ""
	}
}

// offerNextLevel asks whether to carry on, and reports the answer.
//
// This question is the whole of the Ctrl-C fix, so where it is asked matters
// as much as that it is asked. By the time it prints, internal/pty has
// restored the host terminal out of raw mode and the sandbox shell is gone,
// so Ctrl-C here is the ordinary interrupt it is in every other command-line
// program: it ends the game with no level open and nothing half provisioned.
// Inside a level it was the sandbox's, and it still is. There is no moment
// where it is neither.
//
// Enter means yes, because a learner working through the campaign presses it
// far more often than they stop, and because the alternative to answering is
// always available and never destructive. Anything else, including EOF from
// a closed stdin, means no: this provisions a container and a learner who
// did not answer did not ask for one.
func offerNextLevel(in io.Reader, out io.Writer) bool {
	fmt.Fprint(out, "\nCarry on to the next level? [Y/n] ")

	line, ok := readLine(in)
	if !ok {
		// EOF with nothing typed leaves the cursor at the end of the
		// question. The newline is what stops the shell prompt that follows
		// from landing on it.
		fmt.Fprintln(out)
		return false
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	}
	return false
}

// readLine reads one line from in, one byte at a time, and reports whether
// it read anything at all before EOF.
//
// A byte at a time rather than a bufio.Scanner, which is what the sandbox
// confirmation in cmd_sandbox.go uses, and the difference is not stylistic.
// A Scanner reads ahead by up to its whole buffer, and this stdin is not
// finished with: the very next thing that reads it is internal/pty, handing
// the learner's keystrokes to the next level's shell. Anything buffered here
// would be swallowed, so a learner who typed ahead would lose it. Reading to
// the newline and no further leaves the rest where it belongs. cmd_sandbox.go
// can afford a Scanner because nothing reads that stdin again.
func readLine(in io.Reader) (string, bool) {
	if in == nil {
		return "", false
	}

	var (
		b   strings.Builder
		buf [1]byte
		any bool
	)
	for {
		n, err := in.Read(buf[:])
		if n > 0 {
			any = true
			if buf[0] == '\n' {
				return strings.TrimSuffix(b.String(), "\r"), true
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			return strings.TrimSuffix(b.String(), "\r"), any
		}
	}
}

// canAsk reports whether there is somebody at the other end of in to answer
// the question.
//
// A stdin that is not a terminal is a script, a pipe, or CI, and a question
// nobody can answer must not be asked: `play` then does exactly what it did
// before any of this, which is to play one level and return. A nil reader is
// the same answer for the same reason.
func canAsk(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// chooseLevel decides which level to play and why.
//
// A nil level with a nil error means the campaign is complete. An explicit
// id that is locked is refused by naming the prerequisites that are not
// met, because "locked" on its own tells a learner nothing they can act on.
func chooseLevel(pack *content.Pack, nodes []game.Node, levelID string) (*content.Level, string, error) {
	if levelID == "" {
		next, ok := game.Next(nodes)
		if !ok {
			return nil, "", nil
		}
		lvl, found := pack.Level(next.LevelID)
		if !found {
			// Resolve only ever returns ids it resolved to a loaded level,
			// so this is unreachable. Reporting it beats a nil dereference.
			return nil, "", unknownLevel(next.LevelID, levelOrder(pack))
		}
		return lvl, positionReason(pack, nodes, next), nil
	}

	lvl, found := pack.Level(levelID)
	if !found {
		return nil, "", unknownLevel(levelID, levelOrder(pack))
	}

	for _, n := range nodes {
		if n.LevelID != levelID {
			continue
		}
		if n.Availability == game.AvailableLocked {
			return nil, "", ux.Fail(
				fmt.Sprintf("play level %q", levelID),
				nil,
				fmt.Sprintf("That level is locked until you have passed or skipped %s. Run `shellforge map` to see the campaign, or `shellforge play` to carry on where you left off.",
					strings.Join(n.BlockedBy, " and ")),
				docAnchorLevelNotFound,
			)
		}
		return lvl, positionReason(pack, nodes, n), nil
	}
	return lvl, "", nil
}

// positionReason says where a level sits, so the learner can tell at a
// glance whether it is the one they expected.
func positionReason(pack *content.Pack, nodes []game.Node, node game.Node) string {
	position, total := 0, 0
	for _, n := range nodes {
		if n.ActID != node.ActID {
			continue
		}
		total++
		if n.LevelID == node.LevelID {
			position = total
		}
	}

	actTitle := node.ActID
	for _, act := range pack.Acts {
		if act.ID == node.ActID {
			actTitle = act.Title
			break
		}
	}

	reason := fmt.Sprintf("%s, level %d of %d.", actTitle, position, total)
	if len(node.BlockedBy) == 0 && node.Availability == game.AvailablePassed {
		return reason + " You have passed this one before; replaying can improve your best score."
	}
	return reason
}

// renderCampaignComplete is what a learner reads when there is nothing left
// to unlock. It exits zero: finishing is not a failure.
func renderCampaignComplete(pack *content.Pack, nodes []game.Node, xp int) string {
	var b strings.Builder
	passed := 0
	for _, n := range nodes {
		if n.Availability == game.AvailablePassed {
			passed++
		}
	}

	b.WriteString(fmt.Sprintf("You have finished every level this pack has: %s.\n", plural(passed, "level")))
	if current, _, ok := game.RankFor(pack, xp); ok {
		b.WriteString(fmt.Sprintf("Final rank: %s, on %d XP.\n", rankName(current), xp))
	}
	b.WriteString("Run `shellforge stats` to see the whole picture, or `shellforge play <level-id>` to replay one.\n")
	return b.String()
}

// xpOf reads the total XP, reporting zero rather than failing: this is used
// only to decorate a message, and a learner who has finished the campaign
// should not be told off by an error because a read went wrong.
func xpOf(ctx context.Context, st *store.Store, profileID int64, packID string) int {
	xp, err := st.TotalXP(ctx, profileID, packID)
	if err != nil {
		return 0
	}
	return xp
}
