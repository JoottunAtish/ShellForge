//go:build unix

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// run allocates the pseudo terminal, starts command in it, and copies bytes
// between it and this process's own stdin and stdout until the child exits.
// It returns the child's exit status.
func run(fifoPath string, command []string) (int, error) {
	// #nosec G204 -- command comes from the runtime's own argv vector, built
	// in internal/runtime/{docker,wsl}.Session.Attach from validated fields
	// and a compile-time default. It is never a shell string and never
	// reaches a shell.
	cmd := exec.Command(command[0], command[1:]...)

	// pty.Start sets Setsid and Setctty, which is what makes the pseudo
	// terminal the child's CONTROLLING terminal. That is not decoration:
	// it is what job control, Ctrl-C, vim, less, htop and tab completion
	// all need, and non-negotiable rule 1 requires every one of them to
	// work.
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return 1, fmt.Errorf("allocate a pseudo terminal: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	if fifoPath != "" {
		stopResize, err := startResizeWatcher(ptmx, fifoPath)
		if err != nil {
			// A shell with a stuck window size is worth having. One
			// that refuses to open is not, so this is reported and
			// carried rather than fatal.
			fmt.Fprintf(os.Stderr, "sf-ptyhost: window size channel unavailable: %v\n", err)
		} else {
			defer stopResize()
		}
	}

	// Copy the master to stdout in the foreground and stdin to the master
	// in the background, so that the function returns when the pseudo
	// terminal reaches EOF rather than when stdin does. The learner's
	// shell ending is what ends this process; the host closing stdin is
	// what ends the shell.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(os.Stdout, ptmx)
	}()
	go func() {
		_, _ = io.Copy(ptmx, os.Stdin)
		// Stdin reaching EOF means the host has gone: it closed the
		// pipe, or `docker exec` and `wsl.exe` exited under it. Closing
		// the master end hangs the terminal up, which sends SIGHUP to
		// the foreground process group and ends the learner's shell.
		//
		// Without this the shell waits on a terminal nobody is typing
		// at any more and this process never exits, which inside a
		// container means a stray bash for the life of the sandbox. A
		// pty master does not pass its own EOF through to the slave,
		// so nothing else here would have ended it.
		//
		// Losing whatever the shell had not yet written is correct
		// rather than unfortunate: this is a hangup, and a hangup is
		// what a real terminal does. The learner's own `exit` takes the
		// other path, where the shell ends first and the drain above
		// completes normally, so the final OSC 133;D marker still
		// reaches the host.
		_ = ptmx.Close()
	}()

	// Wait for the master to drain BEFORE reaping, so the last thing the
	// shell wrote reaches the host. Reaping first and exiting would race
	// the final bytes, and the bytes at the end of a session are the OSC
	// 133;D marker carrying the exit status of the learner's last command.
	<-drained

	err = cmd.Wait()
	return exitCode(err), nil
}

// exitCode turns cmd.Wait's error into the status to exit with, so that a
// level's `exit 3` reaches the host as 3 rather than as a generic failure.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
		// Killed by a signal. Report it the way a shell does.
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
	}
	return 1
}

// startResizeWatcher creates the window size FIFO and applies every
// "rows cols" line the host writes to it.
//
// A FIFO rather than a framed message inside stdin, because stdin carries
// the learner's keystrokes verbatim and must stay byte exact: any in-band
// framing would need escaping, and an escape the learner can type is an
// escape that will eventually be typed. The host writes to this FIFO with
// the same argv-only mechanism cmd_run.go's serveControlRequests already
// uses for the check channel, one `tee` per resize.
//
// It is opened O_RDWR rather than O_RDONLY on purpose. Opening a FIFO read
// only blocks until a writer appears, which would stall the shell until the
// first resize; and once a writer closes, a read only FIFO reports EOF,
// which would end the watcher after the first resize. Holding a writer open
// here does away with both.
func startResizeWatcher(ptmx *os.File, fifoPath string) (stop func(), err error) {
	if err := unix.Mkfifo(fifoPath, 0o600); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create %s: %w", fifoPath, err)
	}

	fifo, err := os.OpenFile(fifoPath, os.O_RDWR, 0o600) // #nosec G304 -- a fixed path under the level's own state directory, built by the runtime
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", fifoPath, err)
	}

	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(fifo)
		for scanner.Scan() {
			select {
			case <-done:
				return
			default:
			}
			rows, cols, ok := parseWinsize(scanner.Text())
			if !ok {
				continue
			}
			// A failed resize is never fatal to the learner's shell,
			// matching internal/pty.Mux, which discards the error at
			// every one of its own resize call sites.
			_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
		}
	}()

	return func() {
		close(done)
		_ = fifo.Close()
		_ = os.Remove(fifoPath)
	}, nil
}

// parseWinsize reads one "rows cols" line. It refuses anything else rather
// than guessing: a zero or absurd size is worse than a stale one, because a
// full screen application renders against it immediately.
func parseWinsize(line string) (rows, cols uint16, ok bool) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return 0, 0, false
	}
	r, err := strconv.ParseUint(fields[0], 10, 16)
	if err != nil || r == 0 {
		return 0, 0, false
	}
	c, err := strconv.ParseUint(fields[1], 10, 16)
	if err != nil || c == 0 {
		return 0, 0, false
	}
	return uint16(r), uint16(c), true
}
