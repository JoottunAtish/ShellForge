module github.com/JoottunAtish/ShellForge

go 1.23.0

// Day 1 Session A introduces spf13/cobra (#12) and creack/pty (#9, #10).
// creack/pty lands here directly: internal/runtime/docker imports it for
// Session.Attach. golang.org/x/term lands here directly too, as of #10:
// internal/pty/mux.go imports it for raw mode and window size, and it is
// now on the approved set in .claude/skills/go-style/SKILL.md. spf13/cobra
// is still not imported by anything and will show as indirect until #12
// lands.
//
// go get and go mod tidy have both been observed bumping this go directive
// to whatever toolchain ran them, 1.25.0 on this branch, when the naive
// latest tag of a new dependency requires it. That is not an intentional
// floor raise: golang.org/x/term v0.45.0 and its own golang.org/x/sys
// dependency both declare go 1.25.0 in their own go.mod, which forces this
// module's floor up to match anything that depends on them. Pinning to
// golang.org/x/term v0.33.0 and golang.org/x/sys v0.34.0 instead, the
// newest pair whose own go.mod stays at go 1.23.0, keeps this module's own
// floor at 1.23.0 rather than 1.25.0. If a future dependency change bumps
// this line again, check whether it is a real floor raise or the same trap
// before accepting it.
//
// The approved dependency set lives in one place: the Dependencies table in
// .claude/skills/go-style/SKILL.md. Ask before adding anything outside that
// list. (This comment previously pointed at a "Go: style and formatting"
// heading in CLAUDE.md that does not exist; CLAUDE.md only indexes the
// skills now, it does not carry the table itself.)

// charmbracelet/glamour is PINNED to v0.10.0 on purpose, and the pin is load
// bearing rather than incidental. v1.0.0 declares `go 1.24.0` in its own
// go.mod, which would drag this module's floor up with it, exactly the trap
// described above for golang.org/x/term. v0.10.0 declares `go 1.23.0`, and
// every module it requires directly (x/text, chroma/v2, lipgloss, termenv,
// x/ansi, goldmark, goldmark-emoji, bluemonday, reflow, colorprofile,
// x/cellbuf) declares 1.23.0 or lower, so the floor here stays where it is.
// It also asks for golang.org/x/term v0.31.0, which loses to the v0.33.0
// pinned below under minimal version selection, so that pin survives too.
//
// Before bumping this, check the candidate's own `go` directive. There is a
// test for the floor: TestGoDirectiveStaysAtTheSupportedFloor in
// cmd/shellforge.
require (
	github.com/charmbracelet/glamour v0.10.0
	github.com/creack/pty v1.1.24
	github.com/goccy/go-yaml v1.19.2
	github.com/spf13/cobra v1.10.2
	golang.org/x/term v0.33.0
	modernc.org/sqlite v1.38.0
)

require (
	github.com/alecthomas/chroma/v2 v2.14.0 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/charmbracelet/colorprofile v0.2.3-0.20250311203215-f60798e515dc // indirect
	github.com/charmbracelet/lipgloss v1.1.1-0.20250404203927-76690c660834 // indirect
	github.com/charmbracelet/x/ansi v0.8.0 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.13 // indirect
	github.com/charmbracelet/x/exp/slice v0.0.0-20250327172914-2fdc97757edf // indirect
	github.com/charmbracelet/x/term v0.2.1 // indirect
	github.com/dlclark/regexp2 v1.11.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/muesli/reflow v0.3.0 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/yuin/goldmark v1.7.8 // indirect
	github.com/yuin/goldmark-emoji v1.0.5 // indirect
	golang.org/x/exp v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/net v0.33.0 // indirect
	golang.org/x/sys v0.34.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	modernc.org/libc v1.65.10 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
