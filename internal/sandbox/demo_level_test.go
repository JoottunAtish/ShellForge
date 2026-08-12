package sandbox

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// errorLine matches a log line the way the level's own solution does:
// case-insensitively, because the solution is `grep -ri ERROR`. Counting any
// other way would let the fixture disagree with its answer without any test
// noticing.
var errorLine = regexp.MustCompile(`(?i)error`)

// countErrorLines counts lines matching ERROR case-insensitively, which is
// what `grep -ri ERROR ... | wc -l` produces: matching LINES, not matching
// occurrences.
func countErrorLines(content []byte) int {
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(content), "\n"), "\n") {
		if errorLine.MatchString(line) {
			n++
		}
	}
	return n
}

// --------------------------------------------------------------------------
// The asset must be internally consistent with its answer.
//
// This is the project's rule that a level's fixture is verified by counting it
// the way the solution counts it, never by adjusting the check until it
// passes.
// --------------------------------------------------------------------------

// TestFixtureMatchesTheAnswer is the consistency test the ticket requires: the
// three generated log files together contain exactly demoErrorCount lines
// matching ERROR, counted case-insensitively across all three.
func TestFixtureMatchesTheAnswer(t *testing.T) {
	level := Demo()

	if len(level.Files) != len(demoLogFiles) {
		t.Fatalf("Demo() materializes %d files, want %d", len(level.Files), len(demoLogFiles))
	}

	total := 0
	for _, f := range level.Files {
		got := countErrorLines(f.Content)
		total += got
		if got == 0 {
			t.Errorf("%s contains no ERROR lines at all", f.Path)
		}
	}

	if total != demoErrorCount {
		t.Errorf("the generated logs contain %d ERROR lines, but the level's answer is %d; the asset and its answer disagree", total, demoErrorCount)
	}
	if level.Answer() != fmt.Sprint(demoErrorCount) {
		t.Errorf("Answer() = %q, want %q", level.Answer(), fmt.Sprint(demoErrorCount))
	}
}

// TestPerFileErrorCountMatchesItsBudget checks each file individually, so a
// generator bug that moved ERROR lines between files while keeping the total
// right still fails.
func TestPerFileErrorCountMatchesItsBudget(t *testing.T) {
	level := Demo()
	for i, spec := range demoLogFiles {
		content := level.Files[i].Content
		if got := countErrorLines(content); got != spec.errorLines {
			t.Errorf("%s has %d ERROR lines, want %d", spec.name, got, spec.errorLines)
		}
		if got := len(strings.Split(strings.TrimRight(string(content), "\n"), "\n")); got != spec.lines {
			t.Errorf("%s has %d lines, want %d", spec.name, got, spec.lines)
		}
	}
}

// TestErrorBudgetSumsToTheAnswer pins the arithmetic that makes the answer a
// property of the generator rather than an observation about it.
func TestErrorBudgetSumsToTheAnswer(t *testing.T) {
	sum := 0
	for _, f := range demoLogFiles {
		sum += f.errorLines
		if f.errorLines >= f.lines {
			t.Errorf("%s budgets %d ERROR lines out of %d total; there is no room for anything else", f.name, f.errorLines, f.lines)
		}
	}
	if sum != demoErrorCount {
		t.Errorf("the per-file ERROR budgets sum to %d, but demoErrorCount is %d", sum, demoErrorCount)
	}
}

// TestNoiseNeverMatchesTheAnswer is the trap this fixture is most likely to
// fall into. The solution greps case-insensitively, so one stray "Error" or
// "errors" in a noise message would add a matching line and make the level's
// own answer wrong, with nothing else in the suite noticing.
func TestNoiseNeverMatchesTheAnswer(t *testing.T) {
	for _, n := range demoNoiseLines {
		if errorLine.MatchString(n.text) {
			t.Errorf("noise message %q matches ERROR case-insensitively; it would inflate the answer", n.text)
		}
		if errorLine.MatchString(n.level) {
			t.Errorf("noise level %q matches ERROR case-insensitively; it would inflate the answer", n.level)
		}
	}
	for _, m := range demoErrorMessages {
		if strings.Count(strings.ToLower(m), "error") > 0 {
			t.Errorf("error message %q contains ERROR in its text; the level label is what should match, so this makes the fixture harder to reason about", m)
		}
	}
}

// TestSetupIsDeterministic asserts the generator is a pure function of its
// seed. Setup must be deterministic and offline: the same level must produce
// byte-identical assets on every machine and on every call.
func TestSetupIsDeterministic(t *testing.T) {
	first, second := Demo(), Demo()
	for i := range first.Files {
		if !bytesEqual(first.Files[i].Content, second.Files[i].Content) {
			t.Errorf("%s differs between two Demo() calls; the generator is not deterministic", first.Files[i].Path)
		}
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestEveryAssetLivesUnderTheLevelRoot asserts the manifest cannot reach
// outside the level's world, which is what makes teardown sufficient.
func TestEveryAssetLivesUnderTheLevelRoot(t *testing.T) {
	level := Demo()
	for _, f := range level.Files {
		if !strings.HasPrefix(f.Path, level.Root+"/") {
			t.Errorf("asset %q is not under the level root %q", f.Path, level.Root)
		}
		if f.Owner != demoOwner {
			t.Errorf("asset %q is owned by %q, want %q", f.Path, f.Owner, demoOwner)
		}
		if f.Mode.Perm() == 0 {
			t.Errorf("asset %q has mode 0, so the learner could not read it", f.Path)
		}
	}
}

// --------------------------------------------------------------------------
// Refusal tests for the destructive path.
//
// These are mandatory, not optional, and they are the half that protects the
// learner's own machine. The success path for deletion is the easy half.
// --------------------------------------------------------------------------

// TestValidateLevelRootRefusals is the refusal table. Every row must be
// refused before any path reaches a recursive delete.
func TestValidateLevelRootRefusals(t *testing.T) {
	cases := []struct {
		name string
		root string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"root of the filesystem", "/"},
		{"current directory", "."},
		{"a tilde, which no shell expands here", "~"},
		{"a tilde path", "~/quest"},
		{"the learner home directory itself", "/home/learner"},
		{"the learner home with a trailing slash", "/home/learner/"},
		{"traversal out of the home directory", "/home/learner/../etc"},
		{"traversal from inside the level root", "/home/learner/quest/../../etc"},
		{"a bare traversal", ".."},
		{"another user home directory", "/home/atish/quest"},
		{"an absolute path outside the home", "/etc"},
		{"a system directory", "/usr/lib"},
		{"a relative path", "quest"},
		{"a windows host path", `C:\Users\Admin`},
		{"a windows host path with forward slashes", "C:/Users/Admin"},
		{"a path that only looks like the prefix", "/home/learner2/quest"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateLevelRoot(tc.root); err == nil {
				t.Fatalf("validateLevelRoot(%q) accepted a path it must refuse", tc.root)
			}
		})
	}
}

// TestValidateLevelRootAccepts covers the other side, so the guard is not
// refusing everything and passing its refusal table by accident.
func TestValidateLevelRootAccepts(t *testing.T) {
	cases := []string{
		"/home/learner/quest",
		"/home/learner/quest/",
		"/home/learner/quest/nested",
		demoRoot,
	}
	for _, root := range cases {
		t.Run(root, func(t *testing.T) {
			if err := validateLevelRoot(root); err != nil {
				t.Fatalf("validateLevelRoot(%q) refused a valid level root: %v", root, err)
			}
		})
	}
}

// TestResolveLevelRootRefusesASymlinkEscape is the refusal a host-side string
// check cannot make on its own. A learner can create a symlink during a
// level, so the root that was safe when the level loaded can point somewhere
// else by the time teardown runs.
func TestResolveLevelRootRefusesASymlinkEscape(t *testing.T) {
	cases := []struct {
		name     string
		resolved string
	}{
		{"escapes to another user home", "/home/atish/quest"},
		{"escapes to a system directory", "/etc"},
		{"escapes to the learner home itself", "/home/learner"},
		{"resolves somewhere else under the home", "/home/learner/elsewhere"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeSession{
				exec: func(argv []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
					if argv[0] == "readlink" {
						return runtime.ExecResult{Stdout: []byte(tc.resolved + "\n")}, nil
					}
					return runtime.ExecResult{}, fmt.Errorf("unexpected argv %v", argv)
				},
			}
			if _, err := resolveLevelRoot(context.Background(), s, demoRoot); err == nil {
				t.Fatalf("resolveLevelRoot accepted a root that resolves to %q inside the sandbox", tc.resolved)
			}
		})
	}
}

// TestTeardownRefusesBeforeItDeletes asserts the guard runs first. A refused
// root must produce zero rm calls, not a refusal reported after the fact.
//
// It asserts on chown as well as rm. The chown is recursive and runs as root,
// so on the wrong path it can lock the learner out of directories it does not
// own the contents of; it is a state-changing call and it must be behind the
// same guard. The guard does hold today, because resolveLevelRoot runs before
// either call, but asserting only on rm would let a future reordering that
// hoisted the chown above the guard pass this test unchanged.
func TestTeardownRefusesBeforeItDeletes(t *testing.T) {
	s := &fakeSession{
		exec: func(argv []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
			if argv[0] == "readlink" {
				// Echo back whatever was asked, so resolution is not what
				// refuses here: the lexical guard is.
				return runtime.ExecResult{Stdout: []byte(argv[len(argv)-1] + "\n")}, nil
			}
			return runtime.ExecResult{}, nil
		},
	}

	level := Demo()
	level.Root = "/home/learner/../etc"

	if err := level.Teardown(context.Background(), s); err == nil {
		t.Fatal("Teardown accepted a level root containing a traversal")
	}
	for _, argv := range s.argvs {
		if argv[0] == "rm" || argv[0] == "chown" {
			t.Fatalf("Teardown ran %v after the guard should have refused", argv)
		}
	}
}

// TestTeardownDeletesOnlyTheLevelRootInsideTheSandbox pins the shape of the
// one destructive call this package makes: rm, recursive, with an explicit
// end-of-options marker, on exactly the validated root, inside the sandbox.
func TestTeardownDeletesOnlyTheLevelRootInsideTheSandbox(t *testing.T) {
	s := newEchoingSession()
	if err := Demo().Teardown(context.Background(), s); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	var removals [][]string
	for _, argv := range s.argvs {
		if argv[0] == "rm" {
			removals = append(removals, argv)
		}
	}
	if len(removals) != 1 {
		t.Fatalf("Teardown made %d rm calls, want exactly 1: %v", len(removals), removals)
	}
	want := []string{"rm", "-rf", "--", demoRoot}
	if strings.Join(removals[0], " ") != strings.Join(want, " ") {
		t.Errorf("Teardown ran %v, want %v", removals[0], want)
	}
}

// TestTeardownRemovesAsTheLearnerNotRoot is a regression test for a real
// defect, found by running the golden sequence against a live container rather
// than by any unit test.
//
// The sandbox drops every Linux capability and adds back only CHOWN and FOWNER,
// so root inside it does not hold CAP_DAC_OVERRIDE and does not bypass
// ordinary permission checks. Unlinking a file needs write permission on its
// directory, and the level root is learner-owned, so a root `rm -rf` failed
// with Permission denied on every single entry.
func TestTeardownRemovesAsTheLearnerNotRoot(t *testing.T) {
	s := newEchoingSession()
	if err := Demo().Teardown(context.Background(), s); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	user, ok := s.userFor("rm")
	if !ok {
		t.Fatal("Teardown never ran rm")
	}
	if user == "root" {
		t.Error("Teardown removes the level root as root, which fails with Permission denied " +
			"because the sandbox drops CAP_DAC_OVERRIDE; it must run as the learner that owns the root")
	}
	if user != demoUser {
		t.Errorf("Teardown removed the level root as %q, want %q", user, demoUser)
	}
}

// TestTeardownChownsAsRoot asserts the repair step runs as the one user that
// can perform it. CAP_CHOWN is one of the two capabilities the sandbox keeps,
// and the learner cannot chown a directory they do not already own.
func TestTeardownChownsAsRoot(t *testing.T) {
	s := newEchoingSession()
	if err := Demo().Teardown(context.Background(), s); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	user, ok := s.userFor("chown")
	if !ok {
		t.Fatal("Teardown never ran chown, so a root-owned level root left by a half-finished Setup stays undeletable")
	}
	if user != "root" {
		t.Errorf("Teardown chowned as %q, want root", user)
	}
}

// TestTeardownToleratesAChownFailure asserts the repair step is best effort.
// The common case is that the level root does not exist at all, and chown
// rightly fails on a missing path; that must not fail the teardown.
func TestTeardownToleratesAChownFailure(t *testing.T) {
	s := &fakeSession{
		exec: func(argv []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
			switch argv[0] {
			case "readlink":
				return runtime.ExecResult{Stdout: []byte(argv[len(argv)-1] + "\n")}, nil
			case "chown":
				return runtime.ExecResult{ExitCode: 1, Stderr: []byte("chown: cannot access: No such file or directory")}, nil
			default:
				return runtime.ExecResult{}, nil
			}
		},
	}
	if err := Demo().Teardown(context.Background(), s); err != nil {
		t.Fatalf("Teardown failed on a chown that could not find the level root: %v", err)
	}
}

// --------------------------------------------------------------------------
// Setup ordering
// --------------------------------------------------------------------------

// TestSetupIsTeardownFirst asserts setup is idempotent by construction: the
// removal happens before the push, so running it twice cannot leave stale
// state from a previous attempt.
func TestSetupIsTeardownFirst(t *testing.T) {
	s := newEchoingSession()
	if err := Demo().Setup(context.Background(), s); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	rmAt, pushAt, chownAt := -1, -1, -1
	for i, op := range s.ops {
		switch {
		case strings.HasPrefix(op, "exec rm"):
			rmAt = i
		case op == "pushfiles":
			pushAt = i
		case strings.HasPrefix(op, "exec chown"):
			chownAt = i
		}
	}

	if rmAt < 0 || pushAt < 0 || chownAt < 0 {
		t.Fatalf("Setup did not run all three of rm, PushFiles and chown: %v", s.ops)
	}
	if rmAt > pushAt {
		t.Errorf("Setup pushed files before tearing down (rm at %d, push at %d): %v", rmAt, pushAt, s.ops)
	}
	if chownAt < pushAt {
		t.Errorf("Setup chowned the root before pushing files, so the pushed directories stay root-owned: %v", s.ops)
	}
}

// TestSetupGivesTheLevelRootToTheLearner is a regression test for a real
// defect. PushFiles creates the ancestor directories of every entry as uid 0
// and only chmods them, so without an explicit chown the learner cannot
// create report.txt in their own level root and the level is unsolvable.
func TestSetupGivesTheLevelRootToTheLearner(t *testing.T) {
	s := newEchoingSession()
	if err := Demo().Setup(context.Background(), s); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	for _, argv := range s.argvs {
		if argv[0] != "chown" {
			continue
		}
		if strings.Join(argv, " ") == strings.Join([]string{"chown", "-R", demoOwner, "--", demoRoot}, " ") {
			return
		}
	}
	t.Fatalf("Setup never chowned %s to %s, so the learner could not write report.txt into it: %v", demoRoot, demoOwner, s.argvs)
}

// TestSetupMaterializesEveryAsset asserts the manifest actually reaches the
// session rather than being built and dropped.
func TestSetupMaterializesEveryAsset(t *testing.T) {
	s := newEchoingSession()
	level := Demo()
	if err := level.Setup(context.Background(), s); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(s.pushed) != 1 {
		t.Fatalf("Setup called PushFiles %d times, want 1", len(s.pushed))
	}
	if got, want := len(s.pushed[0].Files), len(level.Files); got != want {
		t.Errorf("Setup pushed %d files, want %d", got, want)
	}
}

// --------------------------------------------------------------------------
// Check
// --------------------------------------------------------------------------

// TestCheck covers the pass and fail sides against a fake session, including
// the near-misses a real learner produces.
func TestCheck(t *testing.T) {
	level := Demo()
	answer := level.Answer()

	cases := []struct {
		name     string
		exitCode int
		stdout   string
		want     bool
	}{
		{"the right answer", 0, answer, true},
		{"the right answer with a trailing newline", 0, answer + "\n", true},
		{"the right answer with surrounding whitespace", 0, "  " + answer + "  \n", true},
		{"report.txt is missing", 1, "", false},
		{"report.txt is empty", 0, "", false},
		{"report.txt holds only a newline", 0, "\n", false},
		{"the count is one too low", 0, "146", false},
		{"the count is one too high", 0, "148", false},
		{"the answer plus prose", 0, "the answer is " + answer, false},
		{"the wc output including a filename", 0, answer + " logs/app-1.log", false},
		{"a number on a second line as well", 0, answer + "\n12\n", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeSession{
				exec: func(argv []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
					if argv[0] != "cat" {
						return runtime.ExecResult{}, fmt.Errorf("unexpected argv %v", argv)
					}
					if got, want := argv[len(argv)-1], level.ReportPath(); got != want {
						return runtime.ExecResult{}, fmt.Errorf("Check read %q, want %q", got, want)
					}
					return runtime.ExecResult{ExitCode: tc.exitCode, Stdout: []byte(tc.stdout)}, nil
				},
			}

			passed, message, err := level.Check(context.Background(), s)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if passed != tc.want {
				t.Errorf("Check passed = %v, want %v (report.txt held %q)", passed, tc.want, tc.stdout)
			}
			if message == "" {
				t.Error("Check returned an empty message; a learner needs to be told what happened either way")
			}
		})
	}
}

// TestCheckNeverMutates asserts the check is read-only. A check that writes to
// the sandbox is a bug, and this is the cheap version of the filesystem hash
// the golden runner does.
func TestCheckNeverMutates(t *testing.T) {
	mutators := []string{"rm", "mv", "cp", "chmod", "chown", "touch", "mkdir", "tee", "truncate", "dd", "ln", "sed"}

	s := newEchoingSession()
	if _, _, err := Demo().Check(context.Background(), s); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(s.pushed) != 0 {
		t.Errorf("Check called PushFiles, which writes to the sandbox")
	}
	for _, argv := range s.argvs {
		for _, m := range mutators {
			if argv[0] == m {
				t.Errorf("Check ran %q, which can mutate sandbox state: %v", m, argv)
			}
		}
		if strings.Contains(strings.Join(argv, " "), ">") {
			t.Errorf("Check ran something containing a redirect: %v", argv)
		}
	}
}

// TestCheckReportsARuntimeFailureAsAnError asserts a broken runtime is not
// silently reported to the learner as a failed level.
func TestCheckReportsARuntimeFailureAsAnError(t *testing.T) {
	boom := errors.New("the daemon went away")
	s := &fakeSession{
		exec: func(_ []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
			return runtime.ExecResult{}, boom
		},
	}
	_, _, err := Demo().Check(context.Background(), s)
	if !errors.Is(err, boom) {
		t.Fatalf("Check error = %v, want it to wrap %v", err, boom)
	}
}

// TestCheckRunsAsTheLearner asserts the check sees the level the way the
// learner left it. Reading report.txt as root would pass on a file the
// learner could not actually have created.
func TestCheckRunsAsTheLearner(t *testing.T) {
	var got string
	s := &fakeSession{
		exec: func(_ []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
			got = opts.User
			return runtime.ExecResult{ExitCode: 1}, nil
		},
	}
	if _, _, err := Demo().Check(context.Background(), s); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got != demoUser {
		t.Errorf("Check ran as %q, want %q", got, demoUser)
	}
}

// TestCheckBoundsItsRead asserts the check reads with a timeout.
//
// The path being read is one the learner owns, and cat is not guaranteed to
// return: report.txt as a named pipe with no writer blocks forever. Without
// ExecOpts.Timeout the docker backend never reports TimedOut at all, so the
// read has no bound, the control channel is wedged for the rest of the
// session, and Check's own TimedOut branch is unreachable code.
func TestCheckBoundsItsRead(t *testing.T) {
	var got runtime.ExecOpts
	s := &fakeSession{
		exec: func(_ []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
			got = opts
			return runtime.ExecResult{ExitCode: 1}, nil
		},
	}
	if _, _, err := Demo().Check(context.Background(), s); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Timeout <= 0 {
		t.Error("Check read report.txt with no ExecOpts.Timeout. A report.txt that is a named pipe then blocks the read " +
			"forever, which wedges the control channel and means no later check is ever served.")
	}
}

// TestCheckReportsATimeout asserts the bound above is reported to the learner
// with something they can act on, rather than as a silent level failure.
func TestCheckReportsATimeout(t *testing.T) {
	s := &fakeSession{
		exec: func(_ []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
			return runtime.ExecResult{ExitCode: -1, TimedOut: true}, nil
		},
	}
	passed, _, err := Demo().Check(context.Background(), s)
	if err == nil {
		t.Fatal("Check reported no error for a read that timed out")
	}
	if passed {
		t.Error("Check passed on a read that timed out")
	}
	if !strings.Contains(err.Error(), "rm ~/quest/report.txt") {
		t.Errorf("the timeout error does not tell the learner what to run next: %v", err)
	}
}

// TestCaseSensitivityDoesNotChangeTheAnswer records the fact the failure
// messages depend on.
//
// The only thing in this fixture that matches ERROR is the literal level
// label, so counting case-sensitively and case-insensitively give the same
// number. That is why no failure message asks a stuck learner whether their
// search was case-insensitive: it cannot be the cause, and sending them after
// it wastes the one message they read.
//
// If a future fixture grows lowercase error text, this test fails, and the
// wording can be revisited deliberately instead of becoming accidentally true.
func TestCaseSensitivityDoesNotChangeTheAnswer(t *testing.T) {
	insensitive, sensitive := 0, 0
	for _, f := range Demo().Files {
		for _, line := range strings.Split(strings.TrimRight(string(f.Content), "\n"), "\n") {
			if errorLine.MatchString(line) {
				insensitive++
			}
			if strings.Contains(line, "ERROR") {
				sensitive++
			}
		}
	}
	if insensitive != sensitive {
		t.Errorf("case-insensitive counting finds %d lines and case-sensitive finds %d. "+
			"Case sensitivity now changes the answer, so the failure messages may need to mention it.", insensitive, sensitive)
	}
	if sensitive != demoErrorCount {
		t.Errorf("case-sensitive counting finds %d lines, but the answer is %d", sensitive, demoErrorCount)
	}
}

// TestCheckFailureMessagesNameRealFailureModes is the regression test for a
// failure message that sent a stuck learner after something that could not be
// the cause.
//
// The original message asked "Is your search case-insensitive?" for every
// failure, which by TestCaseSensitivityDoesNotChangeTheAnswer above can never
// be why the count is wrong. It also reported "the number is off" for a
// report.txt holding the RIGHT number in the wrong shape, which is the one
// case where recounting is exactly the wrong next move.
func TestCheckFailureMessagesNameRealFailureModes(t *testing.T) {
	level := Demo()

	cases := []struct {
		name      string
		exitCode  int
		stdout    string
		wantInMsg string
		wantNotIn string
	}{
		{
			name:      "missing says so and does not talk about the count",
			exitCode:  1,
			wantInMsg: "no ~/quest/report.txt",
			wantNotIn: "case-insensitive",
		},
		{
			name:      "empty is told apart from wrong",
			stdout:    "\n",
			wantInMsg: "empty",
			wantNotIn: "case-insensitive",
		},
		{
			name:      "a wrong number quotes what is there",
			stdout:    "12",
			wantInMsg: `"12"`,
			wantNotIn: "case-insensitive",
		},
		{
			name:      "the right count in the wrong shape quotes it rather than blaming the number",
			stdout:    level.Answer() + " logs/app-1.log",
			wantInMsg: level.Answer() + " logs/app-1.log",
			wantNotIn: "case-insensitive",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeSession{
				exec: func(_ []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
					return runtime.ExecResult{ExitCode: tc.exitCode, Stdout: []byte(tc.stdout)}, nil
				},
			}
			passed, message, err := level.Check(context.Background(), s)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if passed {
				t.Fatalf("Check passed on %q", tc.stdout)
			}
			if !strings.Contains(message, tc.wantInMsg) {
				t.Errorf("the message does not mention %q: %s", tc.wantInMsg, message)
			}
			if strings.Contains(strings.ToLower(message), tc.wantNotIn) {
				t.Errorf("the message asks about %q, which cannot be the cause: %s", tc.wantNotIn, message)
			}
		})
	}
}

// TestCheckFailureMessagesNeverLeakTheAnswer asserts a learner cannot read the
// count off a failure. The wrong-content message quotes what they wrote, so
// this only holds while nothing else in the message names the total.
func TestCheckFailureMessagesNeverLeakTheAnswer(t *testing.T) {
	level := Demo()

	for _, stdout := range []string{"", "\n", "12", "not a number"} {
		s := &fakeSession{
			exec: func(_ []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
				return runtime.ExecResult{Stdout: []byte(stdout)}, nil
			},
		}
		_, message, err := level.Check(context.Background(), s)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if strings.Contains(message, level.Answer()) {
			t.Errorf("the failure message for %q contains the answer %q: %s", stdout, level.Answer(), message)
		}
	}
}

// TestQuoteReportContentTruncates asserts a learner who redirects a whole log
// file into report.txt does not get it read back at them.
func TestQuoteReportContentTruncates(t *testing.T) {
	long := strings.Repeat("x", 4000)
	got := quoteReportContent(long)
	if len(got) > 200 {
		t.Errorf("quoteReportContent returned %d characters for a 4000 character file", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("a truncated value does not say so: %s", got)
	}

	// A multi-byte rune straddling the cut must not be split into an invalid
	// sequence. strconv.Quote would escape the broken half as \x.., which is
	// unreadable noise in a message aimed at a beginner.
	runeHeavy := strings.Repeat("é", 100)
	if got := quoteReportContent(runeHeavy); strings.Contains(got, `\x`) {
		t.Errorf("truncation split a rune: %s", got)
	}
}

// --------------------------------------------------------------------------
// The fake session
// --------------------------------------------------------------------------

// fakeSession is a runtime.Session that records what it was asked to do and
// answers from an injected function. It exists so every test above runs with
// no Docker daemon.
type fakeSession struct {
	exec   func(argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error)
	argvs  [][]string
	users  []string
	ops    []string
	pushed []runtime.FileManifest
	closed int
}

// userFor reports the User the fake was asked to run the first call to the
// named binary as.
func (f *fakeSession) userFor(bin string) (string, bool) {
	for i, argv := range f.argvs {
		if argv[0] == bin {
			return f.users[i], true
		}
	}
	return "", false
}

var _ runtime.Session = (*fakeSession)(nil)

// newEchoingSession returns a fake whose readlink echoes the path it was
// given, so resolution succeeds and the lexical guard is what decides.
func newEchoingSession() *fakeSession {
	return &fakeSession{
		exec: func(argv []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
			if argv[0] == "readlink" {
				return runtime.ExecResult{Stdout: []byte(argv[len(argv)-1] + "\n")}, nil
			}
			return runtime.ExecResult{}, nil
		},
	}
}

func (f *fakeSession) Exec(_ context.Context, argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
	f.argvs = append(f.argvs, argv)
	f.users = append(f.users, opts.User)
	f.ops = append(f.ops, "exec "+strings.Join(argv, " "))
	if f.exec == nil {
		return runtime.ExecResult{}, nil
	}
	return f.exec(argv, opts)
}

func (f *fakeSession) Attach(_ context.Context, _ runtime.AttachOpts) (runtime.PTY, error) {
	f.ops = append(f.ops, "attach")
	return nil, errors.New("fakeSession: Attach is not implemented")
}

func (f *fakeSession) PushFiles(_ context.Context, m runtime.FileManifest) error {
	f.pushed = append(f.pushed, m)
	f.ops = append(f.ops, "pushfiles")
	return nil
}

func (f *fakeSession) PullFile(_ context.Context, p string) ([]byte, error) {
	f.ops = append(f.ops, "pullfile "+p)
	return nil, errors.New("fakeSession: PullFile is not implemented")
}

func (f *fakeSession) Close() error {
	f.closed++
	f.ops = append(f.ops, "close")
	return nil
}
