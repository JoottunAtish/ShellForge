package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// docAnchorPackInvalid is the heading in docs/05-troubleshooting.md that
// explains a pack that does not validate.
const docAnchorPackInvalid = "level-pack-invalid"

// newValidateCommand returns `shellforge author validate`, which reports
// every authoring invariant a pack breaks.
//
// It reports all of them, not the first. An author who can fix one error per
// run stops running the validator, and one problem per line is the shape an
// editor's quickfix list reads.
func newValidateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <pack-path>",
		Short: "Validate a content pack against docs/LEVEL-FORMAT.md",
		Long: "Check a content pack against every authoring invariant in docs/LEVEL-FORMAT.md.\n\n" +
			"Every problem found is reported, one per line, in file order. Warnings, such as a\n" +
			"level listed in pack.yaml that has no file yet, are printed but do not fail the run.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			asJSON, _ := cmd.Flags().GetBool("json")
			return runValidate(cmd.OutOrStdout(), args[0], asJSON)
		},
	}
	cmd.Flags().Bool("json", false, "Print the report as JSON")
	return cmd
}

// validateReport is the --json document. It carries ok separately from the
// problem list so that a caller scripting against it does not have to
// reimplement the warnings-do-not-fail rule.
type validateReport struct {
	Pack     string            `json:"pack"`
	OK       bool              `json:"ok"`
	Problems []content.Problem `json:"problems"`
}

// runValidate loads the pack at dir, validates it, and writes the report.
func runValidate(out io.Writer, dir string, asJSON bool) error {
	// The two failures are reported separately. Folding them together and
	// wrapping err unconditionally prints "%!w(<nil>)" for the path that
	// exists but is a file, which is worse than a bare Go error: it tells
	// the author nothing and looks like a crash.
	const remediationNotAPack = "Give the path of a pack directory, the one holding pack.yaml and levels/. For the pack that ships with Shellforge that is `shellforge author validate packs/core-linux-basics`."

	info, err := os.Stat(dir)
	if err != nil {
		return ux.Fail("validate the content pack",
			fmt.Errorf("read %s: %w", dir, err),
			remediationNotAPack, docAnchorPackInvalid)
	}
	if !info.IsDir() {
		return ux.Fail("validate the content pack",
			fmt.Errorf("%s is a file, not a pack directory", dir),
			remediationNotAPack, docAnchorPackInvalid)
	}

	fsys := os.DirFS(dir)

	pack, err := content.LoadPack(fsys, ".")
	if err != nil {
		return err // LoadPack already wraps its failures with a remediation
	}

	report := content.Validate(pack, verify.TypeChecker{},
		content.WithPackFS(fsys, "."),
		content.WithHostRoot(dir),
	)

	if asJSON {
		if err := writeJSONReport(out, dir, report); err != nil {
			return err
		}
	} else {
		writeTextReport(out, dir, report)
	}

	if report.OK() {
		return nil
	}
	return ux.Fail("validate the content pack",
		fmt.Errorf("%s: %d problems must be fixed", dir, len(report.Errors())),
		"Each line above names the file, the level and the field. Fix them and run the same command again. The schema is documented in docs/LEVEL-FORMAT.md.",
		docAnchorPackInvalid)
}

// writeJSONReport prints the machine-readable form.
func writeJSONReport(out io.Writer, dir string, report content.Report) error {
	doc := validateReport{Pack: dir, OK: report.OK(), Problems: report.Problems}
	if doc.Problems == nil {
		doc.Problems = []content.Problem{} // an empty list, never a JSON null
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return ux.Fail("write the validation report", err,
			"Check that the destination you redirected output to is writable.", "")
	}
	return nil
}

// writeTextReport prints one problem per line, then a one-line summary.
func writeTextReport(out io.Writer, dir string, report content.Report) {
	for _, p := range report.Problems {
		fmt.Fprintf(out, "%s: %s\n", p.Level, p.Error())
	}

	errs := len(report.Errors())
	warnings := len(report.Problems) - errs
	switch {
	case errs == 0 && warnings == 0:
		fmt.Fprintf(out, "%s: valid.\n", dir)
	case errs == 0:
		fmt.Fprintf(out, "%s: valid, with %s.\n", dir, plural(warnings, "warning"))
	default:
		fmt.Fprintf(out, "%s: %s, %s.\n", dir, plural(errs, "problem"), plural(warnings, "warning"))
	}
}

// plural renders a count with its noun, pluralized the simple way. Every noun
// this is called with takes a plain -s.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
