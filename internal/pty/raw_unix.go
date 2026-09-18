//go:build unix

package pty

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// startResizeWatcher arms a SIGWINCH watcher for the duration of one Run
// call and returns a function that disarms it.
//
// The host terminal delivers SIGWINCH to this process on every resize while
// the learner's shell is attached. Without forwarding that into the
// sandbox via Resize, a full screen application such as vim keeps rendering
// against a stale size, and the corruption is visible immediately.
//
// The returned stop does not come back until the goroutine has actually
// returned, for the reason spelled out on the Windows twin in
// raw_windows.go: Resize reaches into the sandbox, and one issued after Run
// has torn the session down is a real fault, not a harmless late call. The
// two watchers are kept the same shape deliberately, so a reader who has
// understood one has understood both.
func startResizeWatcher(m *Mux) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			// Checked first, and on its own, because a select with two
			// ready cases picks between them at random. A burst of
			// SIGWINCH from a learner dragging the window edge could
			// otherwise keep winning the toss after stop was called.
			select {
			case <-done:
				return
			default:
			}

			select {
			case <-ch:
				cols, rows, err := m.getSize(m.fd)
				if err == nil {
					_ = m.resize(clampToUint16(rows), clampToUint16(cols))
				}
			case <-done:
				return
			}
		}
	}()

	// sync.Once so a caller that stops twice does not panic on a second
	// close. The wait is outside it on purpose: every caller must observe
	// the goroutine gone, not just the first one.
	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
		})
		<-stopped
	}
}

// brokenPipe reports a platform specific "the other end of the pipe is
// gone" that errors.Is against syscall.EPIPE does not already catch.
//
// Nothing to add here: EPIPE is the whole of it on unix, and sessionOver
// tests for that directly. The function exists so that sessionOver stays
// one cross platform expression rather than growing a build tag of its own.
func brokenPipe(error) bool { return false }
