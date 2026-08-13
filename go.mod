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

require (
	github.com/creack/pty v1.1.24
	github.com/goccy/go-yaml v1.19.2
	github.com/spf13/cobra v1.10.2
	golang.org/x/term v0.33.0
	modernc.org/sqlite v1.38.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/exp v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/sys v0.34.0 // indirect
	modernc.org/libc v1.65.10 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
