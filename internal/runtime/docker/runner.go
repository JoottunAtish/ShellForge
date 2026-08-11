package docker

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// cancelGrace bounds how long a cancelled docker invocation is given to
// exit on its own once signalled, before Wait forcibly kills it. See the
// comment in execRunner.run on why the signal matters as much as the
// timeout.
const cancelGrace = 5 * time.Second

// runner executes one docker invocation and reports its outcome. Every
// dockerRuntime and dockerSession method builds an argv and calls run
// exactly once per docker process; nothing here ever touches a host shell.
//
// This indirection exists so the argv-construction tests can assert the
// exact argv a method produces without spawning docker, by substituting a
// fakeRunner that records what it was asked to run.
type runner interface {
	// run executes argv[0] with argv[1:] as arguments, feeding stdin to the
	// process and returning what it wrote to stdout and stderr separately,
	// its exit code, and an error only when the process could not be
	// started or waited on at all. A non-zero exit is reported through
	// code, never through err.
	run(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, code int, err error)
}

// execRunner is the real runner, backed by os/exec. bin is the resolved
// path to the docker binary; argv[0] is always "docker" as far as callers
// are concerned, and execRunner substitutes bin so the command name itself
// is never variable, per the security skill.
type execRunner struct {
	bin string
}

func (r execRunner) run(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, code int, err error) {
	if len(argv) == 0 || argv[0] != "docker" {
		return nil, nil, 0, errors.New("docker: runner.run called with an argv that does not start with \"docker\"")
	}

	// #nosec G204 -- r.bin is exec.LookPath("docker") resolved once in New
	// and never variable from config; argv[1:] is the vector every method
	// in this package builds from allowlist-validated identifiers and
	// caller-supplied argv elements passed straight to the sandbox, never
	// a shell string.
	cmd := exec.CommandContext(ctx, r.bin, argv[1:]...)
	// exec.CommandContext's default cancellation is cmd.Process.Kill(),
	// SIGKILL, on the local docker client. That only ever stops the local
	// client: unlike `docker run` and `docker attach`, `docker exec` has
	// no --sig-proxy, so killing the client, gently or otherwise, does
	// not by itself tell the daemon to stop the process it started inside
	// the container. Terminating the sandbox-side process is
	// dockerSession.Exec's job, via the PID marker in session.go; this
	// SIGTERM is only about ending the local client promptly rather than
	// leaving it to WaitDelay's hard kill.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = cancelGrace
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	if ctx.Err() != nil {
		return outBuf.Bytes(), errBuf.Bytes(), -1, ctx.Err()
	}
	if runErr == nil {
		return outBuf.Bytes(), errBuf.Bytes(), 0, nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return outBuf.Bytes(), errBuf.Bytes(), exitErr.ExitCode(), nil
	}
	return outBuf.Bytes(), errBuf.Bytes(), -1, runErr
}
