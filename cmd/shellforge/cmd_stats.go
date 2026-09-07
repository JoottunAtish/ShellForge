package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// newStatsCommand returns `shellforge stats`, which prints XP, rank,
// per-act progress and achievements.
//
// Like `map`, it reads the embedded pack and the progress database and
// never touches a sandbox: no runtime is resolved and nothing is
// provisioned, so this works with Docker stopped, which is the state a
// learner checking their progress on the train is in.
func newStatsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "stats",
		GroupID: groupProgress,
		Short:   "Show XP, rank, per-act progress, and achievements",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ascii, _ := cmd.Flags().GetBool("ascii")
			return runStats(cmd.Context(), cmd.OutOrStdout(), ascii)
		},
	}
	cmd.Flags().Bool("ascii", false, "Force ASCII output with no colour or escape sequences")
	return cmd
}

// runStats loads the pack, reads recorded progress, and writes the report.
func runStats(ctx context.Context, out io.Writer, ascii bool) error {
	pack, err := content.Embedded()
	if err != nil {
		return err // Embedded already wraps its own failure as a *ux.Error
	}

	st, profile, err := openProgress(ctx)
	if err != nil {
		return err
	}
	defer st.Close()

	xp, err := st.TotalXP(ctx, profile.ID, pack.ID)
	if err != nil {
		return ux.Fail("read your total XP", err, remediationRunDoctor, "")
	}

	states, err := st.LevelStates(ctx, profile.ID, pack.ID)
	if err != nil {
		return ux.Fail("read your recorded progress", err, remediationRunDoctor, "")
	}

	unlocked, err := st.Achievements(ctx, profile.ID)
	if err != nil {
		return ux.Fail("read your achievements", err, remediationRunDoctor, "")
	}

	nodes, err := game.Resolve(pack, states)
	if err != nil {
		return ux.Fail("resolve the campaign map", err, remediationPackCycle, docAnchorPackInvalid)
	}

	color := ux.ColorEnabled(out) && !ascii
	fmt.Fprint(out, renderStats(pack, nodes, xp, unlocked, color))
	return nil
}

// Remediations shared by the progress-reading verbs, kept together so the
// wording cannot drift between `map`, `stats`, `play` and `skip`.
const (
	remediationRunDoctor = "Run: shellforge doctor"

	remediationPackCycle = "The content pack has a cycle in its level prerequisites. Run: shellforge author validate packs/core-linux-basics"
)

// openProgress opens the progress database and returns it with this
// build's single profile.
//
// The caller closes the store. A failure to open it is a ux.Fail rather
// than a silent fallback to playing without saving: a learner who thinks
// their progress is being recorded when it is not is worse off than one
// who is told the file cannot be written. store.Open already classifies
// its own failures with the progress-db-* anchors, so its error is
// returned untouched.
func openProgress(ctx context.Context) (*store.Store, store.Profile, error) {
	dbPath, err := platform.DatabasePath()
	if err != nil {
		return nil, store.Profile{}, ux.Fail(
			"find the progress database",
			err,
			"Check that your home directory is set and readable, then run: shellforge doctor",
			"")
	}

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return nil, store.Profile{}, err // Open already wraps its own failure as a *ux.Error
	}

	profile, err := st.EnsureProfile(ctx, progressProfileName)
	if err != nil {
		st.Close()
		return nil, store.Profile{}, ux.Fail("read your learner profile", err, remediationRunDoctor, "")
	}
	return st, profile, nil
}
