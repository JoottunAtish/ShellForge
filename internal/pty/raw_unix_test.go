//go:build unix

package pty

import (
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestStartResizeWatcher_StopWaitsForTheGoroutine pins the fix for issue
// #141 on the side of the build that CI can actually run it on.
//
// The Windows twin of this watcher is what #141 was filed against, and no
// runner this project can reach executes a Windows test against a real
// console. The defect is not Windows specific though: both watchers used to
// close a channel and return, so stop could come back while the goroutine
// was part way through a tick, and a resize could land afterwards. This test
// proves the property that fixes it, on Linux, where it runs on every push.
//
// It asserts the strong form deliberately. Counting resizes after stop, the
// way the Windows test does, can pass by luck; blocking the goroutine inside
// a tick and then watching whether stop returns anyway cannot.
func TestStartResizeWatcher_StopWaitsForTheGoroutine(t *testing.T) {
	p, mux, _, _ := newTestMux(t)

	entered := make(chan struct{})
	release := make(chan struct{})
	// Unlike the Windows twin, this watcher takes no priming size read
	// before it starts, so the first getSize call is already the one
	// inside a tick. Blocking it is what parks the goroutine mid-tick.
	var blockOnce sync.Once
	mux.getSize = func(int) (cols, rows int, err error) {
		blockOnce.Do(func() {
			close(entered)
			<-release
		})
		return 100, 30, nil
	}

	stop := startResizeWatcher(mux)

	// Wake the watcher so it enters a tick and parks inside getSize.
	// SIGWINCH is ignored by default, so raising it here disturbs nothing
	// else in the test binary.
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatalf("raise SIGWINCH: %v", err)
	}
	<-entered

	returned := make(chan struct{})
	go func() {
		stop()
		close(returned)
	}()

	select {
	case <-returned:
		t.Fatal("stop returned while the watcher was still inside a tick: a resize can still reach a session Run has torn down")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return after the watcher finished its tick")
	}

	after := len(p.resizesSnapshot())
	time.Sleep(50 * time.Millisecond)
	if got := len(p.resizesSnapshot()); got != after {
		t.Errorf("resize count grew from %d to %d after stop returned, want the watcher goroutine to have exited", after, got)
	}
}

// TestStartResizeWatcher_StopIsSafeTwice covers the sync.Once: a second stop
// must neither panic on a repeated close nor block forever waiting on a
// goroutine that has already gone.
func TestStartResizeWatcher_StopIsSafeTwice(t *testing.T) {
	_, mux, _, _ := newTestMux(t)

	stop := startResizeWatcher(mux)
	stop()

	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a second stop did not return")
	}
}
