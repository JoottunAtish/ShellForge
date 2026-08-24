//go:build windows

package wsl

import "github.com/creack/pty"

// Resize cannot do anything on Windows today: wslPTY wraps whatever
// creack/pty.Start allocated around the `wsl.exe --exec` child, and that
// package has no ConPTY implementation to resize through, the same
// unconditional gap session.go's Attach already reports through
// pty.ErrUnsupported. A resize during a full screen program such as vim
// still corrupts the display, which is a known limitation, not an
// oversight, and returning nil here would silently claim otherwise:
// internal/pty.Mux's own resize call sites (Run's initial resize, both the
// unix and Windows SIGWINCH watchers) already discard whatever error a
// resize returns, treating one failed resize as never fatal to the
// learner's shell, so surfacing the real cause here costs nothing a caller
// was relying on.
//
// TODO(v0.2): a real Windows ConPTY implementation, for Attach and for this
// Resize, is issue #138, filed while implementing #71 for exactly this gap.
// #68, which this comment used to point at, landed on main in 3a6ff31
// (PR #131) as the SIGWINCH watcher's Windows console-mode groundwork and
// does not touch this gap, so do not reuse its number.
func (p *wslPTY) Resize(rows, cols uint16) error {
	return pty.ErrUnsupported
}
