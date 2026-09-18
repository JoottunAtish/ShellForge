// Package sandboxpty drives a pseudo terminal that lives inside the sandbox
// rather than on the host.
//
// It is a separate package from internal/runtime because that one is types
// only, deliberately and with a test that says so: it declares the Runtime,
// Session and PTY interfaces and no behaviour at all. This is the behaviour.
//
// Layer L1, beside the backends rather than under them. It is not under
// internal/runtime/ on purpose either: that prefix is reserved for backend
// implementations, which only cmd/shellforge, internal/runtime and
// internal/sandbox may import, and both backends need this.
package sandboxpty

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"sync"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// PipePTY is a PTY whose pseudo terminal lives inside the sandbox rather
// than on the host.
//
// Both backends used to allocate the pseudo terminal here, on the host,
// around `docker exec -it` or `wsl.exe --exec`. creack/pty has no Windows
// implementation, so that could never work from a Windows console, which is
// issue #138. The pseudo terminal now lives where the shell is, allocated by
// cmd/sf-ptyhost inside the sandbox, and the host exchanges raw bytes with
// it over two ordinary pipes. Pipes work everywhere, so one implementation
// serves both operating systems and there is no Windows-specific path left
// to go stale untested.
//
// Nothing here transforms a byte in either direction. internal/pty parses
// OSC 133 markers out of this stream to build the journal, so a single
// rewritten escape sequence would corrupt verification and scoring.
type PipePTY struct {
	cmd    *exec.Cmd
	stdin  *os.File
	stdout *os.File
	stderr *cappedBuffer
	resize func(rows, cols uint16) error

	closeOnce sync.Once
	closeErr  error
}

var _ runtime.PTY = (*PipePTY)(nil)

// StartPipePTY wires cmd's stdin and stdout to pipes, starts it, and returns
// the PTY the multiplexer drives. resize is how a window size reaches the
// sandbox; it may be nil on a backend that cannot deliver one.
//
// The pipes are made with os.Pipe rather than cmd.StdinPipe and
// cmd.StdoutPipe on purpose. os/exec's own documentation says it is
// incorrect to call Wait before all reads from a StdoutPipe have completed,
// because Wait closes it, and Mux.Run does exactly that: it reads the shell's
// output and waits for the process at the same time. Holding the pipe ends
// here instead takes that race off the table.
func StartPipePTY(cmd *exec.Cmd, resize func(rows, cols uint16) error) (*PipePTY, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("runtime: create the sandbox stdin pipe: %w", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, fmt.Errorf("runtime: create the sandbox stdout pipe: %w", err)
	}

	stderr := &cappedBuffer{}
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, err
	}

	// The child holds the ends it needs now. Closing this side's copies is
	// what makes the read end see EOF when the child exits: a pipe stays
	// open while any writer does, and this process would otherwise be one.
	_ = inR.Close()
	_ = outW.Close()

	return &PipePTY{cmd: cmd, stdin: inW, stdout: outR, stderr: stderr, resize: resize}, nil
}

func (p *PipePTY) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *PipePTY) Write(b []byte) (int, error) { return p.stdin.Write(b) }

// Resize sends the new window size to the pseudo terminal inside the
// sandbox. A backend that cannot deliver one reports that rather than
// silently claiming success: internal/pty.Mux discards the error at every
// call site, treating one failed resize as never fatal, so saying so here
// costs nothing a caller was relying on.
func (p *PipePTY) Resize(rows, cols uint16) error {
	if p.resize == nil {
		return fmt.Errorf("runtime: this backend cannot deliver a window size")
	}
	return p.resize(rows, cols)
}

// Close hangs the sandbox shell up by closing its stdin, which is what
// cmd/sf-ptyhost watches for, and releases this side's pipe ends.
//
// It does not kill the child. Both backends build theirs with
// exec.CommandContext, so cancelling the session's context is what
// guarantees no process is left behind, and killing here as well would race
// the shell's own clean exit.
func (p *PipePTY) Close() error {
	p.closeOnce.Do(func() {
		if err := p.stdin.Close(); err != nil {
			p.closeErr = err
		}
		if err := p.stdout.Close(); err != nil && p.closeErr == nil {
			p.closeErr = err
		}
	})
	return p.closeErr
}

// Wait blocks until the attached process exits, and folds in whatever the
// in-sandbox pty host wrote to stderr. Without that, a pty host that refused
// to start would surface as a bare non-zero exit with no reason attached.
func (p *PipePTY) Wait() error {
	err := p.cmd.Wait()
	if err == nil {
		return nil
	}
	if detail := p.stderr.String(); detail != "" {
		return fmt.Errorf("%w: %s", err, detail)
	}
	return err
}

// cappedBuffer collects at most cappedBufferMax bytes and discards the rest.
// The pty host writes a line or two at most, and an unbounded buffer on a
// session that may run for an hour is a leak waiting for the one level that
// makes something chatty.
type cappedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const cappedBufferMax = 4096

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := cappedBufferMax - len(b.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.buf = append(b.buf, p[:room]...)
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

var _ io.Writer = (*cappedBuffer)(nil)

// PtyHostPath is where images/Containerfile installs cmd/sf-ptyhost inside
// the sandbox. Both backends exec it, so it is declared once.
const PtyHostPath = "/opt/shellforge/bin/sf-ptyhost"

// DefaultSandboxStateDir is where the FIFO goes when a caller passes no
// SF_STATE, matching images/rc/instrument.bash's own default.
const DefaultSandboxStateDir = "/home/learner/.shellforge"

// WinsizeFIFOPath is the path inside the sandbox that the pty host watches
// for window sizes.
//
// It is built with path.Join, never filepath.Join. These are sandbox paths,
// and on a Windows host filepath would join them with backslashes, which is
// the same trap internal/platform/sandboxpath.go already documents.
func WinsizeFIFOPath(stateDir string) string {
	if stateDir == "" {
		stateDir = DefaultSandboxStateDir
	}
	return path.Join(stateDir, "winsize")
}

// PtyHostCommand wraps the command a backend would otherwise have run
// directly, so that it runs on a pseudo terminal allocated inside the
// sandbox instead of on one allocated on the host.
func PtyHostCommand(stateDir string, command []string) []string {
	argv := []string{PtyHostPath, "--winsize-fifo", WinsizeFIFOPath(stateDir), "--"}
	return append(argv, command...)
}

// WinsizeLine is what the host writes to the FIFO for one resize. The pty
// host parses exactly this shape and ignores anything else.
func WinsizeLine(rows, cols uint16) []byte {
	return fmt.Appendf(nil, "%d %d\n", rows, cols)
}

// ErrNoInteractiveShell is what a backend that cannot open one returns.
// Nothing returns it today: both backends can, since the pseudo terminal
// moved inside the sandbox. It exists so that a backend which cannot say
// yes has somewhere truthful to say no, which is what issue #77 asked for
// and what the CLI used to answer with a GOOS test instead.
func ErrNoInteractiveShell(backend string) error {
	return ux.Fail(
		"open an interactive sandbox shell",
		fmt.Errorf("the %s backend reports no interactive shell support", backend),
		"Run `shellforge doctor` to see which backends this machine has, and `shellforge init` to provision one that supports a shell.",
		"no-runtime-available",
	)
}

// ErrPtyHostMissing is the refusal for a sandbox provisioned from an image
// built before the pseudo terminal moved inside it.
//
// This is a real upgrade path, not a theoretical one. ensureImage reuses an
// image that is already present by name, and containerIsStale compares the
// container against the image rather than the image against what the code
// now expects, so upgrading the binary alone leaves an older image in place.
// Without this the failure would be `docker exec` reporting that an
// executable was not found, which reads as a broken install rather than as
// one command away from fixed.
func ErrPtyHostMissing() error {
	return ux.Fail(
		"open the sandbox shell",
		fmt.Errorf("%s is not present in the sandbox", PtyHostPath),
		"This sandbox was built before the interactive shell moved inside it. Run `shellforge sandbox rebuild` to replace it, then try again.",
		"sandbox-needs-rebuild",
	)
}
