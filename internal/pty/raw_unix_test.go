//go:build unix

package pty

import (
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The unix resize watcher had no tests at all before issue #141. That is
// why the stop race it shares with the Windows watcher only ever showed up
// on Test (windows-latest): both builds had the bug, one build had a test.
// These pin the stop contract on this side so the two cannot drift again.

// sigwinch delivers SIGWINCH to this process, which is what the host
// terminal does on a real resize. The default disposition of SIGWINCH is to
// ignore it, so a delivery nothing is watching for is harmless.
func sigwinch(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatalf("raise SIGWINCH: %v", err)
	}
}

func TestStartResizeWatcher_SigwinchTriggersResize(t *testing.T) {
	p, mux, _, _ := newTestMux(t)

	var calls int32
	mux.getSize = func(int) (cols, rows int, err error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return 100, 30, nil
		}
		return 100, 30, nil
	}

	stop := startResizeWatcher(mux)
	t.Cleanup(stop)

	waitUntil(t, 2*time.Second, func() bool {
		sigwinch(t)
		return len(p.resizesSnapshot()) > 0
	})

	if got := p.resizesSnapshot()[0]; got != [2]uint16{30, 100} {
		t.Errorf("first resize = %v, want [rows=30 cols=100]", got)
	}
}

// TestStartResizeWatcher_StopWaitsForAResizeInFlight is the deterministic
// form of the #141 assertion, and the one that would have caught the bug on
// this build.
//
// It does not sleep, poll, or race the scheduler for its result. It parks
// the watcher goroutine inside m.getSize, which is the step immediately
// before m.resize, and then proves two things in order: that stop does not
// return while that call is outstanding, and that it does return once the
// goroutine is free to finish and exit. A stop that merely closed a channel
// would return at the first check and fail on the line below it.
func TestStartResizeWatcher_StopWaitsForAResizeInFlight(t *testing.T) {
	p, mux, _, _ := newTestMux(t)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var sizeCalls int32

	mux.getSize = func(int) (cols, rows int, err error) {
		if atomic.AddInt32(&sizeCalls, 1) == 1 {
			entered <- struct{}{}
			<-release
		}
		return 100, 30, nil
	}

	stop := startResizeWatcher(mux)

	// Drive SIGWINCH until the watcher is parked inside getSize.
	deadline := time.After(2 * time.Second)
	for parked := false; !parked; {
		sigwinch(t)
		select {
		case <-entered:
			parked = true
		case <-deadline:
			t.Fatal("the watcher never entered getSize within 2s")
		case <-time.After(time.Millisecond):
		}
	}

	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("stop returned while a resize was still in flight; the watcher goroutine can still call m.resize after stop returns")
	case <-time.After(50 * time.Millisecond):
		// Correct: stop is waiting, which is the whole contract.
	}

	close(release)

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return within 2s after the resize completed; the wait never ends")
	}

	// Whatever was in flight landed before stop returned, so the count is
	// final from here on. Nothing after this point may add to it.
	after := len(p.resizesSnapshot())
	for i := 0; i < 50; i++ {
		sigwinch(t)
	}
	time.Sleep(20 * time.Millisecond)
	if got := len(p.resizesSnapshot()); got != after {
		t.Errorf("resize count grew from %d to %d after stop returned, want the watcher goroutine to have exited", after, got)
	}
}

// TestStartResizeWatcher_StopIsIdempotent pins the other half of the same
// contract. Mux.restoreOnce already establishes that a teardown in this
// package may be called twice; stop closed a channel unconditionally, which
// panics the second time.
func TestStartResizeWatcher_StopIsIdempotent(t *testing.T) {
	_, mux, _, _ := newTestMux(t)

	stop := startResizeWatcher(mux)
	stop()
	stop()
	stop()
}
