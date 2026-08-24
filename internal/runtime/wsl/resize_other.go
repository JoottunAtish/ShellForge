//go:build !windows

package wsl

import "github.com/creack/pty"

// Resize sets the terminal window size through creack/pty. This build tag
// exists only so the package still compiles on the non-Windows hosts this
// project's own CI and this run both use; wslPTY itself is never
// constructed there because Attach refuses a fake runner and wsl.exe does
// not exist to attach to.
func (p *wslPTY) Resize(rows, cols uint16) error {
	return pty.Setsize(p.file, &pty.Winsize{Rows: rows, Cols: cols})
}
