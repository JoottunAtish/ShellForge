package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/docker"
	"github.com/JoottunAtish/ShellForge/internal/verify"
	"github.com/JoottunAtish/ShellForge/packs"
)

// `shellforge author test`, the golden test runner, and what `make golden` calls.
//
// The Makefile has called this since Day 0 and it did not exist, so the target
// has never done anything. It runs the same contract the Go golden test runs,
// through the same runGoldenLevel, because two definitions of "is this level
// correct" would drift and then disagree.

// goldenSandboxName is the container `author test` provisions.
//
// Deliberately NOT the production shellforge-sandbox. An author running `make
// golden` while a learner has a level open in another terminal must not have
// their world torn down, and this harness tears down every level it touches.
const (
	goldenSandboxName  = "shellforge-authortest"
	goldenSandboxImage = "shellforge-authortest"
)

// newAuthorTestCommand returns `shellforge author test`.
func newAuthorTestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test [--all] [level-id...]",
		Short: "Run the golden test: setup, checks fail, solution, checks pass",
		Long: "Run every level through the golden contract in docs/LEVEL-FORMAT.md section 7.\n\n" +
			"For each level: set its world up, confirm every required check FAILS, run the level's own\n" +
			"solution as the learner, confirm every required check PASSES, confirm the checks changed\n" +
			"nothing, then tear down and confirm nothing was left behind.\n\n" +
			"This needs a Docker daemon running Linux containers, because it verifies real state in a\n" +
			"real sandbox. It provisions its own container, never the one `shellforge run` uses.",
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			asJSON, _ := cmd.Flags().GetBool("json")
			packDir, _ := cmd.Flags().GetString("pack")
			return runAuthorTest(cmd.Context(), cmd.OutOrStdout(), authorTestOptions{
				all:     all,
				ids:     args,
				asJSON:  asJSON,
				packDir: packDir,
			})
		},
	}
	cmd.Flags().Bool("all", false, "Test every level in the pack, in campaign order")
	cmd.Flags().Bool("json", false, "Emit one JSON object per level")
	cmd.Flags().String("pack", "", "Pack directory to test (default: the pack embedded in this binary)")
	return cmd
}

// authorTestOptions is what the command parsed.
type authorTestOptions struct {
	all     bool
	ids     []string
	asJSON  bool
	packDir string
}

// runAuthorTest is the verb's body.
//
// Exit codes match `author validate`'s rather than inventing a third scale: the
// error return becomes a non-zero exit through main, and a level that fails the
// contract is reported as a problem rather than as a crash.
func runAuthorTest(ctx context.Context, out io.Writer, opts authorTestOptions) error {
	if opts.all && len(opts.ids) > 0 {
		return ux.Fail(
			"work out which levels to test",
			nil,
			"Name levels, or pass --all, but not both. `shellforge author test --all` tests the whole pack.",
			"",
		)
	}

	pack, packFS, err := loadPackForTesting(opts.packDir)
	if err != nil {
		return err
	}

	order := levelOrder(pack)
	ids := opts.ids
	if opts.all {
		ids = order
	}
	if len(ids) == 0 {
		return ux.Fail(
			"work out which levels to test",
			nil,
			fmt.Sprintf("Name a level, or pass --all. This pack has %s: %s.",
				plural(len(order), "level"), strings.Join(order, ", ")),
			"",
		)
	}

	levels, err := resolveLevels(pack, ids, order)
	if err != nil {
		return err
	}

	rt, sess, cleanup, err := openGoldenSandbox(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	_ = rt

	harness := goldenHarness{
		sess:   sess,
		packFS: packFS,
		engine: verify.NewEngine(),
	}

	var failed int
	for _, level := range levels {
		report := runGoldenLevel(ctx, harness, level)
		if !report.OK {
			failed++
		}
		if err := writeGoldenReport(out, report, opts.asJSON); err != nil {
			return err
		}
	}

	if !opts.asJSON {
		switch {
		case failed == 0:
			fmt.Fprintf(out, "%s tested, all passed the golden contract.\n", plural(len(levels), "level"))
		default:
			fmt.Fprintf(out, "%s tested, %d failed.\n", plural(len(levels), "level"), failed)
		}
	}

	if failed > 0 {
		return ux.Fail(
			fmt.Sprintf("verify %s against the golden contract", plural(failed, "level")),
			nil,
			"Each line above names the stage that failed. A level whose own solution does not satisfy its "+
				"checks is a broken level: fix the level, never loosen the check to make it pass.",
			docAnchorPackInvalid,
		)
	}
	return nil
}

// loadPackForTesting loads either the embedded pack or one from disk, and returns
// a filesystem rooted AT the pack so a level's `source:` resolves.
func loadPackForTesting(dir string) (*content.Pack, fs.FS, error) {
	if dir == "" {
		pack, err := content.Embedded()
		if err != nil {
			return nil, nil, err
		}
		packFS, err := packFilesystem()
		if err != nil {
			return nil, nil, err
		}
		return pack, packFS, nil
	}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, nil, ux.Fail(
			fmt.Sprintf("read the pack at %q", dir),
			err,
			"Give the path of a pack directory, the one holding pack.yaml and levels/, as in "+
				"`shellforge author test --all --pack packs/core-linux-basics`.",
			docAnchorPackInvalid,
		)
	}

	packFS := os.DirFS(dir)
	pack, err := content.LoadPack(packFS, ".")
	if err != nil {
		return nil, nil, err
	}
	return pack, packFS, nil
}

// resolveLevels turns ids into levels, refusing an unknown one before any
// container is provisioned.
func resolveLevels(pack *content.Pack, ids, order []string) ([]*content.Level, error) {
	out := make([]*content.Level, 0, len(ids))
	for _, id := range ids {
		level, ok := pack.Level(id)
		if !ok {
			return nil, unknownLevel(id, order)
		}
		out = append(out, level)
	}
	return out, nil
}

// openGoldenSandbox provisions the author-test container.
func openGoldenSandbox(ctx context.Context) (runtime.Runtime, runtime.Session, func(), error) {
	rt, err := docker.New(goldenSandboxName, goldenSandboxImage)
	if err != nil {
		// docker.New already returns a ux.Error naming the missing binary. It is
		// returned rather than skipped on purpose: the Go golden test skips
		// without a daemon, so something has to be honest, and a `make golden`
		// that reported success having tested nothing would be worse than no
		// gate at all.
		return nil, nil, nil, err
	}

	if err := rt.Provision(ctx, runtime.ImageSpec{Name: goldenSandboxImage}); err != nil {
		return nil, nil, nil, ux.Fail(
			"provision the sandbox for testing levels",
			err,
			"Run `docker info` to confirm the daemon is running and serving Linux containers, then try again.",
			"docker-daemon-down",
		)
	}

	sess, err := rt.StartSession(ctx, runtime.SessionSpec{
		User:     sandboxUser,
		WorkDir:  sandboxHome,
		StateDir: setupStateDir(),
		Env:      map[string]string{"SF_STATE": setupStateDir()},
	})
	if err != nil {
		return nil, nil, nil, ux.Fail(
			"open a session on the sandbox",
			err,
			"Run `docker ps` to see whether the container is running, then try again.",
			"sandbox-missing",
		)
	}

	return rt, sess, func() { _ = sess.Close() }, nil
}

// writeGoldenReport prints one level's result.
//
// The text form mirrors `author validate`: one line per level, the stage in the
// line so a CI log alone is diagnosable, and a summary at the end.
func writeGoldenReport(out io.Writer, report goldenReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(out)
		if err := enc.Encode(report); err != nil {
			return ux.Fail("write the golden test report", err,
				"Check that the destination you redirected output to is writable.", "")
		}
		return nil
	}

	if report.OK {
		fmt.Fprintf(out, "%s: ok%s\n", report.LevelID, formatTimings(report.Timings))
		return nil
	}

	fmt.Fprintf(out, "%s: %s: %s\n", report.LevelID, report.Stage, report.Problem)
	return nil
}

// formatTimings renders the per-stage durations in a stable order.
func formatTimings(timings map[string]string) string {
	if len(timings) == 0 {
		return ""
	}
	// Stage order, not alphabetical: the numbers are read as a sequence.
	stages := []string{stageSetup, stagePreCheck, stageSolution, stagePostCheck, stagePurity, stageTeardown}
	seen := map[string]bool{}
	var parts []string
	for _, stage := range stages {
		if d, ok := timings[stage]; ok {
			parts = append(parts, stage+" "+d)
			seen[stage] = true
		}
	}
	// Anything the list above does not know about, so a new stage is not
	// silently dropped from the report.
	var extra []string
	for stage, d := range timings {
		if !seen[stage] {
			extra = append(extra, stage+" "+d)
		}
	}
	sort.Strings(extra)
	parts = append(parts, extra...)

	return " (" + strings.Join(parts, ", ") + ")"
}

// goldenPackFS is the embedded pack rooted at the pack directory, for the Go
// golden test. It exists so the test does not repeat the fs.Sub that
// packFilesystem explains.
func goldenPackFS() (fs.FS, error) { return fs.Sub(packs.FS, packs.CoreLinuxBasics) }
