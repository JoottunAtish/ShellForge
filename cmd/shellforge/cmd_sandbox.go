package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	goruntime "runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/pty"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/sandbox"
)

// Doc anchors this file's ux.Fail calls need, named as constants rather
// than inlined so cmd/shellforge/docanchor_test.go can resolve every one of
// them the same way it resolves a literal.
const (
	anchorSandboxMissing   = "sandbox-missing"
	anchorSandboxUnhealthy = "sandbox-unhealthy"
	anchorWindowsNeedsWSL  = "windows-needs-wsl"
	anchorTerminalNoVT     = "terminal-no-vt"
)

// newSandboxCommand returns the `sandbox` group and its four real
// subcommands: status, shell, rebuild, and destroy. `build` is gone as of
// issue #71: Provision is documented idempotent and already builds the
// image or distribution, so it had no behaviour distinct from `init`.
//
// Every subcommand carries its own --runtime flag, parsed with
// sandbox.ParseBackend exactly as `init` parses its own, defaulting to
// sandbox.Auto so the common case is unchanged. Without this, a learner
// who ran `shellforge init --runtime=docker` on a machine where WSL2 is
// also available has no way to tell `sandbox destroy` which backend they
// actually provisioned: every verb resolving Auto could pick the other
// backend, report success, and leave the real sandbox untouched. There is
// no state file remembering the choice; naming it again on the command
// line is the only way to be understood correctly.
func newSandboxCommand(resolve resolveFunc) *cobra.Command {
	sandboxCmd := &cobra.Command{
		Use:     "sandbox",
		GroupID: groupManage,
		Short:   "Inspect, rebuild, or destroy the sandbox",
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether the sandbox is provisioned and running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			want, _ := cmd.Flags().GetString("runtime")
			return runSandboxStatus(cmd.Context(), cmd.OutOrStdout(), resolve, want)
		},
	}
	statusCmd.Flags().String("runtime", "auto", "Which backend to check: auto, wsl, or docker. Name the one you provisioned with, if not auto.")

	shellCmd := &cobra.Command{
		Use:   "shell",
		Short: "Open an interactive shell inside the sandbox",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			want, _ := cmd.Flags().GetString("runtime")
			return runSandboxShell(cmd.Context(), resolve, want)
		},
	}
	shellCmd.Flags().String("runtime", "auto", "Which backend to open a shell in: auto, wsl, or docker. Name the one you provisioned with, if not auto.")

	destroyCmd := &cobra.Command{
		Use:   "destroy",
		Short: "Remove the sandbox container, or the WSL distribution",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			want, _ := cmd.Flags().GetString("runtime")
			return runSandboxDestroy(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), resolve, yes, want)
		},
	}
	destroyCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	destroyCmd.Flags().String("runtime", "auto", "Which backend to remove: auto, wsl, or docker. Name the one you provisioned with, if not auto.")

	rebuildCmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Destroy the sandbox, then provision it again from scratch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			want, _ := cmd.Flags().GetString("runtime")
			return runSandboxRebuild(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), resolve, yes, want)
		},
	}
	rebuildCmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
	rebuildCmd.Flags().String("runtime", "auto", "Which backend to rebuild: auto, wsl, or docker. Name the one you provisioned with, if not auto.")

	sandboxCmd.AddCommand(statusCmd, shellCmd, rebuildCmd, destroyCmd)
	return sandboxCmd
}

// runSandboxStatus reports whether the sandbox is provisioned and running.
// It is read-only: it never starts, repairs, or provisions anything.
//
// wantFlag is validated BEFORE resolve is ever called, through
// sandbox.ParseBackend, the same contract runInit follows: an unknown
// value must construct nothing.
func runSandboxStatus(ctx context.Context, out io.Writer, resolve resolveFunc, wantFlag string) error {
	want, err := sandbox.ParseBackend(wantFlag)
	if err != nil {
		return err
	}

	rt, _, err := resolve(ctx, want)
	if err != nil {
		return err
	}

	st, err := rt.Status(ctx)
	if err != nil {
		return ux.Fail(
			"check the sandbox",
			err,
			"The backend could not be asked about the sandbox. Run `shellforge doctor` to find out why, then run `shellforge sandbox status` again.",
			anchorSandboxUnhealthy,
		)
	}

	fmt.Fprintf(out, "Provisioned: %v\n", st.Provisioned)
	fmt.Fprintf(out, "Running: %v\n", st.Running)
	fmt.Fprintf(out, "Backend: %s\n", st.Backend)
	fmt.Fprintf(out, "Detail: %s\n", st.Detail)

	if !st.Provisioned {
		return ux.Fail(
			"check the sandbox",
			nil,
			"Nothing is provisioned yet. Run `shellforge init` to create the sandbox.",
			anchorSandboxMissing,
		)
	}
	return nil
}

// printRemovalPlan prints exactly what a destroy or rebuild is about to
// remove, in the words sandbox.Plan built. This is the one moment the
// learner can still say no, so it prints before any confirmation is asked
// for, and it names the same absolute path, if any, that verifyRemoved
// checks afterward.
func printRemovalPlan(out io.Writer, plan sandbox.RemovalPlan) {
	fmt.Fprintln(out, "This will remove:")
	for _, item := range plan.Items {
		fmt.Fprintf(out, "  - %s\n", item)
	}
}

// confirmSandboxName is the one confirmation destroy and rebuild share.
//
// yes bypasses the prompt entirely and is the only bypass. Otherwise it
// reads exactly one line from in, trims surrounding whitespace, and
// compares it byte-exactly and case-sensitively against plan.Name. An
// empty line, a mismatch, or EOF all refuse: this is the uninstall path,
// and a safety check that cannot tell whether the answer was really "yes"
// fails closed.
func confirmSandboxName(in io.Reader, out io.Writer, plan sandbox.RemovalPlan, yes bool) error {
	if yes {
		return nil
	}

	fmt.Fprintf(out, "\nType %s to continue, or anything else to cancel: ", plan.Name)

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return refuseSandboxConfirmation()
	}
	if strings.TrimSpace(scanner.Text()) != plan.Name {
		return refuseSandboxConfirmation()
	}
	return nil
}

// refuseSandboxConfirmation is the one ux.Error a declined, mistyped, or
// missing confirmation returns. Nothing is removed on this path.
func refuseSandboxConfirmation() error {
	return ux.Fail(
		"confirm removing the sandbox",
		nil,
		"Nothing was removed. To go ahead, run the command again and type the sandbox name exactly as it is printed, or add --yes to skip the question.",
		"",
	)
}

// runSandboxDestroy prints the removal plan, asks for confirmation unless
// yes is set, destroys the sandbox, and then verifies it is really gone
// rather than trusting a nil error from Destroy.
//
// wantFlag is validated before resolve is ever called, same as
// runSandboxStatus.
func runSandboxDestroy(ctx context.Context, in io.Reader, out io.Writer, resolve resolveFunc, yes bool, wantFlag string) error {
	want, err := sandbox.ParseBackend(wantFlag)
	if err != nil {
		return err
	}

	rt, choice, err := resolve(ctx, want)
	if err != nil {
		return err
	}

	plan, err := sandbox.Plan(ctx, choice)
	if err != nil {
		return err
	}

	printRemovalPlan(out, plan)

	if err := confirmSandboxName(in, out, plan, yes); err != nil {
		return err
	}

	if err := rt.Destroy(ctx); err != nil {
		return ux.Fail(
			"remove the sandbox",
			err,
			"Run `shellforge sandbox status` to see what is still there, then run `shellforge sandbox destroy` again. The path printed above is the one to check by hand if it keeps failing.",
			anchorSandboxUnhealthy,
		)
	}

	return verifyRemoved(ctx, out, rt, plan)
}

// runSandboxRebuild says what it is about to do, asks for the SAME
// confirmation as destroy, then destroys the sandbox and provisions it
// again, in that order. The announcement prints before either step runs.
//
// wantFlag is validated before resolve is ever called, same as
// runSandboxStatus.
func runSandboxRebuild(ctx context.Context, in io.Reader, out io.Writer, resolve resolveFunc, yes bool, wantFlag string) error {
	want, err := sandbox.ParseBackend(wantFlag)
	if err != nil {
		return err
	}

	rt, choice, err := resolve(ctx, want)
	if err != nil {
		return err
	}

	plan, err := sandbox.Plan(ctx, choice)
	if err != nil {
		return err
	}

	printRemovalPlan(out, plan)
	fmt.Fprintln(out, "This rebuilds the sandbox: it is destroyed first, then provisioned again from scratch.")

	if err := confirmSandboxName(in, out, plan, yes); err != nil {
		return err
	}

	if err := rt.Destroy(ctx); err != nil {
		return ux.Fail(
			"remove the sandbox",
			err,
			"Run `shellforge sandbox status` to see what is still there, then run `shellforge sandbox destroy` again. The path printed above is the one to check by hand if it keeps failing.",
			anchorSandboxUnhealthy,
		)
	}

	if err := rt.Provision(ctx, sandbox.Spec()); err != nil {
		return ux.Fail(
			"provision the sandbox",
			err,
			"Run `shellforge doctor` to check the backend this machine uses, fix anything it names, then run `shellforge init` again.",
			anchorSandboxUnhealthy,
		)
	}

	fmt.Fprintln(out, "The sandbox has been rebuilt.")
	return nil
}

// verifyRemoved asks the backend again after Destroy returned nil, because
// a nil error is not proof: it is only proof once Status agrees. A
// sandbox still reporting Provisioned is a ux.Fail naming the absolute
// path to check, never a quiet success. A Status call that errors is
// exactly as unproven: destructive-safety's fail-closed rule means a check
// that cannot determine whether the operation succeeded must not report
// success, so that case is also a ux.Fail, not a fall-through to
// "Removed."
func verifyRemoved(ctx context.Context, out io.Writer, rt runtime.Runtime, plan sandbox.RemovalPlan) error {
	checkTarget := plan.VerifyPath
	if checkTarget == "" {
		checkTarget = fmt.Sprintf("the output of `%s`", plan.VerifyCommand)
	}

	st, statusErr := rt.Status(ctx)
	if statusErr != nil {
		return ux.Fail(
			"confirm the sandbox was removed",
			statusErr,
			fmt.Sprintf("Destroy itself reported success, but checking afterward failed, so removal is not confirmed. Open %s and check it yourself, then run `shellforge sandbox destroy` again if anything is still there.", checkTarget),
			anchorSandboxUnhealthy,
		)
	}
	if st.Provisioned {
		return ux.Fail(
			"confirm the sandbox was removed",
			nil,
			fmt.Sprintf("The backend still reports a sandbox after removing it. Open %s and check it yourself, then run `shellforge sandbox destroy` again.", checkTarget),
			anchorSandboxUnhealthy,
		)
	}

	fmt.Fprintf(out, "Removed. Check %s if you want to confirm by hand.\n", checkTarget)
	return nil
}

// checkSandboxShellSupported refuses `sandbox shell` up front on a host
// that cannot open an interactive shell at all: see checkInteractiveShellSupported
// in cmd_run.go for the full reason, which applies here unchanged. Refusing
// here, before resolve or Provision ever runs, saves a multi-minute
// provision that would only fail at the last step anyway. Issue #138 owns
// the Windows console work that would let this stop refusing.
func checkSandboxShellSupported() error {
	if goruntime.GOOS != "windows" {
		return nil
	}
	return ux.Fail(
		"open an interactive sandbox shell on Windows",
		nil,
		"Open your WSL distribution, change to this repository, then build and run there: "+
			"`go build -o bin/shellforge ./cmd/shellforge && ./bin/shellforge sandbox shell`. "+
			"Neither backend can open an interactive shell from PowerShell or the command prompt yet.",
		anchorWindowsNeedsWSL,
	)
}

// runSandboxShell attaches an interactive shell inside the sandbox.
//
// It wires no journal, no control channel, and no level state: SF_STATE
// points at the same state directory `run` uses, so instrument.bash has
// somewhere to write, and that is all. The in-sandbox `check` shim will not
// work here, which is correct: there is no level.
//
// wantFlag is validated before resolve is ever called, same as
// runSandboxStatus.
func runSandboxShell(ctx context.Context, resolve resolveFunc, wantFlag string) error {
	if err := checkSandboxShellSupported(); err != nil {
		return err
	}

	want, err := sandbox.ParseBackend(wantFlag)
	if err != nil {
		return err
	}

	rt, _, err := resolve(ctx, want)
	if err != nil {
		return err
	}

	sess, err := rt.StartSession(ctx, runtime.SessionSpec{
		User:     sandboxUser,
		WorkDir:  sandboxHome,
		StateDir: setupStateDir(),
		Env:      map[string]string{"SF_STATE": setupStateDir()},
	})
	if err != nil {
		// A sandbox that has never been provisioned, or was just destroyed,
		// is not the same problem as an unhealthy one: sending a first-time
		// learner to `sandbox status` under sandbox-unhealthy just tells
		// them to run init anyway, one extra command later than necessary.
		if errors.Is(err, runtime.ErrSandboxMissing) {
			return ux.Fail(
				"start a shell inside the sandbox",
				err,
				"Run `shellforge init` to create the sandbox, then run `shellforge sandbox shell` again.",
				anchorSandboxMissing,
			)
		}
		return ux.Fail(
			"start a shell inside the sandbox",
			err,
			"Run `shellforge sandbox status` to confirm the sandbox is running, then run `shellforge sandbox shell` again.",
			anchorSandboxUnhealthy,
		)
	}
	defer func() { _ = sess.Close() }()

	sandboxPTY, err := sess.Attach(ctx, runtime.AttachOpts{
		User:    sandboxUser,
		WorkDir: sandboxHome,
	})
	if err != nil {
		return ux.Fail(
			"start a shell inside the sandbox",
			err,
			"Run `shellforge sandbox status` to confirm the sandbox is running, then run `shellforge sandbox shell` again.",
			anchorSandboxUnhealthy,
		)
	}
	// Mux.Run's drain closes sandboxPTY on every select branch, but Run has
	// two earlier returns, a failed fd resolve and a failed makeRaw, that
	// happen before it takes ownership. Without this, `shellforge sandbox
	// shell | cat` on either of those paths leaks the exec child and its
	// master fd.
	defer func() { _ = sandboxPTY.Close() }()

	mux := pty.New(sandboxPTY, os.Stdin, os.Stdout)
	if runErr := mux.Run(ctx); runErr != nil {
		if errors.Is(runErr, pty.ErrSignalled) {
			return nil
		}
		return ux.Fail(
			"run the sandbox shell",
			runErr,
			"If your terminal is behaving oddly, run `reset`. Then run `shellforge sandbox shell` again.",
			anchorTerminalNoVT,
		)
	}

	fmt.Fprintln(os.Stdout, "Shell exited.")
	return nil
}
