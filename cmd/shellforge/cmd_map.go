package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// mapProfileName is the profile `shellforge map` reads progress against.
// v0.1 is single profile only, so Store.EnsureProfile ignores this name on
// every call after the first; it is still a named constant, not an inline
// literal, so the day a second profile exists there is exactly one place to
// change. It matches cmd_run.go's own sandboxUser constant in spirit, not
// in meaning: that one names the unprivileged user inside the sandbox, this
// one names a row in the progress database, and the two happen to share a
// string today only because v0.1 has exactly one of each.
const mapProfileName = "learner"

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

	dbPath, err := platform.DatabasePath()
	if err != nil {
		return ux.Fail("find the progress database",
			err,
			"Check that your home directory is set and readable, then run: shellforge doctor",
			"")
	}

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return err // Open already wraps its own failure as a *ux.Error
	}
	defer st.Close()

	profile, err := st.EnsureProfile(ctx, mapProfileName)
	if err != nil {
		return ux.Fail("read your learner profile",
			err,
			"Run: shellforge doctor",
			"")
	}

	states, err := st.LevelStates(ctx, profile.ID, pack.ID)
	if err != nil {
		return ux.Fail("read your recorded progress",
			err,
			"Run: shellforge doctor",
			"")
	}

	nodes, err := game.Resolve(pack, states)
	if err != nil {
		return ux.Fail("resolve the campaign map",
			err,
			"The content pack has a cycle in its level prerequisites. Run: shellforge author validate packs/core-linux-basics",
			docAnchorPackInvalid)
	}

	color := ux.ColorEnabled(os.Stdout) && !ascii
	fmt.Fprint(out, renderMap(pack, nodes, color))
	return nil
}
