//go:build windows

package pty

import (
	"os"
	"sync/atomic"
	"syscall"
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

	after := len(p.resizesSnapshot())
	time.Sleep(20 * pollInterval)
	if got := len(p.resizesSnapshot()); got != after {
		t.Errorf("resize count grew from %d to %d after stop, want the watcher goroutine to have exited", after, got)
	}
}

// brokenPipe is the Windows half of sessionOver's "the shell is gone" set.
// Windows never reports EPIPE, so without this a learner typing ahead of
// their own `exit` saw the level fail.
func TestBrokenPipeRecognisesTheWindowsHangup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"ERROR_BROKEN_PIPE, the far end already gone", syscall.ERROR_BROKEN_PIPE, true},
		{"wrapped by os.PathError, what a real write returns", &os.PathError{Op: "write", Path: "|1", Err: syscall.ERROR_BROKEN_PIPE}, true},
		{"ERROR_NO_DATA, the far end closing mid read", errNoData, true},
		{"nil is not this function's business", nil, false},
		{"ERROR_ACCESS_DENIED is a genuine fault", syscall.ERROR_ACCESS_DENIED, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := brokenPipe(tc.err); got != tc.want {
				t.Errorf("brokenPipe(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
