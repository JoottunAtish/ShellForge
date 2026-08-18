//go:build windows

package pty

import "time"

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
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
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
	return func() {
		close(done)
	}
}
