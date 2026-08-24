//go:build !windows

package platform

import (
	"fmt"
	"os"
)

// EnableVirtualTerminal is a no-op on every platform other than Windows:
// their terminals already understand ANSI escape sequences and have no
// console mode to flip. The returned restore is also a no-op, safe to call
// any number of times, so a caller needs no build tag to use this
// unconditionally.
func EnableVirtualTerminal() (restore func(), err error) {
	return func() {}, nil
}

// SupportsVirtualTerminal always reports true outside Windows: ANSI output
// needs no console mode change on these platforms, so there is nothing that
// could prevent it.
func SupportsVirtualTerminal() (ok bool, detail string) {
	return true, "not applicable outside Windows; ANSI output needs no console mode change here"
}

// StdoutIsRedirected reports whether stdout is a file or a pipe rather than
// a terminal, using the standard os.ModeCharDevice check on its file mode.
//
// Outside Windows SupportsVirtualTerminal always answers true, so this is
// the only place a caller can tell "stdout is not a terminal at all" apart
// from "stdout is a terminal". terminal_vt_support uses it for exactly
// that: a redirected stdout makes the virtual-terminal question not apply,
// rather than a pass or a fail on its own terms.
func StdoutIsRedirected() (ok bool, detail string) {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false, fmt.Sprintf("could not stat stdout: %v", err)
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return false, "stdout is a terminal"
	}
	return true, "stdout is redirected to a file or pipe"
}
