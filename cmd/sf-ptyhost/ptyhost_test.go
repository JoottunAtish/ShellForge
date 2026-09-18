//go:build unix

package main_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildPtyhost builds the binary once per test binary run and returns its
// path. The tests below drive it exactly as internal/runtime does: over
// ordinary pipes, with no pseudo terminal anywhere on this side. That is the
// whole point of issue #138, and it is why these tests are worth running on
// Linux: the bytes they exercise are the same bytes a Windows host will
// exchange, and no CI runner this project can reach will ever execute the
// Windows half.
var buildPtyhost = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "sf-ptyhost-build")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "sf-ptyhost")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", errors.New("go build: " + err.Error() + ": " + string(out))
	}
	return bin, nil
})

// session is one sf-ptyhost run driven over pipes.
type session struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *safeBuffer
	stderr *safeBuffer
	fifo   string
}

func start(t *testing.T, command ...string) *session {
	t.Helper()

	bin, err := buildPtyhost()
	if err != nil {
		t.Fatalf("build sf-ptyhost: %v", err)
	}

	fifo := filepath.Join(t.TempDir(), "winsize")
	argv := append([]string{"--winsize-fifo", fifo, "--"}, command...)
	cmd := exec.Command(bin, argv...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	out := &safeBuffer{}
	errBuf := &safeBuffer{}
	cmd.Stderr = errBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("start sf-ptyhost: %v", err)
	}
	go func() { _, _ = io.Copy(out, stdout) }()

	s := &session{cmd: cmd, stdin: stdin, out: out, stderr: errBuf, fifo: fifo}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return s
}

func (s *session) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(s.stdin, line); err != nil {
		t.Fatalf("write %q to the pty host: %v", line, err)
	}
}

// waitFor polls the collected output for want. Polling rather than a single
// read because a pseudo terminal delivers in whatever chunks the kernel
// chooses, which is the same reason internal/pty's OSC parser has to handle
// a marker split across reads.
func (s *session) waitFor(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(s.out.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("did not see %q within %s.\nstdout so far:\n%s\nstderr:\n%s", want, timeout, s.out.String(), s.stderr.String())
}

func (s *session) wait(t *testing.T) int {
	t.Helper()
	err := s.cmd.Wait()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	t.Fatalf("wait: %v", err)
	return -1
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestTheChildGetsARealControllingTerminal is the assertion the whole design
// rests on. Non-negotiable rule 1 requires vim, less, htop, job control and
// tab completion to work, and every one of them needs the shell's stdin to
// be a terminal that the shell controls. Over plain pipes it would not be.
func TestTheChildGetsARealControllingTerminal(t *testing.T) {
	s := start(t, "sh", "-c", "test -t 0 && test -t 1 && echo HAS-TTY; tty > /dev/null && echo CONTROLLING")

	s.waitFor(t, "HAS-TTY", 10*time.Second)
	s.waitFor(t, "CONTROLLING", 10*time.Second)
}

// TestResizeReachesTheChild pins the window size channel. A resize that does
// not arrive is what corrupts a full screen application's display mid
// session, which is one of issue #138's own acceptance criteria.
func TestResizeReachesTheChild(t *testing.T) {
	s := start(t, "sh")

	// The FIFO is created by the pty host, so wait for it rather than
	// racing its startup.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(s.fifo); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pty host never created %s.\nstderr:\n%s", s.fifo, s.stderr.String())
		}
		time.Sleep(5 * time.Millisecond)
	}

	f, err := os.OpenFile(s.fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open the winsize fifo: %v", err)
	}
	if _, err := io.WriteString(f, "40 120\n"); err != nil {
		t.Fatalf("write the winsize: %v", err)
	}
	_ = f.Close()

	// stty reads the size off the terminal itself, so this asks the child
	// what it believes rather than asking the pty host what it sent.
	s.send(t, "stty size\n")
	s.waitFor(t, "40 120", 10*time.Second)

	s.send(t, "exit\n")
}

// TestOutputIsByteExact is the reason a host side ConPTY was rejected.
// internal/pty/osc.go parses OSC 133 markers out of this stream to build the
// journal, and the journal is what verification, scoring and par are
// computed from. Anything in the path that reorders or rewrites escape
// sequences breaks all three silently.
func TestOutputIsByteExact(t *testing.T) {
	const marker = "\x1b]133;A\x07\x1b]133;B\x07"
	s := start(t, "sh", "-c", `printf '\033]133;A\007\033]133;B\007MARKED'`)

	s.waitFor(t, "MARKED", 10*time.Second)
	if got := s.out.String(); !strings.Contains(got, marker) {
		t.Errorf("OSC 133 markers did not survive the pty host verbatim.\ngot %q\nwant it to contain %q", got, marker)
	}
}

// TestExitStatusPropagates pins the status the host reads back. A level that
// ends with a non-zero exit has to be distinguishable from one that ended
// cleanly, and the pty host sits between the shell and the runtime.
func TestExitStatusPropagates(t *testing.T) {
	for _, want := range []int{0, 3, 7} {
		s := start(t, "sh", "-c", "exit "+itoa(want))
		if got := s.wait(t); got != want {
			t.Errorf("exit status = %d, want %d", got, want)
		}
	}
}

// TestStdinCloseEndsTheSession pins the shutdown path the host relies on:
// closing the write end of stdin ends the shell, which ends the pty host,
// which is what lets `docker exec` and `wsl.exe` exit and the host see EOF.
func TestStdinCloseEndsTheSession(t *testing.T) {
	s := start(t, "sh")
	_ = s.stdin.Close()

	done := make(chan int, 1)
	go func() { done <- s.wait(t) }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the pty host outlived its stdin.\nstdout:\n%s\nstderr:\n%s", s.out.String(), s.stderr.String())
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
