package platform

import (
	"os"
	"runtime"
	"testing"
)

func TestIsWindowsTerminal(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Run("non-Windows never treats WT_SESSION as meaningful", func(t *testing.T) {
			t.Setenv("WT_SESSION", "")
			if ok, detail := IsWindowsTerminal(); ok || detail == "" {
				t.Errorf("IsWindowsTerminal() = (%v, %q), want (false, non-empty) with WT_SESSION unset", ok, detail)
			}
			t.Setenv("WT_SESSION", "some-session-guid")
			if ok, detail := IsWindowsTerminal(); ok || detail == "" {
				t.Errorf("IsWindowsTerminal() = (%v, %q), want (false, non-empty) even with WT_SESSION set", ok, detail)
			}
		})
		return
	}

	t.Run("WT_SESSION set", func(t *testing.T) {
		t.Setenv("WT_SESSION", "some-session-guid")
		if ok, detail := IsWindowsTerminal(); !ok || detail == "" {
			t.Errorf("IsWindowsTerminal() = (%v, %q), want (true, non-empty)", ok, detail)
		}
	})
	t.Run("WT_SESSION unset", func(t *testing.T) {
		t.Setenv("WT_SESSION", "")
		if ok, detail := IsWindowsTerminal(); ok || detail == "" {
			t.Errorf("IsWindowsTerminal() = (%v, %q), want (false, non-empty)", ok, detail)
		}
	})
}

func TestSupportsVirtualTerminal_NonWindowsAlwaysSupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows answer depends on whether stdout is a real console; covered separately")
	}
	ok, detail := SupportsVirtualTerminal()
	if !ok || detail == "" {
		t.Errorf("SupportsVirtualTerminal() = (%v, %q), want (true, non-empty) outside Windows", ok, detail)
	}
}

func TestEnableVirtualTerminal_NonWindowsIsANoOp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows behaviour depends on a real console; covered separately")
	}
	restore, err := EnableVirtualTerminal()
	if err != nil {
		t.Fatalf("EnableVirtualTerminal() error = %v, want nil outside Windows", err)
	}
	if restore == nil {
		t.Fatal("EnableVirtualTerminal() restore = nil, want a callable no-op")
	}
	// Safe to call more than once, and calling it at all must not panic.
	restore()
	restore()
}

// TestEnableVirtualTerminal_Windows exercises both branches of the one
// property that cannot be pinned in advance in a CI runner: whether the
// process happens to have a real console attached to stdout.
//
// With a console, EnableVirtualTerminal must round trip the exact modes
// GetConsoleMode read beforehand, and restore must be idempotent. Without
// one, most CI runs, GetConsoleMode itself fails, and this asserts the
// acceptance criterion that failure surfaces as an error, never a panic,
// with a nil restore a caller cannot mistake for success.
func TestEnableVirtualTerminal_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}

	supported, _ := SupportsVirtualTerminal()

	restore, err := EnableVirtualTerminal()
	if !supported {
		if err == nil {
			t.Fatal("EnableVirtualTerminal() error = nil for a non-console stdout, want an error")
		}
		if restore != nil {
			t.Error("EnableVirtualTerminal() restore != nil alongside a non-nil error")
		}
		return
	}

	if err != nil {
		t.Fatalf("EnableVirtualTerminal() error = %v, want nil with a real console attached", err)
	}
	if restore == nil {
		t.Fatal("EnableVirtualTerminal() restore = nil, want a callable restore")
	}
	restore()
	restore()
}

// TestEnableVirtualTerminal_Windows_PipedStdoutErrors forces the one
// failure path GetConsoleMode has: a stdout that is not a console handle at
// all. Swapping the package var is safe here because Go tests within one
// package never run this test in parallel with another that reads
// os.Stdout, and it is restored before the test returns.
func TestEnableVirtualTerminal_Windows_PipedStdoutErrors(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer func() {
		os.Stdout = orig
		_ = r.Close()
		_ = w.Close()
	}()
	os.Stdout = w

	restore, err := EnableVirtualTerminal()
	if err == nil {
		t.Fatal("EnableVirtualTerminal() error = nil for a piped stdout, want an error")
	}
	if restore != nil {
		t.Error("EnableVirtualTerminal() restore != nil alongside a non-nil error")
	}
}
