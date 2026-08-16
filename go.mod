module github.com/JoottunAtish/ShellForge

go 1.25.0

// Day 1 Session A introduces spf13/cobra (#12) and creack/pty (#9, #10).
// creack/pty lands here directly: internal/runtime/docker imports it for
// Session.Attach. golang.org/x/term lands here directly too, as of #10:
// internal/pty/mux.go imports it for raw mode and window size, and it is
// now on the approved set in .claude/skills/go-style/SKILL.md. spf13/cobra
// is still not imported by anything and will show as indirect until #12
// lands.
//
// The go directive above is 1.25.0, and it was 1.23.0 until a security fix
// moved it. Read this before moving it again.
//
// go get and go mod tidy have both been observed bumping the directive to
// whatever toolchain ran them when the naive latest tag of a new dependency
// requires it. That is the trap, and it is not what happened here. It did
// happen once: golang.org/x/term v0.45.0 and its own golang.org/x/sys
// dependency both declare go 1.25.0, so the pins at x/term v0.33.0 and
// x/sys v0.34.0 below were chosen as the newest pair still declaring
// go 1.23.0. Those pins are now historical rather than load bearing, since
// the floor they were protecting has moved past them; they are left alone
// because bumping them buys nothing this project needs.
//
// What moved the floor was govulncheck reporting GO-2026-5970, an infinite
// loop on invalid input in golang.org/x/text, which charmbracelet/glamour
// brings into the module. The fix is x/text v0.39.0, which declares
// go 1.25.0 in its own go.mod, and Go will not build a module whose
// dependency asks for a newer directive than the module itself declares. So
// the fix and the old floor were mutually exclusive, and the choice was
// between dropping glamour and moving the floor. The maintainer chose to
// keep glamour: it is maintained upstream and carries features this project
// has not specified yet, and a newer minimum Go is not itself a security
// cost. The directive is a MINIMUM, so raising it hands users a standard
// library with more fixes in it, not fewer. The cost is compatibility only:
// building from source now needs Go 1.25 rather than 1.23.
//
// The rule for the next person has not changed. If a dependency bump moves
// this line, find out which dependency and why. Pinning it back is the
// default answer. Raising the floor is a decision that needs a reason
// written down here, the way this one does.
//
// The approved dependency set lives in one place: the Dependencies table in
// .claude/skills/go-style/SKILL.md. Ask before adding anything outside that
// list. (This comment previously pointed at a "Go: style and formatting"
// heading in CLAUDE.md that does not exist; CLAUDE.md only indexes the
// skills now, it does not carry the table itself.)

// charmbracelet/glamour stays at v0.10.0, but the reason has changed and the
// pin is no longer load bearing. It was pinned here because v1.0.0 declares
// `go 1.24.0` and would have dragged the old 1.23.0 floor up with it. The
// floor is 1.25.0 now, so that objection is gone and v1.0.0 is available.
//
// It is not taken yet for a reason unrelated to the floor: v1.0.0 moves to
// lipgloss v2 and relocates the ansi.StyleConfig that briefingStyle in
// cmd/shellforge/render_brief.go is written against. That is a mechanical
// rename rather than a redesign, but it is a change to the briefing renderer
// and it wants its own commit and its own look at the rendered output, not a
// ride along inside a security bump.
//
// Three of glamour's transitive dependencies are held ABOVE what v0.10.0 asks
// for, because govulncheck found a reachable vulnerability in each version it
// would otherwise select: golang.org/x/text v0.39.0 (GO-2026-5970),
// github.com/yuin/goldmark v1.7.17 (GO-2026-5320, XSS in the HTML renderer
// glamour drives) and golang.org/x/net v0.38.0 (GO-2025-3595). They sit in
// the indirect require block below, which is where `go mod tidy` puts them
// since no package here imports them directly, but the versions there are
// still what minimal version selection picks. Do not lower any of the three.
// glamour v0.10.0 builds and renders correctly against all three, and the
// briefing renderer's own tests cover that.
//
// Before bumping glamour, check the candidate's own `go` directive. There is
// a test for the floor: TestGoDirectiveStaysAtTheSupportedFloor in
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
	github.com/yuin/goldmark v1.7.17 // indirect
	github.com/yuin/goldmark-emoji v1.0.5 // indirect
	golang.org/x/exp v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/sys v0.34.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	modernc.org/libc v1.65.10 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
