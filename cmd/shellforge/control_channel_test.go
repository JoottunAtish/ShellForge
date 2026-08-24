package main

import (
	"context"
	"os/exec"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/docker"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// The end-to-end test of the control channel, against a real container and the
// real in-sandbox shim.
//
// This test exists because its absence let a real defect reach a manual run.
// Every unit test of the control channel passed, `check` was wired correctly on
// both sides, and the learner still saw nothing at all, because the docker
// backend silently discarded ExecOpts.Stdin: `docker exec` needs -i, and
// without it the reply `tee` opened the response FIFO, wrote zero bytes, and
// closed. Everything looked right and nothing worked.
//
// Nothing here needs a host pseudo terminal, so unlike the Mux this runs on
// Windows too. It drives serveControlRequests directly and invokes the shim the
// way the learner does.

// controlSandboxName is the container this file provisions. Distinct from every
// other test container so `go test ./...` cannot race it across packages.
const controlSandboxName = "shellforge-controltest"

// requireLinuxDocker skips unless a Docker daemon running Linux containers is
// available, matching the probe in internal/runtime/docker/contract_test.go.
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

// controlSession provisions the test sandbox and returns an open session,
// cleaning both up even when the test fails.
func controlSession(t *testing.T, ctx context.Context) runtime.Session {
	t.Helper()
	requireLinuxDocker(t)

	rt, err := docker.New(controlSandboxName, controlSandboxName)
	if err != nil {
		t.Fatalf("docker.New(%q): %v", controlSandboxName, err)
	}
	if err := rt.Provision(ctx, runtime.ImageSpec{Name: controlSandboxName}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := rt.Destroy(cleanupCtx); err != nil {
			t.Errorf("Destroy left %s behind: %v", controlSandboxName, err)
		}
	})

	sess, err := rt.StartSession(ctx, runtime.SessionSpec{
		User:     sandboxUser,
		WorkDir:  sandboxHome,
		StateDir: setupStateDir(),
		Env:      map[string]string{"SF_STATE": setupStateDir()},
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

// controlLevelID is the level this file plays.
//
// pipe-05 rather than any other: it has two required objectives and a solution
// that satisfies both, so the shim's reply carries a real checklist and a real
// verdict rather than a single line that would look the same whether the
// engine ran or not.
const controlLevelID = "pipe-05"

// TestControlChannelAnswersTheShim is the regression test for the silent
// `check`. It runs the real /opt/shellforge/bin/check shim inside the sandbox
// and asserts the learner actually sees a verdict.
func TestControlChannelAnswersTheShim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	sess := controlSession(t, ctx)

	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	level, ok := pack.Level(controlLevelID)
	if !ok {
		t.Fatalf("%s is not in the embedded pack", controlLevelID)
	}
	packFS, err := goldenPackFS()
	if err != nil {
		t.Fatalf("root the pack filesystem: %v", err)
	}
	session, err := game.NewSession(game.Config{
		Level:    level,
		Sess:     sess,
		PackFS:   packFS,
		Verifier: verify.NewEngine(),
	})
	if err != nil {
		t.Fatalf("build the level: %v", err)
	}

	// Registered before Setup, so a Setup that fails halfway still has its
	// partial state removed rather than left behind in the container.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		_ = session.Teardown(cleanupCtx)
	})
	if err := session.Setup(ctx); err != nil {
		t.Fatalf("set %s up: %v", level.ID, err)
	}

	reqPath := path.Join(session.StateDir(), "control.req")
	resPath := path.Join(session.StateDir(), "control.res")
	if err := prepareControlChannel(ctx, sess, reqPath, resPath); err != nil {
		t.Fatalf("prepareControlChannel: %v", err)
	}

	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	served := make(chan struct{})
	go func() {
		defer close(served)
		// The production responder, with colour off: this test compares
		// against plain words, and a learner with NO_COLOR set gets exactly
		// this path.
		serveControlRequests(serveCtx, sess, &gameResponder{session: session, level: level, color: false}, reqPath, resPath)
	}()

	// runShim invokes the shim the way the learner's shell does: through the
	// PATH that instrument.bash sets, with SF_STATE pointing at the state
	// directory holding the FIFOs.
	runShim := func(t *testing.T, verb string) string {
		t.Helper()
		res, err := sess.Exec(ctx, []string{"/opt/shellforge/bin/" + verb}, runtime.ExecOpts{
			User:    sandboxUser,
			Env:     map[string]string{"SF_STATE": session.StateDir()},
			Timeout: 60 * time.Second,
		})
		if err != nil {
			t.Fatalf("run the %s shim: %v", verb, err)
		}
		if res.TimedOut {
			t.Fatalf("the %s shim never returned. The host side is not answering, so the shim is still blocked reading the response FIFO.", verb)
		}
		out := strings.TrimSpace(string(res.Stdout))
		if out == "" {
			t.Fatalf("the %s shim printed NOTHING. The learner sees a bare prompt and no verdict.\n"+
				"  shim stderr: %q\n"+
				"  shim exit:   %d\n"+
				"  This is what a discarded ExecOpts.Stdin looks like: the reply `tee` opens the "+
				"response FIFO, writes zero bytes, and closes, so the shim's `cat` gets an "+
				"immediate EOF. Check that `docker exec` is passed -i.",
				verb, res.Stderr, res.ExitCode)
		}
		return out
	}

	// The verdict is a summary line inside a checklist, not the first line of
	// the reply, so these look for the words rather than a prefix. The
	// checklist is the point: it is what tells a learner which objective is
	// outstanding, and a reply that carried only a verdict would pass a prefix
	// check while telling them nothing.
	//
	// Before the solution, check must report a failure, and must say so out
	// loud rather than silently.
	got := runShim(t, "check")
	if !strings.Contains(got, "NOT YET:") {
		t.Errorf("before the solution, `check` said %q, want it to say NOT YET:", got)
	}
	if strings.Contains(got, "PASS:") {
		t.Errorf("before the solution, `check` reported a pass: %q", got)
	}

	// Solve it the way the learner does, through the same helper the golden
	// harness uses, so this test and that one cannot disagree about what
	// running a solution means.
	if err := applySolution(ctx, sess, level); err != nil {
		t.Fatalf("run the solution: %v", err)
	}

	// Ask the engine directly as well. If this said no, a missing PASS below
	// would be a level or engine problem rather than the control channel
	// problem this test exists to catch, and the failure should say which.
	result, err := session.Check(ctx)
	if err != nil {
		t.Fatalf("check %s directly: %v", level.ID, err)
	}
	if !result.Passed {
		t.Fatalf("%s does not pass its own solution, so this test cannot tell whether the control channel works", level.ID)
	}

	got = runShim(t, "check")
	if !strings.Contains(got, "PASS:") {
		t.Errorf("after the solution, `check` said %q, want it to say PASS:", got)
	}

	// The checklist reaches the learner too, not only the verdict.
	if !strings.Contains(got, level.Objectives[0].Text) {
		t.Errorf("the reply does not carry the objective checklist: %q", got)
	}

	// The channel has to survive more than one request, which is the part a
	// single-shot implementation would pass and a real session would not.
	got = runShim(t, "check")
	if !strings.Contains(got, "PASS:") {
		t.Errorf("on the third request, `check` said %q, want PASS:. The control loop does not survive repeated use.", got)
	}

	// A verb that is not built yet still has to answer, rather than hang.
	if got := runShim(t, "hint"); !strings.Contains(got, "not built yet") {
		t.Errorf("`hint` said %q, want it to say it is not built yet", got)
	}

	// Cancelling the context must end the loop, so no `cat` is left holding
	// the FIFO open after the session ends.
	stopServing()
	select {
	case <-served:
	case <-time.After(30 * time.Second):
		t.Error("serveControlRequests ignored context cancellation and would outlive the session")
	}
}
