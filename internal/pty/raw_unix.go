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
// stop returns only once the goroutine below has exited, so no m.resize
// call can still be in flight when it does. Closing done and returning was
// not enough: a signal already selected leaves the goroutine on its way
// into m.resize, which then runs against a pseudo terminal whose session
// has ended. The Windows sibling in raw_windows.go had the same gap and
// reported it as an intermittently red -race build (issue #141); this build
// had it too and simply had no test to notice.
//
// stop is safe to call more than once, matching Mux.restoreOnce's
// discipline, and every caller waits for the exit rather than only the
// first.
func startResizeWatcher(m *Mux) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	done := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
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

	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			signal.Stop(ch)
			close(done)
		})
		wg.Wait()
	}
}
