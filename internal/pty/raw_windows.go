//go:build windows

package pty

// startResizeWatcher is a no-op on Windows for Day 1. Windows has no
// SIGWINCH equivalent; console resize detection needs ConPTY specific
// handling.
//
// TODO(v0.2): wire real resize detection here. See the Day 3 Windows
// ticket.
func startResizeWatcher(m *Mux) (stop func()) {
	return func() {}
}
