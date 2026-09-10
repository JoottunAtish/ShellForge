package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/pty"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// Nothing in this file may reach a Docker daemon. Every test either exercises
// pure argument or protocol logic, or drives the control channel against the
// fake session at the bottom of the file.

func TestParseRunArgs(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantLevel string
		wantDebug bool
	}{
		{"only the level", []string{"demo"}, "demo", false},
		{"debug with an equals sign", []string{"demo", "--log-level=debug"}, "demo", true},
		{"debug as two arguments", []string{"demo", "--log-level", "debug"}, "demo", true},
		{"the flag before the level", []string{"--log-level=debug", "demo"}, "demo", true},
		{"a non-debug log level", []string{"demo", "--log-level=info"}, "demo", false},
		{"another level id parses fine here", []string{"nav-01"}, "nav-01", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseRunArgs(tc.args, "nav-01")
			if err != nil {
				t.Fatalf("parseRunArgs(%q): %v", tc.args, err)
			}
			if opts.levelID != tc.wantLevel {
				t.Errorf("levelID = %q, want %q", opts.levelID, tc.wantLevel)
			}
			if opts.debug != tc.wantDebug {
				t.Errorf("debug = %v, want %v", opts.debug, tc.wantDebug)
			}
		})
	}
}

// TestParseRunArgsLiveCheck covers AC8: `run` with live checking disabled
// must behave exactly as it did before this ticket, and the switch defaults
// to on so the feature reaches a learner who never reads a flag list.
func TestParseRunArgsLiveCheck(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantLive bool
		wantErr  bool
	}{
		{"no flag defaults to on", []string{"nav-01"}, true, false},
		{"off with an equals sign", []string{"nav-01", "--live-check=off"}, false, false},
		{"off as two arguments", []string{"nav-01", "--live-check", "off"}, false, false},
		{"on with an equals sign", []string{"nav-01", "--live-check=on"}, true, false},
		{"on as two arguments", []string{"nav-01", "--live-check", "on"}, true, false},
		{"false is accepted as off", []string{"nav-01", "--live-check=false"}, false, false},
		{"true is accepted as on", []string{"nav-01", "--live-check=true"}, true, false},
		{"the flag before the level id", []string{"--live-check=off", "nav-01"}, false, false},
		{"alongside --log-level", []string{"nav-01", "--log-level=debug", "--live-check=off"}, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseRunArgs(tc.args, "nav-01")
			if err != nil {
				t.Fatalf("parseRunArgs(%q): %v", tc.args, err)
			}
			if opts.live != tc.wantLive {
				t.Errorf("live = %v, want %v", opts.live, tc.wantLive)
			}
		})
	}

	t.Run("a bogus value joined", func(t *testing.T) {
		_, err := parseRunArgs([]string{"nav-01", "--live-check=sometimes"}, "nav-01")
		if err == nil {
			t.Fatal("parseRunArgs accepted a bogus --live-check value")
		}
		assertUserFacing(t, err)
	})

	t.Run("a bogus value split", func(t *testing.T) {
		_, err := parseRunArgs([]string{"nav-01", "--live-check", "sometimes"}, "nav-01")
		if err == nil {
			t.Fatal("parseRunArgs accepted a bogus --live-check value")
		}
		assertUserFacing(t, err)
	})

	t.Run("--live-check with nothing after it", func(t *testing.T) {
		_, err := parseRunArgs([]string{"nav-01", "--live-check"}, "nav-01")
		if err == nil {
			t.Fatal("parseRunArgs accepted --live-check with no value")
		}
		assertUserFacing(t, err)
	})
}

func TestParseRunArgsRejections(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no level at all", nil},
		{"only a flag", []string{"--log-level=debug"}},
		{"an unknown flag", []string{"demo", "--wat"}},
		{"a short flag", []string{"demo", "-v"}},
		{"--log-level with nothing after it", []string{"demo", "--log-level"}},
		{"two level ids", []string{"demo", "nav-01"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseRunArgs(tc.args, "nav-01")
			if err == nil {
				t.Fatalf("parseRunArgs(%q) accepted arguments it should refuse", tc.args)
			}
			assertUserFacing(t, err)
		})
	}
}

// TestCmdRunRefusesAnUnknownLevel asserts an id that is not in the pack fails
// before anything is provisioned, and that the message names the ids that are.
//
// The id here used to be nav-01, back when the only playable level was the
// hardcoded demo. nav-01 is a real level now, so the test needs an id that is
// genuinely absent or it would assert the opposite of what it says.
func TestCmdRunRefusesAnUnknownLevel(t *testing.T) {
	err := cmdRun(context.Background(), []string{"nav-99"})
	if err == nil {
		t.Fatal("cmdRun accepted a level that is not in the pack")
	}
	assertUserFacing(t, err)
	if !strings.Contains(err.Error(), "nav-99") {
		t.Errorf("the error should name the level the user asked for, got: %v", err)
	}

	var uxErr *ux.Error
	if errors.As(err, &uxErr) {
		if uxErr.DocAnchor != docAnchorLevelNotFound {
			t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, docAnchorLevelNotFound)
		}
		// The remediation lists the real ids, which is what makes a typo
		// recoverable without reading the documentation.
		for _, want := range []string{"nav-01", "files-04"} {
			if !strings.Contains(uxErr.Remediation, want) {
				t.Errorf("the remediation does not list the real level %q: %q", want, uxErr.Remediation)
			}
		}
		// An id that is not in the pack must never be offered back as the
		// fix for a typo. "demo" is the one worth naming: it was a real,
		// specially cased level until #96 deleted it, so it is the id most
		// likely to creep back into a suggestion list by habit.
		if strings.Contains(uxErr.Remediation, "demo") {
			t.Errorf("the remediation offers an id that is not in the pack: %q", uxErr.Remediation)
		}
	}
}

// TestCheckInteractiveShellSupported pins the host guard on both sides of the
// platform split, because the failure it prevents is a multi-minute image build
// followed by an error the user cannot act on.
//
// creack/pty's Windows StartWithSize returns ErrUnsupported unconditionally, so
// Attach cannot work on a Windows host at all. The runtime contract suite has
// no Attach assertion, which is why it passes on Windows and does not catch
// this.
func TestCheckInteractiveShellSupported(t *testing.T) {
	err := checkInteractiveShellSupported("nav-01")

	if goruntime.GOOS == "windows" {
		if err == nil {
			t.Fatal("the Windows guard let `run` through, so it would fail at Attach after building the image")
		}
		assertUserFacing(t, err)

		var uxErr *ux.Error
		if errors.As(err, &uxErr) {
			if uxErr.DocAnchor != "windows-needs-wsl" {
				t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, "windows-needs-wsl")
			}
			if !strings.Contains(uxErr.Remediation, "WSL") {
				t.Errorf("the remediation does not tell the user to use WSL: %q", uxErr.Remediation)
			}
		}
		return
	}

	if err != nil {
		t.Fatalf("the guard refused on %s, where a host pseudo terminal works: %v", goruntime.GOOS, err)
	}
}

// TestCmdRunChecksTheLevelBeforeThePlatform asserts an unknown level is
// reported as an unknown level on every host, rather than being masked by the
// Windows guard.
func TestCmdRunChecksTheLevelBeforeThePlatform(t *testing.T) {
	err := cmdRun(context.Background(), []string{"nav-99"})
	if err == nil {
		t.Fatal("cmdRun accepted an unknown level")
	}
	if strings.Contains(err.Error(), "Windows") {
		t.Errorf("an unknown level was reported as a platform problem: %v", err)
	}
}

// assertUserFacing asserts an error is one a learner can act on: routed
// through ux.Fail, and carrying a remediation. Non-negotiable 6 in CLAUDE.md.
func assertUserFacing(t *testing.T, err error) {
	t.Helper()
	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("error is not a *ux.Error, so it would reach the terminal as a bare Go error: %v", err)
	}
	if strings.TrimSpace(uxErr.Remediation) == "" {
		t.Errorf("error %q carries no remediation", uxErr.Op)
	}
}

// --------------------------------------------------------------------------
// The control channel wire format
// --------------------------------------------------------------------------

func TestParseControlRequest(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantVerb string
		wantArgs string
	}{
		{"a bare verb", "check\n", "check", ""},
		{"the shim's own format, with an empty argument field", "check\t\n", "check", ""},
		{"a verb with an argument", "hint\t--reveal\n", "hint", "--reveal"},
		{"no trailing newline", "check", "check", ""},
		{"surrounding whitespace", "  check\t \n", "check", ""},
		{"an empty line yields no verb", "\n", "", ""},
		{"whitespace only yields no verb", "   \n", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verb, args := parseControlRequest(tc.line)
			if verb != tc.wantVerb {
				t.Errorf("verb = %q, want %q", verb, tc.wantVerb)
			}
			if args != tc.wantArgs {
				t.Errorf("args = %q, want %q", args, tc.wantArgs)
			}
		})
	}
}

// controlVerbLevel is the level the two tests below answer verbs for.
//
// A real level out of the embedded pack rather than a hand-built one: `brief`
// renders the authored briefing and prints the authored checklist, so a
// synthetic level would only prove the renderer can echo a Go string literal.
func controlVerbLevel(t *testing.T) *content.Level {
	t.Helper()

	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	level, ok := pack.Level("pipe-05")
	if !ok {
		t.Fatal("pipe-05 is not in the embedded pack")
	}
	return level
}

// controlVerbResponder builds the production responder over a fake session and
// a fake verifier, so the reply a learner would see is asserted with no
// container and no check engine.
//
// game.Verifier is an interface declared in the consuming package for exactly
// this: Session's orchestration is testable with a two-method fake. Run
// answers with the canned result the subtest wants, which is what decides
// whether the learner reads PASS or NOT YET.
func controlVerbResponder(t *testing.T, result verify.LevelResult) *gameResponder {
	t.Helper()

	level := controlVerbLevel(t)
	session, err := game.NewSession(game.Config{
		Level: level,
		Sess:  &fakeSession{},
		Verifier: &fakeVerifier{
			run: func(context.Context, []verify.Check, verify.Env) verify.LevelResult {
				return result
			},
		},
	})
	if err != nil {
		t.Fatalf("build the level: %v", err)
	}
	return &gameResponder{checker: session, level: level, color: false}
}

// TestHandleControlVerb drives the verb table against a fake session and a fake
// verifier, so the reply a learner would see is asserted without a container.
func TestHandleControlVerb(t *testing.T) {
	passing := verify.LevelResult{
		Passed: true,
		Objectives: []verify.ObjectiveResult{
			{ID: "obj1", Text: "report.txt holds the total ERROR count", Status: verify.StatusPass},
		},
	}
	failing := verify.LevelResult{
		Objectives: []verify.ObjectiveResult{
			{ID: "obj1", Text: "report.txt holds the total ERROR count", Status: verify.StatusFail, Message: "report.txt is missing"},
		},
		PrimaryFailure: &verify.ObjectiveResult{
			ID: "obj1", Text: "report.txt holds the total ERROR count", Status: verify.StatusFail, Message: "report.txt is missing",
		},
	}
	inconclusive := verify.LevelResult{
		Objectives: []verify.ObjectiveResult{
			{ID: "obj1", Text: "report.txt holds the total ERROR count", Status: verify.StatusError, Message: "the daemon went away"},
		},
		PrimaryFailure: &verify.ObjectiveResult{
			ID: "obj1", Text: "report.txt holds the total ERROR count", Status: verify.StatusError, Message: "the daemon went away",
		},
	}

	cases := []struct {
		name    string
		verb    string
		result  verify.LevelResult
		want    string
		notWant string
	}{
		{name: "check passes on the right answer", verb: "check", result: passing, want: "PASS:"},
		{name: "check fails on a wrong answer", verb: "check", result: failing, want: "NOT YET:", notWant: "PASS:"},
		{name: "a failing check shows the authored on_fail", verb: "check", result: failing, want: "report.txt is missing"},
		{name: "a check that could not decide says so, and is not a wrong answer", verb: "check", result: inconclusive, want: "not a wrong answer", notWant: "PASS:"},
		{name: "brief reprints the briefing", verb: "brief", want: "Billing service"},
		{name: "brief reprints the objective checklist", verb: "brief", want: "report.txt holds the total ERROR count"},
		// This responder is built from a game.Session with no orchestrator
		// behind it, which is what a test that only cares about check and
		// brief needs. Both verbs say so plainly rather than panicking on
		// the nil, and render_hint_test.go and render_reset_test.go cover
		// what they do when there IS an orchestrator.
		{name: "hint says so when there is no ladder behind it", verb: "hint", want: "`hint` is not available"},
		{name: "reset says so when there is nothing to rebuild", verb: "reset", want: "`reset` is not available"},
		{name: "an unknown verb says so", verb: "wat", want: `unknown request "wat"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			responder := controlVerbResponder(t, tc.result)
			reply := responder.Reply(context.Background(), tc.verb, "")

			if !strings.Contains(reply, tc.want) {
				t.Errorf("reply = %q, want it to contain %q", reply, tc.want)
			}
			if tc.notWant != "" && strings.Contains(reply, tc.notWant) {
				t.Errorf("reply = %q, want it NOT to contain %q", reply, tc.notWant)
			}
			if !strings.HasSuffix(reply, "\n") {
				t.Errorf("reply %q does not end in a newline, so the shim's output would run into the next prompt", reply)
			}
		})
	}
}

// TestHandleControlVerbReportsABrokenRuntime asserts a runtime failure is
// reported to the learner rather than silently read as a failed level.
//
// There are two ways one reaches the learner and both are covered, because
// they are rendered by different code. A check that could not determine an
// answer comes back inside a LevelResult and is rendered as an inconclusive
// verdict; a run that could not be attempted at all comes back as an error and
// is rendered by renderCheckError. Neither may ever say PASS.
func TestHandleControlVerbReportsABrokenRuntime(t *testing.T) {
	t.Run("a check that could not determine an answer", func(t *testing.T) {
		responder := controlVerbResponder(t, verify.LevelResult{
			Objectives: []verify.ObjectiveResult{
				{ID: "obj1", Text: "report.txt holds the total ERROR count", Status: verify.StatusError, Message: "the daemon went away"},
			},
			PrimaryFailure: &verify.ObjectiveResult{
				ID: "obj1", Text: "report.txt holds the total ERROR count", Status: verify.StatusError, Message: "the daemon went away",
			},
		})

		reply := responder.Reply(context.Background(), "check", "")
		if !strings.Contains(reply, "could not be checked") {
			t.Errorf("reply = %q, want the objective line to say it could not be checked", reply)
		}
		if !strings.Contains(reply, "not a wrong answer") {
			t.Errorf("reply = %q, want it to tell the learner this is not a wrong answer", reply)
		}
		if strings.Contains(reply, "PASS") {
			t.Errorf("a broken runtime must never report PASS: %q", reply)
		}
	})

	t.Run("a run that could not be attempted", func(t *testing.T) {
		reply := renderCheckError(errors.New("the daemon went away"), "pipe-05")
		if !strings.Contains(reply, "could not run") {
			t.Errorf("reply = %q, want it to say the check could not run", reply)
		}
		if strings.Contains(reply, "PASS") {
			t.Errorf("a broken runtime must never report PASS: %q", reply)
		}
	})
}

// fakeVerifier is a game.Verifier that answers from injected functions, so a
// game.Session can be built and checked with no engine and no sandbox.
type fakeVerifier struct {
	build func(specs []verify.Spec) ([]verify.Check, error)
	run   func(ctx context.Context, checks []verify.Check, env verify.Env) verify.LevelResult
}

var _ game.Verifier = (*fakeVerifier)(nil)

func (f *fakeVerifier) Build(specs []verify.Spec) ([]verify.Check, error) {
	if f.build == nil {
		// One Check per Spec, per Verifier.Build's own contract: NewSession
		// now refuses a Verifier that returns a different count, so a
		// default that always answered nil (however many specs it was
		// given) would fail every level this fake builds, not exercise the
		// one this test wants.
		checks := make([]verify.Check, 0, len(specs))
		for range specs {
			checks = append(checks, nil)
		}
		return checks, nil
	}
	return f.build(specs)
}

func (f *fakeVerifier) Run(ctx context.Context, checks []verify.Check, env verify.Env) verify.LevelResult {
	if f.run == nil {
		return verify.LevelResult{}
	}
	return f.run(ctx, checks, env)
}

// --------------------------------------------------------------------------
// Control channel setup
// --------------------------------------------------------------------------

// TestPrepareControlChannelRecreatesTheFifos pins the argv shapes. The
// container outlives any one run, so a stale pair has to be removed rather
// than reused, and mkfifo must come after the removal or it fails on an
// existing path.
func TestPrepareControlChannelRecreatesTheFifos(t *testing.T) {
	s := &fakeSession{}
	const dir = "/home/learner/.shellforge"
	const req, res = dir + "/control.req", dir + "/control.res"

	if err := prepareControlChannel(context.Background(), s, dir, req, res); err != nil {
		t.Fatalf("prepareControlChannel: %v", err)
	}
	if len(s.argvs) != 2 {
		t.Fatalf("prepareControlChannel made %d calls, want 2: %v", len(s.argvs), s.argvs)
	}

	// The advance sentinel is removed with them. A stale one, left by a
	// level whose shell ended before it read the file, would make the next
	// level end the moment the learner typed `next`.
	wantRemove := []string{"rm", "-f", "--", req, res, dir + "/" + advanceSentinel}
	wantCreate := []string{"mkfifo", "--", req, res}
	if got := strings.Join(s.argvs[0], " "); got != strings.Join(wantRemove, " ") {
		t.Errorf("first call = %q, want %q", got, strings.Join(wantRemove, " "))
	}
	if got := strings.Join(s.argvs[1], " "); got != strings.Join(wantCreate, " ") {
		t.Errorf("second call = %q, want %q", got, strings.Join(wantCreate, " "))
	}
}

func TestPrepareControlChannelFailsOnANonZeroExit(t *testing.T) {
	s := &fakeSession{
		exec: func(argv []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
			if argv[0] == "mkfifo" {
				return runtime.ExecResult{ExitCode: 1, Stderr: []byte("mkfifo: cannot create fifo")}, nil
			}
			return runtime.ExecResult{}, nil
		},
	}
	err := prepareControlChannel(context.Background(), s, "/a", "/a/req", "/a/res")
	if err == nil {
		t.Fatal("prepareControlChannel reported success even though mkfifo exited non-zero")
	}
}

// --------------------------------------------------------------------------
// Draining the control loop on the way out
// --------------------------------------------------------------------------

// TestWaitForControlLoop covers both sides of the bound.
//
// The exit path has to wait for the control loop, because the loop is what
// kills the sandbox-side process it started: returning without waiting races
// process exit and can leave a `cat` holding the FIFO open in a container that
// outlives the run. It also has to give up, because a loop that will not come
// back must not be able to hold the learner's prompt hostage.
func TestWaitForControlLoop(t *testing.T) {
	t.Run("returns true once the loop has finished", func(t *testing.T) {
		served := make(chan struct{})
		close(served)
		if !waitForControlLoop(served, 5*time.Second) {
			t.Error("waitForControlLoop reported a timeout for a loop that had already returned")
		}
	})

	t.Run("gives up rather than blocking the exit path", func(t *testing.T) {
		served := make(chan struct{}) // never closed: the loop is wedged
		start := time.Now()
		if waitForControlLoop(served, 20*time.Millisecond) {
			t.Error("waitForControlLoop claimed a wedged loop had returned")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("waitForControlLoop took %s to give up on a 20ms budget", elapsed)
		}
	})
}

// --------------------------------------------------------------------------
// The debug event log
// --------------------------------------------------------------------------

// TestLogCommandEventsEndsEveryLineWithCarriageReturn is a regression guard
// for a specific rendering bug. Run has the host terminal in raw mode, so the
// line discipline that would normally supply the carriage return is off and a
// bare "\n" renders as a staircase.
func TestLogCommandEventsEndsEveryLineWithCarriageReturn(t *testing.T) {
	events := make(chan pty.CommandEvent, 2)
	events <- pty.CommandEvent{ExitCode: 0, Cwd: "/home/learner/quest", Duration: 12 * time.Millisecond}
	events <- pty.CommandEvent{ExitCode: 1, Cwd: "/home/learner", Duration: 3 * time.Millisecond}
	close(events)

	var buf bytes.Buffer
	logCommandEvents(context.Background(), events, &buf, true, nil)

	out := buf.String()
	if out == "" {
		t.Fatal("nothing was logged with debug enabled")
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		if strings.Contains(line, "\n") {
			t.Errorf("line %q contains a bare newline; in raw mode it must end \\r\\n", line)
		}
	}
	if !strings.Contains(out, "exit=1") || !strings.Contains(out, "/home/learner/quest") {
		t.Errorf("the log dropped a field it is meant to record: %q", out)
	}
}

// TestLogCommandEventsNeverLogsRawCommandText asserts the one field that could
// carry a password the learner typed stays out of the log.
func TestLogCommandEventsNeverLogsRawCommandText(t *testing.T) {
	const secret = "mysql -pSuperSecret123"

	events := make(chan pty.CommandEvent, 1)
	events <- pty.CommandEvent{Raw: secret, ExitCode: 0, Cwd: "/home/learner"}
	close(events)

	var buf bytes.Buffer
	logCommandEvents(context.Background(), events, &buf, true, nil)

	if strings.Contains(buf.String(), "SuperSecret") {
		t.Errorf("the debug log leaked raw command text: %q", buf.String())
	}
}

// TestLogCommandEventsDrainsWithoutDebug asserts the channel is drained even
// when nothing is printed. A consumer that stops reading makes the
// multiplexer drop events and warn about it on the learner's terminal.
func TestLogCommandEventsDrainsWithoutDebug(t *testing.T) {
	events := make(chan pty.CommandEvent, 1)
	events <- pty.CommandEvent{ExitCode: 0}
	close(events)

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		logCommandEvents(context.Background(), events, &buf, false, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("logCommandEvents did not drain the channel and return")
	}
	if buf.Len() != 0 {
		t.Errorf("logCommandEvents printed %q without debug enabled", buf.String())
	}
}

// TestLogCommandEventsCallsOnCommandOncePerEvent is AC1's trigger half: the
// journal wiring and the drain are already proved elsewhere, so what this
// pins is that every event on the channel calls onCommand exactly once, in
// order, and that logCommandEvents still returns when the channel closes.
func TestLogCommandEventsCallsOnCommandOncePerEvent(t *testing.T) {
	events := make(chan pty.CommandEvent, 3)
	events <- pty.CommandEvent{ExitCode: 0}
	events <- pty.CommandEvent{ExitCode: 1}
	events <- pty.CommandEvent{ExitCode: 0}
	close(events)

	var calls int32
	onCommand := func() { atomic.AddInt32(&calls, 1) }

	done := make(chan struct{})
	go func() {
		logCommandEvents(context.Background(), events, io.Discard, false, onCommand)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("logCommandEvents did not return after its channel closed")
	}

	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("onCommand was called %d times, want exactly 3", got)
	}
}

// TestLogCommandEventsToleratesANilOnCommand covers the default, disabled
// path: `run` without --live-check passes no callback, and that must not
// panic on the very first event.
func TestLogCommandEventsToleratesANilOnCommand(t *testing.T) {
	events := make(chan pty.CommandEvent, 1)
	events <- pty.CommandEvent{ExitCode: 0}
	close(events)

	done := make(chan struct{})
	go func() {
		logCommandEvents(context.Background(), events, io.Discard, false, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("logCommandEvents did not return with a nil onCommand")
	}
}

// TestLogCommandEventsKeepsDrainingWhenOnCommandIsSaturated is design point
// 1(b) made into a test: pty.Mux.emit drops events when its consumer falls
// behind, so logCommandEvents must never block waiting for onCommand, even
// when nothing is reading what onCommand tries to report.
func TestLogCommandEventsKeepsDrainingWhenOnCommandIsSaturated(t *testing.T) {
	const n = 200
	events := make(chan pty.CommandEvent, n)
	for i := 0; i < n; i++ {
		events <- pty.CommandEvent{ExitCode: 0}
	}
	close(events)

	// A capacity-1 channel nobody ever reads: the first non-blocking send
	// fills it, and every send after that hits the default branch. If
	// onCommand's own contract were violated by a blocking implementation,
	// this would hang instead of returning.
	saturated := make(chan struct{}, 1)
	onCommand := func() {
		select {
		case saturated <- struct{}{}:
		default:
		}
	}

	done := make(chan struct{})
	go func() {
		logCommandEvents(context.Background(), events, io.Discard, false, onCommand)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("logCommandEvents did not drain 200 events with a saturated onCommand consumer")
	}
}

func TestLogCommandEventsStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan pty.CommandEvent) // never written, never closed

	done := make(chan struct{})
	go func() {
		logCommandEvents(ctx, events, &bytes.Buffer{}, true, nil)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("logCommandEvents ignored context cancellation and would outlive the session")
	}
}

// --------------------------------------------------------------------------
// Wiring live checking into the run flow
// --------------------------------------------------------------------------
//
// play itself is not driven directly here: every fakeSession in this package
// refuses Attach (there is no fake host pseudo terminal in this repository),
// so play always returns at that step before it would ever reach the point
// this wiring lives at. The decision play makes there, whether to start live
// checking at all, is exactly what startLiveChecking factors out, and that
// factoring is what makes the decision testable without a PTY.

// fakeLiveLevel is a playable that also implements liveLevel, recording
// whether StartLive and CommandRan were reached.
type fakeLiveLevel struct {
	plainPlayable
	startLiveCalls  int
	commandRanCalls int
}

func (f *fakeLiveLevel) StartLive(context.Context) (<-chan []verify.ObjectiveResult, func()) {
	f.startLiveCalls++
	return nil, func() {}
}

func (f *fakeLiveLevel) CommandRan() { f.commandRanCalls++ }

// plainPlayable implements playable and nothing else, standing in for a
// level that predates live checking.
type plainPlayable struct{}

func (plainPlayable) LevelID() string                 { return "nav-01" }
func (plainPlayable) Root() string                    { return "/home/learner/quest" }
func (plainPlayable) StateDir() string                { return "" }
func (plainPlayable) Setup(context.Context) error     { return nil }
func (plainPlayable) Teardown(context.Context) error  { return nil }
func (plainPlayable) Responder(bool) controlResponder { return nil }
func (plainPlayable) PrintBriefing(io.Writer, bool)   {}

func TestStartLiveCheckingHonoursOptsLive(t *testing.T) {
	t.Run("live checking off starts nothing", func(t *testing.T) {
		f := &fakeLiveLevel{}
		onCommand, transitions, wait := startLiveChecking(context.Background(), runOptions{live: false}, f)
		if onCommand != nil {
			t.Error("startLiveChecking returned a callback with live checking off")
		}
		if transitions != nil {
			t.Error("startLiveChecking returned a transition channel with live checking off")
		}
		if wait == nil {
			t.Fatal("startLiveChecking returned a nil wait function with live checking off; it must be a callable no-op")
		}
		wait()
		if f.startLiveCalls != 0 || f.commandRanCalls != 0 {
			t.Errorf("StartLive was called %d times and CommandRan %d times with live checking off, want 0 and 0",
				f.startLiveCalls, f.commandRanCalls)
		}
	})

	t.Run("live checking on starts the level's live checker", func(t *testing.T) {
		f := &fakeLiveLevel{}
		onCommand, _, wait := startLiveChecking(context.Background(), runOptions{live: true}, f)
		if onCommand == nil {
			t.Fatal("no onCommand callback with live checking on")
		}
		onCommand()
		if f.commandRanCalls != 1 {
			t.Errorf("onCommand called CommandRan %d times, want 1", f.commandRanCalls)
		}
		if f.startLiveCalls != 1 {
			t.Errorf("StartLive was called %d times, want 1", f.startLiveCalls)
		}
		if wait == nil {
			t.Fatal("startLiveChecking returned a nil wait function with live checking on")
		}
		wait()
	})

	t.Run("a playable that does not implement liveLevel is left alone", func(t *testing.T) {
		onCommand, transitions, wait := startLiveChecking(context.Background(), runOptions{live: true}, plainPlayable{})
		if onCommand != nil || transitions != nil {
			t.Error("startLiveChecking found a live checker on a playable that does not implement one")
		}
		if wait == nil {
			t.Fatal("startLiveChecking returned a nil wait function for a playable with no live checker")
		}
		wait()
	})
}

// --------------------------------------------------------------------------
// Printing a live pass's transitions
// --------------------------------------------------------------------------

// TestPrintLiveTransitionsWritesEachBatchAndStopsOnClose covers the normal
// path: one batch arrives, is rendered, and the goroutine returns once the
// channel closes, matching Run's own contract for Transitions().
func TestPrintLiveTransitionsWritesEachBatchAndStopsOnClose(t *testing.T) {
	transitions := make(chan []verify.ObjectiveResult, 1)
	transitions <- []verify.ObjectiveResult{
		{ID: "location", Text: "quest/answer.txt holds the folder you are standing in", Status: verify.StatusPass},
	}
	close(transitions)

	var rec recordingInterjector
	done := make(chan struct{})
	go func() {
		printLiveTransitions(context.Background(), transitions, &rec, false)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("printLiveTransitions did not return when its channel closed")
	}

	if !strings.Contains(rec.joined(), "quest/answer.txt holds the folder you are standing in") {
		t.Errorf("nothing was printed for the transition: %q", rec.joined())
	}
}

// recordingInterjector stands in for pty.Mux in the printer's own tests. The
// printer's whole job is now deciding WHAT to hand Interject and when to
// stop, so what it hands over is what these tests assert on; where and when
// those bytes reach the terminal is internal/pty's contract and is tested
// there.
type recordingInterjector struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recordingInterjector) Interject(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
}

func (r *recordingInterjector) joined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.msgs, "")
}

// TestPrintLiveTransitionsStopsOnContextCancellation covers the disabled
// path: a nil or never-written channel must not stop ctx cancellation from
// ending the goroutine.
func TestPrintLiveTransitionsStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var transitions chan []verify.ObjectiveResult // nil: the disabled-live-checking shape

	done := make(chan struct{})
	go func() {
		printLiveTransitions(ctx, transitions, &recordingInterjector{}, false)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("printLiveTransitions ignored context cancellation")
	}
}

// --------------------------------------------------------------------------
// The fake session
// --------------------------------------------------------------------------

// fakeSession is a runtime.Session that records argv vectors and answers from
// an injected function, so every test in this file runs with no daemon.
type fakeSession struct {
	exec  func(argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error)
	argvs [][]string
}

var _ runtime.Session = (*fakeSession)(nil)

func (f *fakeSession) Exec(_ context.Context, argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
	f.argvs = append(f.argvs, argv)
	if f.exec == nil {
		return runtime.ExecResult{}, nil
	}
	return f.exec(argv, opts)
}

func (f *fakeSession) Attach(_ context.Context, _ runtime.AttachOpts) (runtime.PTY, error) {
	return nil, errors.New("fakeSession: Attach is not implemented")
}

func (f *fakeSession) PushFiles(_ context.Context, _ runtime.FileManifest) error { return nil }

func (f *fakeSession) PullFile(_ context.Context, _ string) ([]byte, error) {
	return nil, errors.New("fakeSession: PullFile is not implemented")
}

func (f *fakeSession) Close() error { return nil }

// --------------------------------------------------------------------------
// The briefing reprinted after a clear
// --------------------------------------------------------------------------

// briefingPlayable is a playable whose PrintBriefing writes a real level's
// briefing through the real renderer, which is what makes the assertions
// below about CRLF and the leading blank line worth anything.
type briefingPlayable struct {
	plainPlayable
	level *content.Level
}

func (b briefingPlayable) PrintBriefing(w io.Writer, color bool) {
	printBriefing(w, b.level, defaultBriefWidth, color)
}

func TestClearBannerRestoresWhatTheBriefingShowed(t *testing.T) {
	lvl := briefingPlayable{level: &content.Level{
		ID:      "nav-01",
		Title:   "First Contact",
		Version: 1,
		Briefing: "## 08:15, Monday. Your first day.\n\n" +
			"There is a folder called `quest` in your home directory.\n",
		Objectives: []content.Objective{
			{ID: "location", Text: "quest/answer.txt holds the folder you are standing in"},
			{ID: "used-pwd", Text: "Found it with a single command", Optional: true},
		},
	}}

	got := clearBanner(lvl, false)

	// Everything the learner lost has to be in it: the title, the prose, and
	// the checklist, which is the only place a level's deliverable filenames
	// appear when the briefing does not name them.
	for _, want := range []string{
		"First Contact",
		"first day",
		"quest/answer.txt holds the folder you are standing in",
		"Found it with a single command (bonus)",
		"Type `check`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the reprinted briefing is missing %q:\n%q", want, got)
		}
	}
}

// TestClearBannerIsCRLFTerminated pins the one mechanical requirement
// internal/pty puts on this text. It is written into a terminal held in raw
// mode, where a bare LF moves down without returning the carriage, so a
// briefing that kept its plain newlines would come out as a staircase down
// the right of the screen.
func TestClearBannerIsCRLFTerminated(t *testing.T) {
	lvl := briefingPlayable{level: &content.Level{
		ID: "nav-01", Title: "First Contact", Version: 1,
		Briefing:   "Line one.\n\nLine two.\n",
		Objectives: []content.Objective{{ID: "a", Text: "do the thing"}},
	}}

	got := clearBanner(lvl, false)
	for i := 0; i < len(got); i++ {
		if got[i] == '\n' && (i == 0 || got[i-1] != '\r') {
			t.Fatalf("byte %d is an LF with no CR before it:\n%q", i, got)
		}
	}
}

// TestClearBannerStartsAtTheTopOfTheScreen pins the other difference from the
// pre-attach briefing. That one opens with a blank line because it prints
// partway down a screen that already has output on it; this one is written to
// a screen that was just erased, with the cursor at the top left, where a
// leading blank line is just a wasted row.
func TestClearBannerStartsAtTheTopOfTheScreen(t *testing.T) {
	lvl := briefingPlayable{level: &content.Level{
		ID: "nav-01", Title: "First Contact", Version: 1,
		Briefing:   "Line one.\n",
		Objectives: []content.Objective{{ID: "a", Text: "do the thing"}},
	}}

	got := clearBanner(lvl, false)
	if strings.HasPrefix(got, "\r\n") || strings.HasPrefix(got, "\n") {
		t.Errorf("the reprinted briefing opens with a blank line:\n%q", got)
	}
}

// TestClearBannerIsEmptyWithNoBriefing keeps the off switch honest: a
// playable with nothing to reprint must hand internal/pty an empty string,
// which turns the behaviour off rather than printing an empty frame after
// every clear.
func TestClearBannerIsEmptyWithNoBriefing(t *testing.T) {
	if got := clearBanner(plainPlayable{}, false); got != "" {
		t.Errorf("clearBanner on a playable with no briefing = %q, want empty", got)
	}
}
