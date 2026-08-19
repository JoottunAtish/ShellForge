package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/JoottunAtish/ShellForge/internal/doctor"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// docAnchorDataDirUnwritable is the heading in docs/05-troubleshooting.md
// that explains a data directory Shellforge cannot create or write to. A
// package-level constant, not an inline literal, so
// cmd/shellforge/docanchor_test.go can verify it the same way it verifies
// every other anchor in this package.
const docAnchorDataDirUnwritable = "progress-db-unwritable"

// newDoctorCommand returns `shellforge doctor`. prober is the SandboxProber
// the sandbox_health probe pings; nil is legal and makes that probe report
// warn, with a detail saying no runtime was supplied. Choosing and
// provisioning a real runtime is the Day 3 CLI ticket; this parameter is
// what makes that a one-argument change when it lands.
func newDoctorCommand(prober doctor.SandboxProber) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "doctor [--fix] [--json]",
		GroupID: groupSetup,
		Short:   "Check this machine and explain how to fix anything missing",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")
			doFix, _ := cmd.Flags().GetBool("fix")
			return runDoctor(cmd.Context(), cmd.OutOrStdout(), prober, asJSON, doFix)
		},
	}
	cmd.Flags().Bool("json", false, "Print the report as JSON")
	cmd.Flags().Bool("fix", false, "Perform the reversible fixes that need no elevation")
	return cmd
}

// runDoctor gathers the report once, renders it, applies --fix if asked,
// and returns the exit-status error. The report is not re-gathered after
// --fix: re-probing would double the runtime and print two tables that
// differ in one row, and the only fixable finding is a Warn, which never
// affects the exit status anyway.
func runDoctor(ctx context.Context, out io.Writer, prober doctor.SandboxProber, asJSON, doFix bool) error {
	report := doctor.Run(ctx, prober)
	report.Version = version

	var fixed, refused []string
	var fixErr error
	if doFix {
		fixed, refused, fixErr = doctor.Fix(ctx, report)
	}

	if asJSON {
		if err := doctor.RenderJSONWithFixOutcome(out, report, fixed, refused); err != nil {
			return ux.Fail("write the doctor report", err,
				"Check that the file or pipe you redirected output to is writable, then run `shellforge doctor --json` again.",
				"")
		}
	} else {
		if err := doctor.RenderTable(out, report); err != nil {
			return ux.Fail("write the doctor report", err,
				"Check that the file or pipe you redirected output to is writable, then run `shellforge doctor` again.",
				"")
		}
		if doFix {
			printFixOutcome(out, fixed, refused)
		}
	}

	if fixErr != nil {
		return ux.Fail("apply the doctor fixes", fixErr,
			"Check that you can create files in the folder the error names, then run `shellforge doctor --fix` again. Nothing was deleted.",
			docAnchorDataDirUnwritable)
	}

	return doctorExitError(report)
}

// doctorExitError returns a *ux.Error, and therefore a non-zero exit
// status, when and only when report.Failed() is true. A warn-only report
// returns nil: this is the half of AC3 that is easy to get wrong by
// treating "not ok" as "failed".
func doctorExitError(report doctor.Report) error {
	failed := 0
	for _, res := range report.Results {
		if res.Status == doctor.Fail {
			failed++
		}
	}
	if failed == 0 {
		return nil
	}
	return ux.Fail("check this machine",
		fmt.Errorf("%d of %d checks failed", failed, len(report.Results)),
		"Each failing row above names the command that fixes it. Fix them in the order shown, then run `shellforge doctor` again.",
		"")
}

// printFixOutcome writes what --fix did and what it refused, in the table
// path. The --json path folds the same two lists into the document instead;
// see doctor.RenderJSONWithFixOutcome.
func printFixOutcome(w io.Writer, fixed, refused []string) {
	fmt.Fprintln(w)
	if len(fixed) == 0 && len(refused) == 0 {
		fmt.Fprintln(w, "Nothing to fix.")
		return
	}
	for _, d := range fixed {
		fmt.Fprintf(w, "Fixed: %s\n", d)
	}
	for _, r := range refused {
		fmt.Fprintf(w, "Not fixed, run it yourself: %s\n", r)
	}
	fmt.Fprintln(w, "\nRun `shellforge doctor` again to confirm.")
}
