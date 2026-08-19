package main

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/pty"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// The safety coverage that says the learner cannot break their own machine,
// against a real YAML level rather than against a hardcoded Go one.
//
// These tests were internal/sandbox/demo_isolation_test.go and
// internal/sandbox/demo_golden_test.go until #96. They moved here, unchanged in
// what they assert, because the level they need is now data: the demo they used
// to run against is deleted, and the promise they protect is not.
//
// Every test in this file goes through requireGoldenSandbox, so it runs only in
// the Sandbox image CI job and for a developer who asked for it with
// SHELLFORGE_GOLDEN=1. The names are listed in that job's own -run pattern: a
// test that exists but never runs proves nothing.
//
// The isolation test is the automated form of the Day 1 exit criterion "rm -rf /
// inside the sandbox does nothing to the host". A manual run of `sudo rm -rf /*`
// does NOT establish it, because the container is created with --security-opt
// no-new-privileges and sudo is refused before the removal ever runs: that
// proves sudo is blocked, not that the filesystem is isolated. The removal has
// to actually execute for the criterion to mean anything.

const (
	// isolationLevelID is the level these tests play.
	//
	// pipe-05 rather than any other: its setup.root is /home/learner/quest, it
	// materializes real assets from the pack, and it carries a solution that
	// satisfies its own required checks, so "Setup recovers the world" can be
	// proved by solving the level again rather than by looking at a directory
	// listing.
	isolationLevelID = "pipe-05"

	// sandboxMarkerPath and sandboxMarkerID identify OUR container from the
	// inside.
	//
	// Nothing below deletes anything on the host, and the removal runs through
	// Session.Exec, which physically cannot reach the host filesystem. This is
	// the belt to that pair of braces, and it is the guard the
	// destructive-safety skill asks for by name: confirm the thing is ours
	// before running anything destructive against it, and refuse rather than
	// proceed when the marker is missing. The file is root owned and mode 0444
	// inside the image, so a process running as the learner cannot forge it.
	sandboxMarkerPath = "/opt/shellforge/.sandbox-id"
	sandboxMarkerID   = "shellforge-sandbox-v1"

	// destructiveRemoval is what a learner can type, and what a level teaching
	// rm will eventually ask them to type by accident.
	//
	// It runs as the learner, with no sudo. A leading `sudo` would prove
	// nothing: no-new-privileges refuses it before the removal runs at all.
	// Root-owned paths such as /bin and /usr survive only because the container
	// drops CAP_DAC_OVERRIDE, which is why the session is still usable
	// afterwards.
	destructiveRemoval = "rm -rf / 2>/dev/null; rm -rf /* 2>/dev/null; rm -rf ~ 2>/dev/null; true"
)

// isolationLevel opens a sandbox session and builds a game.Session for
// pipe-05 on it, with no setup run yet.
func isolationLevel(t *testing.T, ctx context.Context) (runtime.Session, *game.Session, *content.Level) {
	t.Helper()

	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	level, ok := pack.Level(isolationLevelID)
	if !ok {
		t.Fatalf("%s is not in the embedded pack", isolationLevelID)
	}
	packFS, err := goldenPackFS()
	if err != nil {
		t.Fatalf("root the pack filesystem: %v", err)
	}

	sess := goldenSession(t, ctx)
	session, err := game.NewSession(game.Config{
		Level:    level,
		Sess:     sess,
		PackFS:   packFS,
		Verifier: verify.NewEngine(),
	})
	if err != nil {
		t.Fatalf("build the level: %v", err)
	}
	return sess, session, level
}

// requireOurSandbox refuses to go on unless the session is inside a Shellforge
// sandbox container.
//
// Fail closed: a missing or unexpected marker is a stop, not a warning. The
// only caller is the destructive test, and the whole point is that it never
// runs its removal anywhere it did not mean to.
func requireOurSandbox(t *testing.T, ctx context.Context, sess runtime.Session) {
	t.Helper()

	res, err := sess.Exec(ctx, []string{"cat", "--", sandboxMarkerPath}, runtime.ExecOpts{
		User:    sandboxUser,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("read the sandbox marker %s: %v", sandboxMarkerPath, err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("the sandbox marker %s is missing, so this session may not be a Shellforge sandbox. Refusing to run a recursive delete.", sandboxMarkerPath)
	}
	if got := strings.TrimSpace(string(res.Stdout)); got != sandboxMarkerID {
		t.Fatalf("the sandbox marker %s reads %q, want %q. Refusing to run a recursive delete.", sandboxMarkerPath, got, sandboxMarkerID)
	}
}

// TestSandboxHasNoHostMounts asserts the container cannot see the host
// filesystem at all. This is the structural half of the promise, and it holds
// whether or not anything destructive ever runs.
func TestSandboxHasNoHostMounts(t *testing.T) {
	requireGoldenSandbox(t)

	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess := goldenSession(t, ctx)

	// The WSL automount paths must not exist. On the Docker backend they never
	// would, but asserting it here means the same expectation is already
	// written down for the WSL backend, where it is load bearing: /mnt/c inside
	// the sandbox is a direct route to the learner's real files.
	for _, p := range []string{"/mnt/c", "/mnt/d", "/mnt/wsl", "/host"} {
		res, err := sess.Exec(ctx, []string{"test", "-e", p}, runtime.ExecOpts{User: sandboxUser})
		if err != nil {
			t.Fatalf("probe %s: %v", p, err)
		}
		if res.ExitCode == 0 {
			t.Errorf("%s exists inside the sandbox, which is a path to the host filesystem", p)
		}
	}

	// And the container itself must carry no bind mount. Asking Docker
	// directly is stronger than probing paths, because it catches a mount at
	// a name nothing above thought to check.
	out, err := exec.CommandContext(ctx, "docker", "inspect",
		"--format", "{{range .Mounts}}{{.Type}}:{{.Source}}->{{.Destination}} {{end}}",
		"--", goldenSandboxName).Output()
	if err != nil {
		t.Fatalf("docker inspect: %v", err)
	}
	if mounts := strings.TrimSpace(string(out)); mounts != "" {
		t.Errorf("the sandbox container has host mounts, so the host filesystem is reachable from inside: %s", mounts)
	}
}

// TestSandboxIsolationLeavesTheHostUntouched is the real isolation test. It
// runs a recursive delete inside the sandbox, as the learner, for real, and
// then proves two things: the host is untouched, and the level's own Setup puts
// the world back.
func TestSandboxIsolationLeavesTheHostUntouched(t *testing.T) {
	requireGoldenSandbox(t)

	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess, session, level := isolationLevel(t, ctx)
	requireOurSandbox(t, ctx, sess)

	// Registered before Setup, so a Setup that fails halfway still has its
	// partial state removed rather than left for the next test to inherit.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		_ = session.Teardown(cleanupCtx)
	})
	if err := session.Setup(ctx); err != nil {
		t.Fatalf("set %s up: %v", level.ID, err)
	}

	// A canary on the HOST, in this test's own temp directory. If the sandbox
	// could reach the host filesystem, a recursive delete from / would be the
	// thing that found it. t.TempDir is removed by the test framework, so this
	// leaves nothing behind either way.
	canaryDir := t.TempDir()
	canaryPath := filepath.Join(canaryDir, "do-not-delete.txt")
	const canaryContent = "the host filesystem must survive this test"
	if err := os.WriteFile(canaryPath, []byte(canaryContent), 0o600); err != nil {
		t.Fatalf("write the host canary: %v", err)
	}

	// Record the host's own working directory listing too, so the assertion is
	// not limited to one file in one temp directory. This is the package
	// directory that `go test` runs in, not the repository root.
	hostWorkDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	before, err := os.ReadDir(hostWorkDir)
	if err != nil {
		t.Fatalf("read the host working directory: %v", err)
	}

	// The real thing, as the learner. This deletes everything the learner owns
	// inside the container: their home, the level world, and the state
	// directory.
	res, err := sess.Exec(ctx, []string{"/bin/bash", "-lc", destructiveRemoval}, runtime.ExecOpts{
		User: sandboxUser,
		Env:  map[string]string{"HOME": sandboxHome},
	})
	if err != nil {
		t.Fatalf("run the destructive removal: %v", err)
	}
	t.Logf("the destructive removal exited %d", res.ExitCode)

	// 1. The host canary is untouched, byte for byte.
	got, err := os.ReadFile(canaryPath)
	if err != nil {
		t.Fatalf("THE HOST CANARY IS GONE. A delete inside the sandbox reached the host filesystem: %v", err)
	}
	if string(got) != canaryContent {
		t.Fatalf("THE HOST CANARY WAS MODIFIED. want %q, got %q", canaryContent, got)
	}

	// 2. The host working directory still has everything it started with.
	after, err := os.ReadDir(hostWorkDir)
	if err != nil {
		t.Fatalf("THE HOST WORKING DIRECTORY IS GONE: %v", err)
	}
	beforeNames := make(map[string]bool, len(before))
	for _, entry := range before {
		beforeNames[entry.Name()] = true
	}
	afterNames := make(map[string]bool, len(after))
	for _, entry := range after {
		afterNames[entry.Name()] = true
	}
	for name := range beforeNames {
		if !afterNames[name] {
			t.Errorf("the host working directory lost entries: %q was present before and is gone now", name)
		}
	}

	// 3. The level world really was destroyed inside the sandbox. Without this
	// the test could pass because the removal silently did nothing at all,
	// which would make the first two assertions meaningless.
	//
	// The level root itself is the assertion, not a file inside it: a probe for
	// something the learner had not created yet would be absent either way and
	// would prove nothing.
	for _, p := range []string{level.Setup.Root, path.Join(level.Setup.Root, "logs", "billing.log")} {
		probe, err := sess.Exec(ctx, []string{"test", "-e", p}, runtime.ExecOpts{User: sandboxUser})
		if err != nil {
			t.Fatalf("probe %s: %v", p, err)
		}
		if probe.ExitCode == 0 {
			t.Fatalf("%s survived the removal, so nothing was deleted inside the sandbox either and this test proved nothing about isolation", p)
		}
	}

	// 4. Setup recovers. This is what makes the promise usable rather than
	// merely true: a learner who wrecks their sandbox gets it back, and the
	// level is solvable again afterwards.
	if err := session.Setup(ctx); err != nil {
		t.Fatalf("Setup could not recover %s after a destructive removal: %v", level.ID, err)
	}
	if err := applySolution(ctx, sess, level); err != nil {
		t.Fatalf("run the level's solution after recovery: %v", err)
	}
	result, err := session.Check(ctx)
	if err != nil {
		t.Fatalf("check after recovery: %v", err)
	}
	if !result.Passed {
		t.Errorf("%s does not pass after recovery, so Setup did not put the world back: %s",
			level.ID, checkFailureSummary(result))
	}
}

// checkFailureSummary names the objectives that did not pass, for a failure
// message that says which half of the recovery is missing.
func checkFailureSummary(res verify.LevelResult) string {
	var missing []string
	for _, obj := range res.Objectives {
		if obj.Optional || obj.Status == verify.StatusPass {
			continue
		}
		missing = append(missing, obj.ID)
	}
	if len(missing) == 0 {
		return "no required objective is outstanding, which contradicts the result"
	}
	return "outstanding required objectives: " + strings.Join(missing, ", ")
}

// TestSudoIsRefusedByNoNewPrivileges records what the sandbox actually does
// today, and flags the conflict it creates.
//
// The image grants the learner passwordless sudo on purpose: its own comment
// says Act V teaches sudo. The container is then created with --security-opt
// no-new-privileges, which stops sudo from gaining privileges at all, so the
// grant has no effect and `sudo anything` fails with "the no new privileges
// flag is set".
//
// For the safety gate this is a belt-and-braces win. For Act V it is a
// contradiction between the image and the runtime that has to be resolved
// before those levels can be authored. This test pins the current behaviour so
// the decision is made deliberately rather than discovered by a learner.
//
// TODO(v0.2): decide whether Act V drops no-new-privileges for levels that
// declare they need sudo, or whether the curriculum teaches sudo without
// running it. Do NOT resolve it by removing the flag to make a level pass.
func TestSudoIsRefusedByNoNewPrivileges(t *testing.T) {
	requireGoldenSandbox(t)

	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess := goldenSession(t, ctx)

	res, err := sess.Exec(ctx, []string{"sudo", "-n", "id", "-u"}, runtime.ExecOpts{
		User:    sandboxUser,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("run sudo: %v", err)
	}
	if res.ExitCode == 0 && strings.TrimSpace(string(res.Stdout)) == "0" {
		t.Errorf("sudo succeeded and returned uid 0. That contradicts --security-opt no-new-privileges, " +
			"so either the flag was removed or the sandbox is more permissive than it was designed to be. " +
			"If this was deliberate, update the Containerfile comment and this test together.")
		return
	}
	t.Logf("sudo is refused, as the container configuration intends: exit %d, stderr %q",
		res.ExitCode, strings.TrimSpace(string(res.Stderr)))
}

// TestInstrumentationEmitsMarkersForARealSession is the only place
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
	// Windows gets there through WSL. Skipping is honest, failing is not.
	if goruntime.GOOS == "windows" {
		t.Skip("Attach needs a host pseudo terminal, and creack/pty has no Windows implementation; see the windows-needs-wsl doc anchor")
	}
	requireGoldenSandbox(t)

	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess, session, level := isolationLevel(t, ctx)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		_ = session.Teardown(cleanupCtx)
	})
	if err := session.Setup(ctx); err != nil {
		t.Fatalf("set %s up: %v", level.ID, err)
	}

	sandboxPTY, err := sess.Attach(ctx, runtime.AttachOpts{
		User:    sandboxUser,
		WorkDir: level.Setup.Root,
		Env:     map[string]string{"SF_STATE": session.StateDir()},
	})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer func() { _ = sandboxPTY.Close() }()

	log := &markerLog{}
	parser := pty.NewParser(discardWriter{}, log.add)

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
		return countMarkerKind(evs, pty.CommandStart) >= 1
	})

	// A command that succeeds.
	send("true\n")
	log.waitFor(t, "the first command to complete", func(evs []pty.Event) bool {
		return countMarkerKind(evs, pty.CommandDone) >= 2
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
		return markerHasExitCode(evs, 7)
	})

	// A cd, which has to move the reported working directory.
	send("cd " + sandboxHome + "\n")
	log.waitFor(t, "the new working directory to be reported", func(evs []pty.Event) bool {
		return markerHasCwd(evs, sandboxHome)
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
		if countMarkerKind(seen, want) == 0 {
			t.Errorf("no %s marker in the stream: %s", want, strings.Join(kinds, " "))
		}
	}

	// The exit code has to survive exactly, not merely be non-zero. If every
	// code is 0, something in __sf_after runs before `local ec=$?`.
	if !markerHasExitCode(seen, 7) {
		t.Errorf("the exit code 7 from `(exit 7)` was never reported. "+
			"If every code is 0, something in __sf_after runs before it captures $?. Codes seen: %v", exitCodes)
	}

	// The cwd has to be reported, and the cd has to move it.
	if !markerHasCwd(seen, level.Setup.Root) {
		t.Errorf("no OSC 7 marker reported the level root %q. Cwds seen: %v", level.Setup.Root, cwds)
	}
	if !markerHasCwd(seen, sandboxHome) {
		t.Errorf("the `cd %s` was never reported. Cwds seen: %v", sandboxHome, cwds)
	}
}

// markerLog collects parser events from the PTY reader goroutine so the test
// goroutine can wait on them.
type markerLog struct {
	mu     sync.Mutex
	events []pty.Event
}

func (l *markerLog) add(ev pty.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
}

func (l *markerLog) snapshot() []pty.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]pty.Event, len(l.events))
	copy(out, l.events)
	return out
}

// waitFor blocks until pred is satisfied by the events collected so far, or
// fails the test.
//
// This replaces a fixed sleep between scripted lines. A sleep long enough to be
// reliable on a loaded CI runner is far longer than the shell actually needs,
// and a sleep short enough to be quick is flaky: the failure then looks like a
// missing marker rather than a test that did not wait.
func (l *markerLog) waitFor(t *testing.T, what string, pred func([]pty.Event) bool) {
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

func countMarkerKind(evs []pty.Event, kind pty.EventKind) int {
	n := 0
	for _, ev := range evs {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

func markerHasExitCode(evs []pty.Event, code int) bool {
	for _, ev := range evs {
		if ev.Kind == pty.CommandDone && ev.ExitCode == code {
			return true
		}
	}
	return false
}

func markerHasCwd(evs []pty.Event, cwd string) bool {
	for _, ev := range evs {
		if ev.Kind == pty.CwdReport && ev.Cwd == cwd {
			return true
		}
	}
	return false
}

// discardWriter is where the forwarded byte stream goes. This test is about the
// events, not about what a terminal would have shown.
type discardWriter struct{}

func (discardWriter) Write(b []byte) (int, error) { return len(b), nil }
