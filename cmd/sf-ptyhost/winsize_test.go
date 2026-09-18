//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// makeWinsizeFIFO has to end with a real FIFO at the path whatever it found
// there, because the host writes every resize to it for the whole session
// and a regular file is read once and then silently ignored.
//
// The regular file case is not hypothetical. internal/pty's Mux issues its
// first resize the instant Attach returns, the host delivers it with
// `tee`, and tee opens with O_CREAT, so any run where this process starts
// second finds an ordinary file already sitting there.
func TestMakeWinsizeFIFOAlwaysEndsWithAFIFO(t *testing.T) {
	isFIFO := func(t *testing.T, path string) bool {
		t.Helper()
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("lstat %s: %v", path, err)
		}
		return info.Mode()&os.ModeNamedPipe != 0
	}

	t.Run("nothing there yet", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "winsize")

		early, err := makeWinsizeFIFO(path)
		if err != nil {
			t.Fatalf("makeWinsizeFIFO: %v", err)
		}
		if early != "" {
			t.Errorf("recovered %q from a path that was empty, want nothing", early)
		}
		if !isFIFO(t, path) {
			t.Error("did not create a FIFO")
		}
	})

	t.Run("a regular file the host's first resize won the race with", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "winsize")
		if err := os.WriteFile(path, []byte("24 80\n"), 0o600); err != nil {
			t.Fatalf("seed the racing write: %v", err)
		}

		early, err := makeWinsizeFIFO(path)
		if err != nil {
			t.Fatalf("makeWinsizeFIFO: %v", err)
		}
		if !isFIFO(t, path) {
			t.Fatal("left a regular file in place, so every later resize goes to a file nobody reads")
		}

		// Returned rather than discarded: it is the starting window size,
		// and throwing it away would cost the shell its shape until the
		// learner happened to resize the window by hand.
		rows, cols, ok := parseWinsize(early)
		if !ok {
			t.Fatalf("recovered %q, which does not parse as a window size", early)
		}
		if rows != 24 || cols != 80 {
			t.Errorf("recovered %d by %d, want 24 by 80", rows, cols)
		}
	})

	t.Run("a FIFO a previous session left behind", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "winsize")
		if err := unix.Mkfifo(path, 0o600); err != nil {
			t.Fatalf("seed the stale FIFO: %v", err)
		}

		early, err := makeWinsizeFIFO(path)
		if err != nil {
			t.Fatalf("makeWinsizeFIFO: %v", err)
		}
		if early != "" {
			t.Errorf("recovered %q from a FIFO, want nothing: reading one here would block", early)
		}
		if !isFIFO(t, path) {
			t.Error("replaced a perfectly good FIFO with something else")
		}
	})

	t.Run("a directory, which is neither and must not be read", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "winsize")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("seed the directory: %v", err)
		}

		// os.Remove clears an empty directory, so this ends with a FIFO
		// rather than an error. Either is defensible; what matters is
		// that it does not try to read the directory and does not leave
		// a non-FIFO behind.
		early, err := makeWinsizeFIFO(path)
		if err != nil {
			t.Fatalf("makeWinsizeFIFO: %v", err)
		}
		if early != "" {
			t.Errorf("recovered %q from a directory, want nothing", early)
		}
		if !isFIFO(t, path) {
			t.Error("did not end with a FIFO")
		}
	})
}
