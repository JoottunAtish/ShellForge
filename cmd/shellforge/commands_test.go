package main

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// documentedVerbs is the full, intentional top-level verb set. Twelve of
// these come straight from the project brief's scope table
// (docs/design/PROJECT-BRIEF.md): doctor, init, play, run, check, hint,
// reset, skip, map, stats, sandbox, author. version and bug-report are kept
// alongside them: version already had real, tested behaviour before this
// ticket, and internal/platform/ux.Render's own fallback message for an
// unexpected error tells the learner to run `shellforge bug-report`, so
// dropping either verb would point that message at a command that does not
// exist.
var documentedVerbs = []string{
	"doctor", "init", "play", "run", "check", "hint", "reset", "skip",
	"map", "stats", "sandbox", "bug-report", "author", "version",
}

// documentedSandboxSubcommands and documentedAuthorSubcommands match the
// group interfaces named in the ticket: "sandbox build|rebuild|destroy|status"
// and "author validate|scaffold|test|record".
var documentedSandboxSubcommands = []string{"build", "rebuild", "destroy", "status"}
var documentedAuthorSubcommands = []string{"validate", "scaffold", "test", "record"}

func topLevelNames(t *testing.T, root *cobra.Command) []string {
	t.Helper()
	names := make([]string, 0, len(root.Commands()))
	for _, c := range root.Commands() {
		names = append(names, c.Name())
	}
	sort.Strings(names)
	return names
}

// TestRegisteredVerbSetMatchesTheDocumentedSet guards against a verb quietly
// disappearing, or an undocumented one quietly appearing.
func TestRegisteredVerbSetMatchesTheDocumentedSet(t *testing.T) {
	root := NewRootCommand(VersionInfo{})

	got := topLevelNames(t, root)
	want := append([]string(nil), documentedVerbs...)
	sort.Strings(want)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("registered verbs = %v, want exactly %v", got, want)
	}
}

func TestEveryVerbFromTheBriefIsRegistered(t *testing.T) {
	root := NewRootCommand(VersionInfo{})
	required := []string{
		"doctor", "init", "play", "run", "check", "hint",
		"reset", "skip", "map", "stats", "sandbox", "author",
	}
	for _, name := range required {
		if findCommand(root, name) == nil {
			t.Errorf("verb %q from the project brief is not registered", name)
		}
	}
}

func findCommand(root *cobra.Command, name string) *cobra.Command {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func subcommandNames(t *testing.T, root *cobra.Command, parent string) []string {
	t.Helper()
	p := findCommand(root, parent)
	if p == nil {
		t.Fatalf("%q is not registered", parent)
	}
	names := make([]string, 0, len(p.Commands()))
	for _, c := range p.Commands() {
		names = append(names, c.Name())
	}
	sort.Strings(names)
	return names
}

func TestSandboxIsAGroupWithItsFourSubcommands(t *testing.T) {
	root := NewRootCommand(VersionInfo{})
	got := subcommandNames(t, root, "sandbox")
	want := append([]string(nil), documentedSandboxSubcommands...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sandbox subcommands = %v, want exactly %v", got, want)
	}
}

func TestAuthorIsAGroupWithItsFourSubcommands(t *testing.T) {
	root := NewRootCommand(VersionInfo{})
	got := subcommandNames(t, root, "author")
	want := append([]string(nil), documentedAuthorSubcommands...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("author subcommands = %v, want exactly %v", got, want)
	}
}

// TestEveryTopLevelCommandHasAShortDescription keeps `shellforge help`
// honest: a command with no Short prints blank in the summary list.
func TestEveryTopLevelCommandHasAShortDescription(t *testing.T) {
	root := NewRootCommand(VersionInfo{})
	for _, c := range root.Commands() {
		if c.Short == "" {
			t.Errorf("command %q has no Short description; it would print blank in help", c.Name())
		}
		if c.GroupID == "" {
			t.Errorf("command %q has no GroupID, so it would never appear under a heading in help", c.Name())
		}
	}
}

// TestNewRootCommandWithEmptyVersionInfoDoesNotPanic is an explicit
// acceptance criterion of #48.
func TestNewRootCommandWithEmptyVersionInfoDoesNotPanic(t *testing.T) {
	root := NewRootCommand(VersionInfo{})
	if root == nil {
		t.Fatal("NewRootCommand returned nil")
	}
}

// execCommand runs root with args and returns the error Execute produced,
// with stdout and stderr captured rather than sent to the real terminal.
func execCommand(t *testing.T, root *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// TestUnimplementedVerbsExplainThemselves checks every stub, at the top
// level and inside the sandbox and author groups, fails through the
// standard user-facing error path rather than a bare cobra error, per
// #48's acceptance criteria.
func TestUnimplementedVerbsExplainThemselves(t *testing.T) {
	stubs := [][]string{
		{"doctor"}, {"init"}, {"play"}, {"check"}, {"hint"}, {"reset"},
		{"skip"}, {"map"}, {"stats"}, {"bug-report"},
		{"sandbox", "build"}, {"sandbox", "rebuild"}, {"sandbox", "destroy"}, {"sandbox", "status"},
		{"author", "scaffold"}, {"author", "record"},
	}

	// `author validate` was a stub when #48 wrote this list and is real as of
	// #53. `author test` is real as of the golden harness. Both are deliberately
	// absent above rather than deleted from the documented set: author_test.go
	// covers validate, and author_test_cmd_test.go covers test. A real verb still
	// errors without arguments, but it errors about its arguments rather than
	// about not existing, so it does not belong in this list.

	for _, args := range stubs {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := NewRootCommand(VersionInfo{})
			_, err := execCommand(t, root, args...)
			if err == nil {
				t.Fatalf("shellforge %s: expected an error from an unimplemented verb", strings.Join(args, " "))
			}
			assertUserFacing(t, err)
			if !strings.Contains(err.Error(), "not built yet") {
				t.Errorf("error should say the verb is not built yet, got: %v", err)
			}
		})
	}
}

func TestVersionCommandPrintsBuildInfo(t *testing.T) {
	root := NewRootCommand(VersionInfo{Version: "v0.1.0-test", Commit: "abc123", BuildDate: "2026-08-12"})
	out, err := execCommand(t, root, "version")
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	for _, want := range []string{"v0.1.0-test", "abc123", "2026-08-12"} {
		if !strings.Contains(out, want) {
			t.Errorf("version output missing %q: %q", want, out)
		}
	}
}

// TestRunCommandDisablesFlagParsing pins the reason parseRunArgs, tested
// separately in cmd_run_test.go, still owns --log-level under cobra.
func TestRunCommandDisablesFlagParsing(t *testing.T) {
	root := NewRootCommand(VersionInfo{})
	run := findCommand(root, "run")
	if run == nil {
		t.Fatal("run is not registered")
	}
	if !run.DisableFlagParsing {
		t.Error("run must disable cobra flag parsing so parseRunArgs keeps owning --log-level")
	}
}

// TestVersionFlagAliasesStillWork is the regression test review on #80
// asked for: the old hand-rolled dispatcher special-cased `-v` and
// `--version` to the `version` verb, and the cobra rewrite silently dropped
// that alias until this fix.
func TestVersionFlagAliasesStillWork(t *testing.T) {
	for _, flag := range []string{"-v", "--version"} {
		t.Run(flag, func(t *testing.T) {
			root := NewRootCommand(VersionInfo{Version: "v0.1.0-test", Commit: "abc123", BuildDate: "2026-08-12"})
			out, err := execCommand(t, root, flag)
			if err != nil {
				t.Fatalf("shellforge %s failed: %v", flag, err)
			}
			for _, want := range []string{"v0.1.0-test", "abc123", "2026-08-12"} {
				if !strings.Contains(out, want) {
					t.Errorf("shellforge %s output missing %q: %q", flag, want, out)
				}
			}
		})
	}
}

// TestBareInvocationShowsHelp asserts a plain `shellforge`, with no verb and
// no -v/--version, still behaves like the old dispatcher's no-args case:
// print usage and exit cleanly, not an error.
func TestBareInvocationShowsHelp(t *testing.T) {
	root := NewRootCommand(VersionInfo{})
	out, err := execCommand(t, root)
	if err != nil {
		t.Fatalf("bare invocation returned an error: %v", err)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("bare invocation did not print usage: %q", out)
	}
}

// TestUnknownCommandAndFlagAreUserFacing is the other regression test review
// on #80 asked for. root.go sets SilenceErrors, so cobra's own parse-time
// errors (an unknown command, an unknown flag) reach main() unwrapped; this
// pins that renderableError, main.go's fix, turns each into a proper
// *ux.Error with a remediation, and that the rendered message never falls
// into ux.Render's "this is unexpected, it is our bug" fallback, which
// would send a learner filing a bug report over their own typo.
func TestUnknownCommandAndFlagAreUserFacing(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown command", []string{"nope"}},
		{"unknown flag", []string{"--bogus"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := NewRootCommand(VersionInfo{})
			_, execErr := execCommand(t, root, tc.args...)
			if execErr == nil {
				t.Fatalf("shellforge %s: expected an error", strings.Join(tc.args, " "))
			}

			var uxErrBefore *ux.Error
			if errors.As(execErr, &uxErrBefore) {
				t.Fatalf("cobra's own error for %q was already a *ux.Error; this test no longer exercises renderableError's wrapping path", tc.args)
			}

			wrapped := renderableError(execErr)
			var uxErr *ux.Error
			if !errors.As(wrapped, &uxErr) {
				t.Fatalf("renderableError did not produce a *ux.Error for %v: %v", tc.args, wrapped)
			}
			if strings.TrimSpace(uxErr.Remediation) == "" {
				t.Errorf("wrapped error for %v carries no remediation", tc.args)
			}

			var out bytes.Buffer
			ux.Render(&out, wrapped)
			rendered := out.String()
			if strings.Contains(rendered, "unexpected") || strings.Contains(rendered, "bug-report") {
				t.Errorf("shellforge %s still renders as an unexpected internal error, misdirecting the learner: %q",
					strings.Join(tc.args, " "), rendered)
			}
			if !strings.Contains(rendered, "shellforge help") {
				t.Errorf("shellforge %s rendered output does not point at `shellforge help`: %q",
					strings.Join(tc.args, " "), rendered)
			}
		})
	}
}

// TestRenderableErrorPassesThroughAnExistingUxError asserts renderableError
// is a no-op for an error a verb's own RunE already wrapped, such as a stub
// command's "not built yet" message: it must not be re-wrapped or lose its
// own remediation and doc anchor.
func TestRenderableErrorPassesThroughAnExistingUxError(t *testing.T) {
	original := ux.Fail("do the thing", nil, "try the other thing", "some-anchor")
	got := renderableError(original)
	var uxErr *ux.Error
	if !errors.As(got, &uxErr) {
		t.Fatalf("renderableError lost the *ux.Error: %v", got)
	}
	if uxErr != original {
		t.Errorf("renderableError re-wrapped an error that was already user-facing")
	}
}
