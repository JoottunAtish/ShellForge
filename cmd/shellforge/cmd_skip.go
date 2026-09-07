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

// `skip`: mark a level skipped so the campaign can continue past one that
// is not landing.
//
// Skipping is a real choice rather than a failure. store.StatusSkipped
// already exists and game.Resolve's unlock rule already accepts it, so the
// only new thing here is saying so in words a learner can act on: nothing
// was awarded, and `play <level-id>` comes back to it whenever they want.
//
// It writes one status and deletes nothing.

// newSkipCommand returns `shellforge skip`.
func newSkipCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "skip <level-id>",
		GroupID: groupPlaying,
		Short:   "Mark a level skipped and unlock what comes after it",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			levelID := ""
			if len(args) == 1 {
				levelID = args[0]
			}
			return runSkip(cmd.Context(), cmd.OutOrStdout(), levelID)
		},
	}
}

// runSkip records levelID as skipped.
//
// An empty id skips the level `play` would have chosen, which is the one a
// learner staring at a level they cannot finish is actually looking at.
func runSkip(ctx context.Context, out io.Writer, levelID string) error {
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

	node, level, err := levelToSkip(pack, nodes, levelID)
	if err != nil {
		return err
	}

	if err := skipRefusal(node, level); err != nil {
		return err
	}

	if err := st.SetLevelStatus(ctx, profile.ID, pack.ID, level.ID, level.Version, store.StatusSkipped); err != nil {
		return ux.Fail("record the level as skipped", err, remediationRunDoctor, "")
	}

	fmt.Fprintf(out, "Skipped %s, %s. No XP was awarded for it.\n", level.ID, level.Title)
	fmt.Fprintf(out, "Whatever it was blocking is unlocked now. Run `shellforge play` to carry on, or `shellforge play %s` to come back to it whenever you want.\n", level.ID)
	return nil
}

// skipRefusal reports why levelID cannot be skipped, or nil when it can.
//
// Its own function so the guard is exercised by a test directly, rather
// than only through runSkip, which needs a progress database to reach.
//
// Two refusals. A locked level is refused for the same reason `play`
// refuses it: a skip unlocks whatever the level was blocking, so skipping a
// locked one would walk straight past every prerequisite between here and
// there and unlock work the learner has not reached. A passed level is
// refused because there is nothing left to skip.
func skipRefusal(node game.Node, level *content.Level) error {
	switch node.Availability {
	case game.AvailableLocked:
		return ux.Fail(
			fmt.Sprintf("skip level %q", level.ID),
			nil,
			fmt.Sprintf("That level is locked until you have passed or skipped %s, so there is nothing to skip yet. Run `shellforge map` to see the campaign, or `shellforge skip` on its own to skip the level you are on.",
				strings.Join(node.BlockedBy, " and ")),
			docAnchorLevelNotFound,
		)
	case game.AvailablePassed:
		return ux.Fail(
			fmt.Sprintf("skip level %q", level.ID),
			nil,
			fmt.Sprintf("You have already passed that level, so there is nothing to skip. Run `shellforge play` to carry on, or `shellforge play %s` to replay it.", level.ID),
			"",
		)
	default:
		return nil
	}
}

// levelToSkip resolves the level a skip applies to, which is the named one
// or, when none was named, the one `play` would have chosen.
func levelToSkip(pack *content.Pack, nodes []game.Node, levelID string) (game.Node, *content.Level, error) {
	if levelID == "" {
		next, ok := game.Next(nodes)
		if !ok {
			return game.Node{}, nil, ux.Fail(
				"work out which level to skip",
				nil,
				"There is no level waiting to be played, so there is nothing to skip. Run `shellforge map` to see where you are.",
				"",
			)
		}
		levelID = next.LevelID
	}

	level, found := pack.Level(levelID)
	if !found {
		return game.Node{}, nil, unknownLevel(levelID, levelOrder(pack))
	}
	for _, n := range nodes {
		if n.LevelID == levelID {
			return n, level, nil
		}
	}
	return game.Node{LevelID: levelID}, level, nil
}
