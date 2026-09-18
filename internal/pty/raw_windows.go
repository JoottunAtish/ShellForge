//go:build windows

package pty

import (
	"errors"
	"sync"
	"syscall"
	"time"
)

// defaultResizePollInterval is how often startResizeWatcher checks the
// console size when Mux has not overridden resizePollInterval for a test.
//
// 250ms is frequent enough that a learner dragging the window edge does not
// notice the lag before a full screen application catches up, and infrequent
// enough that polling GetConsoleScreenBufferInfo, which is what
// term.GetSize calls into on Windows, costs nothing worth measuring.
const defaultResizePollInterval = 250 * time.Millisecond

// startResizeWatcher polls the sandbox's reported terminal size for the
// duration of one Run call and forwards a change into the sandbox via
// resize.
//
// Windows has no SIGWINCH equivalent, unlike the unix watcher in
// raw_unix.go that this mirrors in shape. There are two ways to learn about
// a resize on Windows: poll GetConsoleScreenBufferInfo, or read
// WINDOW_BUFFER_SIZE_EVENT records off the console input handle. The second
// is not taken: Mux.Run already forwards the host's stdin to the sandbox
// verbatim, and consuming console input events in a second place here would
// swallow the learner's keystrokes, which presents as "the game randomly
// drops characters".
//
// Polling goes through m.getSize, the same injectable field the unix
// watcher and Run's own initial resize both use, rather than calling the
// Windows console API directly from here. That keeps this watcher testable
// without a real console, and keeps GetConsoleScreenBufferInfo out of this
// file entirely: on Windows, term.GetSize, m.getSize's production value,
// already calls it.
//
// The returned stop does not come back until the goroutine has actually
// returned. Closing a channel and returning was not enough, and issue #141
// is what that cost: the goroutine can be part way through a tick when stop
// is called, so a resize could still land after stop had returned. That made
// TestStartResizeWatcher_StopEndsTheGoroutine intermittently red on
// windows-latest under -race, and it is a production bug as well as a test
// one, because Resize reaches into the sandbox and must not be issued
// against a session Run has already torn down.
func startResizeWatcher(m *Mux) (stop func()) {
	interval := m.resizePollInterval
	if interval <= 0 {
		interval = defaultResizePollInterval
	}

	// Read the starting size before the goroutine below starts, so the
	// first tick only fires a resize on an actual change rather than
	// racing the initial resize Run already performed before calling this.
	lastCols, lastRows, _ := m.getSize(m.fd)

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			// Checked first, and on its own, because a select with two
			// ready cases picks between them at random. Without this,
			// a closed done could lose the toss repeatedly and the
			// goroutine would keep resizing after stop was called.
			select {
			case <-done:
				return
			default:
			}

			select {
			case <-ticker.C:
				cols, rows, err := m.getSize(m.fd)
				if err != nil {
					continue
				}
				if cols == lastCols && rows == lastRows {
					continue
				}
				lastCols, lastRows = cols, rows
				_ = m.resize(clampToUint16(rows), clampToUint16(cols))
			case <-done:
				return
			}
		}
	}()

	// sync.Once so a caller that stops twice does not panic on a second
	// close, matching platform.EnableVirtualTerminal's restore. The wait
	// is outside it on purpose: every caller must observe the goroutine
	// gone, not just the first one.
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		<-stopped
	}
}

// brokenPipe reports a platform specific "the other end of the pipe is
// gone" that errors.Is against syscall.EPIPE does not already catch.
//
// Windows does not report EPIPE. A pipe whose far end has gone fails with
// ERROR_BROKEN_PIPE, which does not map onto a POSIX errno on the way out
// of the syscall package, so testing for EPIPE alone misses every Windows
// session. It means the session is over, which is what sessionOver asks.
//
// errNoData is the other half of the same condition and syscall does not
// export it, so it is spelled out. Windows raises it on a pipe the far end
// closed while a read was in flight, as against ERROR_BROKEN_PIPE for one
// that was already gone.
const errNoData = syscall.Errno(232)

func brokenPipe(err error) bool {
	return errors.Is(err, syscall.ERROR_BROKEN_PIPE) || errors.Is(err, errNoData)
}
