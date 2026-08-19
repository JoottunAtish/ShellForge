package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/sandbox"
)

// resolveFunc resolves the sandbox backend named by want and constructs its
// Runtime. It is the seam `init` and every `sandbox` subcommand share, so a
// test can substitute a recording fake with no Docker daemon and no
// wsl.exe.
type resolveFunc func(ctx context.Context, want sandbox.Backend) (runtime.Runtime, sandbox.Choice, error)

// defaultResolver is the real resolveFunc: internal/sandbox.Resolve, with
// every Options field left at its zero value except Want, which means the
// real platform, the real availability probes, and the real backend
// constructors.
func defaultResolver(ctx context.Context, want sandbox.Backend) (runtime.Runtime, sandbox.Choice, error) {
	return sandbox.Resolve(ctx, sandbox.Options{Want: want})
}

// newInitCommand returns `shellforge init`.
func newInitCommand(resolve resolveFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "init [--runtime=auto|wsl|docker]",
		GroupID: groupSetup,
		Short:   "Provision the sandbox. Run once, takes a few minutes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			want, _ := cmd.Flags().GetString("runtime")
			return runInit(cmd.Context(), cmd.OutOrStdout(), resolve, want)
		},
	}
	cmd.Flags().String("runtime", "auto", "Which backend to use: auto, wsl, or docker")
	return cmd
}

// runInit resolves a backend, provisions it, and names what to run next.
//
// The --runtime flag is validated BEFORE resolve is ever called, through
// sandbox.ParseBackend: an unknown value must construct nothing, which is
// the same contract sandbox.Resolve enforces one layer down.
//
// A cancellation mid-provision is reported through ux and nothing is ever
// destroyed to "clean up": Provision's own idempotency is the only
// recovery this command relies on, because the CLI cannot tell a first
// provision from a repair of a working sandbox, and getting that wrong
// would delete one.
func runInit(ctx context.Context, out io.Writer, resolve resolveFunc, wantFlag string) error {
	want, err := sandbox.ParseBackend(wantFlag)
	if err != nil {
		return err
	}

	rt, choice, err := resolve(ctx, want)
	if err != nil {
		return err
	}

	fmt.Fprintln(out, choice.Reason)
	fmt.Fprintln(out, "Preparing the sandbox. The first run builds the image, which takes a few minutes.")

	if err := rt.Provision(ctx, sandbox.Spec()); err != nil {
		if errors.Is(err, context.Canceled) {
			return ux.Fail(
				"provision the sandbox",
				err,
				"Setup stopped when you pressed Ctrl-C. Nothing was deleted. Run `shellforge sandbox status` to see where it got to, then run `shellforge init` again to carry on.",
				"",
			)
		}
		return ux.Fail(
			"provision the sandbox",
			err,
			"Run `shellforge doctor` to check the backend this machine uses, fix anything it names, then run `shellforge init` again.",
			"sandbox-unhealthy",
		)
	}

	fmt.Fprintln(out, "The sandbox is ready.")
	fmt.Fprintln(out, nextStepAfterInit())
	return nil
}

// nextStepAfterInit names the first level in campaign order, reusing
// content.Embedded() and cmd_run.go's own levelOrder so the suggestion
// never drifts from the pack. A packaging problem must not fail a
// provisioning command that otherwise succeeded: if the pack cannot be
// loaded, or has no levels, this points at `shellforge help` instead.
func nextStepAfterInit() string {
	pack, err := content.Embedded()
	if err != nil {
		return "Run `shellforge help` to see what to do next."
	}
	order := levelOrder(pack)
	if len(order) == 0 {
		return "Run `shellforge help` to see what to do next."
	}
	return fmt.Sprintf("Run `shellforge run %s` to start.", order[0])
}
