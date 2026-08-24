package main

import (
	"errors"
	"testing"
)

// TestWithConsoleRestoresTheConsoleOnAPanic pins the reason withConsole's
// restore has to run in a defer rather than after a plain call: a terminal
// left in a modified console mode after a crash is unrecoverable from
// outside the process, which is why this is a test and not a comment.
//
// Verified to fail without the fix: with the defer temporarily removed from
// withConsole, this test failed with "restore count = 0, want 1" because the
// panic unwound past the restore call before it ran.
func TestWithConsoleRestoresTheConsoleOnAPanic(t *testing.T) {
	restoreCount := 0
	enable := func() (func(), error) {
		return func() { restoreCount++ }, nil
	}

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("the panic from body did not propagate out of withConsole")
			}
		}()
		_ = withConsole(enable, func() error {
			panic("body panicked")
		})
	}()

	if restoreCount != 1 {
		t.Errorf("restore count = %d, want 1", restoreCount)
	}
}

// TestWithConsoleRestoresTheConsoleOnAnError asserts the body's error passes
// through withConsole unchanged, so a verb's *ux.Error is never stripped or
// wrapped on its way out.
func TestWithConsoleRestoresTheConsoleOnAnError(t *testing.T) {
	restoreCount := 0
	enable := func() (func(), error) {
		return func() { restoreCount++ }, nil
	}

	wantErr := errors.New("the body failed")
	err := withConsole(enable, func() error {
		return wantErr
	})

	if !errors.Is(err, wantErr) {
		t.Errorf("withConsole returned %v, want the body's own error %v unchanged", err, wantErr)
	}
	if restoreCount != 1 {
		t.Errorf("restore count = %d, want 1", restoreCount)
	}
}

// TestWithConsoleToleratesANilRestoreWithNoError guards the nil-restore
// branch directly: an enable that returns a nil restore func alongside a
// nil error must not be called through the deferred restore(), which would
// otherwise panic. The original guard was keyed on err alone (`if err !=
// nil { restore = func(){} }`), so a nil restore paired with a nil error
// slipped through uncaught; this exercises that exact combination.
func TestWithConsoleToleratesANilRestoreWithNoError(t *testing.T) {
	enable := func() (func(), error) {
		return nil, nil
	}

	bodyRan := false
	err := withConsole(enable, func() error {
		bodyRan = true
		return nil
	})

	if !bodyRan {
		t.Fatal("the body never ran")
	}
	if err != nil {
		t.Errorf("withConsole returned %v, want nil", err)
	}
}

// TestWithConsoleRunsTheBodyEvenWhenEnablingFails asserts a legacy console
// that cannot take ENABLE_VIRTUAL_TERMINAL_PROCESSING still gets to run the
// verb, with plain output, rather than refusing to start.
func TestWithConsoleRunsTheBodyEvenWhenEnablingFails(t *testing.T) {
	enable := func() (func(), error) {
		return nil, errors.New("this console has no virtual terminal support")
	}

	bodyRan := false
	wantErr := errors.New("body's own result")
	err := withConsole(enable, func() error {
		bodyRan = true
		return wantErr
	})

	if !bodyRan {
		t.Fatal("the body never ran after enable failed")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("withConsole returned %v, want the body's own error %v", err, wantErr)
	}
}
