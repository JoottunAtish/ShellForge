//go:build windows

package pty

import (
	"sync/atomic"
	"testing"
	"time"
)

// pollInterval is short enough that these tests do not sit around waiting
// for the production 250ms cadence, but long enough that the assertions
// below are not themselves racing the watcher goroutine's first tick.
const pollInterval = time.Millisecond

func TestStartResizeWatcher_ChangedSizeTriggersResize(t *testing.T) {
	p, mux, _, _ := newTestMux(t)
	mux.resizePollInterval = pollInterval

	var calls int32
	mux.getSize = func(int) (cols, rows int, err error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return 80, 24, nil
		}
		return 100, 30, nil
	}

	stop := startResizeWatcher(mux)
	t.Cleanup(stop)

	waitUntil(t, 2*time.Second, func() bool {
		return len(p.resizesSnapshot()) > 0
	})

	resizes := p.resizesSnapshot()
	if resizes[0] != [2]uint16{30, 100} {
		t.Errorf("first resize = %v, want [rows=30 cols=100]", resizes[0])
	}
}

func TestStartResizeWatcher_UnchangedSizeDoesNotResize(t *testing.T) {
	p, mux, _, _ := newTestMux(t)
	mux.resizePollInterval = pollInterval
	mux.getSize = func(int) (cols, rows int, err error) { return 80, 24, nil }

	stop := startResizeWatcher(mux)
	time.Sleep(20 * pollInterval)
	stop()

	if resizes := p.resizesSnapshot(); len(resizes) != 0 {
		t.Errorf("resizes = %v, want none reported for an unchanged size", resizes)
	}
}

// TestStartResizeWatcher_StopIsIdempotent pins the half of the stop
// contract that has nothing to do with timing. Mux.restoreOnce already
// establishes that a teardown in this package may be called twice; stop
// closed a channel unconditionally, which panics the second time.
//
// Its unix counterpart is in raw_unix_test.go, so the two builds cannot
// drift on this again (issue #141).
func TestStartResizeWatcher_StopIsIdempotent(t *testing.T) {
	_, mux, _, _ := newTestMux(t)
	mux.resizePollInterval = pollInterval

	stop := startResizeWatcher(mux)
	stop()
	stop()
	stop()
}

func TestStartResizeWatcher_StopEndsTheGoroutine(t *testing.T) {
	p, mux, _, _ := newTestMux(t)
	mux.resizePollInterval = pollInterval

	var calls int32
	mux.getSize = func(int) (cols, rows int, err error) {
		// Always different from the previous call, so every tick fires a
		// resize until stop is called, which is what makes the assertion
		// below prove the goroutine actually exited rather than merely
		// coinciding with an unchanged size.
		n := atomic.AddInt32(&calls, 1)
		return 80 + int(n), 24, nil
	}

	stop := startResizeWatcher(mux)
	waitUntil(t, 2*time.Second, func() bool {
		return len(p.resizesSnapshot()) > 0
	})
	stop()

	// stop waits for the watcher goroutine to exit, so this count is
	// already final and the sleep below is not what makes the assertion
	// hold. It is kept as an amplifier: against a stop that only closed a
	// channel and returned, it gives the goroutine the ticks it needs to
	// prove the point loudly rather than one time in ten.
	after := len(p.resizesSnapshot())
	time.Sleep(20 * pollInterval)
	if got := len(p.resizesSnapshot()); got != after {
		t.Errorf("resize count grew from %d to %d after stop, want the watcher goroutine to have exited", after, got)
	}
}
