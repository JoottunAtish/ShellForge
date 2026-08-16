package main

import (
	"context"
	"os/exec"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/docker"
	"github.com/JoottunAtish/ShellForge/internal/sandbox"
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

	level := sandbox.Demo()
	sess, err := rt.StartSession(ctx, runtime.SessionSpec{
		User:     level.User(),
		WorkDir:  sandboxHome,
		StateDir: level.StateDir(),
		Env:      map[string]string{"SF_STATE": level.StateDir()},
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

// TestControlChannelAnswersTheShim is the regression test for the silent
// `check`. It runs the real /opt/shellforge/bin/check shim inside the sandbox
// and asserts the learner actually sees a verdict.
func TestControlChannelAnswersTheShim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	sess := controlSession(t, ctx)
	level := sandbox.Demo()

	if err := level.Setup(ctx, sess); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	reqPath := path.Join(level.StateDir(), "control.req")
	resPath := path.Join(level.StateDir(), "control.res")
	if err := prepareControlChannel(ctx, sess, reqPath, resPath); err != nil {
		t.Fatalf("prepareControlChannel: %v", err)
	}

	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	served := make(chan struct{})
	go func() {
		defer close(served)
		serveControlRequests(serveCtx, sess, &demoResponder{level: level, sess: sess}, reqPath, resPath)
	}()

	// runShim invokes the shim the way the learner's shell does: through the
	// PATH that instrument.bash sets, with SF_STATE pointing at the state
	// directory holding the FIFOs.
	runShim := func(t *testing.T, verb string) string {
		t.Helper()
		res, err := sess.Exec(ctx, []string{"/opt/shellforge/bin/" + verb}, runtime.ExecOpts{
			User:    level.User(),
			Env:     map[string]string{"SF_STATE": level.StateDir()},
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

	// Before the solution, check must report a failure, and must say so out
	// loud rather than silently.
	got := runShim(t, "check")
	if !strings.HasPrefix(got, "FAIL:") {
		t.Errorf("before the solution, `check` said %q, want it to start with FAIL:", got)
	}

	// Solve it the way the learner does.
	if res, err := sess.Exec(ctx, []string{"/bin/bash", "-lc", level.Solution}, runtime.ExecOpts{
		User: level.User(),
		Env:  map[string]string{"HOME": "/home/learner"},
	}); err != nil {
		t.Fatalf("run the solution: %v", err)
	} else if res.ExitCode != 0 {
		t.Fatalf("the solution exited %d: %s", res.ExitCode, res.Stderr)
	}

	got = runShim(t, "check")
	if !strings.HasPrefix(got, "PASS:") {
		t.Errorf("after the solution, `check` said %q, want it to start with PASS:", got)
	}
	if !strings.Contains(got, level.Answer()) {
		t.Errorf("the passing verdict does not mention the answer %q: %q", level.Answer(), got)
	}

	// The channel has to survive more than one request, which is the part a
	// single-shot implementation would pass and a real session would not.
	got = runShim(t, "check")
	if !strings.HasPrefix(got, "PASS:") {
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
