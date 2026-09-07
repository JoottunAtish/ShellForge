package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

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
// It plays one level and returns to the host shell rather than provisioning
// the next one automatically. Auto-advancing is a bigger interaction
// decision than it looks: it makes Ctrl-C ambiguous, and `play` is one
// keystroke away. Worth revisiting on Day 6, recorded here so it reads as a
// decision.

// playOptions is what `play` parsed out of its arguments.
type playOptions struct {
	// LevelID names a specific level to replay. Empty means resume.
	LevelID string

	// DryRun is --next: say which level is next and provision nothing.
	DryRun bool

	// Debug is --log-level=debug, threaded into the shared run flow.
	Debug bool
}

// newPlayCommand returns `shellforge play`.
func newPlayCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "play [level-id] [--next] [--log-level=debug]",
		GroupID:            groupPlaying,
		Short:              "Start, or resume at the next level",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := parsePlayArgs(args)
			if err != nil {
				return err
			}
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
	var opts playOptions

	badFlag := func(a string) error {
		return ux.Fail(
			fmt.Sprintf("understand the option %q", a),
			nil,
			"Run `shellforge help play` for the usage. The options are --next and --log-level=debug.",
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

// runPlay resolves the level to play and plays it.
//
// The order matters and is pinned by a test: the pack, the database and the
// choice of level all happen BEFORE anything slow, so a learner who
// expected a different level can read the reason and press Ctrl-C rather
// than wait several minutes for a container they did not want.
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

	states, err := st.LevelStates(ctx, profile.ID, pack.ID)
	if err != nil {
		return ux.Fail("read your recorded progress", err, remediationRunDoctor, "")
	}

	nodes, err := game.Resolve(pack, states)
	if err != nil {
		return ux.Fail("resolve the campaign map", err, remediationPackCycle, docAnchorPackInvalid)
	}

	choice, reason, err := chooseLevel(pack, nodes, opts.LevelID)
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

	return runLevel(ctx, runOptions{levelID: choice.ID, debug: opts.Debug}, pack, choice)
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
