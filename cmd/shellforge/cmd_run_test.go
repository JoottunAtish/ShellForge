package main

import (
	"bytes"
	"context"
	"errors"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/pty"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/sandbox"
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
		{"just the level", []string{"demo"}, "demo", false},
		{"debug with an equals sign", []string{"demo", "--log-level=debug"}, "demo", true},
		{"debug as two arguments", []string{"demo", "--log-level", "debug"}, "demo", true},
		{"the flag before the level", []string{"--log-level=debug", "demo"}, "demo", true},
		{"a non-debug log level", []string{"demo", "--log-level=info"}, "demo", false},
		{"another level id parses fine here", []string{"nav-01"}, "nav-01", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseRunArgs(tc.args)
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
			_, err := parseRunArgs(tc.args)
			if err == nil {
				t.Fatalf("parseRunArgs(%q) accepted arguments it should refuse", tc.args)
			}
			assertUserFacing(t, err)
		})
	}
}

// TestCmdRunRefusesAnUnknownLevel asserts the only level id that reaches the
// Docker path is the one that exists. Every other id must fail before
// provisioning anything.
func TestCmdRunRefusesAnUnknownLevel(t *testing.T) {
	err := cmdRun(context.Background(), []string{"nav-01"})
	if err == nil {
		t.Fatal("cmdRun accepted a level that does not exist yet")
	}
	assertUserFacing(t, err)
	if !strings.Contains(err.Error(), "nav-01") {
		t.Errorf("the error should name the level the user asked for, got: %v", err)
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
	err := checkInteractiveShellSupported()

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
	err := cmdRun(context.Background(), []string{"nav-01"})
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

// TestHandleControlVerb drives the verb table against a fake session, so the
// reply a learner would see is asserted without a container.
func TestHandleControlVerb(t *testing.T) {
	level := sandbox.Demo()

	cases := []struct {
		name       string
		verb       string
		catExit    int
		catStdout  string
		wantPrefix string
	}{
		{"check passes on the right answer", "check", 0, level.Answer(), "PASS:"},
		{"check fails on a wrong answer", "check", 0, "1", "FAIL:"},
		{"check fails when report.txt is missing", "check", 1, "", "FAIL:"},
		{"brief reprints the briefing", "brief", 0, "", "##"},
		{"hint is honest about not existing", "hint", 0, "", "`hint` is not built yet"},
		{"reset is honest about not existing", "reset", 0, "", "`reset` is not built yet"},
		{"an unknown verb says so", "wat", 0, "", `unknown request "wat"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeSession{
				exec: func(argv []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
					return runtime.ExecResult{ExitCode: tc.catExit, Stdout: []byte(tc.catStdout)}, nil
				},
			}
			reply := handleControlVerb(context.Background(), s, level, tc.verb, "")
			if !strings.HasPrefix(strings.TrimSpace(reply), tc.wantPrefix) {
				t.Errorf("reply = %q, want it to start with %q", reply, tc.wantPrefix)
			}
			if !strings.HasSuffix(reply, "\n") {
				t.Errorf("reply %q does not end in a newline, so the shim's output would run into the next prompt", reply)
			}
		})
	}
}

// TestHandleControlVerbReportsABrokenRuntime asserts a runtime failure is
// reported to the learner rather than silently read as a failed level.
func TestHandleControlVerbReportsABrokenRuntime(t *testing.T) {
	s := &fakeSession{
		exec: func(_ []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
			return runtime.ExecResult{}, errors.New("the daemon went away")
		},
	}
	reply := handleControlVerb(context.Background(), s, sandbox.Demo(), "check", "")
	if !strings.Contains(reply, "could not run") {
		t.Errorf("reply = %q, want it to say the check could not run", reply)
	}
	if strings.HasPrefix(reply, "PASS") {
		t.Error("a broken runtime must never report PASS")
	}
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
	const req, res = "/opt/shellforge/state/control.req", "/opt/shellforge/state/control.res"

	if err := prepareControlChannel(context.Background(), s, req, res); err != nil {
		t.Fatalf("prepareControlChannel: %v", err)
	}
	if len(s.argvs) != 2 {
		t.Fatalf("prepareControlChannel made %d calls, want 2: %v", len(s.argvs), s.argvs)
	}

	wantRemove := []string{"rm", "-f", "--", req, res}
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
	err := prepareControlChannel(context.Background(), s, "/a/req", "/a/res")
	if err == nil {
		t.Fatal("prepareControlChannel reported success even though mkfifo exited non-zero")
	}
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
	logCommandEvents(context.Background(), events, &buf, true)

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
	logCommandEvents(context.Background(), events, &buf, true)

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
		logCommandEvents(context.Background(), events, &buf, false)
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

func TestLogCommandEventsStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan pty.CommandEvent) // never written, never closed

	done := make(chan struct{})
	go func() {
		logCommandEvents(ctx, events, &bytes.Buffer{}, true)
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
// The briefing
// --------------------------------------------------------------------------

// TestPrintBriefingShowsTheObjective asserts the learner is told what to do
// and how to submit before the prompt appears.
func TestPrintBriefingShowsTheObjective(t *testing.T) {
	var buf bytes.Buffer
	printBriefing(&buf, sandbox.Demo())
	out := buf.String()

	for _, want := range []string{"report.txt", "logs/", "check"} {
		if !strings.Contains(out, want) {
			t.Errorf("the briefing never mentions %q: %s", want, out)
		}
	}
}

// TestBriefingDoesNotLeakTheAnswer asserts the briefing poses the problem
// rather than solving it.
func TestBriefingDoesNotLeakTheAnswer(t *testing.T) {
	level := sandbox.Demo()
	var buf bytes.Buffer
	printBriefing(&buf, level)

	if strings.Contains(buf.String(), level.Answer()) {
		t.Errorf("the briefing contains the answer %q", level.Answer())
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
