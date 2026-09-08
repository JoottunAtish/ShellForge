package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/JoottunAtish/ShellForge/internal/bugreport"
	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/doctor"
	"github.com/JoottunAtish/ShellForge/internal/platform"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/sandbox"
	"github.com/JoottunAtish/ShellForge/internal/store"
)

// bugReportWriter is bugreport.Write's own signature, named here so
// runBugReport can take a substitute in tests without ever opening a
// second real file: see TestBugReportRemovesOnlyThePartialBundleItCreated.
type bugReportWriter func(w io.Writer, r bugreport.Report, logDir string) error

// newBugReportCommand returns `shellforge bug-report`.
func newBugReportCommand(v VersionInfo, p bugreport.Prober) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "bug-report [--journal] [--out <path>]",
		GroupID: groupManage,
		Short:   "Bundle diagnostics for a GitHub issue, with the journal redacted",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			includeJournal, _ := cmd.Flags().GetBool("journal")
			out, _ := cmd.Flags().GetString("out")
			return runBugReport(cmd.Context(), cmd.OutOrStdout(), v, p, includeJournal, out, bugreport.Write)
		},
	}
	cmd.Flags().Bool("journal", false, "Include the commands you typed, with secrets redacted")
	cmd.Flags().String("out", "", "Where to write the zip (default: shellforge-bugreport-<timestamp>.zip in the current directory)")
	return cmd
}

// runBugReport resolves the output path, refuses to overwrite anything
// already there, gathers every source best effort, and writes the bundle.
//
// Every source is optional to Collect, so a store.Open failure, a
// content.Embedded failure, and a prober failure all leave the
// corresponding Sources field zero and let bugreport.Collect turn that into
// a Note instead of a command failure: the most likely reason someone runs
// this command is that something, often the sandbox backend, is not
// working, and a diagnostic tool that refuses to run at that exact moment
// is useless.
func runBugReport(ctx context.Context, w io.Writer, v VersionInfo, prober bugreport.Prober, includeJournal bool, outPath string, writeBundle bugReportWriter) error {
	path := outPath
	if path == "" {
		path = bugreport.BundleName(time.Now())
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return ux.Fail(
			fmt.Sprintf("write the bug report to %s", path),
			err,
			"Check that --out names a path this program can resolve, then try again.",
			"",
		)
	}

	if info, statErr := os.Stat(absPath); statErr == nil && info.IsDir() {
		return ux.Fail(
			fmt.Sprintf("write the bug report to %s", absPath),
			fmt.Errorf("%s is a directory", absPath),
			"That path is a directory. Pass --out a filename, for example `shellforge bug-report --out ./report.zip`.",
			"",
		)
	}

	f, err := os.OpenFile(absPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ux.Fail(
				fmt.Sprintf("write the bug report to %s", absPath),
				err,
				"There is already a file at that path and shellforge will not overwrite it. Pass a different --out, or move the existing file, then run `shellforge bug-report` again.",
				"",
			)
		}
		return ux.Fail(
			fmt.Sprintf("write the bug report to %s", absPath),
			err,
			"Check that you can write to that directory, then run `shellforge bug-report --out <path>` with a path you own.",
			"",
		)
	}

	sources := bugreport.Sources{
		Build: bugreport.Build{
			Version:   v.Version,
			Commit:    v.Commit,
			BuildDate: v.BuildDate,
			GoVersion: goruntime.Version(),
			GOOS:      goruntime.GOOS,
			GOARCH:    goruntime.GOARCH,
		},
		Doctor:         doctor.Run(ctx, sandbox.NewProber()),
		Prober:         prober,
		IncludeJournal: includeJournal,
	}

	if pack, packErr := content.Embedded(); packErr == nil {
		sources.Pack = pack
	}

	if dbPath, dbErr := platform.DatabasePath(); dbErr == nil {
		if s, openErr := store.Open(ctx, dbPath); openErr == nil {
			defer s.Close()
			sources.Store = s
			if profile, profErr := s.EnsureProfile(ctx, "learner"); profErr == nil {
				sources.ProfileID = profile.ID
			}
		}
	}

	report, err := bugreport.Collect(ctx, sources)
	if err != nil {
		// bugreport.Collect's own contract is that this never happens
		// today; if it ever does, the partial file is removed the same
		// way a Write failure below removes it, rather than leaving a
		// zero-byte bundle for a learner to attach by mistake.
		f.Close()
		os.Remove(absPath)
		return ux.Fail(
			"collect the bug report",
			err,
			"Please try `shellforge bug-report` again; if it keeps failing, note the exact error when you file an issue by hand.",
			"",
		)
	}

	logDir, _ := platform.LogDir()

	writeErr := writeBundle(f, report, logDir)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(absPath)
		if writeErr == nil {
			writeErr = closeErr
		}
		return ux.Fail(
			"write the bug report archive",
			writeErr,
			"The partial file was removed. Try `shellforge bug-report --out <path>` pointing at a directory with free space.",
			"",
		)
	}

	fmt.Fprintf(w, "Wrote %s. Nothing was uploaded: this is a file on your machine. Read it before you attach it to an issue.\n", absPath)
	return nil
}

// sandboxBugReportProber adapts internal/sandbox to bugreport.Prober, so
// internal/bugreport never names Docker or WSL. The same seam
// doctor.SandboxProber uses, for the same reason.
type sandboxBugReportProber struct{}

// Probe pings the sandbox for Provisioned, Running and Detail, then
// resolves a backend for its name and its static Capabilities(): resolving
// constructs the backend (a cheap exec.LookPath, never a daemon call) but
// does not provision or start anything, so this stays a read-only probe
// like Ping. A resolution failure returns a zero Sandbox and the error;
// Collect turns that into a Note, never a command failure.
func (sandboxBugReportProber) Probe(ctx context.Context) (bugreport.Sandbox, error) {
	provisioned, running, detail, err := sandbox.NewProber().Ping(ctx)
	if err != nil {
		return bugreport.Sandbox{}, err
	}

	// Want: sandbox.Auto, named explicitly. Options.Want has no default:
	// the zero value is the empty string, and Resolve rejects it through
	// ParseBackend with "unknown --runtime value", which blames a flag the
	// learner never typed. Passing Options{} here made every bundle carry a
	// note about --runtime and no sandbox state at all, on a machine with a
	// perfectly good Docker daemon.
	rt, choice, err := sandbox.Resolve(ctx, sandbox.Options{Want: sandbox.Auto})
	if err != nil {
		return bugreport.Sandbox{}, err
	}
	caps := rt.Capabilities()

	return bugreport.Sandbox{
		Backend:     string(choice.Chosen),
		Detail:      detail,
		Provisioned: provisioned,
		Running:     running,
		Caps: map[string]bool{
			"networking":   caps.Networking,
			"systemd":      caps.Systemd,
			"multi_user":   caps.MultiUser,
			"snapshotting": caps.Snapshotting,
			"privileged":   caps.Privileged,
		},
	}, nil
}
