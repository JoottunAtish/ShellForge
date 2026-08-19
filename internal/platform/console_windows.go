//go:build windows

package platform

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

// EnableVirtualTerminal turns on ANSI processing for stdout and VT input for
// stdin, and returns a function restoring the previous console modes.
//
// The restore function is safe to call more than once and must be called on
// every exit path, including a panic. Leaving VT input enabled on a console
// the user keeps working in afterwards turns their arrow keys into raw
// escape sequences, which looks exactly like the game broke their terminal.
func EnableVirtualTerminal() (restore func(), err error) {
	outHandle := windows.Handle(os.Stdout.Fd())
	inHandle := windows.Handle(os.Stdin.Fd())

	var outMode, inMode uint32
	if err := windows.GetConsoleMode(outHandle, &outMode); err != nil {
		return nil, fmt.Errorf("platform: read the console output mode: %w", err)
	}
	if err := windows.GetConsoleMode(inHandle, &inMode); err != nil {
		return nil, fmt.Errorf("platform: read the console input mode: %w", err)
	}

	if err := windows.SetConsoleMode(outHandle, outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return nil, fmt.Errorf("platform: enable virtual terminal processing on stdout: %w", err)
	}
	if err := windows.SetConsoleMode(inHandle, inMode|windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil {
		// Stdout's mode already changed above. Put it back before
		// reporting failure, so a caller that gives up on this error is
		// not left with stdout in a different mode than it found it.
		_ = windows.SetConsoleMode(outHandle, outMode)
		return nil, fmt.Errorf("platform: enable virtual terminal input on stdin: %w", err)
	}

	var once sync.Once
	restore = func() {
		once.Do(func() {
			_ = windows.SetConsoleMode(outHandle, outMode)
			_ = windows.SetConsoleMode(inHandle, inMode)
		})
	}
	return restore, nil
}

// SupportsVirtualTerminal reports whether ANSI output can be enabled at
// all, without changing anything. It is what the doctor probe calls.
//
// The only thing that can prevent enabling virtual terminal processing on a
// current Windows 10 or later build is stdout not being a console at all,
// for example when it is redirected to a pipe or a file, so that is the
// question this asks: whether GetConsoleMode succeeds on it.
func SupportsVirtualTerminal() (ok bool, detail string) {
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(os.Stdout.Fd()), &mode); err != nil {
		return false, fmt.Sprintf("stdout is not a console: %v", err)
	}
	return true, "stdout is a console that supports virtual terminal processing"
}

// StdoutIsRedirected reports whether stdout is a file or a pipe rather than
// a console, using GetFileType. That answers a different question than
// SupportsVirtualTerminal's GetConsoleMode call: GetFileType asks what kind
// of handle stdout is, rather than whether its console mode can be
// changed, so terminal_vt_support can tell "the question does not apply,
// stdout was redirected" apart from "stdout is a real console that cannot
// do virtual terminal processing" as two genuinely different signals,
// never by parsing SupportsVirtualTerminal's own detail string.
func StdoutIsRedirected() (ok bool, detail string) {
	fileType, err := windows.GetFileType(windows.Handle(os.Stdout.Fd()))
	if err != nil {
		return false, fmt.Sprintf("could not determine the stdout handle type: %v", err)
	}
	if fileType == windows.FILE_TYPE_CHAR {
		return false, "stdout is a character device (a console)"
	}
	return true, "stdout is redirected to a file or pipe"
}
