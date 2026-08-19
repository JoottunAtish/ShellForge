package doctor

import (
	"context"

	"github.com/JoottunAtish/ShellForge/internal/platform"
)

// terminalVTProbe checks whether the current stdout supports ANSI virtual
// terminal sequences, via internal/platform.SupportsVirtualTerminal.
type terminalVTProbe struct {
	baseProbe
}

func (p terminalVTProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}
	ok, detail := platform.SupportsVirtualTerminal()
	if ok {
		return p.result(OK, detail, "No action needed.")
	}

	// SupportsVirtualTerminal can answer false either because stdout is a
	// real console that cannot do virtual terminal processing, or because
	// stdout was redirected to a file or a pipe, in which case the question
	// does not apply at all: `shellforge doctor --json > report.json` must
	// not fail a healthy machine just because a file is not a console.
	// StdoutIsRedirected is a second, independent signal for telling those
	// apart, never a parse of this detail string.
	if redirected, _ := platform.StdoutIsRedirected(); redirected {
		return p.result(OK, "stdout is redirected to a file or pipe, so virtual terminal support does not apply", "No action needed.")
	}

	return p.result(Fail, detail,
		"Install Windows Terminal from the Microsoft Store and run Shellforge in it. If you cannot, redirect output to a file or set NO_COLOR=1.")
}

// windowsTerminalProbe checks whether this process runs under Windows
// Terminal rather than the legacy console, via
// internal/platform.IsWindowsTerminal. Windows only.
//
// TODO(v0.2): no glyph or font probe. Measuring it means drawing and
// reading the cursor back, and a wrong answer is worse than no answer.
type windowsTerminalProbe struct {
	baseProbe
}

func (p windowsTerminalProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}
	ok, detail := platform.IsWindowsTerminal()
	if ok {
		return p.result(OK, detail, "No action needed.")
	}
	return p.result(Warn, detail, "Install Windows Terminal from the Microsoft Store.")
}
