package doctor

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// cancelGrace bounds how long a cancelled invocation is given to exit on
// its own once signalled, before Wait forcibly kills it. Copied in shape
// from internal/runtime/docker/runner.go.
const cancelGrace = 5 * time.Second

// runner runs one argv vector. Copied in shape from internal/runtime/docker
// so that argv construction is testable without spawning anything. A
// non-zero exit is reported through code and never through err; a
// cancelled context returns code -1 and the context error.
type runner interface {
	run(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, code int, err error)
}

// execRunner is the real runner. Unlike internal/runtime/docker's, which
// only ever shells out to "docker", this package's probes shell out to
// several different binaries (uname, docker, wsl.exe, powershell.exe), so
// there is no single logical name to bind at construction time. Instead
// argv[0] itself is the logical name for every call, resolved fresh with
// exec.LookPath each time.
type execRunner struct{ grace time.Duration }

// newExecRunner returns the real runner, used by Run in production.
func newExecRunner() *execRunner {
	return &execRunner{grace: cancelGrace}
}

func (r *execRunner) run(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, code int, err error) {
	if len(argv) == 0 {
		return nil, nil, 0, errors.New("doctor: runner.run called with an empty argv")
	}

	bin, lookErr := exec.LookPath(argv[0])
	if lookErr != nil {
		return nil, nil, -1, lookErr
	}

	// #nosec G204 -- bin is exec.LookPath(argv[0]) resolved fresh for this
	// call; argv[1:] is a vector every probe builds from compile-time
	// constants, never from a level, a flag, or a host shell string.
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = r.grace
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
