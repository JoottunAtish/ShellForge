//go:build !windows

package platform

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
