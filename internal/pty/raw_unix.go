//go:build unix

package pty

import (
	"os"
	"os/signal"
	"syscall"
)

// startResizeWatcher arms a SIGWINCH watcher for the duration of one Run
// call and returns a function that disarms it.
//
// The host terminal delivers SIGWINCH to this process on every resize while
// the learner's shell is attached. Without forwarding that into the
// sandbox via Resize, a full screen application such as vim keeps rendering
// against a stale size, and the corruption is visible immediately.
func startResizeWatcher(m *Mux) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
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
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
