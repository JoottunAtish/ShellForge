package platform

import (
	"os"
	"runtime"
)

// IsWindowsTerminal reports whether this process runs under Windows
// Terminal, by the presence of the WT_SESSION environment variable Windows
// Terminal sets on every process it starts.
//
// The distinction matters because the legacy Windows console, conhost, can
// garble a full screen application's display when the window is resized
// mid session; see docs/05-troubleshooting.md#terminal-not-windows-terminal.
//
// On any platform other than Windows the question does not apply: this
// reports false with a detail saying so, and no caller may treat that as a
// problem. Nothing here needs a build tag, so both this and
// SupportsVirtualTerminal compile and answer the same way on every
// platform, without a caller ever knowing which OS it is running on.
func IsWindowsTerminal() (ok bool, detail string) {
	if runtime.GOOS != "windows" {
		return false, "not applicable outside Windows"
	}
	if os.Getenv("WT_SESSION") != "" {
		return true, "running under Windows Terminal"
	}
	return false, "not running under Windows Terminal; full screen programs can garble on resize in the legacy console"
}
