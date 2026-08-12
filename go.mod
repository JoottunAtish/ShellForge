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
	golang.org/x/term v0.33.0
)

require golang.org/x/sys v0.34.0 // indirect
