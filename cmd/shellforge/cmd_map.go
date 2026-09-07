package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// progressProfileName is the profile every progress-reading verb works
// against: `map`, `stats`, `play` and `skip` alike.
// v0.1 is single profile only, so Store.EnsureProfile ignores this name on
// every call after the first; it is still a named constant, not an inline
// literal, so the day a second profile exists there is exactly one place to
// change. It matches cmd_run.go's own sandboxUser constant in spirit, not
// in meaning: that one names the unprivileged user inside the sandbox, this
// one names a row in the progress database, and the two happen to share a
// string today only because v0.1 has exactly one of each.
const progressProfileName = "learner"

// newMapCommand returns `shellforge map`, which prints the campaign as a
// tree of passed, available, and locked levels. It reads the embedded pack
// and the progress database and never touches a sandbox: no runtime is
// resolved, nothing is provisioned, so this works with Docker stopped.
func newMapCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "map",
		GroupID: groupProgress,
		Short:   "Show the campaign as a tree of passed, available, and locked levels",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ascii, _ := cmd.Flags().GetBool("ascii")
			return runMap(cmd.Context(), cmd.OutOrStdout(), ascii)
		},
	}
	cmd.Flags().Bool("ascii", false, "Force ASCII output with no colour or escape sequences")
	return cmd
}

// runMap loads the embedded pack, reads recorded progress from the
// database, resolves the unlock state, and writes the rendered tree to out.
//
// Every step here is either a pure read (content.Embedded, store's own
// EnsureProfile and LevelStates) or a pure function (game.Resolve,
// renderMap); nothing here writes to the sandbox, because nothing here
// touches a sandbox at all.
func runMap(ctx context.Context, out io.Writer, ascii bool) error {
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

	color := ux.ColorEnabled(out) && !ascii
	fmt.Fprint(out, renderMap(pack, nodes, color))
	return nil
}
