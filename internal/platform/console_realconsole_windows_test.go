//go:build windows

package platform

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestEnableVirtualTerminal_RealConsoleRoundTrip pins the one acceptance
// criterion no other test in this package can reach: that against a real
// console EnableVirtualTerminal sets exactly the two virtual terminal bits
// and its restore puts both modes back to the exact values it read.
//
// TestEnableVirtualTerminal_Windows cannot prove that. A test binary's
// stdout is a pipe on every runner and every developer machine, so
// GetConsoleMode on it always fails and that test always takes its
// no-console fallback branch. The console-attached branch it documents has
// therefore never executed anywhere. This test reaches it by opening the
// console's own CONOUT$ and CONIN$ devices, which exist independently of
// where stdout happens to be redirected, and pointing os.Stdout and
// os.Stdin at them for the duration.
//
// It deliberately does not call AllocConsole to manufacture a console when
// the process has none. AllocConsole requires FreeConsole first, which
// detaches the console from the whole test process rather than from this
// test, and a suite CI runs must not do that. When no console is attached,
// as on every CI runner, this skips and the assertion below is only ever
// made on a developer's Windows machine.
func TestEnableVirtualTerminal_RealConsoleRoundTrip(t *testing.T) {
	conOut, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no console attached to this process, cannot open CONOUT$: %v", err)
	}
	defer func() { _ = conOut.Close() }()

	conIn, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no console attached to this process, cannot open CONIN$: %v", err)
	}
	defer func() { _ = conIn.Close() }()

	outHandle := windows.Handle(conOut.Fd())
	inHandle := windows.Handle(conIn.Fd())

	var beforeOut, beforeIn uint32
	if err := windows.GetConsoleMode(outHandle, &beforeOut); err != nil {
		t.Skipf("CONOUT$ opened but is not a console mode handle: %v", err)
	}
	if err := windows.GetConsoleMode(inHandle, &beforeIn); err != nil {
		t.Skipf("CONIN$ opened but is not a console mode handle: %v", err)
	}

	// This test changes the mode of a console the developer running it keeps
	// using afterwards. Put both modes back whatever happens below,
	// including a failed assertion partway through, so a broken restore
	// under test cannot leave their arrow keys emitting escape sequences.
	defer func() {
		_ = windows.SetConsoleMode(outHandle, beforeOut)
		_ = windows.SetConsoleMode(inHandle, beforeIn)
	}()

	// Not parallel, and neither is the piped-stdout test that also swaps
	// these, so the two cannot interleave.
	origOut, origIn := os.Stdout, os.Stdin
	os.Stdout, os.Stdin = conOut, conIn
	defer func() { os.Stdout, os.Stdin = origOut, origIn }()

	if ok, detail := SupportsVirtualTerminal(); !ok {
		t.Fatalf("SupportsVirtualTerminal() = (false, %q), want true for a real console", detail)
	}

	restore, err := EnableVirtualTerminal()
	if err != nil {
		t.Fatalf("EnableVirtualTerminal() error = %v, want nil for a real console", err)
	}
	if restore == nil {
		t.Fatal("EnableVirtualTerminal() restore = nil alongside a nil error")
	}

	var afterOut, afterIn uint32
	if err := windows.GetConsoleMode(outHandle, &afterOut); err != nil {
		t.Fatalf("GetConsoleMode(stdout) after enable: %v", err)
	}
	if err := windows.GetConsoleMode(inHandle, &afterIn); err != nil {
		t.Fatalf("GetConsoleMode(stdin) after enable: %v", err)
	}

	if afterOut&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
		t.Errorf("stdout mode = %#x, want ENABLE_VIRTUAL_TERMINAL_PROCESSING (%#x) set",
			afterOut, uint32(windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING))
	}
	if afterIn&windows.ENABLE_VIRTUAL_TERMINAL_INPUT == 0 {
		t.Errorf("stdin mode = %#x, want ENABLE_VIRTUAL_TERMINAL_INPUT (%#x) set",
			afterIn, uint32(windows.ENABLE_VIRTUAL_TERMINAL_INPUT))
	}

	// Exactly those bits and no others. A current Windows 11 console already
	// has ENABLE_VIRTUAL_TERMINAL_PROCESSING on by default, so on stdout
	// this is usually a no-op rather than a change, which is why the
	// comparison is against the expected union and not against a difference.
	if wantOut := beforeOut | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING; afterOut != wantOut {
		t.Errorf("stdout mode = %#x, want exactly %#x (was %#x)", afterOut, wantOut, beforeOut)
	}
	if wantIn := beforeIn | windows.ENABLE_VIRTUAL_TERMINAL_INPUT; afterIn != wantIn {
		t.Errorf("stdin mode = %#x, want exactly %#x (was %#x)", afterIn, wantIn, beforeIn)
	}

	// Restore returns both handles to the exact modes read beforehand, and
	// repeating it is harmless rather than a second, different write.
	for i := 1; i <= 3; i++ {
		restore()

		var gotOut, gotIn uint32
		if err := windows.GetConsoleMode(outHandle, &gotOut); err != nil {
			t.Fatalf("GetConsoleMode(stdout) after restore call %d: %v", i, err)
		}
		if err := windows.GetConsoleMode(inHandle, &gotIn); err != nil {
			t.Fatalf("GetConsoleMode(stdin) after restore call %d: %v", i, err)
		}
		if gotOut != beforeOut {
			t.Errorf("after restore call %d, stdout mode = %#x, want exactly %#x", i, gotOut, beforeOut)
		}
		if gotIn != beforeIn {
			t.Errorf("after restore call %d, stdin mode = %#x, want exactly %#x", i, gotIn, beforeIn)
		}
	}
}
