package sandbox

import (
	"context"
	"os/exec"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/pty"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/docker"
)

// The Docker-dependent half of this ticket's test plan. Everything here skips
// cleanly without a Linux Docker daemon, so the suite is safe to run on any
// machine, including a CI runner with no daemon and a Docker Desktop in
// Windows-container mode.
//
// These tests import internal/runtime/docker, which internal/archtest permits:
// internal/sandbox is in its runtimeImplAllowed set, and collectImports skips
// _test.go files regardless.

// goldenSandboxName is the container and image this file provisions.
//
// It is deliberately NOT the production "shellforge-sandbox" and not
// runtimetest.SandboxName either. A test that provisioned the production
// sandbox would leave a container behind and could race the learner's own
// session; sharing the contract suite's name would race that suite, because
// `go test ./...` runs different packages in parallel. Docker's layer cache
// makes the extra tag cheap after the first build.
const goldenSandboxName = "shellforge-demogolden"

// goldenTimeout bounds the whole sequence. The first run includes an image
// build, which is minutes, not seconds.
const goldenTimeout = 15 * time.Minute

// requireLinuxDocker skips unless a Docker daemon running Linux containers is
// available, matching the probe in internal/runtime/docker/contract_test.go.
// A Docker Desktop in Windows-container mode answers `docker version` happily
// and then cannot pull a Linux-only base image at all.
func requireLinuxDocker(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not on PATH")
	}
	probe := exec.CommandContext(context.Background(), "docker", "version", "--format", "{{.Server.Os}}")
	out, err := probe.Output()
	if err != nil {
		t.Skip("the Docker daemon is not answering (`docker version` failed)")
	}
	if serverOS := strings.TrimSpace(string(out)); serverOS != "linux" {
		t.Skipf("the Docker daemon runs %s containers; the sandbox image is Linux only", serverOS)
	}
}

// goldenSession provisions the golden sandbox and returns an open session on
// it, cleaning both up even when the test fails.
func goldenSession(t *testing.T, ctx context.Context) runtime.Session {
	t.Helper()
	requireLinuxDocker(t)

	rt, err := docker.New(goldenSandboxName, goldenSandboxName)
	if err != nil {
		t.Fatalf("docker.New(%q): %v", goldenSandboxName, err)
	}
	if err := rt.Provision(ctx, runtime.ImageSpec{Name: goldenSandboxName}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	// t.Cleanup, not defer: a t.Fatal below skips a deferred call in the
	// calling function but always runs cleanups.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := rt.Destroy(cleanupCtx); err != nil {
			t.Errorf("Destroy left %s behind: %v", goldenSandboxName, err)
		}
	})

	sess, err := rt.StartSession(ctx, runtime.SessionSpec{
		User:     demoUser,
		WorkDir:  "/home/learner",
		StateDir: demoStateDir,
		Env:      map[string]string{"SF_STATE": demoStateDir},
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() {
		if err := sess.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return sess
}

// TestGoldenSequence is the contract every level must satisfy, prototyped here
// ahead of the Day 6 golden runner:
//
//  1. fresh sandbox, run Setup
//  2. run the check. It must FAIL. A level that passes before the learner has
//     done anything has broken checks, and this step catches more real bugs
//     than step 4.
//  3. run the solution as the learner
//  4. run the check. It must PASS.
//  5. run teardown. The level root is gone.
func TestGoldenSequence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess := goldenSession(t, ctx)
	level := Demo()

	if err := level.Setup(ctx, sess); err != nil {
		t.Fatalf("step 1, Setup: %v", err)
	}

	passed, message, err := level.Check(ctx, sess)
	if err != nil {
		t.Fatalf("step 2, Check: %v", err)
	}
	if passed {
		t.Fatalf("step 2: the check PASSED before the solution ran, so it does not actually verify anything: %s", message)
	}

	// A login shell, because the solution is written the way a learner would
	// type it, with a ~ that only a shell expands. HOME is passed explicitly
	// rather than relying on what docker exec -u infers.
	res, err := sess.Exec(ctx, []string{"/bin/bash", "-lc", level.Solution}, runtime.ExecOpts{
		User: demoUser,
		Env:  map[string]string{"HOME": "/home/learner"},
	})
	if err != nil {
		t.Fatalf("step 3, run the solution: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("step 3: the solution exited %d, so this level's own answer does not work: %s",
			res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}

	passed, message, err = level.Check(ctx, sess)
	if err != nil {
		t.Fatalf("step 4, Check: %v", err)
	}
	if !passed {
		// Read back what the solution actually produced. Without this the
		// failure says only "did not pass", and the interesting question is
		// always what the number came out as.
		got, _ := sess.Exec(ctx, []string{"cat", "--", level.ReportPath()}, runtime.ExecOpts{User: demoUser})
		t.Fatalf("step 4: the check FAILED after the solution ran.\n"+
			"  check said: %s\n"+
			"  report.txt holds: %q\n"+
			"  the level expects: %q\n"+
			"  Do NOT loosen the check to make this pass. If the solution and the check disagree, the level is wrong.",
			message, strings.TrimSpace(string(got.Stdout)), level.Answer())
	}

	if err := level.Teardown(ctx, sess); err != nil {
		t.Fatalf("step 5, Teardown: %v", err)
	}
	gone, err := sess.Exec(ctx, []string{"test", "-e", level.Root}, runtime.ExecOpts{User: demoUser})
	if err != nil {
		t.Fatalf("step 5, confirm the level root is gone: %v", err)
	}
	if gone.ExitCode == 0 {
		t.Errorf("step 5: %s still exists after Teardown", level.Root)
	}
}

// TestSetupIsIdempotent asserts running Setup twice is safe, which is what
// teardown-first buys. A second run must leave exactly the pristine level, not
// the learner's edits from the first.
func TestSetupIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess := goldenSession(t, ctx)
	level := Demo()

	if err := level.Setup(ctx, sess); err != nil {
		t.Fatalf("first Setup: %v", err)
	}
	// Solve it, so there is state for the second Setup to clear.
	if _, err := sess.Exec(ctx, []string{"/bin/bash", "-lc", level.Solution}, runtime.ExecOpts{
		User: demoUser,
		Env:  map[string]string{"HOME": "/home/learner"},
	}); err != nil {
		t.Fatalf("run the solution: %v", err)
	}
	if err := level.Setup(ctx, sess); err != nil {
		t.Fatalf("second Setup: %v", err)
	}

	passed, _, err := level.Check(ctx, sess)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if passed {
		t.Error("the check still passes after a second Setup, so setup did not clear the previous attempt")
	}
}

// TestTheLearnerOwnsTheLevelRoot is the regression test for a defect that
// makes the level unsolvable. PushFiles creates the ancestor directories of
// every entry as uid 0 and only chmods them, so without the chown in Setup the
// learner cannot create report.txt in their own level root and the solution
// fails with Permission denied.
//
// This asserts the property directly rather than the chown call: it writes to
// the level root as the learner, which is precisely what the solution does.
func TestTheLearnerOwnsTheLevelRoot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess := goldenSession(t, ctx)
	level := Demo()
	if err := level.Setup(ctx, sess); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	res, err := sess.Exec(ctx, []string{"touch", "--", level.Root + "/.write-probe"}, runtime.ExecOpts{User: demoUser})
	if err != nil {
		t.Fatalf("write probe: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("the learner cannot create a file in %s, so the level is unsolvable: %s",
			level.Root, strings.TrimSpace(string(res.Stderr)))
	}

	// The log files must be readable by the learner too.
	for _, f := range level.Files {
		res, err := sess.Exec(ctx, []string{"head", "-n", "1", "--", f.Path}, runtime.ExecOpts{User: demoUser})
		if err != nil {
			t.Fatalf("read %s: %v", f.Path, err)
		}
		if res.ExitCode != 0 {
			t.Errorf("the learner cannot read %s: %s", f.Path, strings.TrimSpace(string(res.Stderr)))
		}
	}
}

// TestCheckIsPure asserts the check does not mutate sandbox state, by hashing
// the level tree before and after two check runs. This is the standalone form
// of the golden runner's step 6.
func TestCheckIsPure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess := goldenSession(t, ctx)
	level := Demo()
	if err := level.Setup(ctx, sess); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Content, permission bits, ownership and size of every entry under the
	// level root, in a stable order.
	hashScript := "find " + level.Root + " -printf '%P %m %u:%g %s\\n' | sort; " +
		"find " + level.Root + " -type f -exec sha256sum {} + | sort"

	before, err := sess.Exec(ctx, []string{"/bin/bash", "-lc", hashScript}, runtime.ExecOpts{User: demoUser})
	if err != nil {
		t.Fatalf("hash the level tree: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, _, err := level.Check(ctx, sess); err != nil {
			t.Fatalf("Check run %d: %v", i+1, err)
		}
	}

	after, err := sess.Exec(ctx, []string{"/bin/bash", "-lc", hashScript}, runtime.ExecOpts{User: demoUser})
	if err != nil {
		t.Fatalf("hash the level tree: %v", err)
	}

	if string(before.Stdout) != string(after.Stdout) {
		t.Errorf("the check mutated sandbox state.\nbefore:\n%s\nafter:\n%s", before.Stdout, after.Stdout)
	}
}

// TestInstrumentationEmitsMarkersForARealSession is the first time
// images/rc/instrument.bash actually runs. It attaches a real instrumented
// bash, feeds it a scripted session, and asserts the marker stream carries the
// exit code and the working directory the game needs.
//
// It reads the PTY through pty.NewParser rather than through pty.Mux on
// purpose. Mux puts the HOST terminal into raw mode, and `go test` has no
// terminal, so Mux cannot run here at all. The parser is the half that decides
// what the markers mean, and this is what proves the producer and the consumer
// agree.
func TestInstrumentationEmitsMarkersForARealSession(t *testing.T) {
	// Attach allocates a pseudo terminal on the HOST with creack/pty, whose
	// Windows implementation returns ErrUnsupported unconditionally. This is
	// not a sandbox limitation and not something this test can work around;
	// Windows gets WslRuntime on Day 3. Skipping is honest, failing is not.
	if goruntime.GOOS == "windows" {
		t.Skip("Attach needs a host pseudo terminal, and creack/pty has no Windows implementation; see the windows-needs-wsl doc anchor")
	}

	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess := goldenSession(t, ctx)
	level := Demo()
	if err := level.Setup(ctx, sess); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	sandboxPTY, err := sess.Attach(ctx, runtime.AttachOpts{
		User:    demoUser,
		WorkDir: level.Root,
		Env:     map[string]string{"SF_STATE": demoStateDir},
	})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer func() { _ = sandboxPTY.Close() }()

	log := &eventLog{}
	parser := pty.NewParser(discard{}, log.add)

	// Read the PTY in the background, through the parser.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := sandboxPTY.Read(buf)
			if n > 0 {
				// The parser's Write is where events fire.
				_, _ = parser.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	send := func(line string) {
		t.Helper()
		if _, err := sandboxPTY.Write([]byte(line)); err != nil {
			t.Fatalf("write %q to the sandbox pty: %v", line, err)
		}
	}

	// Wait for the shell's own first prompt before typing anything, so the
	// first command is not raced against bash still starting up.
	log.waitFor(t, "the first prompt", func(evs []pty.Event) bool {
		return countKind(evs, pty.CommandStart) >= 1
	})

	// A command that succeeds.
	send("true\n")
	log.waitFor(t, "the first command to complete", func(evs []pty.Event) bool {
		return countKind(evs, pty.CommandDone) >= 2
	})

	// A command that fails with a known status.
	//
	// A SUBSHELL, deliberately. A bare `exit 7` at an interactive prompt
	// terminates the shell rather than failing a command, and `exit 7 || true`
	// does the same: `||` never gets its chance because exit does not return.
	// That is exactly what the first version of this test did, and CI caught
	// it: the shell died on line two, the remaining script went nowhere, and
	// the failure read as "instrument.bash loses exit codes" when
	// instrument.bash was working perfectly.
	send("(exit 7)\n")
	log.waitFor(t, "an exit code of 7 to be reported", func(evs []pty.Event) bool {
		return hasExitCode(evs, 7)
	})

	// A cd, which has to move the reported working directory.
	send("cd /home/learner\n")
	log.waitFor(t, "the new working directory to be reported", func(evs []pty.Event) bool {
		return hasCwd(evs, "/home/learner")
	})

	send("exit\n")
	select {
	case <-readDone:
	case <-time.After(30 * time.Second):
		t.Fatal("the sandbox shell did not exit")
	}
	if err := parser.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	seen := log.snapshot()
	if len(seen) == 0 {
		t.Fatal("instrument.bash emitted no OSC markers at all. The rc file is not being sourced, " +
			"or PS1/PS0/PROMPT_COMMAND are not set. Read the component-traps skill before debugging.")
	}

	var kinds []string
	var exitCodes []int
	var cwds []string
	for _, ev := range seen {
		kinds = append(kinds, ev.Kind.String())
		switch ev.Kind {
		case pty.CommandDone:
			exitCodes = append(exitCodes, ev.ExitCode)
		case pty.CwdReport:
			cwds = append(cwds, ev.Cwd)
		}
	}
	t.Logf("marker stream: %s", strings.Join(kinds, " "))
	t.Logf("exit codes: %v", exitCodes)
	t.Logf("cwds: %v", cwds)

	// All five markers have to appear. A missing PromptStart or CommandStart
	// means the PS1 readline markers are wrong; a missing PreExec means PS0 is.
	for _, want := range []pty.EventKind{pty.PromptStart, pty.CommandStart, pty.PreExec, pty.CommandDone, pty.CwdReport} {
		if countKind(seen, want) == 0 {
			t.Errorf("no %s marker in the stream: %s", want, strings.Join(kinds, " "))
		}
	}

	// The exit code has to survive exactly, not merely be non-zero. If every
	// code is 0, something in __sf_after runs before `local ec=$?`.
	if !hasExitCode(seen, 7) {
		t.Errorf("the exit code 7 from `(exit 7)` was never reported. "+
			"If every code is 0, something in __sf_after runs before it captures $?. Codes seen: %v", exitCodes)
	}

	// The cwd has to be reported, and the cd has to move it.
	if !hasCwd(seen, level.Root) {
		t.Errorf("no OSC 7 marker reported the level root %q. Cwds seen: %v", level.Root, cwds)
	}
	if !hasCwd(seen, "/home/learner") {
		t.Errorf("the `cd /home/learner` was never reported. Cwds seen: %v", cwds)
	}
}

// eventLog collects parser events from the PTY reader goroutine so the test
// goroutine can wait on them.
type eventLog struct {
	mu     sync.Mutex
	events []pty.Event
}

func (l *eventLog) add(ev pty.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
}

func (l *eventLog) snapshot() []pty.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]pty.Event(nil), l.events...)
}

// waitFor blocks until pred is satisfied by the events collected so far, or
// fails the test.
//
// This replaces a fixed sleep between scripted lines. A sleep long enough to be
// reliable on a loaded CI runner is far longer than the shell actually needs,
// and a sleep short enough to be quick is flaky: the failure then looks like a
// missing marker rather than a test that did not wait.
func (l *eventLog) waitFor(t *testing.T, what string, pred func([]pty.Event) bool) {
	t.Helper()

	const (
		timeout = 30 * time.Second
		poll    = 10 * time.Millisecond
	)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred(l.snapshot()) {
			return
		}
		time.Sleep(poll)
	}

	var kinds []string
	for _, ev := range l.snapshot() {
		kinds = append(kinds, ev.Kind.String())
	}
	t.Fatalf("timed out after %s waiting for %s.\n  marker stream so far: %s", timeout, what, strings.Join(kinds, " "))
}

func countKind(evs []pty.Event, kind pty.EventKind) int {
	n := 0
	for _, ev := range evs {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

func hasExitCode(evs []pty.Event, code int) bool {
	for _, ev := range evs {
		if ev.Kind == pty.CommandDone && ev.ExitCode == code {
			return true
		}
	}
	return false
}

func hasCwd(evs []pty.Event, cwd string) bool {
	for _, ev := range evs {
		if ev.Kind == pty.CwdReport && ev.Cwd == cwd {
			return true
		}
	}
	return false
}

// discard is an io.Writer that throws everything away.
//
// The parser needs somewhere to forward the bytes it does not recognize, and
// this test only cares about the events. Deliberately not io.Discard-with-a-
// comment: naming it makes the intent obvious at the call site.
type discard struct{}

func (discard) Write(b []byte) (int, error) { return len(b), nil }
